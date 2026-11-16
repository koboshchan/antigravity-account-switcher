package quota

import (
	"context"
	"github.com/Muriel-Gasparini/antigravity-account-switcher/internal/domain"
	"net/http"
	"net/http/httptest"
	"runtime"
	"testing"
	"time"
)

func TestHubClientRetrievesIndependentArgonSummary(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1internal:retrieveUserQuotaSummary" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if got := r.Header.Get("User-Agent"); got != "antigravity/hub/2.23.0 "+runtime.GOOS+"/"+runtime.GOARCH {
			t.Errorf("outdated quota UA %q", got)
		}
		w.Write([]byte(`{"groups":[{"displayName":"Gemini Models","buckets":[{"bucketId":"gemini-weekly","window":"weekly","remainingFraction":0.42858255}]},{"displayName":"Gemini 4 Argon","buckets":[{"bucketId":"gemini-argon-wtu-weekly","window":"weekly","remainingFraction":0.068522,"resetTime":"2026-11-22T09:23:54Z"}]}]}`))
	}))
	defer server.Close()
	_, accounts, quotas := setupTestStore(t)
	p, _ := NewPoller(accounts, quotas, WithBaseURL(server.URL))
	buckets, err := p.fetchQuotaSummary(context.Background(), &domain.Account{ID: "fixture", AccessToken: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != 2 || domain.BucketFamily(buckets[1]) != domain.QuotaFamilyArgon || buckets[1].RemainingFraction != 0.068522 || buckets[1].Window != domain.QuotaWindow("weekly") || buckets[1].ResetTime.IsZero() {
		t.Fatalf("lost real independent bucket %#v", buckets)
	}
	known, available := domain.FamilyQuota(buckets, domain.QuotaFamilyArgon, time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC))
	if !known || !available {
		t.Fatal("reported Argon not available")
	}
	if buckets[0].RemainingFraction == buckets[1].RemainingFraction {
		t.Fatal("projected generic quota")
	}
}
func TestLocalSummaryWithoutArgonRequiresDirectFetch(t *testing.T) {
	for _, buckets := range [][]*domain.QuotaBucket{nil, {{BucketID: "gemini-weekly", RemainingFraction: 1}}, {{BucketID: "3p-weekly", RemainingFraction: 1}}} {
		if !needsDirectQuotaSummary(buckets) {
			t.Fatal("partial local quota hid Argon")
		}
	}
	if needsDirectQuotaSummary([]*domain.QuotaBucket{{BucketID: "gemini-argon-wtu-weekly", RemainingFraction: 0}}) {
		t.Fatal("explicit exhausted Argon treated as missing")
	}
}

func TestHubUserAgentOnLegacyAndForcedRefresh(t *testing.T) {
	for _, mode := range []string{"legacy", "refresh"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			legacy := false
			refreshed := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if got := r.Header.Get("User-Agent"); got != quotaUserAgent() {
					t.Errorf("missing hub UA on %s request %d: %q", mode, calls, got)
				}
				if r.URL.Path == "/v1internal:retrieveUserQuota" {
					legacy = true
				} else if calls == 1 {
					if mode == "legacy" {
						w.WriteHeader(404)
					} else {
						w.WriteHeader(401)
					}
					return
				}
				w.Write([]byte(`{"buckets":[{"bucketId":"gemini-argon-wtu-weekly","window":"weekly","remainingFraction":0.068522}]}`))
			}))
			defer server.Close()
			_, accounts, quotas := setupTestStore(t)
			p, _ := NewPoller(accounts, quotas, WithBaseURL(server.URL), WithTokenRefresher(TokenRefresherFunc(func(context.Context, string) (string, time.Time, error) {
				refreshed = true
				return "new-fake", time.Now().Add(time.Hour), nil
			})))
			acc := &domain.Account{ID: "fixture", Email: "fixture@test.invalid", Status: domain.AccountStatusActive, AccessToken: "fake", RefreshToken: "fake-refresh", TokenExpiry: time.Now().Add(time.Hour)}
			if err := accounts.Create(context.Background(), acc); err != nil {
				t.Fatal(err)
			}
			buckets, err := p.fetchQuotaSummary(context.Background(), acc)
			if err != nil || len(buckets) != 1 {
				t.Fatalf("quota request failed: %v", err)
			}
			if calls != 2 || (mode == "legacy" && !legacy) || (mode == "refresh" && !refreshed) {
				t.Fatalf("wrong branch calls=%d legacy=%v refreshed=%v", calls, legacy, refreshed)
			}
		})
	}
}
