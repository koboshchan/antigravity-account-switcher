package web

import (
	"strings"
	"testing"

	"github.com/Muriel-Gasparini/antigravity-account-switcher/internal/config"
	"github.com/Muriel-Gasparini/antigravity-account-switcher/internal/domain"
)

func TestModelCatalogPreservesLiveModelsAndAddsSuggestions(t *testing.T) {
	live := &domain.ModelInfo{ID: "gemini-4-argon", DisplayName: "Account-specific Argon", Category: "gemini", Recommended: true}
	spare := make([]*domain.ModelInfo, 1, 64)
	spare[0] = live
	api := &APIHandler{appConfig: &config.Config{ModelPrimary: "future-model-7", ModelSecondary: "claude-private"}}
	models := api.ensureConfiguredModelsPresent(spare)
	byID := map[string]*domain.ModelInfo{}
	for _, model := range models {
		if byID[model.ID] != nil {
			t.Fatalf("duplicate model %s", model.ID)
		}
		byID[model.ID] = model
	}
	if byID[live.ID] != live || live.DisplayName != "Account-specific Argon" {
		t.Fatal("live Google model metadata must take precedence")
	}
	for _, id := range []string{"claude-opus-5-5", "claude-sonnet-5-5", "gemini-3.8-flash-low", "gemini-4-hydrogen-high", "gemini-4-helium", "orchestrator"} {
		m := byID[id]
		if m == nil || !strings.Contains(m.DisplayName, "Suggested") || m.Recommended {
			t.Errorf("%s must be selectable but clearly unverified", id)
		}
	}
	if byID["future-model-7"].Category != "unknown" || byID["claude-private"].Category != "claude_gpt" {
		t.Fatal("configured custom IDs must remain available with accurate categories")
	}
	if spare[:cap(spare)][1] != nil {
		t.Fatal("catalog merge mutated caller backing array")
	}
}
