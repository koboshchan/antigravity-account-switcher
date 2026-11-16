package sqlite

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/Muriel-Gasparini/antigravity-account-switcher/internal/domain"
)

func TestQuotaAvailabilityPersistsAndRequiresLaterPoll(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "availability.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	accounts := NewAccountRepository(db)
	if err := accounts.Create(ctx, &domain.Account{ID: "a", Email: "a@test.invalid", Status: domain.AccountStatusActive}); err != nil {
		t.Fatal(err)
	}
	quotas := NewQuotaRepository(db)
	oldPoll := time.Now()
	if err := quotas.SetQuotaUnavailable(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if err := quotas.ClearQuotaUnavailableBefore(ctx, "a", oldPoll); err != nil {
		t.Fatal(err)
	}
	if blocked, err := quotas.IsQuotaUnavailable(ctx, "a"); err != nil || !blocked {
		t.Fatalf("older poll cleared permission failure: %t %v", blocked, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	quotas = NewQuotaRepository(db)
	if blocked, err := quotas.IsQuotaUnavailable(ctx, "a"); err != nil || !blocked {
		t.Fatalf("restart lost block: %t %v", blocked, err)
	}
	if err := quotas.ClearQuotaUnavailableBefore(ctx, "a", time.Now()); err != nil {
		t.Fatal(err)
	}
	if blocked, err := quotas.IsQuotaUnavailable(ctx, "a"); err != nil || blocked {
		t.Fatalf("later successful poll failed to clear: %t %v", blocked, err)
	}
}

func TestCooldownObservationUsesNanosecondOrdering(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "nano.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := NewAccountRepository(db).Create(ctx, &domain.Account{ID: "a", Email: "a@test.invalid", Status: domain.AccountStatusActive}); err != nil {
		t.Fatal(err)
	}
	quotas := NewQuotaRepository(db)
	reset := time.Now().Add(time.Hour)
	if err := quotas.SetFamilyCooldown(ctx, "a", domain.QuotaFamilyArgon, reset); err != nil {
		t.Fatal(err)
	}
	// Deterministic same-millisecond observations, one nanosecond apart.
	observed := time.Now().UnixNano()
	if _, err := db.ExecContext(ctx, `UPDATE family_cooldowns SET observed_at=? WHERE account_id='a'`, observed); err != nil {
		t.Fatal(err)
	}
	if err := quotas.ClearFamilyCooldownBefore(ctx, "a", domain.QuotaFamilyArgon, time.Unix(0, observed-1)); err != nil {
		t.Fatal(err)
	}
	got, err := quotas.GetFamilyCooldown(ctx, "a", domain.QuotaFamilyArgon)
	if err != nil || !got.After(time.Now()) {
		t.Fatalf("older same-ms poll erased cooldown: %v %v", got, err)
	}
	if err := quotas.ClearFamilyCooldownBefore(ctx, "a", domain.QuotaFamilyArgon, time.Unix(0, observed+1)); err != nil {
		t.Fatal(err)
	}
	got, err = quotas.GetFamilyCooldown(ctx, "a", domain.QuotaFamilyArgon)
	if err != nil || !got.IsZero() {
		t.Fatalf("later poll did not clear: %v %v", got, err)
	}
}

func TestFamilyActivePersistsAcrossDatabaseReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "families.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	accounts := NewAccountRepository(db)
	repo := NewQuotaRepository(db)
	for _, id := range []string{"a", "b", "c"} {
		if err := accounts.Create(ctx, &domain.Account{ID: id, Email: id + "@test.invalid", Status: domain.AccountStatusActive}); err != nil {
			t.Fatal(err)
		}
	}
	want := map[domain.QuotaFamily]string{domain.QuotaFamilyGemini: "a", domain.QuotaFamilyClaudeGPT: "b", domain.QuotaFamilyArgon: "c"}
	for family, id := range want {
		if err := repo.SetFamilyActive(ctx, family, id); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	repo = NewQuotaRepository(db)
	for family, id := range want {
		got, err := repo.GetFamilyActive(ctx, family)
		if err != nil || got != id {
			t.Fatalf("%s got=%s err=%v", family, got, err)
		}
	}
	if err := repo.SetQuotaUnavailable(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if err := repo.SetFamilyActive(ctx, domain.QuotaFamilyGemini, "a"); err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.GetFamilyActive(ctx, domain.QuotaFamilyGemini); got != "" {
		t.Fatal("unavailable account reselected")
	}
}
