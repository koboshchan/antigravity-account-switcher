package proxy

import (
	"context"
	"github.com/Muriel-Gasparini/antigravity-account-switcher/internal/domain"
	"github.com/Muriel-Gasparini/antigravity-account-switcher/internal/store/sqlite"
	"testing"
	"time"
)

func TestUltraArgonCarbonUseGeminiCapacityAndCooldown(t *testing.T) {
	for _, model := range []string{"argon", "gemini-4-argon-high", "carbon", "gemini-4-carbon"} {
		t.Run(model, func(t *testing.T) {
			ctx := context.Background()
			db, accounts, _, _ := setupTestDB(t)
			quotas := sqlite.NewQuotaRepository(db)
			for _, id := range []string{"a", "b"} {
				if err := accounts.Create(ctx, &domain.Account{ID: id, Email: id + "@test.invalid", Status: domain.AccountStatusActive, IsActive: id == "a"}); err != nil {
					t.Fatal(err)
				}
				if err := quotas.UpsertBuckets(ctx, []*domain.QuotaBucket{{AccountID: id, BucketID: "gemini-5h", RemainingFraction: 0.8}, {AccountID: id, BucketID: "gemini-weekly", RemainingFraction: 0.3}}); err != nil {
					t.Fatal(err)
				}
				if err := quotas.SetSharedGemini(ctx, id, true); err != nil {
					t.Fatal(err)
				}
			}
			engine := NewFailoverEngine(accounts, nil, nil, WithQuotaRepository(quotas))
			a, err := engine.ResolveForModel(ctx, model)
			if err != nil || a.ID != "a" {
				t.Fatalf("resolve %v %v", a, err)
			}
			_, _, next, err := engine.HandleExhaustion(ctx, a, model)
			if err != nil || next.ID != "b" {
				t.Fatalf("rotation %v %v", next, err)
			}
			reset, err := quotas.GetFamilyCooldown(ctx, "a", domain.QuotaFamilyGemini)
			if err != nil || !reset.After(time.Now()) {
				t.Fatal("shared cooldown missing", err)
			}
			reset, _ = quotas.GetFamilyCooldown(ctx, "a", domain.QuotaFamilyArgon)
			if !reset.IsZero() {
				t.Fatal("independent cooldown fabricated")
			}
			for _, m := range []string{"gemini-3.8-flash-high", "argon", "carbon"} {
				got, err := engine.ResolveForModel(ctx, m)
				if err != nil || got.ID != "b" {
					t.Fatalf("%s reused shared exhausted account %v %v", m, got, err)
				}
			}
			// Weekly exhaustion blocks even when burst quota is healthy.
			_ = quotas.UpsertBuckets(ctx, []*domain.QuotaBucket{{AccountID: "b", BucketID: "gemini-weekly", RemainingFraction: 0}})
			if got, err := engine.ResolveForModel(ctx, model); err == nil || got != nil {
				t.Fatal("weekly exhausted pool eligible")
			}
		})
	}
}
