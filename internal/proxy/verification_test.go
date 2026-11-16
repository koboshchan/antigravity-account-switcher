package proxy

import (
	"context"
	"github.com/Muriel-Gasparini/antigravity-account-switcher/internal/domain"
	"github.com/Muriel-Gasparini/antigravity-account-switcher/internal/store/sqlite"
	"testing"
)

func TestVerificationInvalidatesAllFamiliesAndNeverTimerRecovers(t *testing.T) {
	ctx := context.Background()
	db, accounts, _, _ := setupTestDB(t)
	quotas := sqlite.NewQuotaRepository(db)
	for _, id := range []string{"a", "b"} {
		if err := accounts.Create(ctx, &domain.Account{ID: id, Email: id + "@test.invalid", Status: domain.AccountStatusActive, IsActive: id == "a"}); err != nil {
			t.Fatal(err)
		}
	}
	models := map[domain.QuotaFamily]string{domain.QuotaFamilyGemini: "gemini-3.8-flash", domain.QuotaFamilyArgon: "argon", domain.QuotaFamilyClaudeGPT: "claude-sonnet-5"}
	for family := range models {
		if err := quotas.SetFamilyActive(ctx, family, "a"); err != nil {
			t.Fatal(err)
		}
	}
	if err := quotas.SetVerification(ctx, "a", "https://accounts.google.com/v"); err != nil {
		t.Fatal(err)
	}
	engine := NewFailoverEngine(accounts, nil, nil, WithQuotaRepository(quotas))
	for family, model := range models {
		if id, _ := quotas.GetFamilyActive(ctx, family); id != "" {
			t.Fatal("stale pointer")
		}
		next, err := engine.ResolveForModel(ctx, model)
		if err != nil || next == nil || next.ID != "b" {
			t.Fatalf("blocked account selected %v %v", next, err)
		}
		quotas.SetFamilyActive(ctx, family, "a")
		if id, _ := quotas.GetFamilyActive(ctx, family); id == "a" {
			t.Fatal("blocked pointer persisted")
		}
	}
	if v, _ := quotas.GetVerification(ctx, "a"); v == nil {
		t.Fatal("routing cleared verification")
	}
}
