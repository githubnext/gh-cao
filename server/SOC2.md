# Go server SOC 2 readiness assessment

> Historical evidence: the assessments below apply to their cited source SHAs,
> not the current PostgreSQL-only implementation. SQLite backends are removed;
> recorded SQLite results are not current validation or supported deployment
> guidance. See `ARCHITECTURE.md` and `server/POSTGRES.md` for current boundaries.
> This note does not reassess controls or establish operating effectiveness.

## Current-source reassessment - 2026-10-22 UTC

Assessed source **`53458a46d983356cdc2b8c5f27903026ca707a82`**, RFC-035
record schema version 1, on macOS/arm64 with Go 1.27.3. This section supersedes
the older snapshots below **only for current-source implementation presence
and the bounded test results stated here**. Original observations, decisions,
and lifecycle history remain intact. No production configuration, examination
period, organizational evidence, or operator/auditor approval was supplied.
Operating effectiveness remains **not-assessed**.

**Conclusion:** the current checkout contains technical corrections for all
ten original design gaps and DATA-08's stale test fixture. The ten corrected
properties below are now `design-supported` at this SHA within the stated
limits; this does not close the original findings or their rollout/evidence
requirements. In particular, older statements that DATA-01/02, EXEC-01 and
OPS-01/03 remain unintegrated proposals are historical, not current-source
descriptions. No new demonstrated runtime design gap was established by this
bounded reassessment. SOC 2 readiness is **undetermined**, not compliant.

### Scope, source drift and coverage

Scope is the maintained Go server inventory and direct shared-query/deployment
dependencies. Source was compared with the original investigation baseline
`e3daa5d7ad4fa537e6432359076b44f4a90be171`; changed claims were inspected and
rechecked rather than carrying the original test results forward as current.
The original investigation runtime was
`ef7d9fb65ea952826f9d81a94bd5890d3cfd5634`.
The current code includes the assessment/remediation integration in
`d4f06dd01` and the development-only source-build restriction in `1f05c9663`.
This is source ancestry evidence, not remote deployment or live PR-state
verification.

The data-flow and profile inventory below still applies: browser/listener to
OAuth/API/query store; signed GitHub ingress to collection/admission/gateway;
step-up administrator to hosted CLI queue/executor; committed audit rows to an
independent destination; reviewed source to packaged deployment. Data classes
include repository/workflow evidence, account IDs and logins, OAuth credentials,
session/revocation state, execution output, audit history and telemetry.
GitHub, database/volume providers, Azure, the independent evidence receiver,
image registries and identity/organizational processes remain external
boundaries.

| Inventory surface | Current coverage and limitation |
| --- | --- |
| Entrypoints, hosting, HTTP/OAuth/MCP | Current authentication/session/stream/rebuild code and focused synthetic tests; no deployed ingress, membership, provider or proxy validation |
| Canonical stores, SQL and shared query engine | Current resource/transport/temporal contracts and local evaluator tests; PostgreSQL execution, roles and snapshot parity remain partly skipped |
| Ingestion, collection and operational adapters | Current erasure, replay intent, shared filtering, adapter contracts and tests; real PostgreSQL/Redis restart, withdrawal and fault paths skipped |
| Derived services and model | Repository memory, marketplace, operational-value and SQLite tests; external registries, physical resource ceilings and governed disposal unverified |
| Admission, gateway and execution | Synthetic policy, replay, requester identity, broker, egress and gateway tests; real executor kernels, upstream compiled runtimes and durable provider operation unverified |
| Audit, telemetry, diagnostics and lifecycle | Synthetic chain/export faults, TLS telemetry receipts, rebuild lifecycle and doctor tests; real independent destination, alerts and recovery unverified |
| Build, release, deployment and API contract | Current source-build guard, template telemetry and candidate scan/SBOM definitions reviewed; mocked deployment checks only, no image scan/build/provenance or full generated-contract gate run |
| Test infrastructure and clock/shared contracts | Exercised transitively by selected deterministic tests; simulator/bootstrap stress and platform integration are not independently reassessed |

Local SQLite/capability, hosted/external, Azure Functions, PostgreSQL/Redis/memory,
admission-only, gateway and experimental CLI are **separate evaluated designs**,
not an asserted deployed feature inventory. Native Apple/Windows, real
container-engine egress (then Docker; Podman now replaces it), fleet stress, live integrations and production access were excluded
from execution. These exclusions do not approve DATA-09 or EXEC-07.

### Current control/evidence matrix

Source citations in this table are repository-relative at the reassessment SHA.
Each row is a narrow claim, not satisfaction of a whole TSC criterion.

| Control claim | Working TSC mapping | Status | Source evidence | Test evidence | Deployment/period evidence needed |
| --- | --- | --- | --- | --- | --- |
| DATA-01: repository shard identity is prefix-free and erasure preflights ownership | CC6.1; candidate PI1/A1 | design-supported | `server/internal/collect/lake.go:80-88,143-211` | passed: collision/legacy/preflight cases in component log | Collision inventory, preserved legacy repair evidence, effective rollout and filesystem behavior |
| DATA-02: collector withdrawal filters shared/derived values and revision-keys affected cache results | CC6.5; candidate C1/P | design-supported | `server/internal/collect/lake_values.go:15-89`; `server/internal/collect/projector.go:236-250`; `server/internal/server/query_cache.go:101-145` | passed: local shared/zero-enrollment cases; skipped: backend-dependent end-to-end withdrawal | Actual withdrawal through canonical/cache copies; independent publication/hold disposal decision |
| DATA-03: erasure retains work until projection intent is recorded; completion records the captured generation after canonical commit | CC7.2; candidate PI1/C1 | design-supported | `server/internal/collect/worker.go:289-309`; `server/internal/collect/projector.go:125-142,185-216` | passed: marker/cancellation/generation synthetic faults; skipped: real-backend canonical recovery | Real backend restart/outage, volume guarantees, accepted-work recovery evidence |
| IAM-001: provider expiry, rather than session deadlines, governs cleanup expiry; failed cleanup retains ownership | CC6.2-CC6.3, CC7.4 | design-supported | `server/internal/server/oauth.go:163-165,795-799,1264-1278`; `server/internal/server/oauth_expiry_test.go:16-344` | passed: unknown-expiry/no-refresh, restart/key and capacity synthetic cases | Coordinated old-replica retirement, legacy grant reconciliation, actual key/revocation lifecycle |
| IAM-002: OAuth revision-stream authorization has fixed deadlines and heartbeat session checks | CC6.2-CC6.3 | design-supported | `server/internal/server/server.go:1264-1360` | passed: stream revocation/deadline/outage/drain tests | Effective proxy/write behavior; approve residual heartbeat/read-timeout and membership windows |
| EXEC-01: queued attribution carries numeric account ID and execution checks its live identity | CC6.2-CC6.3; candidate PI1 | design-supported | `server/internal/operational/cli_commands.go:101-105`; `server/internal/hostedcli/executor.go:278-315` | passed: same-login/different-ID, legacy and prefetch recheck fixtures | Coordinated web/executor rollout, old work disposition and live provider/access evidence |
| OPS-01: hosted rebuild requires durable admission/start audit and reports terminal audit failures | CC6.3, CC7.2-CC7.3 | design-supported | `server/internal/server/operations.go:86-89,116-170,183-223` | passed: synthetic lifecycle/admission/terminal faults; skipped: PostgreSQL independent history | Reconcile missing terminal IDs; retained independent lifecycle evidence; cross-store commits are not atomic |
| OPS-02: Azure templates explicitly support optional Go OTLP and log opt-in | CC7.2-CC7.3 | design-supported | `server/azure/main.bicep:45-69,615-625`; `server/azure/collector.bicep:299-307`; `server/internal/telemetry/azure_deployment_test.go:31-120` | passed: template-driven synthetic TLS receipts/failure isolation and CLI config tests | Effective per-role collector configuration, backend receipts, absent-signal/failure alerts and acknowledgements |
| OPS-03: Azure source-build apply refuses non-development config/plan | CC7.1, CC8.1 | design-supported | `internal/cli/deploy_azure_apply.go:43-54`; `.github/workflows/cao-package.yml:432-518` | passed: mocked source-build, digest binding/read-back and context tests | Production image/source/digest assurance and promotion evidence; development declaration is not environment attestation |
| OPS-04: Redis persistence diagnostics preserve unknown and narrowly describe observed AOF success | CC7.1-CC7.2; candidate A1 | design-supported | `server/internal/doctor/checks_redis.go:371-450` | passed: missing/invalid/RDB/AOF diagnostic fixtures | Provider persistence settings and restore proof; INFO success does not establish durability |
| DATA-08: native-measure temporal contract uses the maintained shared fixture | CC8.1; candidate PI1 | design-supported | `server/internal/query/sql_temporal_test.go:182-225` | passed: current query package; PostgreSQL execution cases skipped | Remaining provider-backed SQL/fixture gate, applicable release evidence and authorized closure |
| Transport, durable store selection and privilege separation design | CC6.1, CC6.3, CC6.7 | design-supported | `server/internal/postgresconn/config.go:29-61`; `server/internal/server/operational_factory.go:82-119`; `server/postgres/operational-grants.sql:1-45` | passed: transport/composition tests; skipped: real DB privilege/adapter tests | Actual roles/inheritance/PUBLIC/default ACLs, no runtime owner credentials, encryption/key/TLS exceptions |
| Independent anchored audit delivery design | CC7.2-CC7.3, CC9.2 | design-supported | `server/internal/auditdelivery/delivery.go:96-200`; `server/audit-delivery/README.md:3-57` | passed: local TLS/chain/replay/fault fixtures; skipped: real destination | Independently administered retention/anchors, conformance, scheduler/backlog/deadman alerts and period receipts |
| Governance, people, risk, communication, monitoring ownership and vendors | CC1-CC5, CC9 | evidence-needed | `server/SECURITY.md:1037-1062`; RFC-035 sections 3-5 | not-run: organizational examination | Approved controls/owners, personnel/access process, risk register, vendor reviews and operating samples |
| Scope, commitments, Type 1/Type 2 objective and additional category applicability | CC1-CC9; A1/PI1/C1/P1-P8 | evidence-needed | RFC-035 sections 3 and 14; SCOPE-01/02 | not-run: operator/auditor decisions | Approved system description, categories, providers, complementary user controls and date/period |
| Recovery, retention/deletion, real executor isolation and physical resource ceilings | CC6-CC9; candidate A1/PI1/C1/P | evidence-needed | DATA-04/05/06/07/10, IAM-003/004/005/006, EXEC-02 through EXEC-08, OPS-05 through OPS-08 below | skipped/not-run for providers/platforms/restore/period | Sanitized deployment evidence, intended-platform tests, restore/deletion exercises and approved residual-risk decisions |
| Local/volatile profile exclusion from specific durability commitments | CC6.3; candidate A1/C1 | not-applicable-proposed | DATA-09 below; `server/internal/server/operational_factory.go:84-100` | passed: explicit volatile-mode guards only | Operator/auditor-approved narrow scope; local data confidentiality/privacy remains accounted for |

The source-only scan exception remains an **OPS-08 evidence request**, not a
new proven vulnerability: `.grype.yaml:1-7` excludes a zlib CVE with a reason
but no in-file approver/expiry. An external approved exception may exist.
Current CVE/fix status was not verified. DATA-10 also remains evidence-needed:
SQL limits are checked using executed statistics
(`server/internal/postgresx/sql_plan.go:50-155`), repository-memory manifest
loading precedes bounded file checks
(`server/internal/repositorymemory/memory.go:112-170`), and generic marketplace
JSON decoding has no explicit byte cap
(`server/internal/marketplace/resolve.go:83-95`). No exploit, measured resource
failure or newly mandatory control is inferred.

### Executed evidence and limitations

All commands below exited **0** on this source. Counts are Go test/subtest
events, not unique controls; skipped children remain unverified even when a
parent passes. PostgreSQL/Redis endpoints and opt-in live/stress/native tests
were unset for assessment commands. No service was started, provider accessed,
runtime changed or dependency installed.

Evidence directory:
`/Users/peli/.copilot/session-state/3f68bcfc-fea5-4512-8db5-91db11b972e2/files/`.

| Check / evidence file | Actual outcome |
| --- | --- |
| `go -C server test -p 1 -count=1 -timeout=180s -json` over collect, ingest, query, sqlbuilder, postgresconn, operational/..., redisx, admission, hostedcli, githubapp, cliegress, githubquota, webhookgateway, webhookgatewayhost, webhookgatewaylocal, audit, auditdelivery, telemetry, logger, doctor; `soc2-component-tests.jsonl` | 3,149 passed / 250 skipped test events; 23 package passes; no failures |
| Same runner, `./internal/server -run 'Test.*(OAuth\|Session\|Revocation\|Expired\|EventStream\|Rebuild\|Admin\|CSRF\|CORS\|OIDC\|Proxy\|RateLimit\|Listen\|HostedModes\|Capability\|Authorization)'`; `soc2-http-tests.jsonl` | 332 passed / 19 skipped; one package pass; no failures |
| `go test -p 1 -count=1 -timeout=180s -json ./internal/cli -run '^TestDeployAzure(SourceBuild\|ImageBindings\|ImageReadBack\|DevelopmentContext\|Telemetry)'`; `soc2-deployment-tests.jsonl` | 44 passed / zero skipped; one package pass; mocked Azure only |
| Server runner over repositorymemory, marketplace, operationalvalue, model, sqlitex, postgresx; `soc2-data-support-tests.jsonl` | 447 passed / 56 skipped; five package passes; model package has no tests |
| `go -C internal/dashboardquery test -p 1 -count=1 -timeout=180s -json ./...`; `soc2-shared-query-tests.jsonl` | 1,125 passed / zero skipped; one package pass |

The artifact report records exact commands and full exclusions. Skips include
durable role/adapter/restart/atomicity/erasure cases, PostgreSQL query/storage,
Redis, destination conformance, external OpenObserve, published upstream
interoperability and native executor boundaries. Intentional subprocess helper
skips are not provider failures. Full repository validation, race detection,
image scanning, TypeSpec regeneration and historical operation were not run
as part of this bounded read-only review. The AICPA framework landing page was
checked on 2026-10-22; approved engagement criteria still require the auditor.

### Prioritized work and operator decisions

This is a proposed delivery sequence, **not new authorization, assignment,
risk acceptance or closure**. Existing remediation decisions below remain
unchanged. Calendar estimates would be unreliable without the deployed
inventory, evidence availability and auditor-approved examination objective.

| Finding/request | Operator choice | Suggested owner | Evidence needed to close | Decision |
| --- | --- | --- | --- | --- |
| First: SCOPE-01/02, GOV-01 | Approve system boundary, security baseline, additional categories and Type 1/Type 2 objective; assign accountable control owners | Service/compliance owner + independent auditor | System description, commitments, provider carve-outs, complementary user controls, approved criteria, date/period, policies/risk/personnel/vendor records | pending |
| Before production reliance: DATA-01/02/03, IAM-001/002, EXEC-01, OPS-01/02/03/04, DATA-08 | Verify current corrections on the intended release and perform coordinated rollout checks; do not open duplicate fixes from historical snapshots | Server/release + IAM/data/SRE | Release/deployed SHA and digests, relevant real-backend gates, legacy identity/grant/shard reconciliation, effective proxy/collector behavior, retained closure approval | existing remediate; lifecycle in-progress, not closed |
| High priority: DATA-04/05, IAM-003/005/006, EXEC-05, OPS-07 | Collect effective least-privilege, identity, transport and key evidence; separately authorize any proven deployment corrections | IAM/cloud security + DBA | Sanitized effective access/grants/ingress/secret inventory, reviews/offboarding, current/previous-key retirement and credential-revocation exercises | pending |
| High priority: OPS-05, OPS-OP-01 | Provision or verify independent audit custody and operate scheduled delivery/monitoring | SecOps/evidence platform | Independent anchors/retention/deletion protection, real adapter conformance, committed-row completeness, checkpoint/backlog/deadman alerts and acknowledged outage exercise | pending |
| High priority: OPS-06, DATA-07, EXEC-04, BCP-OP-01 | Define RTO/RPO and perform an approved isolated restore/failover exercise | SRE/data/platform | Timed restore including queues, revocation debt, permanent replay receipts, audit/anchors, keys and lake; measured loss and failback reconciliation | pending |
| Before enabling privileged executors: EXEC-02/05/07 | Validate intended Linux isolation/egress and process/key separation; approve narrow native-development exclusions if justified | Execution/platform security | Real intended-platform boundary and termination evidence, daemon/host ownership, digest/egress records and legacy-work disposition | pending |
| Before audit: DATA-06, EXEC-08, DATA-OP-01 | Approve per-copy data classification, retention/deletion/hold rules and verify maintenance | Data/privacy/compliance + DBA | Lake/canonical/cache/output/audit/independent-packet/backup inventory, deletion SLA/backlog/hold samples and backup expiry evidence | pending |
| Before audit: OPS-08, REL-OP-01 | Verify release enforcement, scanner exceptions and evidence retention | Release/change/security | Protected review/check/environment/bypass policy, exact artifact provenance/deployed digest, exception approver/rationale/expiry, rollback exercise and retained samples | pending |
| Before engagement close: IAM-004, EXEC-03/06, DATA-09/10 | Approve accurate assurance/cooperative-admission/race commitments, profile scope and resource limits | Service/security/auditor + query/SRE | Actual MFA/SSO policy, client/accounting failure behavior, ambiguous-send runbooks, explicit exclusions and physical capacity measurements | pending |
| Throughout a Type 2 period: all operating controls | Operate controls and retain attributable samples for the agreed period; complete independent examination | Accountable control owners + auditor | Access/change reviews, alerts/incidents, vulnerability dispositions, restore/rotation/deletion records and approved evidence sampling/retention | pending |

Type 1 assesses design at the agreed date; Type 2 additionally needs sustained
operating evidence. Neither today's tests nor the daily SelfCare worker can
retroactively create that evidence. Stronger isolation, KMS/HSM signing or HA
may be appropriate against approved commitments, but are not universal SOC 2
requirements or automatically authorized changes.

### Follow-up hardening authorization and metadata-read proposal

After the current-source assessment, the operator directed: "start separate
sessions to apply improvements". This authorizes the five bounded repository
workstreams below, with separate PRs; it does not authorize merges, production
changes, risk acceptance, exclusions or control closure.

| Workstream | Coordinated session | Current evidence |
| --- | --- | --- |
| DATA-10 metadata input bounds | `84846eb9-d1aa-42e5-876e-1371572695ea` | Draft [#19422](https://github.com/githubnext/gh-cao/pull/19422); proposal received |
| DATA-10 PostgreSQL resource budgets | `e8629f70-f53a-4dce-b9f8-4da29d04de4f` | Draft [#19425](https://github.com/githubnext/gh-cao/pull/19425); elapsed-time/operator-ceiling proposal received |
| OPS-01/05 missing-terminal audit detection | `ffb27ccf-ac05-42a3-9161-37e5f2fbd65f` | Draft [#19426](https://github.com/githubnext/gh-cao/pull/19426); bounded read-only detector proposed |
| OPS-08 release/deployment evidence collection | `bb48a53b-9959-4b2a-88ce-5f7f7c703f42` | Draft [#19423](https://github.com/githubnext/gh-cao/pull/19423); proposal received |
| IAM-006/EXEC-05 key custody and rotation | `baaf0260-80d5-48ed-86e4-fd85e0eb9584` | Draft [#19424](https://github.com/githubnext/gh-cao/pull/19424); offline rollover preflight proposed, not managed signing |

The metadata proposal is commit
`7f22e61b5f81b8891b2be01023b486d25cc73cf2`, based on
`4beb309dd0e415e7bb82e156cdc463d6c21fb55f`. The reporting session verified
that both affected production files were unchanged from the assessment SHA
before its patch. Coordinator independently checked open/draft/head/base
identity; it did not rerun the child session's tests.

At the proposal SHA, `server/internal/repositorymemory/memory.go:20-27,123-134`
caps manifest input at 8 MiB before decoding;
`server/internal/marketplace/resolve.go:82-105` caps generic JSON input at
16 MiB independent of Content-Length. Both read at most limit plus one byte,
reject overflow and validate the complete document. Existing file/path/hash
and repository-signal bounds remain. Required oversized registry responses
produce visible diagnostics; optional README failure behavior is retained
(`server/internal/marketplace/marketplace.go:392-402`;
`server/internal/marketplace/resolve.go:615-631`).

These two byte-bound claims are **design-supported at the proposal SHA**,
working mapping CC7 with candidate A1/PI1. This assessment branch does not
contain their runtime changes. The proposal intentionally rejects larger
inputs and trailing JSON documents/garbage; production workload suitability
still needs verification.

Session-reported verification: focused repositorymemory/marketplace race tests
passed 215 test/subtest events, zero failures/skips; selected server consumers
passed six events with six PostgreSQL-dependent skips. Scoped CI-pinned
golangci-lint reported zero issues; vet/build, whitespace and 73-workflow
compilation passed. Boundary-minus/exact/plus, dishonest length, bounded reads,
malformed/trailing input, closure, cancellation/read failures and caller
diagnostics were covered. Supporting report:
`/Users/peli/.copilot/session-state/84846eb9-d1aa-42e5-876e-1371572695ea/files/metadata-read-hardening-evidence.md`.

**DATA-10 remains evidence-needed and open.** Decision `remediate`, lifecycle
`in-progress` applies only to these authorized hardening slices; it is not
authorization to change fleet resource policy. Review/integration, decoded
heap/concurrency/DB/temp ceilings, filesystem I/O deadlines, deployed workload
suitability and period evidence remain outstanding. Operating effectiveness
is **not-assessed**. No exploitability or compliance conclusion follows.

#### Release evidence collector proposal

Draft [#19423](https://github.com/githubnext/gh-cao/pull/19423), commit
`fde438115bfc8bfec9ea2a5db81d6704baa72659`, starts from
`4beb309dd0e415e7bb82e156cdc463d6c21fb55f`, a descendant of the assessment
SHA. Coordinator verified open/draft/head/base identity, not runtime execution.
The collector is owner-invoked and read-only; no workflow, permission,
retention, deployment or automatic schedule is changed.

At that proposal SHA, `scripts/collect-release-evidence.mjs:21-115` collects
bounded package/promotion run-attempt and job references, approval IDs and
artifact identities/digests/expiry. Lines 117-140 optionally verify
attestations against exact image/source/repository/reusable publisher/SLSA
identity and matching run-attempt invocation. Lines 142-183 summarize existing
Azure development receipts with source/per-target/configured-reference
checks; lines 201-240 emit versioned attributed JSON with collector hash,
tool versions and explicit incomplete boundaries. Usage and limitations are
documented in `server/README.md:2329-2388`.

Session-reported validation: 73 synthetic collector/package/promotion tests
passed, zero failures/skips; root lint, CAO typecheck, whitespace and seeded
73-workflow compilation passed. Tests cover owner-only report permissions,
overwrite refusal, bounded/malformed/paginated input, identity and redaction.
Missing locked dependencies were restored before final checks. No real
registry/API integration or deployment was executed.

The narrow collection/identity-binding design is **design-supported at the
proposal SHA**, working mapping CC8.1/CC7.2. REL-OP-01/OPS-08 remain
**evidence-needed**, lifecycle **open**; only this collector implementation is
authorized `remediate` / `in-progress`. Reports deliberately remain incomplete:
run-scoped approvals/artifacts do not prove attempt binding or independent
approval, configured image read-back does not measure a running image, and
development rebuilds inherit no official package assurance. Raw verifier
responses are not archived; normalized receipts retain response hashes and
commands only. Operators still must independently retain original package
metadata/checksums, SBOM, signed bundles/verifier evidence, verify archive
access/expiry, and supply deployed measurements, enforced approvals/checks,
scan-exception dispositions, rollback and period records. Operating
effectiveness is **not-assessed**; no finding is closed.

**2026-10-22 user-directed implementation correction:** the collection session
is replacing the JavaScript collector with native Go operator command
`gh cao evidence release` under `internal/cli`, reusing Azure receipt types
and bounded `internal/ghcli` execution without a dashboard-server dependency.
The JavaScript citations and 73-test result above remain historical evidence
at the original proposal SHA, not validation of the replacement. Coordinator
observed open/draft head `4b5caa6483cb861df2988b70d7036fdbb7b7ddd6`;
final source-bound native implementation evidence and broader validation
handoff are pending. No new authority, retention or operating-evidence claim
is authorized by this language/location correction.

**Native migration final handoff, 2026-10-22:** verified open/draft head
`ef1e3356ca4aff3aff20f2a3e64e69f0c9d5799b` supersedes the pending revision
above. The JavaScript collector and its test file are removed. At this SHA,
`internal/cli/evidence.go:72` defines the native command/flags;
`internal/cli/evidence_release.go:274` collects the report;
`internal/cli/evidence_provenance.go:39` binds verifier policy/invocation;
`internal/cli/evidence_azure.go:41` reuses existing Azure receipt types and
development-only/per-target digest checks; `internal/ghcli/bounded.go:16`
bounds subprocess output with redacted failures. Operator contracts are in
`docs/cao-cli.md:29` and `server/README.md:2329`. No Node or dashboard-server
dependency is introduced.

The session reports all checks exiting zero: root lint/CAO typecheck,
`make lint`, `go test -race -json ./internal/cli ./internal/ghcli ./cmd/gh-cao -count=1`,
73-workflow compilation, docs build (two discovery tests), whitespace and
standalone build/help with `PATH=/nonexistent`. Race results: 1,322 passing
test/subtest events, one skipped `TestSetupRuntimePublishedRelease` requiring
`CAO_SETUP_RUNTIME_INTEGRATION=1`; all three packages passed. Log:
`/Users/peli/.copilot/session-state/bb48a53b-9959-4b2a-88ce-5f7f7c703f42/files/native-evidence-go-tests.jsonl`.
Coordinator verified publication/change surfaces, not runtime test execution.
The narrow native collection design is **design-supported at this SHA**;
REL-OP-01/OPS-08 remain **evidence-needed/open**. Reports remain incomplete,
raw verifier bundles are not archived, and destination ACLs remain
operator-owned (mode 0600 only where supported, especially relevant on
Windows). Actual registry/API verification, production image measurements,
independent retention/approval, exceptions/rollback and period evidence
remain unverified. Operating effectiveness is **not-assessed**.

**Usefulness review, 2026-10-22:** same source
`ef1e3356ca4aff3aff20f2a3e64e69f0c9d5799b`; coordinator verified the PR is
now open/ready, not merged. This operator-invoked GitHub/GHCR and optional
local Azure-receipt tool is useful as a bounded evidence index and
exact-identity checkpoint, not a self-contained independently retained CC8
audit packet. The following are conditional capability/evidence requests,
not confirmed security defects or violations of its documented contract.
Each is **evidence-needed/open**, confidence **high**, decision **pending**;
no additional implementation is authorized by this review.

| Request / severity / working mapping | Expected property and source-bound observation | Counter-evidence | Operator choice / suggested owner / closure evidence / tradeoff |
| --- | --- | --- | --- |
| OPS-USE-01 / medium / CC7.2, CC8.1 | A self-contained packet remains inspectable after source expiry. `internal/cli/evidence_provenance.go:7-15,61-66` retains a normalized response/hash, not the signed bundle/original bytes; a hash cannot reconstruct absent evidence. | Exact subject/source/signer/invocation and collector identity remain useful index evidence; missing retention is explicit. | Keep index-only scope or authorize bounded export of existing signed bundles/minimized verifier receipts/checksum manifest through approved delivery. Release owner/evidence custodian; close with original retained artifacts plus independent delivery/access/retention proof. Export alone is not independent retention and requires approved privacy/access handling. |
| OPS-USE-02 / medium / CC8.1 | A change sample binds candidate source/assets, approval and promotion. `internal/cli/evidence_release.go:249-272,299-307` checks workflow/run identity but lacks candidate/release/asset bindings; run-scoped approvals do not prove attempt-specific approval. | Unknown source binding is explicit; workflow SHA is not treated as product SHA; existing promotion artifacts can supply evidence. | Authorize consumption/checksums of existing candidate/validation artifacts, with exact source/release/assets/run/attempt/approval links. Release/change manager; close with an approved linked sample. Expired/conflicting artifacts remain incomplete; correlation must not duplicate promotion authority or infer independent approval. |
| OPS-USE-03 / low / CC7.1, CC8.1 | Reviewers can identify a declared gate and applicable policy. `internal/cli/evidence_release.go:168-191` records job IDs/step numbers/conclusions, not semantic test/scan/policy/database/exception references. | Excluding arbitrary names minimizes untrusted disclosure; exact workflow/job references permit investigation. | Define allowlisted immutable gate/policy/results/exception mappings. Release security owner; close with a reviewed control-to-result map and exception evidence. Exact supported contracts require maintenance; unknown shapes remain unmapped, without fuzzy inference or arbitrary name export. |
| OPS-USE-04 / low / CC7.2, CC8.1 | Operators distinguish recoverable collection failures and require evidence for an agreed purpose. `internal/cli/evidence_release.go:169,194,224,324-333` and `internal/cli/evidence_provenance.go:41` collapse some reasons to unavailable-or-invalid and always report incomplete. | Sanitized failures avoid raw-data exposure; mandatory identity failures stop output, and missing evidence is never promoted to success. | Define sanitized reason codes and an opt-in required-evidence profile with nonzero unmet-requirement results. Operator tooling owner; close with an agreed diagnostic/profile contract and checks. Collection completeness must remain separate from approval, retention and compliance; diagnostics must not expose raw responses or credentials. |

Session-reported source traces and focused synthetic checks passed:
`go test ./internal/cli ./internal/ghcli -run 'TestEvidence|TestExecBounded|TestBoundedGHHelper|TestCommands|TestInvalidCommands|TestOperatorCommandInventory' -count=1`
and `git diff --check main...HEAD`, both exit zero. No real attestation,
registry, Azure, approval, archive or examination-period observation occurred.
The session reports no high-confidence vulnerabilities in its delegated
security review and no safe bug fix/unresolved high-confidence defect in
fix-first review; this is not an independent coordinator security assessment.
Report:
`/Users/peli/.copilot/session-state/bb48a53b-9959-4b2a-88ce-5f7f7c703f42/files/evidence-command-usefulness-review.md`.
REL-OP-01/OPS-08 remain open. Examination scope, commitments, sampling,
retention and deployment coverage still require service-owner/auditor
agreement; operating effectiveness remains **not-assessed**.

#### Offline App key rollover preflight proposal

Draft [#19424](https://github.com/githubnext/gh-cao/pull/19424), commit
`eb1d079ad24c62402692b9bfea2263c831e96b15`, starts from the same
`4beb309dd0e415e7bb82e156cdc463d6c21fb55f` descendant baseline.
Coordinator verified open/draft/head/base identity. This is an offline
operator aid, not a change to runtime signing, key custody or deployed roles.

At the proposal SHA, `server/internal/githubapp/key_check.go:13-98` and
`server/cmd/cao-dashboard/app_key_check.go:14-73` implement explicit
`cao-dashboard app-key-check --key-file`: bounded regular-file RSA PKCS#1/
PKCS#8 consistency/size checks, exact SHA-256 SPKI public-fingerprint pinning,
unchanged-key rollover rejection, public-only output and sanitized failures.
The narrow preflight claim is **design-supported**, working CC6.1/CC6.7.
It cannot establish GitHub registration, remote key retirement or invalidation
of previously issued installation tokens.

Managed signing is deliberately **not implemented**. Existing Azure contracts
deliver Key Vault secrets as PEM and current App/gateway transports construct
startup-loaded PEM signers. The dependency's `WithSigner` seam has no request
context and supplies no provider key/version/identity/deployment contract.
Operator decisions are still needed for threat model/provider, import
feasibility of a GitHub-registered key, immutable version/public-key/RS256
binding, isolated signing grants, bounded cancellation and no-fallback
recovery. A signer abstraction alone would not establish these boundaries.

Session-reported race tests across githubapp, dashboard command, hostedcli and
gateway command passed 1,317 test/subtest events with 12 skips: deployed-data
benchmarks, PostgreSQL serving/recovery, Apple-native and real container-engine egress (then Docker).
New synthetic parser/command/rollover tests did not skip; they covered
overlap, simulated old-key denial/replacement, provider denial/unavailability,
invalid keys/pins, bounds and sanitized errors. Final scoped lint reported
zero issues; fmt/vet/focused race and seeded 73-workflow compilation passed.
Coordinator did not rerun those tests. Report:
`/Users/peli/.copilot/session-state/baaf0260-80d5-48ed-86e4-fd85e0eb9584/files/app-key-custody-report.md`.

EXEC-05/OPS-07 and related IAM-006 requests remain **evidence-needed** and
**open**; only the preflight implementation is authorized
`remediate` / `in-progress`. Required evidence still includes approved
versions/fingerprints, actual process/mount/App reach, replica drain/restart,
fresh mint/old-key-denial and issued-token cleanup, approvals and period
records. No KMS/provider call, key rotation/revocation, production or
permission change occurred. Operating effectiveness is **not-assessed**;
managed-signing decisions remain pending and no finding is closed.

**2026-10-22 parser follow-up:** verified open/draft proposal head
`451e6e94e00d6b089e321c1d1c57e711c1fc986e` supersedes the initial
`eb1d079ad24c62402692b9bfea2263c831e96b15` for current-source claims.
At the revised SHA, `server/internal/githubapp/key_check.go:35-43`
requires exactly one PEM BEGIN marker: `pem.Decode` could otherwise skip a
malformed or unterminated leading same-type block and accept a later valid
key. Parser regressions cover both cases; the command regression rejects
the malformed prefix without success output. The session reports reproducing
failures before fixing, then passing full changed-package race suites, vet,
pinned formatting and targeted lint (zero issues). Coordinator inspected the
published diff and verified PR identity, but did not rerun runtime tests.
This corrects the offline diagnostic's input contract; it establishes neither
a runtime-signing vulnerability nor closure of custody/provider/operating
evidence requests. Existing source citations and test counts above describe
the initial proposal; the follow-up does not change their historical scope.

#### PostgreSQL read-lifetime and operator-ceiling proposal

Draft [#19425](https://github.com/githubnext/gh-cao/pull/19425), commit
`95d5da40f14c75b8c12d0436419e9e2980b0afd7`, starts from
`4beb309dd0e415e7bb82e156cdc463d6c21fb55f`. Coordinator verified
open/draft/head/base identity; validation below is attributed to the session.

At this proposal SHA, `server/internal/postgresx/store.go:319-348` starts the
existing 60-second canonical read deadline before pool admission, honors
shorter caller deadlines and installs transaction-local statement/lock/idle
timeouts without widening stricter nonzero operator settings. Previously the
statement setting could replace a stricter ceiling and pool admission had no
whole-read deadline. The narrow elapsed-time/operator-ceiling claim is
**design-supported**, working CC7.1 with candidate A1.1. It deliberately bounds
cumulative setup/wait/statements rather than allowing each statement another
60 seconds. Result semantics and the existing per-store pool remain unchanged.

Session-reported PostgreSQL 18.6 validation used an owned private Unix-socket
cluster with synthetic schemas, subsequently stopped and removed. Full
postgresx tests passed in 587.663 seconds; focused physical/planner race cases
passed without skips. Query/sqlbuilder/command tests, vet/build, full server
lint (zero issues), final scoped lint/format and 73-workflow compilation passed.
Focused cases before service setup explicitly skipped; those were not service
evidence. Source and measurements:
`server/internal/postgresx/read_limits_test.go:23-245`;
`server/POSTGRES.md:69-127` at the proposal SHA.

Bounded observations included inherited 50ms statement cancellation at about
51ms, saturated pool cancellation at about 51ms without admitting its callback,
a 100ms caller deadline at about 101ms, and lock rejection with SQLSTATE 55P03
through the actual production statistics boundary. A generated 20,000-row
sort at inherited work_mem=64kB spilled 1,652 written temporary blocks /
6,448kB disk; inherited temp_file_limit=64kB rejected it with SQLSTATE 53400.
Times are local observations, not universal deadline guarantees. The spill
fixture uses the read wrapper, not representative Dashboard Language data.
Report:
`/Users/peli/.copilot/session-state/e8629f70-f53a-4dce-b9f8-4da29d04de4f/files/postgres-resource-budgets.md`.

**DATA-10 remains evidence-needed overall**. Only this source slice is
authorized `remediate` / `in-progress`; no closure or deployed setting change.
Physical statistics still precede logical rejection. No new guessed
work_mem/temp/parallel-worker policy was installed. CPU/I/O/memory/disk total
cost, fleet-wide replica/worker capacity, PostgreSQL 16/managed-provider
cancellation, legitimate workload suitability and period evidence remain
unverified. Deployed-artifact benchmarking was not run because local server/
data prerequisites were absent. Operating effectiveness is **not-assessed**.

**2026-10-22 constant-centralization follow-up:** verified open/draft head
`c623141efe611da61117875b2a004319c2b7787d` follows the session-reported merge
of main `bf11b39b0`. The published follow-up diff moves the unchanged 60-second
value to `server/internal/inputlimits/limits.go:20` as
`PostgresReadTransactionTimeout`, updating PostgreSQL consumers and tests.
The session reports post-merge inputlimits/postgresx/query tests, scoped lint
and build passing, with database-backed cases explicitly skipped in this
constant-only rerun. The owned-local physical observations above remain
attributed to `95d5da40f14c75b8c12d0436419e9e2980b0afd7`, not the new
merged head. Coordinator inspected the follow-up diff, not a combined
database-backed rerun; resource behavior/policy conclusions remain unchanged.

#### Revised metadata budgets supersede the initial proposal

User-directed revision in the metadata session supersedes the initial
8 MiB/16 MiB budgets recorded above and in SOC2-LOG-023. Current verified
draft [#19422](https://github.com/githubnext/gh-cao/pull/19422) head is
`51a569977074f87c7191c80621817424f060e2bf`. Original observations remain
history, not current defaults.

At this head `server/internal/inputlimits/limits.go:8-43` centralizes defaults:
2 MiB manifest (`CAO_REPOSITORY_MEMORY_MANIFEST_MAX_BYTES`), 512 KiB ordinary
marketplace JSON (`CAO_MARKETPLACE_JSON_MAX_BYTES`) and a separate 8 MiB
recursive repository tree (`CAO_MARKETPLACE_TREE_JSON_MAX_BYTES`).
Unset selects the default; present-empty, zero, negative, malformed, overflow
or maximum-platform-int values fail explicitly without echoing the value or
falling back. Limits reserve one byte for overflow detection and resolve
before relevant I/O. Source:
`server/internal/repositorymemory/memory.go:102-135`;
`server/internal/marketplace/resolve.go:84-112,417-418`. Other content and
repository-signal limits remain unchanged; maximum 256 KiB base64 package
content compatibility is covered.

The session reports final race tests across inputlimits/repositorymemory/
marketplace passing 283 test/subtest events, zero failures/skips; scoped lint
zero issues, vet/build including consumers, whitespace and 73-workflow
compilation passed. The prior six PostgreSQL consumer skips remain unverified
and were not rerun. Coordinator verified PR identity, not execution.
The updated report linked above preserves both revisions. Narrow byte-bound
claims remain design-supported at the new proposal SHA; effective environment,
operator-selected increases and deployed workload suitability are explicit
capacity-evidence dependencies. No startup validation of unused readers,
deployment or broader DATA-10 closure is claimed.

#### Bounded audit lifecycle detection proposal

Draft [#19426](https://github.com/githubnext/gh-cao/pull/19426), commit
`8346491ecaa735bc902b327d41f601124a066bad`, uses the same descendant base
`4beb309dd0e415e7bb82e156cdc463d6c21fb55f`. Coordinator verified
open/draft/head/base identity. At this SHA,
`server/internal/auditlifecycle/rebuild.go:82-272` and
`server/cmd/cao-dashboard/audit_lifecycles.go:18-60` implement
`cao-dashboard audit lifecycles` via OpenAuditReader/ListAudit only.

Default scan is 10,000 rows, hard bound 100,000 plus one lookahead, pages at
most 1,000, default deadline two minutes. Default one-hour grace has a
35-minute minimum beyond the current work timeout/lease. JSON/exit status
distinguishes possibly-active pending IDs, missing terminal evidence,
retained/selected suffixes, gaps, truncation, invalid/duplicate/conflicting
evidence and status-write failure. Capped scans do not label unread
completions missing; duplicates cannot reset grace, gaps suppress unsupported
missing-terminal claims and suffixes preserve incomplete coverage.
Canceled/failed/succeeded completions are recognized without choosing an
outcome for conflicting records. No history/status/canonical writes, fabricated
outcomes, replay, cross-store atomicity or new scheduler/service is introduced.

The narrow detection/uncertainty/read-only claims are **design-supported** at
the proposal SHA, working CC7.2/CC7.3 with CC6.3 for read-only authority.
Session-reported final race results: detector 34 passing test/subtest events,
CLI 276 passing/three PostgreSQL-dependent skips; focused rebuild/reader tests
passed with PostgreSQL independent-history skipped. Nonrace audit/SQLite,
vet, full-server formatting/lint (zero issues) and 73-workflow compilation
passed. Coordinator did not rerun these tests; no service was started.
Report:
`/Users/peli/.copilot/session-state/ffb27ccf-ac05-42a3-9161-37e5f2fbd65f/files/audit-lifecycle-handoff.md`.

OPS-01/OPS-05 remain unresolved, with detector implementation
`remediate` / `in-progress` only. Operators still must approve grace/cadence,
run the real SELECT-only adapter, retain independently anchored complete
history, monitor dead checks/backlog/alerts and manually reconcile uncertain
IDs without synthesizing outcomes or replaying ingestion. Row-capped/retained
history is not a full-history proof. Operating effectiveness is
**not-assessed**.

All five follow-up sessions have now delivered separate draft proposals.
Their source claims are tied to separate heads, not a tested combined release
or proof of integration/deployment. No merge, operational closure, approved
exclusion or risk acceptance is implied.

## Assessment boundary and current status

Started 2026-10-21 against source revision
`ef7d9fb65ea952826f9d81a94bd5890d3cfd5634`.
This is an evidence-driven readiness investigation, not a SOC 2 attestation or
a declaration of compliance. No deployed environment, examination period,
service commitments, or organizational control evidence has been supplied.
This assessment alone authorizes no runtime or deployment changes. Subsequent
user-directed repository remediation is recorded in the authorization section
below; production changes and merging remain unauthorized.

All four bounded component investigations have reported. The initial
technical pass found **10 scoped design gaps** (one high, eight medium,
one low), **21 evidence-needed records**, and **one proposed profile
exclusion**, in addition to the initial cross-cutting evidence register.
These are workstream records, not independent criterion/control counts;
shared operational evidence dependencies are linked below. The initial
dispositions are preserved below; current remediation decisions are recorded
separately. Operating effectiveness is not assessed and no compliance
conclusion follows.

Highest-priority demonstrated design issue: DATA-01 can delete another
repository's evidence when distinct coordinates flatten to the same shard
prefix. Other demonstrated data gaps concern shared withdrawal erasure and
erasure projection ordering. Identity gaps concern unknown-expiry credential
cleanup and metadata-only long-lived stream authorization; EXEC-01's impact
depends on unverified account rename/reassignment behavior. OPS findings are
explicitly scoped to the stated audit/monitoring/build/diagnostic objectives.
An existing temporal query contract check also failed (DATA-08); the
assessment MUST NOT be described as having a clean overall validation gate.

The reusable procedure is
[soc2-server-assessment](../.github/skills/soc2-server-assessment/SKILL.md).
The maintained [assessment specification, RFC-035](../specs/server-soc2-compliance.md)
defines the evidence and decision contract using BCP 14 requirement language,
stable `S2-` requirement IDs and role-specific conformance classes; the append-only
[assessment changelog](SOC2-CHANGELOG.md) records changes and dispositions.
This register uses specification version 1. Component reports and later
operator decisions MUST update this register and append a changelog event.
RFC conversion is editorial: existing finding/status/decision history and
the established specification path remain unchanged.
The authoritative security intent remains [SECURITY.md](SECURITY.md) and the
referenced specifications. This inventory does not change the architecture.

Initial scope covers the `server/` Go module and its direct shared query,
deployment, and build dependencies. GitHub, database/cloud providers, the
operator's identity lifecycle, and downstream workflows are external trust
boundaries: assess their integration here, request independent operational
evidence, and do not assume their controls are effective.

Security/Common Criteria is the starting baseline. Availability, Processing
Integrity, Confidentiality, and Privacy require explicit applicability
decisions with the operator/auditor. They are not declared out of scope.
The [AICPA framework reference](https://www.aicpa-cima.com/resources/download/2017-trust-services-criteria-with-revised-points-of-focus-2022)
was consulted on 2026-10-21; exact engagement criteria and edition still need
confirmation.

## System and data-flow map

```text
Browser -> trusted listener/proxy -> same-origin Go app -> canonical query store
                                   | OAuth sessions       PostgreSQL (hosted)
                                   | quotas/cache         SQLite (local)
                                   v
                            operational services
                            PostgreSQL / Redis / memory

GitHub -> signature/OIDC checks -> collection/admission/gateway queues
                                      |                  |
                                      v                  v
                             canonical publication   dispatch GitHub workflows

Step-up administrator -> declared CLI queue -> isolated executor -> GitHub App API

Security-relevant actions -> audit chain -> independent delivery destination
All roles -> bounded telemetry -> operator monitoring and incident process
Reviewed source -> CI/package scans -> image/artifact -> operator-controlled host
```

The arrows are logical boundaries, not a promise that all roles/features run
together. Production gateway ingress and dispatch can be separate processes;
hosted CLI web admission and execution have different credential authority.

| Component | Primary paths | Assets and boundary | Workstream |
| --- | --- | --- | --- |
| Entrypoints and hosting | `server/cmd/`, `server/hosting/`, `server/internal/server/hosted.go` | Local bearer vs hosted OAuth; process roles; trusted HTTPS listener/proxy | IAM |
| HTTP, OAuth, browser session, MCP | `server/internal/server/` auth, OAuth, proxy, CORS, CSRF, rate-limit and MCP handlers | User/admin identity, membership refresh, step-up, Actions OIDC and API tokens | IAM |
| Canonical database and query execution | `server/internal/postgresx/`, `dashboardstore/`, `sqlitex/`, `query/`, `sqlbuilder/`, `internal/dashboardquery/` | Dataset-wide access; bounded queries; transaction/snapshot integrity | DATA |
| Ingestion and collection | `server/internal/ingest/`, `collect/`, server collector handlers | Manifest/source trust, collection credentials, atomic publication and retention | DATA |
| Operational adapters and database privileges | `server/internal/operational/`, `redisx/`, `postgresconn/`, `server/postgres/` | Sessions, protected queues, replay records; owner/runtime/retention/evidence separation | DATA |
| Derived services | `server/internal/repositorymemory/`, `marketplace/`, `operationalvalue/`, `model/` | Untrusted evidence and external registries; caching/publication authority | DATA |
| Activation and conclusion protocols | `server/internal/admission/`, server admission handlers | Exact enrollment, OIDC, accounting freshness, replay and partial evidence | EXEC |
| Webhook gateway | `server/internal/webhookgateway*/`, `server/cmd/cao-webhook-gateway/` | Signed ingress, policy revisions, discovery, durable dispatch and ambiguous outcomes | EXEC |
| Hosted CLI and credential/egress brokers | `server/internal/hostedcli/`, `githubapp/`, `cliegress/`, `githubquota/`, CLI relay/quota commands | Step-up admission, live repository permissions, isolated execution, token cleanup | EXEC |
| Audit trail and independent export | `server/internal/audit/`, `auditdelivery/`, `server/audit-delivery/` | Evidence integrity, independent anchors/destination, retention and alerting | OPS |
| Telemetry, diagnostics and lifecycle | `server/internal/telemetry/`, `logger/`, `doctor/`, server health/lifecycle | Sensitive-data exclusion, alert routing, shutdown/recovery and incident evidence | OPS |
| Build, release, deployment and contract | `server/Dockerfile`, `server/spec/`, `server/azure/`, `.github/workflows/cgo.yml`, `cao-package.yml`, relevant deployment scripts | Provenance, vulnerability management, approvals, secrets, rollback, service providers | OPS |
| Test infrastructure | `server/internal/simulator/`, `testutil/`, package tests | Synthetic evidence only; never a substitute for production operating evidence | Each owner |
| Shared infrastructure | `server/internal/clock/` and shared contracts | Time/expiry assumptions and package relationships | Each owner |

Paths abbreviated within the table are relative to `server/internal/` where
unambiguous. The assessment reports cite exact paths and line ranges.

### Profiles that must not be conflated

| Profile | Assessment distinction |
| --- | --- |
| Local `serve` with SQLite | Trusted loopback operator, capability authentication, read-only snapshot; not a public hosted deployment |
| Hosted/external listener | GitHub OAuth and explicit organization/team policy; same-origin app; exact trusted proxy/host boundary |
| Azure Functions | Hosted identity plus platform listener, secret-manager, network and lifecycle controls; templates are not deployed evidence |
| Operational PostgreSQL | Durable default; canonical and operational state have distinct contracts and privilege requirements |
| Operational Redis | Explicit alternative with transport, namespace, capacity and durability assumptions |
| Operational memory | Explicit single-process volatile exception; restart invalidates state and can lose pending work |
| Admission-only and separate workers | Disabled/explicitly selected capabilities; protected PostgreSQL replay and enrollment required |
| Gateway local SQLite vs production roles | Local file authority differs from PostgreSQL role separation; ingress and dispatch credentials differ |
| Experimental hosted CLI | Off by default; execution platform, egress, image, live authority and cleanup evidence needed per enabled runner |

## Investigation assignments

Four coordinated read-only sessions own the workstreams above. They follow
the assessment skill, investigate implementation and focused tests deeply,
and save reports as session artifacts. They must not fix issues or change
production. Each report includes its exact source SHA, component coverage,
control matrix, findings, limitations and operator decision requests.

| Workstream | Working criteria focus | Initial state |
| --- | --- | --- |
| IAM: identity and HTTP access | CC6, CC7; authorization, sessions, transport and resource admission | Report received; scoped review complete; operational evidence pending |
| DATA: persistence and data handling | CC6, CC7; candidate C1, PI1 and A1; storage, integrity, retention and recovery | Report received; scoped review complete; operational evidence pending |
| EXEC: admission, gateway and execution | CC6, CC7, CC8; candidate PI1; bounded authority and privileged side effects | Report received; scoped review complete; operational evidence pending |
| OPS: audit, operations and releases | CC7, CC8, CC9; candidate A1; evidence integrity and operational dependencies | Report received; scoped review complete; operational evidence pending |

The investigation baseline, including the shared assessment procedure, is
`e3daa5d7ad4fa537e6432359076b44f4a90be171`. It changes documentation only
relative to the runtime revision above. Coordinated session identifiers:

| Workstream | Session ID |
| --- | --- |
| IAM | `0cab34cf-451f-469b-88d8-6c3727a86702` |
| DATA | `3665e605-4134-497a-b22a-08335594f363` |
| EXEC | `295a6967-1963-4ef9-80a1-ec87bee9f18b` |
| OPS | `76a730cd-672d-4c67-9e03-cb2c3c0460fe` |

These are working mappings, not conclusions about whole criteria.
Cross-cutting CC1-CC5 governance and risk assessment remain coordinator-owned
operator evidence requests. Completion requires consolidation of all four
reports, deduplication, and explicit accounting for unreviewed surfaces.

## Initial evidence and operator decision register

The entries below are evidence requests, not confirmed implementation defects.
Source review establishes neither deployed settings nor historical operation.
No operator decision has been made.

| ID | Status | Operator decision/evidence needed | Suggested accountable role |
| --- | --- | --- | --- |
| SCOPE-01 | evidence-needed | Confirm system boundary, production profiles/features, commitments, Type 1/Type 2 objective and examination date/period | Service owner and auditor |
| SCOPE-02 | evidence-needed | Determine applicability of A1, PI1, C1 and P1-P8 using data classification, contracts and actual processing | Compliance/privacy owner |
| GOV-01 | evidence-needed | Supply approved control owners, risk assessment, security policies, personnel training and vendor oversight evidence for CC1-CC5/CC9 | Compliance owner |
| IAM-OP-01 | evidence-needed | Supply sanitized effective identity/admin/App scope configuration, access review and offboarding evidence | Identity owner |
| DATA-OP-01 | evidence-needed | Supply database-role review, encryption/key rotation, retention/deletion configuration, backup and restore evidence | Data/platform owner |
| OPS-OP-01 | evidence-needed | Supply independent audit anchor/destination/retention evidence, alert delivery/acknowledgements and incident exercise records | Security operations |
| REL-OP-01 | evidence-needed | Supply enforced branch protections, actual release approvals, image provenance/digests, scan dispositions and rollback evidence | Release owner |
| BCP-OP-01 | evidence-needed | Define RTO/RPO and service objectives; supply recovery/failover exercises and vendor dependency review | Service/platform owner |

Existing [hosted CLI SOC 2 mapping](SECURITY.md#soc-2-evidence-boundary),
[PostgreSQL authority separation](POSTGRES.md#authorities), and
[independent audit delivery](audit-delivery/README.md) are starting evidence,
not controls certified by this report.

## Consolidation and decision gate

### Current remediation authorization and sessions

On 2026-10-21 the operator directed: "start session to fix each findings",
then "create an self care agentic workfler worker that does this check again
and fixes the top 3 issues daily".

This authorizes scoped repository implementation/tests and draft PRs for the
10 design gaps and investigation/remediation of the failed DATA-08 gate.
Those records now have decision **remediate**, lifecycle **in-progress**.
The assessment observations/statuses below remain the original baseline,
not claims that fixes are verified, merged or deployed. Suggested owners
remain suggested organizational roles; the sessions own repository work only.

Evidence-only records other than DATA-08 and the proposed exclusion retain
decision **pending**, lifecycle **open**. The directive does not manufacture
deployment/history evidence, accept risk, approve an exclusion, grant secret
access, widen rollout, or authorize production execution/merge.

Each remediation session starts from the consolidated assessment baseline
`748093b701ef9cf22e0323ac84fc6b026ea526ac`, owns one focused draft PR and
reports validation/limitations to the coordinator. Central assessment records
remain coordinator-owned to avoid conflicting edits.

| Finding / work | Session ID | Current state |
| --- | --- | --- |
| DATA-01 shard identity/erasure collisions | `603b87ae-e256-4f86-b590-e9181384fda8` | Open non-draft [#19325](https://github.com/githubnext/gh-cao/pull/19325); integrated-base head `dfdd11f3c`, CI pending; not merged/closed |
| DATA-02 shared withdrawal erasure | `270f92b9-f242-4792-aebc-5333c54a7f91` | Open non-draft [#19326](https://github.com/githubnext/gh-cao/pull/19326); shared/derived erasure proposal, not merged/closed |
| DATA-03 durable erasure projection intent | `fcb3024f-e955-4995-89c0-9a3234795319` | [#19322](https://github.com/githubnext/gh-cao/pull/19322) merged into assessment branch; combined/default-branch validation pending |
| IAM-001 unknown-expiry credential cleanup | `5b7894b6-166a-4b55-bfdb-7723269d970d` | [#19327](https://github.com/githubnext/gh-cao/pull/19327) merged into assessment branch; coordinated rollout/reconciliation and default-branch closure pending |
| IAM-002 existing-stream revocation | `69b8c769-0b58-4322-ae9f-8fe97875d0ca` | [#19318](https://github.com/githubnext/gh-cao/pull/19318) merged into assessment branch; default-branch/provider closure pending |
| EXEC-01 immutable queued requester identity | `1b133541-ad96-4c4e-9944-4497920f71e0` | Open non-draft [#19328](https://github.com/githubnext/gh-cao/pull/19328); immutable actor proposal, not merged/closed |
| OPS-01 durable rebuild attribution | `10ab76b1-e205-4b8c-ad67-28870959d441` | Open non-draft [#19323](https://github.com/githubnext/gh-cao/pull/19323); durable lifecycle proposal, not merged/closed |
| OPS-02 explicit Azure Go telemetry route | `69ae4f91-2de9-4617-b1ec-35698d58ba66` | [#19324](https://github.com/githubnext/gh-cao/pull/19324) merged into assessment branch; deployed role/collector/alert evidence pending |
| OPS-03 source-built image assurance | `61c4bba4-3caa-43af-89e5-5b3b3573097f` | Draft [#19317](https://github.com/githubnext/gh-cao/pull/19317); development-only restriction proposed, not merged/closed |
| OPS-04 unknown persistence diagnostic | `39f2395b-aea6-4e1e-9dbc-d9867e7c8083` | [#19304](https://github.com/githubnext/gh-cao/pull/19304) merged into assessment branch; targeted integration passed; default-branch/deployment closure pending |
| DATA-08 failing temporal query contract | `ca0f22b3-e8a7-4244-bf2c-35c0e1027c3d` | [#19305](https://github.com/githubnext/gh-cao/pull/19305) merged into assessment branch; targeted integration passed; default-branch gate pending |
| Daily SelfCare SOC 2 worker | `e0f32d7e-d753-4484-8876-251fd467257d` | [#19310](https://github.com/githubnext/gh-cao/pull/19310) merged into assessment branch; contract checks passed; active-control/default-branch integration pending |

The recurring worker must follow
[specification section 8](../specs/server-soc2-compliance.md#8-daily-selfcare-reassessment-and-bounded-remediation).
It is a proposed reviewed workflow, not an app session automation. Daily
execution is not active until the workflow/policy changes are reviewed,
merged and installed on the applicable default branch.

The following report sections preserve initial observations and initial
pending decisions. This current authorization table supersedes only those
decision/lifecycle fields explicitly listed above. A draft PR is not closure.

On 2026-10-21 at approximately 15:39 UTC the coordinator refreshed GitHub:
OPS-04 and DATA-08 PRs are both open and non-draft with unchanged heads.
Their initial draft creation is preserved in history; the current review
state was not overridden. OPS-04's Go CI fails on the existing DATA-08
temporal query assertion, according to the remediation session's log review
and baseline-tree comparison. The coordinator verified the failing check and
the separate DATA-08 correction PR; it has not merged/copied either fix or
declared the affected integration gate clean.

Subsequent GitHub refresh observed OPS-04 merged at 15:40:18 UTC and
DATA-08 at 15:40:41 UTC, into this assessment branch as `90b865397` and
`c85b8dfe7` respectively. These remote merges were not initiated by the
coordinator. It preserved them while rebasing one unpublished documentation
commit, without overriding remote work or changing PR review state.

The coordinator then ran
`go -C server test ./internal/doctor ./internal/query -count=1 -json` on
the integrated source: **311 passing test/subtest events, 18 skipped,
zero failures**. All skips are PostgreSQL-dependent query cases; the
renamed native-measure contract and doctor cases passed. The DATA-08 session's
isolated PostgreSQL-backed result remains additional, separately sourced
evidence. Artifact:
`/Users/peli/.copilot/session-state/d19b961a-c3f0-469a-847f-bcd18d3c8d23/files/soc2-integrated-doctor-query-tests.jsonl`.

Both records remain decision **remediate**, lifecycle **in-progress** for
default-branch integration/relevant final checks. OPS-04's original design-gap
and DATA-08's original failed-gate observations remain historical; the
assessment branch now contains their technical corrections. No deployed
provider/backup control or operating-period evidence is closed.

#### Daily worker proposal received 2026-10-21

Draft [#19310](https://github.com/githubnext/gh-cao/pull/19310), commit
`c132cc6d01809d2b99fb86e36dd56430355a44ef`, proposes
`self-care-server-soc2`. Coordinator verified open/draft/head/base identity,
12-file scope and the policy diff's new worker registration only; no existing
target/mode/authority change. The dispatch ceiling grows from 20 to 21 to
include the worker, without changing orchestrator/worker credit ceilings.

The proposal reuses the existing every-20-minutes orchestrator's rolling
24-hour eligibility convention, plus a manual-dispatch duplicate-day guard.
Queued/in-progress and recent failed/cancelled runs count; unavailable,
ambiguous or truncated history defers dispatch. Shared admission and an
exact-target/live activation guard remain mandatory.

Daily JSON/Markdown artifacts and a run summary are produced independently of
the remediation PR guard. Selection ranks current verified actionable code
gaps by severity/confidence/stable ID/path, excludes addressed or open
remediations across authors, and permits at most three compatible fixes.
Evidence-only gaps are not forced into code changes or fabricated closure.
An existing worker PR suppresses another PR, not assessment; material durable
register/changelog deltas are then marked pending/incomplete for human
consolidation.

Compiled safe-output proposal: at most one draft PR, 12 files (four production
Go files plus six tests in one package, plus two assessment records), 50 KiB
patch bound, and only `server/internal/**/*.go`, `server/cmd/**/*.go`,
`server/SOC2.md` and `server/SOC2-CHANGELOG.md` paths. Protected-file fallback
to an issue is disabled. Workflow/policy/credential changes cannot be smuggled
through that output. Unsupported wider fixes are deferred. Limits are 45
minutes/400 AI credits with bounded read/check/report work.

The authoring session reports 303 workflow/policy contract tests and 258
admission/resolver/failure integration tests passing without skips, lint and
both repository type checks passing, and 73 seeded workflow compile/lock
outputs reviewed. These validate authoring contracts, not runtime production
controls or actual daily dispatch. Validation artifacts:
`/Users/peli/.copilot/session-state/e0f32d7e-d753-4484-8876-251fd467257d/files/daily-soc2-checks/`.

Activation still requires assessment prerequisites and worker source/policy
on the active reviewed control branch, required existing credentials and
Actions configuration. No dispatch/deployment/merge was performed by the
authoring session/coordinator. Fourteen-day daily artifacts are not a
substitute for operator-approved examination-period evidence retention
(OPS-08). No previous finding is closed by authoring this worker.

GitHub subsequently reported this worker merged into the assessment branch
at 15:50:10 UTC as `4b63f3dcb6478dbc42f948d262131b2b32593a6e`.
The coordinator preserved that externally merged change while rebasing its
unpublished DATA-03 documentation update. Integrated
`node --test tests/unit/workflow-contract-selfcare-soc2.test.mjs` passed
all seven tests, zero failures/skips. Initial execution lacked the locked
`yaml` dependency; `npm ci --ignore-scripts --no-audit --no-fund` restored
dependencies before rerun, without manifest/lock changes. Integrated contract
log:
`/Users/peli/.copilot/session-state/d19b961a-c3f0-469a-847f-bcd18d3c8d23/files/soc2-integrated-worker-contracts.log`.
This is assessment-branch integration, not a daily run or activation proof;
default/active-control-branch prerequisites and operating evidence remain.

#### IAM-002 and OPS-03 proposals received 2026-10-21

The coordinator verified both proposals' GitHub open/draft/head/base identity
and commit scopes. Both remain decision **remediate**, lifecycle
**in-progress**. Original assessment observations are unchanged and neither
proposal is merged, deployed or a closed operational control.

IAM-002 draft [#19318](https://github.com/githubnext/gh-cao/pull/19318),
commit `6839329d8c442f58f5ab552ddda792c104bc2c59`, bounds OAuth revision
streams by the earliest membership recheck deadline (AuthorizedAt plus five
minutes), token-refresh threshold, or admitted administrator-assurance
deadline. Fixed context/write deadlines prevent traffic from extending
authority. A session-store read each 20-second heartbeat, with five-second
timeout, disconnects revoked/unavailable authority. Reconnect reuses existing
authorization/refresh/cookie helpers; expired administrator assurance does
not regain health metadata; local capability compatibility is retained.

The session reports reproducing the baseline adverse logout stream with an
artifact overlay, then adding actual old-stream/hub cleanup, membership/admin
expiry, refresh/reconnect, outage/timeout, blocked-check abort/drain,
bounded-read and real loopback write-deadline coverage. Focused race checks
passed five repetitions. Full server tests reported 370 top-level passes and
68 top-level skips (1,179 pass/72 skip events with subtests), zero failures;
format/lint/vet/build and 72-workflow compilation passed. Database-dependent
tests remain skipped. Remaining evidence includes proxy buffering/lifetimes,
already-written bytes and real backend outages: revocation observation is
bounded by heartbeat plus read timeout, while undiscovered membership loss
retains the ordinary five-minute window. IAM-001 storage-lifecycle integration
must preserve expired-session read denial.

OPS-03 draft [#19317](https://github.com/githubnext/gh-cao/pull/19317),
commit `bb897f7e76932704aa5c22640257abfaa940237b`, chooses an explicit
**development-only source-build apply restriction**, not an invented
production scan/promotion policy. Config and reviewed plan must both declare
development before Azure calls; legacy/missing/production declarations and
missing per-target digest parameters fail closed. Read-only production
planning does not authorize apply. Separate Functions, ingestion and optional
provisioning digests bind into Bicep and are read back before jobs; receipts
state scan/SBOM/provenance limitations and capture source/context identities.
Generated ARM is regenerated, not hand-edited.

The session reports `make check`, focused assurance race tests, 35
Bicep/assets/package contract tests, repository lint/both type checks,
72-workflow compile, docs build and diff checks passing. Azure calls were
mocked. Development declaration does not verify subscription classification;
configured-image read-back is not a running-image measurement, and local
context/status hashes are not signed atomic provenance. Production remains
unsupported by this apply route without a separate reviewed target-specific
scan/disposition/SBOM/provenance/promotion gate. OPS-02 must preserve these
image parameters when reconciling telemetry template changes.

#### DATA-03 proposal received 2026-10-21

Draft [#19322](https://github.com/githubnext/gh-cao/pull/19322), commit
`79830714dd4b40453b0c15eefa1bf6569c475af7`, changes five files:
collector worker/projector, two test files and README. Coordinator verified
open/draft/head/base and scope. Decision **remediate**, lifecycle
**in-progress**; original design-gap is not closed by the unmerged proposal.

Both erasure branches use one `processErasure` helper: remove shards, persist
projection intent, then ACK. Failed/cancelled marker writes leave the original
lease reclaimable. Requested/completed projection generations are separate;
completion is recorded only after canonical ingestion commits, and cannot
consume newer intent. Existing backend StateStore/queue/lease and canonical
transaction contracts are preserved.

The session reports the full collector race suite on isolated Redis and
PostgreSQL passing 260 test/subtest events, zero failures/skips:
`GOMAXPROCS=2 go -C server test -p 1 -race ./internal/collect -count=1 -json`.
Coverage includes both durable adapters, marker failure/cancel/reclaim,
canonical removed/kept rows, rollback, pre/post-commit crash-equivalent panics,
completion failure and newer concurrent intent. An initial loaded-host run
hit an existing 30-second parity-test deadline; the unchanged bounded-concurrency
rerun passed. Server lint/format/build, collector vet and workflow compile
also reportedly passed. Final artifact:
`/Users/peli/.copilot/session-state/fcb3024f-e955-4995-89c0-9a3234795319/files/data03-collect-race-final.jsonl`.

This is controlled local backend evidence, not provider power-loss/OS-kill,
deployed recovery-objective, principal-isolation or encryption proof. Services
were stopped. DATA-01/02 remain separate; integration must use their
`ForgetContext(ctx, lease.Task.Repository)` within the new single helper,
not restore the former duplicated branches.

#### DATA-01, DATA-02, OPS-01 and OPS-02 proposals received 2026-10-21

Coordinator verified each open/draft/head/base and commit scope. All retain
decision **remediate**, lifecycle **in-progress**. These proposals are
unmerged; no deployed correction or operational closure is inferred.

DATA-01 draft [#19325](https://github.com/githubnext/gh-cao/pull/19325),
commit `03c5fdb5c89a9b45752151ee4094cb878fb4520a`, replaces flattened
repository identity with prefix-free
`repo-v2~owner~repository~-logs-`, fitting maximum supported coordinate
lengths and separating legacy/new namespaces. Identity-verified, no-clobber
legacy migration precedes cache selection; erasure preflights every candidate
before deletion; mixed-identity compaction fails before mutating any group.
Ambiguous legacy evidence remains untouched for explicit operator repair.
Unsupported hard links fail safely retaining source. Both legacy/new raw
replay remain supported; shared/derived values stay DATA-02-owned.

The DATA-01 session reports 253 Activity-area tests passing without skips,
and final serialized race tests for collector/ingest/simulator on isolated
Redis/PostgreSQL passing 339 tests (233/11/95), zero failures/skips.
Root lint/both type checks, collector lint/format, server build and workflow
compile passed. An initial concurrent-load parity timeout passed on unchanged
standalone/serialized rerun; services were removed. Deployed collision
inventory, migration rollout and filesystem/operator repair evidence remain
required; no production loss or collision was observed.

DATA-02 draft [#19326](https://github.com/githubnext/gh-cao/pull/19326),
commit `ca720d64091b12efb6408489b8c3e3f4f6c3c010`, filters collector
shared values by exact case-insensitive repository identity while preserving
unrelated raw/extensions. It invalidates affected disposable normalized/cache
shards, serializes erasure/projection with a cross-process lake lock, prunes
enrollment before best-effort acquisition including zero enrollment, and
keys operational-value query caches by committed canonical revision.
Filtering/locking is inside `ForgetContext`; no prefix migration or ACK
protocol duplication.

The DATA-02 session reports passing a real webhook-to-worker-to-Activity-
compaction-to-isolated-PostgreSQL withdrawal regression, including unrelated
values, zero enrollment, acquisition failure and failed-compaction retry.
Focused race/transfer/cache checks, collector/value suites with isolated Redis,
value race/publication-preservation cases, server build/affected vet,
format/lint and workflow compile passed. Initial shared Redis/disk conditions
were avoided with owned tmpfs services, later removed. Not every service-backed
test/full root/fleet/provider-principal check ran. Independently published
packets remain intentionally protected pending disposal-authority decisions;
prior committed snapshots serve during failed projection until retry.
Old cache bytes expire under bounded TTL but cannot serve after revision.
Actual shared-filesystem advisory-lock support is an operator prerequisite,
not a demonstrated multi-host/power-loss guarantee.

OPS-01 draft [#19323](https://github.com/githubnext/gh-cao/pull/19323),
commit `0f0a42612a1b3cf9e561d37b31be92cd324ae375`, proposes durable
actor-attributable admission/start/outcome events with a fresh rebuild ID
(not the lease token), trace/revision/timestamps and bounded fixed outcomes.
Admission/start precede reconciliation. Terminal/status storage failures are
explicit; durable evidence can survive mirror failure, but not joint storage/
mirror outage. Authorization/lease/publication semantics remain, with
intentional hosted rebuild refusal (503) where no separate durable audit
service is supplied, including Redis/memory profiles. Shared denial-audit
failure also surfaces 503 without granting access. API contract becomes 2.10.0.

The OPS-01 session reports PostgreSQL runtime-role/SELECT-only independent
reader-chain checks, SQLite retained history and focused outage/cancel/drain/
success/failure race tests passing; server lint/format/build/vet, TypeSpec
29 tests plus reproducibility, and workflow compile passed. Its baseline
full unfiltered suite reproduced DATA-08 only; a filtered run skipped that
known failure, not a clean unfiltered gate. DATA-08 is now corrected separately
in the parent. Cross-store publication/audit/status are explicitly non-atomic;
crashes/uncertain appends can leave unpaired IDs needing independent
reconciliation. No outbox, complete crash reconstruction, actual rebuild,
production retention/export/anchor/alert effectiveness or period proof claimed.

OPS-02 draft [#19324](https://github.com/githubnext/gh-cao/pull/19324),
commit `2296bdd84ca101d2a9632819020bc502dad87826`, supplies reviewed
optional HTTPS OTLP/HTTP endpoint and explicit log opt-in for Functions/
collector/backfill, exporter headers via Key Vault-only secret references
and conditional secret-scoped grants. CLI validation/secure parameter/
redaction/secret-free plan behavior is tested. Core remains vendor-neutral
with no Azure SDK or exporter readiness dependency; selected worker debug
namespaces are narrowed on log opt-in. External collector ownership is
documented; Bicep/ARM generation is deterministic.

The OPS-02 session reports root/server lint/format, full root CLI tests,
telemetry/logger tests and command builds, authenticated-TLS protobuf
trace/metric/log receipt and failure-isolation race checks, secure config/
plan tests, Azure template/assets contracts, both type checks, workflow compile
and docs build passing. Azure calls/credentials were synthetic.
Actual per-role settings, approved collector TLS/auth/network/retention,
backend receipts and absent-signal/failure-alert response still require
operator evidence. Request availability and independent audit must remain
separate. Combining OPS-02/03 requires preserving both template changes and
regenerating ARM with matching Bicep 0.44.1.

#### IAM-001 proposal received 2026-10-21

Draft [#19327](https://github.com/githubnext/gh-cao/pull/19327), commit
`6926c313c9c059af4a8e4f9ebb06d26dbeac0df8`, proposes provider-expiry
provenance, CAS-staged no-refresh cleanup and retention of final cleanup
ownership past inactive session TTL across Redis/PostgreSQL/memory.
Protected transition capacity and bounded cancellation-independent temporary
owners cover concurrent revocation/persistence failure. Expired session
reads/CAS still deny authority, including IAM-002 heartbeat reads.
Coordinator verified open/draft/head/base and 22-file code/test/runtime-doc
scope; central assessment records were not changed by that session.

The session reports complete affected server/operational/Redis suites with
isolated real Redis/PostgreSQL and role-boundary coverage passing under
serialized packages, race lifecycle regressions, full server build,
format/lint (zero issues), file-size/diff hygiene and workflow compile.
Focused JSON reports 48 passes, zero failures/skips. Initial concurrent
bootstrap deadlocks and socket-vs-loopback fixture failures were resolved
through isolated numeric loopback/serialization, not weakened assertions.

Rollout requires stopping old replicas for new Redis envelopes while
retaining stable prefixes/protected data/current and previous keys.
Legacy queued records remain unknown-expiry debt. Already deleted session/
provider-grant owners require separately authorized reconciliation.
Forced process kill between provider issuance and durable persistence remains
an explicit issuance window; opted-in memory still loses debt on restart.
No production/provider/deployment operation or grant reconciliation occurred.
Decision **remediate**, lifecycle **in-progress**; neither operational
revocation effectiveness nor original finding is closed by this draft.

#### EXEC-01 proposal and current review snapshot

Draft [#19328](https://github.com/githubnext/gh-cao/pull/19328), commit
`6ecfccfe666e607887e20c9b94333fb234997e94`, persists OAuth numeric
`ActorID` through PostgreSQL/SQLite command JSON and idempotency/claim paths.
Prefetch/execution require the same live permission response to identify that
account before minting/consuming work tokens. Display login remains
attribution. Unknown/changed identity and revoked/unavailable permission
deny; legacy records remain readable but fail before tokens/subprocess,
settle normally and require explicit fresh admission to retry. No identity
backfill, original-record rewrite or pending-work deletion is performed.

Coordinator verified open/draft/head/base and 21-file scope. The session
reports complete affected serial Go suites, build/vet/lint, focused race,
real isolated PostgreSQL/SQLite operational cases, unchanged-generated
29-test TypeSpec check and workflow compile passing. TLC enabled/disabled/
execution models passed (651,418 / 332 / 896 states); these bounded models
are not proof of live provider behavior. An initial concurrent native
termination fixture failure passed in the complete serial rerun. Temporary
database/tools were removed. Evidence resides under
`/Users/peli/.copilot/session-state/1b133541-ad96-4c4e-9944-4497920f71e0/files/`.

No SQL schema/namespace migration is needed for opaque invocation JSON;
the hosted runtime contract becomes 1.1.0. Rollout must pause admissions,
stop/reconcile old executors, upgrade web/executor replicas together and
explicitly review/re-admit intended legacy work. Mixed old executors ignore
IDs and are unsafe. Provider rename/reassignment behavior remains unobserved,
not an exploit claim. Decision **remediate**, lifecycle **in-progress**.

A subsequent GitHub snapshot confirms all eleven finding-specific sessions
have now delivered PRs. Three corrections (OPS-04, DATA-08, IAM-002) and the
daily worker are merged into this assessment branch; eight fix PRs remain
open. DATA-01/02/03 and OPS-01/02 are now non-draft; IAM-001, EXEC-01 and
OPS-03 remain draft. The coordinator preserves externally changed review
state. IAM-002 merged at 15:52:18 UTC as `24db44b03`, not by coordinator
action; no main/control-branch activation or deployed effectiveness is implied.

While the coordinator published the preceding snapshot, DATA-03 was externally
merged as `c5625fcf5`. The coordinator fetched and preserved that remote code
and rebased only its unpublished documentation commit. DATA-03 is therefore
now integrated into the assessment branch, not a current open proposal;
combined DATA-01/02 integration and required final checks remain pending.
The earlier snapshot's counts are historical, not a claim of present PR state.

#### DATA-01 integrated-base update and subsequent remote integrations

DATA-01 [#19325](https://github.com/githubnext/gh-cao/pull/19325) now has
head `dfdd11f3cb8dad7fce79e43602446ccce7ba8a6b`, incorporating assessment
base `a643b5f17`. Its single `processErasure` call retains cancellation-aware
`ForgetContext` and DATA-03 durable intent-before-ACK/generation behavior.
Preflight additionally validates explicit normalized repository IDs, owner/name
and repositoryId references, allowing legitimate normalized replay shards while
rejecting conflicting identities. The session reports 271 collector tests
with real isolated Redis/PostgreSQL under race detection, zero skips/failures,
plus lint/types/build and 73-workflow compile. Coordinator verified current
head/open/non-draft and **in-progress CI**, not a completed CI gate.
Deployment collision inventory/legacy repair remains unresolved.

A GitHub refresh reports OPS-02 merged at 15:59:15 UTC as `a643b5f17`,
and IAM-001 at 15:59:48 UTC as `775d5ee7e`. The coordinator fast-forwarded
those externally merged changes without initiating merges or overriding work.
EXEC-01 is now open/non-draft; earlier draft observations remain history.
Six of eleven code corrections and the daily worker are now integrated into
the assessment branch; five code PRs remain open (DATA-01/02, EXEC-01,
OPS-01/03). Default/control-branch deployment and operational closure remain
pending. IAM-001 coordinated-replica/key/debt reconciliation and OPS-02 real
collector/role/alert evidence are still required despite source integration.

#### OPS-04 remediation proposal received 2026-10-21

Draft [#19304](https://github.com/githubnext/gh-cao/pull/19304), commit
`c480c3027785444a53e814802bf9776bb9f02fbc`, targets this assessment branch.
The coordinator verified its open/draft state, exact head and three-file
scope: doctor implementation/tests and server README. The finding remains
**design-gap**, decision **remediate**, lifecycle **in-progress** until
review/integration and applicable closure evidence; it is not deployed.

The proposal warns on missing/malformed persistence observations, preserves
unknown AOF enablement instead of coercing it to disabled, and no longer treats
RDB-only historical success as proof of active snapshot policy. Explicit
enabled-AOF successful writes pass only as a limited observation, not proof
of durability/recoverability. Known write errors still warn.

The remediation session reports passing formatting, doctor normal/race
tests, vet/build, full server golangci-lint (zero issues), workflow compilation
and diff checks. Added tables cover missing/malformed/partial/RDB/AOF/
combined/provider omissions. These are reported local results, not a new
independent provider assessment or historical operating evidence.

No CONFIG access, live Redis changes or merge occurred. Provider persistence
policy, backup retention, accepted-work RPO/RTO and actual restore operation
remain evidence requests under DATA-07/OPS-06 and are not closed by this PR.

#### DATA-08 remediation proposal received 2026-10-21

Draft [#19305](https://github.com/githubnext/gh-cao/pull/19305), commit
`d2d640bd3301617d27919e6634ef63c2b50f321f`, targets this assessment branch.
The coordinator verified open/draft/head/base identity and that the only
changed file is `server/internal/query/sql_temporal_test.go`.

The remediation session traced the failure to stale fixture wiring:
[#19293](https://github.com/githubnext/gh-cao/pull/19293) intentionally
retired legacy Operational Value pages and moved
`operational-value-plot-series` to
`dashboard/site/test/fixtures/native-measure-history-query.json`.
The SQL test still searched `campaign-insights.json`. The proposal names
the test `TestTemporalSQLNativeMeasureHistoryQuery`, loads the shared
fixture, preserves panel/link/order assertions and adds canonical source/
native metric identity, point metadata, dimension-isolation and 100,000-row
SQL bound checks. It does not alter production queries or infer a runtime
processing defect.

The session reports reproducing the original failure, then passing the
renamed targeted check and full query suite against isolated PostgreSQL
18.6 with both `POSTGRES_URL` and `TEMPORAL_TEST_POSTGRES_URL`: 214 passing
test/subtest events, zero failures/skips. Related dashboard contract
selectors passed 74 tests; query vet/build/lint, formatting, workflow
compilation and diff checks passed. Temporary DB resources were removed;
no deployed service/data was examined. Evidence:
`/Users/peli/.copilot/session-state/ca0f22b3-e8a7-4244-bf2c-35c0e1027c3d/files/data08-query-tests.json`.

Decision remains **remediate**, lifecycle **in-progress**, evidence status
**evidence-needed** at the original baseline until reviewed integration.
The historical failed gate is preserved, not rewritten as a pass.
Post-integration relevant-gate evidence is still required for closure;
other workstreams' skipped database/operating evidence remains unverified.

### DATA report received 2026-10-21

Assessed exact source `e3daa5d7ad4fa537e6432359076b44f4a90be171`, runtime
`ef7d9fb65ea952826f9d81a94bd5890d3cfd5634`. Detailed report:
`/Users/peli/.copilot/session-state/3665e605-4134-497a-b22a-08335594f363/files/soc2-data-assessment.md`.
All records are lifecycle **open**, decision **pending**, operating
effectiveness **not-assessed**. Confidence is **high** in observations and
evidence absence; DATA-10 operational impact confidence is **medium**.
No production loss, deployed collision or actual crash-loss was established.

| ID / status / severity | Profile, expected/observed property and working mapping | Source evidence at assessed SHA | Counter-evidence / limits |
| --- | --- | --- | --- |
| DATA-01 / design-gap / high | Collector lake: repository identity must remain distinct during acquisition/erasure. Flattening slash to hyphen makes valid `octo-a/b` and `octo/a-b` share `octo-a-b-logs-`; Forget of one deletes the other's matching files. CC6.1; PI1.3/A1.2 candidates | `server/internal/collect/lake.go:74-78,125-145,160-169`; `server/internal/collect/runner.go:249-276` | Production-helper observation reproduced cross-repository file deletion using synthetic files. Volatile startup detects ambiguity (`server/internal/collect/volatile_recovery.go:89-91,146-172`), but ordinary durable collection/Forget does not use that check. Enrollment ownership/leases do not make encoding injective. |
| DATA-02 / design-gap / medium | Collector withdrawal: shared `operational-values.jsonl` retains withdrawn repository bytes; Forget removes only prefixed shards, while retained merge has no withdrawal filter and zero-enrollment refresh returns early. CC6.5; C1.2/P4/P6 candidates | `server/internal/collect/lake.go:125-145`; `server/internal/collect/projector.go:273-279,302-330`; `server/internal/operationalvalue/collector.go:341-357,845-897`; `specs/server-ingestion.md:362-388` | Synthetic production-helper test confirms retained disk bytes, NOT canonical reappearance. Prefixed shards are removed and durable erasure admission exists. Explicit independently published packets require DATA-06's separate scope decision, not indiscriminate deletion. |
| DATA-03 / design-gap / medium | Collector erasure ACK precedes durable projection intent; crash/cancellation/state-write failure in the gap can leave old canonical rows until later replay/activity. CC7.2; PI1.3/C1.2 candidates | `server/internal/collect/worker.go:153-165,242-271,312-347`; `server/internal/collect/queue.go:320-327`; `server/internal/collect/projector.go:122-131`; `specs/server-ingestion.md:386-389` | Production worker-helper observation leaves queue empty and projection marker false; no real crash injected. Restart backfill/future activity can repair, and scope refresh persists intent before ACK. This is not permanent loss or a backend replacement-atomicity defect. |
| DATA-04 / evidence-needed / high conditional authority priority | Hosted PostgreSQL/Redis: deployed owner/runtime/evidence/retention roles, inherited/PUBLIC/default privileges and principal isolation remain unknown. CC6.1-CC6.3 | `server/postgres/operational-grants.sql:1-45`; `server/postgres/canonical-grants.sql:1-28`; `server/POSTGRES.md:70-176`; `server/internal/operational/postgres/store.go:154-177`; `server/internal/postgresx/provisioned.go:15-31` | Granular fresh-role grants, guards/fixed functions and explicit provisioning support design; startup is not a scrubber for all inherited privileges. Real-principal tests skipped. Namespaces/SQLite files do not establish hostile-principal isolation. |
| DATA-05 / evidence-needed / medium | Persistent databases/lake/snapshots: encryption at rest, key custody, credential rotation/recovery and transport exceptions need actual configuration/period evidence. CC6.1, CC6.7; C1.1 candidate | `server/internal/postgresconn/config.go:29-60`; `server/internal/redisx/client.go:112-151`; `server/internal/server/oauth.go:1313-1369`; `server/internal/operational/sqlite/store.go:82-107`; `server/internal/collect/lake.go:200-232` | Verified remote DB TLS and encrypted session/revocation records support design. File permissions/WAL are not disk encryption and canonical data is not globally application-encrypted. Coordinate IAM-006/OPS-07. |
| DATA-06 / evidence-needed / medium | Governed retention, derived copies, independent packets, legal holds and deployed maintenance/deletion schedules are unknown. CC6.5; C1.2/P3-P8 candidates | `server/internal/postgresx/partitions.go:19-22,33-100,213-288`; `server/internal/operationalvalue/collector.go:30-36`; `server/internal/operational/postgres/schema.sql:158-182`; `server/internal/postgresx/value_preservation.go:13-74` | Default run/linked/value/audit lifetimes differ (30/7/365/400 days); daily canonical partitions and bounded audit oldest-prefix retention need schedule/backlog review. This does not assert any chosen duration violates commitments. DATA-02 is the separate demonstrated withdrawal gap. |
| DATA-07 / evidence-needed / medium | Provider persistence/replication/eviction and filesystem power-loss semantics have not established accepted-work RPO/RTO. CC7.3-CC7.5, CC9.2; A1.2/A1.3 candidates | `server/internal/operational/postgres/store.go:365-380`; `server/internal/redisx/stream.go:28-85,145-210`; `server/internal/collect/lake.go:200-232`; `server/internal/operationalvalue/collector.go:923-952` | Atomic rename/publication is not unconditional power-loss durability: lake helper does not sync parent directory; value writer lacks file sync. Replay/incomplete-recovery handling exists. No crash-loss finding; OPS-06 owns overall backup/restore commitments. |
| DATA-08 / evidence-needed / medium validation priority | Existing `TestTemporalSQLDeployedCampaignQuery` fails: required `operational-value-plot-series` fragment query not found. CC8.1; PI1.2 candidate | `server/internal/query/sql_temporal_test.go:182-220` | Scoped package command exit 1 with this sole failed test event; no DB needed. Could be stale test or declarative drift; no runtime processing defect inferred. Independent local evaluator passes do not resolve the failed SQL/campaign contract. |
| DATA-09 / not-applicable-proposed / informational | Local read-only SQLite, local operational SQLite and acknowledged hosted memory need explicit applicability decisions for production durability/principal-isolation commitments. CC6.3; A1.2/C1.1 candidates | `server/internal/server/local_sqlite.go:45-52`; `server/internal/sqlitex/store.go:58-73`; `server/internal/operational/sqlite/store.go:1-5,82-107`; `server/internal/server/operational_factory.go:85-102` | Local/volatile guards preserve identity controls but do not grant durable availability. Local confidentiality/privacy is not automatically excluded. No exclusion approved. |
| DATA-10 / evidence-needed / low | Physical SQL/temp/process and metadata-source ceilings need bounded-work measurements, not only logical row/operation limits. CC7.1; A1.1 candidate | `server/internal/postgresx/sql_plan.go:104-172`; `server/internal/postgresx/store.go:317-339`; `server/internal/repositorymemory/memory.go:112-134,159-174`; `server/internal/marketplace/resolve.go:78-91` | Query timeout/structural limits and trusted-source/HTTP bounds reduce exposure. Manifest reads/JSON helper lack some pre-materialization byte guards; actual resource impact or arbitrary attacker source control not demonstrated. Fleet stress not run. |

#### DATA operator choices and closure evidence

| Finding | Proposed choice / tradeoff | Suggested owner | Evidence required for closure |
| --- | --- | --- | --- |
| DATA-01 | Prioritize sanitized collision inventory and authorize collision-safe encoding/migration or fail-closed admission; preserve legacy evidence. | Collector/data + platform | Valid colliding-coordinate production-helper regressions, legacy cache/replay migration, unrelated-evidence preservation and sanitized rollout verification |
| DATA-02 | Define and authorize repository-scoped shared/derived evidence deletion; separately decide independent packet/hold authority. | Data + privacy/compliance | End-to-end withdrawal across shared shards/canonical/cache/applicable packets, zero-enrollment, unrelated rows and failure/retry trace |
| DATA-03 | Persist follow-up before ACK and review restart-safe marker/epoch clearing; safe repeat projection trades load for recoverability. | Collector reliability | Marker-write/cancel/crash failure injection through canonical commit, preserving concurrent intent; review `server/internal/collect/projector.go:174-195` |
| DATA-04 | Collect effective grants and authorize disposable real-principal checks. | DBA/platform/security | Sanitized ownership/membership/table/column/default/PUBLIC ACL/search-path review, absent owner credentials in runtime, distinct principal boundaries and real role tests |
| DATA-05 | Collect provider/disk/KMS/TLS/key lifecycle evidence; choose encryption design against commitments, not blanket field encryption. | Platform/security + IAM/OPS | Sanitized encryption/credential scope/rotation/retirement/recovery references and explicit transport exceptions |
| DATA-06 | Approve per-copy retention, partition overshoot, erasure SLA and holds; retain independent evidence only under agreed authority. | Data/privacy + DBA | Approved inventory/lifetimes, actual maintenance/backlog/hold/alert records, end-to-end disposal and backup-expiration references |
| DATA-07 | Define accepted-work RPO/RTO and assess selected volume/provider durability. | SRE/platform + data/OPS | Effective WAL/replication/non-eviction/volume settings, real recovery/replay tests preserving queues/revocations/audit and residual replay window |
| DATA-08 | Investigate contract/test drift in an authorized follow-up; do not weaken/skip assertion to green the gate. | Dashboard/query | Reviewed cause/disposition, targeted failing test and clean relevant SQL/fixture gate |
| DATA-09 | Approve narrow profile exclusion or prohibit weak profiles for specific commitments. | Service owner + auditor | Actual enabled-profile inventory and explicit applicability approval; classified local data remains accounted for |
| DATA-10 | Measure effective DB/process/source ceilings and authorize tighter bounds only if required. | Query/data + SRE | Work/temp/concurrency/source-publication bounds, targeted worst-case measurements and alert evidence; assess legitimate-workload rejection tradeoff |

#### DATA verification and remaining coverage

The main scoped package command exited **1**, reporting 1,055 passed,
220 skipped and one failed test/subtest events: DATA-08. It covered
postgresconn/sqlbuilder/query/sqlitex, memory/Redis/PostgreSQL stores,
ingest/collect, registry/repository memory/value services and model/store
contracts. Additional commands passed: selected boundary cases (66 pass,
20 skip events), operational/SQLite conformance (118 pass, 21 skip),
`go -C internal/dashboardquery test -json ./...` (1,121 pass), and synthetic
snapshot/CLI download checks (76 pass).

Three artifact-only `go -C server test -overlay ... -json ./internal/collect
-run '^TestDATAObserve' -count=1` observations passed by reproducing current
DATA-01/02/03 adverse behavior, not verifying fixes. Counts overlap and include
subtests; they MUST NOT be summed into unique controls or a compliance rate.
Real PostgreSQL/Redis role, retention, recovery, transaction/parity and
connection-loss paths remain skipped/unverified; no live services were used.
The initial local Go 1.27.1 prerequisite failed, then automatic required
toolchain selection allowed execution. No runtime/repository changes made.
Full exact commands, overlay sources, logs and skip breakdown are in the
report artifact directory.

Source hashes authenticate content relative to their manifest, not publisher
identity. Operator-selected server snapshots can use HTTP and lack the native
CLI's independent manifest pin; trusted HTTPS or independently verified local
input remains an explicit operator boundary. Replay cannot reconstruct
unretained external events or lost revocation/independent-publication state.

### IAM and EXEC reports received 2026-10-21

Both reports assessed exact source
`e3daa5d7ad4fa537e6432359076b44f4a90be171` and unchanged runtime
`ef7d9fb65ea952826f9d81a94bd5890d3cfd5634`. Detailed reports:

- IAM: `/Users/peli/.copilot/session-state/0cab34cf-451f-469b-88d8-6c3727a86702/files/soc2-iam-readiness.md`.
- EXEC: `/Users/peli/.copilot/session-state/295a6967-1963-4ef9-80a1-ec87bee9f18b/files/exec-soc2-readiness.md`.

All findings below have lifecycle **open**, operator decision **pending** and
operating effectiveness **not-assessed**. Confidence is **high** in the stated
source observation/evidence absence; EXEC-01's conditional provider-lifecycle
impact has **medium** confidence. No live exploit, incident, production
configuration or provider lifecycle behavior was established.

| ID / status / severity | Profile, expected/observed property and working mapping | Source evidence at assessed SHA | Counter-evidence / limits |
| --- | --- | --- | --- |
| IAM-001 / design-gap / medium | OAuth responses without expiry metadata: callback assigns local deadlines; no-refresh near-expiry deletes session without staging credential cleanup, and queued cleanup treats assigned deadlines as provider expiry. Session lifetime is not actual credential expiry. CC6.2, CC6.3, CC7.4 | `server/internal/server/oauth.go:483-495,744-752,1136-1147,1175-1181` | CAO access fails closed; cookies clear; normal logout/CAS/durable retry design is supported. Concern is abandoned still-valid provider credentials, not restored CAO authority or disclosed tokens. Provider grants/scopes remain unknown. |
| IAM-002 / design-gap / low | OAuth SSE streams retain connection-time authorization after revocation/admin assurance expiry; no application-level stream revalidation or maximum age. CC6.2, CC6.3 | `server/internal/server/server.go:1011-1044,1258-1320,1336-1341,1892-1906`; `server/internal/server/oauth.go:690-742` | Only revision/healthRevision integers continue, not rows, credentials or execution authority. New HTTP requests fail; drain/cancel closes streams. Proxy lifetime may mitigate but was not verified. |
| IAM-003 / evidence-needed / high evidence priority | Effective audience, administrator scope and identity lifecycle are unknown. Explicit AllowedUsers authorizes OR org/team, intentionally; dataset-wide entitlement needs approval. CC6.1-CC6.3; CC1-CC5/CC9.2 dependencies | `server/internal/server/oauth.go:970-990`; `server/internal/server/hosted.go:176-189`; `server/internal/server/azure.go:323-337`; `server/internal/server/server.go:1065-1100` | Explicit-user/member-removal/assurance/OIDC scenarios pass. Source cannot establish real access reviews/offboarding or that org/team-only shorthand describes effective configuration. No accidental bypass asserted. |
| IAM-004 / evidence-needed / medium | Privileged OAuth assurance is recent authorization plus 2FA enrollment/attested SSO, not evidence of a fresh second-factor ceremony. CC6.3 | `server/internal/server/oauth.go:400-403,525-529,614-651`; `specs/server-hosted-cli.md:111-136` | Bounded assurance age/admin allowlist/no-refresh extension are enforced. This matches the existing assurance specification; a stronger company MFA claim needs provider evidence, not a fabricated implementation defect. |
| IAM-005 / evidence-needed / high evidence priority | Trusted proxy/platform forwarding and custom-handler isolation need deployed proof. Azure intentionally relies on platform forwarding rather than app CIDRs. CC6.6, CC6.7, CC7.1, CC9.2 | `server/internal/server/hosted.go:198-208,281-318`; `server/internal/server/azure.go:108-153,171-185,318-322,375-389,414-425`; `server/cmd/cao-dashboard/azure_functions.go` | Loopback defaults, host-neutral trusted peers/TLS and development gates exist. No exposed backend or forged-header acceptance was demonstrated. Coordinate with OPS-07. |
| IAM-006 / evidence-needed / medium | Key custody/rotation, durable revocation and real-adapter failure/restart guarantees lack operating evidence; volatile exceptions need explicit disposition. CC6.2, CC6.7, CC7.4, CC9.1; A1/C1 candidates | `server/internal/server/oauth.go:262-277,1116-1181,1319-1382`; `specs/server-operational-storage.md:181-210`; `server/internal/testutil/postgres.go:19-28` | AES-GCM, current/previous key support, CAS and capability validation support design. Memory intentionally loses retries on restart. Durable storage does not fix IAM-001's independent application lifecycle defect. |
| EXEC-01 / design-gap / medium | Hosted CLI queues preserve mutable actor login, not numeric requester identity; delayed permission recheck cannot prove it addresses the original account after rename/reassignment. CC6.2, CC6.3; PI1 candidate | `server/internal/operational/cli_commands.go:95-106`; `server/internal/server/hosted_cli.go:250-255`; `server/internal/hostedcli/executor.go:303-313,405-417`; `server/internal/githubapp/scoped_token.go:211-230` | Conditional stronger identity-continuity objective, not a wire-spec violation or proven GitHub exploit. Admission step-up, live writer permission and single-repository limits remain; callback/consent bind numeric identity and active renames deny subsequent session use, but not already queued work. |
| EXEC-02 / evidence-needed / high if production CLI enabled | Real Linux rootless Podman isolation/egress has not been validated on the intended executor. CC6.6, CC6.7, CC7.1 | `specs/server-hosted-cli.md:440-608`; `server/internal/server/hosted_cli_runtime.go:30-75`; `server/internal/hostedcli/oci_container.go:55-127`; `server/internal/hostedcli/container_egress.go:37-155`; `server/internal/hostedcli/podman_isolation.go` | Strict startup/network-none/read-only/capability/relay/digest/effective-isolation controls have mock coverage; `TestContainerEgressRealBoundary` (`CAO_TEST_PODMAN_EGRESS=1`) supplies the real-boundary evidence when run on the executor. CONNECT restricts destinations, not content/exfiltration to allowed hosts. Real kernel boundary deliberately not run on this macOS assessment. |
| EXEC-03 / evidence-needed / medium | Admission is a cooperative start check: protected accurate accounting publisher and effective client/outage configuration are required, not an unavoidable financial cap. CC6.3, CC7.2; PI1 candidate | `server/internal/admission/policy.go:140-203`; `server/internal/server/activation_admission.go:47-58`; `server/ADMISSION.md:3-21,182-210,266-278` | Exact identity/enrollment/freshness fail closed; conclusion deliberately admits completed-run evidence independently of budget freshness. Upstream client interoperability fixtures were absent. |
| EXEC-04 / evidence-needed / medium | Durable replay receipts, fences, cleanup journals and recovery need selected real-backend/namespace/capacity evidence. CC6.5, CC7.4, CC7.5; A1/PI1 candidates | `server/internal/operational/postgres/activation_admission.go:40-77`; `server/internal/operational/postgres/conclusion_admission.go:27-95`; `server/internal/hostedcli/broker.go:394-484,491-600`; `server/internal/hostedcli/recovery.go:10-37` | Synthetic/SQLite restart/fault cases support design, not PostgreSQL durability. Permanent receipts intentionally trade capacity for replay protection; deleting them is not an authorized recovery. DATA/OPS recovery evidence remains a dependency. |
| EXEC-05 / evidence-needed / high if privileged features enabled | Gateway/CLI App keys, process identities, secret mounts and installation reach require actual separation/access evidence. CC6.1-CC6.3, CC6.5, CC9.2 | `server/cmd/cao-webhook-gateway/main.go:175-202`; `server/internal/githubapp/webhook_gateway_role.go:14-64`; `server/internal/githubapp/scoped_token.go:126-192,318-353`; `server/internal/server/hosted_cli_env.go:85-139` | Role-variable rejection, dedicated keys and repository-scoped tokens support design. App-key/host compromise has broader reach. Startup-loaded keys require coordinated restart; gateway tokens must not be described as hosted-CLI single-use cleanup tokens. |
| EXEC-06 / evidence-needed / medium | Authorization observation, local reservation and remote acceptance are not atomic; accepted sends cannot be recalled and ambiguity is terminal for automatic replay. CC6.3, CC7.4, CC8.1; PI1 candidate | `server/internal/webhookgatewayhost/authority.go:104-176`; `server/internal/webhookgateway/worker.go:128-249`; `specs/rfc-033-webhook-gateway.md:764-808`; `specs/server-hosted-cli.md:300-322` | Fresh worker checks/fences/ambiguity handling bound risk. CLI step-up is checked at admission, not each execution. Do not promise immediate remote revocation or exactly-once workflow execution. |
| EXEC-07 / evidence-needed / medium for enabled native development | Apple/Windows SQLite development runners need actual platform ownership, workspace and termination evidence or explicit exclusion. CC6.1, CC6.6, CC7.4, CC7.5 | `server/internal/hostedcli/apple_container.go:34-98`; `server/internal/hostedcli/apple_execution.go:109-183`; `server/internal/hostedcli/windows_sandbox.go:148-234,273-356,389-496`; `specs/server-hosted-cli.md:400-467` | Fake-command/lifecycle tests pass; explicit platform/SQLite gating exists. Native runners permit network access and are not production Podman's restricted-egress boundary. Real Apple/Windows behavior unverified. |
| EXEC-08 / evidence-needed / low scoping priority, medium if confidential outputs unmanaged | Output/evidence classification, retention and scope require approval; local simulation/quota cannot establish real dispatch or distributed authority. CC7.2, CC8.1, CC9; A1/PI1/C1/P applicability | `server/internal/hostedcli/output.go:14-127`; `server/ADMISSION.md:141-168`; `server/internal/webhookgatewaylocal/simulation.go:128-133`; `server/cmd/cao-cli-quota/main.go:414-441` | Known-token redaction is not universal PII/content redaction; protected receipts intentionally outlive compact payloads. Local dispatcher is explicitly synthetic and local quota uses memory. Coordinate DATA/OPS evidence, not duplicate independent-audit findings. |

#### IAM and EXEC operator choices and closure evidence

| Finding | Proposed choice / tradeoff | Suggested owner | Evidence required for closure |
| --- | --- | --- | --- |
| IAM-001 | Separate session lifetime from provider expiry and preserve unknown-expiry cleanup; reconcile abandoned grants. Retained cleanup consumes protected capacity and may need escalation. | Identity/security + DATA | No-refresh/queued unknown-expiry regressions, provider-confirmed expiry cases, durable restart/outage/key/capacity tests and sanitized outstanding-grant reconciliation |
| IAM-002 | Bound/revalidate/reconnect stream authority within approved revocation interval, or explicitly accept metadata-only residual risk. Checks/reconnects add load. | HTTP/identity | Logout/member-loss/admin-expiry/outage stream tests showing updates stop/narrow, plus deployed proxy lifetime policy |
| IAM-003 | Approve exact audience and collect access-lifecycle evidence; any narrowing is a separate operator policy decision. | IAM/compliance | Effective deployed revision/config, dataset/role entitlement approvals and sanitized access-review/offboarding/exception samples |
| IAM-004 | Approve accurate recent-authorization assurance wording or require stronger provider-side ceremony. | Identity architect + compliance | Approved assurance policy/age, provider SSO/session enforcement and bounded period evidence |
| IAM-005 | Collect authorized topology/header/isolation evidence; request stronger gates only if separately chosen. | Platform/network + IAM/OPS | Reviewed bind/ingress/final-hop TLS/overwrite policy, no public development modes, authorized untrusted-path rejection tests |
| IAM-006 | Collect real-adapter/key-drain/cleanup operation evidence and decide scoped volatile exceptions. | Secrets/platform + DATA/IAM | Key rotation/retirement runbook, restart/restore/failure tests, queue drain/alert records and approved exceptions |
| EXEC-01 | Investigate numeric actor continuity and provider lifecycle assumptions; changing queue contract requires legacy-record handling. | Execution maintainer + IAM | Account lifecycle/queue-age policy, reviewed immutable-ID contract, changed-ID/same-login and renamed-original fixtures, deployment disposition of old pending commands |
| EXEC-02 | Collect intended-executor evidence; do not infer enablement authorization. | Execution platform + release/OPS | Real Linux boundary/allowed-traffic results, OS/daemon/admin boundary, reviewed egress policy and relay/image/source digests |
| EXEC-03 | Confirm cooperative-control commitments and complementary budget controls. | Admission/accounting owner | Protected atomic publisher/client configuration, freshness/failed-publication alerts, pinned interoperability and outage policy |
| EXEC-04 | Collect DATA-owned real backend/recovery proof; preserve fail-closed ambiguity and receipts. | DATA/platform + execution | PostgreSQL/Redis fault/restart tests, grants/namespaces/capacity, old-process termination and restore/replay reconciliation |
| EXEC-05 | Review actual secret/App/host reach and key rotation/offboarding. | Identity/secrets + execution | Sanitized process/mount identity inventory, App scopes/access approvals, daemon controls, pause/drain/two-key restart evidence |
| EXEC-06 | Approve accurate authorization/race contract or request separately reviewed hardening. | Service/security + control operator | Offboarding/cancel runbook, residual-race decision, ambiguous-send/label/downstream reconciliation exercise |
| EXEC-07 | Collect native-platform tests or propose exclusion; strict VM ownership can block shared-workstation use. | Workstation/runtime + compliance | Intended-platform workspace/DACL/VM ownership/termination tests, or operator/auditor-approved scope exclusion |
| EXEC-08 | Confirm classifications and local/synthetic boundaries without deleting protected replay/evidence blindly. | Service/compliance + DATA/OPS | Approved inventory, retention/deletion/backup and independent evidence references; explicit exclusions if warranted |

#### IAM and EXEC verification and deduplication

IAM used existing focused Go tests plus three artifact-only Go overlay
observations reproducing IAM-001's two cleanup paths and IAM-002's
post-revocation stream delivery. The overlay tests passed both ordinary and
`-race` runs: they confirm adverse current behavior, **not fixes**. No
repository tests or runtime files were changed. Main IAM selectors passed
127 and 51 top-level tests respectively, with seven and eight top-level
skips; selectors overlap and MUST NOT be summed as unique coverage.
Four local SQLite and two signature/admin-route tests also passed.
Sixteen PostgreSQL-dependent test/subtest events were skipped. Full selectors,
overlay source, commands and JSON logs remain alongside the report.

EXEC's two bounded `go -C server test -json -count=1 -timeout=180s`
invocations passed 486 top-level tests across 13 package results, zero failed
events and 39 skipped test/subtest events. Package tests cover admission,
hosted CLI, gateway/host/local, App/scoped tokens, egress/quota and selected
server/CLI handlers. Selectors explicitly excluded real Linux container-engine egress (then Docker),
real Apple containers and fleet tests. Windows platform cases, real
PostgreSQL/Redis and pinned upstream compiled-runtime interoperability remain
unverified. Parent tests with skipped children do not verify those children.

IAM and EXEC ran on macOS without live provider, production, service setup or
dependency installation. Runtime/adapter/deployment effectiveness and
historical operation remain unassessed. EXEC-01 exclusively owns the mutable
login handoff: IAM confirms callback/consent numeric binding and active rename
denial (`server/internal/server/oauth.go:333-346,525-540,706-710,785-787`)
but neither binds queued work to the original ID. IAM-006/EXEC-04 depend on
DATA adapter evidence; IAM-005/EXEC-05 overlap OPS deployment evidence and
are linked requests rather than duplicate confirmed defects.

### OPS report received 2026-10-21

Source assessed: `e3daa5d7ad4fa537e6432359076b44f4a90be171`; runtime
`ef7d9fb65ea952826f9d81a94bd5890d3cfd5634`. The report's source matches
the investigation baseline; subsequent coordinator changes are assessment
documentation only. Detailed report:
`/Users/peli/.copilot/session-state/76a730cd-672d-4c67-9e03-cb2c3c0460fe/files/ops-soc2-readiness.md`.
The durable findings and limitations are recorded below; the session artifact
contains supporting control matrices, full commands and output references.

All eight findings have lifecycle **open** and decision **pending**.
Confidence is **high** in the stated source observation or evidence absence,
not in any claim that a production deployment is affected. Operating
effectiveness is **not-assessed**. Severity is a readiness prioritization,
not a vulnerability/exploitability rating. Criteria mappings are provisional.

| ID / status / severity | Profile, working mapping and observed property | Source evidence at assessed SHA | Counter-evidence / limits |
| --- | --- | --- | --- |
| OPS-01 / design-gap / medium | Administrative rebuild: successful changes lack durable actor-attributable lifecycle history; the replaceable rebuild status has no actor/request ID, and terminal persistence errors are discarded. CC6.3, CC7.2, CC7.3 | `server/internal/server/operations.go:56-136,352-357`; `server/internal/server/server.go:1103-1112` | Admin/step-up/CSRF gating, projection lease, atomic publication and denied-admin audit remain. This is an evidence-coverage objective, not an authorization bypass or a claim every local operation must use the hosted-CLI audit table. |
| OPS-02 / design-gap / medium | Azure template-only monitoring: Application Insights settings do not activate Go OTLP exports without the separate endpoint/collector and log opt-in. CC7.2, CC7.3 | `server/azure/main.bicep:466-469`; `server/azure/collector.bicep:282-300`; `server/internal/telemetry/telemetry.go:82-108`; `server/README.md:3173-3177` | Platform/container logs, stderr audit mirrors and independent delivery may provide alternative monitoring. A tenant overlay may already supply OTLP; none was provided. Optional instrumentation is not evidence that all monitoring is absent. |
| OPS-03 / design-gap / medium | Production use of Azure development source-build without an external gate: ACR build/apply lacks the official candidate image scan/SBOM/attestation step for those rebuilt targets. CC7.1, CC8.1 | `internal/cli/deploy_azure_images.go:251-309`; `internal/cli/deploy_azure_apply.go:130-149,186-208`; `.github/workflows/cao-package.yml:427-527` | Explicitly a development path; digest-pinned bases, reviewed context, digest receipts, locked tags and plan checks exist. An independent pipeline could supply the missing gate. Published-image guarantees do not cover later rebuilds. |
| OPS-04 / design-gap / medium | Redis doctor: empty RDB status with AOF disabled is labelled healthy writes instead of insufficient evidence. CC7.1, CC7.2; A1 candidate | `server/internal/doctor/checks_redis.go:375-430`; `server/internal/doctor/checks_redis_test.go:310-314` | Known write errors warn and other diagnostics expose persistence details. The existing passing test confirms this diagnostic behavior, not disabled backups, lost writes or a provider SLA failure. |
| OPS-05 / evidence-needed / high if independently durable audit is relied upon | Independent destination, anchor enrollment, complete committed-row export, retention and independent heartbeat/alert operation are unestablished. CC6.3, CC7.2, CC7.3, CC9.2; C1 candidate | `server/audit-delivery/README.md:3-12,49-57,95-109,133-171,201-221`; `server/SECURITY.md:879-901` | Chain/anchor verification, read-back, immutable objects, CAS and replay fault tests support design. Real destination conformance was skipped. Storage-owned events need committed-row export rather than mirror-only collection. |
| OPS-06 / evidence-needed / high if availability/durable-work commitments apply | RTO/RPO and complete consistent restoration of protected operational state, lake evidence and audit history are unestablished. CC7.5, CC9.1; A1.2/A1.3 candidate | `server/azure/network.bicep:105-112`; `server/azure/collector.bicep:143-208`; `server/README.md:2299-2306,3306-3309` | Seven-day baseline PostgreSQL backups, rebuild/replay and drain exist; baseline HA/geo backup is disabled. These are tradeoffs, not universal SOC 2 failures. Rebuilding canonical projections does not recover all accepted work or audit/revocation state. |
| OPS-07 / evidence-needed / medium | Effective cloud privileges, secret/key rotation, network restrictions and log/evidence classification are unknown. CC6.1, CC6.3, CC6.7, CC9.2; C1/P applicability pending | `server/azure/main.bicep:299-315,433-447,527-577`; `server/azure/worker-roles.bicep:15-36`; `server/azure/function-roles.bicep:48-76`; `server/azure/collector.bicep:143-221` | Secret-scoped RBAC, managed identities, TLS and separated roles are modeled; public authenticated ingress is not itself a defect. Templates cannot establish actual grants, key retirement, lake account-key access or deployed boundaries. |
| OPS-08 / evidence-needed / medium | Production release approvals, actual digest/provenance verification, scan exception disposition, evidence retention and rollback need period evidence. CC7.1, CC7.4, CC8.1, CC9.2 | `.github/workflows/cao-package-publish.yml:199-303`; `.github/workflows/release-promote.yml:110-155`; `.github/workflows/cao-package.yml:314-323,487-498,566-579`; `.grype.yaml:1-7` | Official image scanning/sealed publication/attestation is design-supported. Trivy ignores unfixed findings and the committed zlib exception for CVE-2026-85091 has no recorded approver/expiry; current CVE/fix status was not verified. An external approval/archive may exist. Passing scans do not close exceptions. |

#### OPS operator choices and closure evidence

Suggested owners below are not assigned or authorized by the assessment.
No risk acceptance, exclusion, remediation or production action has been
approved.

| Finding | Proposed choice / tradeoff | Suggested owner | Evidence required for closure |
| --- | --- | --- | --- |
| OPS-01 | Define audited admin coverage; authorize lifecycle audit or demonstrate equivalent protected history. A restricted local exception requires explicit acceptance. | Server owner + SecOps | Actor/correlation/start/outcome fixtures, outage behavior, retained independent evidence and approved scope; account for cross-store transaction coupling without logging payloads |
| OPS-02 | Complete a reviewed collector overlay or approve an adequate alternative monitoring boundary; avoid making request availability depend on the telemetry backend without design review. | Platform observability | Sanitized effective OTLP/log settings, TLS/collector topology, end-to-end receipts, forced-failure alert acknowledgement |
| OPS-03 | Restrict the source-build route to development or gate each exact target digest before production promotion; equivalent independent assurance is acceptable if reviewed. | Release/platform security | Per-target scan policy/results/dispositions, SBOM and source/context provenance, promotion approval and deployed digest read-back; include dirty-context digest |
| OPS-04 | Authorize explicit unknown/warn semantics and separately verify provider durability; do not demand unsupported managed-service CONFIG permissions. | Server diagnostics + data platform | Missing-field regression no longer falsely passes, plus approved provider persistence and restore evidence |
| OPS-05 | Collect independent audit operating evidence and authorize any missing provisioning separately. | SecOps/evidence platform | Real adapter conformance, independent anchor provenance, WORM/role/retention/checkpoint policy, complete sequence reconciliation, dead-scheduler/backlog alert receipts and outage/retention-gap exercise |
| OPS-06 | Approve recovery commitments and an isolated restore exercise, or explicitly accept a time-bounded service limitation. | Service owner + SRE/data platform | Complete protected-state backup inventory, timed restore/data-loss measurements, queue/revocation/audit-anchor/lake reconciliation, key availability and failback report |
| OPS-07 | Collect sanitized effective settings and assess key, network, vendor and privacy boundaries. | Cloud security with IAM/DATA/privacy | Privilege/access review references, identity separation, key rotation/retirement exercise, network exceptions, lake/export authority and log/evidence classification/retention review |
| OPS-08 | Retain examination-period exact-source/digest change samples and review exceptions; target-specific rebuilt images require separate evidence. | Release/change manager | Required-check/review/environment/bypass rules, actual approvals, scanner policy/database/results, exception approver/expiry/reassessment, verified provenance/deployed digest, evidence archive and rollback drill |

#### OPS verification and remaining coverage

The investigator reported these bounded results, with exact selectors and logs
in the detailed report:

- `go -C server test ./internal/audit ./internal/auditdelivery ./internal/telemetry ./internal/logger ./internal/doctor -count=1 -json`:
  passed, 151 top-level passes; real audit destination and OpenObserve tests
  skipped, plus an intentional parent subprocess-helper skip.
- Focused server/Functions/dashboard audit and lifecycle command: passed,
  21 top-level passes and five PostgreSQL-dependent skips. Focused
  diagnostics/hosting command: passed, eight passes and two PostgreSQL skips.
- Mocked Azure CLI deployment selectors: eight passes. These did not
  authenticate to Azure, build images or verify actual deployment.
- `npm --prefix server/spec test`: passed, 29 tests, after missing locked
  dependencies were restored; `node --test tests/unit/cao-package-contract.test.mjs`:
  five passes after a missing root dependency restore;
  `node --test tests/unit/cao-azure-assets.test.mjs`: seven passes.
- API TypeSpec compile with `noEmit`: passed, zero diagnostics. No
  generated-artifact reproducibility gate or artifact regeneration was run.

Local execution used Go 1.27.3/darwin-arm64 and Node 26.9.0; Node differed
from CI's Node 24. No scanner/build/publication, real database/Redis provider,
production access, restore/failover, real alert routing or period-operation
test was run. Doctor coverage was bounded; generic logging APIs were not
proved to redact arbitrary caller fields. DATA, IAM and EXEC implementation
internals were outside OPS ownership; their reports are consolidated above.
CC1-CC5 and category applicability remain operator evidence requests.

### Overall decision gate

For every scoped control claim, use `design-supported`, `design-gap`,
`evidence-needed`, or `not-applicable-proposed`; record tests and operating
effectiveness separately. Report confirmed gaps separately from hypotheses.
Preserve citations, limitations, and compensating controls.

The final operator table must identify the finding, working TSC mapping,
severity/confidence, affected profile, proposed action, suggested owner,
closure evidence, and pending decision. The operator chooses whether to
investigate, remediate, collect evidence, approve an exclusion, or accept a
time-bounded risk. This assessment does not make those choices automatically.

All four bounded technical reports are now consolidated, including recorded
coverage exclusions, skipped checks and the failed DATA-08 validation.
Organizational/deployment/period evidence remains pending. Scoped repository
remediation is underway and the daily worker is proposed as listed above;
OPS-04 and DATA-08 technical corrections are integrated and locally checked
on this assessment branch, but default-branch/deployment closure is pending.
IAM-001/002, DATA-03 and OPS-02 are also integrated. EXEC-01, OPS-01/03 and DATA-01/02
remain separately validated proposals awaiting review, not integrated fixes.
The daily worker source is integrated
into this assessment branch but not demonstrated active on the control branch.
The conclusion is
**initial technical assessment delivered; SOC 2 readiness undetermined**.
