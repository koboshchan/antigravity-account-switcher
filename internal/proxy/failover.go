package proxy

import (
	"bytes"
	"context"
	"fmt"
	"github.com/Muriel-Gasparini/antigravity-account-switcher/internal/domain"
	"net/http"
	"sync"
	"time"
)

type FailoverAction int

const (
	ActionNone FailoverAction = iota
	ActionRotateAccount
)

func (a FailoverAction) String() string {
	if a == ActionRotateAccount {
		return "ActionRotateAccount"
	}
	return "ActionNone"
}

type FailoverOption func(*FailoverEngine)

func WithQuotaRepository(repo domain.QuotaRepository) FailoverOption {
	return func(f *FailoverEngine) { f.quotaRepo = repo }
}

type FailoverEngine struct {
	mu               sync.RWMutex
	accountRepo      domain.AccountRepository
	quotaRepo        domain.QuotaRepository
	eventBroadcaster domain.EventBroadcaster
	eventRepo        domain.EventRepository
	familyCooldowns  map[string]map[domain.QuotaFamily]time.Time
	familyActive     map[domain.QuotaFamily]string
}

func NewFailoverEngine(accounts domain.AccountRepository, events domain.EventBroadcaster, repo domain.EventRepository, opts ...FailoverOption) *FailoverEngine {
	f := &FailoverEngine{accountRepo: accounts, eventBroadcaster: events, eventRepo: repo}
	for _, opt := range opts {
		opt(f)
	}
	return f
}
func (f *FailoverEngine) SetQuotaRepository(repo domain.QuotaRepository) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.quotaRepo = repo
}
func (f *FailoverEngine) HandleExhaustion(ctx context.Context, acc *domain.Account, model string) (FailoverAction, string, *domain.Account, error) {
	if acc == nil {
		return ActionNone, model, nil, domain.ErrAccountNotFound
	}
	if err := ctx.Err(); err != nil {
		return ActionNone, model, nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var next *domain.Account
	var err error
	if modelFamily(model) != "" {
		next, err = f.rotateFamilyLocked(ctx, acc, model)
	} else {
		next, err = f.rotateAccountLocked(ctx, acc, nil)
	}
	return ActionRotateAccount, model, next, err
}

func IsExhaustionResponse(statusCode int, body []byte) bool {
	if statusCode == http.StatusTooManyRequests {
		return true
	}
	if statusCode == http.StatusForbidden && len(body) > 0 {
		return bytes.Contains(body, []byte("RESOURCE_EXHAUSTED")) ||
			bytes.Contains(body, []byte("RATE_LIMIT_EXCEEDED")) ||
			bytes.Contains(body, []byte("QuotaFailure"))
	}
	return false
}

// cachedQuotaBucket represents a normalized in-memory quota bucket snapshot.
type cachedQuotaBucket struct {
	category          ModelCategory
	bucketIDLower     string
	displayNameLower  string
	modelNameLower    string
	hasPro            bool
	hasFlash          bool
	remainingFraction float64
	remainingAmount   int64
	resetTime         time.Time
}

func (f *FailoverEngine) RotateAccount(ctx context.Context, exhaustedAcc *domain.Account) (*domain.Account, error) {
	if exhaustedAcc == nil {
		return nil, domain.ErrAccountNotFound
	}

	var eventsToEmit []*domain.ProxyEvent
	defer func() {
		for _, ev := range eventsToEmit {
			f.emitEvent(ev)
		}
	}()

	f.mu.Lock()
	defer f.mu.Unlock()

	return f.rotateAccountLocked(ctx, exhaustedAcc, &eventsToEmit)
}

func (f *FailoverEngine) rotateAccountLocked(
	ctx context.Context,
	exhaustedAcc *domain.Account,
	eventsToEmit *[]*domain.ProxyEvent,
) (*domain.Account, error) {
	emit := func(ev *domain.ProxyEvent) {
		if eventsToEmit != nil {
			*eventsToEmit = append(*eventsToEmit, ev)
		} else {
			f.emitEvent(ev)
		}
	}

	// 1. Anti-stampede check: Did another concurrent request already rotate away from exhaustedAcc?
	currentActive, err := f.accountRepo.GetActive(ctx)
	if err == nil && currentActive != nil && currentActive.ID != exhaustedAcc.ID && currentActive.IsAvailable() && f.quotaEndpointAvailableLocked(ctx, currentActive) {

		return currentActive, nil
	}

	// 2. Mark the exhausted account in the repository
	if err := f.accountRepo.UpdateStatus(ctx, exhaustedAcc.ID, domain.AccountStatusExhausted); err != nil {
		emit(&domain.ProxyEvent{
			Type:      domain.EventTypeError,
			AccountID: exhaustedAcc.ID,
			Message:   fmt.Sprintf("Failed to update status for exhausted account %s: %v", exhaustedAcc.Email, err),
			Timestamp: time.Now().UTC(),
		})
	}

	// Broadcast EventTypeFailover429
	emit(&domain.ProxyEvent{
		Type:      domain.EventTypeFailover429,
		AccountID: exhaustedAcc.ID,
		Message:   fmt.Sprintf("Account %s (%s) marked exhausted due to HTTP 429 / RESOURCE_EXHAUSTED", exhaustedAcc.Email, exhaustedAcc.ID),
		Details: map[string]any{
			"account_id": exhaustedAcc.ID,
			"email":      exhaustedAcc.Email,
			"reason":     "RESOURCE_EXHAUSTED",
		},
		Timestamp: time.Now().UTC(),
	})

	// 3. Find next available account
	nextAcc, err := f.accountRepo.GetNextAvailable(ctx, exhaustedAcc.ID)
	if err != nil || nextAcc == nil {
		// Pool is completely exhausted
		emit(&domain.ProxyEvent{
			Type:      domain.EventTypeQuotaExhausted,
			AccountID: exhaustedAcc.ID,
			Message:   "All accounts in the pool are exhausted",
			Details: map[string]any{
				"last_account_id": exhaustedAcc.ID,
				"last_email":      exhaustedAcc.Email,
			},
			Timestamp: time.Now().UTC(),
		})
		return nil, domain.ErrNoAvailableAccount
	}

	if !f.quotaEndpointAvailableLocked(ctx, nextAcc) {
		candidates, listErr := f.accountRepo.List(ctx)
		if listErr != nil {
			return nil, listErr
		}
		nextAcc = nil
		for _, candidate := range candidates {
			if candidate.ID != exhaustedAcc.ID && candidate.IsAvailable() && f.quotaEndpointAvailableLocked(ctx, candidate) {
				nextAcc = candidate
				break
			}
		}
		if nextAcc == nil {
			return nil, domain.ErrNoAvailableAccount
		}
	}
	// 4. Set next account active atomically
	if err := f.accountRepo.SetActive(ctx, nextAcc.ID); err != nil {
		return nil, fmt.Errorf("failed to set active account to %s: %w", nextAcc.ID, err)
	}

	// Broadcast EventTypeAccountSwitched
	emit(&domain.ProxyEvent{
		Type:      domain.EventTypeAccountSwitched,
		AccountID: nextAcc.ID,
		Message:   fmt.Sprintf("Rotated active account from %s to %s", exhaustedAcc.Email, nextAcc.Email),
		Details: map[string]any{
			"from_account_id": exhaustedAcc.ID,
			"from_email":      exhaustedAcc.Email,
			"to_account_id":   nextAcc.ID,
			"to_email":        nextAcc.Email,
		},
		Timestamp: time.Now().UTC(),
	})

	return nextAcc, nil
}

func (f *FailoverEngine) emitEvent(event *domain.ProxyEvent) {
	if f.eventBroadcaster == nil && f.eventRepo == nil {
		return
	}
	if f.eventBroadcaster != nil {
		f.eventBroadcaster.Broadcast(event)
	}
	if f.eventRepo != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = f.eventRepo.Record(ctx, event)
	}
}
