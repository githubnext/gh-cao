# Local Azure service integration

This Linux-only harness runs the CAO Azure Functions hosting boundary against
Azurite and a real PostgreSQL container. It uses no Azure subscription, Azure login,
Azure credentials, or production secrets. It does not
emulate managed identity, Key Vault, deployed networking, Azure edge behavior,
or managed PostgreSQL TLS; those remain real-Azure smoke-test concerns.

## Prerequisites

- Linux with Bash, Docker, Go, Node.js, Python 3, curl, `flock`, unzip, and
  `setsid`
- A running Docker daemon
- Cached Go dependencies for both the root Azure deployment CLI and Go server.
  Populate these before entering the hermetic service tests:

  ```bash
  go mod download && go -C server mod download
  ```

Azure Functions Core Tools 4.15.1 is downloaded on first use from its official
GitHub release and verified by SHA-256. PostgreSQL 16 and Azurite 3.37.0 images
are pinned by tag or digest.

Run the complete suite:

```bash
npm run test:integration:azure-services
```

## Bicep service inventory and local equivalents

The inventory includes all eight `server/azure/*.bicep` modules, existing
resources, and optional collector/ADX resources. The inventory guard compares
every resource type with the generated ARM template; a new service fails the
guard until its local coverage and limitations are reviewed.

| Bicep resource type | Local equivalent and separate case | Not established locally |
| --- | --- | --- |
| `Microsoft.Web/sites` | [Functions Core Tools](https://learn.microsoft.com/en-us/azure/azure-functions/functions-run-local); route forwarding, health/readiness, shared PostgreSQL rate limits and missing configuration | Azure edge forwarding, scale-in, platform timeouts |
| `Microsoft.Web/serverfarms` | Bicep/ARM hosting-plan contract assertions; no Azure plan emulator | Premium capacity, billing, always-on guarantees |
| `Microsoft.Storage/storageAccounts` | [Azurite](https://learn.microsoft.com/en-us/azure/storage/common/storage-use-azurite); separate signed Blob read/write/ETag/denial, Queue visibility/receipt/acknowledgement and Table entity/ETag cases | Entra/RBAC, managed storage durability; Table emulation is preview |
| `Microsoft.DBforPostgreSQL/flexibleServers` | Real PostgreSQL 16; production canonical adapter publication/reopen/contract-drift case | Flexible Server management, managed TLS, backups, HA |
| `Microsoft.DBforPostgreSQL/flexibleServers/databases` | Production PostgreSQL operational adapter conformance; sessions, CAS, admission, queue capacity and leases in isolated namespaces | Azure database provisioning and restore |
| `Microsoft.ContainerRegistry/registries` | Existing Azure CLI protocol fixture and production deploy adapter digest-binding/read-back cases; no ACR emulator | Registry push/pull, managed builds, AcrPull authorization |
| `Microsoft.App/managedEnvironments` | Bicep/ARM environment, subnet and logging contracts; no managed-environment emulator | Azure environment lifecycle and networking |
| `Microsoft.App/containerApps` | Collector image, identity, secret and deployment contract cases; ordinary local collection adapters are not a Container Apps emulator | Managed scheduling, replica coordination and autoscaling |
| `Microsoft.App/jobs` | Production deploy adapter job ordering, failure and cancellation cases through the Azure CLI protocol fixture; setup journey separately executes real provisioning/ingestion subprocesses | Azure scheduler, execution isolation and platform job cancellation |
| `Microsoft.KeyVault/vaults` | Local HTTP identity/secret fixture through the production `KeyVault.HTTPClient` seam; secret reads, rotation, denial, unavailability and redirect rejection | Azure vault RBAC, purge protection and App Service reference resolution |
| `Microsoft.KeyVault/vaults/secrets` | Same fixture; missing/empty secret failure, uncached rotation; separate deploy adapter unresolved-reference case | Actual Key Vault references and secret propagation |
| `Microsoft.ManagedIdentity/userAssignedIdentities` | Separate App Service and IMDS HTTP cases, audience/client selection, expired tokens and identity rejection | Entra token issuance, identity assignment and Azure authorization |
| `Microsoft.Authorization/roleAssignments` | Existing least-privilege Bicep/ARM role/scope assertions; no Azure RBAC emulator | Effective deployed permissions |
| `Microsoft.OperationalInsights/workspaces` | Workspace/shared-role logging deployment contracts; no Log Analytics emulator | Workspace retention, query execution and receipt of Azure platform logs |
| `Microsoft.Insights/components` | Template-driven local TLS OTLP receiver case decodes production trace/metric/log protobuf and checks failure isolation; **not** an Application Insights emulator | Azure ingestion, Functions host telemetry and managed alerts |
| `Microsoft.Insights/diagnosticSettings` | Diagnostic destination/category deployment contracts; no Azure diagnostic-settings emulator | Delivery of actual platform diagnostics |
| `Microsoft.Network/virtualNetworks` | Bicep/ARM subnet/delegation assertions; no Azure VNet emulator | Routing, NSG enforcement and isolation |
| `Microsoft.Network/privateDnsZones` | Private DNS naming and resource-link contracts | Azure private DNS resolution |
| `Microsoft.Network/privateDnsZones/virtualNetworkLinks` | Private DNS/VNet linkage contracts | Effective DNS registration and reachability |
| `Microsoft.Network/privateEndpoints` | ADX opt-in private endpoint/subnet/group contracts | Private Link connectivity and approval |
| `Microsoft.Network/privateEndpoints/privateDnsZoneGroups` | ADX private DNS zone association contracts | Actual endpoint DNS resolution |
| `Microsoft.Kusto/clusters` | Optional [official Kusto emulator](https://learn.microsoft.com/en-us/azure/data-explorer/kusto-emulator-overview); existing real-engine functional integration suite | Entra, streaming/managed ingestion, Azure cluster management and retention enforcement |
| `Microsoft.Kusto/clusters/databases` | Same optional real engine, isolated databases and generated KQL/mapping/query cases | Managed database durability, retention and Azure permissions |

There is no supported Azure-wide local emulator. Protocol fixtures and
deployment-contract assertions above are deliberately not reported as managed
service integration. No production endpoint, HTTPS requirement, credentials or
rollout authority is weakened for tests. Every directly accessed Azure resource crosses an injectable interface. Existing
contracts are retained, and concrete Blob/ADX archive dependencies and the
Key Vault managed-identity dependency are replaced with owned service contracts:

| Direct access | Consumer interface | Production adapter / local integration |
| --- | --- | --- |
| ARM resources, ACR, Functions settings/restart, Container Apps jobs, role/provider/name checks | `azcli.Executor` in deployment dependencies | `azcli.Process`; existing strict Azure CLI protocol simulator and deploy tests |
| PostgreSQL canonical data | `dashboardstore.Store` / `dashboardstore.Reader` | `postgresx.Store`; real PostgreSQL publication/reopen tests |
| PostgreSQL operational state | `operational.Store` and its domain service interfaces | `operational/postgres`; real multi-client conformance |
| Key Vault secret read | `tokenbroker.PrivateKeyReader` | `tokenbroker.KeyVault`; local HTTP fixture |
| App Service/IMDS managed identity | `tokenbroker.ManagedIdentityTokenSource` | `tokenbroker.ManagedIdentity`; separate local HTTP identity cases |
| ADX streaming export and trace verification | `adxingest.Sink` / `adxtrace.Sink` | `adxingest.Client`; existing protocol and optional real Kusto engine suites |
| Optional private archive Blob objects, leases and pointer CAS | `sessionarchiveblob.BlobStore` | Owned Blob HTTP adapter; new real Azurite integration through the interface, including verified retries after `BlobAlreadyExists` |
| Optional private archive ADX metadata/query access | `sessionarchiveblob.QueryService` | Owned bounded ADX HTTP adapter; injected HTTP fixture and separate Azure conformance |
| Archive publication and hosted read consumer | `sessionarchive.Publisher` / hosted reader/publisher interfaces | Legacy and native archive adapters; injected-service publication/restart/withdrawal cases |
| Go telemetry export | OpenTelemetry provider/exporter interfaces | Existing OTLP exporters and local TLS protobuf receiver |

`NewWithServices` and `NewNativeWithServices` accept explicit archive services.
Both retain the existing configuration, scope and acceptance gates. Missing
services fail; unavailable adapters do not trigger another backend. Immutable
verification, lease ownership, conditional pointer publication, bounded complete
query results, cancellation and withdrawal/recovery semantics remain part of the
owned contracts. Service interfaces contain domain-owned results and errors,
not database handles, Azure SDK types or raw HTTP responses.

Functions owns its platform Blob/Queue/Table accesses; CAO has no production
Queue/Table client to wrap. Bicep-only plan, network, diagnostics and role
resources are accessed through the deployment executor, not invented runtime
clients. No universal Azure service object or additional deployment resources
are introduced.

The default service suite does **not** run or claim ADX engine acceptance.
After reviewing Microsoft's license, run it separately:

```bash
ACCEPT_EULA=Y bash scripts/test-kusto-emulator.sh
```

The license prohibits benchmarks. The official emulator requires Linux/x86
with the documented CPU features. An unavailable or unselected engine is
incomplete ADX evidence, not a passing alternative.

For an already-running isolated local stack, service cases can run separately
from Functions forwarding:

```bash
node tests/integration/azure-local-services.test.mjs PATH/TO/runtime.json
node tests/integration/azure-local-functions.test.mjs PATH/TO/runtime.json
```

The service runner requires explicit numeric-loopback endpoints, fresh
synthetic resources and namespaces, and cached Go dependencies. It disables
Go downloads, selects tests explicitly, rejects skipped or empty Go test
selections, and fails on missing/unavailable services rather than falling back.
Storage REST requests are test clients for the Functions platform dependency;
they are not an additional production storage adapter.

The dispatcher also supports iterative use:

```bash
./scripts/azure-local/azure-local.sh start
./scripts/azure-local/azure-local.sh wait
./scripts/azure-local/azure-local.sh test
./scripts/azure-local/azure-local.sh logs
./scripts/azure-local/azure-local.sh stop
```

`run` always stops its services, including after a test failure or interruption.
`stop` is idempotent. It removes generated settings and transient state while
logs remain under `.tmp/azure-local/<run>/logs/`. Set
`AZURE_LOCAL_STATE_DIR` to use an explicit isolated state directory or run
several stacks in parallel. Set `AZURE_LOCAL_WAIT_TIMEOUT` to change the
120-second readiness deadline.

## Process model

Each run owns a state directory, a uniquely named PostgreSQL container, a uniquely
named Azurite container, a Functions Core Tools process group, generated
Functions metadata, dynamically published loopback ports, and unique canonical
and operational namespaces. The state records exact process/container identities; teardown never
uses global process discovery. Fixed internal container ports are published to
dynamic host ports to avoid collisions.

Readiness polling verifies PostgreSQL readiness, an Azurite Blob endpoint response, and
the CAO `/api/readiness` endpoint through Functions Core Tools. Timeouts print
all available service logs and fail.

Build, canonical and operational database bootstrap, and fixture ingestion
failures print the failed stage, exit status, and captured output before teardown.
Their log files remain available alongside the service logs. Fixture ingestion
verifies the checked-in `server/testdata/deployed-subset/payload-hashes.json`
manifest; a mismatch fails startup rather than skipping or weakening integrity
checks.

## Repository audit checklist

Before changing this harness:

- inspect `server/internal/server/azure.go` and the `cao-dashboard azure-functions` custom
  handler entrypoint;
- inspect hosted environment validation and retain production PostgreSQL TLS
  and HTTPS requirements;
- inspect the PostgreSQL operational namespace, rate-limit, ingestion, and integration tests;
- inspect `server/azure/main.bicep` and its contract tests without changing the
  production deployment contract for local convenience;
- inspect existing dashboard build, server test fixtures, Docker, and GitHub
  Actions conventions;
- confirm all local settings are synthetic and no workflow secret or Azure
  credential is required.

## Functions end-to-end contract

The integration test enters through the real Functions HTTP surface. It proves
that Core Tools registers and forwards the CAO routes, health/readiness reach
shared server logic and PostgreSQL, and PostgreSQL-backed pre-authentication
rate-limit state survives separate HTTP requests. Missing reviewed host policy
still makes the custom handler fail predictably. Separate service cases exercise
Azurite Blob, Queue and Table before entering the Functions surface. Each case
cleans up its own synthetic container, queue, table, database schema or namespace.
