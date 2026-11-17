package proxy

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/Muriel-Gasparini/antigravity-account-switcher/internal/domain"
)

// A quota snapshot is not evidence of a license for every model in the pool.
func isSubscriptionRequired(status int, body []byte) bool {
	if status != http.StatusForbidden {
		return false
	}
	var response struct {
		Error struct {
			Details []struct {
				Reason string `json:"reason"`
			} `json:"details"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &response) != nil {
		return false
	}
	for _, detail := range response.Error.Details {
		if detail.Reason == "SUBSCRIPTION_REQUIRED" {
			return true
		}
	}
	return false
}

func entitlementModelKey(model string) string {
	return strings.TrimPrefix(strings.ToLower(strings.TrimSpace(model)), "models/")
}

// Back off only the rejected model, not Gemini/Argon quota or the whole account.
// The bounded retry delay allows newly granted subscriptions to recover.
func (f *FailoverEngine) handleSubscriptionRequired(ctx context.Context, acc *domain.Account, model string) (*domain.Account, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.modelDenied == nil {
		f.modelDenied = make(map[string]map[string]time.Time)
	}
	if f.modelDenied[acc.ID] == nil {
		f.modelDenied[acc.ID] = make(map[string]time.Time)
	}
	f.modelDenied[acc.ID][entitlementModelKey(model)] = time.Now().Add(30 * time.Minute)
	return f.selectFamilyAccountLocked(ctx, model, acc.ID)
}
