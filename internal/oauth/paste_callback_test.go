package oauth

import (
	"context"
	"fmt"
	"github.com/Muriel-Gasparini/antigravity-account-switcher/internal/domain"
	"github.com/Muriel-Gasparini/antigravity-account-switcher/test/mocks"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPastedCallbackValidationAndRemoteCompletion(t *testing.T) {
	server := mocks.NewMockGoogleServer()
	defer server.Close()
	_, repo := setupTestStore(t)
	svc := NewOAuthService(repo, WithTokenURL(server.URL+"/token"), WithUserInfoURL(server.URL+"/oauth2/v3/userinfo"), WithAuthURL(server.URL+"/o/oauth2/v2/auth"))
	urls := make(chan string, 1)
	result := make(chan *domain.Account, 1)
	errs := make(chan error, 1)
	go func() {
		acc, err := svc.StartLoopbackFlow(context.Background(), func(string) error { return nil }, func(u string) { urls <- u })
		result <- acc
		errs <- err
	}()
	auth := <-urls
	u, _ := url.Parse(auth)
	redirect := u.Query().Get("redirect_uri")
	state := u.Query().Get("state")
	callback := redirect + "?code=mock_auth_code_123&state=" + state
	base, _ := url.Parse(callback)
	for _, mutate := range []func(*url.URL){func(u *url.URL) { u.Host = "example.com" }, func(u *url.URL) { u.Host = "127.0.0.1:1" }, func(u *url.URL) { u.Path = "/different" }, func(u *url.URL) { u.User = url.UserPassword("a", "b") }, func(u *url.URL) { u.Scheme = "https" }, func(u *url.URL) { u.RawQuery = "code=mock&state=wrong" }, func(u *url.URL) { u.Fragment = "bad" }, func(u *url.URL) { u.RawQuery += "&state=" + state }} {
		candidate := *base
		mutate(&candidate)
		if _, err := svc.SubmitCallbackURL(context.Background(), candidate.String()); err == nil {
			t.Errorf("accepted invalid callback shape")
		}
	}
	acc, err := svc.SubmitCallbackURL(context.Background(), callback)
	if err != nil || acc == nil {
		t.Fatalf("remote completion failed %v", err)
	}
	if got := <-result; got == nil || got.ID != acc.ID {
		t.Fatal("outstanding flow not notified")
	}
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SubmitCallbackURL(context.Background(), callback); err == nil {
		t.Fatal("replay accepted")
	}
	stored, err := repo.GetByID(context.Background(), acc.ID)
	if err != nil || stored == nil {
		t.Fatal("success before account stored")
	}
}
func TestPastedCallbackExpiredAndNeverFetchesInput(t *testing.T) {
	_, repo := setupTestStore(t)
	svc := NewOAuthService(repo, WithStateTTL(time.Millisecond))
	state := strings.Repeat("s", 43)
	svc.stateStore.Put(&PendingAuth{State: state, RedirectURI: "http://127.0.0.1:1234/oauth/callback", CreatedAt: time.Now().Add(-time.Hour)})
	svc.completions[state] = func(*domain.Account, error) { t.Fatal("expired flow notified") }
	if _, err := svc.SubmitCallbackURL(context.Background(), "http://127.0.0.1:1234/oauth/callback?code=secret&state="+state); err == nil {
		t.Fatal("expired callback accepted")
	}
}

func TestConcurrentLocalAndPastedCallbacksHaveOneOwner(t *testing.T) {
	for _, local := range []bool{false, true} {
		t.Run(fmt.Sprint("local=", local), func(t *testing.T) {
			google := mocks.NewMockGoogleServer()
			defer google.Close()
			_, repo := setupTestStore(t)
			var exchanges atomic.Int32
			entered := make(chan struct{})
			release := make(chan struct{})
			token := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				exchanges.Add(1)
				close(entered)
				<-release
				proxy, _ := url.Parse(google.URL)
				httputil.NewSingleHostReverseProxy(proxy).ServeHTTP(w, r)
			}))
			defer token.Close()
			svc := NewOAuthService(repo, WithTokenURL(token.URL+"/token"), WithUserInfoURL(google.URL+"/oauth2/v3/userinfo"), WithAuthURL(google.URL+"/auth"), WithFlowTimeout(3*time.Second))
			urls := make(chan string, 1)
			done := make(chan error, 1)
			go func() {
				acc, err := svc.StartLoopbackFlow(context.Background(), func(string) error { return nil }, func(u string) { urls <- u })
				if err == nil && acc == nil {
					err = fmt.Errorf("nil account")
				}
				done <- err
			}()
			auth, _ := url.Parse(<-urls)
			callback := auth.Query().Get("redirect_uri") + "?code=mock_auth_code_123&state=" + auth.Query().Get("state")
			winner := make(chan error, 1)
			go func() {
				if local {
					resp, err := http.Get(callback)
					if err == nil {
						resp.Body.Close()
						if resp.StatusCode != 200 {
							err = fmt.Errorf("callback status %d", resp.StatusCode)
						}
					}
					winner <- err
				} else {
					_, err := svc.SubmitCallbackURL(context.Background(), callback)
					winner <- err
				}
			}()
			<-entered
			if _, err := svc.SubmitCallbackURL(context.Background(), callback); err == nil {
				t.Fatal("duplicate callback accepted")
			}
			// An unrelated local callback cannot end a valid in-progress exchange.
			if resp, err := http.Get(strings.Replace(callback, "state=", "state=wrong", 1)); err == nil {
				resp.Body.Close()
			}
			select {
			case err := <-done:
				t.Fatalf("loser ended waiting flow: %v", err)
			default:
			}
			close(release)
			if err := <-winner; err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			if exchanges.Load() != 1 {
				t.Fatalf("exchange count %d", exchanges.Load())
			}
		})
	}
}
