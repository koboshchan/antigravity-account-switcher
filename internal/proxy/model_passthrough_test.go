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
)

func TestProxyPreservesClientModelWithDashboardDefaults(t *testing.T) {
	// IDs are arbitrary opaque values, not an allowlist or availability claim.
	models := []string{
		"gemini-4-argon-low", "gemini-4-argon-medium", "gemini-4-argon-high",
		"gemini-4-hydrogen-low", "gemini-4-hydrogen-high",
		"gemini-4-helium", "gemini-4-helium-high", "helium",
		"claude-opus-5-5-high", "claude-sonnet-5-5-medium",
		"gemini-3.8-flash-low", "orchestrator", "future-unknown-model",
	}
	for _, model := range models {
		for _, source := range []string{"body", "path", "query"} {
			t.Run(model+"/"+source, func(t *testing.T) {
				_, repo, _, _ := setupTestDB(t)
				now := time.Now().UTC()
				acc := &domain.Account{ID: "acc", Email: "test@example.com", AccessToken: "test",
					IsActive: true, Status: domain.AccountStatusActive, CreatedAt: now, UpdatedAt: now}
				if err := repo.Create(context.Background(), acc); err != nil {
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
				called := false
				upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					called = true
					gotBody, _ := io.ReadAll(r.Body)
					got, _, _ := ExtractModelFromRequest(r, gotBody)
					if got != model {
						t.Errorf("model changed from %q to %q", model, got)
					}
					if string(gotBody) != body {
						t.Error("request body changed")
					}
					if r.URL.RequestURI() != path {
						t.Error("request URL changed")
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"ok":true}`)
				}))
				defer upstream.Close()
				engine := NewFailoverEngine(repo, nil, nil)
				handler, err := NewProxyHandler(repo, WithTargetURL(upstream.URL), WithFailoverEngine(engine), WithMaxRetries(0))
				if err != nil {
					t.Fatal(err)
				}
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
				if !called || response.Code != 200 {
					t.Fatalf("upstream called=%t status=%d", called, response.Code)
				}
			})
		}
	}
}
