package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfig_BackwardCompatibility_DefaultsPreserved(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("ANTIGRAVITY_CONFIG_DIR", tmpDir)

	legacyJSON := `{
  "port": 8888,
  "db_path": "/tmp/legacy.db",
  "upstream_url": "https://daily-cloudcode-pa.googleapis.com",
  "quota_interval": "10m"
}`
	configPath := filepath.Join(tmpDir, ConfigFileName)
	if err := os.WriteFile(configPath, []byte(legacyJSON), 0o644); err != nil {
		t.Fatalf("failed to write legacy config: %v", err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatalf("failed to load legacy config: %v", err)
	}

	if cfg.Port != 8888 {
		t.Errorf("expected port 8888, got %d", cfg.Port)
	}
	if err := cfg.Validate(); err != nil {
		t.Errorf("expected loaded legacy config to be valid, got: %v", err)
	}
}

func TestParseBool(t *testing.T) {
	t.Parallel()

	truthy := []string{"1", "t", "T", "true", "True", "TRUE", "yes", "Yes", "YES", "y", "Y", "on", "On", "ON", "  true  ", " yes "}
	for _, s := range truthy {
		val, err := ParseBool(s)
		if err != nil {
			t.Errorf("expected ParseBool(%q) to succeed, got error: %v", s, err)
		}
		if !val {
			t.Errorf("expected ParseBool(%q) to be true, got false", s)
		}
	}

	falsy := []string{"0", "f", "F", "false", "False", "FALSE", "no", "No", "NO", "n", "N", "off", "Off", "OFF", "  false  ", " no "}
	for _, s := range falsy {
		val, err := ParseBool(s)
		if err != nil {
			t.Errorf("expected ParseBool(%q) to succeed, got error: %v", s, err)
		}
		if val {
			t.Errorf("expected ParseBool(%q) to be false, got true", s)
		}
	}

	invalid := []string{"", "   ", "2", "-1", "maybe", "enabled", "disabled", "null", "foo"}
	for _, s := range invalid {
		val, err := ParseBool(s)
		if err == nil {
			t.Errorf("expected ParseBool(%q) to fail, got value: %t", s, val)
		}
	}
}

func TestResolveAntigravityBin_ExplicitOverride(t *testing.T) {
	tmpDir := t.TempDir()
	fakeBin := filepath.Join(tmpDir, "antigravity")
	if err := os.WriteFile(fakeBin, []byte("#!/bin/sh\necho test\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	resolved, err := ResolveAntigravityBin(fakeBin)
	if err != nil {
		t.Fatalf("expected resolution to succeed, got %v", err)
	}

	absFake, _ := filepath.Abs(fakeBin)
	if resolved != absFake {
		t.Errorf("expected %s, got %s", absFake, resolved)
	}
}

func TestFindAntigravityIcon(t *testing.T) {
	tmpDir := t.TempDir()
	binPath := filepath.Join(tmpDir, "Antigravity-x64", "antigravity")
	if err := os.MkdirAll(filepath.Dir(binPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binPath, []byte("fake binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	iconPath := filepath.Join(tmpDir, "icon.png")
	if err := os.WriteFile(iconPath, []byte("fake icon png"), 0o644); err != nil {
		t.Fatal(err)
	}

	found := FindAntigravityIcon(binPath)
	if found != iconPath {
		t.Errorf("expected found icon %s, got %s", iconPath, found)
	}
}
