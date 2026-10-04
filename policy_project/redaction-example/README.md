# Redact personal data: working example

A minimal AI gateway in Go that enforces statement 3.1 (redact personal data
sent to external models) and 3.2 (block card numbers and secrets) of the
sample policy POL-AI-003, using an embedded Open Policy Agent (OPA) engine.

Run the tests: `go test -v ./...` (Go 1.26, pulls OPA v1.21). The tests first
compile the approved policy with `policyc` and run the gateway against the
signed bundle it produces.

## From policy to bundle (`cmd/policyc`)

    go run ./cmd/policyc    # writes build/pol_ai_003-2.1.tar.gz

- `policies/POL-AI-003.yaml`: the policy in authoring format (what the editor saves)
- `governance/approvals/POL-AI-003-2.1.json`: approval record with the file's sha256
- `governance/runtime-data.yaml`: exceptions and per-app rollout mode
- Pipeline: validate, check approval hash and signers, generate Rego and
  tests from templates, run tests with OPA's tester, build a signed bundle.
  Editing the policy after approval stops the build.

## Request flow

1. **Identify the app.** The team's gateway key maps to an app record
   (id, approvals) and a destination (provider, external or self-hosted).
2. **Detect.** `detect.go` scans each message with regex and returns findings:
   type + message index + byte offsets. If `PII_SERVICE_URL` is set, the
   gateway also calls the NER sidecar in `pii-service/` (spaCy/Presidio or
   GLiNER2) and merges its findings; regex wins where both overlap. If the
   sidecar is unreachable the request is blocked (503). Values are never
   sent to OPA.
3. **Decide.** `policy.go` loads the signed bundle (rejecting a bad
   signature) and sends `{app, destination, findings}` to the generated rules. OPA answers with
   decisions such as `{"statement":"3.1","action":"redact","finding_ids":[...]}`.
   Exceptions and monitor/enforce rollout come from the bundle's data.
4. **Enforce.** Block wins over redact. `redact.go` replaces each finding
   with a stable placeholder (`[EMAIL_1]`), keeping a per-request vault so
   the reply can optionally be restored for the user.
5. **Forward.** The rewritten request goes to the provider with the real
   provider key. All other request fields pass through unchanged.
6. **Evidence.** One JSON log line per decision with policy id, version,
   statement, action and finding counts, never the data itself.

Example from the test run:

    in:  Customer jane.doe@example.com (phone +1 415-555-0134) ...
    out: Customer [EMAIL_1] (phone [PHONE_1]) ...
    log: {"policy":"POL-AI-003","policy_version":"2.1","statement":"3.1",
          "action":"redact","finding_types":{"pii.email":2,"pii.phone":1},"redacted":3}

## Finding types a policy can use

`registry/registry.go` is the single list of finding types. The compiler
rejects any other type, so a policy cannot ask for data nothing detects.

| Type | Found by |
| --- | --- |
| `pii.email`, `pii.phone`, `pii.national_id`, `pci.card_number`, `secret.api_key` | regex in the gateway (`detect.go`) |
| `pii.name`, `pii.address`, `pii.bank_account`, `secret.password` | PII service only (`PII_SERVICE_URL`) |

When a statement uses a PII-service type, `policyc` marks it
`needs_pii_service` in the bundle data, and the gateway refuses to start
with that bundle unless `PII_SERVICE_URL` is set. Otherwise those statements
would never fire. To add a type: add it to the registry, map a label to it in
the sidecar's `LABELS`, and add a placeholder name in `redact.go`.

## What a production version adds

- Streaming (SSE) responses, with response-side scanning and restore on the fly.
- Other API shapes: Anthropic messages, content-part arrays, tool-call arguments.
- Policy bundles pulled from the governance service instead of local files.
- Per-statement fail-open or fail-closed, driven by the policy record.
