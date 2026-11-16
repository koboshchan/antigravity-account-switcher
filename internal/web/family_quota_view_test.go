package web

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Muriel-Gasparini/antigravity-account-switcher/internal/domain"
)

func TestAccountAPIHidesNumbersWhenQuotaUnavailable(t *testing.T) {
	ctx := context.Background()
	_, accounts, quotas, _, _, _, _ := setupTestWeb(t)
	acc := &domain.Account{ID: "a", Email: "a@test.invalid", Status: domain.AccountStatusActive}
	if err := accounts.Create(ctx, acc); err != nil {
		t.Fatal(err)
	}
	if err := quotas.UpsertBuckets(ctx, []*domain.QuotaBucket{{AccountID: "a", BucketID: "gemini-weekly", RemainingFraction: 0.777}, {AccountID: "a", BucketID: "gemini-4-argon-weekly", RemainingFraction: 0.456}}); err != nil {
		t.Fatal(err)
	}
	availability := quotas.(domain.QuotaAvailabilityRepository)
	api := NewAPIHandler(accounts, quotas, nil, nil, nil, nil, "test")
	check := func(blocked bool) {
		t.Helper()
		for _, path := range []string{"/api/accounts", "/api/accounts/a"} {
			w := httptest.NewRecorder()
			api.HandleAccounts(w, httptest.NewRequest("GET", path, nil))
			if w.Code != 200 {
				t.Fatal(w.Body.String())
			}
			var rows []*AccountWithBuckets
			if path == "/api/accounts" {
				if err := json.Unmarshal(w.Body.Bytes(), &rows); err != nil {
					t.Fatal(err)
				}
			} else {
				var row AccountWithBuckets
				if err := json.Unmarshal(w.Body.Bytes(), &row); err != nil {
					t.Fatal(err)
				}
				rows = []*AccountWithBuckets{&row}
			}
			if len(rows) != 1 {
				t.Fatal("missing account")
			}
			row := rows[0]
			if row.QuotaUnavailable != blocked {
				t.Fatal("availability marker wrong")
			}
			if blocked {
				if len(row.Buckets) != 0 {
					t.Fatalf("API exposed cached numbers: %s", w.Body.String())
				}
				for _, family := range row.Families {
					if family.Status != "unavailable" || family.ResetTime != nil {
						t.Fatal("unavailable API exposed family quota evidence")
					}
				}
			} else {
				if len(row.Buckets) != 2 {
					t.Fatal("successful recovery hid numbers")
				}
			}
		}
	}
	check(false)
	if err := availability.SetQuotaUnavailable(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	check(true)
	if err := availability.ClearQuotaUnavailableBefore(ctx, "a", time.Now()); err != nil {
		t.Fatal(err)
	}
	check(false)
	// Generic Gemini is never projected into Argon's independent quota view.
	views := api.familyQuotaViews(ctx, acc, []*domain.QuotaBucket{{BucketID: "gemini-weekly", RemainingFraction: 1}}, false)
	if views[domain.QuotaFamilyArgon].Status != "unknown" || views[domain.QuotaFamilyArgon].ResetTime != nil {
		t.Fatalf("invented Argon evidence: %+v", views)
	}
}
