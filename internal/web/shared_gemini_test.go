package web

import (
	"context"
	"github.com/Muriel-Gasparini/antigravity-account-switcher/internal/domain"
	"testing"
	"time"
)

func TestUltraViewUsesGeminiPoolWithoutInventingBuckets(t *testing.T) {
	ctx := context.Background()
	_, accounts, quotas, _, _, _, _ := setupTestWeb(t)
	acc := &domain.Account{ID: "ultra", Email: "ultra@test.invalid", Status: domain.AccountStatusActive}
	if err := accounts.Create(ctx, acc); err != nil {
		t.Fatal(err)
	}
	topology := quotas.(domain.SharedGeminiRepository)
	if err := topology.SetSharedGemini(ctx, acc.ID, true); err != nil {
		t.Fatal(err)
	}
	api := NewAPIHandler(accounts, quotas, nil, nil, nil, nil, "test")
	buckets := []*domain.QuotaBucket{{BucketID: "gemini-5h", RemainingFraction: 0.8}, {BucketID: "gemini-weekly", RemainingFraction: 0.2}}
	views := api.familyQuotaViews(ctx, acc, buckets, false)
	if v := views[domain.QuotaFamilyArgon]; !v.SharedGemini || v.Status != "eligible" {
		t.Fatalf("missing shared pool %+v", v)
	}
	if len(buckets) != 2 {
		t.Fatal("fabricated bucket")
	}
	reset := time.Now().Add(time.Hour)
	if err := quotas.(domain.FamilyCooldownRepository).SetFamilyCooldown(ctx, acc.ID, domain.QuotaFamilyGemini, reset); err != nil {
		t.Fatal(err)
	}
	views = api.familyQuotaViews(ctx, acc, buckets, false)
	if v := views[domain.QuotaFamilyArgon]; v.Status != "cooldown" || v.ResetTime == nil {
		t.Fatalf("shared cooldown missing %+v", v)
	}
	views = api.familyQuotaViews(ctx, acc, buckets, true)
	if v := views[domain.QuotaFamilyArgon]; v.SharedGemini || v.Status != "unavailable" {
		t.Fatalf("unavailable inferred Ultra %+v", v)
	}
}
