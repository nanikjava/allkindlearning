package main

import (
	"fmt"
	"regexp"
	"strings"
)

// Policy is the authoring format saved by the editor (policies/*.yaml).
type Policy struct {
	PolicyID    string      `yaml:"policy_id"`
	Version     string      `yaml:"version"`
	Title       string      `yaml:"title"`
	Owner       string      `yaml:"owner"`
	Approvers   []string    `yaml:"approvers"`
	ReviewEvery string      `yaml:"review_every"`
	MapsTo      []string    `yaml:"maps_to"`
	Statements  []Statement `yaml:"statements"`
}

type Statement struct {
	ID             string      `yaml:"id"`
	Text           string      `yaml:"text"`
	EnforcedBy     string      `yaml:"enforced_by"` // gateway | workflow | attestation
	AppliesWhen    []Condition `yaml:"applies_when"`
	Unless         []Condition `yaml:"unless"`
	Detect         []string    `yaml:"detect"`
	Action         string      `yaml:"action"`
	OnCheckFailure string      `yaml:"on_check_failure"`
	Workflow       string      `yaml:"workflow"`
	Evidence       string      `yaml:"evidence"`
}

// Condition compares a field of the gateway's policy input with a value.
// Exactly one of Equals or Contains is set.
type Condition struct {
	Field    string `yaml:"field"`
	Equals   string `yaml:"equals"`
	Contains string `yaml:"contains"`
}

// Approval is the record the review workflow writes when every approver
// has signed. The compiler refuses to build without a matching one.
type Approval struct {
	PolicyID      string `json:"policy_id"`
	Version       string `json:"version"`
	ContentSHA256 string `json:"content_sha256"`
	Approvals     []struct {
		Role     string `json:"role"`
		User     string `json:"user"`
		SignedAt string `json:"signed_at"`
	} `json:"approvals"`
}

// Registries: the only values the editor offers in its dropdowns. Keeping
// them closed lists is also what makes code generation safe, because no
// free text from a policy ever lands in Rego outside a JSON string.
var (
	knownDetectors = set("pii.email", "pii.phone", "pii.national_id", "pci.card_number", "secret.api_key")
	knownActions   = set("block", "redact")
	// input field -> whether it is a list (use contains) or a scalar (use equals)
	knownFields = map[string]bool{
		"destination.hosting":  false,
		"destination.provider": false,
		"app.id":               false,
		"app.risk_tier":        false,
		"app.approved_for":     true,
	}
	idPattern = regexp.MustCompile(`^[0-9]+(\.[0-9]+)*$`)
)

func Validate(p *Policy) error {
	var errs []string
	add := func(f string, a ...any) { errs = append(errs, fmt.Sprintf(f, a...)) }

	if !regexp.MustCompile(`^POL-[A-Z]+-[0-9]{3}$`).MatchString(p.PolicyID) {
		add("policy_id %q must look like POL-AI-003", p.PolicyID)
	}
	if p.Version == "" || len(p.Approvers) == 0 {
		add("version and approvers are required")
	}
	seen := map[string]bool{}
	for _, s := range p.Statements {
		where := "statement " + s.ID
		if !idPattern.MatchString(s.ID) || seen[s.ID] {
			add("%s: id must be unique and numeric like 3.1", where)
		}
		seen[s.ID] = true
		switch s.EnforcedBy {
		case "workflow", "attestation":
			continue // enforced outside the gateway; nothing to compile
		case "gateway":
		default:
			add("%s: enforced_by must be gateway, workflow or attestation", where)
			continue
		}
		if !knownActions[s.Action] {
			add("%s: unknown action %q", where, s.Action)
		}
		if s.OnCheckFailure != "open" && s.OnCheckFailure != "closed" {
			add("%s: on_check_failure must be open or closed", where)
		}
		if len(s.Detect) == 0 {
			add("%s: detect needs at least one detector", where)
		}
		for _, d := range s.Detect {
			if !knownDetectors[d] {
				add("%s: unknown detector %q", where, d)
			}
		}
		for _, c := range append(append([]Condition{}, s.AppliesWhen...), s.Unless...) {
			isList, ok := knownFields[c.Field]
			switch {
			case !ok:
				add("%s: unknown field %q", where, c.Field)
			case isList && (c.Contains == "" || c.Equals != ""):
				add("%s: %s is a list, use contains", where, c.Field)
			case !isList && (c.Equals == "" || c.Contains != ""):
				add("%s: %s is a single value, use equals", where, c.Field)
			}
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("policy %s is invalid:\n  - %s", p.PolicyID, strings.Join(errs, "\n  - "))
	}
	return nil
}

func set(vals ...string) map[string]bool {
	m := map[string]bool{}
	for _, v := range vals {
		m[v] = true
	}
	return m
}
