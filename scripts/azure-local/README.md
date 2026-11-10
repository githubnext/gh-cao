# Local Azure service integration

This Linux-only harness runs the CAO Azure Functions hosting boundary against
Azurite and a real PostgreSQL container. It uses no Azure subscription, Azure login,
or Azure credentials. Its default integration profile uses only synthetic data and
credentials; an explicit GitHub.com development profile uses your own OAuth app.
It does not
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

## GitHub.com local development

Use the same Functions/Azurite/PostgreSQL boundary with the **real built dashboard**
and ordinary GitHub.com OAuth and organization-membership authorization:

```bash
export CAO_GITHUB_CLIENT_ID=YOUR_DEVELOPMENT_OAUTH_CLIENT_ID
export CAO_GITHUB_ALLOWED_ORGS=YOUR_ORGANIZATION
read -rsp 'Development OAuth client secret: ' CAO_GITHUB_CLIENT_SECRET; echo
export CAO_GITHUB_CLIENT_SECRET
npm run dashboard:local:azure
```

Install the existing dashboard npm dependencies first (`npm ci` in
`dashboard/site/`). Register a dedicated development OAuth app on GitHub.com with
homepage `http://localhost:7071/` and callback
`http://localhost:7071/auth/callback`. Authorize the app for the explicitly listed
organizations as required by their OAuth policies. The existing production OAuth
APIs, scopes, token exchange, membership checks, session encryption, secure
cookies, and CSRF checks are unchanged. This is **not** the separate
`CAO_GITHUB_LOCAL_USER` profile and does not bypass organization membership.
Use the printed **localhost** URL, not `127.0.0.1`: browsers permit the existing
Secure OAuth cookies on HTTP localhost but may reject them on numeric-loopback
HTTP origins. The actual listening socket remains bound to `127.0.0.1`.
Do not expose this listener through tunnels or proxies.

`dev` binds Core Tools explicitly to `127.0.0.1`, defaults to stable port `7071`,
waits for readiness, prints the local URL and evidence selection, and runs in the
foreground until Ctrl+C. It then removes its exact host process group, containers
(including their anonymous volumes), generated settings, and runtime state. No
collector, GitHub webhook, dispatch worker, Azure credentials, or rollout change
is enabled. This profile exercises real GitHub.com authentication, not simulated
GitHub APIs; successful login still requires valid credentials and authorized
organization membership.

| Environment variable | Development behavior |
| --- | --- |
| `CAO_GITHUB_CLIENT_ID`, `CAO_GITHUB_CLIENT_SECRET` | Required real development OAuth credentials; never pass secrets as command arguments |
| `CAO_GITHUB_ALLOWED_ORGS` | Required comma-separated exact organization logins; no wildcard or implicit catalog scope |
| `AZURE_LOCAL_FUNCTIONS_PORT` | Optional stable unprivileged port, default `7071`; an unavailable port fails startup |
| `CAO_GITHUB_REDIRECT_URL` | Optional; if set, must exactly match `http://localhost:PORT/auth/callback` |
| `CAO_SESSION_SECRET` | Optional, at least 32 characters; otherwise generated cryptographically for each fresh stack |
| `AZURE_LOCAL_SOURCE_DIRECTORY` | Optional existing **absolute** deployed Activity snapshot directory, ingested with production hash verification |
| `AZURE_LOCAL_STATE_DIR` | Optional absolute isolated directory; default `.tmp/azure-local/github/` |

Without a source directory, startup ingests `server/azure/empty-data`, the
production empty-evidence artifact. Empty views mean **no collected evidence**,
not a live fleet. The synthetic integration fixture is never generated or
implicitly loaded in this profile. Importing a snapshot does not collect or
refresh GitHub evidence. Restart with an explicitly updated snapshot to replace
it; local data is disposable and removed on stop.

Both canonical data and operational state use **ephemeral PostgreSQL**, not Redis.
Restart persistence applies only while that container exists: stopping removes
sessions and any pending GitHub credential-revocation work. Deleting local state
does **not** prove remote tokens were revoked. Sign out of the dashboard before
stopping the stack. If the stack was interrupted, or remote cleanup is uncertain,
revoke the dedicated development app's authorization in your GitHub account's
Settings → Applications. The harness does not issue broad automatic revocation
calls or claim durable token cleanup after deleting the container.

Other `CAO_*` environment configuration is rejected before startup rather than
silently inheriting a collector, alternative authentication policy, or remote
service. Core Tools receives a clean environment and private settings. State and
log directories are owner-only (`0700`), generated secret files are private
(`0600`), PostgreSQL has a random local password, and verbose host logging is
disabled. Development failures and `logs` print private log **paths**, not their
contents; retained logs may contain sensitive operational metadata. Never upload
the development state or logs as CI artifacts. Do not use shell tracing.

For a background stack, select the profile explicitly:

```bash
AZURE_LOCAL_PROFILE=github ./scripts/azure-local/azure-local.sh start
AZURE_LOCAL_PROFILE=github ./scripts/azure-local/azure-local.sh wait
AZURE_LOCAL_PROFILE=github ./scripts/azure-local/azure-local.sh logs
AZURE_LOCAL_PROFILE=github ./scripts/azure-local/azure-local.sh stop
```

`start` refuses existing GitHub stack state: stop it explicitly before changing
credentials, evidence, or port. Profiles cannot reuse or tear down one another's
state, including when given the same explicit directory. `test` and `run` remain
synthetic-only and must never be pointed at a development stack.
The non-secret `profile` marker remains with retained logs after stopping or
failed startup, so the synthetic profile cannot read or reuse old development
logs. Use a fresh directory to change profiles. Existing unmarked, nonempty
directories also fail closed; inspect their retained files and choose fresh
state rather than implicitly adopting them.

With a development stack running and native Chromium installed, verify the real
browser cookie/callback boundary separately:

```bash
PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH=/usr/bin/chromium \
  node tests/integration/azure-local-dev-browser.test.mjs \
  .tmp/azure-local/github/runtime.json
```

This test intercepts the real local login redirect before following GitHub; it requires
no live GitHub login and never exchanges an authorization code. It verifies
Secure-cookie persistence, invalid-state rejection, valid-state consumption,
anonymous API denial, actual loopback binding, and absence of configured secrets
from host logs. It does not establish a successful GitHub token exchange or
organization membership.

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
- confirm integration settings stay synthetic, development OAuth is explicitly
  selected and private, and neither profile requires workflow secrets or Azure
  credentials.

## Functions end-to-end contract

The integration test enters through the real Functions HTTP surface. It proves
that Core Tools registers and forwards the CAO routes, health/readiness reach
shared server logic and PostgreSQL, and PostgreSQL-backed pre-authentication
rate-limit state survives separate HTTP requests. Missing reviewed host policy
still makes the custom handler fail predictably. Separate service cases exercise
Azurite Blob, Queue and Table before entering the Functions surface. Each case
cleans up its own synthetic container, queue, table, database schema or namespace.
