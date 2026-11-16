package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemovedFallbackFieldsAreIgnoredAndNotReemitted(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ANTIGRAVITY_CONFIG_DIR", dir)
	if err := os.WriteFile(filepath.Join(dir, ConfigFileName), []byte(`{"port":8181,"model_primary":{"invalid":"old"},"model_secondary":42,"fallback_secondary_enabled":"old-value"}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != 8181 {
		t.Fatal("lost saved port")
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, removed := range []string{"model_primary", "model_secondary", "fallback_secondary_enabled"} {
		if strings.Contains(string(data), removed) {
			t.Errorf("reemitted removed %s", removed)
		}
	}
	if DefaultConfig().Port != 8080 {
		t.Fatal("fixed default changed")
	}
}
