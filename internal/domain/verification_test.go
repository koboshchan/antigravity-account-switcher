package domain

import "testing"

func TestVerificationStructuredVariantsAndSafeLinks(t *testing.T) {
	for _, body := range []string{
		`{"error":{"details":[{"reason":"VALIDATION_REQUIRED","metadata":{"validation_url":"https://accounts.google.com/v?secret=x"}}]}}`,
		`{"ineligibleTiers":[{"reasonCode":"VALIDATION_REQUIRED","validationUrl":"https://antigravity.google/verify"}]}`,
		`{"done":true,"error":{"details":[{"reason":"VALIDATION_REQUIRED","metadata":{"verifyURL":"https://accounts.google.com/v"}}]}}`,
		`{"verifyURL":"https://accounts.google.com/v"}`,
	} {
		if got := DetectVerification([]byte(body)); got == nil || got.URL == "" {
			t.Fatalf("missed structured variant %s", body)
		}
	}
	if DetectVerification([]byte(`{"error":{"code":403,"message":"Permission denied"}}`)) != nil {
		t.Fatal("generic403 classified as verification")
	}
	for _, link := range []string{"http://accounts.google.com/v", "javascript:alert(1)", "https://google.com.evil.test/v", "https://evilgoogle.com/v", "https://user@accounts.google.com/v", "https://accounts.google.com:444/v", "https://accounts.google.com/v#secret", "https://evil.test/v"} {
		if SafeVerificationURL(link) != "" {
			t.Errorf("unsafe link accepted %q", link)
		}
	}
	if got := DetectVerification([]byte(`{"error":{"details":[{"reason":"VALIDATION_REQUIRED","metadata":{"validation_url":"javascript:bad"}}]}}`)); got == nil || got.URL != "" {
		t.Fatal("unsafe link should retain block without link")
	}
}

func TestEligibleTierContextWinsOverUnrelatedNotices(t *testing.T) {
	notice := `"ineligibleTiers":[{"reasonCode":"VALIDATION_REQUIRED","validationUrl":"https://accounts.google.com/v"}]`
	for _, context := range []string{`"cloudaicompanionProject":"project","currentTier":{"id":"free-tier"}`, `"cloudaicompanionProject":{"id":"project"},"paidTier":{"id":"pro"}`, `"allowedTiers":[{"id":"free-tier"}]`, `"allowedTiers":[{"id":"other","isDefault":true}]`} {
		if DetectVerification([]byte(`{`+context+`,`+notice+`}`)) != nil {
			t.Fatalf("usable context blocked %s", context)
		}
	}
	for _, context := range []string{`"currentTier":{"id":"free-tier"}`, `"cloudaicompanionProject":"project"`, `"allowedTiers":[{"id":"other"}]`} {
		if DetectVerification([]byte(`{`+context+`,`+notice+`}`)) == nil {
			t.Fatalf("unusable context accepted %s", context)
		}
	}
	for _, alias := range []string{"verification_url", "verificationUrl", "appealUrl"} {
		if got := DetectVerification([]byte(`{"error":{"details":[{"metadata":{"` + alias + `":"https://accounts.google.com/v"}}]}}`)); got == nil || got.URL == "" {
			t.Fatalf("missing metadata alias %s", alias)
		}
	}
}
