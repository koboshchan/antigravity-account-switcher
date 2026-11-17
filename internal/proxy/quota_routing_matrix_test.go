package proxy

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Muriel-Gasparini/antigravity-account-switcher/internal/domain"
	"github.com/Muriel-Gasparini/antigravity-account-switcher/internal/store/sqlite"
)

// Exercise HTTP routing, not just the family classifier or candidate selector.
func TestQuotaRoutingMatrix(t *testing.T) {
	for _, model := range []string{"gemini-3.8-flash-high", "gemini-4-argon-trial", "claude-sonnet-5-5"} {
		for _, source := range []string{"body", "path", "query"} {
			for _, active := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/active=%t", model, source, active), func(t *testing.T) {
					ctx := context.Background()
					db, accounts, _, _ := setupTestDB(t)
					quotas := sqlite.NewQuotaRepository(db)
					now := time.Now().UTC()
					wantedFamily, otherFamily := "gemini", "claude"
					if strings.Contains(model, "argon") {
						wantedFamily = "argon"
					}
					if strings.HasPrefix(model, "claude") {
						wantedFamily, otherFamily = otherFamily, wantedFamily
					}
					for i, status := range []domain.AccountStatus{domain.AccountStatusActive, domain.AccountStatusDisabled, domain.AccountStatusError, domain.AccountStatusExhausted} {
						id := fmt.Sprintf("account-%d", i)
						acc := &domain.Account{ID: id, Email: id + "@example.test", AccessToken: id, TokenExpiry: now.Add(time.Hour), Status: status, IsActive: active && i == 0, CreatedAt: now.Add(time.Duration(i) * time.Second), UpdatedAt: now.Add(time.Duration(i) * time.Second)}
						if err := accounts.Create(ctx, acc); err != nil {
							t.Fatal(err)
						}
						fraction := 0.0
						if i > 0 {
							fraction = 0.55
						}
						buckets := []*domain.QuotaBucket{
							{AccountID: id, BucketID: wantedFamily + "-5h", DisplayName: wantedFamily + " models (5h)", Window: "5h", RemainingFraction: 1, ResetTime: now.Add(time.Hour)},
							{AccountID: id, BucketID: wantedFamily + "-weekly", DisplayName: wantedFamily + " models (weekly)", Window: "weekly", RemainingFraction: fraction, ResetTime: now.Add(48 * time.Hour)},
							{AccountID: id, BucketID: otherFamily + "-weekly", DisplayName: otherFamily + " models (weekly)", Window: "weekly", RemainingFraction: 0, ResetTime: now.Add(72 * time.Hour)},
						}
						if err := quotas.UpsertBuckets(ctx, buckets); err != nil {
							t.Fatal(err)
						}
					}
					called := 0
					upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						called++
						if got := r.Header.Get("Authorization"); got != "Bearer account-3" {
							t.Errorf("routed to %q, want family-eligible exhausted legacy account", got)
						}
						body, _ := io.ReadAll(r.Body)
						got, _, _ := ExtractModelFromRequest(r, body)
						if got != model {
							t.Errorf("requested model changed to %q", got)
						}
						_, _ = io.WriteString(w, `{"ok":true}`)
					}))
					defer upstream.Close()
					engine := NewFailoverEngine(accounts, nil, nil, WithQuotaRepository(quotas))
					handler, err := NewProxyHandler(accounts, WithTargetURL(upstream.URL), WithFailoverEngine(engine))
					if err != nil {
						t.Fatal(err)
					}
					path, body := "/v1internal:generateContent", `{"model":"`+model+`","request":{"contents":[]}}`
					if source == "path" {
						path = "/v1internal/models/" + model + ":generateContent"
						body = "{}"
					}
					if source == "query" {
						path += "?model=" + model
						body = "{}"
					}
					recorder := httptest.NewRecorder()
					handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
					if recorder.Code != http.StatusOK || called != 1 {
						t.Fatalf("status=%d upstream calls=%d body=%s", recorder.Code, called, recorder.Body.String())
					}
				})
			}
		}
	}
}
