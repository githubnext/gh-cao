---
name: update-server-api-specs
description: Update the Go dashboard server HTTP contract in TypeSpec, regenerate OpenAPI 3.1 and JSON Schemas, and keep the implementation conformant.
---

# Update server API specs

Use this skill when adding or changing the dashboard server's HTTP API. Read
`server/spec/README.md` first for the contract's scope and writer workflow.
`server/spec/*.tsp` is the editable API contract; `server/spec/generated/` contains
committed output, not hand-edited source. Do not specify MCP internals.

## Procedure

1. Identify the affected HTTP operation in `server/spec/` and its registered
   handler in `server/internal/server/server.go`. Read the handler, its Go
   request/response types, and relevant callers. For Dashboard Language queries,
   consult `server/internal/query/types.go` and `specs/dashboard-data.md`.
2. Edit the appropriate domain `.tsp` file first (query, health, auth, canonical,
   memory, or operations); share reusable models in `common.tsp`. Import a new
   domain from `main.tsp` rather than putting all routes in one file. Specify
   method, path, headers, body, statuses, and examples. Describe deployment
   differences: local bearer capability; hosted GitHub OAuth session and
   `X-CSRF-Token` on protected mutations; public health/readiness; and signed
   GitHub webhooks. Never expose OAuth tokens, storage internals, or secrets.
3. Bring the Go handler and any client into conformance with the edited spec.
   When changing routes or structured payloads, extend the relevant checks in
   `server/spec/contract.test.mjs`. Preserve the existing API unless the change
   explicitly calls for a breaking change.
4. Install the pinned compiler/emitters with `npm --prefix server/spec ci`.
   From the repository root, run `npm run dashboard:server:spec` to regenerate
   `server/spec/generated/openapi.json` and the selected JSON Schemas. Inspect
   the generated diff for unintended routes, security requirements, payload
   changes, and sensitive example data. Do not edit generated files manually.
5. Run `npm --prefix server/spec run check` to verify the generated artifacts
   match the TypeSpec source and Go route/field contract. If implementation
   changed, also run `go -C server test ./...` and the applicable lint checks.
   Commit the `.tsp`, implementation, tests, and generated artifacts together.

If a desired behavior cannot be represented faithfully in the current spec,
report the discrepancy rather than claiming the generated contract is
authoritative for behavior it does not describe.
