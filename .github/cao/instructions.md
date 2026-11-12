# Central Agentic Ops Instructions

## Campaign categories

Treat top-level Central Agentic Ops campaigns as operational campaigns by default. They contain an orchestrator and at least one independently dispatchable worker, use `shared/control.md`, and follow `skills/create-cao-campaign/SKILL.md`.

The `dashboard/` directory contains the shared Dashboard Language runtime,
not an installable campaign. The legacy Activity, dashboard artifact, and local
Value workflows and their component manifests are removed.

## CAO and gh-aw authority

Central Agentic Ops governs **whether and where** an installed operation may run: standalone workflow, campaign, and worker enablement, eligible owners and repositories, review/live ceilings, inventory partitioning, and rollout.

gh-aw governs **how** an authorized workflow executes: engines and models, per-run turns and AI Credit limits, tools, network access, permissions, generated job topology, authentication mechanics, and safe-output primitives and execution.

Treat this as a one-way boundary. CAO may deny a run or narrow its scope, but it must never grant or expand a gh-aw capability. Do not add engine settings, per-run limits, tools, permissions, credentials, jobs, or safe-output primitives to `.github/workflows/cao.json` or its resolver. Conversely, gh-aw execution capability never grants CAO rollout or live authority. CAO policy, credential reach, dispatch narrowing, and the compiled gh-aw workflow are cumulative boundaries; none substitutes for another. Target repository files do not participate in activation authority.

Orchestrators select and dispatch within the resolved rollout envelope; they do not perform target work. Workers enforce one dispatched target, resolve current policy before model execution, and do not discover repositories, dispatch downstream work, escalate mode, or accept credentials in the dispatch envelope.

`debug` is a request-only exception for one manually launched worker: require a
human `workflow_dispatch` actor with current control-repository write access,
retain policy scope and enablement, reject dispatcher-envelope inputs, and force
gh-aw safe-output staging. It is not a policy ceiling, dispatcher mode, or live
authority. GitHub cannot verify local CLI origin; repository-memory staging
remains gh-aw-owned.

## Workflow authoring

Start from the desired outcome, target scope, and execution requirements, not
a request to choose a pattern. Follow `docs/operation-patterns.md`: visibility
without changing execution needs observation only; deterministic work, stages,
approvals, fan-in, and long waits use ordinary Actions with agentic reasoning
only where needed. Independent per-target execution, history, retries, or
specialized workers justify a campaign. Otherwise, caller-supplied exact
targets use direct operations; CAO-selected inventory processed in one bounded
run uses a standalone fleet workflow. Explain the simplest suitable structure
and ask only for genuinely missing requirements, never "which pattern?"

Infer execution structure, never authority. This decision cannot widen policy,
access, scope, output routing, or compiled capabilities, and cannot authorize
installation, enablement, dispatch, or live execution. Preserve explicit
reviewed live authorization and policy resolution at the exact workflow SHA.

CAO's authority kernel is topology-independent. Campaigns, standalone fleet
execution and direct operations are supported operating patterns, not distinct
sources of authority. Direct workflows import `shared/operation.md`, declare
their exact slug under `control-plane.operations`, and request an explicit
target set. They must retain the CAO pre-activation output and activation gate.
Multiple targets are review-only with a common validated destination; live
requires one explicit target and target-bound compiled safe outputs. Direct
operations cannot discover targets, dispatch downstream work or carry a
dispatcher envelope. See `docs/authority-envelopes.md`.

For an operational campaign, apply `skills/create-cao-campaign/SKILL.md`. It defines the CAO orchestrator/worker topology, rollout boundary, and required `.github/workflows/shared/control.md` imports.

For a standalone agentic workflow, import `shared/control.md` with
`role: standalone` and `workflow: <exact-workflow-slug>` instead of campaign and
worker identities. Declare it under `control-plane.workflows`, and preserve the
pre-activation admission output and activation gate used by campaign workflows.
It may execute directly against its deterministically admitted repository set,
using each target's precomputed mode and output destination, without downstream
dispatch or additional discovery. No campaign descriptor or ownership record is
required. See `docs/configuration.md` for the policy and import contract.

## Agent runtime

Agentic workflows execute the installed JavaScript runtime under `activity/`
and `dashboard/`, using `gh cao COMMAND` or the relevant
JavaScript module directly. Do not build, install, or invoke the native `gh cao`
extension as an agent runtime dependency. The native extension and root
`install-gh-cao.sh` are operator tools; their dedicated build, release, and installation
tests do not change the agent runtime boundary.

For Activity-backed analysis, follow
`skills/cao-cli/references/workflow-runtime.md`: use the restored cache with an
explicit database path, or fall back to bounded read-only tools when the runtime
or evidence is unavailable. Do not install the operator extension as a fallback.

## Operational snapshot inputs

- The legacy Activity, Dashboard artifact, and local Value workflows are retired. The root manifest installs validation; the runtime still supplies explicit snapshot analysis and hosted collection tooling.
- Keep data collection and cache publication out of operational campaigns and dashboard build jobs. Consumers must validate the scope, freshness, and completeness of explicitly prepared operational evidence.
- Prepared snapshots are evidence, not historical authority or permission to widen CAO policy or credential reach.

## Dashboard contract

- Materialize the shared dashboard runtime through the root CAO installer. Do not reinstall the retired component workflows or their manifests.
- Use an explicitly acquired, verified operational snapshot for local analysis or the authenticated hosted collector. Do not discover legacy dashboard bundles or treat Pages as operational evidence.
- Keep report source modules under `dashboard/report/`; the CAO materializer preserves those canonical paths in control repositories.
- Preserve scope, freshness, and completeness metadata when reading a prepared snapshot.
- Keep the production renderer under `dashboard/site/`; the CAO materializer preserves that canonical path in control repositories.
- Only the documentation workflow publishes Pages, using generated synthetic enterprise data. It must not restore operational artifacts, collection caches, credentials, or live services. Cloud production and local development are the real-data paths.
