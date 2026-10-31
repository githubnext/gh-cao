---
title: Central Agentic Ops Activity Specification
description: Normative collection, snapshot, data-quality, authority, and consumer requirements for CAO Activity.
version: 1.2.0
status: Working Draft
editors:
  - GitHub Next
---

# Central Agentic Ops Activity Specification

**Version:** 1.2.0
**Status:** Working Draft
**Latest Version:** https://github.com/githubnext/gh-cao/blob/main/specs/activity.md
**Editors:** GitHub Next

## Abstract

This specification defines CAO Activity as the shared collection boundary for
Central Agentic Ops. It defines Activity's authority, required inputs and
outputs, immutable snapshot identity, data-quality semantics, failure behavior,
and consumer obligations. It does not define dashboard presentation, workflow
rollout policy, or durable operational outcomes.

Implementers should read `docs/activity.md` first, then this specification,
`specs/dashboard-data.md`, and the collection implementation under `activity/`.
Historical reconstruction of campaign-defined operational-value observations is
defined by `specs/operational-value-history.md`.

## 1. Status and conformance

This document is a Working Draft and may be updated, replaced, or made
obsolete. Sections 2 through 8 are normative. This introduction, examples, and
explicitly identified notes are informative.

The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT", "SHOULD",
"SHOULD NOT", "RECOMMENDED", "NOT RECOMMENDED", "MAY", and "OPTIONAL" are to
be interpreted as described in [RFC 2119](https://www.rfc-editor.org/rfc/rfc2119).

An Activity publisher conforms when it satisfies Sections 2 through 7. An
Activity consumer conforms when it satisfies Section 8.

## 2. Role and authority

CAO Activity is deterministic collection infrastructure. It MUST collect one
bounded `gh aw logs` JSONL snapshot for reuse. It MAY produce deterministic,
rebuildable run-information and run-linked record transport shards from that
snapshot, but MUST NOT supplement or reinterpret the collected evidence.

Activity:

- MUST NOT select rollout targets or determine rollout mode;
- MUST NOT act as an orchestrator, worker, or agent;
- MUST NOT write to target repositories;
- MUST NOT treat workflow execution as proof of progress, outcome, or value;
- MUST preserve the distinction between observed evidence, derived records,
  and unavailable or incomplete evidence.

The control policy remains the authority for rollout and repository scope.
Activity MAY record resolved policy and inventory as evidence, but that record
does not grant authority.

A public control repository MUST NOT collect evidence from a non-public
repository. Before collection begins, Activity MUST validate repository
visibility: it MUST resolve the control repository's own visibility, then
either validate each explicitly allowed repository or enumerate the complete
organization or user installation scope reachable by its credentials. When the
control repository is public and any resolved repository is not public,
Activity MUST fail closed rather than collect. A visibility failure report
MUST report only the count of rejected repositories and MUST NOT include
non-public repository names or other identifying details.

## 3. Collection boundary

An Activity refresh MUST create a coherent snapshot from the inputs available
to the same workflow run. Inputs are limited to compiled workflow metadata in
the checked-out control repository, the bounded GitHub Actions workflow
registries for repositories resolved by the reviewed control policy, a
compatible prior JSONL cache entry, and the bounded run metadata, audits, and
declared artifacts acquired by one `gh aw logs` invocation.

The prior JSONL cache entry MAY be maintained internally as a carried-forward
set of wildcard shard files rather than one growing file. Shard-based caching
is a collection and ingestion efficiency mechanism only: it MUST NOT change
the evidence scope, window, or completeness of a refresh, and it MUST NOT
allow a shard's data to be skipped from the published snapshot merely because
it was skipped from re-ingestion.

The publisher MUST bound remote acquisition by repository scope, evidence
window, pagination, or another explicit limit. For each repository already
resolved into the allowed collection scope, the publisher MAY enumerate the
GitHub Actions workflow registry to obtain authoritative workflow path, display
name, active or disabled state, stable link, and native identifier evidence.
Repository-owned workflows discovered this way MUST remain standalone runtime
inventory and MUST NOT be attributed to a CAO campaign, registered as a campaign
worker, or treated as rollout authority. Campaign ownership and admission
evidence remain bounded to declarations in the checked-out control repository.

Registry enumeration MUST be paginated and report availability, completeness,
and per-repository failures. A failed registry read MUST remain partial or
unavailable evidence and MUST NOT be represented as a complete empty registry.
Deleted registry entries MUST NOT be represented as current Workflows, and an
unknown registry state MUST remain unknown rather than being inferred from
local compilation status.
Activity MUST NOT invoke per-workflow run-list, per-run detail, Jobs, or another
remote fallback collector to fill missing runtime fields.

## 4. Snapshot contract

The scheduled and manually dispatchable Activity workflow is the only
publisher of the shared Activity cache. Each published snapshot MUST use an
immutable identity derived from the publisher's workflow run ID and run
attempt. A consumer that dispatches Activity MUST restore the exact snapshot
for the completed run and attempt rather than an unspecified latest snapshot.

The snapshot consists of the JSONL produced by `gh aw logs`, compact
run-information shards, detailed record shards, and rebuildable projections.
Run-information shards MUST contain only Campaign, Repository, Workflow, and Run
records. Record shards MUST contain only Domain, Tool, Audit, and Issue records.
Every record-shard transport record MUST carry its canonical Run identity.

Normalized run-information and record shards MUST use JSONL files with a
`.jsonl` extension. The first line MUST be a metadata envelope and every
following line MUST contain exactly one canonical record and its collection.
Publishers MUST NOT emit normalized `.json` shards or a single JSON object that
contains a complete canonical batch.

Consumers MUST stream normalized JSONL from the response body and commit
bounded record batches while bytes continue to arrive. They MUST NOT call
`Response.json()`, buffer the complete response text, or materialize the full
transport shard before writing canonical storage. This constraint bounds peak
memory independently of snapshot size for browsers with constrained heaps,
including iOS WebKit. A missing response stream MUST fail closed rather than
fall back to full-file parsing.

The two phases MUST be a complete partition of the source records. Publishers
MUST omit a phase shard when it contains no records, so the run-information and
record shard sets MAY contain different filename stems. Consumers MUST validate
the phase labels before phased ingestion and MUST fall back to a complete
compatible transport or fail closed when the phased set is invalid.

Phase filenames MUST preserve the source-shard ordering prefix before their
content and normalization hashes. Publishers and consumers MUST process both
phases in that order so a later observation of the same canonical entity wins
over an earlier observation; content-hash order MUST NOT determine precedence.

A consumer MUST load all run-information shards before record shards and MAY
expose the resulting Run queries while record ingestion continues. This
intermediate state is partial: it MUST NOT be represented as a complete
snapshot, and record-dependent queries MUST remain unavailable or stale until
the record phase succeeds. Failure or cancellation of the record phase MUST NOT
invalidate already committed Run information, but the consumer MUST retry the
missing record phase rather than mark the snapshot complete. Consumers MUST
determine availability, completeness, freshness, and scope for their own use
and MUST NOT infer those properties from row counts.

The phase split optimizes time to first useful Run query, avoids rewriting
unchanged canonical records, and omits empty phase payloads. Phase metadata and
direct record-to-Run identity add bounded overhead. Consumers SHOULD avoid
background record transfer on metered or data-saver connections.

The concrete cache file and identity rule are defined by
[`activity/README.md`](../activity/README.md). Changing the file or identity
rule is a contract change and MUST be reviewed with affected consumers. Any
internal wildcard shard directory used to carry forward cache entries across
runs is an implementation detail of that cache file; it MUST NOT be treated as
an alternate or partial published snapshot by a consumer.

Shard compaction MUST attribute every record to exactly one declared repository
group. A record without a repository MUST be attributed only by a run identity
that resolves to a single repository across the retained shard snapshot. The
compactor MUST fail closed, without replacing the original shards, for
contradictory identities, missing or conflicting parent-run identities, unknown
records, or mixed shards containing undeclared repositories. After a fresh
per-repository download, the collector MAY instead exclude records with a
verified foreign repository and repository-free GitHub API rate-limit audits
from the scoped shard directory; the other fail-closed conditions MUST still
apply. Any such exclusion MUST be counted and MUST mark the collection partial
rather than complete. Compaction of restored cache shards MUST use the strict
behavior.

The cache is a reusable transport and efficiency mechanism. It MUST NOT be
represented as durable historical authority.

## 5. Data quality

For every evidence class used by a consumer, the consumer MUST establish:

- **availability:** whether usable evidence was obtained;
- **completeness:** whether the declared scope and evidence window were fully
  evaluated;
- **freshness:** whether the observation satisfies the consumer's required
  time boundary; and
- **coverage:** the evaluated portion of the declared population when it can
  be measured.

An empty collection MUST NOT imply a complete zero unless the evidence class is
available, complete, and fresh for the requested scope and window. Unknown,
missing, stale, and partial states MUST remain distinguishable from zero.

## 6. Evidence precedence

Native `gh aw` logs, audits, and declared artifacts are authoritative for the
fields they provide. Consumers deriving records from them MUST retain
sufficient identity and provenance to relate an observation to its repository,
workflow, run, and evidence source when those dimensions are available.

When audit generation is enabled, a publisher MAY retain bounded normalized
audit aggregates and non-secret agent, model, runtime, compiler, firewall, and
gateway identifiers. It MUST NOT publish raw prompts, messages, tool arguments,
authorization values, credentials, response bodies, secret values, or
transcripts from audit or usage artifacts. Nested values, collections, and
strings MUST have explicit publication bounds.

Retained records MUST NOT overwrite a more authoritative artifact-derived
field with a weaker observation. They MUST remain distinguishable from
observations collected during the current refresh.

Credentials, tokens, private keys, and secret values MUST NOT appear in the
snapshot, telemetry ledger, or derived sources. Credential telemetry MAY use a
stable non-secret alias, role, or credential class.

## 7. Failure and retained snapshots

When primary log collection fails, the publisher MAY preserve a compatible
prior JSONL snapshot. It MUST fail the workflow rather than publish a
post-processed failure state.

The publisher MUST NOT respond to a failed `gh aw logs` invocation by issuing
per-workflow run-list, per-run detail, or Jobs API fallback requests. Missing
run fields MUST remain unavailable rather than be inferred from a weaker,
independently collected source.

If neither primary collection nor a compatible retained snapshot provides
usable evidence, the publisher MUST fail. Missing scope, credentials, access,
or required evidence MUST fail closed.

## 8. Consumer obligations

An Activity consumer:

- MUST validate that the snapshot covers its required repository scope and
  evidence window;
- MUST honor availability, completeness, freshness, and coverage metadata;
- MUST NOT render missing or incomplete evidence as an authoritative zero;
- MUST fetch or explicitly report missing evidence when the snapshot cannot
  satisfy its contract;
- MUST NOT infer rollout or target-writing authority from snapshot contents;
- MUST preserve evidence provenance through derived records where supported by
  the source; and
- SHOULD reuse one compatible snapshot instead of repeating equivalent remote
  collection.

Dashboard source vocabulary and source-level semantics are defined by the
[Dashboard Language Specification](../docs/dashboard-language-specification.md).
Dashboard presentation and attention semantics are defined by the
[Dashboard Specification](dashboard.md).

## Informative references

- [CAO Activity](../docs/activity.md) explains the workflow and its dashboard
  relationship for operators and contributors.
- [Data Acquisition Audit](data-acquisition-audit.md) inventories current API,
  indexing, and caching behavior; it is informative rather than normative.
