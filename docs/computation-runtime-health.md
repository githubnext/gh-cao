---
title: Runtime Health Computation
description: Evaluate current Campaign, orchestrator, and worker-target runtime health from canonical Run evidence.
---

# Runtime health computation

The `runtime-health` computation answers:

> Has each evaluated Campaign partition produced a successful Run, is it
> running, or does its current evidence require attention?

It is a deterministic measure over canonical Campaign, Workflow, Repository,
and Run records. It does not inspect raw workflow logs, call GitHub, diagnose
repository outcomes, or authorize remediation.

The current measure contract is:

```json
{
  "measureId": "runtime-health",
  "measureVersion": "1.0.0"
}
```

For the normative computation model, see the
[Computations Specification](https://github.com/githubnext/gh-cao/blob/main/specs/computations.md).

## Run it

Acquire a successful Activity run's operational snapshot with Actions read
access. There is no implicit public snapshot; Pages contains only synthetic data.

```bash
gh run download RUN_ID --repo OWNER/CONTROL --name cao-activity-index --dir .cao
gh cao computation runtime-health
```

For an explicitly provided operational manifest URL, use
`gh cao download --url URL`; see [Agent analysis](agent-analysis.md).

Evaluate one Campaign:

```bash
gh cao computation runtime-health --campaign dependabot
```

Include bounded diagnostic evidence for one Campaign:

```bash
gh cao computation runtime-health --campaign dependabot --diagnose
```

`--diagnose` requires `--campaign`. Use `--database FILE` and
`--inventory FILE` to evaluate another canonical snapshot and its matching
inventory.

## Evaluation partitions

The query boundary selects and orders two partition kinds:

| Partition | Identity |
| --- | --- |
| Orchestrator | Campaign and orchestrator Workflow |
| Worker target | Campaign, worker Workflow, and target Repository |

Runs must be supplied newest first by Run time, GitHub Run ID, and attempt.
The measure rejects incorrectly ordered input rather than silently sorting it,
because a wrong success boundary can change the answer.

For each partition, evaluation stops at the latest observed successful Run.
Only newer Runs contribute to the current failure streak. This prevents old,
already-recovered failures from remaining active forever.

## Answers

| Answer | Meaning |
| --- | --- |
| `yes` | The latest relevant terminal boundary is a success. |
| `running` | There is active Run evidence and no newer terminal failure. |
| `no` | One or more terminal non-successes occurred after the latest success. |
| `not-observed` | No Run was observed for the selected partition. |
| `unknown` | Available evidence cannot establish a reliable state. |

`needsAttention` is true for `no`, `not-observed`, and `unknown`. It is false
for `yes` and `running`.

A terminal failure followed by an active Run sets
`recoveryMayBeInProgress: true`. Portfolio intelligence uses this field to
suppress a new recommendation until the active Run reaches a terminal state.

## Orchestrator gate

Orchestrators are evaluated before workers:

- An orchestrator answer of `no` blocks worker evaluation.
- Missing or indeterminate orchestrator evidence makes worker evaluation
  indeterminate.
- Workers are evaluated only when all selected orchestrator evidence is
  eligible.

This avoids interpreting missing worker results as worker failures when the
Campaign could not dispatch work successfully.

Worker observations also retain target-scope membership:

- `expected` means the Repository is in the declared target scope.
- `observed-extra` means execution evidence exists outside that scope.
- `unknown` means membership could not be established.

Observed-extra partitions remain historical evidence but do not worsen the
configured Campaign aggregate. Portfolio intelligence suppresses their failure
signals instead of recommending action against an undeclared target.

## Failure groups

Terminal non-successes after the success boundary are grouped by the first
available classification:

1. `failureKind`;
2. `classification`;
3. `conclusion`; or
4. `unknown-error`.

Each group reports:

- occurrence and distinct Run-attempt counts;
- conclusion counts;
- latest observation time;
- bounded failure-job and failure-step labels; and
- bounded canonical Run references.

Output is capped at 20 error groups and 20 Run references per group. Omitted
counts remain explicit.

## Result shape

The portfolio result contains:

```json
{
  "measureId": "runtime-health",
  "measureVersion": "1.0.0",
  "campaignResults": [],
  "partitionResults": [],
  "errorGroups": []
}
```

`campaignResults` summarizes the orchestrator gate and aggregate answer.
`partitionResults` explains every evaluated partition and its success boundary.
`errorGroups` is the bounded input consumed by
[portfolio intelligence](computation-intelligence.md).

With `--diagnose`, the result also contains bounded canonical Run metadata,
audits, tool errors, firewall blocks, safe-output observations, and a GitHub
Actions Run link when available. Diagnostics explain where to investigate;
they do not change the measure answer.

## Interpretation limits

Runtime success means only that GitHub Actions recorded a successful Run. It
does not prove that:

- a repository outcome was attained;
- an output was accepted;
- the Campaign created operational value; or
- another execution is authorized.

Use Campaign-specific operational-value evidence for outcome attainment and
the reviewed CAO policy for authority.
