---
title: Central Agentic Ops Control Architecture Specification
description: Normative architecture for JSON-governed rollout and central authority over gh-aw workflows.
version: 1.0.0
status: Working Draft
editors:
  - GitHub Next
---

# Central Agentic Ops Control Architecture Specification

**Version:** 1.0.0  
**Status:** Working Draft  
**Latest Version:** https://github.com/githubnext/gh-cao/blob/main/specs/control-architecture.md
**Editors:** GitHub Next

## Abstract

This specification defines the Central Agentic Ops (CAO) control architecture and its JSON configuration model. CAO governs whether and where an installed GitHub Agentic Workflows (gh-aw) operation may run, including rollout and live authority. gh-aw governs how an authorized workflow executes, including engine limits, generated job topology, authentication, and safe-output execution. This specification defines the cumulative authority model, deterministic resolution lifecycle, conformance requirements, and compliance tests that preserve that boundary.

Implementers should read this specification after the explanatory
`docs/architecture.md` and before changing `.github/workflows/cao.json`,
`.github/workflows/shared/control.md`, or its policy resolver.

## Status of This Document

This document is a Working Draft and may be updated, replaced, or made obsolete. It is the canonical normative specification for the Central Agentic Ops control architecture; documentation under `docs/` is explanatory and MUST NOT override this specification. Implementations MUST identify the exact version against which they claim conformance. Sections 2 through 9 are normative. Section 1, examples, references, and the change log are informative unless they contain an explicit normative statement.

## Table of Contents

1. [Introduction](#1-introduction)
2. [Conformance](#2-conformance)
3. [Terminology](#3-terminology)
4. [Architecture and Authority](#4-architecture-and-authority)
5. [JSON Configuration](#5-json-configuration)
6. [Resolution Lifecycle](#6-resolution-lifecycle)
7. [Failure and Revocation](#7-failure-and-revocation)
8. [Compliance Testing](#8-compliance-testing)
9. [Security and Privacy Considerations](#9-security-and-privacy-considerations)
10. [Appendices](#10-appendices)
11. [References](#11-references)
12. [Change Log](#12-change-log)

## 1. Introduction

### 1.1 Purpose

CAO operates agentic workflows from a central control repository across an explicitly bounded set of target repositories. Campaigns are an optional orchestrator/worker packaging pattern, not a prerequisite for governed execution. This specification separates organizational operating authority from workflow execution capability so neither layer can silently grant authority owned by the other.

### 1.2 Scope

This specification covers:

- `.github/workflows/cao.json` as the sole persistent non-secret CAO policy authority;
- standalone workflow, campaign, and worker enablement, repository scope, rollout, and output-mode ceilings;
- deterministic policy resolution, narrowing, provenance, and revocation; and
- the authority boundary between CAO and gh-aw.

This specification does not define gh-aw workflow syntax, AI engine behavior, generated job implementation, credential formats, safe-output primitive semantics, or GitHub repository governance controls.

### 1.3 Design Goals

1. One reviewable, version-controlled JSON authority for CAO policy.
2. Fail-closed authorization before model invocation.
3. Deterministic narrowing to one effective run envelope.
4. No duplication of gh-aw execution controls in CAO policy.
5. Auditable control-policy provenance.
6. No compatibility path for legacy CAO policy variables.

### 1.4 Architectural Summary

```mermaid
flowchart LR
    C[Control policy] --> R[CAO resolver]
    D[Dispatch request] --> R
    R -->|deny or narrow| G[Compiled gh-aw workflow]
    G --> E[Engine and generated jobs]
    G --> A[Authentication]
    G --> S[Safe-output execution]
    S --> O[Review repository or live target]
```

CAO is an authorization and rollout layer. gh-aw is the execution layer. A run proceeds only through the intersection of both layers.

## 2. Conformance

### 2.1 Requirements Notation

The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT", "SHOULD", "SHOULD NOT", "RECOMMENDED", "NOT RECOMMENDED", "MAY", and "OPTIONAL" in this document are to be interpreted as described in [RFC 2119](https://www.ietf.org/rfc/rfc2119.txt).

### 2.2 Conformance Classes

1. **Conforming Policy Document:** A `.github/workflows/cao.json` document satisfying Section 5 and the published JSON Schema.
2. **Conforming CAO Resolver:** A deterministic implementation satisfying Sections 5 through 7 without granting gh-aw capabilities.
3. **Conforming gh-aw Integration:** A compiled workflow integration preserving gh-aw ownership of the execution concerns in Section 4.2.
4. **Conforming Control Repository:** A protected repository using a Conforming CAO Resolver and dispatching only within resolved authority.

An implementation is conforming only when it satisfies every MUST and MUST NOT applicable to its claimed class. A partially conforming implementation MAY report individual test results but MUST NOT claim conformance to that class.

### 2.3 Compliance Levels

| Level | Name | Required conformance |
| --- | --- | --- |
| 1 | Configuration | Conforming Policy Document |
| 2 | Controlled Execution | Level 1, CAO Resolver, gh-aw Integration, and Control Repository |
| 3 | Authorized Live Operation | Level 2 with explicit live scope in control policy |

Review-only operation MAY conform to Level 2. Live operation MUST NOT claim Level 3 unless every live target-campaign pair is explicitly authorized by control policy.

## 3. Terminology

**CAO:** Central Agentic Ops, the policy and rollout control layer defined here.

**gh-aw:** GitHub Agentic Workflows, the workflow compiler and execution framework governed by its source workflow contract.

**Control repository:** The repository from which CAO campaigns, policy, and workflow runs are operated.

**Target repository:** A repository admitted for analysis or mutation by a worker or standalone workflow run.

**Rollout authority:** CAO authority to enable standalone workflows, campaigns, and workers and bound repositories, modes, inventory slices, repository counts, rollout percentages, and cross-run admission.

**Execution capability:** A capability declared by gh-aw source, including an engine, per-run limit, tool, network host, permission, generated job, authentication mechanism, or safe-output primitive.

**Credential reach:** The repositories and GitHub API operations accessible through the job-local credential selected and provisioned by gh-aw. CAO may require sufficient reach for an admitted repository but does not select, mint, or propagate the credential.

**Effective envelope:** The immutable, least-permissive result of CAO policy, parent dispatch bounds when present, one-run narrowing, worker ceilings, credential reach, and compiled gh-aw capabilities.

**Persistent output mode:** The `review` or `live` ceiling stored in control
defaults, campaign policy, exact-target policy, or worker policy. `debug` is
never a persistent output mode.

**Run-scoped debug request:** A `safe_output_mode: debug` input supplied for one
manually launched worker run. It permits bounded analysis and staged output
proposals, not campaign rollout or repository-mutation authority.

## 4. Architecture and Authority

### 4.1 Mandatory Ownership Boundary

CAO governs **whether and where** an operation may run. gh-aw governs **how** an authorized workflow executes.

| Concern | CAO authority | gh-aw authority |
| --- | --- | --- |
| Enablement | Standalone workflow, campaign, and worker gates | Execution of an admitted workflow |
| Repository scope | Eligible owners, targets, and review destinations | Use of the admitted repository |
| Output mode | Campaign and worker review/live ceilings | Declared safe-output behavior for the effective mode |
| Rollout | Scan, partition, batch, percentage, repository-count, and monthly campaign admission | Execution of admitted runs |
| Live authority | Campaign, target, and worker live-mode ceilings | Execution only within the admitted mode and target |
| Engine limits | No authority | Engine, model, per-run turns, AI Credits, and timeout |
| Job topology | No authority | Generated jobs, dependencies, and runtime harness |
| Authentication | Require sufficient reach but store or mint no credentials | Credential selection, job-local token minting, and propagation |
| Safe outputs | Maximum mode, destination, and target boundary | Primitive declaration, validation, and execution |

**CAO-ARC-001:** CAO MAY deny a run or narrow its rollout or target scope.

**CAO-ARC-002:** CAO MUST NOT grant or expand an engine, model, per-run turn limit, per-run AI Credit limit, tool, network host, permission, generated job, credential, authentication mechanism, or safe-output primitive beyond the compiled gh-aw workflow.

**CAO-ARC-003:** A gh-aw execution capability MUST NOT be interpreted as CAO rollout or live authority.

**CAO-ARC-004:** CAO policy, credential reach, dispatch narrowing, and compiled gh-aw capabilities MUST be cumulative boundaries. No boundary substitutes for another.

### 4.2 gh-aw Execution Ownership

gh-aw source owns engine and model selection, `max-turns`, `max-ai-credits`, timeouts, tools, network access, permissions, generated job topology, authentication mechanics, and safe-output primitives.

**CAO-GHA-001:** CAO configuration and resolution MUST NOT define, override, or raise a gh-aw engine or native per-run limit.

**CAO-GHA-002:** The CAO resolver MUST execute as a contributed step in the existing gh-aw job topology. It MUST NOT add a parallel policy job, rewire generated dependencies, or replace gh-aw control flow.

**CAO-GHA-003:** CAO policy MUST NOT contain credentials or authentication bootstrap values. The resolver MUST NOT mint a second token or transport credentials through policy records, dispatch inputs, artifacts, prompts, or job outputs.

**CAO-GHA-004:** CAO MUST NOT synthesize, enable, or bypass a safe-output primitive absent from the compiled workflow. gh-aw MUST NOT execute a declared safe output outside the mode, destination, or target admitted by CAO.

### 4.3 Trust Sources

| Record | Trust source | Authority granted |
| --- | --- | --- |
| Control policy | Exact workflow commit on the protected control default branch | Rollout, repository, and live-mode authority |
| Dispatch request | GitHub Actions event payload | Request-only narrowing for one run |
| Credentials | GitHub Actions secrets or a run-scoped token | Authentication capability only |
| gh-aw source | Protected workflow source compiled by gh-aw | Execution capabilities and native limits |
| Effective envelope | Deterministic resolver output | Derived authorization for one run |

Possession of credentials MUST NOT imply rollout authority. Target repository files MUST NOT widen, narrow, or veto control policy.

### 4.4 Central Execution Topology

Orchestrators and workers execute from the private control repository. Target repositories provide data and receive declared safe outputs; they MUST NOT receive or execute the control repository's workflow definitions.

**CAO-EXE-001:** An orchestrator MUST discover, filter, rank, and select repositories only within its effective rollout envelope. It MUST dispatch only eligible workers declared by its compiled gh-aw workflow and MUST NOT perform target-repository mutation itself.

**CAO-EXE-002:** A worker MUST process exactly the admitted `target_repo`, supplied by its dispatcher or manual debug request. It MUST NOT perform organization-wide discovery, select another target, dispatch downstream workers, or escalate its effective mode.

**CAO-EXE-003:** Every orchestrator-to-worker dispatch MUST carry `target_repo`, `safe_output_mode`, `safe_output_repo`, `correlation_id`, `central_repo`, and `control_plane_run_url`; it MAY carry a campaign-specific `batch_label`. Its mode MUST be `review` or `live`, never `debug`. Credentials MUST NOT be included in this envelope.

**CAO-EXE-004:** A local debugger MAY request `debug` only by directly launching
one worker through `workflow_dispatch`. Admission MUST verify the authoritative
event's requested mode, target, destination, and human sender, require current
write, maintain, or admin permission on the control repository, and reject
bot callers or reruns by a different triggering actor. Unavailable evidence
MUST fail closed. GitHub does not attest the local origin of a CLI/API request;
human manual dispatch is the enforceable launch boundary.

Debug MUST retain exact-workflow-SHA policy validation, declared and enabled
campaign and worker identity, owner and repository scope, request limits,
credential reach, and API-capacity checks. It MUST require an explicit target
and matching output repository. Dispatcher correlation ID, central repository,
and originating-run URL inputs MUST be absent; they MUST NOT be synthesized.
Precompute MUST recheck debug launch eligibility and record the debug actor,
`launch_kind: manual-debug`, `safe_outputs_staged: true`, the actual control
repository, and empty dispatcher correlation fields.

Debug applies only to the individually requested worker run. A campaign MUST
NOT be configured, scheduled, enabled, or rolled out in debug mode. Debug MUST
NOT be a persistent policy value, an orchestrator mode, or live mutation
authority. Compiled shared control MUST force global gh-aw safe-output
staging and suppress automatic activation and failure issues for debug runs.
Repository-memory staging remains a gh-aw capability, not a CAO job replacement.

The orchestrator is the rollout decision point. Each worker is an independent enforcement point that revalidates the envelope before execution.

### 4.5 Standalone Workflow Execution

**CAO-EXE-005:** An agentic workflow MAY participate directly in CAO without a
campaign, orchestrator, worker, or campaign installation record. It MUST import
shared control as `standalone`, declare its exact compiled workflow slug, expose
the pre-activation authorization output, and gate activation on successful
admission and precompute. The resolver MUST match that identity against
`GITHUB_WORKFLOW_REF` and resolve `control-plane.workflows.<slug>` at the exact
workflow SHA. Undeclared or disabled workflows MUST NOT activate.

A standalone run MUST receive the same policy validation, scope, request
narrowing, and API-capacity checks as an orchestrator. Its precompute MUST use
the same bounded inventory and deterministic cell/batch partitioning. It MUST
exclude archived and disabled repositories and cap the admitted deterministic
subset by `max-repositories` and `rollout-percent`; each admitted target MUST
carry its own effective mode and output destination. An empty set permits only
no-op execution; unavailable required inventory evidence MUST fail closed.

The workflow MAY perform its mission directly across that set, but MUST NOT
discover additional repositories, dispatch downstream work, or carry a
dispatcher envelope. Review targets MUST route proposals to the admitted review
repository; live targets MUST route target writes only to that target through
declared safe outputs. Workflow `max-mode`, when present, MUST narrow every
target's mode. Credential reach, tools, and safe-output capabilities remain
gh-aw-owned cumulative boundaries; CAO MUST NOT expand them for a fleet run.

### 4.6 Direct Operations and Authority Envelopes

**CAO-EXE-006:** A direct operation MAY execute an explicitly requested target
set without campaign/worker records or inventory topology. It MUST declare its
exact workflow slug under `control-plane.operations`, import shared operation
control, and retain the pre-activation authorization output and activation gate.
Workflow identity MUST match the compiled workflow reference. Identity conflicts
with campaign, worker or standalone declarations MUST reject.

The role-independent authority kernel MUST perform no discovery, credential
acquisition, external requests or dispatch. Requests and verified execution
context MUST be separate. V1 envelopes MUST pair each target, mode and output
destination and bind grants to the control repository, policy SHA, workflow
reference, run ID and attempt. Unsupported versions or required enforcement
controls MUST reject. A request or envelope MUST NOT grant gh-aw capabilities.

Direct precompute MUST re-resolve exact-revision policy, compare the admitted
envelope with the current request and execution binding, and reject scope or
destination widening. Required repository identity, archive/disable and
destination evidence MUST be checked before execution. Redirects, duplicate
targets and missing evidence MUST fail closed. Multiple targets MUST be
review-only with a common destination. Live MUST use one explicit `target_repo`
and target-bound compiled safe outputs. Direct operations MUST NOT discover
additional targets, carry dispatcher envelopes or delegate work. Narrowing
comparisons MUST NOT be treated as provenance verification or authorization.
Request-only `debug` remains restricted to manual single-target workers.

## 5. JSON Configuration

### 5.1 Location and Validation

**CAO-CFG-001:** Persistent non-secret CAO policy MUST exist only at `.github/workflows/cao.json`.

**CAO-CFG-002:** The file MUST be UTF-8 JSON with an object root and `version` equal to integer `1`.

**CAO-CFG-003:** The document MUST contain at least one of `control-plane` or `target-authority` and MUST conform to `.github/workflows/shared/cao.schema.json`, based on JSON Schema Draft 2020-12.

**CAO-CFG-004:** Unknown properties, duplicate object keys, malformed campaign, worker, or workflow identifiers, and GitHub Actions expressions MUST be rejected.

**CAO-CFG-005:** Implementations MUST NOT read `CENTRAL_AGENTIC_OPS_*` Actions variables as policy defaults, overrides, aliases, or compatibility fallbacks.

**CAO-CFG-006:** A control repository MUST declare `gh-aw-version` as a `v`-prefixed semantic version. Infrastructure that installs the gh-aw CLI MUST use this value as its compiler-version source of truth. The field does not grant execution capability or rollout authority.

The `$schema` property SHOULD identify `https://raw.githubusercontent.com/githubnext/gh-cao/main/.github/workflows/shared/cao.schema.json`.

### 5.2 Document Roles

A control repository uses `control-plane`. A target repository uses `target-authority`. A repository serving both roles MAY contain both. The document MUST NOT contain secrets, private keys, access tokens, or authentication headers.

### 5.3 Control-Plane Model

| Property | Purpose | Omission behavior |
| --- | --- | --- |
| `scope` | Eligible owners and optional repository restriction | Resolver safe defaults |
| `inventory` | Scan, cell, and batch ceilings | Schema defaults |
| `web` | Presentation settings for deterministic web surfaces | Campaignd defaults |
| `defaults` | Mode, repository, and rollout defaults | Schema defaults |
| `campaigns` | Explicit campaign and worker workflow declarations | Undeclared campaigns and workers are disabled |
| `workflows` | Standalone agentic workflows keyed by exact compiled workflow slug | Undeclared workflows are disabled |
| `operations` | Direct explicit-target operations keyed by exact compiled workflow slug | Undeclared operations are disabled |
| `publishing` | Deterministic reviewed-output publication | Disabled |

`allowed-owners` and `allowed-repositories` are cumulative when both are non-empty. Wildcards MUST NOT be accepted in version 1.

| Inventory property | Constraint |
| --- | --- |
| `max-scan-repositories` | Integer from 1 through 100000 |
| `cell-count` | Integer from 1 through 1000 |
| `cell-index` | Non-negative integer smaller than `cell-count` |
| `batch-size` | Integer from 1 through 100000 |
| `batch-index` | Non-negative integer |

Inventory partitioning MUST be deterministic for the same inventory and effective values.

The optional `web.favicon` value MUST be an absolute HTTPS URL without credentials, query, or fragment, or a non-traversing `./` relative path. Web presentation settings MUST NOT grant or widen rollout authority.

The optional `web.host` object declares non-secret dashboard hosting
modules and Redis connection indirection. It MUST NOT contain a Redis URL,
password, certificate, token, or other credential value. Environment references
MUST be uppercase environment-variable names. A host declaration MUST compose
exactly one target module with exactly one Redis provider module. Target modules
define authentication, listener ownership, HTTPS, and platform-proxy trust.
Redis modules define environment mappings, replica and collection constraints,
session semantics, process namespace isolation, and verified TLS inputs.

Provider modules under `web.host.redis.module` only select environment-variable
defaults and consistency capabilities. They MUST resolve into the same generic
Redis connection model and
MUST NOT grant authority, embed credentials, disable certificate verification,
or introduce provider-specific request handling. An invalid or internally
inconsistent host declaration MUST fail policy validation or server startup.
Target and Redis modules MUST be reviewed, compiled registry entries rather than
code loaded from policy or another runtime path. Fixed module capabilities MUST
NOT be overridden by policy. Module composition MUST complete and be validated
before the server accepts traffic.
Hosted server implementations MUST require the modular `web.host` declaration
and MUST reject the earlier flat host-capability shape, `redis.preset`, and
environment-only host selection.

#### 5.3.1 Deployment-specific host extensions

A control repository MAY keep a deployment-specific JSON document alongside
`.github/workflows/cao.json` and load it explicitly for a hosted dashboard.
Such a document is not a second rollout policy and MUST NOT be used to
authorize an orchestrator or worker. It MUST contain `extends` and
`control-plane.web.host`; its only permitted root keys are `extends` and
`control-plane`, whose only permitted nested keys before `host` are `web` and
`host`, respectively. It MUST NOT set scope, inventory, defaults, campaigns,
target authority, publishing, credentials, or other policy fields. The host
value MUST satisfy the same requirements as `web.host` in Section 5.3.

`extends` MUST identify a non-empty relative path. Imports MUST resolve
relative to the importing file, remain within the entry document's directory
after resolving symlinks, and reject cycles and chains deeper than eight
imports. Duplicate JSON keys and GitHub Actions expressions MUST be rejected.
The host extension is merged into its base document, preserving all
non-host fields; the composed policy MUST be validated before dashboard use.
Missing files, invalid imports, disallowed keys, or invalid composition MUST
fail closed. Only `.github/workflows/cao.json` at the reviewed workflow SHA
grants rollout and target authority, regardless of which host extension the
dashboard loads.

| Campaign property | Constraint | Authority type |
| --- | --- | --- |
| `mode` | `review` or `live` | Output-mode ceiling |
| `max-repositories` | Integer from 1 through 1000 | Rollout ceiling |
| `rollout-percent` | Integer from 1 through 100 | Rollout ceiling |
| `monthly-ai-credit-budget` | Deprecated compatibility field; no runtime admission effect | None |

**CAO-CFG-007:** Persistent mode fields MUST accept only `review` or `live`.
The resolver MUST reject `debug` in `control-plane.defaults.mode`,
`control-plane.campaigns.<campaign>.mode`,
`control-plane.campaigns.<campaign>.targets.<repository>.mode`, and
`control-plane.campaigns.<campaign>.workers.<worker>.max-mode`. There MUST NOT
be a campaign-level debug enablement flag, inherited debug default, or policy
override that converts normal campaign dispatches into debug runs.

`monthly-ai-credit-budget` MUST NOT replace, raise, or reinterpret gh-aw `max-ai-credits` or `max-turns`. CAO accepts the field for compatibility with existing policy files, but the resolver MUST NOT read month-to-date AI Credit usage or use the value to admit, deny, or cap repositories. Native gh-aw per-run limits remain cumulative.

An absent campaign is disabled. A campaign's `workers` map declares its worker identities and exact workflow slugs; undeclared workers are disabled. Every worker requires `workflow`, defaults to enabled, may set `enabled: false`, and may set `max-mode` only to narrow the resolved campaign or exact-target mode. Campaign, worker, and workflow identifiers use lowercase kebab-case, and workflow identities must be unique within a campaign.

An entry under `control-plane.workflows` MAY declare `enabled`, `mode`,
`max-mode`, `max-repositories`, `rollout-percent`, `targets`, and the deprecated
`monthly-ai-credit-budget` compatibility field. It MUST NOT contain campaign
workers or execution capabilities. Its key MUST be the exact lowercase
kebab-case workflow slug, not a display name or campaign alias. Enablement
defaults to true for declared workflows; `max-mode` is an optional ceiling
applied after default and exact-target modes. Target policies MUST satisfy the
same global repository and owner scope constraints as campaign targets.
Persistent modes, including `max-mode` and target modes, MUST be `review` or
`live`, never `debug`. A standalone import MUST NOT also declare campaign or
worker identities; campaign roles MUST NOT use standalone policy identity.

An entry under `control-plane.operations` MAY contain `enabled`, `mode`,
`max-mode`, `max-repositories` and `targets` only. Global mode and repository-cap
defaults apply when absent. Fleet topology and execution capabilities MUST NOT
be added to this declaration. Declared operations default to enabled, with
review and one repository as the base defaults; examples SHOULD explicitly
disable operations pending review. Target entries MUST satisfy global scope.
Bare setup MUST reject declared operations before changing scope or credentials.

### 5.4 Precedence and Narrowing

Persistent values resolve in this order:

```text
schema defaults < control-plane.defaults < campaign or standalone workflow policy < exact target policy < optional worker or standalone workflow ceiling
```

Dispatch values are intersected with the persistent result. They are requests and MUST NOT become persistent authority.

| Dispatch input | Permitted effect |
| --- | --- |
| `safe_output_mode` | Preserve `review` or `live`, or lower `live` to `review` |
| `target_repo` | Select one authorized repository |
| `safe_output_repo` | Select one authorized review destination |
| `max_repos` | Lower the repository ceiling |
| `rollout_percent` | Lower the rollout percentage |
| Cell and batch inputs | Select a valid partition within the authorized universe |

**CAO-CFG-006:** A widening dispatch request MUST fail with a stable policy reason. It MUST NOT be silently accepted, persisted, or converted into broader authority.

The table governs ordinary dispatcher requests. A direct, single-worker debug
request is the separate execution case in CAO-EXE-004, not a third campaign
mode or an additional value in the rollout-mode ordering. It MUST NOT modify
persistent policy, carry a parent dispatcher envelope, or authorize subsequent
runs. It uses the admitted target as its staged output destination rather than
resolving a review repository.

When no `target_repo` input is supplied, the resolver MUST use the workflow's
compiled `CAO_DEFAULT_TARGET_REPOSITORY` value, when non-empty, as the requested
target for both policy resolution and the runtime context. An explicit
`target_repo` input takes precedence. The default is a request, not authority:
it MUST pass the same target validation and policy intersection as a dispatched
`target_repo`, and an empty value MUST leave the request unset. A scheduled
workflow MAY set it to its own repository to scope inventory to that
repository.

## 6. Resolution Lifecycle

### 6.1 Resolver Requirements

Every operational orchestrator and worker MUST import the shared CAO control component before model invocation.

**CAO-RES-001:** The resolver MUST use dependency-free Node.js and MUST NOT execute code fetched from a control or target repository.

**CAO-RES-002:** Campaign, role, and worker identifiers MUST come from static workflow source, not dispatch inputs, environment variables, repository content, or display names.

**CAO-RES-003:** Control policy MUST be fetched at the exact `github.workflow_sha` used by the running workflow.

### 6.2 Resolution Sequence

The resolver MUST:

1. record the workflow revision;
2. fetch, parse, and validate control policy at that revision;
3. resolve static campaign, role, and worker identity;
4. resolve defaults, enablement, scope, rollout, inventory, and mode ceilings;
5. intersect dispatch-request narrowing;
6. verify that the job-local credential supplied by gh-aw can access the admitted repositories and required APIs, without selecting, minting, or storing that credential in CAO policy;
7. write effective policy and provenance; and
8. emit native gh-aw `noop` for expected denial or continue through normal gh-aw execution.

### 6.3 Effective Record

The resolver MUST write exactly one derived record to `/tmp/gh-aw/agent/control-precompute.json`. Every authorized record MUST include authorization status and reason, workflow or campaign identity, role, worker when applicable, effective mode and routing, control repository, workflow and policy commit SHA, lowercase SHA-256 policy digest, schema version, and resolution time.

An orchestrator record MUST additionally include inventory version and batch identity; configured and effective repository and rollout caps; repository discovery status; the bounded candidate repositories; and eligible worker workflows with skip reasons. A worker record MUST additionally include the target, worker enablement and mode ceiling, and the standard dispatch envelope. A manual debug worker MUST instead use the run-specific provenance in CAO-EXE-004: `safe_output_mode: debug`, `launch_kind: manual-debug`, `debug_actor`, and `safe_outputs_staged: true`, with the actual control repository and empty dispatcher correlation fields. Its candidate and downstream-worker lists MUST be empty.

A standalone record MUST identify `control_role: standalone` and `workflow`,
leave campaign and worker identities empty, and include inventory/batch
provenance, effective repository and rollout caps, and only the admitted
`candidate_repositories`, each with `safe_output_mode` and `safe_output_repo`.
Its `worker_workflows` list MUST be empty.

Consumers MUST treat the record's `candidate_repositories`, `worker_workflows`, `effective_max_repos`, `safe_output_mode`, and `safe_output_repo` fields as authoritative. They MUST NOT reconstruct those values from workflow inputs.

The record MUST NOT be treated as persistent configuration. Environment variables MAY transport derived values, but changing them MUST NOT alter the validated record.

### 6.4 Worker Revalidation

A worker MUST treat parent policy data as provenance, not current authority, and independently resolve current policy after dispatch and before model invocation. Its authority is the least permissive intersection of the parent envelope, current CAO policy, worker ceilings, credential reach, and compiled gh-aw workflow.

A newer policy MAY revoke or narrow an outstanding dispatch before worker execution. It MUST NOT widen the dispatched envelope. Continuous policy polling during model or safe-output execution is not required; immediate revocation after worker precomputation requires workflow cancellation, credential revocation, or another external execution control.

A manual debug worker has no parent envelope. It MUST independently resolve
policy at its exact workflow SHA and recheck its human launch eligibility
during precompute; it MUST NOT infer parent authority or fabricate an
orchestrator run to satisfy ordinary dispatch requirements.

## 7. Failure and Revocation

The resolver MUST fail closed for missing or invalid policy, unknown identities, invalid bounds, widening requests, unauthorized destinations, or unavailable required authentication.

An expected denial MUST write a stable reason to the effective record, emit gh-aw's native `noop`, and perform no model invocation. An integrity failure MUST fail visibly before model execution and MUST NOT print secrets or unnecessary untrusted content.

Operators MAY revoke authority through policy narrowing or credential revocation. Workers MUST revalidate current policy so revocation after orchestration can stop pending work. Workflow disablement, run cancellation, and organization Actions policy are emergency execution controls, not alternative CAO policy stores.

## 8. Compliance Testing

### 8.1 Test Procedure

A compliance suite MUST record the implementation revision and claimed level, use isolated fixtures, test permitted and denied cases, verify effective provenance, verify that denial causes no model invocation, and report every applicable test as passed or failed.

### 8.2 Required Tests

| Test ID | Requirement | Expected result | Level |
| --- | --- | --- | --- |
| T-CFG-001 | Validate a minimal control document | Accepted | 1 |
| T-CFG-002 | Add unknown properties, duplicate keys, expressions, or unknown identifiers | Rejected | 1 |
| T-CFG-003 | Supply only legacy CAO variables | Ignored as policy and denied | 1 |
| T-CFG-004 | Put execution fields such as engine, `max-ai-credits`, permissions, or safe outputs in CAO JSON | Rejected | 1 |
| T-CFG-005 | Set `debug` in a default, campaign, exact-target, or worker-ceiling mode field | Rejected; persistent modes remain `review` and `live` | 1 |
| T-ARC-001 | Use broad credentials against a target outside CAO scope | Denied | 2 |
| T-ARC-002 | Declare gh-aw capabilities without CAO live policy | No rollout or live authority inferred | 2 |
| T-ARC-003 | Configure deprecated monthly admission while a per-run gh-aw limit remains available | Monthly value ignored; native limit unchanged | 2 |
| T-EXE-001 | Inspect an orchestrator dispatch and worker run | Standard envelope present, credentials absent, worker bound to one target | 2 |
| T-EXE-002 | Manually launch one enabled, in-scope worker with `debug`, matching target/output repositories, and an authorized human actor | Admitted without dispatcher provenance; no campaign policy changed | 2 |
| T-EXE-003 | Request debug for an orchestrator, non-manual event, bot, different rerun actor, or actor without verifiable control-repository write access | Denied before model invocation | 2 |
| T-EXE-004 | Supply dispatcher correlation fields or event-mismatched mode, target, or destination for a debug run | Denied; no fabricated dispatcher envelope | 2 |
| T-EXE-005 | Request debug for an undeclared or disabled campaign/worker, out-of-scope target, or raised request limit | Denied; debug does not bypass policy | 2 |
| T-RES-001 | Resolve when workflow SHA differs from latest default branch | Policy at workflow SHA used | 2 |
| T-RES-002 | Request live under a review ceiling | Rejected as widening | 2 |
| T-RES-003 | Request lower rollout limits | Accepted as narrowing | 2 |
| T-RES-004 | Narrow policy after dispatch but before worker start | Worker uses narrower policy | 2 |
| T-RES-005 | Broaden policy after dispatch but before worker start | Worker remains bounded by parent envelope | 2 |
| T-RES-006 | Trigger expected denial | Effective record and native `noop`; no model invocation | 2 |
| T-RES-007 | Inspect authorized orchestrator and worker records | Required role-specific fields and provenance present | 2 |
| T-RES-008 | Inspect a manual debug worker handoff and revoke actor write access before precompute | Run-specific debug provenance when admitted; denied after revocation; no candidate or downstream-worker discovery | 2 |
| T-GHA-001 | Inspect generated workflow topology and authentication | Native gh-aw jobs and token path remain authoritative | 2 |
| T-GHA-002 | Request an undeclared safe output | Primitive unavailable regardless of CAO mode | 2 |
| T-GHA-003 | Resolve review mode for a live-capable workflow | No target mutation | 2 |
| T-GHA-004 | Compile a debug-capable worker and inspect its output execution boundary | Global gh-aw safe-output staging forced for debug; automatic activation/failure issues suppressed; no new primitive or live authority | 2 |
| T-SEC-001 | Compile an operational workflow without a deployment environment | Accepted; generated jobs have no deployment environment and credentials remain secret references | 2 |

### 8.3 Compliance Checklist

| Requirement group | Test IDs | Level | Status |
| --- | --- | --- | --- |
| JSON document and schema | T-CFG-001 through T-CFG-005 | 1 | Required |
| One-way authority boundary | T-ARC-001 through T-ARC-003 | 2 | Required |
| Central execution topology | T-EXE-001 through T-EXE-005 | 2 | Required |
| Deterministic resolution | T-RES-001 through T-RES-008 | 2 | Required |
| gh-aw execution ownership | T-GHA-001 through T-GHA-004 | 2 | Required |
| Credential and source boundary | T-SEC-001 | 2 | Required |
| Live control authority | T-RES-001 through T-RES-007 | 3 | Required |

## 9. Security and Privacy Considerations

Policy and workflow source MUST be protected on the control repository's default branch. Operational credentials MUST use GitHub Actions secrets and variables scoped to the control repository or its organization and MUST follow least-privilege repository access. Normal operational workflows MUST NOT require a GitHub Actions deployment environment; gh-aw-generated jobs MUST retain secret references without creating an implicit deployment boundary.

Implementations MUST use least-privilege credentials, MUST NOT expose secrets in effective records or logs, and MUST NOT execute fetched policy as code. JSON MUST be parsed with a structured parser; `eval` or equivalent interpretation is prohibited.

A job-local GitHub App installation token MUST be minted with its `repositories` scope bound to the single admitted repository for its purpose: the dispatched `target_repo` for a read credential, and `safe_output_repo` (falling back to the control repository only when no admitted destination is resolved) for a write credential used by safe outputs. An implementation MUST NOT default App token scope to the control repository when a different repository is admitted, and MUST NOT request installation-wide (`repositories: ["*"]`) access as a substitute for correct scoping.

Review mode SHOULD be the default. Live execution requires the cumulative intersection of CAO live authorization, worker live capability, sufficient credentials, and declared gh-aw safe outputs.

The effective record SHOULD contain only identifiers and provenance required for authorization and audit. It MUST NOT contain prompt content, repository source, credentials, or unrelated personal data. Access and retention SHOULD follow the operator's security and data-retention policies.

## 10. Appendices

### Appendix A: Examples

#### A.1 Minimal Review Policy

```json
{
  "$schema": "https://raw.githubusercontent.com/githubnext/gh-cao/main/.github/workflows/shared/cao.schema.json",
  "version": 1,
  "control-plane": {
    "scope": {
      "allowed-repositories": ["acme/payments-api"]
    },
    "campaigns": {
      "dependabot": {
        "workers": {
          "update-planner": {
            "workflow": "dependabot-update-planner"
          }
        }
      }
    }
  }
}
```

#### A.2 Non-Conforming Capability Expansion

The following is invalid because these execution capabilities belong to gh-aw and are absent from the CAO schema:

```json
{
  "version": 1,
  "control-plane": {
    "engine": "copilot",
    "max-ai-credits": 500,
    "permissions": { "contents": "write" },
    "safe-outputs": { "create-pull-request": {} }
  }
}
```

#### A.3 Single-Run Debug Request

This is a manual worker's `workflow_dispatch` input object, not a campaign
policy document or an orchestrator dispatch:

```json
{
  "target_repo": "acme/payments-api",
  "safe_output_repo": "acme/payments-api",
  "safe_output_mode": "debug"
}
```

It is admitted only under CAO-EXE-004. Dispatcher correlation inputs are
omitted. Writing the same mode into a campaign's `cao.json` entry is invalid
under CAO-CFG-007.

### Appendix B: Resolution Outcomes and Errors

The following stable `reason` values are produced by the version 1 policy resolver. They are authorization outcomes, not exception codes.

| Reason | Meaning | Required behavior |
| --- | --- | --- |
| `authorized` | The campaign and, where applicable, installed worker passed initial policy resolution | Continue remaining authorization checks |
| `control-plane-absent` | The document contains no control-plane policy | Deny and emit native `noop` |
| `campaign-undeclared` | The requested campaign is absent | Deny and emit native `noop` |
| `campaign-disabled` | The requested campaign is explicitly disabled | Deny and emit native `noop` |
| `worker-disabled` | The requested worker is explicitly disabled | Deny and emit native `noop` |

Invalid JSON, schema violations, unknown static identities, widening requests, unavailable required evidence, and target-authority integrity failures are fatal policy errors. A conforming implementation MUST fail before model invocation for these errors. It SHOULD report a stable, non-secret diagnostic, but this specification does not standardize exception message text.

## 11. References

### 11.1 Normative References

- **[RFC 2119]** Bradner, S. [Key words for use in RFCs to Indicate Requirement Levels](https://www.ietf.org/rfc/rfc2119.txt). March 1997.
- **[JSON]** Bray, T. [The JavaScript Object Notation Data Interchange Format](https://www.rfc-editor.org/rfc/rfc8259). RFC 8259, December 2017.
- **[JSON Schema 2020-12]** [JSON Schema Core](https://json-schema.org/draft/2020-12/json-schema-core) and [Validation](https://json-schema.org/draft/2020-12/json-schema-validation).
- **[CAO Schema]** [Central Agentic Ops Policy Schema](../.github/workflows/shared/cao.schema.json).

### 11.2 Informative References

- **[CAO Architecture]** [Architecture](../docs/architecture.md).
- **[CAO Configuration]** [Configure the Control Plane](../docs/configuration.md).
- **[gh-aw]** [GitHub Agentic Workflows](https://github.com/github/gh-aw).
- **[Semantic Versioning]** [Semantic Versioning 2.0.0](https://semver.org/spec/v2.0.0.html).

## 12. Change Log

### Version 1.0.0 (Working Draft)

- Defined the JSON-governed CAO control architecture.
- Assigned rollout and target authority to CAO.
- Assigned engine limits, generated job topology, authentication, and safe-output execution to gh-aw.
- Defined the central orchestrator and worker execution contract, credential reach, runtime records, deprecated monthly budget compatibility, revalidation timing, and protected environment boundary.
- Required job-local GitHub App installation tokens to be scoped to the single admitted target or review repository rather than defaulting to the control repository or an installation-wide wildcard.
- Added conformance classes, compliance levels, tests, examples, and security and privacy considerations.
- Defined the workflow-supplied default target request that applies when no `target_repo` input is given.
- Defined single-run manual worker debug admission, rejected campaign-level debug policy and dispatches, and added provenance and forced-staging compliance cases.