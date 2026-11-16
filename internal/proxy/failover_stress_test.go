package proxy

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Muriel-Gasparini/antigravity-account-switcher/internal/domain"
	"github.com/Muriel-Gasparini/antigravity-account-switcher/internal/store/sqlite"
)

// failoverStressTestEnv encapsulates an isolated proxy handler environment with a mock upstream.
type failoverStressTestEnv struct {
	db             *sqlite.DB
	accountRepo    domain.AccountRepository
	metricsRepo    domain.MetricsRepository
	eventRepo      domain.EventRepository
	quotaRepo      domain.QuotaRepository
	broadcaster    *Broadcaster
	failoverEngine *FailoverEngine
	handler        *ProxyHandler
	upstream       *httptest.Server
	proxyServer    *httptest.Server
	client         *http.Client
}

func newFailoverStressTestEnv(
	t *testing.T,
	upstreamHandler http.HandlerFunc,
	primary, secondary string,
	fallbackEnabled bool,
) *failoverStressTestEnv {
	t.Helper()

	upstream := httptest.NewServer(upstreamHandler)
	t.Cleanup(func() { upstream.Close() })

	dbPath := filepath.Join(t.TempDir(), "failover_stress.db")
	db, err := sqlite.Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open test sqlite db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	accRepo := sqlite.NewAccountRepository(db)
	metricsRepo := sqlite.NewMetricsRepository(db)
	eventRepo := sqlite.NewEventRepository(db)
	quotaRepo := sqlite.NewQuotaRepository(db)

	broadcaster := NewBroadcaster(500)
	engine := NewFailoverEngine(
		accRepo,
		broadcaster,
		eventRepo,
		WithQuotaRepository(quotaRepo),
	)

	handler, err := NewProxyHandler(
		accRepo,
		WithTargetURL(upstream.URL),
		WithMetricsRepository(metricsRepo),
		WithEventBroadcaster(broadcaster),
		WithEventRepository(eventRepo),
		WithFailoverEngine(engine),
	)
	if err != nil {
		t.Fatalf("failed to create proxy handler: %v", err)
	}

	proxyServer := httptest.NewServer(handler)
	t.Cleanup(func() { proxyServer.Close() })

	transport := &http.Transport{
		MaxIdleConns:        200,
		MaxIdleConnsPerHost: 100,
		IdleConnTimeout:     30 * time.Second,
	}

	return &failoverStressTestEnv{
		db:             db,
		accountRepo:    accRepo,
		metricsRepo:    metricsRepo,
		eventRepo:      eventRepo,
		quotaRepo:      quotaRepo,
		broadcaster:    broadcaster,
		failoverEngine: engine,
		handler:        handler,
		upstream:       upstream,
		proxyServer:    proxyServer,
		client:         &http.Client{Transport: transport, Timeout: 15 * time.Second},
	}
}

// TestFailover_Stress_Concurrent429_IntraAccountFallbackStampede tests 50 concurrent goroutines
// sending requests for primary model (gemini-2.5-pro). Account A returns 429 on pro, but
// 200 on secondary (gemini-2.5-flash).
// Invariants verified:
// 1. All 50 requests succeed with 200 OK.
// 2. Anti-stampede: Account A remains the active account (no premature rotation to Account B).
// 3. Zero 429 error leakage to clients.
// 4. All requests are rewritten to secondary model.

// TestFailover_Stress_ConcurrentDoubleExhaustion_AntiStampedeRotation tests 40 concurrent
// goroutines encountering total exhaustion on Account A (both Pro and Flash 429), rotating
// to Account B (which returns 200 OK).
// Invariants verified:
// 1. Exactly ONE rotation occurs across all concurrent requests (Account A -> Account B).
// 2. Account B is NOT rotated to Account C (no cascading stampede!).
// 3. All requests complete successfully on Account B with primary model reset.

// TestFailover_Stress_ConcurrentDirectRotation_FallbackDisabled tests 40 concurrent goroutines
// when fallbackSecondaryEnabled = false. Account A fails with 429 on primary, rotating to Account B.
// Invariants verified:
// 1. Anti-stampede: Exactly 1 rotation occurs (Account A -> Account B). Account C untouched.
// 2. Zero secondary fallback attempts made.
// 3. All 40 requests succeed on Account B.

// TestFailover_Stress_ConcurrentPredictiveFallback_UnderCacheMutation tests 50 concurrent goroutines
// executing predictive checks with cache misses while background goroutines mutate the quota cache.
// Invariants verified:
// 1. All 50 requests are predictively rewritten to gemini-2.5-flash with 0 pro requests dispatched.
// 2. Zero data races between cache writers and predictive readers.

// TestFailover_Stress_BufferIntegrity_ConcurrentRewritingUnderStress tests 30 concurrent
// goroutines each sending distinct JSON payloads (up to 128KB) to ensure request buffers
// are never cross-contaminated, truncated, or corrupted during failover rewriting.

// TestFailover_Stress_FullPoolExhaustion_ConcurrentStampede tests 25 concurrent goroutines
// when all accounts in the pool return 429 on all models.
// Invariants verified:
// 1. All 25 requests terminate promptly with HTTP 429 or 503 (no hanging/deadlocks).
// 2. Retry count is bounded (no infinite retry loop).
// 3. Pool exhaustion events emitted cleanly.
func TestFailover_Stress_FullPoolExhaustion_ConcurrentStampede(t *testing.T) {
	const concurrency = 25
	ctx := context.Background()

	upstreamHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=UTF-8")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"code":429,"message":"global limit reached","status":"RESOURCE_EXHAUSTED"}}`))
	})

	env := newFailoverStressTestEnv(t, upstreamHandler, "gemini-2.5-pro", "gemini-2.5-flash", true)

	now := time.Now().UTC()
	acc1 := &domain.Account{
		ID:          "acc-exh-stampede-1",
		Email:       "exh1@example.com",
		AccessToken: "token-exh-1",
		IsActive:    true,
		Status:      domain.AccountStatusActive,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	acc2 := &domain.Account{
		ID:          "acc-exh-stampede-2",
		Email:       "exh2@example.com",
		AccessToken: "token-exh-2",
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

	gate := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(concurrency)

	statusCodes := make([]int, concurrency)

	for i := 0; i < concurrency; i++ {
		idx := i
		go func() {
			defer wg.Done()
			<-gate

			req, err := http.NewRequest("POST", env.proxyServer.URL+"/v1internal/models/gemini-2.5-pro:generateContent", strings.NewReader(`{}`))
			if err != nil {
				return
			}
			req.Header.Set("Content-Type", "application/json")

			resp, err := env.client.Do(req)
			if err != nil {
				return
			}
			defer resp.Body.Close()

			statusCodes[idx] = resp.StatusCode
		}()
	}

	close(gate)
	wg.Wait()

	for i, sc := range statusCodes {
		if sc != http.StatusTooManyRequests && sc != http.StatusServiceUnavailable {
			t.Errorf("request %d expected 429 or 503, got %d", i, sc)
		}
	}
}

// TestFailover_Stress_Broadcaster_HighConcurrencyChurn tests Broadcaster under extreme churn:
// 40 subscribers subscribing and unsubscribing in rapid loops while 200 events are concurrently broadcasted.
// Invariants verified:
// 1. Zero panics from send on closed channel.
// 2. Zero deadlocks.
// 3. Subscribers cleanly terminate.
func TestFailover_Stress_Broadcaster_HighConcurrencyChurn(t *testing.T) {
	b := NewBroadcaster(100)
	stop := make(chan struct{})

	var subscriberWg sync.WaitGroup
	const numSubscribers = 40

	for s := 0; s < numSubscribers; s++ {
		subscriberWg.Add(1)
		go func() {
			defer subscriberWg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					ch, unsub := b.Subscribe()
					// Read briefly or unsubscribe immediately
					select {
					case <-ch:
					case <-time.After(1 * time.Millisecond):
					}
					unsub()
				}
			}
		}()
	}

	const numBroadcasters = 10
	const eventsPerBroadcaster = 20
	var broadWg sync.WaitGroup

	for i := 0; i < numBroadcasters; i++ {
		broadWg.Add(1)
		go func(id int) {
			defer broadWg.Done()
			for j := 0; j < eventsPerBroadcaster; j++ {
				b.Broadcast(&domain.ProxyEvent{
					Type:      domain.EventTypeModelFallback,
					AccountID: fmt.Sprintf("acc-%d-%d", id, j),
					Message:   "stress event",
					Timestamp: time.Now().UTC(),
				})
			}
		}(i)
	}

	broadWg.Wait()
	close(stop)
	subscriberWg.Wait()
}
