---
title: Deployment bundle generator
description: Generate and check review-only Azure deployment bundles using native Go and the TOSCA standard.
---

`gh cao deploy init`, `generate` and `check` provide native Go, offline
configuration generation within the deployment command. A small YAML file
selects features and backend parameters. The solver derives its bounded component
graph from the generated **TOSCA Simple Profile 1.3** node-template requirements,
validates closure and cycles, and applies a deterministic standard-library
topological sort. It does not parse arbitrary TOSCA or cloud archives and needs
no TOSCA or graph Go library. There is no Node.js runtime, cloud call or automatic
deployment.

Generation requires [Microsoft's official Bicep CLI](https://learn.microsoft.com/azure/azure-resource-manager/bicep/install)
on `PATH`. Every emitted `.bicep` file is compiled with
[`bicep build --no-restore --stdout`](https://learn.microsoft.com/azure/azure-resource-manager/bicep/bicep-cli#build),
using the established compiler, type checker and built-in linter. Diagnostics
are surfaced; compiler errors or a missing tool fail generation without
publishing the output folder. Validation uses a private temporary directory,
disables automatic module restore and makes no Azure calls. It does not prove
subscription permissions, regional availability or live deployment acceptance.
`init` and offline dependency/drift `check` do not require the Bicep CLI.
The standalone `bicep` executable must be on `PATH`; installing only the
`az bicep` wrapper is not sufficient. No compiler is downloaded automatically.

```sh
gh cao deploy init --out /private/operator/requirements.yaml
# Edit the example's identities, feature choices and references.
gh cao deploy generate --config /private/operator/requirements.yaml \
  --out /private/operator/azure-plan
gh cao deploy check /private/operator/azure-plan
```

## Deployment samples

The same input samples are shipped by the command and exercised by Go tests:

| `init --deployment` | Hosting | PostgreSQL | Additional managed services |
| --- | --- | --- | --- |
| `single-vm` | One persistent VM; hosted Go + OAuth + direct TLS | On that same VM, socket/loopback only | Existing external Key Vault; no new managed database |
| `minimal-azure` | One persistent hosted Go VM | Private Azure Flexible Server | Private DNS; existing external Key Vault |
| `azure-functions` | Existing Functions baseline, unchanged Bicep | Private Azure Flexible Server | ACR, target vault, runtime storage, jobs and monitoring |
| `azure-container-apps` | One hosted Go replica on Container Apps Consumption, managed HTTPS | Private Azure Flexible Server | ACR, target vault, provisioning/ingestion jobs and Log Analytics; no Functions, runtime Storage or Premium plan |
| `full-azure` | Functions plus separate archive/receive/dispatch hosts | Private Azure Flexible Server | Collector/backfill, Blob session lake, two separate ADX destinations, gateway, external OTLP |

```sh
gh cao deploy init --deployment single-vm --out /private/operator/vm.yaml
gh cao deploy generate --config /private/operator/vm.yaml --out /private/operator/vm-plan
```

Samples use synthetic identities, PUBLIC test keys, documentation-only networks
and secret names. Replace them before any separately approved deployment.
Even the single-VM profile keeps hosted OAuth, persistent PostgreSQL operational
state, distinct database authorities and vault-backed credentials; it does not
silently substitute SQLite or volatile memory. VM runtime activation, disk
setup, schema bootstrap, TLS and evidence ingestion are explicit runbook steps.
Existing vaults are external prerequisites, not free or verified resources.

The compact profiles deliberately support Actions snapshots only. Select the
Functions profile for optional collection, archive, gateway, ADX and telemetry;
incompatible combinations fail rather than carrying hidden full-stack services.
These are operational artifacts acquired with authorized Actions access, not
the public synthetic Pages demo. Production serves the complete Go/PostgreSQL
application on one origin; local Go/SQLite uses the same views over a real
read-only snapshot. No topology uses Pages as an operational data host.

### Azure Container Apps

```sh
gh cao deploy init --deployment azure-container-apps --out /private/operator/containers.yaml
gh cao deploy generate --config /private/operator/containers.yaml \
  --out /private/operator/containers-plan
gh cao deploy check /private/operator/containers-plan
```

This additive profile replaces the Functions HTTP host, runtime Storage and
Elastic Premium plan with a Container Apps **Consumption** environment. The
same Go `serve-hosted` process serves built assets, OAuth, server-side PostgreSQL
queries and revision events on one origin. Private PostgreSQL, separate
privileged provisioning and runtime-only ingestion identities, scoped Key Vault
references and managed-identity ACR pulls remain. Log Analytics receives
container logs; Application Insights and Redis are not provisioned.

Supply immutable `dashboardImage`, `provisioningImage` and `ingestionImage`
references in the selected `containerRegistryName`, plus `keyVaultName`,
`deploymentName`, `location`, OAuth allowlists, administrators and explicit
retention. Six input-vault secret references are required, including
`githubWebhookSecret` for the existing hosted runtime. The dashboard image must
contain the operator's reviewed `cao.json` with `target.module: container`,
`target.replicas: 1` and `operational-store.backend: postgres`.

The fixed VNet is `10.45.0.0/16`, with separate Container Apps and PostgreSQL
delegated subnets and private database DNS. `trustedProxyCidrs` must identify
independently verified private IPv4/loopback ingress peers, not clients; sample
CIDRs are not live peer evidence. Managed ingress requires HTTPS, forwarding
privately to HTTP port 8080. No trust bypass or alternate public container port
is provided. Verify platform proxy behavior and revision-stream reconnection.

The default `runtimeEnabled: false` stages infrastructure and manual job
definitions without creating the web app. Separately bootstrap ACR and build
approved images; deploy infrastructure, run provisioning to successful
completion, then explicitly selected snapshot/empty ingestion. Only then
regenerate and approve a bundle with `runtimeEnabled: true`. ARM dependencies
do not execute or verify jobs. Follow the generated runbook for exact outputs,
OAuth callback registration, deployed digest checks and application readiness;
TCP platform probes establish only listener availability.

This profile currently supports public OAuth ingress and Actions snapshots only.
Unsupported optional features fail closed. It does not change `gh cao deploy
azure --apply`, which remains Functions-only. Consumption with one minimum
replica, ACR, Key Vault, PostgreSQL and logs still incur costs; this is neither
scale-to-zero nor production acceptance.

## Architecture rule checker

Generation and `deploy check` apply deterministic rules based on the validated
profile's declared capabilities and requirements. `RULES.md` and `rules.json`
are included in the hash-bound bundle; CLI output reports errors and warnings.
Strict errors block generation. Warnings record explicit tradeoffs.

Strict rules require valid Key Vault references for all required role secrets,
no literal secret parameters, hosted OAuth/allowlists, TLS 1.2+ and no public
PostgreSQL endpoint. All optional host credentials/certificates also require
vault references; the generator grants no secret access automatically.

Advisories cover public services without VNet isolation, public edge controls,
managed identity, single-VM failure domains, missing database HA, disks without
backups, vault governance/purge protection and duplicate paid ADX destinations.
They follow [Azure Well-Architected security](https://learn.microsoft.com/azure/well-architected/security/checklist),
[secret-management](https://learn.microsoft.com/azure/well-architected/security/application-secrets)
and [reliability](https://learn.microsoft.com/azure/well-architected/reliability/checklist)
guidance. Severity is this generator's policy, not a Microsoft certification.
Paid-destination tradeoffs also reference the framework's
[cost guidance](https://learn.microsoft.com/azure/well-architected/cost-optimization/checklist).

This is a bounded checker for generated CAO profiles, not a generic arbitrary
Bicep/TOSCA scanner or a live Azure assessment. It does not prove vault existence,
grants, certificate delivery, backups, actual TLS configuration or runtime
effectiveness. Existing-vault and activation prerequisites remain unverified.

The destination's parent must exist; generation never overwrites an existing
file or folder. Files are owner-only. Requirements contain non-secret
parameters and names in an existing input Key Vault, never secret values.
Only Azure is implemented; unknown backends, fields, features, parameters and
unmet requirements fail instead of silently choosing another implementation.

## Generated folder

| File | Purpose |
| --- | --- |
| `MANIFEST.md` | Mermaid architecture and resolved dependency diagrams, with credential boundaries and external/staged gates |
| `topology.yaml` | Generated TOSCA 1.3 types, requirements and service topology |
| `topology.json` | Deterministic projection of the TOSCA requirement graph and bootstrap order |
| `manifest.json` | SHA-256 bindings for the output and TOSCA graph/profile identity |
| `RULES.md`, `rules.json` | Strict architecture failures and advisory tradeoffs with Microsoft guidance |
| `requirements.yaml`, `parameters.json` | Normalized feature choices and Azure parameters with Key Vault references |
| `*.bicep` | Byte-identical copies of the authoritative Azure baseline |
| `README.md`, `TODO.md`, `GITHUB-APPS.md` | Operator instructions, evidence gates and application configuration |
| `AGENT-MCP.md` | GitHub Actions agents' bearer-token plus OIDC flow, permissions, repository/default-branch checks and host activation limits |
| `scripts/` | Bicep validation and read-only what-if; no apply |
| `optional/`, `OPTIONAL-FEATURES.md` | Additional infrastructure and activation guidance when selected |

TOSCA is the deployment representation, not a new authorization source or ARM
scheduler. The CLI supports its generated CAO profile; it does not accept or
execute arbitrary imported TOSCA, CSARs, lifecycle operations or user scriptlets.
The dependency diagram and JSON report derive from TOSCA requirement assignments. The architecture
view additionally marks optional trust flows and external activation gates;
these reference paths do not add enabled resources or capabilities.

### GitHub Actions agents and MCP

Every manifest includes the optional Actions agent -> OIDC issuer -> hosted MCP
path. The current hosted contract requires **both** `Authorization: Bearer
GITHUB_TOKEN` and `X-GitHub-OIDC-Token`. Mint provenance with `id-token: write`
for audience `https://cao.githubnext.com`; the job token needs Actions,
contents, issues and pull-request read permissions.

`AGENT-MCP.md` documents the exact issuer, signature/time validation, live
repository identity/default-branch and permission probes, runner connectivity,
read-only scope and rejection tests. Short-lived job credentials stay in
process memory/environment, not bootstrap secret files or vaults.

Compatible `serve-hosted` deployments must separately opt in with
`--mcp-enabled` and `CAO_MCP_ACTIONS_REPOSITORY`. The unchanged Functions
entrypoint does **not** wire MCP, including in the full Azure sample;
this is explicitly marked as an activation limitation, not silently enabled
by the diagrams. MCP grants no admission, token-broker, dispatch or write authority.

## Feature choices

```yaml
version: 1
backend: azure
features:
  deployment: azure-functions # single-vm | minimal-azure | azure-functions | azure-container-apps
  acquisition: actions # actions | collector, mutually exclusive
  ingress: public # public OAuth | private, external connectivity required
  analytics: none # none | adx, a paid destination, not an exporter
  telemetry: none # none | otlp, an external HTTPS collector
  sessionDataLake: false # dedicated persistent host + private Blob + paid ADX
  webhookGateway: false # isolated receive and dispatch hosts
```

The example supplies the required Azure section. Every non-secret parameter in
the embedded compiled Azure contract is available under `azure.parameters`.
Types, ranges and allowed values are checked. Bicep owns omitted defaults,
including ARM expressions. Resource names, image tag, OAuth client ID and
explicit retention are required. The callback host and an empty organization
list are supplied when omitted; allow at least one organization, team or user.
`azure.secrets` maps secure parameter names to secret names under
`azure.secretVaultId`. Optional secure parameters use the same mechanism.

Collector mode additionally requires `collectorImage`, numeric
`collectorGithubAppId`, `collectorControlRepository`,
`collectorNativeInventoryDigest`, nonempty `githubAdminUsers`, and secret
references for `collectorPrivateKey` and `githubWebhookSecret`. Provision the
exact reviewed inventory independently before worker startup. Keep the
baseline's manual snapshot-ingestion job stopped in collector mode.

OTLP needs a credential-free HTTPS `otlpEndpoint`; optionally enable
`otlpLogsEnabled` and reference `otlpHeaders` through Key Vault.
Private Functions ingress needs independently approved connectivity; disabling
public access alone does not provision a private endpoint or a working route.

## Additional feature infrastructure

When selecting the lake or gateway, add:

```yaml
azure:
  hosts:
    sshPublicKey: "ssh-ed25519 YOUR_REAL_PUBLIC_SSH_KEY"
    archiveAllowedSourceCidrs: [203.0.113.0/24]
    archiveRepositories: [your-org/your-repo]
    gatewayAllowedSourceCidrs: [192.0.2.0/24]
```

Merge these fields into the example; replace documentation-only CIDRs and
the public key. Include archive fields only with `sessionDataLake`, and gateway
CIDRs only with `webhookGateway`. Optional `administrator` and `vmSize` override
`caoadmin` and `Standard_B2s`. Explicit location is required.

The lake uses one persistent VM/managed disk, dedicated private Blob and paid
managed-identity ADX. The gateway has separate TLS receiver and private
dispatcher VMs/identities. A separate peered VNet, deny-by-default role subnets,
PostgreSQL private DNS link and explicit NAT egress preserve baseline subnets.
Review the fixed `10.43.0.0/16` network for overlap.

Hosts remain **staged**, not running with invented approvals. Archive
native-collector integration, source/content/reader authority and real Blob/ADX
acceptance remain independent gates. The current Blob runtime needs explicit
Entra application credentials; VM/ADX managed identities do not replace them.
Gateway Apps, reviewed exact-revision policy and credential isolation remain
separate activation prerequisites. Turning switches off is not teardown,
revocation or evidence deletion.

## Checking and embedded source maintenance

Checking re-solves the TOSCA requirement graph, verifies the dependency
contract/order/cycles, feature/parameter/secret consistency and file hashes,
and rejects stale/orphaned outputs and symlinks. External prerequisites remain
unverified; a valid template is not live readiness, deployment acceptance,
compliance or authorization.

Keep the generating CLI release when checking older bundles. The CLI embeds
deterministic, reviewable snapshots of every bundled template, example,
script and runbook, including the canonical Bicep sources and ARM parameter
contract. It never needs a source checkout or downloads resources at runtime.
`deploy azure` uses the same embedded ARM template and TOSCA requirement graph
for its guarded JSON-config preflight, plan and development apply path.
The YAML bundles are review-only outputs, not apply plan files; their optional
profiles do not expand native apply support. The separate `topology` command
has been removed; use `deploy init`, `deploy generate` and `deploy check`.
After changing `server/azure/`, run
`go generate ./internal/topology`. A parity test rejects stale embedded sources;
the generator never maintains a second baseline resource implementation.
Asset regeneration also compiles the compact and Container Apps ARM parameter contracts,
so it requires the official Bicep CLI on `PATH`. `TestDeploymentSamples` compiles
all five shipped samples with the actual compiler and checks VM/database/Functions/
Container Apps resource definitions (including activation-gated definitions).
Missing Bicep fails those tests; CI installs a checksum-pinned
official compiler. Static rule and bad-configuration regressions run separately.
