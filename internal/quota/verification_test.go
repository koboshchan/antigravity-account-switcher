package quota

import (
	"context"
	"errors"
	"github.com/Muriel-Gasparini/antigravity-account-switcher/internal/domain"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestVerificationPersistsUntilAuthoritativeRecheck(t *testing.T) {
	ctx := context.Background()
	_, accounts, quotas := setupTestStore(t)
	if err := accounts.Create(ctx, &domain.Account{ID: "a", Email: "a@test.invalid", Status: domain.AccountStatusActive, AccessToken: "fake", TokenExpiry: time.Now().Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	status := 400
	body := `{"error":{"details":[{"reason":"VALIDATION_REQUIRED","metadata":{"verifyURL":"https://accounts.google.com/v?secret=fake"}}]}}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status); w.Write([]byte(body)) }))
	defer server.Close()
	p, _ := NewPoller(accounts, quotas, WithBaseURL(server.URL))
	for _, code := range []int{400, 403, 200} {
		status = code
		if _, err := p.PollAccount(ctx, "a"); err == nil {
			t.Fatal("verification response treated as quota success")
		}
		if v, _ := quotas.GetVerification(ctx, "a"); v == nil {
			t.Fatal("not persisted")
		}
	}
	status = 429
	body = `{}`
	p.PollAccount(ctx, "a")
	if v, _ := quotas.GetVerification(ctx, "a"); v == nil {
		t.Fatal("429 cleared verification")
	}
	accounts.UpdateStatus(ctx, "a", domain.AccountStatusError)
	status = 200
	body = `{"buckets":[{"bucketId":"gemini-weekly","remainingFraction":1}]}`
	if _, err := p.PollAccount(ctx, "a"); err != nil {
		t.Fatal(err)
	}
	if v, _ := quotas.GetVerification(ctx, "a"); v != nil {
		t.Fatal("successful live recheck did not clear")
	}
	acc, _ := accounts.GetByID(ctx, "a")
	if acc.Status != domain.AccountStatusError {
		t.Fatal("auth error revived")
	}
	accounts.UpdateStatus(ctx, "a", domain.AccountStatusDisabled)
	quotas.SetVerification(ctx, "a", "https://accounts.google.com/v")
	p.PollAccount(ctx, "a")
	acc, _ = accounts.GetByID(ctx, "a")
	if acc.Status != domain.AccountStatusDisabled {
		t.Fatal("manual disabled revived")
	}
	if v, _ := quotas.GetVerification(ctx, "a"); v == nil {
		t.Fatal("disabled skip cleared verification")
	}
}

func TestLocalSnapshotCannotClearVerification(t *testing.T) {
	ctx := context.Background()
	_, accounts, quotas := setupTestStore(t)
	accounts.Create(ctx, &domain.Account{ID: "a", Email: "a@test.invalid", Status: domain.AccountStatusActive})
	quotas.SetVerification(ctx, "a", "https://accounts.google.com/v")
	p, _ := NewPoller(accounts, quotas)
	if err := p.clearVerificationAfterRecheck(ctx, "a", time.Now().Add(time.Hour), false); err != nil {
		t.Fatal(err)
	}
	if v, _ := quotas.GetVerification(ctx, "a"); v == nil {
		t.Fatal("LS-only snapshot cleared verification")
	}
}

type failedQuotaReader struct{}

func (failedQuotaReader) Read([]byte) (int, error) { return 0, errors.New("fixture read failure") }
func TestVerificationInspectionRejectsOversizeAndReadErrors(t *testing.T) {
	ctx := context.Background()
	_, accounts, quotas := setupTestStore(t)
	accounts.Create(ctx, &domain.Account{ID: "a", Email: "a@test.invalid", Status: domain.AccountStatusActive})
	quotas.SetVerification(ctx, "a", "https://accounts.google.com/v")
	p, _ := NewPoller(accounts, quotas)
	for _, reader := range []io.Reader{strings.NewReader(strings.Repeat(" ", 1024*1024+1)), failedQuotaReader{}} {
		resp := &http.Response{Body: io.NopCloser(reader)}
		if err := p.inspectVerification(ctx, "a", resp); err == nil {
			t.Fatal("invalid response treated as successful inspection")
		}
		resp.Body.Close()
		if v, _ := quotas.GetVerification(ctx, "a"); v == nil {
			t.Fatal("invalid response cleared verification")
		}
	}
}
