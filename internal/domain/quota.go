package domain

import (
	"context"
	"strings"
	"time"
)

// QuotaWindow represents the sliding limit window (daily or weekly).
type QuotaWindow string

const (
	// QuotaWindowDaily denotes daily recurring quota limits.
	QuotaWindowDaily QuotaWindow = "DAILY"
	// QuotaWindowWeekly denotes weekly recurring quota limits.
	QuotaWindowWeekly QuotaWindow = "WEEKLY"
)

// QuotaBucket models a specific model/feature quota limit bucket for an account.
type QuotaBucket struct {
	AccountID         string      `json:"account_id"`
	BucketID          string      `json:"bucket_id"`
	DisplayName       string      `json:"display_name"`
	Window            QuotaWindow `json:"window"`
	RemainingFraction float64     `json:"remaining_fraction"` // 0.0 (exhausted) to 1.0 (full)
	RemainingAmount   int64       `json:"remaining_amount"`   // Available requests or token units
	ResetTime         time.Time   `json:"reset_time"`         // Point in time when quota refreshes
	UpdatedAt         time.Time   `json:"updated_at"`
}

// IsExhausted treats every positive reported fraction as usable. RemainingAmount
// is optional in provider responses, so its default zero cannot imply exhaustion.
func (q *QuotaBucket) IsExhausted() bool {
	return q.RemainingFraction <= 0.0
}

// HasReset determines if the quota reset time has passed relative to now.
func (q *QuotaBucket) HasReset(now time.Time) bool {
	return !q.ResetTime.IsZero() && !now.Before(q.ResetTime)
}

// QuotaRepository defines persistence operations for QuotaBucket entities.
type QuotaRepository interface {
	// UpsertBuckets inserts or updates multiple quota buckets atomically.
	UpsertBuckets(ctx context.Context, buckets []*QuotaBucket) error

	// GetByAccountID retrieves all quota buckets associated with an account.
	GetByAccountID(ctx context.Context, accountID string) ([]*QuotaBucket, error)

	// ListAll returns a mapping of accountID -> list of QuotaBuckets.
	ListAll(ctx context.Context) (map[string][]*QuotaBucket, error)

	// DeleteByAccountID removes all quota buckets for a specific account.
	DeleteByAccountID(ctx context.Context, accountID string) error

	// GetExhaustedAccountsPastReset returns IDs of accounts marked 'exhausted'
	// whose buckets have all passed their reset time.
	GetExhaustedAccountsPastReset(ctx context.Context, now time.Time) ([]string, error)
}

// QuotaFamily is the independently limited provider pool.
type QuotaFamily string

const (
	QuotaFamilyArgon     QuotaFamily = "argon"
	QuotaFamilyGemini    QuotaFamily = "gemini"
	QuotaFamilyClaudeGPT QuotaFamily = "claude_gpt"
)

// BucketFamily recognizes provider-level quota labels without guessing unknown buckets.
func BucketFamily(b *QuotaBucket) QuotaFamily {
	s := strings.ToLower(b.BucketID + " " + b.DisplayName)
	if strings.Contains(s, "argon") {
		return QuotaFamilyArgon
	}
	if strings.Contains(s, "claude") || strings.Contains(s, "gpt") || strings.Contains(s, "3p") {
		return QuotaFamilyClaudeGPT
	}
	if strings.Contains(s, "gemini") || strings.Contains(s, "gemma") {
		return QuotaFamilyGemini
	}
	return ""
}

// FamilyQuota evaluates every relevant window. One blocked weekly window outweighs
// a healthy burst window. Missing data is not proof that a legacy exhausted account recovered.
func FamilyQuota(buckets []*QuotaBucket, family QuotaFamily, now time.Time) (known, available bool) {
	available = true
	for _, b := range buckets {
		if BucketFamily(b) != family {
			continue
		}
		known = true
		if b.IsExhausted() && !b.HasReset(now) {
			available = false
		}
	}
	return
}

// FamilyCooldownRepository stores reactive exhaustion separately from measured quotas.
// ClearFamilyCooldownBefore only clears observations older than a successful poll.
type FamilyCooldownRepository interface {
	SetFamilyCooldown(context.Context, string, QuotaFamily, time.Time) error
	GetFamilyCooldown(context.Context, string, QuotaFamily) (time.Time, error)
	ClearFamilyCooldownBefore(context.Context, string, QuotaFamily, time.Time) error
}

// QuotaAvailabilityRepository persists quota-endpoint permission failures independently
// of authentication or a user's manual disable. Clearing requires a later live poll.
type QuotaAvailabilityRepository interface {
	SetQuotaUnavailable(context.Context, string) error
	IsQuotaUnavailable(context.Context, string) (bool, error)
	ClearQuotaUnavailableBefore(context.Context, string, time.Time) error
}

// SharedGeminiRepository stores topology, never synthetic quota values.
type SharedGeminiRepository interface {
	SetSharedGemini(context.Context, string, bool) error
	GetSharedGemini(context.Context, string) (bool, error)
}

// FamilyActiveRepository stores independent automatic routing selections.
type FamilyActiveRepository interface {
	GetFamilyActive(context.Context, QuotaFamily) (string, error)
	SetFamilyActive(context.Context, QuotaFamily, string) error
}
