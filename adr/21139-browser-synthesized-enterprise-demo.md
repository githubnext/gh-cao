# ADR 21139: Synthesize the Pages enterprise demo in the browser

## Status

Draft

## Context

- The dashboard Pages build publishes a synthetic-only enterprise demo. `ARCHITECTURE.md` and `specs/dashboard-data.md` require it to pass raw samples through production normalization, canonical ingestion, Dashboard Language queries, and the shared views, with no presenter fixtures or separate demo query path.
- Before this pull request, the build generated the sample ahead of time. `scripts/dashboard-demo-data.mjs` produced the raw samples (this PR reduces it from 466 lines to 3), and the browser downloaded the resulting `demo-data/` shards. The PR body describes this as a ~20 MB pregenerated download.
- The PR changes the sample to 1,000 repositories, exactly 5,000 runs, three campaigns, and 4,000 installed agentic workflows, with activity across all repositories (`dashboard/site/src/data/demo/sample.mjs`). Log-normal repository rates distribute the budget with at least one run per repository. After publishing fleet inventory, seeded activity-weighted ingestion interleaves runs without replacement; randomly positioned coverage reservations put every repository into the first 3,000 runs without imposing a uniform round-robin order.
- Generation now runs in the browser data worker. `dashboard/site/src/data/demo/synthesis.js` calls the production ingestion coordinator in bounded batches (`batchSize = 1_000`). Progress is checkpointed in a receipt record `synthetic-demo:population`, so an interrupted sample resumes from committed batches.
- Completed samples are reused within the same UTC day. A new UTC day, a different generator version, a different `DATABASE_VERSION`, or a legacy synthetic/snapshot transaction deletes the isolated demo database before regeneration.
- `dashboard/site/scripts/build.mjs` bundles `sample.mjs` into the deployment. It writes no generated evidence. `DEMO_MODE` in `data-worker.js` routes repository-memory reads to synthetic content, and `startup.js` skips automatic payload-hash updates in demo mode.

Not inferable from current pull request evidence: the measured first-visit generation time, the memory budget for the browser batches, and any target for either.

## Decision

Generate the Pages enterprise demo on demand in the browser data worker, through the same production normalization and canonical IndexedDB ingestion used for collected evidence, in bounded batches. Stop pregenerating and downloading a demo snapshot at build time.

The decision is limited to the Pages demo:

- Hosted Go/PostgreSQL and local `gh cao dashboard` paths are unchanged.
- Demo state stays isolated through the `synthetic-demo` storage suffix, the `demo` service-worker cache, and the `demo-data/` source namespace.
- The receipt lifecycle (progress checkpoints, same-day reuse, reset on day, generator, or schema change) is part of the contract, as specified in `specs/dashboard-data.md`.

## Alternatives Considered

1. **Keep build-time pregeneration and publish the normalized shards for browser download.** This is the pre-PR design, implemented by `scripts/dashboard-demo-data.mjs` before this PR. The PR body names its ~20 MB download as the cost to remove.
2. Not inferable from current pull request evidence. The PR body, diff, and specs do not record a second considered or rejected option.

## Consequences

**Positive**

- The published Pages artifact no longer contains pregenerated inventory or run/detail shards (`docs/deployment-actions.md`, `specs/dashboard-data.md`). The PR body states the ~20 MB download is replaced.
- The sample can be resized in the generator without republishing a snapshot.
- Demo data uses the same normalization, ingestion, and query path as operational data, which keeps the "no separate demo query implementation" rule intact.

**Negative and tradeoffs**

- The first visit, and the first visit on each new UTC day, runs generation, normalization, and IndexedDB writes in the browser. Batching bounds each step, but the PR has no timing or memory measurements for it.
- Browser IndexedDB becomes a per-browser derived cache with more lifecycle states: incomplete, complete, reset, and legacy. Each state needs tests.
- The sample generator (`sample.mjs`, about 500 lines) now ships in the browser bundle.
- Concurrency across tabs relies on `navigator.locks` when it is available. Without Web Locks, `synthesis.js` runs the population task unlocked. Maintainers should confirm this fallback is acceptable.
- Maintainers own acceptance of this draft. Its rationale is limited to what the PR evidence states.
