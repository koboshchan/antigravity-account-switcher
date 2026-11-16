package proxy

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/Muriel-Gasparini/antigravity-account-switcher/internal/domain"
)

type mockAccountRepo struct {
	mu       sync.Mutex
	accounts map[string]*domain.Account
}

func newMockAccountRepo() *mockAccountRepo {
	return &mockAccountRepo{
		accounts: make(map[string]*domain.Account),
	}
}

func (m *mockAccountRepo) addAccount(acc *domain.Account) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.accounts[acc.ID] = acc
}

func (m *mockAccountRepo) Create(ctx context.Context, acc *domain.Account) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.accounts[acc.ID] = acc
	return nil
}

func (m *mockAccountRepo) GetByID(ctx context.Context, id string) (*domain.Account, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	acc, ok := m.accounts[id]
	if !ok {
		return nil, domain.ErrAccountNotFound
	}
	cp := *acc
	return &cp, nil
}

func (m *mockAccountRepo) GetByEmail(ctx context.Context, email string) (*domain.Account, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, acc := range m.accounts {
		if acc.Email == email {
			cp := *acc
			return &cp, nil
		}
	}
	return nil, domain.ErrAccountNotFound
}

func (m *mockAccountRepo) GetActive(ctx context.Context) (*domain.Account, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, acc := range m.accounts {
		if acc.IsActive {
			cp := *acc
			return &cp, nil
		}
	}
	return nil, domain.ErrNoActiveAccount
}

func (m *mockAccountRepo) List(ctx context.Context) ([]*domain.Account, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var list []*domain.Account
	for _, acc := range m.accounts {
		cp := *acc
		list = append(list, &cp)
	}
	return list, nil
}

func (m *mockAccountRepo) SetActive(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	target, ok := m.accounts[id]
	if !ok {
		return domain.ErrAccountNotFound
	}
	for _, acc := range m.accounts {
		acc.IsActive = false
	}
	target.IsActive = true
	target.Status = domain.AccountStatusActive
	target.UpdatedAt = time.Now().UTC()
	return nil
}

func (m *mockAccountRepo) UpdateStatus(ctx context.Context, id string, status domain.AccountStatus) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	acc, ok := m.accounts[id]
	if !ok {
		return domain.ErrAccountNotFound
	}
	acc.Status = status
	acc.UpdatedAt = time.Now().UTC()
	return nil
}

func (m *mockAccountRepo) UpdateToken(ctx context.Context, id string, accessToken string, expiry time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	acc, ok := m.accounts[id]
	if !ok {
		return domain.ErrAccountNotFound
	}
	acc.AccessToken = accessToken
	acc.TokenExpiry = expiry
	return nil
}

func (m *mockAccountRepo) UpdateRefreshToken(ctx context.Context, id string, refreshToken string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	acc, ok := m.accounts[id]
	if !ok {
		return domain.ErrAccountNotFound
	}
	acc.RefreshToken = refreshToken
	return nil
}

func (m *mockAccountRepo) Delete(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.accounts, id)
	return nil
}

func (m *mockAccountRepo) GetNextAvailable(ctx context.Context, excludeID string) (*domain.Account, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var best *domain.Account
	for _, acc := range m.accounts {
		if acc.ID == excludeID {
			continue
		}
		if acc.Status == domain.AccountStatusActive {
			if best == nil || acc.UpdatedAt.Before(best.UpdatedAt) {
				best = acc
			}
		}
	}
	if best == nil {
		return nil, domain.ErrNoAvailableAccount
	}
	cp := *best
	return &cp, nil
}

type mockEventRepo struct {
	mu     sync.Mutex
	events []*domain.ProxyEvent
}

func (m *mockEventRepo) Record(ctx context.Context, event *domain.ProxyEvent) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events = append(m.events, event)
	return nil
}

func (m *mockEventRepo) ListRecent(ctx context.Context, limit int) ([]*domain.ProxyEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.events, nil
}

type mockQuotaRepo struct {
	mu      sync.Mutex
	buckets map[string][]*domain.QuotaBucket
}

func newMockQuotaRepo() *mockQuotaRepo {
	return &mockQuotaRepo{
		buckets: make(map[string][]*domain.QuotaBucket),
	}
}

func (m *mockQuotaRepo) setBuckets(accountID string, buckets []*domain.QuotaBucket) {
	m.mu.Lock()
	defer m.mu.Unlock()
	res := make([]*domain.QuotaBucket, len(buckets))
	for i, b := range buckets {
		cp := *b
		res[i] = &cp
	}
	m.buckets[accountID] = res
}

func (m *mockQuotaRepo) UpsertBuckets(ctx context.Context, buckets []*domain.QuotaBucket) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, b := range buckets {
		cp := *b
		existing := m.buckets[b.AccountID]
		found := false
		for i, eb := range existing {
			if eb.BucketID == b.BucketID {
				existing[i] = &cp
				found = true
				break
			}
		}
		if !found {
			m.buckets[b.AccountID] = append(m.buckets[b.AccountID], &cp)
		}
	}
	return nil
}

func (m *mockQuotaRepo) GetByAccountID(ctx context.Context, accountID string) ([]*domain.QuotaBucket, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	list, ok := m.buckets[accountID]
	if !ok {
		return nil, nil
	}
	res := make([]*domain.QuotaBucket, len(list))
	for i, b := range list {
		cp := *b
		res[i] = &cp
	}
	return res, nil
}

func (m *mockQuotaRepo) ListAll(ctx context.Context) (map[string][]*domain.QuotaBucket, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	res := make(map[string][]*domain.QuotaBucket)
	for k, v := range m.buckets {
		res[k] = append([]*domain.QuotaBucket(nil), v...)
	}
	return res, nil
}

func (m *mockQuotaRepo) DeleteByAccountID(ctx context.Context, accountID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.buckets, accountID)
	return nil
}

func (m *mockQuotaRepo) GetExhaustedAccountsPastReset(ctx context.Context, now time.Time) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return nil, nil
}

func TestIsExhaustionResponse(t *testing.T) {
	cases := []struct {
		statusCode int
		body       string
		expected   bool
	}{
		{http.StatusTooManyRequests, "", true},
		{http.StatusTooManyRequests, `{"error":{"status":"RESOURCE_EXHAUSTED"}}`, true},
		{http.StatusForbidden, `{"error":{"status":"RESOURCE_EXHAUSTED"}}`, true},
		{http.StatusForbidden, `{"error":{"reason":"RATE_LIMIT_EXCEEDED"}}`, true},
		{http.StatusForbidden, `{"error":{"details":[{"@type":"...QuotaFailure"}]}}`, true},
		{http.StatusForbidden, `{"error":{"status":"PERMISSION_DENIED"}}`, false},
		{http.StatusOK, "", false},
		{http.StatusBadRequest, `{"error":{"code":400}}`, false},
		{http.StatusInternalServerError, `{"error":{"code":500}}`, false},
		{http.StatusServiceUnavailable, `{"error":{"code":503}}`, false},
	}

	for _, c := range cases {
		got := IsExhaustionResponse(c.statusCode, []byte(c.body))
		if got != c.expected {
			t.Errorf("IsExhaustionResponse(%d, %q) = %v; want %v", c.statusCode, c.body, got, c.expected)
		}
	}
}

func TestFailoverEngine_SuccessfulRotation(t *testing.T) {
	repo := newMockAccountRepo()
	now := time.Now().UTC()
	accA := &domain.Account{
		ID:          "acc-A",
		Email:       "a@example.com",
		AccessToken: "token-A",
		IsActive:    true,
		Status:      domain.AccountStatusActive,
		UpdatedAt:   now.Add(-10 * time.Minute),
	}
	accB := &domain.Account{
		ID:          "acc-B",
		Email:       "b@example.com",
		AccessToken: "token-B",
		IsActive:    false,
		Status:      domain.AccountStatusActive,
		UpdatedAt:   now.Add(-5 * time.Minute),
	}
	repo.addAccount(accA)
	repo.addAccount(accB)

	broadcaster := NewBroadcaster(10)
	eventsCh, unsubscribe := broadcaster.Subscribe()
	defer unsubscribe()

	eventRepo := &mockEventRepo{}
	engine := NewFailoverEngine(repo, broadcaster, eventRepo)

	nextAcc, err := engine.RotateAccount(context.Background(), accA)
	if err != nil {
		t.Fatalf("expected successful rotation, got error: %v", err)
	}

	if nextAcc.ID != "acc-B" {
		t.Errorf("expected rotated account acc-B, got %s", nextAcc.ID)
	}

	// Verify Account A is marked exhausted
	updatedA, _ := repo.GetByID(context.Background(), "acc-A")
	if updatedA.Status != domain.AccountStatusExhausted {
		t.Errorf("expected acc-A exhausted, got %s", updatedA.Status)
	}

	// Verify Account B is active
	activeAcc, _ := repo.GetActive(context.Background())
	if activeAcc.ID != "acc-B" {
		t.Errorf("expected active account acc-B, got %s", activeAcc.ID)
	}

	// Verify events were broadcast
	var receivedEvents []*domain.ProxyEvent
	for i := 0; i < 2; i++ {
		select {
		case ev := <-eventsCh:
			receivedEvents = append(receivedEvents, ev)
		case <-time.After(1 * time.Second):
			t.Fatalf("timed out waiting for event %d", i+1)
		}
	}

	if receivedEvents[0].Type != domain.EventTypeFailover429 {
		t.Errorf("expected first event %s, got %s", domain.EventTypeFailover429, receivedEvents[0].Type)
	}
	if receivedEvents[1].Type != domain.EventTypeAccountSwitched {
		t.Errorf("expected second event %s, got %s", domain.EventTypeAccountSwitched, receivedEvents[1].Type)
	}
}

func TestFailoverEngine_PoolExhaustion(t *testing.T) {
	repo := newMockAccountRepo()
	accA := &domain.Account{
		ID:          "acc-sole",
		Email:       "sole@example.com",
		AccessToken: "token-sole",
		IsActive:    true,
		Status:      domain.AccountStatusActive,
		UpdatedAt:   time.Now().UTC(),
	}
	repo.addAccount(accA)

	broadcaster := NewBroadcaster(10)
	eventsCh, unsubscribe := broadcaster.Subscribe()
	defer unsubscribe()

	engine := NewFailoverEngine(repo, broadcaster, nil)

	nextAcc, err := engine.RotateAccount(context.Background(), accA)
	if !errors.Is(err, domain.ErrNoAvailableAccount) {
		t.Fatalf("expected ErrNoAvailableAccount, got %v (acc: %+v)", err, nextAcc)
	}

	// Verify accA marked exhausted
	updatedA, _ := repo.GetByID(context.Background(), "acc-sole")
	if updatedA.Status != domain.AccountStatusExhausted {
		t.Errorf("expected exhausted status, got %s", updatedA.Status)
	}

	// Verify EventTypeQuotaExhausted was broadcast
	var quotaExhaustedReceived bool
	for i := 0; i < 2; i++ {
		select {
		case ev := <-eventsCh:
			if ev.Type == domain.EventTypeQuotaExhausted {
				quotaExhaustedReceived = true
			}
		case <-time.After(1 * time.Second):
		}
	}
	if !quotaExhaustedReceived {
		t.Error("expected EventTypeQuotaExhausted event to be broadcast")
	}
}

func TestFailoverEngine_NilAccount(t *testing.T) {
	repo := newMockAccountRepo()
	engine := NewFailoverEngine(repo, nil, nil)

	_, err := engine.RotateAccount(context.Background(), nil)
	if !errors.Is(err, domain.ErrAccountNotFound) {
		t.Errorf("expected ErrAccountNotFound, got %v", err)
	}

	act, _, _, err2 := engine.HandleExhaustion(context.Background(), nil, "gemini-2.5-pro")
	if !errors.Is(err2, domain.ErrAccountNotFound) {
		t.Errorf("expected ErrAccountNotFound from HandleExhaustion, got %v", err2)
	}
	if act != ActionNone {
		t.Errorf("expected ActionNone for nil account, got %v", act)
	}
}
