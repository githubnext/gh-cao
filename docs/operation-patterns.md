---
title: From Outcome to Execution
description: Describe the outcome, target scope, and execution requirements; derive the simplest suitable operation structure.
agent:
  type: architecture
  prominent: true
---

**Describe the work, not a pattern.** Guidance and authoring agents derive the
simplest suitable structure and explain it. Pattern names are reference
details, not a questionnaire or runtime inference engine.

## Describe the work

Provide known requirements:

| Requirement | Describe |
| --- | --- |
| Outcome | What should improve, what output is useful, and what proves completion? |
| Target scope | Which repositories may participate? Will the caller supply exact targets, or should CAO select eligible inventory within scope? |
| Execution | Is visibility enough? Can one bounded run finish the work, or does each target need independent execution, history, retries, or specialized workers? |
| Coordination | Is the work deterministic? Does it need deployment stages, approvals, fan-in, or long waits? When should it start? |
| Authority | Where may outputs go? What reviewed policy and access exist? Live changes require separate explicit authorization. |

For example:

> Inspect supported Node.js versions in `acme/api` and `acme/web` and produce
> one review report. I supply the targets; one bounded run is enough.
> Do not change either target.

The agent should explain: "A direct operation fits your exact targets and one
report." Policy, access, and a common review destination still need verification.

Ask only for genuinely missing requirements, such as whether failed targets
need independent retries. Do not ask "which pattern do you want?"

## How requirements determine the structure

| What you need | Derived structure | Execution boundary |
| --- | --- | --- |
| Existing workflows need shared visibility without changing execution | **Observe only** | Activity and the dashboard collect evidence; monitoring does not add CAO admission. |
| The caller already knows the target set; one bounded mission suffices | **Direct operation** | No discovery or dispatch. Multiple targets are review-only with a common destination; live requires one target. |
| CAO selects a bounded portion of an enrolled fleet for one mission | **Standalone fleet workflow** | Deterministic selection and caps inside one run; no independently retried workers. |
| Target runs need independent isolation and history, retries, or specialized workers | **Campaign** | Separate selection and single-target workers; more workflows, credentials, dispatches, and run correlation. |
| Deterministic work or deployment stages, approvals, fan-in or long waits | **Ordinary GitHub Actions** | Normal jobs and deployments; agentic reasoning only where needed. Ordinary jobs do not automatically inherit CAO admission. |

Independent target execution justifies a campaign even when the caller knows
the targets. Otherwise, supplied targets imply direct execution; selected
inventory in one bounded run implies standalone fleet execution.
Repository count or a schedule alone does not require a campaign.

Explain the fit; avoid unnecessary discovery, workers, or packaging. Explicit
campaign requests may use campaign-specific guides directly.

## Integration reference

| Structure | Implementation |
| --- | --- |
| Direct operation | Declare the exact workflow under `control-plane.operations`; import `shared/operation.md`. See [authority envelopes](authority-envelopes.md). No campaign, inventory, dispatcher envelope, or durable queue API. |
| Standalone fleet | Declare the exact workflow under `control-plane.workflows`; import shared control as `standalone`. See [standalone configuration](configuration.md#standalone-agentic-workflows). Each run is a bounded attempt, not continuation. |
| Campaign | Orchestrators select within policy; workers revalidate one target and ceilings. See [Orchestrators and Workers](orchestrators-and-workers.md) and [Rollout and Routing](rollout-and-routing.md). Installation is not live authorization. |

Triggers, packaging, and hosting do not require campaigns. The optional
webhook gateway has separate ingress and dispatch contracts, not a general
workflow engine; the direct import does not connect to it.

### Repository-local Java backend experiment

The catalog source's `.github/workflows/java-backend-reimplementation.md` is a
daily, manually dispatchable experiment in implementing the CAO specifications
with a Java 21 backend and cloud-ready packaging. Each run attempts one tested
increment under `experiments/java-backend/` and may open one draft pull request.
An open `java-backend-experiment` pull request pauses further attempts; only
merged default-branch code counts as accepted progress.

The workflow reads the checked-out specifications and HTTP contracts, maintains
a section-level implementation status, and preserves validation evidence as a
14-day Actions artifact. Maven verification, negative contract tests and applicable
real-adapter checks gate proposals; unavailable checks are incomplete evidence.
Cloud-ready packaging means local container/configuration work, not deployment.
This private repository-local workflow is not installed by the root campaign,
does not participate in CAO fleet admission, and cannot change production code,
rollout policy, credentials or cloud resources. Its schedule takes effect only
after the compiled workflow is merged into the repository's default branch;
execution still requires the repository's normal Actions and Copilot setup.

## Infer execution structure, never authority

CAO governs whether and where participating workflows run; gh-aw governs how:
tools, network, permissions, credentials, limits, and safe outputs. Policy at
the exact workflow SHA, request, applicable ceilings, access, and compiled
capabilities must all agree. Review workflow and policy changes together.

Deriving a structure cannot enroll repositories, expand scope or access,
change output routing, enable execution, or grant capabilities. Target files,
steering, dashboard visibility, and previous successful runs cannot authorize
work. Missing authority or required evidence fails closed.

**Review is real execution**, potentially writing proposals to the review
destination, not a no-write dry run. Live requires an explicit reviewed policy
decision. Debug is a human-launched staged worker profile, not another
production pattern.

Monitoring is a complete result. Start new agentic execution with one review
target; inspect scope, routing, and outputs before widening. For recovery, see
[Monitor, Recover, and Maintain](operations.md).
