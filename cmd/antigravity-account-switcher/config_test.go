package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Muriel-Gasparini/antigravity-account-switcher/internal/config"
)

// runCLIInSubprocess executes runConfig in a child test process to test commands that invoke os.Exit(1).
func runCLIInSubprocess(t *testing.T, configDir string, args ...string) (stdout string, stderr string, exitCode int) {
	t.Helper()

	cmdArgs := []string{"-test.run=TestCLI_Config_SubprocessEntry", "--"}
	cmdArgs = append(cmdArgs, args...)

	cmd := exec.Command(os.Args[0], cmdArgs...)
	cmd.Env = append(os.Environ(),
		"ANTIGRAVITY_TEST_SUBPROCESS=1",
		"ANTIGRAVITY_CONFIG_DIR="+configDir,
	)

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err := cmd.Run()
	exitCode = 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			t.Fatalf("unexpected error running CLI subprocess: %v", err)
		}
	}

	return outBuf.String(), errBuf.String(), exitCode
}

// TestCLI_Config_SubprocessEntry is the child process hook used by runCLIInSubprocess.
func TestCLI_Config_SubprocessEntry(t *testing.T) {
	if os.Getenv("ANTIGRAVITY_TEST_SUBPROCESS") != "1" {
		return
	}

	args := []string{}
	for i, arg := range os.Args {
		if arg == "--" {
			args = os.Args[i+1:]
			break
		}
	}

	runConfig(args)
	os.Exit(0)
}

func TestCLI_Config_Credentials_SetAndGetRedacted(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("ANTIGRAVITY_CONFIG_DIR", tmpDir)

	var stdout, stderr bytes.Buffer

	// Set client_id
	code := executeConfig([]string{"set", "client_id", "test-client-id-123.apps.googleusercontent.com"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected code 0, got %d: %s", code, stderr.String())
	}

	// Get client_id
	stdout.Reset()
	stderr.Reset()
	code = executeConfig([]string{"get", "client_id"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected code 0, got %d", code)
	}
	if strings.TrimSpace(stdout.String()) != "test-client-id-123.apps.googleusercontent.com" {
		t.Errorf("expected test-client-id-123.apps.googleusercontent.com, got %s", stdout.String())
	}

	// Set client_secret: verify confirmation masks the secret
	stdout.Reset()
	stderr.Reset()
	code = executeConfig([]string{"set", "client_secret", "test-secret-456"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected code 0, got %d: %s", code, stderr.String())
	}
	if strings.Contains(stdout.String(), "test-secret-456") {
		t.Errorf("expected set output to not contain raw secret, got: %s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "[configured]") {
		t.Errorf("expected set output to mention [configured], got: %s", stdout.String())
	}

	// Verify file permissions on config.json are 0600
	cfgPath := filepath.Join(tmpDir, "config.json")
	if fi, err := os.Stat(cfgPath); err == nil {
		perm := fi.Mode().Perm()
		if perm != 0o600 {
			t.Errorf("expected config file permissions 0600, got %o", perm)
		}
	} else {
		t.Fatalf("failed to stat config file: %v", err)
	}

	// Get client_secret (redacted in output)
	stdout.Reset()
	stderr.Reset()
	code = executeConfig([]string{"get", "client_secret"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected code 0, got %d", code)
	}
	if strings.TrimSpace(stdout.String()) != "[configured]" {
		t.Errorf("expected [configured], got %s", stdout.String())
	}

	// Verify env override does not leak into saved config on disk
	t.Setenv("ANTIGRAVITY_CLIENT_SECRET", "transient-env-secret-should-not-persist")
	stdout.Reset()
	stderr.Reset()
	code = executeConfig([]string{"set", "port", "8888"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected code 0, got %d: %s", code, stderr.String())
	}
	diskCfg, err := config.LoadDiskConfig()
	if err != nil {
		t.Fatalf("failed to load disk config: %v", err)
	}
	if diskCfg.ClientSecret != "test-secret-456" {
		t.Errorf("expected disk config to retain test-secret-456 without env leakage, got: %s", diskCfg.ClientSecret)
	}

	// List config
	stdout.Reset()
	stderr.Reset()
	code = executeConfig([]string{"list"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected code 0, got %d", code)
	}
	if !strings.Contains(stdout.String(), "client_id:") || !strings.Contains(stdout.String(), "client_secret:") {
		t.Errorf("expected list output to contain client_id and client_secret, got:\n%s", stdout.String())
	}
}
