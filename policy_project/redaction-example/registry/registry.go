// Package registry lists the finding types a policy may refer to and which
// component produces each one. The policy compiler only accepts types listed
// here, so a policy can never ask the gateway to act on data it cannot find.
package registry

import (
	"sort"
	"strings"
)

// Source is the component that produces a finding type.
type Source string

const (
	// Gateway types come from the regex detectors in detect.go. They are
	// always on and need no other service.
	Gateway Source = "gateway"
	// PIIService types need the NER sidecar in pii-service/ (spaCy/Presidio
	// or GLiNER2), reached through PII_SERVICE_URL. Regex cannot find names
	// or addresses reliably, so these come only from a model.
	PIIService Source = "pii-service"
)

// FindingTypes maps every finding type to the component that produces it.
// Keep it in sync with detect.go and the LABELS maps in pii-service/*.
var FindingTypes = map[string]Source{
	"pii.email":       Gateway,
	"pii.phone":       Gateway,
	"pii.national_id": Gateway,
	"pci.card_number": Gateway,
	"secret.api_key":  Gateway,

	"pii.name":         PIIService,
	"pii.address":      PIIService,
	"pii.bank_account": PIIService,
	"secret.password":  PIIService,
}

// Known reports whether t is a finding type a policy may use.
func Known(t string) bool {
	_, ok := FindingTypes[t]
	return ok
}

// NeedsPIIService reports whether any of the types is only produced by the
// PII service.
func NeedsPIIService(types []string) bool {
	for _, t := range types {
		if FindingTypes[t] == PIIService {
			return true
		}
	}
	return false
}

// List returns all known types, sorted, for error messages and editor dropdowns.
func List() string {
	out := make([]string, 0, len(FindingTypes))
	for t := range FindingTypes {
		out = append(out, t)
	}
	sort.Strings(out)
	return strings.Join(out, ", ")
}
