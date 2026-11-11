---
title: Actions artifacts and the Pages demo
description: Keep operational evidence in bounded Actions artifacts and publish only the synthetic enterprise demo to GitHub Pages.
---

> [!WARNING]
> **Experimental:** Workflow names, payload layouts, and interfaces can change. Review evidence access, retention, and deployment boundaries before using real data.

## About the Actions and Pages boundaries

GitHub Actions runs the control plane and collects operational evidence.
It is not a real-data Pages hosting option:

1. **CAO Activity** collects bounded evidence and publishes the
   `cao-activity-index` artifact, including SQLite and normalized shards.
2. **CAO Dashboard** builds and retains an operational
   `central-agentic-ops-dashboard` artifact for authorized consumers. It does
   not deploy the artifact to Pages.
3. Production dashboards use the same-origin **Go/PostgreSQL** host. Local
   development and analysis use the same Go server with a real **SQLite**
   snapshot.
4. The catalog's **Documentation Pages** workflow builds documentation and a
   **Synthetic enterprise demo**. It never downloads operational artifacts.

The demo exercises the production canonical pipeline, Dashboard Language
queries, and shared views through a browser Web Worker and IndexedDB.
It has no live services and is not evidence about any control repository.
See [Deployment options](deployment.md) for operational hosting.

## Choosing a credential profile

Collection credentials are independent of dashboard hosting. Choose the
least-privilege profile that can read the evidence you need. Installing
monitoring does not enable campaigns or grant target-write access.

| Profile | Best for | Credentials | Limitations |
| --- | --- | --- | --- |
| [Built-in workflow token](authentication.md#public-only-monitoring-quickstart) | Monitor the control repository and confirmed public remote targets | Actions `GITHUB_TOKEN`; no App/PAT setup | Remote scope must be explicitly public-only; restricted evidence remains partial. No cross-repository writes. |
| [GitHub Apps](control-plane-authentication.md#configure-private-github-apps) | Durable private/internal and cross-repository access | Separate private read and write Apps | Approved, selected-repository installations; write access remains limited to approved outputs. |
| [Fine-grained PATs](control-plane-authentication.md#configure-a-fine-grained-token) | Evaluation when suitable Apps are unavailable | Owner-scoped read/write tokens | User-bound, expiring, manually rotated, API-limited; tokens from the same user share quota. |

Explicit App and PAT modes fail closed when selected credentials or mappings
are missing. They do not silently switch profiles. See
[runtime credential selection](authentication.md#runtime-credential-selection).

## Preparing operational evidence

Complete [Set Up CAO](setup-quickstarts.md), review exact collection scope and
authentication, and publish reviewed monitoring files. Setup does not configure
Pages or ask for public-publication consent. Keep a private control repository
when evidence is non-public.

CAO Activity runs hourly, on manual dispatch, and on relevant default-branch
pushes. Each successful default-branch Activity run triggers a Dashboard build.
Automatic Dashboard builds restore the exact triggering run and attempt's cache,
falling back only to that run's artifact; they cannot substitute another run.
Manual builds may reuse the latest successful retained snapshot.

Failed or cancelled collection does not publish an invalid replacement.
Capacity-limited collection may publish explicitly partial coverage; missing
selected credentials, failed visibility checks, or invalid data remain failures.
Before the first collection, a manual Dashboard build can produce partial local
inventory with an empty activity set. This is
[bootstrap evidence](dashboard-data-ingestion.md#before-the-first-activity-snapshot),
not proof that remote repositories have no activity.

### Reading a snapshot locally

Select a successful Activity run and download its artifact with Actions read
access:

```bash
gh run download RUN_ID --repo OWNER/CONTROL --name cao-activity-index --dir .cao
npm run dashboard:local -- --sqlite ../.cao/gh-aw-logs.sqlite
```

The npm command runs from a catalog checkout; relative Go paths resolve from
`server/`. The selected snapshot is served read-only. For native CLI queries,
use `gh cao query --database .cao/gh-aw-logs.sqlite ...`.

If an operator already provides a trusted operational payload location,
`DASHBOARD_DATA_URL` or `gh cao download --url` must name its
`payload-hashes.json` directly. There is no default Pages source, directory
discovery, or implied hosted export endpoint. See
[Download and query a snapshot](dashboard-data-ingestion.md#download-and-query-a-snapshot).

## Building the synthetic Pages demo

```bash
npm run dashboard:pages:build
npm run dashboard:pages -- --base-path /gh-cao/
```

The builder generates synthetic enterprise raw samples and applies production
normalization and canonical ingestion. It accepts no real-data input option.
The output includes no SQLite snapshot or operational agent summary. A
declarative **Synthetic enterprise demo** callout identifies the sample, and
data lives under `demo-data/`, not a root manifest. Browser storage keys use
the `synthetic-demo` suffix and service-worker caches use `demo`.
New data URLs prevent old same-origin service-worker caches from returning
previous real-data payloads.

The enterprise sample includes two campaigns and standalone workflows across
12 repositories, with 672 runs spanning two weeks. It exercises mixed outcomes,
dispatches, models and token costs, tool and skill calls, firewall decisions,
issues and pull requests, experiments, graders/evals, accepted Value evidence,
collection deferrals, catalog updates, and synthetic repository-memory files.
All observations pass through the production adapters and canonical ingestion;
none are presenter fixtures. Missing hosted-only commands, administration, or
live telemetry remain unavailable rather than being fabricated for the demo.

`docs.yml` rebuilds the documentation and demo daily, independently of
operational Activity or Dashboard freshness. Its deployment uses the ordinary
Pages environment and OIDC permissions; agents receive no deployment authority.
`cao-dashboard.yml` is not a Pages publisher, and there is no
`control-plane.campaigns.dashboard.deploy` setting.

This removes live-data publication and implicit acquisition paths, not the
IndexedDB, Web Worker, service-worker cache, or cross-adapter test burden.
Isolation, offline recovery, quota behavior, and query conformance still matter.

## Monitoring and retention

- Inspect Activity and Dashboard workflow logs for artifact, integrity, and
  coverage failures; inspect Documentation Pages separately for demo failures.
- Keep operational artifacts bounded and access-controlled. They remain needed
  for authorized local analysis, canvas use, and hosted ingestion.
- Actions caches are evictable and artifacts expire. Neither is an archive or
  evidence of complete collection. GitHub history and retained source evidence
  remain authoritative.
- Demo build time is not operational freshness. A healthy demo says nothing
  about collection or hosted service health.

## Migrating an older Pages deployment

Updating source or stopping its old publisher does not scrub the deployed site.
Rebuild and redeploy the synthetic demo to replace current Pages artifacts, or
unpublish the old site. Review historical deployment artifacts, downloaded
copies, and any sensitive-data exposure separately.

The separate demo namespace does not use former real-data browser databases.
Those databases remain disposable but are not automatically deleted, nor are
caches outside the demo's scope. Explicitly clear affected site data when
required; do not treat a namespace change as secure erasure.

## Further reading

- [Deployment options](deployment.md)
- [CAO Activity](activity.md)
- [Data ingestion](dashboard-data-ingestion.md)
- [Monitor and recover](operations.md)
