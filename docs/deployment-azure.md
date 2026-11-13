---
title: Deploying the dashboard to Azure
description: Prepare a review-only Azure Container Apps deployment bundle for the hosted Go dashboard and private PostgreSQL.
---

Azure Container Apps is the supported Azure hosting path. Azure Functions
runtime, deployment commands, receipts and the local Core Tools harness are
retired. There is no native Azure apply command or replacement deployment
automation.

> [!WARNING]
> The templates are an experimental review baseline, not production acceptance.
> Complete your organization's security, privacy, network, load, cost,
> monitoring, backup, incident-response and rollback reviews before live use.

## Prepare an offline review bundle

Install the native CAO CLI and the official standalone
[Bicep CLI](https://learn.microsoft.com/azure/azure-resource-manager/bicep/install)
on `PATH`, then:

```sh
gh cao deploy init --deployment azure-container-apps --out /private/operator/containers.yaml
# Replace sample identities, networks, image references and secret names.
gh cao deploy generate --config /private/operator/containers.yaml \
  --out /private/operator/containers-plan
gh cao deploy check /private/operator/containers-plan
```

Generation compiles the emitted Bicep and produces a hash-bound review bundle
with dependency diagrams, architecture checks, parameter references and an
operator runbook. These commands do not call Azure, create resources, grant
credentials, install GitHub Apps or verify live prerequisites. Operators
separately review and execute the Container Apps templates. See
[deployment bundle generation](deployment-topology.md#azure-container-apps)
for the complete parameter and activation contract.

## Azure resources and boundaries

| Resource | Responsibility |
| --- | --- |
| Container Apps Consumption environment and one hosted Go replica | Managed HTTPS ingress; built dashboard, GitHub OAuth, query API and revision events on one origin |
| Private PostgreSQL Flexible Server | Canonical dashboard data and separately owned PostgreSQL operational state |
| Separate manual provisioning and ingestion jobs | Privileged schema provisioning isolated from runtime-only evidence ingestion |
| Key Vault and managed identities | Scoped secret resolution; no secret values in reviewed policy, parameters, logs or browser payloads |
| Azure Container Registry | Operator-approved, immutable dashboard, provisioning and ingestion images |
| VNet, delegated subnets and private DNS | Private database connectivity with separately verified ingress proxy peers |
| Log Analytics | Container logs, not canonical data or evidence of application telemetry delivery |

The base profile uses public OAuth ingress and explicitly selected Actions
snapshots. It does not provision Application Insights. Optional
session-lake and webhook-gateway infrastructure remains separately selected
and staged; it grants no runtime activation or broader authority. The base
profile does not support collector acquisition, OTLP or base ADX analytics.
Unsupported feature combinations fail closed.

Keep GitHub user/organization/team authorization explicit. The browser never
receives database credentials or GitHub tokens and never falls back to Pages,
static operational shards or IndexedDB when hosted services fail. Keep the
dashboard and OAuth on the same origin. Configure independently verified
private ingress proxy CIDRs; do not treat sample CIDRs or forwarded headers as
proof of trusted peers.

## Operator-run activation

Follow the generated runbook for the exact reviewed configuration:

1. Review instance-owned policy, OAuth registration and allowlists, secret
   references, private networking, retention and immutable image references.
   The dashboard image must contain the reviewed `cao.json` with
   `target.module: container`, `target.replicas: 1` and
   `operational-store.backend: postgres`.
2. Bootstrap ACR and prepare approved images independently. Deploy reviewed
   infrastructure with `runtimeEnabled: false`, the default; this stages
   infrastructure and manual job definitions without creating the web app.
3. Run privileged database provisioning to successful completion, then run
   runtime-only ingestion with an explicitly verified operational snapshot or
   explicit empty evidence. Neither ARM dependencies nor bundle checks run or
   verify these jobs. Do not silently substitute empty or synthetic demo data.
4. Only after independent job verification, regenerate and approve a bundle
   with `runtimeEnabled: true`, then deploy it explicitly.
5. Verify configured image digests, the registered OAuth callback, allowed and
   denied sign-in, same-origin API behavior, readiness, trusted proxy handling
   and revision-stream reconnection. TCP platform probes prove listener
   availability only.

Keep schema-owner credentials out of the web runtime and ingestion job.
Provisioning, acquisition enrollment and CAO rollout authority are separate:
infrastructure credentials and a successful deployment never authorize new
repositories, operations, safe outputs or live mode.

## Acceptance and maintenance

Bundle generation and offline checks are not live Azure acceptance, production
image assurance, backup evidence, OAuth validation or proof of backend parity.
Retain independently reviewed source/image provenance, scan and SBOM results,
deployed digest observations, job outcomes and tenant-specific operational
evidence. The retired Functions deployment receipt and release-evidence receipt
option are not part of this path.

Review database backup/restore, secret rotation, cleanup-debt draining, log
redaction, cost and rollback with the actual tenant and GitHub organization.
One minimum replica, ACR, Key Vault, PostgreSQL and logs incur costs; this is
not scale-to-zero. Private archives require their own persistent single-owner
publisher and real destination acceptance; an ephemeral container filesystem
is not a durable spool.

For local development use the existing Go/PostgreSQL server and an explicitly
acquired operational snapshot; no Azure Functions emulator, Azurite/Core Tools
harness or simulated native Azure install journey is maintained. See the
[deployment overview](deployment.md) and
[server guide](https://github.com/githubnext/gh-cao/blob/main/server/README.md).
