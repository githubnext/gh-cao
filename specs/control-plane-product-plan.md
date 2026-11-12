---
title: Potential CAO Control Plane Improvements
description: General guidance for evolving CAO from observability toward a governed operations control plane.
status: Exploration
---

# Potential CAO Control Plane Improvements

> **North star:** CAO makes every agentic change visible, attributable, governable, measurable, and recoverable across the software-development fleet.

## Opportunity

CAO could evolve from a primarily observational dashboard into the team and enterprise control plane for GitHub Agentic Workflows. The experience should help authorized operators understand what agents are doing, whether the work is safe and useful, where human attention is required, and which recovery or governance action is appropriate. This direction should strengthen operations and governance without turning CAO into a general-purpose agent builder.

## Build on the current architecture

The repository already contains the main foundations for this direction. `specs/control-architecture.md`, `.github/workflows/cao.json`, and `.github/workflows/shared/` define policy-governed rollout and execution boundaries. `specs/server-ingestion.md` and `activity/` provide bounded evidence collection. `specs/dashboard-data.md`, `dashboard/site/src/data/`, and `server/` provide canonical, rebuildable projections. `specs/computations.md` defines deterministic insights, while `specs/dashboard.md` already describes a task-centered operator experience.

Potential improvements should extend these contracts rather than create a parallel control system. The dashboard should remain a presentation and interaction surface, Activity should remain evidence infrastructure, computations should remain derived evidence, and the control repository should remain the source of rollout authority. Existing orchestrator discovery and one-target worker enforcement should continue to bound all execution.

## Preserve authority and safety

Any intervention should remain constrained by reviewed policy at the exact workflow SHA, the submitted request, actor authorization, credential reach, and capabilities compiled by gh-aw. A displayed control, available credential, recommendation, simulation, or historical success should never grant authority.

Control requests should pass through an authoritative workflow or server boundary that previews the effect, revalidates authority, executes idempotently, and verifies the resulting state. Review should remain the default. Missing policy, identity, approval, evidence, checkpoint support, or rollback support should fail closed rather than produce a success-shaped result.

Operational actions should produce immutable receipts that preserve actor, reason, time, policy revision, scope, decision, side effects, and verified readback. This should apply equally to successful actions, denials, partial failures, and emergency operations. Sensitive prompts, credentials, context, and connector payloads should remain minimized and access-controlled.

## Improve the evidence and identity model

A common operational evidence envelope could make each run independently auditable without reconstructing intent from logs. It should connect stable work and attempt identity with provenance, workflow and input versions, agent and model configuration, tool activity, artifacts, approvals, verification, outcomes, usage, costs, checkpoints, interventions, and recovery references. Missing, stale, partial, unavailable, and unknown evidence should remain explicit.

Identity should distinguish the requester, workflow owner, agent, implementer, reviewer, approver, and execution principal. Delegation should retain its grant source, scope, constraints, and validity period. Deterministic checks could highlight incompatible role combinations, particularly when the same automation identity both implements and approves a change. The interface should not infer equivalence from names or presentation labels.

## Develop a governed operational loop

The first priority could be a coherent operational loop spanning canonical evidence, declarative queries, attention, intervention, and receipts. A unified projection should make work, runs, ownership, repository context, status, blockers, model and tool use, cost, latency, approval state, stop conditions, and evidence links available through bounded Dashboard Language queries.

The attention experience should derive explainable signals from direct evidence such as policy blocks, approval waits, verification failures, stalled dependencies, budget breaches, coordination conflicts, and incomplete evidence. Authorized operators could then pause, resume, cancel, retry, quarantine, approve, or revoke approval through the control boundary. Replay and rollback should appear only where a workflow explicitly supplies compatible checkpoints or recovery capabilities.

The existing GitHub-native emergency-stop procedure in `docs/operations.md` could become a coordinated fleet operation over an explicit inventory of control repositories. Such an operation should distinguish stopping future execution from cancelling active work, allow explicitly preserved critical jobs, and report every runtime it could not stop. Checked-in campaign switches should remain narrower controls rather than substitutes for active cancellation.

Native per-run limits should remain owned by gh-aw. CAO could complement them with deterministic fleet admission budgets resolved across organization, team, campaign, workflow, model, and time window. Evaluation mode could explain would-be decisions before enforcement is enabled, while strict enforcement could reject new admissions when required accounting is missing or stale.

## Expand coordination and value insight

A later phase could connect goals, assignments, dependencies, handoffs, branches, pull requests, review ownership, and integration order into a shared work graph. Overlap analysis could identify likely edit collisions and recommend sequencing, rebasing, consolidation, or reassignment without mutating work automatically.

Portfolio analytics should move from activity counts toward accepted outcomes and operational impact. Useful measures could cover acceptance, merge, rework, triage, remediation, abandonment, prevented defects, deterministic-versus-agentic execution, and cost per accepted result. Model comparisons should use comparable work and evidence windows. Every measure should disclose its population, interval, baseline, attribution coverage, and uncertainty; runtime success or output creation should not be treated as value.

PR-system simulation could estimate queueing, review load, collision risk, and concurrency effects as versioned derived evidence with explicit assumptions. Catalog baselines, drift reporting, evaluate-before-enforce policy, and reviewable remediation pull requests could help teams manage workflows at fleet scale. Structured traces, run comparison, failure classification, protected context inspection, tool attribution, deterministic replay, and optimization recommendations could support debugging without giving recommendations execution authority.

## Extend through trusted connectors

Cross-product support for Azure, Microsoft Graph, WorkIQ, Teams, or Azure DevOps should reuse the same identity, delegation, approval, evidence, action, and receipt concepts. Connectors should provide verified readback and keep credentials outside policy and evidence records. External systems should not become implicit authority, and connector breadth should not shift CAO toward agent authoring.

## Shape the operator experience

The proposed information architecture can remain compatible with the existing dashboard model. Overview can evolve from Home; Work graph can combine Work and Agents; Runs can expose execution evidence and recovery eligibility; Attention can specialize the existing ranked intervention surface; Value can extend Insights; Policies can extend Settings; Catalog can build on campaign inventory; and Simulation can remain a subordinate analytical view.

Each page should retain progressive disclosure and no more than four essential initial regions. Queries, filtering, joins, grouping, and business derivation should remain declarative and execute through the data worker. Evidence and raw execution details should remain directly reachable from the claims they support rather than becoming disconnected inventories.

## Guidance for implementation experiments

Agents exploring these ideas should begin with the applicable normative specification and preserve the repository's authority boundaries. A useful experiment should be a vertical slice that updates the producer contract, canonical schema, ingestion path, declarative query, view contract, tests, and operator documentation together. Schema evolution should be additive and deterministic, with no fabricated historical identity, approvals, outcomes, costs, or checkpoints.

Experiments should cover denied authority, stale policy, duplicate delivery, partial fleet stop, incompatible roles, budget races, failed readback, unavailable rollback, and connector timeout. Recommendations should remain separate from execution, and executing a recommendation should create a new authorized request and receipt.

Progress could be evaluated through time to identify blocked work, time to verified recovery, evidence-envelope completeness, independent-review coverage, duplicate-work and merge-conflict reduction, cost per accepted outcome, approved workflow adoption, and repeated reasoning converted to deterministic automation. These are candidate measures rather than commitments and should be adopted only with explicit definitions and representative baselines.
