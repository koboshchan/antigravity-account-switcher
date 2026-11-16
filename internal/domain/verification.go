package domain

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"
	"time"
)

// VerificationRequirement is independent of authentication and manual account state.
type VerificationRequirement struct {
	URL        string    `json:"url"`
	ObservedAt time.Time `json:"observed_at"`
}
type VerificationRepository interface {
	SetVerification(context.Context, string, string) error
	GetVerification(context.Context, string) (*VerificationRequirement, error)
	ClearVerificationBefore(context.Context, string, time.Time) error
}

// SafeVerificationURL permits only HTTPS Google-owned verification pages, never arbitrary ports or credentials.
func SafeVerificationURL(raw string) string {
	if len(raw) > 8192 {
		return ""
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.User != nil || u.Opaque != "" || u.Fragment != "" || (u.Port() != "" && u.Port() != "443") {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	if host != "google.com" && !strings.HasSuffix(host, ".google.com") && host != "antigravity.google" {
		return ""
	}
	return u.String()
}

// DetectVerification reads structured Google RPC, onboarding-operation and eligibility payloads.
// It intentionally never stores or displays arbitrary upstream messages.
func DetectVerification(body []byte) *VerificationRequirement {
	var root any
	if len(body) > 1024*1024 || json.Unmarshal(body, &root) != nil {
		return nil
	}
	var walk func(any) *VerificationRequirement
	walk = func(value any) *VerificationRequirement {
		switch v := value.(type) {
		case map[string]any:
			text := func(key string) string { s, _ := v[key].(string); return s }
			reason := strings.ToUpper(text("reason") + text("reasonCode"))
			link := ""
			for _, key := range []string{"validation_url", "validationUrl", "verification_url", "verificationUrl", "verifyURL", "verifyUrl", "appeal_url", "appealUrl"} {
				if s := text(key); s != "" {
					link = s
					break
				}
			}
			if meta, ok := v["metadata"].(map[string]any); ok {
				for _, key := range []string{"validation_url", "validationUrl", "verification_url", "verificationUrl", "verifyURL", "verifyUrl", "appeal_url", "appealUrl"} {
					if s, ok := meta[key].(string); ok && s != "" {
						link = s
						break
					}
				}
			}
			if strings.Contains(reason, "VALIDATION_REQUIRED") || strings.Contains(reason, "VERIFICATION_REQUIRED") || (link != "" && (text("@type") == "" || strings.HasSuffix(text("@type"), "/google.rpc.ErrorInfo"))) {
				return &VerificationRequirement{URL: SafeVerificationURL(link)}
			}
			for _, key := range []string{"error", "details", "ineligibleTiers", "response"} {
				if key == "ineligibleTiers" && usableEligibilityContext(v) {
					continue
				}
				if result := walk(v[key]); result != nil {
					return result
				}
			}
		case []any:
			for _, item := range v {
				if result := walk(item); result != nil {
					return result
				}
			}
		}
		return nil
	}
	return walk(root)
}

// A usable current/paid tier plus project, or an offered tier, wins over unrelated tier notices.
func usableEligibilityContext(v map[string]any) bool {
	project := false
	switch p := v["cloudaicompanionProject"].(type) {
	case string:
		project = strings.TrimSpace(p) != ""
	case map[string]any:
		for _, key := range []string{"id", "projectId", "name"} {
			if text, ok := p[key].(string); ok && strings.TrimSpace(text) != "" {
				project = true
			}
		}
	}
	if project {
		for _, key := range []string{"currentTier", "paidTier"} {
			if tier, ok := v[key].(map[string]any); ok && tier != nil {
				return true
			}
		}
	}
	if tiers, ok := v["allowedTiers"].([]any); ok {
		for _, raw := range tiers {
			if tier, ok := raw.(map[string]any); ok {
				if id, _ := tier["id"].(string); strings.TrimSpace(id) == "free-tier" {
					return true
				}
				if def, _ := tier["isDefault"].(bool); def {
					if id, _ := tier["id"].(string); strings.TrimSpace(id) != "" {
						return true
					}
				}
			}
		}
	}
	return false
}
