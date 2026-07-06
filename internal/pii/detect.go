// Package pii implements regex- and entropy-based PII detection over
// streaming Parquet row groups. All sampling is in-memory and no sensitive
// values are ever written to disk or to log output (spec §7.3).
package pii

import (
	"fmt"
	"math"
	"regexp"
	"strings"
)

// Type identifies a detected PII category.
type Type string

const (
	TypeEmail      Type = "EMAIL"
	TypeSSN        Type = "SSN_US"
	TypeCreditCard Type = "CREDIT_CARD"
	TypePhoneE164  Type = "PHONE_E164"
	TypeAUTFN      Type = "AU_TFN"
	TypeINAadhaar  Type = "IN_AADHAAR"
	TypeAPIKey     Type = "API_KEY_ENTROPY"
)

type pattern struct {
	typ Type
	re  *regexp.Regexp
	// validate is an optional second-pass filter (e.g. Luhn check for cards).
	validate func(s string) bool
}

// patterns are deliberately conservative — we'd rather miss low-confidence
// matches than mis-tag a column. Confidence is recovered via hit rate at the
// aggregator level.
var patterns = []pattern{
	{typ: TypeEmail, re: regexp.MustCompile(`(?i)\b[a-z0-9._%+\-]+@[a-z0-9.\-]+\.[a-z]{2,}\b`)},
	{typ: TypeSSN, re: regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`)},
	{typ: TypeCreditCard, re: regexp.MustCompile(`\b(?:\d[ -]?){13,19}\b`), validate: luhnCheck},
	{typ: TypePhoneE164, re: regexp.MustCompile(`\+[1-9]\d{7,14}`)},
	{typ: TypeAUTFN, re: regexp.MustCompile(`\b\d{3}[ -]?\d{3}[ -]?\d{3}\b`)},
	{typ: TypeINAadhaar, re: regexp.MustCompile(`\b\d{4}\s?\d{4}\s?\d{4}\b`)},
}

// Match is a single positive sample for a column. It carries only the
// detected PII type — never the matching substring — to satisfy the
// zero-persistence guarantee.
type Match struct {
	Type Type `json:"type"`
}

// Detect runs every registered pattern against value. The string is
// inspected but never retained.
func Detect(value string) []Match {
	if len(value) == 0 || len(value) > 4096 {
		return nil
	}
	var out []Match
	seen := map[Type]bool{}
	regexHit := map[Type]bool{}
	for _, p := range patterns {
		if !p.re.MatchString(value) {
			continue
		}
		regexHit[p.typ] = true
		if p.validate != nil {
			loc := p.re.FindString(value)
			if !p.validate(loc) {
				continue
			}
		}
		seen[p.typ] = true
	}
	// Aadhaar and AU TFN patterns collide with long digit runs that look
	// like credit cards. Suppress those whenever the CC surface regex hit,
	// even if Luhn rejected it — otherwise an invalid card "leaks" as a
	// false Aadhaar match.
	if regexHit[TypeCreditCard] {
		delete(seen, TypeAUTFN)
		delete(seen, TypeINAadhaar)
	}
	for t := range seen {
		out = append(out, Match{Type: t})
	}
	// Entropy detection — only run if a regex didn't already classify the
	// value. Catches API keys / tokens that lack an exploitable structure.
	if out == nil && looksLikeAPIKey(value) {
		out = append(out, Match{Type: TypeAPIKey})
	}
	return out
}

// luhnCheck implements the Luhn algorithm for credit-card validation.
func luhnCheck(s string) bool {
	var sum int
	alt := false
	for i := len(s) - 1; i >= 0; i-- {
		c := s[i]
		if c == ' ' || c == '-' {
			continue
		}
		if c < '0' || c > '9' {
			return false
		}
		n := int(c - '0')
		if alt {
			n *= 2
			if n > 9 {
				n -= 9
			}
		}
		sum += n
		alt = !alt
	}
	return sum%10 == 0 && sum > 0
}

// looksLikeAPIKey returns true for high-entropy alphanumeric strings of 24+
// characters that don't contain whitespace. Tuned to flag secrets without
// false-positive triggering on common natural-language strings.
func looksLikeAPIKey(s string) bool {
	if len(s) < 24 {
		return false
	}
	if strings.ContainsAny(s, " \t\n") {
		return false
	}
	if !isAlphaNumericPunct(s) {
		return false
	}
	return shannonEntropy(s) >= 4.0
}

func isAlphaNumericPunct(s string) bool {
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '_' || r == '-' || r == '+' || r == '/' || r == '=':
		default:
			return false
		}
	}
	return true
}

// shannonEntropy returns the Shannon entropy in bits of s.
func shannonEntropy(s string) float64 {
	if len(s) == 0 {
		return 0
	}
	var counts [256]int
	for i := 0; i < len(s); i++ {
		counts[s[i]]++
	}
	n := float64(len(s))
	var h float64
	for _, c := range counts {
		if c == 0 {
			continue
		}
		p := float64(c) / n
		h -= p * math.Log2(p)
	}
	return h
}

// Finding aggregates per-column results across a sampled row-group set.
type Finding struct {
	Table        string  `json:"table"`
	Column       string  `json:"column"`
	PIIType      Type    `json:"pii_type"`
	SampleCount  int     `json:"sample_count"`
	MatchCount   int     `json:"match_count"`
	Confidence   float64 `json:"confidence"`
	RecommendTag string  `json:"recommended_tag"`
}

// Aggregator collects per-(column,type) tallies and turns them into Findings.
type Aggregator struct {
	cells map[string]*cell
	rows  map[string]int // per-column total sampled rows
}

type cell struct {
	matches int
}

// NewAggregator returns an empty aggregator.
func NewAggregator() *Aggregator {
	return &Aggregator{cells: map[string]*cell{}, rows: map[string]int{}}
}

// Record one sampled cell — column carries the column name; value is the
// (still-discarded) sampled value.
func (a *Aggregator) Record(column, value string) {
	a.rows[column]++
	for _, m := range Detect(value) {
		k := column + "\x00" + string(m.Type)
		c := a.cells[k]
		if c == nil {
			c = &cell{}
			a.cells[k] = c
		}
		c.matches++
	}
}

// Findings returns one Finding per (column,type) tuple whose hit rate
// exceeds minConfidence (0..1).
func (a *Aggregator) Findings(table string, minConfidence float64) []Finding {
	out := make([]Finding, 0, len(a.cells))
	for k, c := range a.cells {
		col, typ, ok := splitKey(k)
		if !ok {
			continue
		}
		sampled := a.rows[col]
		if sampled == 0 {
			continue
		}
		conf := float64(c.matches) / float64(sampled)
		if conf < minConfidence {
			continue
		}
		out = append(out, Finding{
			Table:        table,
			Column:       col,
			PIIType:      typ,
			SampleCount:  sampled,
			MatchCount:   c.matches,
			Confidence:   conf,
			RecommendTag: fmt.Sprintf("PII_%s", typ),
		})
	}
	return out
}

func splitKey(k string) (string, Type, bool) {
	idx := strings.IndexByte(k, '\x00')
	if idx < 0 {
		return "", "", false
	}
	return k[:idx], Type(k[idx+1:]), true
}
