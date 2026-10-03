package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/open-policy-agent/opa/v1/bundle"
	"github.com/open-policy-agent/opa/v1/rego"
)

// Decision is one statement's verdict for a request.
type Decision struct {
	Statement  string   `json:"statement"`
	Action     string   `json:"action"` // "block" or "redact"
	Mode       string   `json:"mode"`   // "enforce" or "monitor"
	FindingIDs []string `json:"finding_ids"`
}

// PolicyInput is what the gateway sends to OPA for every request.
type PolicyInput struct {
	App         App         `json:"app"`
	Destination Destination `json:"destination"`
	Findings    []Finding   `json:"findings"`
}

type App struct {
	ID          string   `json:"id"`
	ApprovedFor []string `json:"approved_for"`
}

type Destination struct {
	Provider string `json:"provider"`
	Hosting  string `json:"hosting"` // "external" or "self-hosted"
}

// Policy wraps a compiled OPA query. Compile once at startup; Eval is safe
// to call concurrently and typically takes well under a millisecond.
type Policy struct {
	query    rego.PreparedEvalQuery
	Revision string // bundle revision, logged with every decision
}

// Result is the full policy output, including which policy version decided,
// so every log line can be traced to the wording that was approved.
type Result struct {
	PolicyID      string     `json:"policy_id"`
	PolicyVersion string     `json:"policy_version"`
	Decisions     []Decision `json:"decisions"`
}

// LoadBundle reads a bundle built by policyc and refuses it unless its
// signature verifies, so a tampered or hand-built bundle never loads.
// In production the gateway polls the bundle server and swaps the
// prepared query in place when the revision changes.
func LoadBundle(ctx context.Context, path, verifyKey string) (*Policy, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	vc := bundle.NewVerificationConfig(map[string]*bundle.KeyConfig{
		"policy-signer": {Key: verifyKey, Algorithm: "HS256"},
	}, "policy-signer", "", nil)
	b, err := bundle.NewReader(f).WithBundleVerificationConfig(vc).Read()
	if err != nil {
		return nil, fmt.Errorf("load bundle %s: %w", path, err)
	}
	q, err := rego.New(
		rego.Query("data.aiplane.pol_ai_003"),
		rego.ParsedBundle("policies", &b),
	).PrepareForEval(ctx)
	if err != nil {
		return nil, fmt.Errorf("prepare policy: %w", err)
	}
	return &Policy{query: q, Revision: b.Manifest.Revision}, nil
}

func (p *Policy) Eval(ctx context.Context, in PolicyInput) (*Result, error) {
	rs, err := p.query.Eval(ctx, rego.EvalInput(in))
	if err != nil {
		return nil, err
	}
	if len(rs) == 0 || len(rs[0].Expressions) == 0 {
		return nil, fmt.Errorf("policy returned no result")
	}
	// Round-trip through JSON to turn OPA's generic result into our types.
	raw, _ := json.Marshal(rs[0].Expressions[0].Value)
	var out Result
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
