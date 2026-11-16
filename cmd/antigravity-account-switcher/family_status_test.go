package main

import (
	"github.com/Muriel-Gasparini/antigravity-account-switcher/internal/domain"
	"strings"
	"testing"
	"time"
)

func TestFamilyQuotaStatusDoesNotProjectGeminiIntoArgon(t *testing.T) {
	now := time.Now().UTC()
	acc := &domain.Account{Status: domain.AccountStatusActive}
	buckets := []*domain.QuotaBucket{{BucketID: "gemini-weekly", RemainingFraction: 1}}
	text := formatFamilyQuotaStatus(acc, domain.QuotaFamilyArgon, buckets, time.Time{}, nil, now)
	if !strings.Contains(text, "unknown") || strings.Contains(text, "eligible") {
		t.Fatalf("Argon quota fabricated: %s", text)
	}
	reset := now.Add(time.Hour)
	text = formatFamilyQuotaStatus(acc, domain.QuotaFamilyArgon, buckets, reset, nil, now)
	if !strings.Contains(text, "cooldown") || !strings.Contains(text, reset.Format(time.RFC3339)) {
		t.Fatalf("missing reactive reset: %s", text)
	}
	buckets = append(buckets, &domain.QuotaBucket{BucketID: "gemini-4-argon-weekly", RemainingFraction: 0, ResetTime: reset})
	text = formatFamilyQuotaStatus(acc, domain.QuotaFamilyArgon, buckets, time.Time{}, nil, now)
	if !strings.Contains(text, "blocked") || !strings.Contains(text, reset.Format(time.RFC3339)) {
		t.Fatalf("missing independent Argon limit: %s", text)
	}
}
