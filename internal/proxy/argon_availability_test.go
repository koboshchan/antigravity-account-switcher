package proxy

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Muriel-Gasparini/antigravity-account-switcher/internal/domain"
	"github.com/Muriel-Gasparini/antigravity-account-switcher/internal/quota"
	"github.com/Muriel-Gasparini/antigravity-account-switcher/internal/store/sqlite"
)

func TestThreeFamilies429Isolation(t *testing.T) {
	models := map[domain.QuotaFamily]string{domain.QuotaFamilyArgon: "gemini-4-argon-trial", domain.QuotaFamilyGemini: "gemini-3.8-flash-high", domain.QuotaFamilyClaudeGPT: "claude-sonnet-5"}
	for failedFamily, failedModel := range models {
		t.Run(string(failedFamily), func(t *testing.T) {
			ctx := context.Background()
			db, accounts, _, _ := setupTestDB(t)
			quotas := sqlite.NewQuotaRepository(db)
			for _, id := range []string{"a", "b"} {
				if err := accounts.Create(ctx, &domain.Account{ID: id, Email: id + "@test.invalid", Status: domain.AccountStatusActive, IsActive: id == "a"}); err != nil {
					t.Fatal(err)
				}
				for family := range models {
					if err := quotas.UpsertBuckets(ctx, []*domain.QuotaBucket{{AccountID: id, BucketID: string(family) + "-weekly", RemainingFraction: 1}}); err != nil {
						t.Fatal(err)
					}
				}
			}
			engine := NewFailoverEngine(accounts, nil, nil, WithQuotaRepository(quotas))
			a, _ := accounts.GetByID(ctx, "a")
			_, _, next, err := engine.HandleExhaustion(ctx, a, failedModel)
			if err != nil || next == nil || next.ID != "b" {
				t.Fatalf("rotation next=%v err=%v", next, err)
			}
			engine = NewFailoverEngine(accounts, nil, nil, WithQuotaRepository(quotas))
			for family, model := range models {
				_ = accounts.SetActive(ctx, "a")
				got, err := engine.ResolveForModel(ctx, model)
				want := "a"
				if family == failedFamily {
					want = "b"
				}
				if err != nil || got == nil || got.ID != want {
					t.Fatalf("failed=%s requested=%s got=%v err=%v want=%s", failedFamily, family, got, err, want)
				}
			}
			_ = quotas.SetFamilyCooldown(ctx, "a", failedFamily, time.Now().Add(-time.Second))
			_ = quotas.UpsertBuckets(ctx, []*domain.QuotaBucket{{AccountID: "b", BucketID: string(failedFamily) + "-weekly", RemainingFraction: 0}})
			got, err := engine.ResolveForModel(ctx, failedModel)
			if err != nil || got == nil || got.ID != "a" {
				t.Fatalf("expired cooldown got=%v err=%v", got, err)
			}
		})
	}
}

func TestQuota403BlocksEveryFamilyUntilLaterSuccessfulPoll(t *testing.T) {
	ctx := context.Background()
	db, accounts, _, _ := setupTestDB(t)
	quotas := sqlite.NewQuotaRepository(db)
	now := time.Now()
	for _, id := range []string{"a", "b"} {
		if err := accounts.Create(ctx, &domain.Account{ID: id, Email: id + "@test.invalid", Status: domain.AccountStatusActive, IsActive: id == "a", AccessToken: "test", TokenExpiry: now.Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
		for _, family := range []string{"gemini", "argon", "claude"} {
			if err := quotas.UpsertBuckets(ctx, []*domain.QuotaBucket{{AccountID: id, BucketID: family + "-weekly", RemainingFraction: 1}}); err != nil {
				t.Fatal(err)
			}
		}
	}
	status := 403
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status != 200 {
			w.WriteHeader(status)
			return
		}
		fmt.Fprint(w, `{"buckets":[{"bucketId":"gemini-weekly","remainingFraction":0.7},{"bucketId":"gemini-4-argon-weekly","remainingFraction":0.4},{"bucketId":"claude-weekly","remainingFraction":0.9}]}`)
	}))
	defer server.Close()
	poller, err := quota.NewPoller(accounts, quotas, quota.WithBaseURL(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := poller.PollAccount(ctx, "a"); err == nil {
		t.Fatal("403 reported success")
	}
	cached, _ := quotas.GetByAccountID(ctx, "a")
	if len(cached) != 3 {
		t.Fatal("internal evidence not preserved")
	}
	models := []string{"gemini-3.8-flash-high", "argon-trial", "gemini-4-argon-trial", "claude-sonnet-5", "unknown-custom", ""}
	for _, fallback := range []bool{false, true} {
		engine := NewFailoverEngine(accounts, nil, nil, WithQuotaRepository(sqlite.NewQuotaRepository(db)))
		for _, model := range models {
			_ = accounts.SetActive(ctx, "a")
			got, err := engine.ResolveForModel(ctx, model)
			if err != nil || got == nil || got.ID != "b" {
				t.Fatalf("403 selected blocked row model=%q fallback=%t got=%v err=%v", model, fallback, got, err)
			}
		}
		// Anti-stampede may not adopt the quota-unavailable current account.
		_ = accounts.SetActive(ctx, "a")
		b, _ := accounts.GetByID(ctx, "b")
		next, err := engine.RotateAccount(ctx, b)
		if err == nil || next != nil {
			t.Fatalf("legacy rotation adopted unavailable: %v %v", next, err)
		}
		_ = accounts.UpdateStatus(ctx, "b", domain.AccountStatusActive)
	}
	status = 500
	_, _ = poller.PollAccount(ctx, "a")
	if blocked, _ := quotas.IsQuotaUnavailable(ctx, "a"); !blocked {
		t.Fatal("500 cleared unavailable")
	}
	status = 200
	if _, err := poller.PollAccount(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if blocked, _ := quotas.IsQuotaUnavailable(ctx, "a"); blocked {
		t.Fatal("successful poll did not recover")
	}
	_ = accounts.UpdateStatus(ctx, "b", domain.AccountStatusDisabled)
	engine := NewFailoverEngine(accounts, nil, nil, WithQuotaRepository(quotas))
	for _, model := range models {
		_ = accounts.SetActive(ctx, "a")
		got, err := engine.ResolveForModel(ctx, model)
		if err != nil || got == nil || got.ID != "a" {
			t.Fatalf("successful poll did not recover %q: %v %v", model, got, err)
		}
	}
	for _, state := range []domain.AccountStatus{domain.AccountStatusDisabled, domain.AccountStatusError} {
		_ = accounts.UpdateStatus(ctx, "a", state)
		_, _ = poller.PollAccount(ctx, "a")
		after, _ := accounts.GetByID(ctx, "a")
		if after.Status != state {
			t.Fatal("quota poll reenabled manual/auth state")
		}
	}
}

func TestPositiveLowQuotaRemainsEligible(t *testing.T) {
	ctx := context.Background()
	db, accounts, _, _ := setupTestDB(t)
	quotas := sqlite.NewQuotaRepository(db)
	_ = accounts.Create(ctx, &domain.Account{ID: "a", Email: "a@test.invalid", Status: domain.AccountStatusExhausted, IsActive: true})
	for _, fraction := range []float64{0.01, 0.02, 0.03, 0.04} {
		for _, family := range []string{"argon", "gemini", "claude"} {
			if err := quotas.UpsertBuckets(ctx, []*domain.QuotaBucket{{AccountID: "a", BucketID: family + "-weekly", RemainingFraction: fraction}}); err != nil {
				t.Fatal(err)
			}
		}
		engine := NewFailoverEngine(accounts, nil, nil, WithQuotaRepository(quotas))
		for _, model := range []string{"argon-trial", "gemini-3.8-flash-high", "claude-sonnet-5"} {
			got, err := engine.ResolveForModel(ctx, model)
			if err != nil || got == nil || got.ID != "a" {
				t.Fatalf("positive %.2f rejected for %s: %v %v", fraction, model, got, err)
			}
		}
	}
}
