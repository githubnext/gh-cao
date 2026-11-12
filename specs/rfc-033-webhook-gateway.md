# RFC-033: Policy-governed webhook gateway

**Status:** experimental implementation and bounded-model validation; not deployment acceptance.
**Scope:** a shared GitHub App webhook receiver and durable dispatcher for explicitly approved workflow subscriptions.  
**Safety default:** disabled, fail closed, no inferred repository authority.

The key words MUST, MUST NOT, SHOULD, and MAY are normative requirements for a
conforming implementation. Existing CAO rollout policy and gh-aw execution
capabilities remain independent ceilings. This RFC does not enable an operation,
grant a permission, or authorize any target.

Persisted/wire state values are lowercase: `ready`, `claimed`, `dispatching`,
`succeeded`, `expired`, `rejected`, `cancelled`, `failed`, and `ambiguous`.
Capitalized state names in prose and tables denote those same values.

## 1. Problem and goals

A control repository needs to react to selected GitHub events without installing
an independently privileged event router in every target repository. The gateway
authenticates deliveries, evaluates bounded declarative subscriptions, and
requests an approved workflow dispatch in an approved control repository.
It MUST preserve source confidentiality, explicit enrollment, bounded durable
work, and explainable dispatch outcomes across replicas and crashes.

Goals are reusable event routing, compiler-declared subscriptions, immutable
identity, grants checked in both phases, and truthful uncertainty reporting.
Non-goals include general event forwarding, executing payload-supplied code,
arbitrary HTTP destinations, target writes, event-sourced dashboard queries,
exactly-once workflow execution, and immediate global revocation guarantees.

## 2. Architecture and trust boundaries

```text
GitHub/providers -- authenticated bytes --> receive -- atomic admission --> PostgreSQL
trusted revision approval ---------> subscription registry             |
                                                                      v
GitHub <-- bounded workflow_dispatch -- dispatch deployment <-- fenced claims
```

The receiver verifies, bounds, and projects ephemeral events; it does not execute workflows.
The dispatcher claims durable jobs, refreshes authorization, and makes only the
approved dispatch request. The registry separates discovered metadata from
trusted approvals. Operational storage is not canonical dashboard storage.
Neither payload repository names, workflow contents, compiler metadata, OAuth
sessions, nor credential reach alone confer dispatch authority.

## 3. Immutable identities and tenancy

Namespace keys MUST contain deployment/tenant identity and the GitHub host.
Source identity comprises App ID, installation ID, repository database ID, and,
where needed, immutable organization/account ID. Destination identity comprises
subscriber installation ID, repository ID, workflow identity, approved commit
SHA, and immutable subscription ID/revision. A delivery key also includes the
signed receiver/App identity and `X-GitHub-Delivery`.

Owner/repository names and workflow paths are display/routing locators, not
authority keys. Resolve a locator back to its approved IDs before use. A rename
MAY update a verified locator; transfer, reinstall, visibility change, or workflow
replacement MUST trigger authorization reevaluation. Repository deletion and
name reuse MUST NOT rebind an existing subscription.

The durable admission identity is `(namespace, receiver App ID, delivery ID)`;
its atomic job batch binds stable subscription IDs and exact approved revisions.
Per-job identity MUST NOT change merely because mutable policy or approval
revisions change. Revision changes do not make a duplicate delivery new work.
Deliberate administrator
redrive uses a separately audited identity and MUST NOT erase the original
terminal record.

## 4. Subscription metadata v1

The compiler-facing interchange document is a bounded JSON object with
`schemaVersion: "gh-aw.webhook-subscription/v1"`, required `provider: "github"`,
`id`, `tenantId`, `appId`,
`source`, optional `filter`, `destination`, and `delivery`. Source declares
trusted route and immutable installation/repository/organization constraints;
destination explicitly declares transport kind and bounded field mapping.
An omitted source `repositoryId` means organization-only delivery, not a
wildcard over repositories in that organization. Repository-bearing events
MUST match an explicit source repository ID at ingress, retained-job validation,
and fresh authorization; an organization-only proof MUST NOT authorize them.
The trusted artifact/approval wrapper binds compiler identity, repository ID,
source commit SHA, metadata path, and exact digest; those are not self-asserted
activation authority in the subscription.
Only the explicit GitHub provider is supported; a missing or unknown provider
MUST reject validation. `Source.ID` independently identifies the configured
trusted ingress route and MUST NOT be substituted for provider identity.
Unknown versions, properties outside the versioned schema, invalid IDs, oversized
documents, noncanonical hashes, and unsupported predicate operations fail closed.

Metadata v1 is **untrusted discovery data**, even when generated by a compiler.
It MUST NOT activate itself, widen an existing approval, or supply credentials.
Its digest MUST bind the complete normalized subscription and the compiler
version, workflow revision, dispatch mapping, and declared source/project scope.
The dispatcher MUST load the immutable approved snapshot, not mutable branch
metadata. Event content MUST NOT choose an input name, workflow, ref, destination,
subscription revision, or permission.

## 5. Trusted revision approval and lifecycle

A separately authenticated administrator or reviewed control-repository policy
approves the exact metadata digest and source commit SHA, immutable subscriber
identity, permitted source installations/repositories, visibility ceiling, event
types, input bounds, expiry, and operating mode. Approval authority MUST itself
be checked against the control repository's reviewed policy.

Lifecycle is `Discovered -> Approved -> Active -> Suspended|Removed|Superseded`.
Only a trusted approval creates Active authority. Updates create a new revision;
there is no silent approval inheritance. Removal immediately blocks local new
admissions and cancels ready/claimed work after observation. Approval expiry,
compiler trust removal, and policy narrowing behave like revocation.
Already accepted remote requests cannot be recalled by changing registry state.

The queue persists `QueueRevision { Revision: int64, Digest: string,
Enabled: bool }` separately from each exact compiler `SubscriptionDigest`;
`PolicyRevision` MAY be an alias, not a second revision domain. Jobs carry
`Job.QueueRevision`; `JobDimensions(job)` derives App, Tenant, Source,
Destination, and Workflow budget dimensions from immutable subscription identity.
Admission carries `Admission.Revision`. The
attempt-bound wrapper is `Authorization { Revision QueueRevision;
ApprovedSubscriptionDigest string; Snapshot AuthorizationSnapshot }`; the
snapshot MUST NOT carry a conflicting duplicate revision field.
`ValidateDispatchAuthorization(job, authorization, now)` is the pure validation
gate used before `Queue.BeginDispatch(ctx, fence, authorization)`.
`ApplyRevision` rejects lower revisions and equal revisions whose
digest or enabled flag differs, treats an identical tuple as a no-op, and
atomically applies higher revisions while conservatively cancelling every older
ready/claimed job. Dispatching jobs retain only fenced completion/recovery.
Admission and BeginDispatch require exact equality with the current persisted,
enabled policy; BeginDispatch also checks the job and fresh authorization agree.

## 6. Compiler dependency and workflow identity

The compiler MUST emit the versioned subscription declaration and a stable
binding to the dispatchable workflow artifact and its source revision.
[github/gh-aw#69772](https://github.com/github/gh-aw/pull/69772), inspected at
`fe4f420f3f4f40bb1ebbcca85af0903d3a967bf3`, defines a batch-v2 discovery interchange in
the compiled lock-file header. That interchange is supported for
discovery below; it is not the approved subscription schema in section 4.
An installed compiler version MUST NOT be assumed to emit it merely because
this parser accepts it. Declaration v2 and the runtime envelope below match that
upstream source. A local interoperability test executes its actual runtime;
the declaration fixture is hand-authored, not installed-compiler output.
Section 6.2 defines the private-repository security activation bridge and
section 6.2.2 independent external-source enrollment. Projects remain discovery-only.

Workflow dispatch accepts a branch or tag ref, not an arbitrary guarantee of
execution at the metadata SHA. Use a protected, policy-approved revision
locator whose resolved commit is rechecked before send; generated workflows MUST
verify their expected approved revision before privileged work. Ref mutation
between verification and GitHub processing is another external race: a ref alone
does not prove immutable execution. Where that trust cannot be established,
dispatch MUST remain disabled. Do not claim this RFC solves mutable-ref pinning.

### 6.1. Lock-header trigger mapping

The gateway MUST discover webhook declarations from the single-line
`# gh-aw-manifest: {...}` header in each explicitly selected compiled
`.lock.yml`, not by interpreting YAML jobs, `on` entries, source frontmatter,
or workflow steps. Exactly one header in the leading comment preamble is
required. Missing, duplicate, malformed, multiline, or unsupported-version
headers MUST reject the inventory without emitting a partial mapping.

Manifest `version: 1` carries `webhook_subscriptions`. Its omission or an empty
array denotes no webhook subscriptions; native triggers MUST NOT be inferred
as webhook routes. Explicit null is invalid. Each declaration uses numeric
`schema_version: 1` or `2`, `event`, `types`, `conditions`, optional `any_of`,
optional v2 security `repos` and GitHub-only `orgs` (section 6.2.1), provider
`provider`/`native_events`/`activity_normalization` metadata, and
`dispatch`. Unrelated top-level manifest fields are permitted; unknown
declaration, condition, dispatch, or context properties MUST fail closed.
Duplicate JSON keys, repeated family/identifier pairs in one workflow, conflicting workflow
identities, and unsupported predicate operations MUST be rejected.

The deterministic mapping indexes original webhook event names to all declared
workflow routes. Each entry MUST retain the complete declaration, manifest
version, and SHA-256 of the **entire** lock artifact, including steps after
the header. Workflow identity is `.github/workflows/ID.lock.yml`; the current
compiler emits `repository_dispatch` to `${{ github.repository }}` with
`event_type` equal to `gh-aw-webhook-` plus the first 24 hexadecimal characters
of SHA-256(ID). Workflow path, context paths, and transport limits MUST agree
with this versioned contract. Mapping never resolves a repository template
from an incoming payload.

Declaration v1 retains its single-event context paths for discovery only.
Batch-capable v2 MUST declare the following fixed mapping and batching policy:

```json
{
  "context": {
    "envelope": "github.event.client_payload",
    "aw_context": "github.event.client_payload.aw_context",
    "event": "github.event.client_payload.aw_context.webhook.event",
    "deliveries": "github.event.client_payload.aw_context.webhook.deliveries",
    "items": "aw_context.items"
  },
  "batch": {"mode": "same-event", "deduplicate_by": "delivery_id"}
}
```

Every v2 declaration MUST additionally declare exactly
`payload_projection: {"policy":"resource-url-types-filters/v2"}`. The complete delivery
payload requires `url` and `type`, plus `filters` when predicates or a selected
Azure lifecycle match exist. Every forwarded type MUST be established by the
original delivery and selected by the subscription; an empty intersection cannot
qualify. Severity remains an ordinary compiled condition and only its matching
literal(s) may appear under `filters.severity`.
`filters` MUST be a flat map from compiled condition names to nonempty distinct
homogeneous scalar arrays. It MUST contain only actual matching literals from
the authenticated original delivery, accepted by every applicable condition for
that name, and cover all shared conditions plus one complete matching `any_of`
alternative. It MUST NOT include unmatched array elements, sibling fields, nested
source objects, or cross-combined transition pairs. Azure selected closed/reopened
additionally requires exactly one matching configured terminal name under
`filters["closed-states"]`, as defined in §6.2.2. Without predicates or a selected
Azure lifecycle match, filters MUST be omitted except for command subscriptions'
explicit event/token or label evidence in §6.2.3. Both old `resource-url-types/v1`
and `resource-url-types-filters/v1` policies MUST remain nonactivating.
GitHub notifications carry the single original action, not a state object.
Missing or unknown policies, old `source_path`/`target_path` declarations,
URL-only security payloads, and native Projects bodies MUST reject validation,
never fall back to raw-body forwarding.
The upstream [gateway specification](https://github.com/github/gh-aw/blob/fe4f420f3f4f40bb1ebbcca85af0903d3a967bf3/docs/src/content/docs/specs/webhook-gateway-specification.md)
defines Projects, security, Jira/Linear/Azure, ten GitHub notification families and
two command families, including provider-normalized activity arrays.
This bridge activates only explicitly delegated
security/notification/command routes or independently enrolled provider routes. Projects remain
nonactivating. Unknown families MUST fail the entire artifact closed. No URL or projection
policy supplies enrollment, permissions, or authority.
Mixed versions, paths, modes, or unsupported projections MUST reject validation.

Custom/arbitrary webhook declarations and source enrollments are excluded.
`on.gateway-dispatch.webhooks` and compiler `event:"webhook"` MUST reject,
including previously persisted enrollments. They MUST NOT fall back to the
gateway's separate manually approved route contract or raw-body forwarding.
Only explicit v2 declarations may activate through section 6.2 or 6.2.2. A v1 workflow
MUST NOT silently receive an incompatible batch or fall back to one dispatch
per event. The hand-authored `webhook-manifest-v2.json` fixture matches the
inspected source contract; it does not establish installed-compiler support.

Activity types select alternatives. `conditions` are ANDed; condition values
are typed alternatives. When `any_of` is present, at least one complete
nonempty AND group MUST also match. Status transition pairs MUST NOT be
flattened into independent from/to lists or a cross-product. Supported operand
types are string, boolean, and signed 64-bit integer; no string/number/boolean
coercion is permitted. A `*` path segment traverses array elements only, never
object keys. Missing, null, or incompatible payload operands do not match.
Matching uses delivered data only, without GitHub hydration or state history.

Discovery MUST bound lock artifacts to 8 MiB, header JSON to the configured
metadata ceiling (262,144 bytes by default), conditions to the configured
filter-node ceiling (128 by default), and the explicit local compiled-file index
to 1,024 routes.
The local inventory accepts at most 1,024 explicit absolute file paths and
reserves a 15 MiB declaration budget, leaving output framing within 16 MiB.
Workflow order MUST NOT affect the mapping. A cancelled, unreadable, invalid,
or over-budget inventory MUST fail rather than emit partial success.

`cao-webhook-gateway --role=discover --workflow=/absolute/path/ID.lock.yml`
emits `cao.webhook-trigger-mapping/v1` with `discoveryOnly: true` and `byEvent`.
The repeatable workflow option is discovery-only and MUST NOT be combined with
local simulation or production options/credentials. This role performs no
network requests, storage provisioning, admission, approval, or dispatch.
`TriggerMapping.Match` returns discovery candidates, never authorization.

Offline discovery MUST NOT activate subscriptions or forward raw webhook bodies.
`specs/fixtures/webhook-manifest-v1.json` is a hand-authored interchange fixture,
not proof of an actual compiler run. The manifest and CLI test suites derive
their positive/negative cases from this section. The separate managed profile
below adds activation and durable registry transitions.

### 6.2. Private repository discovery and security activation

Production webhook subscriptions MUST target private repositories. Fresh
authenticated checks MUST deny public, internal, archived, disabled, inaccessible,
or mismatched source/destination repositories. Organization-scoped project
sources additionally require explicit project assignments and a private
destination. This restriction does not turn RFC-034's non-authoritative public
Events API simulation into live authority.

Reviewed `control-plane.webhook-gateway.discovery.repositories` MUST explicitly
bind each `repository` name to its immutable `repositoryId`,
`receivingInstallationId`, and `dispatchInstallationId`. Names and IDs MUST be
unique; there MUST be 1–2,048 entries. Each entry MUST intersect both root allowlists
when both are present. Credential reach MUST NOT enroll additional repositories.
The required boolean `activateSecurity` delegates activation only of
compiler-supported `dependabot_alert` and `code_scanning_alert`. Its absence,
null, or invalid type is invalid; false permits discovery without activation.
No checked-in rollout is enabled merely by implementing this profile.

The dispatch role MUST backfill at startup and routinely rescan enrolled
repositories. `intervalSeconds` defaults to 300 and MUST be 30–3600 when supplied.
Ten-second wakeups process at most eight eligible repositories per round in
round-robin order, with a 30-second deadline per repository. The interval is an
eligibility threshold, not a completion-time guarantee under backpressure.
Every attempt MUST mint a fresh repository-restricted installation token and
verify the configured repository's identity and private classification.
Discovery MUST resolve the current default branch to a commit, list only
`.github/workflows/` at that SHA, and read only regular top-level `.lock.yml`
files at the same SHA. It MUST NOT follow download URLs, symlinks, or submodules.
The final default-head read MUST equal the initial head; moved or incomplete
inventories MUST NOT publish partial active mappings.

The directory response is bounded to 2 MiB and 512 entries; each repository
permits at most 128 compiled locks, 8 MiB per lock, and 16 MiB total fetched lock
bytes. Only validated compiler metadata, immutable identity, commit, digest,
and refresh state may be retained. The combined durable registry is limited
to 1 MiB or the operational record ceiling, whichever is smaller.
The repository count ceiling is an enrollment guardrail, not a storage or quota
reservation. Large fleets SHOULD use source-only inventories and centralized
campaign declarations; rich per-repository metadata can exhaust the byte ceiling
before the count ceiling. Derived discovery routes MUST be bounded to 2,048;
the separate explicit local trigger-mapping ceiling remains 1,024. Raising these
count ceilings MUST NOT expand a reviewed allowlist, API budget, byte ceiling,
per-round scan count, or live activation.
A missing workflow directory (HTTP 404) MAY produce an empty workflow inventory
only after successful authenticated private repository and initial head checks,
and MUST still pass the final head recheck. This permits source-only enrolled
repositories without workflows. Access loss, permission loss, invalid headers,
and exceeded bounds MUST suspend that repository, not preserve last-known-good
active routes. An authenticated empty inventory withdraws its prior workflows.

The fixed HMAC-authenticated `/webhooks/compiler` route MUST exist even before
backfill discovers any subscriptions. The receiving App SHOULD subscribe to
`push`, `repository`, and selected security events on this route. Push
added/modified/removed paths matching top-level `.github/workflows/*.lock.yml`
and repository-change events are invalidation hints. Forced/deleted pushes,
missing commit lists, and reported commit-count truncation invalidate
conservatively. Payload names, refs, paths, and URLs MUST NOT select a fetch
target or grant authority. The receiver MUST bind hints to an enrolled
repository/receiving installation, and atomically suspend its inventory before
HTTP 202. Storage failure MUST return failure. An invalid signature MUST cause
no registry write.

Registry publication MUST use one PostgreSQL operational transaction for
inventory CAS and effective queue-revision changes. Every accepted publication
or relevant hint MUST advance the CAS version, including hints on already-dirty
inventory and timestamp-only refreshes. A scan started before any such hint
MUST NOT overwrite it. Routing changes MUST advance a separate monotonic
effective queue revision and cancel older Ready/Claimed jobs atomically.
Unchanged refreshes MUST retain that revision. The reviewed policy revision
remains a separate monotonic authority ceiling; changing or disabling it clears
the registry. No new dashboard schema or operational table is required.

Only clean private inventories checked no more than twice their configured
interval ago may activate. Future timestamps fail closed. Derived grants MUST
expire at that same freshness bound, and 30-second admission proofs MUST NOT
outlive it. Automatically activated security subscriptions MUST use the
declaring workflow repository's compiler-specific repository-dispatch route.
They require contents-read and event-specific receiving permissions on each
selected source, and independent contents-write dispatch capability on that
workflow repository.
Project declarations MUST NOT autoactivate through this security delegation.

#### 6.2.1. Security source repository selectors

Compiler declaration v2 MAY include top-level `repos`, emitted for security
source selection such as:

```yaml
on:
  gateway-dispatch:
    repos:
      - bar/baz
      - foo/*
    security-alerts: {}
```

The upstream compiler places all declarations under `on.gateway-dispatch`;
roles/bots and other non-trigger settings remain outside that mapping under `on`.
Gateway dispatch is exclusive of all other workflow triggers.
Shared `repos` belongs inside `gateway-dispatch` and applies to GitHub notifications,
code-scanning/Dependabot and commands; trigger-local `repos` is rejected.
The repository gate `"gateway-dispatch": true` in `.github/workflows/aw.json`
remains independently required.
These compilation gates MUST NOT be treated as activation or dispatch authority.
The former workflow `on.webhook-gateway` flag, flat triggers, obsolete
`on.gateway`, singular Projects/security spellings, and old repository
`webhook-triggers` opt-in are rejected. Explicit `workflow_dispatch` or
`repository_dispatch` declarations MUST NOT accompany `gateway-dispatch`.
A shared-`repos`-only mapping is invalid; `security-alerts: {}` with shared `repos`
selects supported Dependabot and code-scanning sources, while explicit sources
remain authoritative. CAO consumes the compiled manifest, not this YAML.
For the supported security families, the normalized manifest and closed v2 wire
contract are unchanged; the pinned runtime interoperability evidence does not
establish installed-compiler emission of this authoring syntax.

**SR-01** A present `repos` MUST be a nonempty bounded array of lowercase, sorted,
unique strings. Each selector MUST be an exact `owner/repository` or a literal
`owner/*`. Wildcard owners, prefix globs, URLs, expressions and exclusions MUST
be rejected. Only compiler-v2 supported security declarations may select
repositories. Absence MUST remain absent; it preserves CAO's prior
same-repository activation and does not implicitly add every enrolled source.

**SR-02** Explicit selectors MUST intersect reviewed `discovery.repositories`
and root scope. Wildcards MUST NOT enumerate App inventory, discover additional
repositories, create enrollment, grant permissions or enable live rollout.
Both the source and declaring workflow repository MUST have clean, fresh,
private inventories. Source-only inventories MAY contain no workflows.
Missing, dirty, expired, future-dated or out-of-scope source inventory MUST NOT
produce a route. An unavailable declaring workflow inventory MUST withdraw all
its routes, regardless of the source inventories.

**SR-03** Each matching immutable source MUST receive its own subscription and
grant, binding that source's tenant, receiving installation, repository and
organization. The destination installation/repository, approved head and
whole-lock digest MUST remain the declaring workflow's. The grant MUST expire
at the earlier source/destination inventory bound. Cross-source events MUST
NOT share a dispatch batch; section 13.1's source-identity boundary is unchanged.
Existing invalidation and queue-epoch fencing MUST apply to both inventories.

**SR-04** Ingress MUST check the signed source repository against explicit
selectors before projecting each original delivery. Egress MUST bind every
projected alert URL to the authenticated current **source**, not the destination,
and enforce the selectors again. Live source access MUST use the receiving App;
workflow artifact approval MUST use the dispatch App and destination
installation/repository. Missing either capability MUST block dispatch.

Security dispatch context MUST use section 13.1's compiler-v2 envelope.
The current compiler supports Dependabot and code-scanning alerts, not
secret-scanning declarations. Its projected payload contains `url` and
`type` from each matching original event's `alert.url` and `action`, and only
the declared matching `filters` when predicates exist. Both reference keys MUST be present,
and the singleton type MUST belong to the event family and selected declaration.
Retained notifications MUST bind that type to their immutable original activity.
The URL MUST be a canonical
HTTPS `api.github.com/repos/OWNER/REPO/{dependabot|code-scanning}/alerts/NUMBER`
URL with no credentials, query, fragment, or encoded path. At ingress its
repository and alert number MUST agree with the signed source event; final
preflight MUST bind every URL's repository to the authenticated current
source identity, including explicitly selected cross-repository routes.
Enterprise API origins are not supported by this bridge.
Private source identities and predicates are checked before projection, and
original activity/immutable IDs remain separate durable event identity.
Raw alerts, unselected resource details, native transition evidence, and
sender provenance MUST NOT persist or be forwarded. Explicitly compiled
predicates may retain only their matching scalar literals. All further resource data
MUST be resolved inside the workflow through a configured provider-authenticated
reader so the provider enforces access. A URL is a reference, not authority;
denied or unavailable reads MUST NOT fall back to native webhook content or
attach credentials to arbitrary URL origins.
The complete request and client payload MUST each fit compiler ceilings before
any deduplication, as well as gateway limits.

Persisted inventories using an obsolete projection contract MUST remain
nonactivating at the current reviewed policy revision. A higher reviewed gateway
policy revision MAY revoke the entire incompatible inventory without interpreting
its obsolete declarations. That reset MUST advance the registry CAS and effective
queue revision atomically, cancel older ready/claimed work, reject lower or
same-revision conflicting control policies, and require fresh bounded discovery.
It MUST NOT reinterpret old delivery payloads, preserve discovered grants, or
expand enrollment. This implementation does not edit rollout policy automatically.

Compiler v2 forbids wire `_gateway` provenance, including a nested workaround.
Approved revision evidence MUST instead be extracted from all durable members
and passed through the typed `CompilerDispatcher` capability. Missing capability
or invalid/mismatched evidence MUST block sending; generic dispatch MUST NOT
bypass compiler preflight. Durable provenance remains mandatory and immutable.

The receiving-side authority check and independent final dispatch preflight
MUST re-fetch the destination's current default head and whole-lock digest. A changed head,
removed workflow, or changed artifact MUST block the POST. This is not immutable
workflow execution: repository dispatch runs GitHub's default-branch workflow,
and mutation after preflight remains an external race. Operators MUST establish
trust in the reviewed/protected default branch before enabling dispatch; where
that cannot be established, dispatch MUST remain disabled.

The `registry`, `discovery`, compiler-projection, signed-ingress, and PostgreSQL
test suites derive positive and negative cases from these requirements:
private-only activation, freshness, no implicit Projects activation, byte bounds,
startup/backfill, dirty-hint races, unchanged refresh, stale CAS, atomic queue
cancellation, restart, capacity rollback, and restricted runtime credentials.
`WebhookSubscriptionRegistry.tla` separately models these local registry/CAS,
freshness, and reservation invariants; it does not model an atomic GitHub
check-to-send operation, prove installed-compiler support, or replace live
deployment acceptance.

The selector, independent-authority and real PostgreSQL signed-ingress/worker
cases derive from SR-01–SR-04. They cover exact/wildcard intersection, distinct
receiving installations, source-only inventories, minimal URL/type/filter two-delivery batches
per source, source invalidation and destination withdrawal.
`WebhookSecuritySources.tla` separately abstracts selector matching as a set of
sources and exhaustively checks its intersection with enrollment/private state,
source/destination freshness at reservation, queue-epoch fencing and accepted
job stability. Its two sources, one destination, two revisions and one clock
advance produced 6,300 distinct states with none left. Enrollment/visibility
are fixed per trace but range over every initial subset; dynamic GitHub
revocation and atomic remote check-to-send are not proved by this model.

Reproduce the separately bounded registry check with the existing evidence
runner (official TLC v1.7.4 JAR); its retained result records input hashes,
configuration bounds, and checked properties:

```bash
python3 scripts/check-webhook-gateway-model.py --jar "$TLA2TOOLS_JAR" \
  --model specs/tla/WebhookSubscriptionRegistry.tla
python3 scripts/check-webhook-gateway-model.py --jar "$TLA2TOOLS_JAR" \
  --model specs/tla/WebhookSecuritySources.tla
```

#### 6.2.2. Independently enrolled external sources

Reviewed `externalSources` MAY explicitly enroll at most 64 Jira, Linear,
or Azure DevOps sources. It requires managed discovery but does not
depend on `activateSecurity`. Each source MUST bind a unique non-reserved route
`id`, family `event`, canonical HTTPS `resourceUrlPrefix` ending `/`, unique
receive-only `secretEnv`, nonempty explicitly enrolled `destinationRepositoryIds`,
and `expiresAt`. Named providers MUST additionally bind their native `scope`:
Jira project key, Linear organization ID, or Azure project ID and `collectionId`.
Custom webhook source enrollment MUST reject.
`specs/fixtures/webhook-external-deliveries-v2.json` illustrates all three bindings.
Source additions require restart; declarations MUST NOT register sources.

Jira MUST authenticate raw bytes using native `X-Hub-Signature: sha256=<hex>`
and use `X-Atlassian-Webhook-Identifier` for retry identity. Linear MUST verify
native `Linear-Signature` HMAC-SHA256, `Linear-Delivery`, and signed
`webhookTimestamp` within one minute; durable success/filtering returns HTTP 200.
Azure service hooks MUST use HTTPS Basic authentication with username `cao`,
a receive-only password, and body `id`; no native HMAC is claimed.
Secrets MUST be 32–512 bytes, resolved only in receive using
`CAO_GATEWAY_SOURCE_...` and optional `_PREVIOUS` environment names. Secret values
MUST NOT enter policy, state, errors, or logs; all other roles reject these variables.
Authentication and admission MUST bind one unchanged effective policy revision.

The receiver MUST bind native scope and resource identity before original-source
conditions and projection. Jira issue.self MUST equal the enrolled REST prefix
plus the same decimal issue.id. Linear MUST use a workspace-qualified canonical
issue URL and the enrolled organization. Azure MUST bind collection/project IDs
and use the work item ID, never an update/revision ID; version-1 snapshot fallback
requires the published contract's evidence. Jira resolution/label changelogs,
Linear updatedFrom and Azure state/tag deltas MUST follow the declared
normalization policy, without hydration or guessed state history. Derive all
established activities, including edited for updates, then intersect with selected
subscription types before persistence or forwarding. Unselected activities MUST
NOT survive; an empty intersection or contradictory closed/reopened evidence MUST
NOT qualify. Missing/ambiguous
transition evidence MUST NOT establish a lifecycle or label transition.

For a selected Azure closed activity, project exactly the entered configured
terminal name in `filters["closed-states"]`; for reopened, exactly the departed
configured terminal name. Complete delivered old/new state evidence is required.
Never forward the unmatched nonterminal side or the entire configured set.
`closed_states` remains `activity_normalization` metadata, not a source-path
equality condition. Without a forwarded selected closed/reopened activity, the
`closed-states` key MUST be absent even if terminal names are configured.

External source authority is current unexpired reviewed enrollment plus native
authentication, not a live provider resource-access/visibility probe. Source
GitHub installation/repository/organization IDs remain zero; a separate external
source ID binds grants and proofs. Source-local delivery IDs MUST be namespaced.
Each source MUST retain independent tenant/subscription/
batch identity. Grants MUST expire no later than enrollment or destination
inventory. Current private GitHub destination authority, head, and whole-lock
digest MUST still be checked independently. Revocation, expiry, inventory
invalidation and policy changes MUST fail closed and fence queued authority.
Native bodies and normalization evidence MUST NOT persist or cross the wire;
only URLs, activities and declared matching filter literals may remain.
Conservatively treat external references as restricted/private, never as evidence
of actual provider visibility. Workflows MUST use authenticated provider readers
for all additional resource data. URLs grant no access.

Shared compiler `orgs` MAY select Projects, GitHub notifications, the two security families and commands,
with lowercase/sorted/unique organization names or `*` patterns (1–39 ASCII
letters/digits/hyphens/stars, no leading/trailing hyphen). Patterns are ORed,
case-insensitively, and intersect `repos`, project scope, conditions, and reviewed
source enrollment. Even `*` MUST NOT enroll sources. Registry activation and live
source preflight MUST verify actual authenticated Organization owner type/ID,
not infer organization authority from URL owner text. External
declarations MUST NOT receive this GitHub-only metadata.

### 6.2.3 Native command subscriptions and label consumption

Command subscriptions MUST use synthetic family `slash_command` or
`label_command`, unchanged numeric-v2 batching and `resource-url-types-filters/v2`.
The normative compiler/runner contract is pinned to
`github/gh-aw@fe4f420f3f4f40bb1ebbcca85af0903d3a967bf3`, specifically
`gateway_command_webhook.cjs`, `validate_webhook.cjs`, `webhook_resource.cjs` and
`webhook_context.cjs`. Native slash parsing/matching uses
`slash_command_matcher.cjs`. Compilation or this receiver implementation MUST
NOT imply deployment or independently reviewed activation.

`command_filter` MUST contain the corresponding `slash-command/v1` or
`label-command/v1` policy, nonempty sorted/distinct `names` (slash at most 25,
label at most 50), sorted/distinct semantic `events`, and native `strategy`.
Slash supports `inline`/`centralized`; labels support `inline`/`decentralized`
and require `remove_label:true`; false or missing metadata MUST reject. Authoring
omission defaults to true, matching the pinned published contract.
Slash MUST omit `remove_label`, including
null. `native_events` MUST be the sorted/distinct native source union and
`types` the exact action union. `conditions` MUST be empty; `any_of`, `provider`
and `activity_normalization` MUST be absent. Shared repos/orgs intersect
independent source enrollment; names/strategies do not grant authority.

| Semantic source | Native delivery | Slash actions | Body | HTML path / required anchor |
| --- | --- | --- | --- | --- |
| `issues` | `issues` | opened, edited, reopened | issue.body | `/OWNER/REPO/issues/N`, none |
| `issue_comment` | `issue_comment`, no issue.pull_request | created, edited | comment.body | `/OWNER/REPO/issues/N#issuecomment-C` |
| `pull_request_comment` | `issue_comment`, with issue.pull_request | created, edited | comment.body | `/OWNER/REPO/pull/N#issuecomment-C` |
| `pull_request` | `pull_request` | opened, edited, reopened | pull_request.body | `/OWNER/REPO/pull/N`, none |
| `pull_request_review_comment` | `pull_request_review_comment` | created, edited | comment.body | `/OWNER/REPO/pull/N#discussion_rC` |
| `discussion` | `discussion` | created, edited | discussion.body | `/OWNER/REPO/discussions/N`, none |
| `discussion_comment` | `discussion_comment` | created, edited | comment.body | `/OWNER/REPO/discussions/N#discussioncomment-C` |

Labels select only issues/pull_request/discussion `labeled` actions and exact
original `label.name`, with unanchored item URLs. Slash commands MUST match an
actual position-zero token using native exact/`*`/trailing-`*` semantics. Reviews,
`submitted`, `ready_for_review`, `review_requested` and `#pullrequestreview-N`
MUST NOT qualify. Numbers and required comment anchors MUST bind positive
original IDs without leading zeroes. Resource URLs MUST bind the reviewed
`discovery.serverUrl` origin and clean base path (default public GitHub), the
actual receiving API configuration and current private source repository.
Inbound URLs MUST NOT choose an API origin or confer source authority.

Only canonical original HTML `url`, selected original singleton `type` and
exactly `filters.event` plus `filters.command` or `filters.label` singleton
actual matching literals may be persisted/forwarded. Bodies and arguments MUST
NOT persist, including for invalid deliveries. Prompt `gateway.events` is
compiler-local normalized `{delivery_id,payload}` access, not a wire field.

Managed command activation MUST require separately reviewed
`discovery.activateCommands:true` (default off), fresh private source/destination
inventories, approved route and independent live grants. Consumption additionally
requires the exact source's reviewed `allowLabelRemoval:true` and current
receiving-App event-category write permission; `remove_label` metadata is not a
write grant. Receive and local simulation MUST remain mutation-free.
The dispatch consumer MUST run only after fenced atomic durable batch admission,
within the existing deadline and shared quota, and recheck current source/write
authority immediately before each REST issue/PR or GraphQL discussion mutation.
Protected atomic attempt markers and expiring confirmed receipts MUST outlive
the maximum job TTL. Confirmed receipts suppress repeated writes across restart;
unknown outcomes MUST NOT replay against subsequently reapplied labels.
Unconfirmed consumption MUST make the batch terminal ambiguous without workflow
dispatch. Minimal authenticated ID/label reads MUST NOT hydrate notifications.

### 6.2.4 GitHub notification subscriptions

The authoritative catalog is `actions/setup/js/github_notification_webhooks.json`
at the pinned upstream SHA. The receiver's embedded semantic catalog MUST match
every native event, action, issue kind, resource path/anchor, URL path, and typed
filter path/enum/action restriction. Runtime conformance checks that equality.
All ten notification families require singleton exact `native_events`, a nonempty
selected subset of catalog actions, and ordinary catalog equality conditions.
`provider`, `activity_normalization`, `command_filter` and `any_of` MUST be absent.

Activation MUST require separately reviewed `discovery.activateNotifications:true`,
absent/false by default, fresh private source/destination inventory, shared repos/
orgs selectors, independent approved routes and current read-only source-category
permissions. Security or command activation MUST NOT activate notifications.
Notification declarations MUST NOT grant write permission or enroll sources.

Before durable admission the receiver MUST classify the authenticated original:
`issues`/`issue_comment` require an issue object with no `pull_request` property;
`pull_request_comment` requires object-valued original `issue.pull_request`.
Null/scalar/array or contradictory evidence MUST NOT infer source identity.
Other PR/discussion families require their original parent objects.
Canonical HTML URL repository, parent number/commit SHA and comment/review ID
MUST match original evidence and the independently configured GitHub origin/
exact Enterprise base path. Review threads MUST use the first delivered comment's
URL and ID; absent/empty comments or missing canonical evidence are nonactivating,
without hydration. Deleted resources remain references even if a later
provider-authenticated workflow read returns not found.

Only `url`, selected singleton original `type`, and actual declared matching
filter literals may persist/forward. Array selectors MUST exclude unmatched
elements; changed-label filters apply only to `labeled`/`unlabeled`.
Bodyless PR `ready_for_review`/`review_requested`, submitted reviews, review threads
and commit comments MUST NOT widen the narrower command catalog.
Source registration remains independent of compiler workflow-trigger exclusivity.
No deployment or hosted interoperability is implied by local runtime conformance.

## 7. Event subscriptions and generic filters

Subscriptions declare event family, optional action, and a bounded typed
predicate tree. Supported primitives SHOULD be exact equality, membership in
bounded literal sets, existence, and boolean conjunction/disjunction/negation.
Only versioned allowlisted payload paths and normalized identity fields are
readable. Missing required fields, type mismatches, unknown operators, excessive
depth/node count, and exceeded evaluation budget MUST reject the match.

No JavaScript, shell, templates with evaluation, network lookup, unbounded regex,
or payload-driven destination selection is allowed. Event/action names are data
in the registry, not special-case dispatch branches. Source repositories MUST
already be explicitly enrolled; filters only narrow the authorized envelope.
Fanout is deterministically bounded and paginated against one registry snapshot;
partial admission MUST be reported and safely resumable without duplicate jobs.

## 8. Projects and cross-repository identity constraints

Reviewed `control-plane.webhook-gateway.projectAssignments` MAY contain at most
128 entries binding one unique `projectId` to its `organizationId` and an
enrolled `repositoryId`. The exact assignment MUST match the subscription's
organization and destination repository at admission and egress. Missing,
duplicate, or differently assigned projects fail closed. Assignments constrain
separately reviewed project subscriptions and grants; they do not activate
compiler project declarations or grant organization-wide repository discovery.

Project-related events MUST use compiler-declared project constraints, not
hardcoded project event names as an authorization shortcut. A constraint binds
GitHub host, immutable project node/database identity as applicable, owning
organization/user identity, allowed item/content repository IDs, and permitted
visibility. Account-level installation or project access does not imply access
to every item repository.

Validate payload identities against fresh authenticated API evidence where
necessary. A project item referencing another repository MUST NOT widen source
scope; inaccessible, redacted, draft, deleted, or unverifiable content fails
closed unless the approved declaration explicitly supports a metadata-only
case that discloses no unauthorized content. Public project visibility does not
make private issue content public. Capability and event availability vary by
GitHub deployment; unsupported subscriptions remain inactive.
For a `projects_v2_item` payload, the containing project's identity is
`projects_v2_item.project_node_id`; `projects_v2_item.node_id` identifies the
item and MUST NOT be substituted for the project. Compiler-declared field
bindings and typed identity validation enforce this distinction rather than
event-name-specific authority branches.
The corresponding generic compiler identity-constraint JSON Pointer is
`/projects_v2_item/project_node_id`, never `/projects_v2_item/node_id`.
Current host authorization additionally checks the exact approved nodes through
the receiving App's authenticated GraphQL transport: each must be a ProjectV2
with the same node ID and owning Organization database ID. The initial
implementation supports organization-owned projects only; user-owned or
unverifiable nodes fail closed. The query fetches identity/ownership only, not
project contents, item fields, or missing filter operands. Every response
participates in shared GraphQL quota accounting.
`Source.ProjectIDs` and `AdminGrant.ProjectIDs` are opaque GraphQL node-ID
strings such as `PVT_...`, not numeric database IDs. Repository, organization,
and installation IDs remain numeric. A nonempty projectIds declaration MUST
have a generic identity constraint whose approved string or string-array value
is exactly the same project-ID set; declaring projects without an event
identity constraint MUST fail closed. The generic predicate supports exact
scalar equality or membership in that declared string set, without hardcoded
Projects authorization branches.

Proposed source-constraint fixture fragment only—not a complete subscription
and not output from a real compiler:

```json
{
  "projectIds": ["PVT_APPROVED_PROJECT_NODE_ID"],
  "identityConstraints": [
    {
      "path": "/projects_v2_item/project_node_id",
      "value": "PVT_APPROVED_PROJECT_NODE_ID"
    }
  ]
}
```

GitHub's published Projects custom-field change example includes
`changes.field_value.field_name`, `field_type`, and `from.name`/`to.name`
for single-select fields. The compiler emits paired transition predicates over
those delivered operands, alongside the containing `project_node_id`, field
name `Status`, and type `single_select`. Sparse schema fixtures containing only
`field_node_id` and `field_type` MUST NOT be treated as evidence that GitHub
never supplies transition names. Conversely, host/version differences and
deliveries missing those operands MUST NOT be repaired by inventing fields.

A requested status-name, label, or transition filter whose operand is absent
evaluates Unknown/nonmatch, including beneath negation; it MUST NOT trigger a
fallback API fetch or fabricate an enriched payload. Such a subscription cannot
claim to implement the requested intent without compiler evidence that the
actual event contract supplies the needed fields. This limitation remains true even with a valid grant and a discovered route.

## 9. Grants at admission and immediately before dispatch

For GitHub sources, admission MUST validate receiver/source installation activity, source repository
membership, applicable event permissions, trusted active subscription revision,
subscriber/destination installation activity and dispatch capability, source and
destination immutable identities, visibility/confidentiality policy, and the
control repository's reviewed rollout policy. These form an intersection, not a
union. A source installation grant never authorizes a subscriber implicitly.
External source authority instead follows §6.2.2; current destination checks and
the reviewed rollout intersection remain mandatory for every source family.
Root allowed-repository and allowed-owner restrictions apply by intersection,
not union. Root-scope changes MUST bump the gateway revision because its
effective policy digest changes; same-revision digest changes conflict.
The isolated receiver has no App private key: the dispatch role delegates
admission evidence through a 30-second authorization proof in the existing
operational StateStore, at one bounded fixed subscription-ID key. Receive accepts
only fresh, complete evidence bound to the exact trusted revision and current
policy. The proof primer runs even with no queued jobs and retains a rotating
cursor across deadline-limited rounds. Failed or slow subscriptions MUST NOT
monopolize the start of every round; an interrupted attempt advances the cursor.
Refresh failures emit sanitized diagnostics through the existing debug logger.
Such a proof is bounded
observation, not an instantaneous guarantee against external GitHub revocation.

The worker MUST independently refresh the same grants before Dispatching and
check any intervening local invalidation before reserving dispatch. A stale cache,
unavailable authorization API, inaccessible policy SHA, or missing evidence
means defer until a bounded deadline or reject, never assume permission.
Known revocation, subscription removal, or disallowed visibility means Cancelled for ready/claimed work.
`AuthorizationSnapshot.Complete = FALSE` is incomplete evidence and MUST defer,
not infer cancellation from default false fields. With Complete true, observed
inaccessibility or disallowed visibility yields a cancellation decision.
Snapshots bind `JobID`, the exact claim `Fence`, and the next dispatch
`Attempt = job.Attempt + 1`, as well as checked-at/expiry and both grants.
Do not fall back to another installation, broader token, anonymous access, or a
new destination.

GitHub provides no transaction spanning authorization lookup and dispatch.
Revocation/visibility changes that have not yet been observed can race with
send and acceptance. The gateway guarantees **no new send after a relevant
local invalidation is observed**, not “no send after remote revocation.”
Observation and send reservation MUST serialize locally. This cannot undo a
request already in flight or an accepted workflow.
Here “send” means the irreversible local send reservation at BeginDispatch,
not the instant bytes reach GitHub. A request handed off earlier can still
arrive or be accepted after local observation. Implementations MUST NOT advertise
a stronger wire-level guarantee or hold a database transaction open throughout
the remote request to pretend this race has disappeared.

## 10. Isolated receive and dispatch deployments

Receive and dispatch MUST be independently deployable processes with separate
secrets, network policy, health checks, concurrency budgets, and database roles.
The receiver holds current/previous webhook verification material and no App
private keys. It obtains admission evidence only from fresh trusted proofs.
The dispatcher alone loads both receiver-App and dispatcher-App private keys:
the former supports fresh source authorization reads, the latter approved
destination dispatch. Every egress authorization independently rechecks live
GitHub destination access, source authority, current root-policy
scope, exact approved revision, and grant; it MUST NOT reuse an admission proof
as authority to send. GitHub sources require live source installation/repository
access; external sources require current reviewed enrollment under §6.2.2,
without claiming live provider access. The dispatcher owns token minting and dispatch egress,
not public webhook ingress.
Both CLI roles are explicit and an unset role fails closed. Receive startup
rejects either App-key file environment variable; dispatch startup rejects
current/previous webhook-secret variables. Plain HTTP receive listeners are
permitted only on numeric loopback; non-loopback listeners require explicit
TLS certificate/key flags.
The receive listener has `GET /healthz` returning 204 and `GET /readyz`
returning 204 only for current valid enabled policy, applied queue revision,
and reachable queue depth, otherwise 503. Health routes bypass the webhook
semaphore. A shared 64-handler limit rejects excess webhook requests with 503
before any body read; admitted handlers have an 8-second context deadline.
HTTP read/write timeouts are both 10 seconds. These bounds are not queue
admission or authorization and MUST NOT bypass either gate.

Each App key rotates independently through its mounted secret file plus
dispatch-role restart. Webhook HMAC rollover is separate: update current and
bounded previous secrets, restart receive, then remove previous material and
restart receive when overlap ends. Rotation MUST NOT widen approved App IDs,
installation reach, grants, or scope.
Deployment MAY use the dedicated `webhook-gateway-runtime` Docker target, which
has no Node/dashboard dependency and runs as nonroot UID 65532. It does not
change the Dockerfile's default final dashboard target.

Ingress authenticates exact raw bytes with the configured webhook secret,
constant-time signature comparison, body/time limits, and bounded parsing before
admission. HTTPS and secret rotation with bounded overlap are required.
Unsigned health checks cannot enqueue work. The dispatcher exposes no public
arbitrary dispatch endpoint. A verified receiver authenticates messages, not
their authority to execute arbitrary instructions.

## 11. Durable queue record and capacity

Every job stores immutable identity/digest, approved subscription snapshot,
payload-free EventIdentity, bounded compiled dispatch request, receive and
expiry times, state, not-before, attempt count, owner, generation, lease deadline,
outcome classification, and bounded diagnostics. Credentials and raw tokens
MUST NOT enter records. Raw source payloads MUST NOT persist, whether matched
or filtered. After HMAC verification, ingress evaluates filters and identity
constraints on ephemeral raw bytes, builds only allowlisted output plus reserved
provenance, and discards the source body. `Job.Event` contains SourceID,
DeliveryID, Name, Action, and numeric InstallationID, RepositoryID, and
OrganizationID, with no Payload. `Job.Payload` is the complete immutable
single-member projection request, not an event body or the outbound batch.

The worker validates stored EventIdentity, exact immutable approved subscription
digest, fresh grants, and each compiled projection's shape/keys/limits, then
builds one bounded, unique-context batch without modifying the stored projections.
It MUST NOT ParseEvent or rerun source
filters on compiled dispatch JSON. The admission DeliveryDigest remains the hash
of the original authenticated delivery; it is independent of projected payload
bytes and MUST NOT be compared as if both byte sequences were identical.
Projected approved leaf data still requires protected database access,
deployment-provided encryption at rest, strict byte/retention bounds, and no
body-level logging. Database time is authoritative for leases and expiry.

Admission atomically reserves both job and byte capacity and creates the dedup
marker plus Ready record. Capacity failure rolls back all three; it MUST NOT
consume delivery identity. Bound pending, leased, delayed, terminal/tombstone,
payload bytes, attempts, fanout, and per-tenant budgets separately. No eviction of
accepted pending work is allowed. Pruning a terminal record MUST retain a dedup
tombstone through the configured redelivery horizon; admission age limits prevent
older deliveries from becoming executable again. GitHub webhook requests have no
universal signed timestamp, so header freshness alone MUST NOT be invented as
replay protection. Expired tombstone redrive needs explicit policy.

## 12. State machine, leases, fences, and crash recovery

| From | Guard/event | To |
| --- | --- | --- |
| Absent | authorized atomic admission | Ready |
| Absent | known policy denial, bounded diagnostic | Rejected |
| Ready | eligible claim, available capacity | Claimed |
| Claimed | refreshed grants, fenced owner, live lease, quota | Dispatching |
| Claimed | lease expires before possible send | Ready, Expired, or Failed |
| Ready | dispatch deadline expires | Expired |
| Ready/Claimed | observed invalidation or newer policy revision | Cancelled |
| Dispatching | accepted response durably recorded | Succeeded |
| Dispatching | proven nonacceptance, exact current policy, budget/deadline remain | Ready |
| Dispatching | proven nonacceptance, current policy invalidated | Cancelled |
| Dispatching | proven nonacceptance, no retry budget | Failed |
| Dispatching | uncertain outcome, crash, or lease expiry | Ambiguous |

Claim increments a monotonically increasing generation and binds owner and
deadline. All worker check/start/send/result writes MUST compare owner,
generation, and live lease; reapers atomically compare the generation/deadline
they inspected. Recovery MUST NOT race a replacement lease. Generation numbers
MUST NOT wrap or be reused. Result updates from stale workers are ignored and
audited, including after another worker claims the same job.

Persist Dispatching before making a network request. A crash after that commit
but before any bytes leave is conservatively Ambiguous if nonacceptance cannot
be proven. Expired Dispatching MUST NOT become Ready. All terminal states are
immutable; operator reconciliation is a new audited action, not an overwrite.
An in-flight request may outlive its lease: fencing protects local writes, not
the remote GitHub API.
Dispatching is the irreversible reservation boundary: later policy invalidation
does not cancel it or prove nonacceptance, even when physical transmission has
not yet started. Fresh grants, exact enabled policy, and hierarchical shared
quota reservations MUST all be committed before that boundary without holding
the transaction open for HTTP.

## 13. Delivery semantics, retries, and expiration

Succeeded means the dispatch endpoint accepted the request, not that an Actions
run started, completed, was authorized internally, or achieved operational value.
The endpoint has no transactional gateway idempotency key. Pin and test the REST
API version: current `2026-03-10` documentation specifies HTTP 200 containing
`workflow_run_id`, `run_url`, and `html_url`, while older supported API versions
may return 204 without a body. The adapter explicitly pins
`githubapp.WebhookGatewayAPIVersion = "2026-03-10"` on both dispatch transports,
overriding go-github v66's older default; tests verify the outbound header.
Its safe outcome retains accepted HTTP status and a validated numeric
WorkflowRunID when available, never the raw success body or unrestricted run
URLs. A bodyless legacy response need not have a run ID and MUST NOT fabricate
one. These observations still mean acceptance only, not successful execution.
A lost
response after external acceptance MUST become Ambiguous even when an unreceived
successful response would have contained the run ID.
Ambiguous is terminal by default and MUST NOT be automatically replayed.

Each subscription explicitly selects workflow_dispatch or repository_dispatch;
neither is an implicit fallback for the other. The latter has a different
contract: HTTP 204, at most 10 top-level
`client_payload` properties, `event_type` at most 100 characters, and payload
under 64 KiB. Selecting it requires separately approved compiler metadata,
input/trigger validation, and endpoint tests; do not reuse workflow_dispatch's
25-input or run-ID response assumptions.
The gateway itself deliberately caps both transports at 10 total properties
and under 64 KiB **after** batch framing and any reserved provenance injection, even
though current GitHub workflow_dispatch supports 25 inputs. Batch framing uses
five properties for generic routes and two for compiler routes; approved projections remain separately bounded.
These gateway bounds MUST NOT be widened merely to match the upstream maximum.
workflow_dispatch accepts its documented HTTP 200 and legacy 204 contracts;
repository_dispatch accepts HTTP 204, not an undocumented 202 response.

Individual stored projections retain internal `cao.webhook/v1` provenance,
including source/delivery/subscription IDs and complete approved Revision.
This is an internal immutable-member representation, not a supported outbound
singleton wire format. Generic routes replace it with section 13.1's batch
provenance; compiler routes carry approved revision evidence out of band.
Both paths MUST first validate all members. User mappings MUST NOT overwrite
or supply `_gateway`; collisions and post-framing byte/property overflows fail
closed. Local input declarations remain contract fixtures, not proof that a
real compiler produced corresponding workflow inputs.

Retry is allowed only on positively classified known-safe nonacceptance, with
bounded attempts, persisted backoff/jitter, not-before, and a fixed original
deadline. A local quota refusal before send is safe to defer. A documented
explicit refusal MAY be retried according to its semantics. Generic 5xx,
connection resets, partial writes, and timeouts are not proof of nonacceptance.
If retry classification is uncertain, choose Ambiguous.
An explicit HTTP 429 rate-limit refusal is a concrete known-nonacceptance retry
case: honor Retry-After and original expiry, bound attempts, and persist backoff.
It MUST NOT be conflated with a lost response or generic 5xx. In the model this
is Send with `accept = FALSE` followed by Result with `known = TRUE`; uncertainty
uses `known = FALSE`, while lease recovery always produces Ambiguous.
Finish MUST recheck current persisted enabled policy and exact revision in the
same transaction before scheduling a safe retry. If authority changed while the
attempt was Dispatching, known nonacceptance becomes Cancelled rather than an
obsolete-policy Ready job. An accepted or uncertain old in-flight attempt can
still finalize Succeeded or Ambiguous; revocation cannot undo its handoff.

Duplicate deliveries, concurrent receivers, and worker restarts MUST preserve
one durable identity. This gives at-most-one possible acceptance per identity
under the modeled safe-failure assumption, not exactly-once workflow execution.
Receiving a duplicate does not create a new deadline or renew approval.

### 13.1. Batched dispatch and unique handler context

**BG-01** A dispatch MUST contain one or more events of the same original kind.
The compiler's normalized handler context MUST expose a unique affected-resource
array at `aw_context.items`, including singleton flushes. Distinct deliveries
remain in `aw_context.webhook.deliveries`; resource deduplication MUST NOT discard
activity history or infer latest state. The compiler, not an inbound `items`
field, derives those identities. Generic approved routes instead expose an
`aw_context` array of unique complete projections, using canonical object-key
order without type coercion; delivery IDs/provenance do not participate.
Neither representation is a chronological event log.

**BG-02** Members MUST have identical full subscription, artifact digest, queue
revision, source route, event family, installation, repository, and organization
identities. Different workflows, event kinds, grants, destinations, or revisions
MUST NOT be mixed. Predicate evaluation remains per signed original event before
admission; batching MUST NOT hydrate, re-filter, or reconstruct relationships.

**BG-03** Compiler repository dispatch MUST use this closed envelope:

```json
{
  "event_type": "APPROVED_WORKFLOW_ROUTE",
  "client_payload": {
    "schema_version": 2,
    "aw_context": {
      "webhook": {
        "event": "dependabot_alert",
        "deliveries": [
          {"delivery_id": "delivery-a", "payload": {"url": "https://api.github.com/repos/owner/repo/dependabot/alerts/12", "type": ["created"]}},
          {"delivery_id": "delivery-b", "payload": {"url": "https://api.github.com/repos/owner/repo/dependabot/alerts/12", "type": ["reopened"]}}
        ]
      }
    }
  }
}
```

The compiler retains both activities and derives one unique affected item from
the canonical resource URL, not sender-supplied resource details.
Each producer delivery ID MUST be unique; admission deduplicates redelivery.
Per-delivery event overrides, supplied `items`, legacy envelopes and additional
provenance keys MUST be rejected. Gateway `aw_context` accepts only `webhook`;
resource-detail side channels are forbidden. Gateway revision evidence stays
out of band. Generic approved repository-dispatch routes have a separate
contract, not a fallback or compatibility path to the compiler runtime:

```json
{
  "event_type": "APPROVED_WORKFLOW_ROUTE",
  "client_payload": {
    "schema_version": 2,
    "event": "dependabot_alert",
    "delivery_ids": ["delivery-a", "delivery-b"],
    "aw_context": [{"url": "https://api.github.com/repos/owner/repo/dependabot/alerts/12",
                    "type": ["created"]}],
    "_gateway": {
      "version": "cao.webhook-batch/v1",
      "sourceId": "compiler",
      "subscriptionId": "APPROVED_SUBSCRIPTION",
      "revision": {"repositoryId": 42, "commitSha": "APPROVED_FULL_SHA",
                   "metadataPath": ".github/workflows/test.lock.yml",
                   "digest": "APPROVED_FULL_SHA256", "compilerId": "gh-aw-manifest-v1"},
      "batchId": "SHA256_OF_SORTED_JOB_IDS"
    }
  }
}
```

This illustrative fragment uses placeholders, not valid enrollment or authority.
All distinct source delivery IDs MUST be retained in sorted order, even when
contexts collapse. `batchId` is SHA-256 of the JSON array of sorted durable job
IDs, not a GitHub idempotency guarantee. Workflow dispatch uses the same five
properties under `inputs`, with `schema_version`, `delivery_ids`, `aw_context`,
and `_gateway` JSON-encoded as strings; `event` is an ordinary string. Approved
metadata MUST assert both `destination.gatewayInput: true` and
`destination.batchInput: true`, attesting the compiled workflow declares all
five batch inputs. Legacy envelopes and missing batch capability MUST fail
closed; no singleton-compatibility transport is permitted.

**BG-04** `BatchQueue.ClaimBatch`, `BeginDispatchBatch`, and `FinishBatch`
MUST persist complete compatible claim groups. Each member retains its own CAS
version, generation, attempt, receipt, TTL, and immutable projection. A unique
claim-group owner and one common minimum lease deadline bind a batch.
BeginDispatchBatch MUST validate every member fence and fresh authority, reject
subsets/repeats, and transition all members to Dispatching with one charge to
each hierarchical rate bucket in one transaction. Any failure MUST roll back
all reservations, attempt increments, and charges. FinishBatch MUST atomically
record the shared acceptance/outcome on every member with all exact fences;
a stale sibling MUST NOT permit partial completion. Safe nonacceptance may
apply each member's original TTL/attempt ceiling independently.

Fresh authenticated GitHub facts MAY be shared only across this exact compatible
identity boundary. Evidence MUST still be bound separately to each member's
job ID, claim generation, next attempt, and checked-at/expiry. Batch authorization
MUST be independent of admission proofs. Shared installation quota acquisition
and release occur once per external attempt, not per context.

**BG-05** An unknown outcome or lost completion MUST make every reserved member
terminal Ambiguous, never automatically requeue individual events. Bounded
maintenance may settle members incrementally, but a partially recovered group
cannot finish or reserve again using stale fences. Policy invalidation MUST
serialize with the complete reservation; in-flight batches remain irreversible.

**BG-06** Long-running production/local roles MUST provide a two-second
coalescing threshold on the oldest eligible member; it is not a minimum event
count. A singleton MUST be eligible after that window, subject to authorization,
quota, and worker availability. Explicit local one-shot mode flushes its already
polled backlog without this delay. The persisted cursor MUST rotate lexically
over `(tenant, subscription)` routes rather than letting a continuously busy
workflow starve another route. Shared concurrency counts active claim groups,
not their member count. This allows server-controlled load balancing across
workflow routes and replicas without starting one workflow per webhook.

**BG-07** Replicas MUST NOT claim the same member concurrently. Complete-group
claiming and cursor advancement MUST commit atomically. Renewal may extend
individual leases before BeginDispatchBatch; any incomplete renewal MUST cause
no send, and reservation MUST restore one common minimum deadline. A reserved
batch member MUST NOT individually extend that deadline while Dispatching.

**BG-08** A batch MUST contain at most 64 retained jobs and fit both gateway and
compiler request/client-payload byte ceilings after all framing. Claims examine
at most 64 eligible candidates in a selected subscription. A non-fitting later
candidate MUST remain Ready; it MUST NOT be dropped, acknowledged as dispatched,
or made Ambiguous merely because another batch is full. Ingress MUST reject a
projection that cannot fit a singleton batch before committing any receipt.
Admission fanout's `QueueOptions.MaxBatch` is a separate bound.

`specs/fixtures/webhook-batch-conformance.json` generates signed-ingress-to-worker
tests for unique contexts, separated kinds, and singleton array shape on both
PostgreSQL. The atomic, recovery, fairness/concurrency, replica, and
packing tests reference BG-04–BG-08 directly. Unknown vector fields/versions fail.
`WebhookDispatchBatch.tla` separately models bounded compatible claim groups,
set-valued contexts, all-member reservation, one token charge, local revocation,
crashes, lease recovery, and uncertain acceptance. Its set-valued wire abstraction
represents unique normalized elements, not compiler delivery-array bytes.
Its retained result covers
108,272 distinct states at the supplied bounds. It does not prove Go refinement,
wall-clock fairness, transport bytes, or a compiler/hosted integration.

```bash
go -C server test -p 1 -race ./internal/webhookgateway \
  ./internal/operational/postgres \
  ./internal/webhookgatewayhost ./internal/githubapp \
  ./internal/webhookgatewaylocal ./cmd/cao-webhook-gateway
python3 scripts/check-webhook-gateway-model.py --jar "$TLA2TOOLS_JAR" \
  --model specs/tla/WebhookDispatchBatch.tla
```

Set `POSTGRES_URL` to a disposable local database to execute PostgreSQL cases;
without it they skip. Package serialization avoids owner-bootstrap DDL contention
on the shared test database. This suite is local conformance, not live acceptance.
Set `WEBHOOK_COMPILER_RUNTIME_DIR` to `actions/setup/js/` from the inspected
upstream commit to execute `TestCompilerBatchRuntimeInteroperability`,
`TestCompilerSelectedSourceRuntimeInteroperability` and
`TestCommandPublishedRuntimeInteroperability`; the command test verifies SHA-256
pins for the five actual command/validator/resource/context/matcher modules.
Without the directory these optional
tests skip. They pass production-built batches through the actual compiler
validator/normalizer and check that two distinct deliveries yield one unique
affected item, and that any out-of-selector URL rejects the complete batch.
They do not demonstrate a hosted workflow run.

## 14. PostgreSQL extension and existing service reuse

Extend `server/internal/operational/` with a focused optional gateway queue/
subscription contract and capability, rather than reusing collection jobs or
CLI commands with incompatible state semantics. PostgreSQL is the initial
durable provider. Construction MUST reject missing, volatile, or process-scoped
capabilities for the isolated gateway topology. Redis/memory MUST report
unsupported until they implement the complete contract and conformance suite;
there is no automatic fallback or dual write.

The PostgreSQL adapter owns namespace-isolated `cao_operational_*` gateway
tables, immutable registry revisions, jobs, dedup tombstones, capacity accounting,
and atomic conditional transitions. Use transactions and row/CAS locking for
claims, concurrent invalidation, retries, and reclamation; bounded
`SKIP LOCKED` selection MAY avoid unrelated-job contention. Admission and
maintenance must follow existing protected-state budgets and cancellation rules.
Operational migrations remain separate from dashboard entity/schema generation.
Policy MUST be read fresh, while compiler metadata MAY be cached only by its
exact immutable approved digest. Adding source routes or changing App IDs,
namespace, queue options, or worker count requires role restart; incompatible
cache changes fail closed instead of hot-reconfiguring authority/topology.

Reuse `server/internal/githubapp/` for existing App authentication and governed
API transports, adding explicitly scoped dispatch functionality without silently
upgrading the read-only collector App. Reuse `githubquota` and focused operational
quota services across replicas; unavailable quota storage blocks sending.
Quota-observation persistence failures during token mint, repository lookup,
source checks, or workflow-revision preflight MUST prevent the outbound POST.
Production composition MUST provide `Worker.Budget`, whose focused contract is
`Acquire(context.Context, Job) (release func(), err error)`, before durable
BeginDispatch, in addition to the queue's atomic hierarchical reservation.
The shell permits nil Budget only for isolated pure-test composition; production
MUST NOT omit it. Acquisition denial leaves the job claimed without consuming a
dispatch attempt, and the acquired callback MUST be released on every exit.
The production paired budget gate/dispatcher owns actual response-based quota
reconciliation. After sending, a reconciliation failure MUST NOT overwrite a known HTTP
acceptance or prevent durable fenced Finish from recording it, and MUST NOT
cause a replay. There is no separate DispatchQuota/DispatchReservation API.
Reuse existing logger/telemetry factories and application lifecycle: cancel
polling, stop/join workers, then close storage. Do not create a second credentials,
quota, logging, or canonical-data framework.

## 15. Rate limits, health, and operational telemetry

Reserve per-installation/API-resource and deployment-wide request capacity
before send. Respect Retry-After, primary/secondary limits, and bounded
concurrency. Quota unavailability means no send; waiting does not bypass expiry.
Do not hold a database transaction during GitHub calls or backoff.

Expose bounded counters for admission outcomes, queue state, age, capacity,
claim/reap, safe retries, stale fences, grant denials, quota deferral, dispatch
acceptance, and ambiguity. Structured logs/traces MUST NOT contain event bodies,
credentials, raw URLs with secrets, private titles, or unrestricted payload
attributes. Hash/correlate sensitive identities only under approved diagnostics
policy; avoid unbounded metric labels. Health distinguishes receiver readiness,
durable storage, registry freshness, worker capacity, and quota/auth degradation.
Dashboard reporting consumes authorized derived evidence, not live queue state
as browser authority.
The gateway defaults `OTEL_SERVICE_NAME` to `cao-webhook-gateway` when unset;
operators MAY select role-specific names without changing bounded metric labels.
The approved dispatch trace may include `cao_webhookgateway.accepted_status`
and numeric `cao_webhookgateway.workflow_run_id`, alongside delivery/job trace
identities. These are not metric labels and do not authorize subsequent work.

## 16. Threat model and security controls

Threats include forged/replayed webhooks, malicious repository metadata,
cross-tenant delivery collisions, repository transfer/name reuse, private data
exfiltration through inputs, removed grants, stale workers, duplicate dispatch,
unbounded fanout, quota exhaustion, and compromised ingress credentials.
Mitigations are signed-byte validation, immutable IDs, trusted revision approval,
both-phase grant checks, allowlisted minimized input mapping, strict bounds,
durable dedup, generation fencing, isolated deployments, and terminal ambiguity.

Do not forward raw payloads, user-controlled refs, callback URLs, or repository
content as trusted instructions. Inputs MUST carry only approved bounded data
and an opaque correlation/job identity. Destination workflows must treat all
event-derived input as untrusted and reapply normal CAO policy/safe-output
controls. Review mode remains the default; the gateway cannot promote to live.
Retain only approved projected leaf data, encrypted with narrowly scoped access
and short approved retention, never raw source events. Deleting projected
payloads must not delete replay protection or falsify audit outcomes.

## 17. Formal model, checked properties, and limitations

[`tla/WebhookGateway.tla`](tla/WebhookGateway.tla) and its base configuration model two
jobs, two distinct workers, one durable pending slot, two time steps, and two
claim generations. Jobs abstract immutable dedup tuples; authorization abstracts
the conjunction of grants, approved revision, and visibility. Remote authority
can change to Revoked, Removed, or Hidden before local observation. Time,
worker crashes/restarts, rate availability, safe nonacceptance, external
acceptance, and lost outcomes are nondeterministic. Records survive crashes.

The additional concurrent configuration uses two pending slots, two jobs, two
workers, one time step, and one claim generation. Unlike the one-slot contention
configuration, it explores two distinct jobs being claimed/dispatching
simultaneously. Both configurations are required: the first checks contention
and safe retry/reclaim generations; the second checks concurrent queue entries.

TLC checks TypeOK, BoundedQueue, AcceptedNeverRetried,
SuccessRequiresAcceptance, ObservedChangeBlocksNewClaims, and DispatchRequiresCheck.
Action properties check immutable terminals, fenced worker mutations,
monotonically increasing generations, and
NoDispatchReservationAfterObservation. Stale tickets remain available to competing workers;
incorrect owner/generation/lease candidates cannot transition. Duplicate arrival
is an explicit stuttering action. Ambiguous outcomes cannot retry. A remote
change between Check and Send deliberately remains reachable; claiming an
instantaneous remote-revocation invariant would be false.
BeginDispatch is the irreversible local reservation; Send abstracts the eventual
possible external outcome, and Result separately models observing or losing
that outcome. Observation cancels ready/claimed work, not already-reserved
dispatching work. The latter can still be externally accepted after observation.
It is not a distributed atomic transaction with GitHub. The model abstracts one
monotonic policy invalidation rather than the concrete int64 revision arithmetic,
which requires adapter conformance tests. Local observation/reservation ordering
must be implemented by the focused storage protocol and cannot stop an earlier
reservation from reaching GitHub afterward.

The finite model is exhaustive within its bounds, not an unbounded proof,
implementation test, cryptographic proof, database durability proof, fairness/
liveness guarantee without the assumptions below, or verification of GitHub.
Time and backoff are abstracted;
rate unavailability represents safe deferral, not rate-counter arithmetic.
Permanent outages may prevent successful dispatch. Safe-failure classification and atomic
database transitions are assumptions that implementation tests must validate.
The model does not prove exact workflow revision execution or exactly-once runs.

Run the official [TLC v1.7.4 release](https://github.com/tlaplus/tlaplus/releases/tag/v1.7.4)
with an existing Java runtime:

```sh
python3 scripts/check-webhook-gateway-model.py --jar /path/to/tla2tools.jar
python3 scripts/check-webhook-gateway-model.py --jar /path/to/tla2tools.jar \
  --config specs/tla/WebhookGateway.concurrent.cfg
python3 scripts/check-webhook-gateway-model.py --jar /path/to/tla2tools.jar \
  --config specs/tla/WebhookGateway.liveness.cfg
```

The runner fixes two TLC workers, breadth-first exhaustive exploration, finite
bounds, fingerprint polynomial, and seed; it fails unless TLC reports success
and an empty exploration queue. It writes model/config/tool digests, command,
state counts, summary evidence, and limitations to
[`tla/WebhookGateway.result.json`](tla/WebhookGateway.result.json).
The concurrent run writes
[`tla/WebhookGateway.concurrent.result.json`](tla/WebhookGateway.concurrent.result.json).
Downloaded tools, extracted standard modules, logs, and state files belong in
an automatically cleaned external work directory by default; `--work-dir` accepts
an explicit override outside the checkout as well. They MUST NOT be committed.
The recorded
result is evidence for the exact input hashes only.

`LivenessSpec` adds explicit weak fairness for Tick and, for each job, Reap and
Expire. The separate liveness configuration retains two workers/two jobs and
two pending slots, with a one-tick horizon and one claim generation to keep complete temporal cycle
checking small. `EventualSettlement` requires every admitted pending job
eventually to reach some terminal outcome. This is expiration/recovery
settlement, **not** eventual successful execution. It assumes clock advancement
and durable maintenance continue even while GitHub or worker processes are
unavailable. No fairness of sending, rate availability, or successful GitHub
responses is assumed. An unfair scheduler, stopped database maintenance, or
stalled clock invalidates this liveness argument.
[`tla/WebhookGateway.liveness.result.json`](tla/WebhookGateway.liveness.result.json)
records the actual bounded temporal check and its fairness assumptions.
The runner derives checked invariant/property names from each configuration,
not from an independent hardcoded list.

## 18. Implementation and validation gates

Sequence is design and executed TLC first; then provider-neutral contracts and
PostgreSQL conformance; then receiver and subscription approval; then isolated
dispatch with fresh grant checks; finally real compiler integration and opt-in
deployment. No queue implementation is authorized by a merely written model.
Changes to states, retry rules, fences, or invalidation ordering require model
updates and a fresh exhaustive result before persistence changes proceed.

Required tests include concurrent duplicate admission/capacity rollback; two
workers/two connections; stale result fencing; lease expiry and process restart;
all terminal states; safe retry and expiration; quota/auth unavailability;
revocation, removal, visibility changes and observation races; rename/transfer/
ID reuse; project cross-repository confidentiality; invalid metadata/approval;
cross-tenant isolation; signature/body bounds; and accepted-but-response-lost
ambiguity. PostgreSQL restart tests must use real persistence, not an in-memory
stub. Grant-race tests must document the guarantee they cannot establish.

A real end-to-end claim requires signed GitHub delivery, actual compiler-produced
metadata at an approved revision, real isolated deployments and PostgreSQL,
scoped installation credentials, a real workflow dispatch, and correlated
observations of acceptance/run behavior. Local fixtures, mocked API responses,
TLC, and compile success MUST NOT be presented as that end-to-end test.
This design/model change makes no such claim.

## 19. References and open dependencies

- [GitHub webhook validation](https://docs.github.com/en/webhooks/using-webhooks/validating-webhook-deliveries).
- [Webhook best practices](https://docs.github.com/en/webhooks/using-webhooks/best-practices):
  quick response, delivery identity, and asynchronous processing.
- [Webhook redelivery](https://docs.github.com/en/webhooks/testing-and-troubleshooting-webhooks/redelivering-webhooks):
  no assumption of automatic redelivery after every failed response.
- [Workflow dispatch REST API](https://docs.github.com/en/rest/actions/workflows#create-a-workflow-dispatch-event):
  endpoint permissions, ref, input contract, and versioned response. Retrieved
  2026-10-17: current documentation describes API `2026-03-10`, 25 input
  properties, and HTTP 200 with run identity; do not apply that contract silently
  to clients pinned to older API versions.
- [GitHub App installation tokens](https://docs.github.com/en/apps/creating-github-apps/authenticating-with-a-github-app/generating-an-installation-access-token).
- [Webhook event payloads](https://docs.github.com/en/webhooks/webhook-events-and-payloads).
- [Projects v2 item webhook payload](https://docs.github.com/en/webhooks/webhook-events-and-payloads#projects_v2_item):
  distinguish `project_node_id` from the item's `node_id`.
- [Projects field-change webhook example](https://github.blog/changelog/2024-06-27-github-issues-projects-graphql-and-webhook-support-for-project-status-updates-and-more/):
  includes `field_name` and single-select `from.name`/`to.name`; a narrower
  third-party JSON schema does not negate the published payload example.
- [Compiler webhook manifest proposal](https://github.com/github/gh-aw/pull/69772):
  versioned normalized declarations in the lock-file header; proposal state
  and exact inspected revision are recorded in section 6.
- [Repository dispatch REST API](https://docs.github.com/en/rest/repos/repos#create-a-repository-dispatch-event):
  a separate HTTP 204 transport with different event/payload limits.
- [GitHub App rate limits](https://docs.github.com/en/rest/using-the-rest-api/rate-limits-for-the-rest-api).
- [Existing operational contracts](server-operational-storage.md),
  [ingestion boundaries](server-ingestion.md), and
  [control architecture](control-architecture.md).

Open dependencies are an agreed approved-subscription/projection bridge and
immutable execution binding; approved App permissions and supported project-event capabilities on
each GitHub host; a trustworthy dispatch-ref execution contract; operational
retention/SLO budgets; and real deployment acceptance evidence. Absence of any
required dependency disables affected subscriptions rather than substituting
handwritten production metadata or broader authority.
