package proxy

import (
	"context"
	"strings"
	"time"

	"github.com/Muriel-Gasparini/antigravity-account-switcher/internal/domain"
)

func modelFamily(model string) domain.QuotaFamily {
	switch CategorizeModel(model) {
	case CategoryArgon:
		return domain.QuotaFamilyArgon
	case CategoryGemini:
		return domain.QuotaFamilyGemini
	case CategoryClaudeGPT:
		return domain.QuotaFamilyClaudeGPT
	default:
		return ""
	}
}

func isCarbonModel(model string) bool {
	lower := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(model)), "models/")
	return lower == "carbon" || strings.HasPrefix(lower, "carbon-") || strings.Contains(lower, "gemini-4-carbon")
}

// Trial Argon remains a distinct, explicitly requested product.
func requiresUltra(model string) bool {
	lower := entitlementModelKey(model)
	return isCarbonModel(model) || (modelFamily(model) == domain.QuotaFamilyArgon && !strings.Contains(lower, "trial"))
}

// accountQuotaFamilyLocked resolves topology per account for mixed-tier pools.
func (f *FailoverEngine) accountQuotaFamilyLocked(ctx context.Context, id, model string) domain.QuotaFamily {
	family := modelFamily(model)
	if family == domain.QuotaFamilyArgon {
		if repo, ok := f.quotaRepo.(domain.SharedGeminiRepository); ok {
			if shared, err := repo.GetSharedGemini(ctx, id); err == nil && shared {
				return domain.QuotaFamilyGemini
			}
		}
	}
	return family
}

// familyEligibleLocked is shared by preflight, rotation and anti-stampede adoption.
// Auth errors and manual disables always win, even when a quota snapshot is healthy.
func (f *FailoverEngine) familyEligibleLocked(ctx context.Context, acc *domain.Account, model string) bool {
	if acc == nil || (acc.Status != domain.AccountStatusActive && acc.Status != domain.AccountStatusExhausted) {
		return false
	}
	if f.modelDenied[acc.ID][entitlementModelKey(model)].After(time.Now()) {
		return false
	}
	if !f.quotaEndpointAvailableLocked(ctx, acc) {
		return false
	}
	// Normal Argon and Carbon are Ultra-only entitlements. Missing/failed topology evidence is
	// not permission to probe a non-Ultra account, including during rotation.
	if requiresUltra(model) {
		repo, ok := f.quotaRepo.(domain.SharedGeminiRepository)
		if !ok {
			return false
		}
		shared, err := repo.GetSharedGemini(ctx, acc.ID)
		if err != nil || !shared {
			return false
		}
	}
	family := f.accountQuotaFamilyLocked(ctx, acc.ID, model)
	if family == "" {
		return acc.IsAvailable()
	}
	now := time.Now().UTC()
	if repo, ok := f.quotaRepo.(domain.FamilyCooldownRepository); ok {
		reset, err := repo.GetFamilyCooldown(ctx, acc.ID, family)
		if err != nil || reset.After(now) {
			return false
		}
	} else if f.familyCooldowns[acc.ID][family].After(now) {
		return false
	}
	known, available := false, true
	if f.quotaRepo != nil {
		buckets, err := f.quotaRepo.GetByAccountID(ctx, acc.ID)
		if err != nil {
			return false
		}
		known, available = domain.FamilyQuota(buckets, family, now)
	}
	return available && (acc.IsAvailable() || known) && (!requiresUltra(model) || known)
}

// ResolveForModel never changes the client's selected model. It permits recovery
// of old global-exhausted rows only with affirmative family-specific quota evidence.
func (f *FailoverEngine) ResolveForModel(ctx context.Context, model string) (*domain.Account, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	// Trial requests prefer eligible confirmed Ultra accounts, promoted upstream.
	if entitlementModelKey(model) == "gemini-4-argon-trial" {
		if accounts, err := f.accountRepo.List(ctx); err == nil {
			for _, acc := range accounts {
				if f.accountQuotaFamilyLocked(ctx, acc.ID, model) == domain.QuotaFamilyGemini && f.familyEligibleLocked(ctx, acc, model) && f.familyEligibleLocked(ctx, acc, "gemini-4-argon-low") {
					if err := f.setActiveForModelLocked(ctx, model, acc.ID); err != nil {
						return nil, err
					}
					return acc, nil
				}
			}
		}
	}
	active, err := f.activeForModelLocked(ctx, model)
	// Model-less legacy traffic has no family to preflight; preserve transparent
	// upstream exhaustion responses, but never adopt disabled or auth-error rows.
	if model == "" && err == nil && active != nil && active.Status == domain.AccountStatusExhausted && f.quotaEndpointAvailableLocked(ctx, active) {
		return active, nil
	}
	if err == nil && f.familyEligibleLocked(ctx, active, model) {
		if err := f.setActiveForModelLocked(ctx, model, active.ID); err != nil {
			return nil, err
		}
		return active, nil
	}
	return f.selectFamilyAccountLocked(ctx, model, "")
}

func (f *FailoverEngine) selectFamilyAccountLocked(ctx context.Context, model, exclude string) (*domain.Account, error) {
	accounts, err := f.accountRepo.List(ctx)
	if err != nil {
		return nil, err
	}
	for _, acc := range accounts {
		if acc.ID == exclude || !f.familyEligibleLocked(ctx, acc, model) {
			continue
		}
		if err := f.setActiveForModelLocked(ctx, model, acc.ID); err != nil {
			return nil, err
		}
		return acc, nil
	}
	if err := f.setActiveForModelLocked(ctx, model, ""); err != nil {
		return nil, err
	}
	return nil, domain.ErrNoAvailableAccount
}

// rotateFamilyLocked records exhaustion and automatically selects within this family.

func (f *FailoverEngine) rotateFamilyLocked(ctx context.Context, acc *domain.Account, model string) (*domain.Account, error) {
	family := f.accountQuotaFamilyLocked(ctx, acc.ID, model)
	now := time.Now().UTC()
	reset := now.Add(5 * time.Minute)
	if f.quotaRepo != nil {
		buckets, err := f.quotaRepo.GetByAccountID(ctx, acc.ID)
		if err != nil {
			return nil, err
		}
		// A blocked weekly window must not be shortened to the burst reset.
		for _, b := range buckets {
			if domain.BucketFamily(b) == family && b.IsExhausted() && b.ResetTime.After(reset) {
				reset = b.ResetTime
			}
		}
	}
	if repo, ok := f.quotaRepo.(domain.FamilyCooldownRepository); ok {
		if err := repo.SetFamilyCooldown(ctx, acc.ID, family, reset); err != nil {
			return nil, err
		}
	} else {
		if f.familyCooldowns == nil {
			f.familyCooldowns = make(map[string]map[domain.QuotaFamily]time.Time)
		}
		if f.familyCooldowns[acc.ID] == nil {
			f.familyCooldowns[acc.ID] = make(map[domain.QuotaFamily]time.Time)
		}
		f.familyCooldowns[acc.ID][family] = reset
	}
	f.emitEvent(&domain.ProxyEvent{Type: domain.EventTypeFailover429, AccountID: acc.ID, Message: "Model family exhausted after upstream quota error", Details: map[string]any{"family": string(family), "reset_time": reset}, Timestamp: now})
	// Persist before adopting another in-flight request's active selection.
	current, err := f.activeForModelLocked(ctx, model)
	if err == nil && current != nil && current.ID != acc.ID && f.familyEligibleLocked(ctx, current, model) {
		return current, nil
	}
	next, err := f.selectFamilyAccountLocked(ctx, model, acc.ID)
	if err != nil {
		f.emitEvent(&domain.ProxyEvent{Type: domain.EventTypeQuotaExhausted, AccountID: acc.ID, Message: "No eligible accounts for requested model family", Timestamp: now})
		return nil, err
	}
	f.emitEvent(&domain.ProxyEvent{Type: domain.EventTypeAccountSwitched, AccountID: next.ID, Message: "Rotated account for requested model family", Timestamp: now})
	return next, nil
}

// quotaEndpointAvailableLocked rejects accounts blocked by a quota-endpoint 403.

func (f *FailoverEngine) quotaEndpointAvailableLocked(ctx context.Context, acc *domain.Account) bool {
	if acc != nil {
		if repo, ok := f.quotaRepo.(domain.VerificationRepository); ok {
			requirement, err := repo.GetVerification(ctx, acc.ID)
			if err != nil || requirement != nil {
				return false
			}
		}
	}
	if acc == nil {
		return false
	}
	if repo, ok := f.quotaRepo.(domain.QuotaAvailabilityRepository); ok {
		blocked, err := repo.IsQuotaUnavailable(ctx, acc.ID)
		return err == nil && !blocked
	}
	return true
}

func (f *FailoverEngine) activeForModelLocked(ctx context.Context, model string) (*domain.Account, error) {
	family := modelFamily(model)
	if family == "" {
		return f.accountRepo.GetActive(ctx)
	}
	id := ""
	if repo, ok := f.quotaRepo.(domain.FamilyActiveRepository); ok {
		var err error
		id, err = repo.GetFamilyActive(ctx, family)
		if err != nil {
			return nil, err
		}
	} else {
		id = f.familyActive[family]
	}
	if id != "" {
		return f.accountRepo.GetByID(ctx, id)
	}
	// Seed an uninitialized family from the legacy preference without mutating it.
	return f.accountRepo.GetActive(ctx)
}
func (f *FailoverEngine) setActiveForModelLocked(ctx context.Context, model, id string) error {
	family := modelFamily(model)
	if family == "" {
		if id == "" {
			return nil
		}
		return f.accountRepo.SetActive(ctx, id)
	}
	if repo, ok := f.quotaRepo.(domain.FamilyActiveRepository); ok {
		return repo.SetFamilyActive(ctx, family, id)
	}
	if f.familyActive == nil {
		f.familyActive = make(map[domain.QuotaFamily]string)
	}
	f.familyActive[family] = id
	return nil
}
