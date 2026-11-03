---
title: Central Agentic Ops GitHub API Capacity Specification
description: Deterministic request-cost, quota, burst-admission, and fleet-capacity modeling for CAO data acquisition.
version: 0.1.0
status: Working Draft
editors:
  - GitHub Next
---

# Central Agentic Ops GitHub API Capacity Specification

## 1. Purpose

GitHub evidence acquisition is a finite shared resource. A collector that is
bounded for one repository can still be unsustainable when many repositories or
workflow-specific collectors become due together. This specification defines a
deterministic planning model for estimating that limit before deployment.

The model is planning evidence, not runtime authority. It does not reserve
quota, grant credentials, enroll repositories, relax a collector ceiling, or
replace runtime request accounting.

## 2. Required distinctions

A conforming model MUST keep these quantities separate:

- **request family:** one bounded acquisition operation, such as a repository
  read, Search page, GraphQL batch, timeline page, artifact lookup or download;
- **quota pool:** the independently governed core, Search, GraphQL or another
  explicitly configured capacity pool;
- **sustained demand:** modeled units across a declared horizon;
- **peak demand:** the aligned demand in one quota window after applying the
  declared collection-spreading window;
- **reviewed collector grant:** the fail-closed per-attempt request ceiling;
- **modeled capacity:** capacity using the scenario's expected request and
  runner costs;
- **guaranteed capacity:** capacity when every admitted collector consumes its
  complete reviewed grant; and
- **runtime observation:** actual requests, GraphQL points, wall time and output
  bytes from a completed attempt; and
- **shared acquisition requirement:** a reviewed source, quota pool, versioned
  primitive, exact population scope, and cache lifetime that may be unioned
  across workflow registrations for one repository.

Average daily capacity MUST NOT hide a per-minute Search bottleneck, a per-hour
core/GraphQL bottleneck, a synchronized scheduled burst, or serialized runner
time. Cache hits MAY reduce modeled demand only when the scenario declares the
hit rate; they MUST NOT enlarge a reviewed request grant or quota limit.
Runtime exact-response cache hits MUST be retained separately from metered
requests so later simulations can calibrate sharing without treating unobserved
requests as free. A hit is valid only when the request identifies the exact
reviewed acquisition requirement and its native Core, Search, or GraphQL pool;
unknown or mismatched identities remain metered.

## 3. Scenario contract

`gh cao api-cost --input FILE` accepts a JSON object with `schemaVersion: 1`:

```json
{
  "schemaVersion": 1,
  "name": "Workflow registry",
  "repositories": 100,
  "workflowsPerRepository": 1,
  "horizonHours": 24,
  "collectionWindowHours": 1,
  "quotas": {
    "core": { "limit": 5000, "reserve": 2000, "windowHours": 1 }
  },
  "operations": [
    {
      "id": "workflow-pages",
      "pool": "core",
      "cadenceHours": 24,
      "scale": { "perRepository": 2 }
    }
  ]
}
```

Each operation declares one pool, cadence, optional cache hit rate, unit cost,
request count, a `burstHours` duration, and a linear scale with `fixed`,
`perRepository`, and `perWorkflow` terms. A scenario MAY declare:

- `admissions` with a pool, reviewed grant, actual pool units, actual requests,
  cadence and collector instances per repository; and
- `runner` with minutes per collector instance, instances per repository and
  parallelism.

Nonlinear source characteristics such as pages per issue or batches of ten
issues MUST be resolved into operation coefficients by a reviewed built-in
profile or by the scenario author. The simulator does not execute collectors or
infer request shapes from source code.

Reviewed Value acquisition manifests MAY supply request-family identity to a
scenario compiler. Requirements may be deduplicated only when repository,
source, pool, primitive, scope, and cache lifetime are identical. A manifest
does not supply request counts, pagination, GraphQL points, wall time, or cache
hit rates by itself; those quantities require conservative reviewed assumptions
or canonical runtime observations.

## 4. Capacity calculation

For each operation and evaluation scale:

1. compute the count per collection cycle from its fixed, repository and
   workflow coefficients;
2. multiply by `1 - cacheHitRate`;
3. multiply by the pool unit cost and the number of cycles in the horizon;
4. compare sustained demand with the pool's non-reserved capacity across the
   same horizon; and
5. divide one aligned fleet cycle across the number of quota windows in
   `collectionWindowHours`;
6. separately divide one repository's operation across its own `burstHours`;
   and
7. use the larger demand, rounded upward, for the pool's peak check.

The modeled collector-admission limit for a serial batch is:

```text
floor((available pool units - reviewed grant) / actual pool units) + 1
```

The guaranteed limit is:

```text
floor(available pool units / reviewed grant)
```

A collector whose modeled request count exceeds its own reviewed grant has zero
supported capacity until its request shape or reviewed grant changes. Runner
capacity is `collectionWindowHours * 60 * parallelism`; exceeding it is a
separate bottleneck even when GitHub quota is sufficient.

The reported fleet maximum is the largest integer repository count satisfying
all modeled constraints. The next repository's failed constraints identify the
bottleneck. The guaranteed maximum applies the full-grant admission constraint
and MUST remain separate from the modeled maximum.

## 5. Daily File Diet profile

`gh cao api-cost --profile daily-file-diet` models the current incremental
collector. The retained scenario obtains the tracked-output population only
through the canonical `operational-value-evidence` logical source. The native
operator executes the same Dashboard Language query against a downloaded
SQLite snapshot by default or against the Go server's PostgreSQL database with
`--server URL`. It MUST NOT read `.github/value-evidence/`, an Activity
workspace, or a compiled-in population fallback. Missing, empty or unavailable
database evidence MUST fail closed instead of producing a result with invented
freshness or population. It models:

- two repository-alias reads and one workflow-source history read;
- six Search terms with a configurable page count;
- issue relationship GraphQL batches of ten;
- one or more timeline pages for every tracked issue;
- producer-run and artifact reads for newly discovered issues;
- pull detail, relationship graph and changed-file pages for mutable pulls;
- artifact metadata and archive downloads for new receipts; and
- immutable snapshot cutoff reads.

The retained scenario assumes merged pull, receipt, Git inventory and blob
measurements are reused. The `hard-bound` scenario applies the collector's
declared population and pagination ceilings. It intentionally shows whether the
source-level hard bound fits the reviewed 700-request grant; it is not a
forecast of ordinary daily traffic and does not require retained evidence.

Git blobless fetches are reported as a separate bounded transport cost because
they do not consume REST, Search or GraphQL quota. Runtime collection remains
the authority for actual metered requests, GraphQL points, wall time, output
bytes and failure state.

## 6. Conformance

Changes to a remote acquisition path SHOULD update or add a simulator profile
or scenario fixture with the same request families, bounds, cadence, reserves
and cache assumptions. Tests MUST cover the limiting quota pool, synchronized
burst behavior, collection spreading, reviewed-grant overflow and runner
serialization where applicable.

Capacity results MUST state their assumptions. Missing page counts, cache hit
rates, GraphQL point costs, wall time or quota limits remain unknown inputs;
production decisions MUST use conservative values rather than silently treating
them as zero.
