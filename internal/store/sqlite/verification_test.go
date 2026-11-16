package sqlite

import (
	"context"
	"github.com/Muriel-Gasparini/antigravity-account-switcher/internal/domain"
	"path/filepath"
	"testing"
	"time"
)

func TestVerificationPersistenceOrderingAndManualState(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "verify.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	accounts := NewAccountRepository(db)
	quotas := NewQuotaRepository(db)
	accounts.Create(ctx, &domain.Account{ID: "a", Email: "a@test.invalid", Status: domain.AccountStatusDisabled})
	before := time.Now()
	if err := quotas.SetVerification(ctx, "a", "https://accounts.google.com/v?secret=fake"); err != nil {
		t.Fatal(err)
	}
	quotas.ClearVerificationBefore(ctx, "a", before)
	db.Close()
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	quotas = NewQuotaRepository(db)
	if got, err := quotas.GetVerification(ctx, "a"); err != nil || got == nil || got.URL == "" {
		t.Fatalf("restart lost verification %v %v", got, err)
	}
	if err := quotas.ClearVerificationBefore(ctx, "a", time.Now()); err != nil {
		t.Fatal(err)
	}
	if got, _ := quotas.GetVerification(ctx, "a"); got != nil {
		t.Fatal("later recheck failed")
	}
	acc, _ := NewAccountRepository(db).GetByID(ctx, "a")
	if acc.Status != domain.AccountStatusDisabled {
		t.Fatal("manual state changed")
	}
}
