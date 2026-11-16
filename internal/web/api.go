package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	_ "time/tzdata"

	"github.com/Muriel-Gasparini/antigravity-account-switcher/internal/config"
	"github.com/Muriel-Gasparini/antigravity-account-switcher/internal/domain"
	"github.com/Muriel-Gasparini/antigravity-account-switcher/internal/oauth"
	"github.com/Muriel-Gasparini/antigravity-account-switcher/internal/quota"
)

// FallbackConfigSetter defines an interface for dynamically updating model fallback settings at runtime.

type QuotaPoller interface {
	PollOnce(ctx context.Context) error
}

// APIHandler implements the REST endpoints and SSE real-time streaming for the switcher.
type APIHandler struct {
	accountRepo    domain.AccountRepository
	quotaRepo      domain.QuotaRepository
	metricsService domain.MetricsService
	broadcaster    domain.EventBroadcaster
	eventRepo      domain.EventRepository
	oauthEngine    oauth.OAuthEngine
	poller         QuotaPoller
	startTime      time.Time
	version        string
	cfgMu          sync.RWMutex
	appConfig      *config.Config
}

// SetConfig sets the configuration pointer for APIHandler.
func (a *APIHandler) SetConfig(c *config.Config) {
	a.cfgMu.Lock()
	defer a.cfgMu.Unlock()
	a.appConfig = c
}

// SetFallbackConfigSetter sets the dynamic fallback setter for live proxy updates.

// SetPoller assigns the quota poller.
func (a *APIHandler) SetPoller(p QuotaPoller) {
	a.poller = p
}

// HandleQuotaRefresh serves POST /api/quota/refresh.
func (a *APIHandler) HandleQuotaRefresh(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if a.poller != nil {
		_ = a.poller.PollOnce(r.Context())
	}

	a.listAccounts(w, r)
}

// NewAPIHandler constructs an APIHandler with provided dependencies.
func NewAPIHandler(
	accountRepo domain.AccountRepository,
	quotaRepo domain.QuotaRepository,
	metricsService domain.MetricsService,
	broadcaster domain.EventBroadcaster,
	eventRepo domain.EventRepository,
	oauthEngine oauth.OAuthEngine,
	version string,
) *APIHandler {
	if version == "" {
		version = "1.1.0"
	}
	return &APIHandler{
		accountRepo:    accountRepo,
		quotaRepo:      quotaRepo,
		metricsService: metricsService,
		broadcaster:    broadcaster,
		eventRepo:      eventRepo,
		oauthEngine:    oauthEngine,
		startTime:      time.Now(),
		version:        version,
	}
}

// StatusResponse represents server health and active account info.
type StatusResponse struct {
	ActiveFamilies map[domain.QuotaFamily]*domain.Account `json:"active_families"`
	Status         string                                 `json:"status"`
	Version        string                                 `json:"version"`
	UptimeSeconds  int64                                  `json:"uptime_seconds"`
	ActiveAccount  *domain.Account                        `json:"active_account,omitempty"`
	TotalAccounts  int                                    `json:"total_accounts"`
	Timestamp      time.Time                              `json:"timestamp"`
}

// HandleStatus serves GET /api/status.
func (a *APIHandler) HandleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx := r.Context()
	var active *domain.Account
	total := 0

	if a.accountRepo != nil {
		active, _ = a.accountRepo.GetActive(ctx)
		if list, err := a.accountRepo.List(ctx); err == nil {
			total = len(list)
		}
	}

	familyActives := make(map[domain.QuotaFamily]*domain.Account)
	if repo, ok := a.quotaRepo.(domain.FamilyActiveRepository); ok && a.accountRepo != nil {
		for _, family := range []domain.QuotaFamily{domain.QuotaFamilyClaudeGPT, domain.QuotaFamilyGemini, domain.QuotaFamilyArgon} {
			id, err := repo.GetFamilyActive(ctx, family)
			if err != nil || id == "" {
				continue
			}
			acc, err := a.accountRepo.GetByID(ctx, id)
			if err == nil && acc != nil && !a.quotaUnavailable(ctx, id) && acc.Status != domain.AccountStatusDisabled && acc.Status != domain.AccountStatusError {
				familyActives[family] = acc
			}
		}
	}
	resp := StatusResponse{
		ActiveFamilies: familyActives,
		Status:         "ok",
		Version:        a.version,
		UptimeSeconds:  int64(time.Since(a.startTime).Seconds()),
		ActiveAccount:  active,
		TotalAccounts:  total,
		Timestamp:      time.Now().UTC(),
	}

	writeJSON(w, http.StatusOK, resp)
}

// AccountWithBuckets encapsulates an Account and its associated quota buckets.
type AccountWithBuckets struct {
	*domain.Account
	Buckets          []*domain.QuotaBucket                  `json:"buckets"`
	Families         map[domain.QuotaFamily]FamilyQuotaView `json:"families"`
	Verification     *domain.VerificationRequirement        `json:"verification,omitempty"`
	QuotaUnavailable bool                                   `json:"quota_unavailable"`
}

// HandleAccounts serves GET /api/accounts and DELETE /api/accounts/{id}.
func (a *APIHandler) HandleAccounts(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/api/accounts")
	path = strings.Trim(path, "/")

	if path == "" {
		if r.Method == http.MethodGet {
			a.listAccounts(w, r)
			return
		}
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	parts := strings.Split(path, "/")
	accountID := parts[0]

	if len(parts) == 2 && parts[1] == "select" {
		if r.Method == http.MethodPost {
			a.selectAccount(w, r, accountID)
			return
		}
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			a.getAccount(w, r, accountID)
			return
		case http.MethodDelete:
			a.deleteAccount(w, r, accountID)
			return
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
	}

	http.NotFound(w, r)
}

func (a *APIHandler) listAccounts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if a.accountRepo == nil {
		writeJSON(w, http.StatusOK, []*AccountWithBuckets{})
		return
	}

	accounts, err := a.accountRepo.List(ctx)
	if err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "failed to list accounts", err)
		return
	}

	var allBuckets map[string][]*domain.QuotaBucket
	if a.quotaRepo != nil {
		allBuckets, _ = a.quotaRepo.ListAll(ctx)
	}

	result := make([]*AccountWithBuckets, 0, len(accounts))
	for _, acc := range accounts {
		var b []*domain.QuotaBucket
		if allBuckets != nil {
			b = allBuckets[acc.ID]
		}
		verification := a.verification(ctx, acc.ID)
		unavailable := a.quotaUnavailable(ctx, acc.ID)
		if unavailable || verification != nil {
			b = nil
		}
		if b == nil {
			b = []*domain.QuotaBucket{}
		}
		result = append(result, &AccountWithBuckets{
			Account:          acc,
			Buckets:          b,
			Families:         a.familyQuotaViews(ctx, acc, b, unavailable),
			QuotaUnavailable: unavailable,
			Verification:     verification,
		})
	}

	writeJSON(w, http.StatusOK, result)
}

func (a *APIHandler) getAccount(w http.ResponseWriter, r *http.Request, id string) {
	ctx := r.Context()
	if a.accountRepo == nil {
		http.NotFound(w, r)
		return
	}

	acc, err := a.accountRepo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, domain.ErrAccountNotFound) {
			http.NotFound(w, r)
			return
		}
		writeErrorJSON(w, http.StatusInternalServerError, "failed to get account", err)
		return
	}

	var buckets []*domain.QuotaBucket
	if a.quotaRepo != nil {
		buckets, _ = a.quotaRepo.GetByAccountID(ctx, id)
	}
	verification := a.verification(ctx, acc.ID)
	unavailable := a.quotaUnavailable(ctx, acc.ID)
	if unavailable || verification != nil {
		buckets = nil
	}
	if buckets == nil {
		buckets = []*domain.QuotaBucket{}
	}

	writeJSON(w, http.StatusOK, &AccountWithBuckets{
		Account:          acc,
		Buckets:          buckets,
		Families:         a.familyQuotaViews(ctx, acc, buckets, unavailable),
		QuotaUnavailable: unavailable,
		Verification:     verification,
	})
}

func (a *APIHandler) selectAccount(w http.ResponseWriter, r *http.Request, id string) {
	ctx := r.Context()
	if a.accountRepo == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "account repository unavailable", nil)
		return
	}

	acc, err := a.accountRepo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, domain.ErrAccountNotFound) {
			writeErrorJSON(w, http.StatusNotFound, "account not found", err)
			return
		}
		writeErrorJSON(w, http.StatusInternalServerError, "failed to lookup account", err)
		return
	}

	if err := a.accountRepo.SetActive(ctx, id); err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "failed to activate account", err)
		return
	}

	evt := &domain.ProxyEvent{
		Type:      domain.EventTypeAccountSwitched,
		AccountID: id,
		Message:   fmt.Sprintf("Account %s (%s) set as active via dashboard", acc.Email, id),
		Timestamp: time.Now().UTC(),
	}
	if a.broadcaster != nil {
		a.broadcaster.Broadcast(evt)
	}
	if a.eventRepo != nil {
		_ = a.eventRepo.Record(ctx, evt)
	}

	if a.poller != nil {
		go func() {
			_ = a.poller.PollOnce(context.Background())
		}()
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success":    true,
		"account_id": id,
		"email":      acc.Email,
	})
}

func (a *APIHandler) deleteAccount(w http.ResponseWriter, r *http.Request, id string) {
	ctx := r.Context()
	if a.accountRepo == nil {
		writeErrorJSON(w, http.StatusServiceUnavailable, "account repository unavailable", nil)
		return
	}

	acc, err := a.accountRepo.GetByID(ctx, id)
	if err != nil {
		if errors.Is(err, domain.ErrAccountNotFound) {
			writeErrorJSON(w, http.StatusNotFound, "account not found", err)
			return
		}
		writeErrorJSON(w, http.StatusInternalServerError, "failed to lookup account", err)
		return
	}

	wasActive := acc.IsActive

	if err := a.accountRepo.Delete(ctx, id); err != nil {
		if errors.Is(err, domain.ErrAccountNotFound) {
			writeErrorJSON(w, http.StatusNotFound, "account not found", err)
			return
		}
		writeErrorJSON(w, http.StatusInternalServerError, "failed to delete account", err)
		return
	}

	if a.quotaRepo != nil {
		_ = a.quotaRepo.DeleteByAccountID(ctx, id)
	}

	// If the deleted account was active, auto-promote next available account
	if wasActive {
		if next, nextErr := a.accountRepo.GetNextAvailable(ctx, ""); nextErr == nil && next != nil {
			_ = a.accountRepo.SetActive(ctx, next.ID)
			if a.broadcaster != nil {
				a.broadcaster.Broadcast(&domain.ProxyEvent{
					Type:      domain.EventTypeAccountSwitched,
					AccountID: next.ID,
					Message:   fmt.Sprintf("Account %s automatically promoted to active following deletion of %s", next.Email, acc.Email),
					Timestamp: time.Now().UTC(),
				})
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"success":    true,
		"account_id": id,
	})
}

// HandleMetrics serves GET /api/metrics.
func (a *APIHandler) HandleMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx := r.Context()
	if a.metricsService == nil {
		writeJSON(w, http.StatusOK, &domain.MetricsDashboardPayload{
			Summary: domain.GlobalDashboardSummary{
				Today:     &domain.AggregatedMetrics{},
				ThisWeek:  &domain.AggregatedMetrics{},
				ThisMonth: &domain.AggregatedMetrics{},
				AllTime:   &domain.AggregatedMetrics{},
			},
			ByAccount: []*domain.AccountMetricsSummary{},
			Timeline:  []*domain.DailyTokenUsage{},
		})
		return
	}

	accountID := r.URL.Query().Get("account_id")
	periodParam := r.URL.Query().Get("period")
	tzParam := r.URL.Query().Get("tz")
	tzOffsetParam := r.URL.Query().Get("tz_offset")
	loc := parseLocation(tzParam, tzOffsetParam)

	if accountID != "" {
		norm := domain.PeriodLifetime
		if periodParam != "" {
			norm = domain.MetricPeriod(strings.ToLower(periodParam))
		}
		summary, err := a.metricsService.GetSummary(ctx, accountID, norm)
		if err != nil {
			writeErrorJSON(w, http.StatusInternalServerError, "failed to get account summary", err)
			return
		}
		writeJSON(w, http.StatusOK, summary)
		return
	}

	payload, err := a.metricsService.GetDashboardPayloadWithLocation(ctx, 14, loc)
	if err != nil {
		writeErrorJSON(w, http.StatusInternalServerError, "failed to compute metrics dashboard payload", err)
		return
	}

	writeJSON(w, http.StatusOK, payload)
}

// parseLocation resolves a *time.Location from either an IANA timezone name or a numeric minute offset.
func parseLocation(tzParam, tzOffsetParam string) *time.Location {
	if tzParam != "" {
		if loc, err := time.LoadLocation(tzParam); err == nil {
			return loc
		}
	}
	if tzOffsetParam != "" {
		// tz_offset in minutes: -new Date().getTimezoneOffset()
		// e.g. -180 for UTC-3, +540 for UTC+9
		if offsetMinutes, err := strconv.Atoi(tzOffsetParam); err == nil {
			hours := offsetMinutes / 60
			mins := int(math.Abs(float64(offsetMinutes % 60)))
			name := fmt.Sprintf("UTC%+03d:%02d", hours, mins)
			return time.FixedZone(name, offsetMinutes*60)
		}
	}
	return time.UTC
}

// HandleEvents serves GET /api/events as a real-time SSE stream.
func (a *APIHandler) HandleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported by client connection", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ctx := r.Context()

	// 1. Send recent historical events if event repository is present
	if a.eventRepo != nil {
		recent, err := a.eventRepo.ListRecent(ctx, 30)
		if err == nil && len(recent) > 0 {
			// ListRecent returns newest first; reverse for chronological playback
			for i := len(recent) - 1; i >= 0; i-- {
				evt := recent[i]
				if data, err := json.Marshal(evt); err == nil {
					_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
				}
			}
			flusher.Flush()
		}
	}

	// 2. Stream real-time events via broadcaster
	if a.broadcaster == nil {
		<-ctx.Done()
		return
	}

	eventChan, unsubscribe := a.broadcaster.Subscribe()
	defer unsubscribe()

	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-eventChan:
			if !ok {
				return
			}
			data, err := json.Marshal(evt)
			if err != nil {
				continue
			}
			_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}
}

// HandleOAuthStart serves POST/GET /oauth/start.
func (a *APIHandler) HandleOAuthStart(w http.ResponseWriter, r *http.Request) {
	if a.oauthEngine == nil {
		writeErrorJSON(w, http.StatusNotImplemented, "OAuth2 engine not configured", nil)
		return
	}

	urlChan := make(chan string, 1)
	errChan := make(chan error, 1)

	// Non-blocking trigger of loopback flow
	go func() {
		acc, err := a.oauthEngine.StartLoopbackFlow(context.Background(), nil, func(authURL string) {
			select {
			case urlChan <- authURL:
			default:
			}
			if a.broadcaster != nil {
				a.broadcaster.Broadcast(&domain.ProxyEvent{
					Type:      domain.EventType("oauth_started"),
					Message:   "OAuth authorization flow initiated",
					Timestamp: time.Now().UTC(),
				})
			}
		})
		if err != nil {
			select {
			case errChan <- err:
			default:
			}
			if a.broadcaster != nil {
				a.broadcaster.Broadcast(&domain.ProxyEvent{
					Type:      domain.EventTypeError,
					Message:   "OAuth flow failed; start sign-in again",
					Timestamp: time.Now().UTC(),
				})
			}
		} else if acc != nil {
			evt := &domain.ProxyEvent{
				Type:      domain.EventTypeAccountSwitched,
				AccountID: acc.ID,
				Message:   fmt.Sprintf("Account %s successfully connected via OAuth", acc.Email),
				Timestamp: time.Now().UTC(),
			}
			if a.broadcaster != nil {
				a.broadcaster.Broadcast(evt)
			}
			if a.eventRepo != nil {
				_ = a.eventRepo.Record(context.Background(), evt)
			}
		}
	}()

	var generatedAuthURL string
	select {
	case generatedAuthURL = <-urlChan:
	case err := <-errChan:
		writeErrorJSON(w, http.StatusInternalServerError, "Failed to initiate OAuth authorization", err)
		return
	case <-time.After(2 * time.Second):
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":   "started",
		"message":  "OAuth loopback flow initiated in browser",
		"auth_url": generatedAuthURL,
	})
}

func writeJSON(w http.ResponseWriter, statusCode int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(data)
}

func writeErrorJSON(w http.ResponseWriter, statusCode int, message string, err error) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)
	detail := ""
	if err != nil {
		detail = err.Error()
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"error": map[string]any{
			"code":    statusCode,
			"message": message,
			"detail":  detail,
		},
	})
}

// ConfigResponse represents the response payload for GET /api/config.

// ConfigUpdateRequest represents the payload for POST /api/config.

// HandleModels serves GET /api/models.
func (a *APIHandler) HandleModels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	ctx := r.Context()
	models, source := a.discoverModels(ctx)

	writeJSON(w, http.StatusOK, ModelsResponse{
		Models: models,
		Source: source,
	})
}

func (a *APIHandler) discoverModels(ctx context.Context) ([]*domain.ModelInfo, string) {
	// Prefer Google's account-specific catalog over a potentially older IDE list.
	if a.accountRepo != nil {
		if activeAcc, err := a.accountRepo.GetActive(ctx); err == nil && activeAcc != nil && activeAcc.AccessToken != "" {
			if ccModels, err := quota.FetchAvailableModelsFromCloudCode(ctx, activeAcc.AccessToken); err == nil && len(ccModels) > 0 {
				return ccModels, "cloud_code_pa"
			}
		}
	}
	if lsModels, err := quota.QueryAvailableModels(ctx); err == nil && len(lsModels) > 0 {
		return lsModels, "language_server"
	}
	return quota.DefaultModelCatalog(), "catalog"
}

// modelCategory labels configured IDs without restricting what the proxy accepts.
func modelCategory(id string) string {
	lower := strings.ToLower(id)
	if strings.Contains(lower, "claude") || strings.Contains(lower, "gpt") {
		return "claude_gpt"
	}
	if strings.Contains(lower, "gemini") {
		return "gemini"
	}
	return "unknown"
}

// FamilyQuotaView distinguishes measured/cached evidence from reactive cooldowns.
type FamilyQuotaView struct {
	Active    bool       `json:"active"`
	Status    string     `json:"status"`
	Evidence  string     `json:"evidence"`
	ResetTime *time.Time `json:"reset_time,omitempty"`
}

func (a *APIHandler) familyQuotaViews(ctx context.Context, acc *domain.Account, buckets []*domain.QuotaBucket, unavailable bool) map[domain.QuotaFamily]FamilyQuotaView {
	result := make(map[domain.QuotaFamily]FamilyQuotaView)
	if a.verification(ctx, acc.ID) != nil {
		for _, family := range []domain.QuotaFamily{domain.QuotaFamilyArgon, domain.QuotaFamilyGemini, domain.QuotaFamilyClaudeGPT} {
			result[family] = FamilyQuotaView{Status: "needs_verification", Evidence: "Complete Google verification, then refresh quotas to recheck"}
		}
		return result
	}

	now := time.Now().UTC()
	for _, family := range []domain.QuotaFamily{domain.QuotaFamilyArgon, domain.QuotaFamilyGemini, domain.QuotaFamilyClaudeGPT} {
		if unavailable {
			result[family] = FamilyQuotaView{Status: "unavailable", Evidence: "Quota endpoint unavailable; a successful live quota poll is required"}
			continue
		}
		active := false
		if repo, ok := a.quotaRepo.(domain.FamilyActiveRepository); ok {
			id, err := repo.GetFamilyActive(ctx, family)
			active = err == nil && id == acc.ID
		}
		known, available := domain.FamilyQuota(buckets, family, now)
		view := FamilyQuotaView{Active: active, Status: "unknown", Evidence: "No family-specific quota reported"}
		var reset time.Time
		if known {
			view.Evidence = "Cached quota evidence; live refresh not guaranteed"
			view.Status = "eligible"
			if !available {
				view.Status = "blocked"
			}
			for _, b := range buckets {
				if domain.BucketFamily(b) == family && b.IsExhausted() && !b.HasReset(now) && b.ResetTime.After(reset) {
					reset = b.ResetTime
				}
			}
		}
		if repo, ok := a.quotaRepo.(domain.FamilyCooldownRepository); ok {
			cooldown, err := repo.GetFamilyCooldown(ctx, acc.ID, family)
			if err != nil {
				view.Status = "unavailable"
			} else if cooldown.After(now) {
				view.Status = "cooldown"
				if cooldown.After(reset) {
					reset = cooldown
				}
			}
		}
		if acc.Status == domain.AccountStatusDisabled || acc.Status == domain.AccountStatusError {
			view.Status = string(acc.Status)
		}
		if acc.Status == domain.AccountStatusExhausted && !known && view.Status == "unknown" {
			view.Evidence = "Legacy exhaustion; no family-specific quota reported"
		}
		if !reset.IsZero() {
			r := reset
			view.ResetTime = &r
		}
		result[family] = view
	}
	return result
}

func (a *APIHandler) quotaUnavailable(ctx context.Context, accountID string) bool {
	if repo, ok := a.quotaRepo.(domain.QuotaAvailabilityRepository); ok {
		unavailable, err := repo.IsQuotaUnavailable(ctx, accountID)
		return err != nil || unavailable
	}
	return false
}

type ModelsResponse struct {
	Models []*domain.ModelInfo `json:"models"`
	Source string              `json:"source"`
}

// HandleOAuthSubmit accepts a callback URL without making any request to that URL.
func (a *APIHandler) HandleOAuthSubmit(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		writeErrorJSON(w, 415, "JSON submission required", nil)
		return
	}
	if origin := r.Header.Get("Origin"); origin != "" {
		u, err := url.Parse(origin)
		if err != nil || u.Host != r.Host || (u.Scheme != "http" && u.Scheme != "https") {
			writeErrorJSON(w, 403, "Cross-origin callback submission rejected", nil)
			return
		}
	}

	engine, ok := a.oauthEngine.(interface {
		SubmitCallbackURL(context.Context, string) (*domain.Account, error)
	})
	if !ok {
		writeErrorJSON(w, http.StatusNotImplemented, "Callback submission unavailable", nil)
		return
	}
	var body struct {
		URL string `json:"callback_url"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 10000)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErrorJSON(w, 400, "Invalid callback submission", nil)
		return
	}
	_, err := engine.SubmitCallbackURL(r.Context(), body.URL)
	if err != nil {
		writeErrorJSON(w, 400, "Callback rejected or authorization failed. Check the pending sign-in or start again.", nil)
		return
	}
	writeJSON(w, 200, map[string]string{"status": "connected"})
}

func (a *APIHandler) verification(ctx context.Context, id string) *domain.VerificationRequirement {
	if repo, ok := a.quotaRepo.(domain.VerificationRepository); ok {
		requirement, err := repo.GetVerification(ctx, id)
		if err != nil {
			return &domain.VerificationRequirement{}
		}
		return requirement
	}
	return nil
}
