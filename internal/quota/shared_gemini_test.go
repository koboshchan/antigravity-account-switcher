package quota

import (
	"context"
	"fmt"
	"github.com/Muriel-Gasparini/antigravity-account-switcher/internal/domain"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSharedGeminiRequiresSuccessfulDirectPoll(t *testing.T) {
	ctx := context.Background()
	_, accounts, quotas := setupTestStore(t)
	acc := &domain.Account{ID: "ultra", Email: "ultra@test.invalid", Status: domain.AccountStatusActive, AccessToken: "fake", TokenExpiry: time.Now().Add(time.Hour)}
	if err := accounts.Create(ctx, acc); err != nil {
		t.Fatal(err)
	}
	status, body := 200, `{"buckets":[{"bucketId":"gemini-5h","remainingFraction":0.7},{"bucketId":"gemini-weekly","remainingFraction":0.3}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status); fmt.Fprint(w, body) }))
	defer srv.Close()
	p, _ := NewPoller(accounts, quotas, WithBaseURL(srv.URL))
	for _, tc := range []struct {
		status int
		body   string
		shared bool
	}{
		{200, body, true},
		{500, "{}", false},
		{200, body, true},
		{403, "{}", false},
		{200, body, true},
		{200, `{"buckets":[{"bucketId":"gemini-weekly","remainingFraction":0.7},{"bucketId":"argon-weekly","remainingFraction":0.2}]}`, false},
		{200, "{}", false},
	} {
		status, body = tc.status, tc.body
		_, err := p.PollAccount(ctx, acc.ID)
		if tc.status == 200 && err != nil {
			t.Fatal(err)
		}
		shared, err := quotas.GetSharedGemini(ctx, acc.ID)
		if err != nil || shared != tc.shared {
			t.Fatalf("status %d shared=%v err=%v", status, shared, err)
		}
	}
}
