# Local telemetry dashboards

These optional development backends receive the Go server's OpenTelemetry
exports. Run the commands below from the repository root. See the
[server telemetry contract](README.md#telemetry) for instrumentation and
exporter configuration shared with hosted deployments.

## Local Aspire telemetry dashboard

The optional [standalone Aspire dashboard](https://aspire.dev/dashboard/overview/)
receives the Go server's existing OTLP/HTTP traces, metrics, and opt-in structured
logs without an Aspire AppHost or a .NET SDK. From the repository root, with
Podman and a `podman compose` provider available:

```bash
npm run dashboard:server:aspire-up
bash scripts/container-compose.sh -f server/aspire-compose.yml logs aspire-dashboard
```

Open the browser-token login URL printed in the container logs, using
`http://127.0.0.1:18888` as the browser origin. Browser authentication remains
enabled. The UI and OTLP/HTTP receiver are bound only to host loopback
(`18888` and `4318`); ingestion is unauthenticated for local development.
Do not expose these ports publicly or reuse this configuration for hosting.
No gRPC port is published: the Go exporters use HTTP/protobuf.

In a dedicated development shell, clear any previous collector-specific
endpoints and authentication headers before selecting Aspire. Signal-specific
endpoints override the shared endpoint, so leaving an OpenObserve or hosted
configuration in place could send telemetry or credentials to the wrong service.

```bash
unset OTEL_EXPORTER_OTLP_TRACES_ENDPOINT OTEL_EXPORTER_OTLP_METRICS_ENDPOINT OTEL_EXPORTER_OTLP_LOGS_ENDPOINT
unset OTEL_EXPORTER_OTLP_HEADERS OTEL_EXPORTER_OTLP_TRACES_HEADERS OTEL_EXPORTER_OTLP_METRICS_HEADERS OTEL_EXPORTER_OTLP_LOGS_HEADERS
export OTEL_SDK_DISABLED=false
export OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318
export OTEL_RESOURCE_ATTRIBUTES=deployment.environment.name=local
export CAO_OTEL_LOGS_ENABLED=true
export DEBUG=cao:server,cao:telemetry
npm run dashboard:local
```

The shared endpoint automatically supplies `/v1/traces`, `/v1/metrics`, and
`/v1/logs`. Browse the CAO dashboard to generate requests, then select the
`cao-dashboard` service in Aspire's traces, metrics, or structured logs pages.
Logs still require a matching `DEBUG` namespace; omit `CAO_OTEL_LOGS_ENABLED`
to export only traces and metrics. The same environment works with direct
`serve` and `ingest` commands. Only telemetry routing changes: Aspire is not
the CAO dashboard, a canonical data source, or a rollout authority.

To confirm all three production exporters can deliver to the running receiver:

```bash
CAO_LOCAL_ASPIRE_INTEGRATION=1 go -C server test ./internal/telemetry -run '^TestLocalOTLPExport$/aspire$' -count=1
```

The opt-in test uses the fixed loopback HTTP endpoint, clears inherited exporter
credentials and signal endpoints, and fails if export is rejected or unreachable.
It publishes synthetic telemetry as `cao-aspire-integration-test`.
The existing `otel-smoke` command is OpenObserve-specific, not an Aspire check.

Stop and remove only this stack with `npm run dashboard:server:aspire-down`.
Telemetry is held in memory and lost when the container is removed. Aspire is
for development diagnostics, not production retention. OpenObserve and the
PostgreSQL/Redis stack are unchanged and are not started by these commands.
Close the dedicated shell when finished to discard its exporter configuration.

## Local Grafana LGTM telemetry

Alternatively, [Grafana's docker-otel-lgtm](https://github.com/grafana/docker-otel-lgtm)
bundles an OpenTelemetry Collector, Grafana, Loki for logs, Tempo for traces, and
Prometheus for metrics in a development container. Start it from the repository
root:

```bash
# Aspire and LGTM both publish port 4318; stop Aspire first if it is running.
npm run dashboard:server:aspire-down
npm run dashboard:server:lgtm-up
bash scripts/container-compose.sh -f server/lgtm-compose.yml logs -f lgtm
```

Wait for the stack's ready message, then open `http://127.0.0.1:3000`.
The image's default Grafana login is `admin` / `admin`; follow any password-change
prompt. The UI and unauthenticated OTLP/HTTP receiver are bound only to host
loopback. This is development infrastructure, not a production deployment.
No privileged mode, host process access, eBPF auto-instrumentation, or external
telemetry forwarding is enabled by this Compose file.

Use the **same dedicated-shell exporter configuration shown for Aspire** above:
clear inherited signal endpoints and headers, set
`OTEL_EXPORTER_OTLP_ENDPOINT=http://127.0.0.1:4318`, enable logs explicitly with
`CAO_OTEL_LOGS_ENABLED=true` and selected `DEBUG` namespaces, and run
`npm run dashboard:local`. In Grafana Explore, select the provisioned Tempo,
Prometheus, or Loki data source to inspect the `cao-dashboard` service.
CAO does not emit profiles just because the image also supports them.

Confirm all three exporters deliver to the running collector with:

```bash
CAO_LOCAL_LGTM_INTEGRATION=1 go -C server test ./internal/telemetry -run '^TestLocalOTLPExport$/lgtm$' -count=1
```

The test publishes synthetic telemetry as `cao-lgtm-integration-test` and fails
on rejected or unreachable exports. Collector acceptance is not proof of
downstream indexing; inspect Grafana for end-to-end diagnostics. `otel-smoke`
remains OpenObserve-specific.

Stop and remove this stack with `npm run dashboard:server:lgtm-down` before
switching back to Aspire. No persistent data volume is configured, so telemetry
is discarded when the container is removed. Neither option changes the
OpenObserve or PostgreSQL/Redis stacks, canonical data, policy, or rollout mode.

## Local OpenObserve telemetry

To inspect local traces and metrics with an alternative backend, start the optional
[OpenObserve self-hosted instance](https://openobserve.ai/docs/getting-started/)
on `http://127.0.0.1:5080` via the separate `server/otel-compose.yml`.
It is not started by the Redis/Postgres Compose stack.
Set an email and a locally held password (8–128 characters with uppercase,
lowercase, digit, and special characters) before its first startup
(do not commit them or put them in command-line arguments):

```bash
read -rp 'OpenObserve email: ' CAO_LOCAL_OTEL_EMAIL
read -rsp 'OpenObserve password: ' CAO_LOCAL_OTEL_PASSWORD; echo
export CAO_LOCAL_OTEL_EMAIL CAO_LOCAL_OTEL_PASSWORD
npm run dashboard:server:otel-up
```

In the same shell, configure the Go server's OTLP/HTTP exporters before
running `serve` or `ingest`. OpenObserve requires Basic authentication and
uses signal-specific endpoints; the shared base OTLP endpoint would append
the wrong paths. The credentials are needed again after a restart to export
telemetry, even though OpenObserve only uses them for account creation on
first startup.

```bash
export OTEL_EXPORTER_OTLP_TRACES_ENDPOINT=http://127.0.0.1:5080/api/default/v1/traces
export OTEL_EXPORTER_OTLP_METRICS_ENDPOINT=http://127.0.0.1:5080/api/default/v1/metrics
export OTEL_EXPORTER_OTLP_HEADERS="Authorization=Basic%20$(printf '%s' "$CAO_LOCAL_OTEL_EMAIL:$CAO_LOCAL_OTEL_PASSWORD" | base64 | tr -d '\n')"
export OTEL_RESOURCE_ATTRIBUTES=deployment.environment.name=local
go -C server run ./cmd/cao-dashboard serve
```

To exercise both exporters from the local Go server integration test (after
starting OpenObserve and setting the variables above in the same shell):

```bash
CAO_LOCAL_OTEL_INTEGRATION=1 go -C server test ./internal/telemetry -run '^TestLocalOpenObserveExport$' -count=1
```

The test skips during ordinary `go test ./...` runs; when enabled it fails if
either the trace or metric export is rejected or unreachable.

Sign in to OpenObserve with the same credentials and select the `default`
organization. The local instance stores data in the `openobserve-data` Podman
volume; `bash scripts/container-compose.sh -f server/otel-compose.yml down` stops it without
deleting that volume. Do not use `down -v` unless you intend to erase its data.
For hosted deployments, supply exporter authentication through the deployment
secret manager instead of exporting it from an interactive shell.
