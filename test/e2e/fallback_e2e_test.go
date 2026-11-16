package e2e

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Muriel-Gasparini/antigravity-account-switcher/internal/config"
	"github.com/Muriel-Gasparini/antigravity-account-switcher/internal/domain"
	"github.com/Muriel-Gasparini/antigravity-account-switcher/test/mocks"
)

const (
	// eventTypeModelFallback is the domain telemetry event type for intra-account model fallback (Feature 4).
	eventTypeModelFallback = domain.EventType("model_fallback")

	defaultPrimaryModel   = "gemini-3.8-flash-high"
	defaultSecondaryModel = "claude-3-5-sonnet"
)

// --- Shared Test Helpers ---

func seedTestAccount(t *testing.T, env *TestEnvironment, id, email, token string, isActive bool, status domain.AccountStatus) *domain.Account {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()
	acc := &domain.Account{
		ID:          id,
		Email:       email,
		AccessToken: token,
		IsActive:    isActive,
		Status:      status,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := env.AccountRepo.Create(ctx, acc); err != nil {
		t.Fatalf("seed account %s: %v", id, err)
	}
	return acc
}

func sendProxyRequest(t *testing.T, client *http.Client, targetURL, method, body string, headers map[string]string) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequest(method, targetURL, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if req.Header.Get("Content-Type") == "" && body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client Do: %v", err)
	}
	respBody, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	return resp, string(respBody)
}

func sendProxySSERequest(t *testing.T, client *http.Client, targetURL, body string, headers map[string]string) (*http.Response, []string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, targetURL, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new SSE request: %v", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "text/event-stream")
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("client Do SSE: %v", err)
	}
	chunks, err := mocks.ParseSSEChunks(resp.Body)
	_ = resp.Body.Close()
	if err != nil {
		t.Fatalf("ParseSSEChunks: %v", err)
	}
	return resp, chunks
}

func runCLI(t *testing.T, extraEnv []string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("go", append([]string{"run", "../../cmd/antigravity-account-switcher"}, args...)...)
	cmd.Env = append(os.Environ(), extraEnv...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func readConfigFile(t *testing.T, configDir string) map[string]any {
	t.Helper()
	cfgPath := filepath.Join(configDir, config.ConfigFileName)
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("failed to read config file at %s: %v", cfgPath, err)
	}
	var res map[string]any
	if err := json.Unmarshal(data, &res); err != nil {
		t.Fatalf("failed to unmarshal config JSON: %v", err)
	}
	return res
}

func writeConfigFile(t *testing.T, configDir string, content map[string]any) {
	t.Helper()
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatalf("create config dir: %v", err)
	}
	data, err := json.MarshalIndent(content, "", "  ")
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	cfgPath := filepath.Join(configDir, config.ConfigFileName)
	if err := os.WriteFile(cfgPath, data, 0o644); err != nil {
		t.Fatalf("write config file: %v", err)
	}
}

// ============================================================================
// TIER 1: FEATURE COVERAGE (Isolated Feature Verification, Features 1-12)
// Threshold: ≥5 per feature (Total ≥ 60)
// ============================================================================

// Feature 1: Configuration Schema & Validation

// Feature 2: Environment Variable Overrides

// Feature 3: CLI Configuration Management

// Feature 4: Domain Telemetry Event

// Feature 5: Model Inspection (Path & JSON Body)

// Feature 6: In-Flight Request Body & Header Rewriting

// Feature 7: Model Family Categorization

// Feature 8: Predictive Quota Fallback

// Feature 9: Reactive 429 Fallback

// Feature 10: Account Rotation Gating

// Feature 11: Anti-Stampede Concurrency Protection

// Feature 12: SSE Streaming Preservation

// ============================================================================
// TIER 2: BOUNDARY & CORNER CASES
// Threshold: ≥5 per feature boundary (Total ≥ 60)
// ============================================================================

// ============================================================================
// TIER 3: CROSS-FEATURE COMBINATIONS
// ============================================================================

// TestTier3_Combination_PredictiveThenReactive:
// Account 1 has 0% primary quota -> predictive check rewrites to secondary.
// Secondary also receives 429 from upstream -> reactive failover rotates to Account 2.
// Account 2 succeeds on primary model.

// TestTier3_Combination_FallbackDisabledVsEnabled compares routing decisions.

// TestTier3_Combination_DoubleExhaustionToRotation:
// Primary 429 -> Secondary 429 -> Account 2 200 OK.

// TestTier3_Combination_EnvOverrideVsConfigFile:
// Config file has fallback disabled, environment variable enables it.

// TestTier3_Combination_QuotaRestoredDuringFallback:
// Account 1 was using secondary model; quota is restored; subsequent request uses primary again.

// TestTier3_Combination_MetricsCaptureOnRewrittenModel:
// SSE metrics correctly recorded after fallback rewriting.

// TestTier3_Combination_ConcurrentPredictiveAndReactive:
// Interleaved predictive 0% and reactive 429 requests.

// TestTier3_Combination_AllAccountsDoubleExhausted:
// Entire account pool fails both tiers -> returns 429 to client.

// ============================================================================
// TIER 4: REAL-WORLD WORKLOAD SCENARIOS
// Threshold: ≥6 realistic application scenarios
// ============================================================================

// Scenario 1: High-volume developer chat session with exhausted Claude quota
// smoothly downgraded to Gemini Flash without session interruption.

// Scenario 2: Predictive quota optimization prevents unnecessary 429 round-trips
// when 5-hour rolling quota is already depleted.

// Scenario 3: Burst of 50 concurrent requests when primary model hits 0%
// handled without stampede or duplicate account rotations.

// Scenario 4: Exhaustion of both primary and secondary models cleanly
// triggers rotation to Account 2 and resets to primary model.

// Scenario 5: Strict enterprise mode with fallback disabled preserves
// instant rotation on primary model exhaustion.

// Scenario 6: Large prompt (10MB) with embedded word 'model' in prompt body
// rewritten accurately without corruption or allocation explosion.

// Scenario 7: Stream interruption resilience: client drops connection
// while fallback retry is in-flight.

// Scenario 8: Session continuity across multi-turn conversation.
