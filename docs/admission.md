---
title: Admission Gates
description: Understand what Central Agentic Ops checks before activation and what authorized-run precompute checks before agent execution.
---

Central Agentic Ops admits a run only when its checked-in control policy authorizes the workflow identity and requested limits. Admission happens in the gh-aw pre-activation job, before activation and before any agent executes.

The hosted Go server also supports the separate
[gh-aw cooperative activation-admission protocol](admission-gate.md)
at `POST /api/admission`. That optional OIDC-authenticated cost gate does not
replace the control-policy checks described here and cannot enable campaigns,
widen target scope, or grant writes.

Once that authority is deployed and the repository is explicitly enrolled,
configure the participating workflow's repository variables with the native CLI:

```sh
# Preview without changing variables.
gh cao setup admission --repo OWNER/REPO --url https://cao.example.org/api/admission --dry-run
gh cao setup admission --repo OWNER/REPO --url https://cao.example.org/api/admission
gh cao admission status --repo OWNER/REPO --json
gh cao admission disable --repo OWNER/REPO --dry-run
```

`gh cao setup admission` configures non-secret variables; no workflow secret is
needed for OIDC authentication.
Setup and disable apply changes unless `--dry-run` is supplied.
Setup uses the URL as the default audience and defaults outages to `block`.
After initial setup, omit `--url` to reuse the target's configured server or
the current control repository's configured server for fleet rollout/recovery.
See [Set up the admission gate](admission-gate.md) for authority preparation,
local readiness checks, a live canary, rollout, and troubleshooting.
`--audience` and `--on-outage ignore` are explicit overrides. Status includes
inherited organization settings; disable shadows them with an empty repository
URL instead of deleting the override. These commands neither deploy the
authority nor enroll repositories, compile workflows, or alter CAO policy.

For bulk rollout, replace `--repo` with one of these mutually exclusive selectors:

```sh
gh cao setup admission --all-repos --url https://cao.example.org/api/admission --dry-run
gh cao setup admission --org acme --org engineering --url https://cao.example.org/api/admission --dry-run
gh cao setup admission --enterprise company --url https://cao.example.org/api/admission --dry-run
```

Remove `--dry-run` to apply. `--all-repos` uses the validated local CAO global
scope: exact repository allowlists, or the visible repository inventory of each
allowed organization. Personal owners require exact repository lists.
`--org` provisions variables with visibility **all**, including future
repositories. Enterprise rollout enumerates organizations and configures each
organization's variables; there is no enterprise variable scope. Existing
repository overrides still take precedence. Enterprise read access does not
substitute for organization Variables write permissions.

`status` and `disable` accept the same selectors. Bulk preflight reads all
targets before mutation; write failures report completed targets and stop
without automatic retry or rollback. The limit is 1000 targets per command.

```text
trigger -> pre-activation admission -> authorized-run precompute -> activation -> agent
                 | denied              | blocked
                 +----------------------+-> no agent execution
```

## CAO versus gh-aw: differential analysis

This comparison is anchored to CAO revision
[`1d0ee7f5813c60078045506827bde4a262b3e645`](https://github.com/githubnext/gh-cao/tree/1d0ee7f5813c60078045506827bde4a262b3e645)
and its [policy pin](https://github.com/githubnext/gh-cao/blob/1d0ee7f5813c60078045506827bde4a262b3e645/.github/workflows/cao.json#L1-L4):
**gh-aw v0.92.6**, tag commit
[`7363a87ff2483635af62f490b52c25a757a50221`](https://github.com/github/gh-aw/tree/7363a87ff2483635af62f490b52c25a757a50221).
Upstream `main` supplies authoring guidance, not evidence that this pinned
compiler implements newer behavior. The repository is both a catalog and a
source-managed control plane; the comparison does not change its live policy.

CAO owns whether and where installed operations run. gh-aw owns how an
authorized execution runs. The effective authority is their **intersection**,
not a choice between two alternative admission systems.

| Gate and owner | Phase and authoritative inputs | Output and denial semantics | Duplication assessment |
| --- | --- | --- | --- |
| gh-aw trigger/actor eligibility | `pre_activation`; event provenance, configured roles/bots, repository permission evidence | Membership result feeds `activated`; denied or unverifiable membership cannot activate. Scheduled events have their own trusted-event path. | Not CAO campaign or target authorization. A permitted App actor still needs CAO policy. [Runtime](https://github.com/github/gh-aw/blob/7363a87ff2483635af62f490b52c25a757a50221/actions/setup/js/check_membership.cjs#L202-L310). |
| gh-aw scheduling/work predicates | `pre_activation`; configured stop time, rate/cooldown history, skip queries, command position and role/bot exclusions | Independent Boolean checks are conjoined into `activated`; false suppresses activation. These checks exist only when configured. | Workflow frequency and relevance, not CAO fleet rollout percentage or inventory partitioning. [Compiler](https://github.com/github/gh-aw/blob/7363a87ff2483635af62f490b52c25a757a50221/pkg/workflow/compiler_pre_activation_job.go#L397-L468). |
| CAO policy admission | Custom steps inside `pre_activation`; exact workflow-SHA control policy, imported role/identity, narrowing-only request inputs | `authorized`, reason, effective policy and admission evidence; denial yields false without running precompute. Runtime/policy absence fails closed. | Unique control-repository authority: standalone/campaign/worker enablement, review/live ceiling and fleet limits. [Resolver](https://github.com/githubnext/gh-cao/blob/1d0ee7f5813c60078045506827bde4a262b3e645/.github/workflows/shared/policy.mjs#L887-L1040), [admission](https://github.com/githubnext/gh-cao/blob/1d0ee7f5813c60078045506827bde4a262b3e645/.github/workflows/shared/control.mjs#L460-L541). |
| CAO request-only debug exception | Admission **and** precompute; authoritative manual event, original human actor, current write/maintain/admin access, exact policy scope, no dispatcher envelope | Denies missing/mismatched evidence; permits only a single target with gh-aw staging. A permission withdrawal between phases blocks the handoff. | Intentional extra restriction: native bot/role eligibility does not prove original-human debug intent or staging. Preserve both checks. [Debug validator](https://github.com/githubnext/gh-cao/blob/1d0ee7f5813c60078045506827bde4a262b3e645/.github/workflows/shared/control.mjs#L371-L420), [staging](https://github.com/githubnext/gh-cao/blob/1d0ee7f5813c60078045506827bde4a262b3e645/.github/workflows/shared/control.md#L82-L85). |
| CAO target/routing precompute | After admission, still before `activation`; admitted scope/ceilings plus inventory, repository access, private-review metadata, installed worker availability and dispatch envelope | Bounded `control-precompute.json`; invalid scope/routes/envelopes throw, capacity recovery emits false. Standalone required-inventory failure blocks; orchestrators can retain an explicit inventory error with no usable targets. | Scope and destination authorization are not gh-aw tool capability checks. Worker policy revalidation is independent of the orchestrator's selection; target files never authorize activation. [Precompute](https://github.com/githubnext/gh-cao/blob/1d0ee7f5813c60078045506827bde4a262b3e645/.github/workflows/shared/control.mjs#L695-L764), [inventory](https://github.com/githubnext/gh-cao/blob/1d0ee7f5813c60078045506827bde4a262b3e645/.github/workflows/shared/control.mjs#L955-L1040). |
| CAO GitHub REST capacity | Admission samples `/rate_limit`; precompute handles subsequent rate-limit failures | Available/limited/unavailable evidence and reset guidance; insufficient or unverifiable capacity prevents activation. | Not gh-aw AI Credits, cooldown, or dispatch-count limits. It is a point-in-time readiness check, not a reservation or cross-run quota. Admission currently samples the workflow token; precompute may use a different App/PAT credential. Do not infer the latter's capacity from the former. [Credential wiring](https://github.com/githubnext/gh-cao/blob/1d0ee7f5813c60078045506827bde4a262b3e645/.github/workflows/shared/control.md#L213-L312). |
| gh-aw execution readiness and policy | `activation`; compiled engine/output secret requirements, OAuth-token checks, strict-mode policy, lock/source integrity and compiler-version checks | Validation failures block downstream execution or expose failure outputs used by generated jobs; optional checks retain their documented exceptions. | CAO's exact-SHA policy read is not a replacement for compiled-source integrity, engine authentication or safe-output credential validation. [Assembly](https://github.com/github/gh-aw/blob/7363a87ff2483635af62f490b52c25a757a50221/pkg/workflow/compiler_activation_job.go#L36-L88), [checks](https://github.com/github/gh-aw/blob/7363a87ff2483635af62f490b52c25a757a50221/pkg/workflow/compiler_activation_steps.go#L36-L149), [lock/version](https://github.com/github/gh-aw/blob/7363a87ff2483635af62f490b52c25a757a50221/pkg/workflow/compiler_activation_steps.go#L150-L229). |
| gh-aw AI Credit controls | Activation daily-workflow accounting when enabled; compiled per-run caps during execution | Daily status/total/exceeded outputs; accounting failures are visible and block startup, subject to configured compiler behavior. Runtime caps remain gh-aw-owned. | CAO repository counts/rollout/dispatch limits bound fleet work, not per-run spend. CAO's deprecated monthly field has no runtime gate; shared control explicitly disables its daily limit with `-1`. Do not resurrect a second credit controller in CAO policy. [Daily builder](https://github.com/github/gh-aw/blob/7363a87ff2483635af62f490b52c25a757a50221/pkg/workflow/compiler_activation_daily_aic.go#L114-L174), [accounting failure](https://github.com/github/gh-aw/blob/7363a87ff2483635af62f490b52c25a757a50221/actions/setup/js/check_daily_aic_workflow_guardrail.cjs#L855-L868), [shared settings](https://github.com/githubnext/gh-cao/blob/1d0ee7f5813c60078045506827bde4a262b3e645/.github/workflows/shared/control.md#L60-L85). |
| Hosted CAO OIDC admission authority | Separate HTTPS service; signed immutable invocation identity, exact protected enrollment, fresh AI Credits accounting and durable replay decisions | Allowances on 200, denial on 403, unavailable on 503; revalidate every retry, preserve the first allowance and permanent expiry/revocation denial. | Not CAO rollout policy and **not a shipped client gate in v0.92.6**. The pinned upstream spec explicitly says proposal/no implementation. CAO's newer v1.2 service requires a compatible client and recompilation; setting variables alone cannot add it to these locks. [Pinned status](https://github.com/github/gh-aw/blob/7363a87ff2483635af62f490b52c25a757a50221/docs/src/content/docs/specs/admission-service-specification.md#L9-L20), [identity](https://github.com/githubnext/gh-cao/blob/1d0ee7f5813c60078045506827bde4a262b3e645/server/internal/admission/identity.go#L63-L158), [decision](https://github.com/githubnext/gh-cao/blob/1d0ee7f5813c60078045506827bde4a262b3e645/server/internal/admission/service.go#L41-L137), [compatible protocol](admission-gate.md#before-you-begin). |

The generated activation condition demonstrates the cumulative boundary:
`needs.pre_activation.outputs.activated == 'true'` **and**
`needs.pre_activation.outputs.cao_authorized == 'true'`
([compiled example](https://github.com/githubnext/gh-cao/blob/1d0ee7f5813c60078045506827bde4a262b3e645/.github/workflows/dependabot.lock.yml#L135-L138)).
The custom CAO output also combines admission with precompute. Failed required
steps and artifact validation/upload block job success independently of that
output; the `!= 'false'` expression alone is not the entire fail-closed boundary.
These are executable gates, not prompt instructions.

### Duplication reduced inside CAO

The [authority envelope framework](authority-envelopes.md) separates the pure
mode/target authority resolver from operating-pattern adapters. Direct operations
use explicit target sets and run-bound versioned envelopes without campaign or
inventory topology; existing campaign and standalone handoffs stay compatible.

The [control runtime](https://github.com/githubnext/gh-cao/blob/main/.github/workflows/shared/control.mjs)
now shares one exact-target scope validator across manual debug admission,
worker dispatch, requested inventory selection, and resolved standalone
candidates. It retains case-insensitive owner/repository comparisons, empty
allowlist semantics, role-specific denial reasons, and scope rechecks at each
existing trust boundary. Review destinations still use their separate
owner/access/privacy rules; they are not incorrectly required to be members of
the target allowlist.

Inventory limit validation also has one implementation, called once for
orchestrator/standalone precompute before remote inventory work. The duplicate
orchestrator numeric checks are removed, while repository/scan/rollout bounds,
cell/batch configuration and orchestrator dispatch bounds remain enforced.
The batch-index check that depends on the actual selected cell remains in
partitioning. Policy parsing still validates the original document; precompute
still validates the admitted handoff. Consolidation removes repeated
implementations, not those distinct phase checks.

### Intentional overlap and remaining work

Keep gh-aw actor/trigger checks, CAO rollout/target checks, debug permission
revalidation, credential narrowing, safe-output staging and handoff validation.
No successful check grants another layer's authority. The hosted service's
enrollment protects its own accounting authority; it neither replaces
`control-plane.workflows`/campaign enablement nor permits target writes.
Correlation IDs and matching orchestrator URLs remain audit linkage, not
cryptographic dispatch provenance.

Repeated per-workflow `cao_authorized`/`cao_reason` output declarations are a
candidate for **upstream** deduplication, not a reason to delete local gates.
In the pinned compiler,
[`mergeJobInjectedSteps`](https://github.com/github/gh-aw/blob/7363a87ff2483635af62f490b52c25a757a50221/pkg/workflow/workflow_import_merge.go#L94-L227)
merges step-injection fields and `continue-on-error`, but not output maps when
the main workflow also defines the job. Moving all outputs to a shared import
would therefore lose them on workflows that augment `pre-activation`.
Recommend upstream deterministic output-map merging with explicit local
precedence and collision tests before centralizing those declarations. Keep
each workflow's activation condition until the pinned compiler has a verified
shared-condition contract as well.

No compiler upgrade, live policy change, credential provisioning, hosted gate
activation, or workflow dispatch is part of this reduction. A future hosted
admission rollout must separately select and validate a compatible client
revision; a future capacity change must explicitly account for the actual
precompute credential rather than remove the API-readiness gate.

Admission queries the GitHub rate-limit API and uses the returned core limit, remaining capacity, and reset time directly. It does not read or write a repository variable to coordinate capacity decisions across runs.

The former `CAO_GITHUB_API_GATE` repository variable is not an authority or integrity input and is no longer used. Capacity is checked directly with the job-local credential, so there is no shared mutable gate or concurrent gate writer.

There is no `.github/cao/src/report.mjs` in the current control runtime. Admission reporting is emitted by `.github/workflows/shared/control.mjs`; dashboard reporting is the separate modular pipeline under `dashboard/report/`.

## What Admission Gates

The shared control component keeps one canonical runtime under `.github/workflows/shared/`. The exact-`github.workflow_sha` shared checkout contains that runtime and `.github/workflows/cao.json`; authorized runs execute `precompute` from the same checkout.

| Check | Admitted when |
| --- | --- |
| Runtime revision | `github.workflow_sha` is an exact commit and the policy and both CAO modules are readable at that revision. |
| Policy document | The JSON has supported keys, types, ranges, unique names, no duplicate keys, and no GitHub Actions expressions. |
| Control plane | `control-plane` exists. |
| Workflow identity | The workflow declares `orchestrator`, `worker`, or `standalone`; workers declare an exact worker identity, while standalone workflow slugs must match the actual compiled workflow reference. |
| Campaign | The campaign, or standalone workflow under `control-plane.workflows`, is declared and not disabled. |
| Worker | A worker is declared under that campaign and not disabled. |
| Target input | A supplied `target_repo` uses exact `owner/repository` form. Scope and access are checked later during precompute. |
| Mode input | `review` or `live` does not exceed the checked-in ceiling; request-only `debug` passes the manual worker and human authorization gates below. |
| Run limits | `max_repos` and `rollout_percent` are valid and do not exceed checked-in policy. |
| GitHub API capacity | The admission job's workflow credential has enough primary REST API capacity for the estimated run. Precompute may use a different credential and handles subsequent rate-limit failures separately. |

A manual dispatch can narrow a run, such as changing an authorized `live` run to `review` or reducing `max_repos`. It cannot promote mode, add scope, enable a standalone workflow, campaign, or worker, or increase a limit.

Standalone workflows use these same gates without campaign records or worker
discovery. Precompute admits a deterministic repository set through the shared
scope, inventory, percentage, and repository limits; each candidate carries its
effective mode and output route. The workflow executes its own mission directly
on that set and cannot discover additional targets or dispatch work. Required
inventory failures prevent activation. See
[standalone workflow configuration](configuration.md#standalone-agentic-workflows).

### Manual Debug Admission

`debug` is accepted only for a directly launched `workflow_dispatch` worker,
not an orchestrator, scheduled run, or bot dispatch. Admission verifies the
authoritative event inputs and human sender, rejects a different rerun actor,
and checks that the sender still has write, maintain, or admin permission on
the control repository. Missing event or permission evidence denies the run.
Precompute repeats that check before producing the worker handoff.

Debug retains exact-SHA policy validation, campaign and worker enablement,
target scope, request limits, credential requirements, and API-capacity
checks. It replaces only dispatcher-envelope requirements: correlation ID,
central repository, and originating-run URL inputs must be empty.
The output repository must equal the explicit target; no review destination
is resolved because gh-aw stages every declared output. Debug cannot be
persisted as a policy mode or dispatched by an orchestrator.
See [Local Worker Debugging](local-debugging.md)
for launch commands, staging behavior, and the local-origin limitation.

## Worker Dispatch Trust Boundary

`workflow_dispatch` authenticates the caller as the GitHub App bot that holds the write-App credential. Allowlisting that bot preserves safe-output worker dispatches, but its login alone is not cryptographic proof of a particular orchestrator run, App installation, or dispatch envelope: a holder of the same App credential can submit equivalent workflow-dispatch inputs directly.

CAO therefore treats the write-App credential, its control-repository secret, and its selected installations as part of the trusted control-plane boundary. A worker independently fails closed unless the policy at its exact workflow SHA declares its campaign and worker, keeps both enabled, accepts the requested mode and output route, and accepts the target owner and any exact repository allowlist. The target repository cannot widen, narrow, or veto this authority through its own files. These checks prevent a dispatch from widening policy, campaign, target, or mode, even when the caller has the allowlisted bot identity.

The correlation ID and control-plane run URL provide audit linkage only; they are dispatch inputs and cannot establish provenance by themselves. A deployment that requires proof that *only* a particular orchestrator run issued a worker dispatch needs a signed, replay-resistant envelope generated outside the agent-visible dispatch inputs (or a GitHub-provided source-run attestation). Do not treat the App login, installation access, or a matching run URL as a substitute for that stronger guarantee.

## What Precompute Gates

Admission is deliberately lightweight and repository-local. Once admitted, `.github/workflows/shared/control.md` runs deterministic precompute checks that need credentials, repository metadata, inventory, or usage evidence.

| Admission | Authorized-run precompute |
| --- | --- |
| Validates policy and workflow identity | Resolves repository inventory and allowlists |
| Rejects disabled or undeclared campaigns and workers | Verifies target and review-repository access |
| Prevents manual inputs from widening policy | Binds live workers to centrally authorized targets and output routes |
| Computes policy ceilings | Applies repository, rollout, and dispatch limits |
| Uses only the control repository revision | Confirms installed worker workflow availability and binds output routing |

Failure in either phase prevents agent execution. Admission denial skips activation; precompute fails closed when an authorized run cannot establish a required remote fact. Successful precompute uploads only `control-precompute.json`; the agent job restores and validates that non-secret artifact before checkout or model invocation.

## Connect Setup and Policy

Setup creates one atomic control-plane revision:

1. Install the gh-aw campaign from an immutable CAO tag or commit.
2. Verify that campaign installation copied `.github/workflows/shared/control.mjs` and `.github/workflows/shared/policy.mjs`.
3. Declare the installed campaign and its worker-to-workflow mapping in `.github/workflows/cao.json`.
4. Commit the workflows, generated locks, campaign records, and policy together, then push before running the campaign.

The Bash installer installs the explicit CAO root package and creates the consumer-owned policy. The package provides one runtime copy under `.github/workflows/shared/`. Controlled workflows receive it through their existing exact-SHA shared checkout; they do not fetch another copy from the CAO repository. Follow [Set Up CAO](setup-quickstarts.md) to bootstrap the repository and configure its exact scope interactively.

Control-plane installation does not declare an operational campaign in consumer-owned policy or grant admission. The CAO setup procedure and checked-in control policy own those decisions.

The [Configuration Reference](configuration.md) defines every policy field. The phase that uses each group is:

| Configuration | Admission effect | Precompute effect |
| --- | --- | --- |
| `control-plane.campaigns` and `workers` | Declares and enables the exact workflow identity. | Resolves installed worker workflow paths. |
| `control-plane.workflows` | Declares and enables the exact standalone workflow identity. | Admits its bounded repository set without worker workflow discovery. |
| `mode`, target `mode`, and worker `max-mode` | Establishes the maximum mode a dispatch may request. | Re-resolves the central ceiling before worker execution. |
| `max-repositories` and `rollout-percent` | Rejects a wider manual request. | Bounds selected repositories. |
| `scope` | Validates and returns the configured owners and repositories. | Filters inventory and rejects out-of-scope targets or review destinations; workers also reject targets outside a configured exact repository allowlist. |
| `inventory` | Validates scan, cell, and batch limits. | Performs bounded discovery and deterministic batching. |

## Diagnose a Skipped Run

Open the run summary and expand **Central Agentic Ops admission**. An authorized run names its campaign and role, and every check in the list is marked ✅. A denied run records a reason, marks every check before the failing one ✅, marks the exact failing check ❌, and leaves activation skipped. The ❌ marker identifies which row of the table below to consult; later checks are left unmarked because admission stopped before reaching them.

| Reason | Check marked ❌ | Configuration or setup to check |
| --- | --- | --- |
| Cannot read or execute CAO runtime | Runtime revision | Verify that the CAO root package installed `.github/workflows/shared/control.mjs` and `policy.mjs` and that both exist at the workflow revision. |
| `control policy validation failed` | Policy document | Validate policy keys, types, ranges, unique names, and expressions in `.github/workflows/cao.json`. |
| `control-plane-absent` | Control plane | Add `control-plane` to `.github/workflows/cao.json`. |
| `role must be orchestrator, worker, or standalone`, `worker identity is required`, `worker identity is forbidden for orchestrators` | Workflow identity | Fix the role and its workflow or worker identity. |
| `standalone workflow identity must match GITHUB_WORKFLOW_REF` | Workflow identity | Set the standalone import's `workflow` to its exact compiled filename slug. |
| `workflow-undeclared`, `workflow-disabled` | Campaign | Review the standalone entry under `control-plane.workflows` and its enablement. |
| `campaign-undeclared` | Campaign | Add the installed campaign under `control-plane.campaigns`. |
| `campaign-disabled` | Campaign | Review the campaign, then remove `enabled: false` when it is safe to resume. |
| `worker-disabled`, `unknown worker: <campaign>/<worker>` | Worker | Review the worker, then remove `enabled: false` or declare it under `control-plane.campaigns.<campaign>.workers` when it is safe to resume. |
| `target_repo must use owner/repository form` | Target input | Fix the `target_repo` manual input to the exact `owner/repository` form. |
| `safe_output_mode exceeds checked-in policy`, `safe_output_mode must be review or live` | Mode input | Narrow the requested `safe_output_mode`, or raise the checked-in campaign, target, or worker `mode`/`max-mode` ceiling. |
| `debug ...` | Mode input | Launch a worker manually as a write-authorized human, use an in-scope target as the output repository, and omit dispatcher-envelope inputs. Inspect the exact reason for missing event or permission evidence. |
| `max_repositories exceeds checked-in policy`, `rollout_percent exceeds checked-in policy`, or an integer-range message | Run limits | Narrow the requested `max_repos`/`rollout_percent`, or raise the checked-in `max-repositories`/`rollout-percent`. |
| `github-api-capacity-insufficient`, `github-api-capacity-unavailable` | GitHub API capacity | Follow the remediation guidance in the run summary and wait until the reported reset time before retrying. |

Fix the checked-in setup or policy, commit and push the new revision, then start a new run. Do not bypass admission by editing a generated `.lock.yml` file or by widening manual inputs.