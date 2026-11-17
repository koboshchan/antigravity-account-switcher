package proxy

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Muriel-Gasparini/antigravity-account-switcher/internal/domain"
	"github.com/Muriel-Gasparini/antigravity-account-switcher/internal/store/sqlite"
)

const licenseDenied = `{"error":{"code":403,"status":"PERMISSION_DENIED","details":[{"reason":"SUBSCRIPTION_REQUIRED"}]}}`

func TestSubscriptionRequiredClassification(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		want   bool
	}{
		{403, licenseDenied, true}, {401, licenseDenied, false}, {403, `{"error":{"message":"SUBSCRIPTION_REQUIRED"}}`, false},
		{403, `{"error":{"details":[{"reason":"OTHER"}]}}`, false}, {403, "not json", false},
	} {
		if got := isSubscriptionRequired(tc.status, []byte(tc.body)); got != tc.want {
			t.Errorf("classification %d %s = %t", tc.status, tc.body, got)
		}
	}
}

func TestSubscriptionFailoverPreservesExactModel(t *testing.T) {
	for _, model := range []string{"gemini-4-argon", "gemini-4-argon-low", "gemini-4-argon-trial"} {
		for _, source := range []string{"body", "path", "query"} {
			t.Run(model+"/"+source, func(t *testing.T) {
				ctx := context.Background()
				db, accounts, _, _ := setupTestDB(t)
				quotas := sqlite.NewQuotaRepository(db)
				for _, id := range []string{"first", "second"} {
					if err := accounts.Create(ctx, &domain.Account{ID: id, Email: id + "@test.invalid", Status: domain.AccountStatusActive, IsActive: id == "first", AccessToken: id, TokenExpiry: time.Now().Add(time.Hour)}); err != nil {
						t.Fatal(err)
					}
					if err := quotas.UpsertBuckets(ctx, []*domain.QuotaBucket{{AccountID: id, BucketID: "argon-weekly", RemainingFraction: 1}, {AccountID: id, BucketID: "gemini-weekly", RemainingFraction: 1}}); err != nil {
						t.Fatal(err)
					}
				}
				for _, id := range []string{"first", "second"} {
					_ = quotas.SetSharedGemini(ctx, id, true)
				}
				calls := 0
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++

					// Read through the public extractor just as the proxy does.
					var builder strings.Builder
					_, _ = io.Copy(&builder, r.Body)

					got, _, _ := ExtractModelFromRequest(r, []byte(builder.String()))
					want := model
					if model == "gemini-4-argon-trial" {
						want = "gemini-4-argon-low"
					}
					if got != want {
						t.Errorf("model changed to %s", got)
					}
					if r.Header.Get("Authorization") == "Bearer first" {
						w.WriteHeader(403)
						_, _ = w.Write([]byte(licenseDenied))
						return
					}
					_, _ = w.Write([]byte(`{"ok":true}`))
				}))
				defer upstream.Close()
				engine := NewFailoverEngine(accounts, nil, nil, WithQuotaRepository(quotas))
				handler, err := NewProxyHandler(accounts, WithTargetURL(upstream.URL), WithFailoverEngine(engine))
				if err != nil {
					t.Fatal(err)
				}
				path, body := "/v1internal:generateContent", `{"model":"`+model+`"}`
				if source == "path" {
					path = "/v1internal/models/" + model + ":generateContent"
					body = "{}"
				}
				if source == "query" {
					path += "?model=" + model
					body = "{}"
				}
				for i := 0; i < 2; i++ {
					w := httptest.NewRecorder()
					handler.ServeHTTP(w, httptest.NewRequest("POST", path, strings.NewReader(body)))
					if w.Code != 200 {
						t.Fatalf("status %d %s", w.Code, w.Body.String())
					}
				}
				if calls != 3 {
					t.Fatalf("expected rejection then two successful attempts, got %d", calls)
				}
				first, _ := accounts.GetByID(ctx, "first")
				engine.mu.Lock()
				if engine.familyEligibleLocked(ctx, first, model) {
					t.Error("rejected model still eligible")
				}
				if !engine.familyEligibleLocked(ctx, first, "gemini-4-argon-other") {
					t.Error("unrelated Argon model blocked")
				}
				if !engine.familyEligibleLocked(ctx, first, "gemini-3-flash") {
					t.Error("Gemini account globally blocked")
				}
				engine.mu.Unlock()
			})
		}
	}
}

func TestSubscriptionAllAccountsDeniedTerminates(t *testing.T) {
	ctx := context.Background()
	db, accounts, _, _ := setupTestDB(t)
	quotas := sqlite.NewQuotaRepository(db)
	_ = accounts.Create(ctx, &domain.Account{ID: "only", Email: "only@test.invalid", Status: domain.AccountStatusActive, IsActive: true, AccessToken: "only", TokenExpiry: time.Now().Add(time.Hour)})
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(403)
		_, _ = w.Write([]byte(licenseDenied))
	}))
	defer upstream.Close()
	engine := NewFailoverEngine(accounts, nil, nil, WithQuotaRepository(quotas))
	handler, err := NewProxyHandler(accounts, WithTargetURL(upstream.URL), WithFailoverEngine(engine))
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("POST", "/v1internal:generateContent", strings.NewReader(`{"model":"gemini-4-argon-trial"}`)))
	if w.Code != 403 || !strings.Contains(w.Body.String(), "SUBSCRIPTION_REQUIRED") || calls != 1 {
		t.Fatalf("status=%d calls=%d body=%s", w.Code, calls, w.Body.String())
	}
}

func TestTrialPrefersUltraAndRestoresTrialOnFallback(t *testing.T) {
	for _, source := range []string{"body", "path", "query"} {
		t.Run(source, func(t *testing.T) {
			ctx := context.Background()
			db, accounts, _, _ := setupTestDB(t)
			quotas := sqlite.NewQuotaRepository(db)
			for _, id := range []string{"standard", "ultra"} {
				if err := accounts.Create(ctx, &domain.Account{ID: id, Email: id + "@test.invalid", AccessToken: id, TokenExpiry: time.Now().Add(time.Hour), Status: domain.AccountStatusActive, IsActive: id == "standard"}); err != nil {
					t.Fatal(err)
				}
				_ = quotas.UpsertBuckets(ctx, []*domain.QuotaBucket{{AccountID: id, BucketID: "gemini-weekly", RemainingFraction: 1}, {AccountID: id, BucketID: "argon-weekly", RemainingFraction: 1}})
			}
			_ = quotas.SetSharedGemini(ctx, "ultra", true)
			calls := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				body, _ := io.ReadAll(r.Body)
				model, _, _ := ExtractModelFromRequest(r, body)
				if calls == 1 {
					if model != "gemini-4-argon-low" || r.Header.Get("Authorization") != "Bearer ultra" {
						t.Errorf("Ultra preference/promotion failed: %s %s", model, r.Header.Get("Authorization"))
					}
					w.WriteHeader(403)
					_, _ = w.Write([]byte(licenseDenied))
					return
				}
				if model != "gemini-4-argon-trial" || r.Header.Get("Authorization") != "Bearer standard" {
					t.Errorf("trial fallback failed: %s %s", model, r.Header.Get("Authorization"))
				}
				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			defer upstream.Close()
			engine := NewFailoverEngine(accounts, nil, nil, WithQuotaRepository(quotas))
			handler, err := NewProxyHandler(accounts, WithTargetURL(upstream.URL), WithFailoverEngine(engine))
			if err != nil {
				t.Fatal(err)
			}
			path, body := "/v1internal:generateContent", `{"model":"gemini-4-argon-trial"}`
			if source == "path" {
				path = "/v1internal/models/gemini-4-argon-trial:generateContent"
				body = "{}"
			}
			if source == "query" {
				path += "?model=gemini-4-argon-trial"
				body = "{}"
			}
			w := httptest.NewRecorder()
			handler.ServeHTTP(w, httptest.NewRequest("POST", path, strings.NewReader(body)))
			if w.Code != 200 || calls != 2 {
				t.Fatalf("status %d calls %d body %s", w.Code, calls, w.Body.String())
			}
		})
	}
}

func TestStreamingRequestPreservesJSONUpstreamError(t *testing.T) {
	_, accounts, _, _ := setupTestDB(t)
	_ = accounts.Create(context.Background(), &domain.Account{ID: "a", Email: "a@test.invalid", AccessToken: "test", IsActive: true, Status: domain.AccountStatusActive, TokenExpiry: time.Now().Add(time.Hour)})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(404)
		_, _ = w.Write([]byte(`{"error":{"status":"NOT_FOUND"}}`))
	}))
	defer upstream.Close()
	handler, err := NewProxyHandler(accounts, WithTargetURL(upstream.URL))
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, httptest.NewRequest("POST", "/v1internal:streamGenerateContent?alt=sse", strings.NewReader(`{"model":"gemini-flash"}`)))
	if w.Code != 404 || !strings.Contains(w.Body.String(), "NOT_FOUND") {
		t.Fatalf("swallowed error %d %s", w.Code, w.Body.String())
	}
}
