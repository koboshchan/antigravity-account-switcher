package proxy

import (
	"context"
	"github.com/Muriel-Gasparini/antigravity-account-switcher/internal/domain"
	"github.com/Muriel-Gasparini/antigravity-account-switcher/internal/store/sqlite"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestUltraModelsNeverUseIndependentOrUnknownAccounts(t *testing.T) {
	for _, model := range []string{"carbon", "Carbon", "models/carbon", "gemini-4-carbon-high", "argon", "gemini-4-argon", "gemini-4-argon-low", "gemini-4-argon-high"} {
		t.Run(model, func(t *testing.T) {
			ctx := context.Background()
			db, accounts, _, _ := setupTestDB(t)
			quotas := sqlite.NewQuotaRepository(db)
			for _, id := range []string{"standard", "unknown", "ultra"} {
				if err := accounts.Create(ctx, &domain.Account{ID: id, Email: id + "@test.invalid", Status: domain.AccountStatusActive, IsActive: id == "standard"}); err != nil {
					t.Fatal(err)
				}
				if err := quotas.UpsertBuckets(ctx, []*domain.QuotaBucket{
					{AccountID: id, BucketID: "gemini-5h", RemainingFraction: 1},
					{AccountID: id, BucketID: "gemini-weekly", RemainingFraction: 1},
				}); err != nil {
					t.Fatal(err)
				}
			}
			if err := quotas.UpsertBuckets(ctx, []*domain.QuotaBucket{{AccountID: "standard", BucketID: "argon-weekly", RemainingFraction: 1}}); err != nil {
				t.Fatal(err)
			}
			if err := quotas.SetSharedGemini(ctx, "ultra", true); err != nil {
				t.Fatal(err)
			}
			engine := NewFailoverEngine(accounts, nil, nil, WithQuotaRepository(quotas))
			got, err := engine.ResolveForModel(ctx, model)
			if err != nil || got.ID != "ultra" {
				t.Fatalf("Carbon did not select Ultra: %v %v", got, err)
			}
			// A Carbon 429 must not rotate to healthy standard or unknown accounts.
			_, _, next, err := engine.HandleExhaustion(ctx, got, model)
			if err == nil || next != nil {
				t.Fatalf("Carbon escaped to non-Ultra: %v %v", next, err)
			}
			got, err = engine.ResolveForModel(ctx, model)
			if err == nil || got != nil {
				t.Fatalf("Carbon preflight escaped cooldown: %v %v", got, err)
			}
			// Argon retains access to its independent pool.
			got, err = engine.ResolveForModel(ctx, "argon-trial")
			if err != nil || got.ID != "standard" {
				t.Fatalf("Argon behavior changed: %v %v", got, err)
			}
			// Missing measured Gemini capacity must not grant Carbon access either.
			if err := quotas.DeleteByAccountID(ctx, "ultra"); err != nil {
				t.Fatal(err)
			}
			if err := quotas.ClearFamilyCooldownBefore(ctx, "ultra", domain.QuotaFamilyGemini, time.Now().Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			got, err = engine.ResolveForModel(ctx, model)
			if err == nil || got != nil {
				t.Fatalf("unknown Gemini capacity accepted: %v %v", got, err)
			}
		})
	}
}

func TestCarbonHTTPNeverFallsBackToNonUltra(t *testing.T) {
	for _, source := range []string{"body", "path", "query"} {
		t.Run(source, func(t *testing.T) {
			ctx := context.Background()
			db, accounts, _, _ := setupTestDB(t)
			quotas := sqlite.NewQuotaRepository(db)
			for _, id := range []string{"standard", "ultra"} {
				if err := accounts.Create(ctx, &domain.Account{ID: id, Email: id + "@test.invalid", Status: domain.AccountStatusActive, IsActive: id == "standard", AccessToken: id, TokenExpiry: time.Now().Add(time.Hour)}); err != nil {
					t.Fatal(err)
				}
				if err := quotas.UpsertBuckets(ctx, []*domain.QuotaBucket{{AccountID: id, BucketID: "gemini-weekly", RemainingFraction: 1}, {AccountID: id, BucketID: "argon-weekly", RemainingFraction: 1}}); err != nil {
					t.Fatal(err)
				}
			}
			// Replace Ultra's snapshot with Gemini only.
			_ = quotas.DeleteByAccountID(ctx, "ultra")
			_ = quotas.UpsertBuckets(ctx, []*domain.QuotaBucket{{AccountID: "ultra", BucketID: "gemini-weekly", RemainingFraction: 1}})
			_ = quotas.SetSharedGemini(ctx, "ultra", true)
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Header.Get("Authorization") != "Bearer ultra" {
					t.Errorf("Carbon reached non-Ultra %q", r.Header.Get("Authorization"))
				}
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte("Ultra exhausted"))
			}))
			defer upstream.Close()
			engine := NewFailoverEngine(accounts, nil, nil, WithQuotaRepository(quotas))
			handler, err := NewProxyHandler(accounts, WithTargetURL(upstream.URL), WithFailoverEngine(engine))
			if err != nil {
				t.Fatal(err)
			}
			path, body := "/v1internal:generateContent", `{"model":"carbon"}`
			if source == "path" {
				path = "/v1internal/models/carbon:generateContent"
				body = "{}"
			}
			if source == "query" {
				path += "?model=carbon"
				body = "{}"
			}
			for i := 0; i < 2; i++ {
				w := httptest.NewRecorder()
				r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
				r.Header.Set("Authorization", "Bearer client")
				handler.ServeHTTP(w, r)
				if w.Code != 429 {
					t.Fatalf("expected exhausted 429 got %d %s", w.Code, w.Body.String())
				}
			}
			if calls != 1 {
				t.Fatalf("expected one Ultra attempt, got %d", calls)
			}
		})
	}
}
