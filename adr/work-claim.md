# ADR: Ledger-Backed Work and Claim Orchestration in CAO

- **Status:** Proposed
- **Repository:** `githubnext/gh-cao`
- **Decision type:** Architecture
- **Scope:** Campaign orchestration, worker dispatch, Work/Claim ledger integration

## Context

CAO orchestrators currently select target repositories and dispatch worker workflows directly.

This couples two distinct responsibilities:

1. deciding what work should happen; and
2. materializing that work as GitHub Actions workflow runs.

Direct dispatch has several limitations:

- dispatch intent is not durable before execution;
- retries and partial failures are difficult to reconcile;
- concurrent orchestrators can dispatch duplicate work;
- there is no durable intermediate representation between an orchestration decision and a worker run;
- the central orchestrator must reason about execution state directly;
- intelligence, campaign orchestration, execution, and observability are more tightly coupled than necessary.

CAO already has:

- canonical repository, workflow, run, outcome, and operational-value data;
- deterministic query and computation infrastructure;
- Dashboard Language;
- CAO Query MCP;
- policy and control precomputation;
- safe-output-based worker dispatch;
- repository-memory-backed campaign state.

Separately, `github/gh-aw` is expected to provide a generic `repo-memory.ledger` capability with append-only JSONL durability, deterministic reconstruction, and an ephemeral SQLite query projection.

CAO should consume that generic ledger rather than implement a separate persistence or query subsystem.

## Decision

Introduce first-class **Work** and **Claim** concepts in CAO.

The orchestration model becomes:

```text
evidence / intelligence / campaign agent
                ↓
              Work
                ↓
              Claim
                ↓
      deterministic dispatcher
                ↓
              Run
                ↓
        outcome / value
```

### Work

A **Work** record represents durable execution intent:

> Something should be done.

Work is independent of the concrete workflow revision that will execute it.

Work may originate from:

- a campaign orchestrator;
- CAO Intelligence;
- another agentic workflow;
- deterministic CAO automation.

Work is immutable ledger data.

Its effective state is derived rather than mutated.

### Claim

A **Claim** represents assignment of one Work item to one immutable worker revision:

```text
worker identity =
(repository, agenticWorkflowId, gitSha)
```

A Claim always has a worker.

There are no unassigned Claims.

Claims are immutable.

Retries create new Claims rather than mutating old Claims.

### Run

A concrete GitHub Actions execution remains represented by the existing canonical CAO `Run` entity.

Therefore:

```text
Attempt = Run correlated with a Claim
```

No separate durable Attempt entity is required initially.

### Result

Execution results should reuse existing CAO Run, Outcome, safe-output, and operational-value evidence.

A separate Result entity should be introduced only if existing canonical evidence proves insufficient.

## Control flow

The architecture becomes:

```text
CAO Intelligence / campaign orchestrator
                  │
                  │ query CAO data
                  │
                  ▼
             append-claim
              safe output
                  │
                  ▼
          repo-memory ledger
            Work + Claim
                  │
                  ▼
          cao-dispatcher.yml
            deterministic
                  │
                  ▼
          worker workflow@SHA
                  │
                  ▼
                 Run
                  │
                  ▼
      Outcome / operational value
```

## Ledger-enabled control workflow

The existing `shared/control.md` remains unchanged initially.

Introduce:

```text
.github/workflows/shared/control-ledger.md
```

Ledger-enabled campaigns explicitly opt in.

`control-ledger.md` preserves the existing orchestrator behavior for:

- candidate discovery;
- ranking;
- rollout limits;
- worker allowlists;
- safe-output mode calculation;
- target filtering;
- campaign policy;
- reporting.

The behavioral change is:

```text
before:
select work → dispatch-workflow

after:
select work → append-claim
```

The agent no longer directly dispatches workers.

## `append-claim` safe output

Add an `append-claim` safe output with an agent-facing API intentionally similar to `dispatch-workflow`.

Example conceptual input:

```json
{
  "worker": "optimization-token-optimizer",
  "target_repo": "githubnext/gh-aw",
  "safe_output_mode": "review",
  "safe_output_repo": "githubnext/gh-cao",
  "decision_id": "optional-derived-decision-id"
}
```

The agent supplies only the logical worker ID.

Trusted JavaScript safe-output code:

1. validates the worker against the configured worker allowlist;
2. validates target repository and output mode against `control-precompute.json`;
3. resolves the worker from trusted workflow inventory;
4. resolves the current trusted workflow commit SHA;
5. freezes the worker identity as `workflowId@sha`;
6. derives or reuses the corresponding Work record;
7. appends an immutable Claim to the ledger;
8. returns Work and Claim identifiers.

The agent MUST NOT provide the worker SHA.

Worker resolution happens once, at Claim creation time.

The dispatcher consumes the frozen worker identity and MUST NOT re-resolve it against a newer branch revision.

## Deterministic dispatcher

Add:

```text
.github/workflows/cao-dispatcher.yml
```

The dispatcher is non-agentic deterministic infrastructure.

It has access to the full CAO query and ledger APIs.

Its responsibilities are:

- query effective Work and Claims;
- identify dispatchable Claims;
- enforce repository-level capacity and policy;
- validate worker identity and revision;
- dispatch the exact workflow revision identified by the Claim;
- correlate the resulting Run with Work and Claim;
- detect stale or superseded Claims;
- create retry Claims according to deterministic policy;
- reconcile partially completed dispatches.

The dispatcher is a reconciler, not a destructive queue consumer.

Re-running it MUST be safe.

## Claim conflict resolution

Concurrent orchestrators may operate from stale repo-memory snapshots and create competing Claims for the same Work.

All Claims remain valid historical records.

The effective Claim is determined by a shared deterministic reducer.

Initial rule:

```text
1. higher valid retry generation wins
2. within the same generation, lowest full record SHA wins
```

Retry generation is derived from `previousClaimId`.

Losing Claims are projected as:

```text
superseded
```

The reducer MUST be shared by:

- `cao-dispatcher.yml`;
- CAO Query MCP;
- dashboard queries;
- reporting and computations.

Physical JSONL order, Git merge order, timestamps, and workflow completion order MUST NOT determine the winner.

## Effective Work state

Work state is derived.

Do not persist mutable status fields such as `running` or `completed`.

Example projection:

```text
no effective Claim
    → ready

effective Claim, no correlated Run
    → assigned

active correlated Run
    → running

failed Run with retry permitted
    → retryable

successful accepted Run/outcome
    → completed

unsatisfied dependencies
    → blocked
```

The ledger stores facts.

CAO queries derive state.

## Persistence

CAO Work and Claim records use the generic `repo-memory.ledger` capability provided by `github/gh-aw`.

The durable representation is append-only JSONL in repo-memory.

CAO does not implement:

- its own Git synchronization;
- its own durable SQLite database;
- distributed locks;
- non-durable cache coordination;
- a separate ledger query language.

The existing repo-memory lifecycle remains responsible for Git-backed persistence.

## CAO database integration

Work and Claim become first-class CAO canonical entities.

Add canonical collections approximately equivalent to:

```text
work
claims
```

Reuse existing canonical entities for:

```text
Attempt → runs
Result  → runs + outcomes + operational values
```

Extend canonical Run correlation with:

```text
decisionId?
workId
claimId
workerSha
```

Ledger ingestion MUST reuse the existing CAO data architecture:

```text
ledger JSONL
   ↓
CAO normalization
   ↓
canonical model
   ↓
existing storage abstraction
   ↓
IndexedDB / SQLite
   ↓
CAO Query MCP / Dashboard Language
```

Do not create a second Work/Claim query stack.

## Intelligence integration

CAO Intelligence remains a derived decision layer.

Its responsibility is:

> Determine and rank what deserves attention.

It MAY produce executable Decisions containing:

- target repository;
- campaign;
- recommended worker or operation;
- evidence boundary;
- expected benefit and cost;
- success condition;
- resource ceiling.

Intelligence MUST NOT dispatch workers.

An authorized `control-ledger.md` orchestrator may convert a Decision into Work and a Claim.

This preserves the boundary:

```text
Intelligence
"What should we consider doing?"
        ↓
Control
"What are we authorized to request?"
        ↓
Work / Claim
"What execution has been requested and assigned?"
        ↓
Dispatcher
"What should run now?"
```

## Security model

Agents may:

- query Work and Claims through CAO Query MCP;
- emit the bounded `append-claim` safe output.

Agents may not:

- choose arbitrary workflow SHAs;
- directly call workflow-dispatch APIs;
- create arbitrary Claim records;
- mutate existing Work or Claims;
- update dispatch state;
- bypass worker allowlists;
- widen review mode to live mode.

Trusted deterministic code owns:

- worker resolution;
- Claim construction;
- workflow dispatch;
- Run correlation;
- retry and stale-Claim reconciliation.

## Idempotency

Repeated orchestrator or intelligence execution must not create uncontrolled duplicate work.

Work should have a stable application-level identity derived from the durable execution intent.

Equivalent active or completed Work should be reused.

A new execution attempt is represented by a new Claim, not by mutating Work or an existing Claim.

Dispatch correlation MUST carry stable identifiers such as:

```text
work_id
claim_id
worker_sha
```

so duplicate or uncertain workflow dispatches can be detected and reconciled.

## Failure handling

The architecture favors detectable duplication over lost work.

Examples:

### Claim written, dispatcher crashes before dispatch

The Claim remains dispatchable and is discovered on the next dispatcher run.

### Workflow dispatched, dispatcher crashes before correlation is persisted

The dispatcher reconciles existing workflow runs using Claim correlation metadata where possible.

### Concurrent Claims

Both remain durable; the deterministic reducer chooses one effective Claim.

### Losing Claim already dispatched

The Run remains historical evidence but is associated with a superseded Claim and MUST NOT silently replace the effective execution result.

## Dashboard

Expose Work and Claims through existing Dashboard Language.

Initial inspection should support:

- ready Work;
- assigned Work;
- running Work;
- retryable Work;
- completed Work;
- effective and superseded Claims;
- Claims by workflow revision;
- Work-to-Claim latency;
- Claim-to-Run latency;
- retries per Work;
- duplicate/superseded execution.

No separate ledger dashboard backend is required.

## Migration

Migration is opt-in.

Existing campaigns continue using:

```text
shared/control.md
→ dispatch-workflow
```

Ledger-enabled campaigns use:

```text
shared/control-ledger.md
→ append-claim
→ cao-dispatcher.yml
```

Start with one low-risk campaign.

Compare:

- selected targets;
- Claims created;
- Runs dispatched;
- duplicate/superseded Claims;
- dispatch latency;
- retry behavior;
- failure recovery;
- resulting operational outcomes.

Only migrate the existing `control.md` path after the ledger-enabled path is proven.

## Consequences

### Positive

- durable separation between decision and execution;
- deterministic recovery after crashes;
- better concurrency behavior;
- explicit provenance from Decision to Work to Claim to Run;
- repository-level dispatch orchestration;
- easier inspection and debugging;
- stable worker revision identity;
- simpler CAO Intelligence integration;
- dispatcher behavior becomes deterministic and testable;
- existing CAO database and query infrastructure is reused.

### Negative

- worker execution becomes eventually reconciled rather than immediate;
- stale snapshots can still cause redundant Claims or even redundant Runs;
- Work/Claim reducers become part of CAO's correctness boundary;
- dispatch latency may increase;
- additional correlation metadata must propagate into worker workflows;
- repo-memory is not distributed ACID, so exact single
