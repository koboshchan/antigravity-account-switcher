package web

import (
	"context"
	"encoding/json"
	"github.com/Muriel-Gasparini/antigravity-account-switcher/internal/domain"
	"net/http/httptest"
	"testing"
)

func TestVerificationAPISeparateFromGenericUnavailableAndHidesPercentages(t *testing.T) {
	ctx := context.Background()
	_, accounts, quotas, _, _, _, _ := setupTestWeb(t)
	accounts.Create(ctx, &domain.Account{ID: "a", Email: "a@test.invalid", Status: domain.AccountStatusActive})
	quotas.UpsertBuckets(ctx, []*domain.QuotaBucket{{AccountID: "a", BucketID: "gemini-weekly", RemainingFraction: 0.777}})
	verification := quotas.(domain.VerificationRepository)
	verification.SetVerification(ctx, "a", "https://accounts.google.com/v")
	api := NewAPIHandler(accounts, quotas, nil, nil, nil, nil, "test")
	w := httptest.NewRecorder()
	api.HandleAccounts(w, httptest.NewRequest("GET", "/api/accounts/a", nil))
	var row AccountWithBuckets
	if err := json.Unmarshal(w.Body.Bytes(), &row); err != nil {
		t.Fatal(err)
	}
	if row.Verification == nil || row.QuotaUnavailable || len(row.Buckets) != 0 {
		t.Fatalf("incorrect verification view %#v", row)
	}
	for _, view := range row.Families {
		if view.Status != "needs_verification" || view.Active {
			t.Fatal("verification not isolated from quota/auth state")
		}
	}
}
