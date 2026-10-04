package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const namePolicy = `policy_id: POL-AI-900
version: "1.0"
title: Names and addresses
owner: group:privacy-office
approvers: [dpo]
statements:
  - id: "1.1"
    text: Names and addresses are redacted before external models.
    enforced_by: gateway
    applies_when:
      - { field: destination.hosting, equals: external }
    detect: [pii.name, pii.address, pii.bank_account]
    action: redact
    on_check_failure: closed
  - id: "1.2"
    text: Passwords are never sent to a model.
    enforced_by: gateway
    detect: [secret.password]
    action: block
    on_check_failure: closed
`

func TestValidateAcceptsPIIServiceTypes(t *testing.T) {
	p := Policy{PolicyID: "POL-AI-900", Version: "1.0", Approvers: []string{"dpo"},
		Statements: []Statement{{ID: "1.1", EnforcedBy: "gateway", Action: "redact",
			OnCheckFailure: "closed", Detect: []string{"pii.name", "pii.address", "pii.bank_account", "secret.password"}}}}
	if err := Validate(&p); err != nil {
		t.Fatalf("new types should be accepted: %v", err)
	}
}

func TestValidateRejectsUnknownType(t *testing.T) {
	p := Policy{PolicyID: "POL-AI-900", Version: "1.0", Approvers: []string{"dpo"},
		Statements: []Statement{{ID: "1.1", EnforcedBy: "gateway", Action: "redact",
			OnCheckFailure: "closed", Detect: []string{"pii.passport"}}}}
	err := Validate(&p)
	if err == nil || !strings.Contains(err.Error(), `unknown detector "pii.passport"`) {
		t.Fatalf("want unknown detector error, got %v", err)
	}
}

// The full pipeline compiles a policy that uses PII-service types, its
// generated Rego tests pass, and the bundle data marks those statements so a
// gateway without the service refuses to load it.
func TestCompilePolicyWithPIIServiceTypes(t *testing.T) {
	dir := t.TempDir()
	policyPath := filepath.Join(dir, "POL-AI-900.yaml")
	if err := os.WriteFile(policyPath, []byte(namePolicy), 0o644); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte(namePolicy))
	approvals := filepath.Join(dir, "approvals")
	os.MkdirAll(approvals, 0o755)
	approval := `{"policy_id":"POL-AI-900","version":"1.0","content_sha256":"` + hex.EncodeToString(sum[:]) +
		`","approvals":[{"role":"dpo","user":"user:test","signed_at":"2026-10-04T00:00:00Z"}]}`
	os.WriteFile(filepath.Join(approvals, "POL-AI-900-1.0.json"), []byte(approval), 0o644)
	dataPath := filepath.Join(dir, "data.yaml")
	os.WriteFile(dataPath, []byte("exceptions: []\nrollout: {}\napps: {}\n"), 0o644)

	out := filepath.Join(dir, "build")
	if err := run(context.Background(), policyPath, approvals, dataPath, out); err != nil {
		t.Fatalf("compile: %v", err)
	}
	if _, err := os.Stat(filepath.Join(out, "pol_ai_900-1.0.tar.gz")); err != nil {
		t.Fatalf("bundle not written: %v", err)
	}
	raw, _ := os.ReadFile(filepath.Join(out, "src", "aiplane", "data.json"))
	var data struct {
		Policies map[string]struct {
			Statements map[string]struct {
				NeedsPIIService bool `json:"needs_pii_service"`
			} `json:"statements"`
		} `json:"policies"`
	}
	json.Unmarshal(raw, &data)
	st := data.Policies["POL-AI-900"].Statements
	if !st["1.1"].NeedsPIIService || !st["1.2"].NeedsPIIService {
		t.Fatalf("statements using PII-service types must be marked: %s", raw)
	}
}
