# ADR: Approve Trajectory-IR Collections as Whole Archive Evidence Subtrees

## Status

Draft

## Context

`specs/session-archive-grader-queries.md` extension E1 asked whether the
trajectory-IR `observations`, `toolCalls`, `provenanceEdges`, `resources` and
`reference` collections may be archived as approved evidence. Before this
change none had a generated `@evidence` family. A policy could still name them,
because approval selects any top-level `data` key, but their exclusion and
redaction were not recorded in `OmissionReasons` and they had no field states.
Several upstream graders at `github/gh-aw`
`5ac122295e5e839aa15a727ecf4762f3e680cc24` read these collections, and
observations and tool calls can carry tool arguments and outputs.

Three options were offered to the user: approve whole collections, approve a
narrow field allowlist (counts, ids, `sourceToolCallId`,
`consumedByActionIds`), or keep them excluded. The user chose whole collections
("you can store them all") and confirmed option 1.

## Decision

1. The five collections are approvable top-level `trajectory.ir` subtrees with
   generated families `ToolCalls`, `Observations`, `Resources`,
   `ProvenanceEdges` (arrays) and `Reference` (object). This is a user-directed
   privacy decision, not an agent inference.
2. Approval stays explicit per event policy and is off by default. It grants
   nothing outside these subtrees: models, cache counters, safe-output
   titles/URLs and tool arguments or outputs outside an approved collection
   remain unapproved.
3. The preservation transform becomes `cao-approved-events-v3`. v2 projections
   never recorded these families' exclusion, redaction or field states, so they
   are rejected rather than read as if the collections were absent. The Parquet
   column layout and `ContractVersion` 2 are unchanged.
4. Excluded, redacted, invalid or oversized collections keep dependent grader
   results unavailable. Nothing is derived or persisted at capture.

## Alternatives Considered

- **Narrow field allowlist.** Lower privacy scope, but approval is a closed
  policy over whole top-level subtrees, so a nested allowlist would be a new
  approval mechanism. The user did not choose it.
- **Keep the collections excluded.** Leaves every E1-gated grader unavailable.
  The user did not choose it.
- **Keep `cao-approved-events-v2`.** Would let v2 projections, which lack the
  new omission records, be reused under the new semantics and report an
  excluded collection as absent.

## Consequences

**Positive:** observation, tool-call, resource, provenance and reference
graders can be computed at query time when an operator approves the evidence;
exclusion and redaction are visible to queries.

**Negative:** approving `toolCalls` or `observations` can retain tool arguments
and other nested values, so operators must review that scope. Existing v2
archives and native ledgers fail closed and require fresh provisioning.
