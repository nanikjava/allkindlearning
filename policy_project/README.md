# Aiplane

Aiplane manages policy and security for an organisation's AI applications:
policies are authored in YAML, approved, compiled to Rego, and shipped as a
signed Open Policy Agent bundle that gateways enforce at runtime.

## Contents

- `iso42001-product-map.html`, `policy-lifecycle.html`,
  `policy-to-code.html`: design notes on the standards mapping (ISO/IEC 42001,
  NIST AI RMF, OWASP LLM Top 10) and the policy authoring-to-code path.
- `ui-mockups/`: editor and review screen mockups.
- `redaction-example/`: a Go prototype of a gateway that redacts personal data,
  with `cmd/policyc`, the compiler from policy YAML to a signed OPA bundle.
  See its README to build and test.

Proposed stack: Go, Postgres, Temporal, OPA.
