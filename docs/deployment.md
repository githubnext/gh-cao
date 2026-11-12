---
title: About deployment options
description: Compare the ways you can host the Central Agentic Ops dashboard, and choose the option that fits your data, audience, and infrastructure.
---

> [!WARNING]
> **Experimental:** All deployment options are experimental. Interfaces, configuration names, infrastructure templates, and procedures can change between releases without a migration path. No option is certified for production use. Before you expose a deployment to real users or data, complete your own security, compliance, privacy, network, monitoring, incident-response, and rollback reviews.

<span id="about-deployment-options"></span>

The [control plane and dashboard](architecture.md#control-plane-and-dashboard)
are deployed separately. The control plane runs in GitHub Actions; this guide
compares where to serve the dashboard and run its queries.

Choosing a deployment option never changes campaign policy, rollout mode, credentials, or target authority. Those settings stay in the reviewed `.github/workflows/cao.json` file in your control repository. For more information, see [Control policy](configuration.md#control-policy) and [Roll out a campaign](rollout-and-routing.md).

## No deployment needed for a local dashboard

For your own investigation, install the release extension and run
`gh cao dashboard --repo OWNER/REPOSITORY` with your locally authenticated
GitHub CLI. It bundles the server, dashboard, and collector, serves on loopback,
and automatically refreshes a private canonical PostgreSQL cache on your machine.
No CAO cloud deployment, Azure setup, hosted dashboard, Redis,
separate Node.js or gh-aw installation, or control-repository setup
is required. GitHub remains the online, authenticated evidence source.

Open the printed **Dashboard:** URL and stop with **Ctrl-C**; the cache remains.
Collection is bounded, not a complete historical backfill. Start with the
[local quickstart](setup-quickstarts.md#open-a-local-dashboard-no-deployment);
the hosted choices below are optional, not prerequisites.

## Comparing the options

Dashboard hosting and collection credentials are separate choices. Production
serves the dashboard through the same-origin Go/PostgreSQL host. Local
development uses the same Go server and views over a canonical PostgreSQL namespace
snapshot. Pages is only a synthetic enterprise demo, never a real-data host.
PostgreSQL also provides separate operational state by default.

| Option | Dashboard host | Where queries run | Who can sign in | Infrastructure you operate |
| --- | --- | --- | --- | --- |
| [Synthetic enterprise demo](deployment-actions.md#building-the-synthetic-pages-demo) | GitHub Pages | Web Worker over isolated browser IndexedDB | No operational sign-in or live services | Documentation/demo workflow only; no operational artifacts |
| [On-demand local dashboard](setup-quickstarts.md#open-a-local-dashboard-no-deployment) | Bundled loopback Go server | Go over a private PostgreSQL cache | Local capability URL; collection uses authenticated `gh` | Podman, or an explicitly configured provisioned database |
| [Local development](dashboard-data-ingestion.md#use-local-postgresql) | Loopback Go server | Go over canonical PostgreSQL | Private local capability URL | Explicitly acquired operational snapshot |
| [Azure Functions](deployment-azure.md) | Azure Functions | On the server, over PostgreSQL | Explicitly allowed GitHub users, organizations or teams | Function App, Elastic Premium plan, PostgreSQL, Key Vault, ACR, runtime storage, jobs and monitoring |
| [Azure Container Apps](deployment-topology.md#azure-container-apps) | Container Apps Consumption, one hosted Go replica | On the server, over PostgreSQL | Explicitly allowed GitHub users, organizations or teams | Container Apps environment, PostgreSQL, Key Vault, ACR, jobs and Log Analytics; no Functions, runtime Storage or Premium plan |

## Choosing an option

For shared operational hosting, rather than the on-demand local command:

1. **Prepare the control plane and evidence.** Follow
   [Set Up CAO](setup-quickstarts.md), then collect a bounded Activity snapshot.
   Setup configures monitoring, not Pages.
2. **Choose collection credentials for the scope.** Monitoring the control
   repository and confirmed public targets needs no App/PAT setup. Prefer
   private GitHub Apps for durable cross-repository access; use fine-grained
   PATs only when eligible and explicitly accepted. See
   [Choosing a credential profile](deployment-actions.md#choosing-a-credential-profile).
3. **Use Go/PostgreSQL for shared operational hosting.** Choose an Azure topology
   or your own container platform. Use Go/PostgreSQL for local development and
   snapshot investigations. Use Pages only to explore the synthetic demo.

Azure runs the host-neutral Go service from the `server/` directory, with authentication, authorization, cross-site request forgery (CSRF), webhook, rate-limit, and logging protections. You can also deploy that service on your own container platform; you are responsible for secret management, ingress, and delivery.

**Server-backed means the Go server serves the dashboard itself.** Built HTML
and assets, GitHub sign-in, query APIs, and revision events use the same hosted
origin. Forward the entire application to the Go handler, not just API paths.
Do not publish a separate Pages or static-hosted frontend for hosted mode.
The browser uses server-side PostgreSQL queries; a hosted failure does not
switch to browser storage or the Pages deployment.

> [!NOTE]
> Your credential profile still matters for server-backed deployments. The CAO Activity workflow collects their evidence in GitHub Actions, using the profile that you configure. Dashboard users sign in separately, through a GitHub OAuth app. The optional Azure collection profile requires a GitHub App and doesn't support PATs.

## About the shared data flow

Every option uses the canonical evidence model and declarative views, not a
separate demo data model or presenter fixtures. Pages generates synthetic raw
samples and passes them through production normalization and worker ingestion;
it never consumes operational artifacts. [Data ingestion](dashboard-data-ingestion.md) owns the collection,
verified payload, retention, and backend-processing details;
[Data model](dashboard-data-model.md) owns entities and identities.
The optional [Azure collection profile](deployment-azure.md#using-the-optional-collection-profile)
uses server-side workers driven by GitHub App webhooks instead of relying only
on scheduled Activity snapshots.

## What every option guarantees

- **Evidence is not authority.** Viewing a result grants no execution or write permission. Pages is read-only; optional canvas commands and hosted CLI actions have separate authorization and approval requirements. See [Know the boundary](dashboard.md#know-the-boundary).
- **Rebuildable dashboard data.** Browser IndexedDB and canonical PostgreSQL hold derived evidence projections. Protected operational state such as audit history, queues, and revocation records is separate and must not be treated as a disposable cache. See the [operational-storage contract](https://github.com/githubnext/gh-cao/blob/main/specs/server-operational-storage.md).
- **Verified payloads.** Each payload file is checked against `payload-hashes.json` before it is parsed. A missing manifest, a hash mismatch, an unsafe path, or a malformed record stops ingestion.
- **No exposed secrets.** Secrets never appear in the dashboard site, API responses, URLs, or logs.

## What no option guarantees

- **Live data.** Operational evidence is only as fresh as its last successful collection and ingestion. The daily Pages demo rebuild is independent and says nothing about operational freshness.
- **Availability.** CAO adds no availability service-level agreement (SLA), backup service, or disaster-recovery objective beyond what the underlying platform provides.
- **Compliance records.** The dashboard is not a compliance record. For compliance evidence, use GitHub Actions run history, your checked-in policy, your infrastructure definitions, and platform audit logs.
- **Automatic workflow control.** Evidence and recommendations do not stop or govern workflows. To stop campaigns, follow [Emergency stop](operations.md#emergency-stop).

## Next steps

- For operational snapshot acquisition and the separate demo, see [Actions artifacts and the Pages demo](deployment-actions.md).
- To plan your control repository's topology, ownership, and enrollment, see [Deployment and governance](deployment-and-governance.md).

## Further reading

- [Dashboard](dashboard.md)
- [Monitor and recover](operations.md), including [Operational artifacts and the Pages demo](operations.md#publishing-pages-reports) and [Incident response](operations.md#incident-response)
- [Optional observability](configuration.md#optional-observability) for the agentic workflows
- [Glossary](glossary.md)
- [`server/README.md`](https://github.com/githubnext/gh-cao/blob/main/server/README.md) and [`server/SECURITY.md`](https://github.com/githubnext/gh-cao/blob/main/server/SECURITY.md), the detailed references for the Go server
