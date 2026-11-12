# Canonical read contract inner loop (explicitly synthetic)

Run these bounded scenarios from the repository root with Go installed; neither
needs production credentials, PostgreSQL, Redis, a container engine, a dashboard build, or
an Activity download:

```sh
npm run test:scenario:dashboard-contract
npm run test:integration:dashboard-contract
```

The first starts the real local Go application in the test process. The second
builds and starts a **separate `cao-dashboard serve` process**, explicitly
selecting a disposable PostgreSQL namespace with `--postgres-url`.
Both run the same deterministic, labelled synthetic scenario through numeric
loopback HTTP using the simulator's endpoint-allowlisted `LocalTransport`.
The subprocess gets an isolated home and an allowlisted environment without
inherited credentials, policy, database URLs, proxies or telemetry exporters.
Missing fixture data fails rather than downloading a production snapshot.
On Windows, use the direct Go commands (set the environment variable in
PowerShell with `$env:CAO_DASHBOARD_CONTRACT_PROCESS = '1'` before the second
command):

```sh
go -C server test ./internal/simulator -run '^TestDashboardReadSyntheticScenario$' -count=1 -timeout=1m -v
# Set CAO_DASHBOARD_CONTRACT_PROCESS=1 explicitly for this separate-process run.
go -C server test ./internal/simulator -run '^TestDashboardReadSyntheticProcessScenario$' -count=1 -timeout=3m -v
```

The fixture uses isolated PostgreSQL provisioning and canonical `model.Row` records:
two fictional repositories, one workflow and a sparse admitted Run at a fixed
timestamp. It is not a parallel data model or query engine. Both scenarios use
the production `dashboardstore` read boundary via the local PostgreSQL adapter,
ordinary `/api/repositories`, `/api/v1/query` and `/api/v1/events` routes,
and server-executed Dashboard Language. Their small labelled HTML placeholder
is **not** a dashboard rendering or browser test.

The scenarios assert that sparse/partial evidence does not become a successful
conclusion or zero cost and that source completeness/freshness remain unknown;
an absent daily projection stays explicitly unavailable, not an empty success.
A test-owned external snapshot commit advances the canonical revision and rejects an
old pagination cursor. Deliberately invalidating the disposable snapshot's schema
version produces HTTP 503 on canonical and query reads, with no fixture fallback;
restoring it recovers ordinary reads. Finally, canceling an already established
revision stream reports request cancellation while a subsequent query succeeds.
All fault writes are test-owned fixture operations, not a new public write API.

The fast scenario runs in the ordinary Go simulator suite; the binary-building
transport scenario is opt-in through its dedicated command. Both clean up their
temporary snapshots, listeners and processes. Synthetic results are
development evidence only: neither test proves PostgreSQL role isolation,
remote transport/TLS/OAuth behavior, in-flight SQL cancellation, remote outage
recovery or restart durability. Real-backend contract checks and hosted checks
remain necessary; use the isolated PostgreSQL helpers in `internal/testutil/`
and the service-backed server suites described in [POSTGRES.md](POSTGRES.md).
