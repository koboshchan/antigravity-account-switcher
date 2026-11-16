package domain

import (
	"testing"
	"time"
)

func TestFamilyQuotaAllWindowsAndResetBoundary(t *testing.T) {
	now := time.Now().UTC()
	buckets := []*QuotaBucket{{BucketID: "gemini-5h", RemainingFraction: 1}, {BucketID: "gemini-weekly", RemainingFraction: 0, ResetTime: now.Add(time.Hour)}, {BucketID: "claude-weekly", RemainingFraction: 0}}
	if known, available := FamilyQuota(buckets, QuotaFamilyGemini, now); !known || available {
		t.Fatal("weekly zero ignored")
	}
	buckets[1].ResetTime = now
	if known, available := FamilyQuota(buckets, QuotaFamilyGemini, now); !known || !available {
		t.Fatal("reset equal to now not honored")
	}
	buckets[1].ResetTime = time.Time{}
	if _, available := FamilyQuota(buckets, QuotaFamilyGemini, now); available {
		t.Fatal("unknown reset invented recovery")
	}
}
