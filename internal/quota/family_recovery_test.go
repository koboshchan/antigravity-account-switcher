package quota

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Muriel-Gasparini/antigravity-account-switcher/internal/domain"
)

func TestPollerFamilyRecoveryPreservesOtherWindows(t *testing.T) {
	ctx := context.Background()
	_, accounts, quotas := setupTestStore(t)
	now := time.Now().UTC()
	acc := &domain.Account{ID: "a", Email: "a@test.invalid", AccessToken: "test", TokenExpiry: now.Add(time.Hour), Status: domain.AccountStatusActive}
	if err := accounts.Create(ctx, acc); err != nil {
		t.Fatal(err)
	}
	healthy := false
	status := http.StatusForbidden
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fraction := "0"
		if healthy {
			fraction = "0.4"
		}
		_, _ = w.Write([]byte(`{"buckets":[{"bucketId":"gemini-5h","displayName":"Gemini Models (5h)","remainingFraction":1},{"bucketId":"gemini-weekly","displayName":"Gemini Models (weekly)","remainingFraction":` + fraction + `,"resetTime":"2099-01-01T00:00:00Z"},{"bucketId":"claude-weekly","displayName":"Claude and GPT models (weekly)","remainingFraction":0,"resetTime":"2099-01-01T00:00:00Z"}]}`))
	}))
	defer server.Close()
	p, err := NewPoller(accounts, quotas, WithBaseURL(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	initial := []*domain.QuotaBucket{{AccountID: "a", BucketID: "gemini-weekly", DisplayName: "Gemini Models (weekly)", RemainingFraction: 0, ResetTime: now.Add(time.Hour)}}
	if err := quotas.UpsertBuckets(ctx, initial); err != nil {
		t.Fatal(err)
	}
	for _, family := range []domain.QuotaFamily{domain.QuotaFamilyGemini, domain.QuotaFamilyClaudeGPT} {
		if err := quotas.SetFamilyCooldown(ctx, "a", family, now.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	assertCooldown := func(family domain.QuotaFamily, want bool) {
		t.Helper()
		reset, err := quotas.GetFamilyCooldown(ctx, "a", family)
		if err != nil {
			t.Fatal(err)
		}
		if reset.After(now) != want {
			t.Fatalf("%s cooldown reset=%v want blocked=%t", family, reset, want)
		}
	}
	for _, code := range []int{403, 429} {
		status = code
		if _, err := p.PollAccount(ctx, "a"); err == nil {
			t.Fatalf("%d invented successful quota response", code)
		}
		buckets, err := quotas.GetByAccountID(ctx, "a")
		if err != nil {
			t.Fatal(err)
		}
		if len(buckets) != 1 || buckets[0].RemainingFraction != 0 {
			t.Fatal("failed API response replaced real quota snapshot")
		}
		assertCooldown(domain.QuotaFamilyGemini, true)
	}
	status = http.StatusOK
	if _, err := p.PollAccount(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	assertCooldown(domain.QuotaFamilyGemini, true) // weekly zero beats burst full
	healthy = true
	if _, err := p.PollAccount(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	assertCooldown(domain.QuotaFamilyGemini, false)
	assertCooldown(domain.QuotaFamilyClaudeGPT, true)
	// Explicit polling must not revive a disabled or auth-error account.
	for _, state := range []domain.AccountStatus{domain.AccountStatusDisabled, domain.AccountStatusError} {
		_ = accounts.UpdateStatus(ctx, "a", state)
		_, _ = p.PollAccount(ctx, "a")
		after, _ := accounts.GetByID(ctx, "a")
		if after.Status != state {
			t.Fatalf("poll changed status %s to %s", state, after.Status)
		}
	}
}

func TestLiveArgonRecoveryIsIndependent(t *testing.T) {
	ctx := context.Background()
	_, accounts, quotas := setupTestStore(t)
	now := time.Now().UTC()
	_ = accounts.Create(ctx, &domain.Account{ID: "a", Email: "a@test.invalid", Status: domain.AccountStatusActive, AccessToken: "test", TokenExpiry: now.Add(time.Hour)})
	for _, family := range []domain.QuotaFamily{domain.QuotaFamilyArgon, domain.QuotaFamilyGemini, domain.QuotaFamilyClaudeGPT} {
		if err := quotas.SetFamilyCooldown(ctx, "a", family, now.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	withArgon := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if withArgon {
			_, _ = w.Write([]byte(`{"groups":[{"displayName":"Gemini 4 Argon","buckets":[{"bucketId":"independent-5h","window":"5h","remainingFraction":0.5},{"bucketId":"independent-weekly","window":"weekly","remainingFraction":0.2}]}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"buckets":[{"bucketId":"gemini-weekly","remainingFraction":1}]}`))
	}))
	defer server.Close()
	p, err := NewPoller(accounts, quotas, WithBaseURL(server.URL))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.PollAccount(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	argon, _ := quotas.GetFamilyCooldown(ctx, "a", domain.QuotaFamilyArgon)
	if !argon.After(now) {
		t.Fatal("generic Gemini cleared Argon cooldown")
	}
	withArgon = true
	if _, err := p.PollAccount(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	argon, _ = quotas.GetFamilyCooldown(ctx, "a", domain.QuotaFamilyArgon)
	if argon.After(now) {
		t.Fatal("fresh Argon-specific evidence did not clear Argon cooldown")
	}
	claude, _ := quotas.GetFamilyCooldown(ctx, "a", domain.QuotaFamilyClaudeGPT)
	if !claude.After(now) {
		t.Fatal("Argon recovered Claude")
	}
	buckets, _ := quotas.GetByAccountID(ctx, "a")
	for _, b := range buckets {
		if domain.BucketFamily(b) != domain.QuotaFamilyArgon {
			t.Fatalf("Argon group lost family: %+v", b)
		}
	}
	if known, _ := domain.FamilyQuota(buckets, domain.QuotaFamilyGemini, now); known {
		t.Fatal("Argon projected into Gemini")
	}
}
