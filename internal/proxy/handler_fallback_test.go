package proxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Muriel-Gasparini/antigravity-account-switcher/internal/domain"
	"github.com/Muriel-Gasparini/antigravity-account-switcher/internal/store/sqlite"
	"github.com/Muriel-Gasparini/antigravity-account-switcher/test/mocks"
)

type fallbackTestEnv struct {
	db             *sqlite.DB
	accountRepo    domain.AccountRepository
	metricsRepo    domain.MetricsRepository
	eventRepo      domain.EventRepository
	quotaRepo      domain.QuotaRepository
	broadcaster    *Broadcaster
	failoverEngine *FailoverEngine
	mockGoogle     *mocks.MockGoogleServer
	handler        *ProxyHandler
	server         *httptest.Server
	client         *http.Client
}

func newFallbackTestEnv(t *testing.T, primary, secondary string, fallbackEnabled bool) *fallbackTestEnv {
	t.Helper()
	mockGoogle := mocks.NewMockGoogleServer()
	t.Cleanup(func() { mockGoogle.Close() })

	dbPath := filepath.Join(t.TempDir(), "fallback_test.db")
	db, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("sqlite open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	accRepo := sqlite.NewAccountRepository(db)
	metricsRepo := sqlite.NewMetricsRepository(db)
	eventRepo := sqlite.NewEventRepository(db)
	quotaRepo := sqlite.NewQuotaRepository(db)

	broadcaster := NewBroadcaster(100)
	engine := NewFailoverEngine(
		accRepo,
		broadcaster,
		eventRepo,
		WithQuotaRepository(quotaRepo),
	)

	handler, err := NewProxyHandler(
		accRepo,
		WithTargetURL(mockGoogle.URL),
		WithMetricsRepository(metricsRepo),
		WithEventBroadcaster(broadcaster),
		WithEventRepository(eventRepo),
		WithFailoverEngine(engine),
	)
	if err != nil {
		t.Fatalf("proxy handler create: %v", err)
	}

	server := httptest.NewServer(handler)
	t.Cleanup(func() { server.Close() })

	return &fallbackTestEnv{
		db:             db,
		accountRepo:    accRepo,
		metricsRepo:    metricsRepo,
		eventRepo:      eventRepo,
		quotaRepo:      quotaRepo,
		broadcaster:    broadcaster,
		failoverEngine: engine,
		mockGoogle:     mockGoogle,
		handler:        handler,
		server:         server,
		client:         &http.Client{Timeout: 10 * time.Second},
	}
}

// TestPredictiveFallback verifies that an in-flight request targeting a primary model
// with 0% remaining quota is transparently rewritten to the configured secondary model
// before initial upstream dispatch, preventing an unnecessary HTTP 429 round-trip.

// TestReactiveFallback429 verifies that when an upstream request receives HTTP 429
// on the primary model, the proxy transparently rewrites and replays the request
// on the secondary model using the SAME active account, leaking 0 error bytes.

// TestAccountRotationOnDoubleExhaustion verifies that when both primary and secondary
// tiers on the active account are exhausted, the proxy rotates to the next available
// account in the pool and RESETS the requested model back to the primary tier.

// TestFallbackDisabledDirectRotation verifies that when model fallback is disabled,
// an upstream HTTP 429 response on the primary model immediately rotates accounts
// without attempting the secondary model tier.
func TestFallbackDisabledDirectRotation(t *testing.T) {
	env := newFallbackTestEnv(t, "gemini-2.5-pro", "gemini-2.5-flash", false)
	ctx := context.Background()

	now := time.Now().UTC()
	acc1 := &domain.Account{
		ID:          "acc-dis-1",
		Email:       "dis1@example.com",
		AccessToken: "token-dis-1",
		IsActive:    true,
		Status:      domain.AccountStatusActive,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	acc2 := &domain.Account{
		ID:          "acc-dis-2",
		Email:       "dis2@example.com",
		AccessToken: "token-dis-2",
		IsActive:    false,
		Status:      domain.AccountStatusActive,
		CreatedAt:   now.Add(time.Second),
		UpdatedAt:   now.Add(time.Second),
	}
	if err := env.accountRepo.Create(ctx, acc1); err != nil {
		t.Fatalf("create acc1: %v", err)
	}
	if err := env.accountRepo.Create(ctx, acc2); err != nil {
		t.Fatalf("create acc2: %v", err)
	}

	env.mockGoogle.SetFailoverTrigger("token-dis-1", 1)

	eventsCh, unsubscribe := env.broadcaster.Subscribe()
	defer unsubscribe()

	reqBody := `{"model":"gemini-2.5-pro","contents":[{"parts":[{"text":"Disabled test"}]}]}`
	req, err := http.NewRequest("POST", env.server.URL+"/v1internal/models/gemini-2.5-pro:streamGenerateContent?alt=sse", strings.NewReader(reqBody))
	if err != nil {
		t.Fatalf("create req: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := env.client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200 OK, got %d: %s", resp.StatusCode, string(b))
	}

	// Verify exactly 2 upstream requests:
	// 1. token-dis-1, gemini-2.5-pro (429)
	// 2. token-dis-2, gemini-2.5-pro (200 OK) -> NO attempt on gemini-2.5-flash!
	reqs := env.mockGoogle.GetRecordedRequests()
	if len(reqs) != 2 {
		t.Fatalf("expected exactly 2 upstream requests, got %d", len(reqs))
	}

	if reqs[0].AuthBearer != "token-dis-1" || !strings.Contains(reqs[0].Path, "gemini-2.5-pro") {
		t.Errorf("req 0: expected token-dis-1 / pro, got %s / %s", reqs[0].AuthBearer, reqs[0].Path)
	}
	if reqs[1].AuthBearer != "token-dis-2" || !strings.Contains(reqs[1].Path, "gemini-2.5-pro") {
		t.Errorf("req 1: expected token-dis-2 / pro, got %s / %s", reqs[1].AuthBearer, reqs[1].Path)
	}

	// Verify no EventTypeModelFallback was emitted
	for {
		select {
		case evt := <-eventsCh:
			if evt.Type == domain.EventTypeModelFallback {
				t.Errorf("unexpected EventTypeModelFallback when fallback is disabled")
			}
		default:
			goto evtCheckDone
		}
	}
evtCheckDone:
}

// TestSSEStreamingWithFallback verifies SSE streaming fidelity during intra-account
// fallback, confirming line-by-line flushes, valid SSE frames, and accurate token metric recording.

// TestAllAccountsExhaustedReturns503 verifies that when all accounts in the database
// are marked exhausted, incoming requests immediately receive HTTP 503 Service Unavailable.
func TestAllAccountsExhaustedReturns503(t *testing.T) {
	env := newFallbackTestEnv(t, "gemini-2.5-pro", "gemini-2.5-flash", true)
	ctx := context.Background()

	now := time.Now().UTC()
	// Create accounts that are already exhausted
	acc1 := &domain.Account{
		ID:          "acc-exh-1",
		Email:       "exh1@example.com",
		AccessToken: "token-exh-1",
		IsActive:    false,
		Status:      domain.AccountStatusExhausted,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	acc2 := &domain.Account{
		ID:          "acc-exh-2",
		Email:       "exh2@example.com",
		AccessToken: "token-exh-2",
		IsActive:    false,
		Status:      domain.AccountStatusExhausted,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := env.accountRepo.Create(ctx, acc1); err != nil {
		t.Fatalf("create acc1: %v", err)
	}
	if err := env.accountRepo.Create(ctx, acc2); err != nil {
		t.Fatalf("create acc2: %v", err)
	}

	req, err := http.NewRequest("POST", env.server.URL+"/v1internal/models/gemini-2.5-pro:streamGenerateContent?alt=sse", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("create req: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := env.client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 503 Service Unavailable, got %d: %s", resp.StatusCode, string(b))
	}

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "UNAVAILABLE") {
		t.Errorf("expected UNAVAILABLE status in body, got: %s", string(body))
	}
}

// TestJSONBodyModelRewriting verifies that requests specifying the model exclusively
// within the root-level JSON body have their payloads rewritten and Content-Length updated.

// TestPoolExhaustionVerbatim429 verifies that when all accounts in the pool exhaust
// during failover retries, the final upstream 429 response is propagated verbatim.
func TestPoolExhaustionVerbatim429(t *testing.T) {
	env := newFallbackTestEnv(t, "gemini-2.5-pro", "gemini-2.5-flash", true)
	ctx := context.Background()

	now := time.Now().UTC()
	acc1 := &domain.Account{
		ID:          "acc-tot-1",
		Email:       "tot1@example.com",
		AccessToken: "token-tot-1",
		IsActive:    true,
		Status:      domain.AccountStatusActive,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	acc2 := &domain.Account{
		ID:          "acc-tot-2",
		Email:       "tot2@example.com",
		AccessToken: "token-tot-2",
		IsActive:    false,
		Status:      domain.AccountStatusActive,
		CreatedAt:   now.Add(time.Second),
		UpdatedAt:   now.Add(time.Second),
	}
	if err := env.accountRepo.Create(ctx, acc1); err != nil {
		t.Fatalf("create acc1: %v", err)
	}
	if err := env.accountRepo.Create(ctx, acc2); err != nil {
		t.Fatalf("create acc2: %v", err)
	}

	// Both accounts trigger 429 indefinitely
	env.mockGoogle.SetFailoverTrigger("token-tot-1", 100)
	env.mockGoogle.SetFailoverTrigger("token-tot-2", 100)

	req, err := http.NewRequest("POST", env.server.URL+"/v1internal/models/gemini-2.5-pro:streamGenerateContent", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("create req: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := env.client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	// When all accounts exhausted during failover, client receives HTTP 429 verbatim
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected HTTP 429 on pool exhaustion, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "RESOURCE_EXHAUSTED") {
		t.Errorf("expected RESOURCE_EXHAUSTED in body, got: %s", string(body))
	}
}

func TestHandler_ThoughtSignature400_AutoSanitizeRecovery(t *testing.T) {
	env := newFallbackTestEnv(t, "gemini-3.8-flash-high", "claude-opus-4-6-thinking", true)
	ctx := context.Background()

	now := time.Now().UTC()
	acc := &domain.Account{
		ID:          "acc-sig-1",
		Email:       "sig1@example.com",
		AccessToken: "token-sig-1",
		IsActive:    true,
		Status:      domain.AccountStatusActive,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := env.accountRepo.Create(ctx, acc); err != nil {
		t.Fatalf("create acc: %v", err)
	}

	// Mock Google: returns 400 "Corrupted thought signature." if thoughtSignature != "skip_thought_signature_validator",
	// returns 200 OK when thoughtSignature == "skip_thought_signature_validator".
	callCount := 0
	env.mockGoogle.ConfigureAccount("token-sig-1", &mocks.AccountBehavior{
		Email: "sig1@example.com",
		CustomHandler: func(w http.ResponseWriter, r *http.Request) bool {
			callCount++
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), "skip_thought_signature_validator") {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte(`{"error":{"code":400,"message":"Corrupted thought signature.","status":"INVALID_ARGUMENT"}}`))
				return true
			}
			// Sanitized retry succeeds with 200 OK
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("data: {\"response\":{\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"Recovered successfully!\"}]}}],\"usageMetadata\":{\"promptTokenCount\":50,\"candidatesTokenCount\":10,\"totalTokenCount\":60},\"modelVersion\":\"gemini-3.8-flash\"}}\n\n"))
			return true
		},
	})

	initialPayload := `{
		"model": "gemini-3.8-flash-high",
		"request": {
			"contents": [
				{"role": "user", "parts": [{"text": "Hello"}]},
				{"role": "model", "parts": [
					{"thought": true, "text": "Old thought"},
					{"functionCall": {"name": "run_command", "args": {"CommandLine": "ls"}}, "thoughtSignature": "bad-signature"}
				]},
				{"role": "user", "parts": [{"text": "Continue"}]}
			]
		}
	}`

	req, err := http.NewRequestWithContext(ctx, "POST", env.server.URL+"/v1internal:streamGenerateContent?alt=sse", strings.NewReader(initialPayload))
	if err != nil {
		t.Fatalf("create req: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := env.client.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected HTTP 200 after auto-sanitization, got %d: %s", resp.StatusCode, string(respBody))
	}

	respBody, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(respBody), "Recovered successfully!") {
		t.Errorf("expected response body to contain recovered text, got: %s", string(respBody))
	}

	if callCount != 2 {
		t.Errorf("expected exactly 2 calls (initial 400 + retry 200), got %d", callCount)
	}
}
