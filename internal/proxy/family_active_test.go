package proxy

import (
	"context"
	"fmt"
	"github.com/Muriel-Gasparini/antigravity-account-switcher/internal/domain"
	"github.com/Muriel-Gasparini/antigravity-account-switcher/internal/store/sqlite"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestIndependentFamilyActiveTokensAndAutomaticRotation(t *testing.T) {
	ctx := context.Background()
	db, accounts, _, _ := setupTestDB(t)
	quotas := sqlite.NewQuotaRepository(db)
	models := map[domain.QuotaFamily]string{domain.QuotaFamilyGemini: "gemini-3.8-flash-high", domain.QuotaFamilyClaudeGPT: "claude-sonnet-5", domain.QuotaFamilyArgon: "gemini-4-argon"}
	for _, id := range []string{"a", "b", "c", "d"} {
		if err := accounts.Create(ctx, &domain.Account{ID: id, Email: id + "@test.invalid", AccessToken: "token-" + id, TokenExpiry: time.Now().Add(time.Hour), Status: domain.AccountStatusActive, IsActive: id == "a"}); err != nil {
			t.Fatal(err)
		}
		for family := range models {
			if err := quotas.UpsertBuckets(ctx, []*domain.QuotaBucket{{AccountID: id, BucketID: string(family) + "-weekly", RemainingFraction: 1}}); err != nil {
				t.Fatal(err)
			}
		}
	}
	for family, id := range map[domain.QuotaFamily]string{domain.QuotaFamilyGemini: "a", domain.QuotaFamilyClaudeGPT: "b", domain.QuotaFamilyArgon: "c"} {
		if err := quotas.SetFamilyActive(ctx, family, id); err != nil {
			t.Fatal(err)
		}
	}
	var gemini429 atomic.Bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		model, _, _ := ExtractModelFromRequest(r, body)
		family := modelFamily(model)
		want := ""
		switch family {
		case domain.QuotaFamilyGemini:
			want = "a"
		case domain.QuotaFamilyClaudeGPT:
			want = "b"
		case domain.QuotaFamilyArgon:
			want = "c"
		default:
			t.Errorf("model changed %q", model)
		}
		auth := r.Header.Get("Authorization")
		if family == domain.QuotaFamilyGemini && gemini429.Load() {
			if auth == "Bearer token-a" {
				w.WriteHeader(429)
				fmt.Fprint(w, `{"error":"quota"}`)
				return
			}
			// Automatic pool order selects b for Gemini only. Claude remains on b.
			want = "b"
		}
		if auth != "Bearer token-"+want {
			t.Errorf("family=%s model=%s bearer=%q want=%s", family, model, auth, want)
		}
		if model != models[family] {
			t.Errorf("rewrote requested model %q", model)
		}
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer upstream.Close()
	engine := NewFailoverEngine(accounts, nil, nil, WithQuotaRepository(quotas))
	handler, err := NewProxyHandler(accounts, WithTargetURL(upstream.URL), WithFailoverEngine(engine))
	if err != nil {
		t.Fatal(err)
	}
	request := func(model string) {
		r := httptest.NewRequest("POST", "/v1internal/models/"+model+":generateContent", strings.NewReader(`{"model":"`+model+`","contents":[{"text":"unchanged"}]}`))
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Errorf("%s status=%d body=%s", model, w.Code, w.Body.String())
		}
	}
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		for _, model := range models {
			wg.Add(1)
			go func(m string) { defer wg.Done(); request(m) }(model)
		}
	}
	wg.Wait()
	gemini429.Store(true)
	request(models[domain.QuotaFamilyGemini])
	request(models[domain.QuotaFamilyClaudeGPT])
	request(models[domain.QuotaFamilyArgon])
	// Recreate the whole routing engine: selections must come from persistent repo.
	engine = NewFailoverEngine(accounts, nil, nil, WithQuotaRepository(sqlite.NewQuotaRepository(db)))
	for family, want := range map[domain.QuotaFamily]string{domain.QuotaFamilyGemini: "b", domain.QuotaFamilyClaudeGPT: "b", domain.QuotaFamilyArgon: "c"} {
		got, err := engine.ResolveForModel(ctx, models[family])
		if err != nil || got == nil || got.ID != want {
			t.Fatalf("persisted %s got=%v err=%v want=%s", family, got, err, want)
		}
	}
	legacy, _ := accounts.GetActive(ctx)
	if legacy.ID != "a" {
		t.Fatalf("family traffic mutated global selection %s", legacy.ID)
	}
	// Preflight exhausted Argon selects another account without affecting Gemini/Claude pointers.
	_ = quotas.UpsertBuckets(ctx, []*domain.QuotaBucket{{AccountID: "c", BucketID: "argon-weekly", RemainingFraction: 0}})
	next, err := engine.ResolveForModel(ctx, models[domain.QuotaFamilyArgon])
	if err != nil || next == nil || next.ID == "c" {
		t.Fatalf("Argon did not switch automatically: %v %v", next, err)
	}
	for _, family := range []domain.QuotaFamily{domain.QuotaFamilyGemini, domain.QuotaFamilyClaudeGPT} {
		id, _ := quotas.GetFamilyActive(ctx, family)
		if id != "b" {
			t.Errorf("Argon overwrote %s pointer %s", family, id)
		}
	}
	// Account-wide403 invalidates every pointer referencing it, not unrelated ones.
	argonID, _ := quotas.GetFamilyActive(ctx, domain.QuotaFamilyArgon)
	if err := quotas.SetQuotaUnavailable(ctx, "b"); err != nil {
		t.Fatal(err)
	}
	for _, family := range []domain.QuotaFamily{domain.QuotaFamilyGemini, domain.QuotaFamilyClaudeGPT} {
		id, _ := quotas.GetFamilyActive(ctx, family)
		if id != "" {
			t.Errorf("403 retained %s pointer %s", family, id)
		}
	}
	id, _ := quotas.GetFamilyActive(ctx, domain.QuotaFamilyArgon)
	if id != argonID {
		t.Fatal("403 deleted unrelated pointer")
	}
	// No family quota remains: fail closed instead of silently changing model/family.
	for _, id := range []string{"a", "b", "c", "d"} {
		_ = quotas.UpsertBuckets(ctx, []*domain.QuotaBucket{{AccountID: id, BucketID: "argon-weekly", RemainingFraction: 0}})
	}
	if got, err := engine.ResolveForModel(ctx, models[domain.QuotaFamilyArgon]); err == nil || got != nil {
		t.Fatalf("all exhausted should fail: %v %v", got, err)
	}
}
