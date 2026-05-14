package pii

import (
	"testing"
)

func TestDetectEmail(t *testing.T) {
	m := Detect("contact us at hello@example.com today")
	if len(m) != 1 || m[0].Type != TypeEmail {
		t.Errorf("got %+v, want EMAIL", m)
	}
}

func TestDetectCreditCardLuhn(t *testing.T) {
	// Visa test number passing Luhn.
	if m := Detect("card: 4111 1111 1111 1111"); len(m) != 1 || m[0].Type != TypeCreditCard {
		t.Errorf("got %+v, want CREDIT_CARD", m)
	}
	// 16 digits but failing Luhn — should not match.
	if m := Detect("card: 4111 1111 1111 1112"); len(m) != 0 {
		t.Errorf("luhn-invalid surface matched %+v", m)
	}
}

func TestDetectAPIKeyEntropy(t *testing.T) {
	// Realistic high-entropy secret (base64-ish, ~40 chars).
	high := "AKIAIOSFODNN7EXAMPLEpQRsTuVwXyZ123456abcd"
	if m := Detect(high); len(m) == 0 || m[0].Type != TypeAPIKey {
		t.Errorf("expected API key match, got %+v", m)
	}
	// Low entropy / dictionary-like strings shouldn't trip the entropy gate.
	if m := Detect("the quick brown fox jumped over the lazy dog"); len(m) != 0 {
		t.Errorf("low entropy matched %+v", m)
	}
}

func TestDetectSSN(t *testing.T) {
	if m := Detect("ssn 123-45-6789"); len(m) != 1 || m[0].Type != TypeSSN {
		t.Errorf("got %+v", m)
	}
}

func TestAggregatorConfidence(t *testing.T) {
	a := NewAggregator()
	a.Record("email", "user1@example.com")
	a.Record("email", "user2@example.com")
	a.Record("email", "user3@example.com")
	a.Record("email", "not an email")
	got := a.Findings("ns.t", 0.5)
	if len(got) != 1 || got[0].PIIType != TypeEmail {
		t.Fatalf("got %+v", got)
	}
	if got[0].Confidence < 0.7 || got[0].Confidence > 0.76 {
		t.Errorf("confidence = %.2f, want ~0.75", got[0].Confidence)
	}
	if got[0].RecommendTag != "PII_EMAIL" {
		t.Errorf("recommend tag = %s", got[0].RecommendTag)
	}
}
