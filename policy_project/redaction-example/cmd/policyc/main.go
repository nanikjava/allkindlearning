// policyc compiles an approved AI policy into a signed OPA bundle.
//
//	go run ./cmd/policyc -policy policies/POL-AI-003.yaml
//
// Pipeline: validate -> check approval -> generate Rego + tests ->
// run tests -> build signed bundle. Any failing step stops the publish.
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/open-policy-agent/opa/v1/bundle"
	"github.com/open-policy-agent/opa/v1/compile"
	"github.com/open-policy-agent/opa/v1/tester"
	"gopkg.in/yaml.v3"
)

func main() {
	policyPath := flag.String("policy", "policies/POL-AI-003.yaml", "policy in authoring format")
	approvalDir := flag.String("approvals", "governance/approvals", "approval records from the review workflow")
	dataPath := flag.String("data", "governance/runtime-data.yaml", "exceptions and rollout exported from governance DB")
	outDir := flag.String("out", "build", "output directory")
	flag.Parse()
	if err := run(context.Background(), *policyPath, *approvalDir, *dataPath, *outDir); err != nil {
		log.Fatalf("publish stopped: %v", err)
	}
}

func run(ctx context.Context, policyPath, approvalDir, dataPath, outDir string) error {
	// 1. Load and validate the authoring format.
	raw, err := os.ReadFile(policyPath)
	if err != nil {
		return err
	}
	var p Policy
	if err := yaml.Unmarshal(raw, &p); err != nil {
		return fmt.Errorf("parse %s: %w", policyPath, err)
	}
	if err := Validate(&p); err != nil {
		return err
	}
	sum := sha256.Sum256(raw)
	hash := hex.EncodeToString(sum[:])
	step("validated", "%s v%s, %d statements", p.PolicyID, p.Version, len(p.Statements))

	// 2. Only approved, unchanged text may be compiled.
	a, err := checkApproval(&p, hash, filepath.Join(approvalDir, p.PolicyID+"-"+p.Version+".json"))
	if err != nil {
		return err
	}
	step("approval ok", "signed by %d approvers, content hash matches", len(a.Approvals))

	// 3. Generate Rego, tests and bundle data.
	var gateway []Statement
	meta := map[string]any{}
	for _, s := range p.Statements {
		if s.EnforcedBy == "gateway" {
			gateway = append(gateway, s)
			meta[s.ID] = map[string]any{"on_check_failure": s.OnCheckFailure, "action": s.Action}
		}
	}
	src := filepath.Join(outDir, "src")
	os.RemoveAll(src)
	pkgDir := filepath.Join(src, "aiplane", PackageName(p.PolicyID))
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		return err
	}
	view := map[string]any{"P": p, "A": a, "Hash": hash, "Gateway": gateway, "Tests": buildTests(gateway)}
	if err := render(policyTmpl, view, filepath.Join(pkgDir, "policy.rego")); err != nil {
		return err
	}
	if err := render(testTmpl, view, filepath.Join(pkgDir, "policy_test.rego")); err != nil {
		return err
	}
	if err := writeData(dataPath, filepath.Join(src, "aiplane", "data.json"), p.PolicyID, p.Version, meta); err != nil {
		return err
	}
	step("generated", "%d gateway rules, %d tests (%d statements enforced outside the gateway)",
		len(gateway), len(view["Tests"].([]testCase)), len(p.Statements)-len(gateway))

	// 4. Compile and run the generated tests. A rule that does not do what
	//    the statement says never reaches a gateway.
	results, err := tester.Run(ctx, src)
	if err != nil {
		return fmt.Errorf("compile: %w", err)
	}
	failed := 0
	for _, r := range results {
		if !r.Pass() {
			failed++
			fmt.Println("   FAIL", r.Name, r.Error)
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d generated tests failed", failed, len(results))
	}
	step("tests passed", "%d of %d", len(results), len(results))

	// 5. Build the signed bundle the gateways download.
	key := os.Getenv("POLICY_SIGNING_KEY")
	if key == "" {
		key = "demo-signing-key-change-me" // production: RS256/ES256 key held in a KMS
		fmt.Println("   warning: POLICY_SIGNING_KEY not set, using the demo key")
	}
	revision := fmt.Sprintf("%s@%s+%s", p.PolicyID, p.Version, hash[:8])
	out := filepath.Join(outDir, PackageName(p.PolicyID)+"-"+p.Version+".tar.gz")
	f, err := os.Create(out)
	if err != nil {
		return err
	}
	defer f.Close()
	bundleMeta := map[string]any{"policies": map[string]any{p.PolicyID: map[string]any{
		"version": p.Version, "content_sha256": hash, "maps_to": p.MapsTo}}}
	err = compile.New().
		WithPaths(src).
		WithFilter(func(path string, info fs.FileInfo, _ int) bool {
			return strings.HasSuffix(info.Name(), "_test.rego") // tests stay out of the bundle
		}).
		WithRoots("aiplane").
		WithRevision(revision).
		WithMetadata(&bundleMeta).
		WithBundleSigningConfig(bundle.NewSigningConfig(key, "HS256", "")).
		WithBundleVerificationKeyID("policy-signer").
		WithOutput(f).
		Build(ctx)
	if err != nil {
		return fmt.Errorf("build bundle: %w", err)
	}
	step("bundle built", "%s (revision %s, signed)", out, revision)
	return nil
}

func checkApproval(p *Policy, hash, path string) (*Approval, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("no approval record for %s v%s: %w", p.PolicyID, p.Version, err)
	}
	var a Approval
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	if a.PolicyID != p.PolicyID || a.Version != p.Version {
		return nil, fmt.Errorf("approval is for %s v%s", a.PolicyID, a.Version)
	}
	if a.ContentSHA256 != hash {
		return nil, fmt.Errorf("policy text changed after approval (approved %s…, now %s…)", a.ContentSHA256[:8], hash[:8])
	}
	signed := map[string]bool{}
	for _, s := range a.Approvals {
		signed[s.Role] = true
	}
	for _, role := range p.Approvers {
		if !signed[role] {
			return nil, fmt.Errorf("missing approval from %s", role)
		}
	}
	return &a, nil
}

func writeData(in, out, policyID, version string, meta map[string]any) error {
	raw, err := os.ReadFile(in)
	if err != nil {
		return err
	}
	var d map[string]any
	if err := yaml.Unmarshal(raw, &d); err != nil {
		return err
	}
	d["policies"] = map[string]any{policyID: map[string]any{"version": version, "statements": meta}}
	b, _ := json.MarshalIndent(d, "", "  ")
	return os.WriteFile(out, b, 0o644)
}

func render(t interface {
	Execute(w io.Writer, data any) error
}, data any, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return t.Execute(f, data)
}

func step(name, f string, a ...any) { fmt.Printf("✓ %-13s %s\n", name, fmt.Sprintf(f, a...)) }
