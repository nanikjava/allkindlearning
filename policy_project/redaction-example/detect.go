package main

import (
	"fmt"
	"regexp"
	"strings"
)

// Finding is one piece of sensitive data found in a request.
// Offsets are byte positions inside Messages[Message].Content.
type Finding struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Message int    `json:"message"`
	Start   int    `json:"start"`
	End     int    `json:"end"`
}

type detector struct {
	typ   string
	re    *regexp.Regexp
	valid func(string) bool // optional extra check to cut false positives
}

// Regex detectors keep the example self-contained. In production, put an
// NER-based service (e.g. Microsoft Presidio) behind the same interface so
// names and addresses are caught too.
var detectors = []detector{
	{typ: "pii.email", re: regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`)},
	{typ: "pii.phone", re: regexp.MustCompile(`\+?\d{1,3}[ -]?\(?\d{2,4}\)?[ -]?\d{3,4}[ -]?\d{3,4}\b`)},
	{typ: "pii.national_id", re: regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`)}, // US SSN format
	{typ: "pci.card_number", re: regexp.MustCompile(`\b(?:\d[ -]?){13,19}\b`), valid: luhn},
	{typ: "secret.api_key", re: regexp.MustCompile(`\b(?:sk-[A-Za-z0-9_-]{20,}|AKIA[0-9A-Z]{16})\b`)},
}

// Detect scans every message and returns non-overlapping findings.
// Detectors earlier in the list win when two findings overlap, except that
// a valid card number beats a phone-number match on the same digits.
func Detect(msgs []Message) []Finding {
	var out []Finding
	n := 0
	for mi, m := range msgs {
		var taken [][2]int
		// Run the high-confidence detectors first so they claim their spans.
		for _, d := range orderedDetectors() {
			for _, loc := range d.re.FindAllStringIndex(m.Content, -1) {
				s, e := loc[0], loc[1]
				if d.valid != nil && !d.valid(m.Content[s:e]) {
					continue
				}
				if overlaps(taken, s, e) {
					continue
				}
				taken = append(taken, [2]int{s, e})
				n++
				out = append(out, Finding{ID: fmt.Sprintf("f%d", n), Type: d.typ, Message: mi, Start: s, End: e})
			}
		}
	}
	return out
}

func orderedDetectors() []detector {
	// secrets and cards first, then PII
	order := []string{"secret.api_key", "pci.card_number", "pii.national_id", "pii.email", "pii.phone"}
	var ds []detector
	for _, t := range order {
		for _, d := range detectors {
			if d.typ == t {
				ds = append(ds, d)
			}
		}
	}
	return ds
}

func overlaps(spans [][2]int, s, e int) bool {
	for _, sp := range spans {
		if s < sp[1] && sp[0] < e {
			return true
		}
	}
	return false
}

func luhn(s string) bool {
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, s)
	if len(digits) < 13 {
		return false
	}
	sum, double := 0, false
	for i := len(digits) - 1; i >= 0; i-- {
		d := int(digits[i] - '0')
		if double {
			d *= 2
			if d > 9 {
				d -= 9
			}
		}
		sum += d
		double = !double
	}
	return sum%10 == 0
}
