package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestConfig_BoundaryPorts tests port numbers at and beyond limits.
func TestConfig_BoundaryPorts(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		port    int
		wantErr bool
	}{
		{-1000, true},
		{-1, true},
		{0, true},
		{1, false},
		{80, false},
		{443, false},
		{8080, false},
		{65535, false},
		{65536, true},
		{100000, true},
	}

	for _, tc := range testCases {
		cfg := DefaultConfig()
		cfg.Port = tc.port
		err := cfg.Validate()
		if tc.wantErr && err == nil {
			t.Errorf("Port %d: expected validation error, got nil", tc.port)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("Port %d: expected valid, got error: %v", tc.port, err)
		}
	}
}

// TestConfig_ModelUnicodeAndExtremeStrings tests unicode, emojis, RTL, and huge strings.

// TestConfig_WhitespaceAndEmptyVariations tests various whitespace characters.

// TestConfig_CaseInsensitiveCollisions tests casing folding under various representations.

// TestConfig_MalformedJSONFiles tests how Load handles malformed, unreadable, or invalid JSON.
func TestConfig_MalformedJSONFiles(t *testing.T) {
	malformedCases := []struct {
		name    string
		content string
	}{
		{"empty file", ""},
		{"syntax error", "{port: 8080}"},
		{"truncated json", `{"port": 8080, "model_primary":`},
		{"array json", `[1, 2, 3]`},
		{"type mismatch bool as string", `{"open_browser": "not_a_bool"}`},
		{"type mismatch port as string", `{"port": "eighty-eighty"}`},
		{"huge invalid number for port", `{"port": 9999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999999}`},
	}

	for _, mc := range malformedCases {
		t.Run(mc.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			t.Setenv("ANTIGRAVITY_CONFIG_DIR", tmpDir)
			cfgFile := filepath.Join(tmpDir, ConfigFileName)
			if err := os.WriteFile(cfgFile, []byte(mc.content), 0o644); err != nil {
				t.Fatal(err)
			}

			cfg, err := Load()
			if err == nil {
				t.Errorf("expected error loading malformed JSON %q, got cfg: %+v", mc.name, cfg)
			}
		})
	}
}

// TestConfig_EnvVarBoundaryAndFuzzing tests extreme and invalid env vars.

// TestConfig_ParseBool_Fuzz tests a comprehensive corpus of inputs for ParseBool.
func TestConfig_ParseBool_Fuzz(t *testing.T) {
	t.Parallel()

	validTrue := []string{
		"1", "t", "T", "true", "True", "TRUE", "TrUe",
		"yes", "Yes", "YES", "yEs", "y", "Y",
		"on", "On", "ON",
		" 1 ", "\ttrue\t", "\nyes\n", "  on  ",
	}
	for _, s := range validTrue {
		b, err := ParseBool(s)
		if err != nil || !b {
			t.Errorf("ParseBool(%q): expected (true, nil), got (%t, %v)", s, b, err)
		}
	}

	validFalse := []string{
		"0", "f", "F", "false", "False", "FALSE", "FaLsE",
		"no", "No", "NO", "nO", "n", "N",
		"off", "Off", "OFF",
		" 0 ", "\tfalse\t", "\nno\n", "  off  ",
	}
	for _, s := range validFalse {
		b, err := ParseBool(s)
		if err != nil || b {
			t.Errorf("ParseBool(%q): expected (false, nil), got (%t, %v)", s, b, err)
		}
	}

	invalids := []string{
		"", " ", "   ", "\t", "\n",
		"2", "-1", "00", "01", "10",
		"null", "nil", "none", "undefined",
		"enable", "enabled", "disable", "disabled",
		"true1", "false0", "yes please", "no thanks",
		"true\x00", "false\x00",
		"yess", "noo",
	}
	for _, s := range invalids {
		b, err := ParseBool(s)
		if err == nil {
			t.Errorf("ParseBool(%q): expected error, got value %t", s, b)
		}
	}
}

// TestConfig_ConcurrentAccess_Race runs 100 goroutines validating, default-loading, and parsing.
