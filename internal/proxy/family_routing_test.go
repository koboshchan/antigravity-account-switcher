package proxy

import (
	"context"
	"testing"
	"time"

	"github.com/Muriel-Gasparini/antigravity-account-switcher/internal/domain"
	"github.com/Muriel-Gasparini/antigravity-account-switcher/internal/store/sqlite"
)

func TestFamilyExhaustionIsolationAndPersistence(t *testing.T) {
	ctx := context.Background()
	db, accounts, _, _ := setupTestDB(t)
	quotas := sqlite.NewQuotaRepository(db)
	now := time.Now().UTC()
	for _, id := range []string{"a", "b"} {
		if err := accounts.Create(ctx, &domain.Account{ID: id, Email: id + "@test.invalid", Status: domain.AccountStatusActive, IsActive: id == "a", AccessToken: "test", TokenExpiry: now.Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
		if err := quotas.UpsertBuckets(ctx, []*domain.QuotaBucket{
			{AccountID: id, BucketID: "claude-5h", RemainingFraction: 1, ResetTime: now.Add(time.Hour)},
			{AccountID: id, BucketID: "claude-weekly", RemainingFraction: 1, ResetTime: now.Add(48 * time.Hour)},
			{AccountID: id, BucketID: "gemini-5h", RemainingFraction: 1, ResetTime: now.Add(time.Hour)},
			{AccountID: id, BucketID: "gemini-weekly", RemainingFraction: 1, ResetTime: now.Add(48 * time.Hour)},
		}); err != nil {
			t.Fatal(err)
		}
	}
	engine := NewFailoverEngine(accounts, nil, nil, WithQuotaRepository(quotas))
	a, _ := accounts.GetByID(ctx, "a")
	_, _, next, err := engine.HandleExhaustion(ctx, a, "claude-sonnet-5")
	if err != nil || next.ID != "b" {
		t.Fatalf("Claude rotation next=%v err=%v", next, err)
	}
	after, _ := accounts.GetByID(ctx, "a")
	if after.Status != domain.AccountStatusActive {
		t.Fatal("known-model exhaustion changed auth status")
	}
	// New engine must still honor the persisted Claude cooldown.
	engine = NewFailoverEngine(accounts, nil, nil, WithQuotaRepository(quotas))
	if err := accounts.SetActive(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	gemini, err := engine.ResolveForModel(ctx, "hydrogen")
	if err != nil || gemini.ID != "a" {
		t.Fatalf("Claude blocked Gemini next=%v err=%v", gemini, err)
	}
	claude, err := engine.ResolveForModel(ctx, "claude-sonnet-5")
	if err != nil || claude.ID != "b" {
		t.Fatalf("Claude cooldown not persistent next=%v err=%v", claude, err)
	}
	// Expired reset becomes eligible without deleting the observation first.
	if err := quotas.SetFamilyCooldown(ctx, "a", domain.QuotaFamilyClaudeGPT, now.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	_ = quotas.UpsertBuckets(ctx, []*domain.QuotaBucket{{AccountID: "b", BucketID: "claude-weekly", RemainingFraction: 0}})
	claude, err = engine.ResolveForModel(ctx, "claude-sonnet-5")
	if err != nil || claude.ID != "a" {
		t.Fatalf("expired cooldown remains sticky next=%v err=%v", claude, err)
	}
}

func TestFamilyAliasesAndUnknownSafety(t *testing.T) {
	for _, model := range []string{"hydrogen", "helium", "models/helium"} {
		if got := CategorizeModel(model); got != CategoryGemini {
			t.Errorf("%s categorized %v", model, got)
		}
	}
	for _, model := range []string{"argon", "Argon", "gemini-4-argon", "models/gemini-4-argon"} {
		if got := CategorizeModel(model); got != CategoryArgon {
			t.Errorf("%s categorized %v", model, got)
		}
	}
	ctx := context.Background()
	db, accounts, _, _ := setupTestDB(t)
	quotas := sqlite.NewQuotaRepository(db)
	if err := accounts.Create(ctx, &domain.Account{ID: "a", Email: "a@test.invalid", Status: domain.AccountStatusExhausted, IsActive: true}); err != nil {
		t.Fatal(err)
	}
	_ = quotas.UpsertBuckets(ctx, []*domain.QuotaBucket{{AccountID: "a", BucketID: "gemini-5h", RemainingFraction: 1}})
	engine := NewFailoverEngine(accounts, nil, nil, WithQuotaRepository(quotas))
	if _, err := engine.ResolveForModel(ctx, "custom-unrecognized"); err == nil {
		t.Fatal("unknown model treated as recovered Gemini")
	}
	for _, status := range []domain.AccountStatus{domain.AccountStatusError, domain.AccountStatusDisabled} {
		_ = accounts.UpdateStatus(ctx, "a", status)
		for _, model := range []string{"gemini-3.8-flash", "claude-sonnet-5", ""} {
			if _, err := engine.ResolveForModel(ctx, model); err == nil {
				t.Errorf("%s routed with status %s", model, status)
			}
		}
	}
}
