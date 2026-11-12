---
title: Portfolio Intelligence Computation
description: Correlate runtime-health evidence into deterministic, review-only Decisions with Campaign contracts and operator feedback.
---

# Portfolio intelligence computation

The `intelligence` computation answers:

> Which current operational conditions should an operator review now or soon,
> and which candidate signals should be suppressed?

It consumes the versioned
[runtime-health result](computation-runtime-health.md), compiled Campaign
intelligence contracts, an optional previous result, and optional
fingerprint-bound feedback. It emits advisory Decisions and suppressions. It
does not admit Work, dispatch workers, or grant authority.

The current measure contract is:

```json
{
  "measureId": "portfolio-decisions",
  "measureVersion": "1.2.0"
}
```

## Run it

Acquire a successful Activity run's operational snapshot with Actions read
access. There is no implicit public snapshot; Pages contains only synthetic data.

```bash
gh run download RUN_ID --repo OWNER/CONTROL --name cao-activity-index --dir .cao
gh cao computation intelligence
```

For an explicitly provided operational manifest URL, use
`gh cao download --url URL`; see [Agent analysis](agent-analysis.md).

Evaluate one Campaign:

```bash
gh cao computation intelligence --campaign dependabot
```

Replay a prior result and apply operator feedback:

```bash
gh cao computation intelligence \
  --previous prior-intelligence.json \
  --feedback decision-feedback.json
```

Use `--database FILE` and `--inventory FILE` together to evaluate another
canonical snapshot.

## Candidate formation

The computation applies these deterministic stages:

1. Convert each runtime-health error group into a stable signal.
2. Suppress signals for `observed-extra` targets.
3. Suppress signals while `recoveryMayBeInProgress` is true.
4. Correlate remaining signals by exact Campaign identity and runtime error
   key.
5. Produce one Decision for each correlation group.
6. Apply feedback only when both the Decision ID and input fingerprint match.
7. Order Decisions by state priority and stable Decision ID.

Correlation does not use timestamps, physical ledger order, Git merge order,
or runtime scheduling as a tie-breaker.

## Decision state

The initial `protect` measure uses observed recurrence:

- `act-now` when the correlation group contains at least three observed
  failures;
- `decide-soon` when it contains fewer than three.

The threshold is recorded in `sensitivity` and the `decisionTrace`. This is not
a probability or a claim about expected benefit.

Every current recommendation is:

```json
{
  "operation": "investigate-runtime-failure",
  "mode": "review",
  "dispatchable": false
}
```

The Decision includes alternatives to defer, do nothing, or obtain additional
evidence. Its execution-authority hard gate is `not-evaluated` with
`dispatch-prohibited`.

## Identity and replay

`decisionId` identifies the semantic condition: the Campaign, Decision class,
operation, and correlated runtime error key. It remains stable when the
underlying evidence changes.

`inputFingerprint` identifies the exact measure version, threshold, Campaign
contract, and signal fingerprints used for one evaluation. It changes when
relevant evidence or configuration changes.

Passing an unchanged result through `--previous` reuses the same logical
Decisions. An incompatible measure version or malformed previous result is
rejected rather than silently reused.

## Campaign contracts and quality

Each Decision references the compiled Campaign intelligence contract used for
that evaluation. The fingerprint therefore changes when declared Campaign
semantics change.

Campaign-authored semantics arrive through the normalized
`intelligenceDeclaration` on the canonical Campaign record. The compiled
contract exposes the declaration version and Campaign identity separately from
the 17 semantic fields. See [Intelligence](intelligence.md#author-a-campaign-declaration)
for the authoring and installation format.

Decision quality combines:

- runtime-health availability and provenance;
- Campaign-contract completeness and maturity;
- signal coverage;
- target attribution coverage; and
- contradiction state.

A partial Campaign contract produces a partial Decision even when runtime
failure evidence is available. This is intentional: CAO can report a recurring
failure without pretending it knows the Campaign's outcome, expected value,
schedule rationale, or attention cost.

## Suppressions

The computation currently emits three suppression rules:

| Rule | Meaning |
| --- | --- |
| `outside-declared-target-scope` | Evidence belongs to a Repository that is not explicitly enrolled. |
| `recovery-in-progress` | A newer active Run may already be recovering the condition. |
| `unchanged-terminal-result` | Terminal feedback already resolved the exact Decision fingerprint. |

Suppressions retain the subject, signal fingerprint, reconsideration
condition, and related Decision ID when applicable. They are historical
explanations, not deleted evidence.

## Feedback

Feedback uses a versioned envelope:

```json
{
  "contractVersion": "1.0.0",
  "records": [{
    "decisionId": "runtime-health-decision:...",
    "inputFingerprint": "sha256:...",
    "disposition": "deferred",
    "observedAt": "2026-10-01T11:00:00Z",
    "actor": "operator:octocat",
    "authority": "control-repository-review"
  }]
}
```

Allowed dispositions are:

| Disposition | Effect for matching evidence |
| --- | --- |
| `accepted` | Terminal; suppress the unchanged Decision. |
| `rejected` | Terminal; suppress the unchanged Decision. |
| `superseded` | Terminal; suppress the unchanged Decision. |
| `recovered` | Terminal; suppress the unchanged Decision. |
| `expired` | Terminal; suppress the unchanged Decision. |
| `deferred` | Nonterminal; attach feedback and retain the Decision. |
| `unresolved` | Nonterminal; attach feedback and retain the Decision. |

Feedback for the same Decision ID but an older input fingerprint is reported as
stale and cannot suppress changed evidence. Feedback for an absent Decision ID
is reported as unmatched. The complete bounded feedback envelope remains in
the result for inspection.

`accepted` records a recommendation disposition only. It is not accepted
outcome evidence or operational-value attainment.

## Result shape

The result contains:

```json
{
  "measureId": "portfolio-decisions",
  "measureVersion": "1.2.0",
  "inputFingerprint": "sha256:...",
  "evidenceQuality": {},
  "evaluatedDecisionClasses": ["protect"],
  "agentInvocations": 0,
  "campaignContracts": [],
  "feedback": {},
  "signalCount": 0,
  "correlatedCandidateCount": 0,
  "decisionCount": 0,
  "suppressionCount": 0,
  "reusedDecisionIds": [],
  "decisions": [],
  "suppressions": [],
  "asOf": null
}
```

Each Decision includes:

- recommendation, alternatives, and consequences;
- hard gates and capacity requirements;
- normalized quality and evidence references;
- Campaign-contract identity and fingerprint;
- applicable feedback;
- correlation and sensitivity information; and
- a trace of inputs, measure versions, rules, assumptions, thresholds, gates,
  constraints, and tie-breakers.

Evidence references are bounded to 50 canonical Run IDs.

## Interpretation limits

The current computation evaluates only the `protect` Decision class over
runtime-health evidence. It does not yet:

- forecast AI Credit or human-attention demand;
- evaluate schedule fitness or Campaign overlap;
- compare operational value across Campaigns;
- select a portfolio under explicit capacity constraints;
- evaluate execution authority; or
- dispatch a selected worker.

Those capabilities require additional declared Campaign facts and independently
versioned measures. See the [intelligence overview](intelligence.md) for the
planned sequence.
