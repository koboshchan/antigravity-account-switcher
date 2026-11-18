package proxy

import (
	"context"
	"github.com/Muriel-Gasparini/antigravity-account-switcher/internal/domain"
	"github.com/Muriel-Gasparini/antigravity-account-switcher/internal/store/sqlite"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestProArgonTrialAfterUltraLoss(t *testing.T) {
	for _, source := range []string{"body", "path", "query"} {
		for _, model := range []string{"gemini-4-argon-trial", "gemini-4-argon-low"} {
			t.Run(source+"/"+model, func(t *testing.T) {
				ctx := context.Background()
				db, accounts, _, _ := setupTestDB(t)
				quotas := sqlite.NewQuotaRepository(db)
				for _, id := range []string{"unknown", "pro", "revoked"} {
					status := domain.AccountStatusActive
					if id == "revoked" {
						status = domain.AccountStatusDisabled
					}
					if err := accounts.Create(ctx, &domain.Account{ID: id, Email: id + "@test.invalid", Status: status, IsActive: id == "unknown", AccessToken: id, TokenExpiry: time.Now().Add(time.Hour)}); err != nil {
						t.Fatal(err)
					}
				}
				_ = quotas.SetSharedGemini(ctx, "revoked", true)
				_ = quotas.UpsertBuckets(ctx, []*domain.QuotaBucket{
					{AccountID: "pro", BucketID: "gemini-argon-wtu-weekly", RemainingFraction: 1},
					{AccountID: "pro", BucketID: "gemini-weekly", RemainingFraction: 0, ResetTime: time.Now().Add(time.Hour)},
					{AccountID: "revoked", BucketID: "gemini-weekly", RemainingFraction: 1},
				})
				calls := 0
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					calls++
					body, _ := io.ReadAll(r.Body)
					got, _, _ := ExtractModelFromRequest(r, body)
					if got != "gemini-4-argon-trial" || r.Header.Get("Authorization") != "Bearer pro" {
						t.Errorf("wrong Pro trial route model=%s", got)
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
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, httptest.NewRequest("POST", path, strings.NewReader(body)))
				if w.Code != 200 || calls != 1 {
					t.Fatalf("status=%d calls=%d body=%s", w.Code, calls, w.Body.String())
				}
			})
		}
	}
}
