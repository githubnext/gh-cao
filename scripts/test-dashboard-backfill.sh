#!/usr/bin/env bash
set -euo pipefail

cd "$(dirname "$0")/.."
if docker compose version >/dev/null 2>&1; then
  compose=(docker compose)
else
  compose=(docker-compose)
fi
project="cao-backfill-$(openssl rand -hex 6)"
compose+=(-p "$project" -f server/backfill-compose.yml)
export CAO_LOCAL_POSTGRES_PASSWORD
export CAO_LOCAL_OTEL_EMAIL=backfill@example.test
export CAO_LOCAL_OTEL_PASSWORD
CAO_LOCAL_POSTGRES_PASSWORD=$(openssl rand -hex 16)
CAO_LOCAL_OTEL_PASSWORD="Aa1!$(openssl rand -hex 16)"

report_dir="${CAO_BACKFILL_REPORT_DIR:-.tmp/go-backfill}"
mkdir -p "$report_dir"
cleanup() {
  status=$?
  trap - EXIT
  containers=()
  while IFS= read -r container; do
    if [ -n "$container" ]; then
      containers+=("$container")
    fi
  done < <("${compose[@]}" ps -aq)
  if [ "${#containers[@]}" -gt 0 ] && ! docker inspect "${containers[@]}" \
    --format '{{json .State}}' > "$report_dir/container-state.jsonl" 2>&1; then
    echo "Could not collect backfill container exit/resource state" >&2
    status=1
  fi
  if ! "${compose[@]}" logs --no-color > "$report_dir/services.log" 2>&1; then
    echo "Could not collect backfill service logs" >&2
    status=1
  fi
  if ! "${compose[@]}" down --volumes --remove-orphans; then
    echo "Could not remove the isolated backfill services" >&2
    status=1
  fi
  exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
"${compose[@]}" up -d --wait --wait-timeout 120 redis postgres

unset CAO_BACKFILL_SKIP_OPENOBSERVE
if "${compose[@]}" pull openobserve > "$report_dir/openobserve-pull.log" 2>&1; then
  "${compose[@]}" up -d --wait --wait-timeout 120 --pull never openobserve
elif grep -Eiq 'toomanyrequests|too many requests|rate.?limit|data limit exceeded' "$report_dir/openobserve-pull.log"; then
  echo "OpenObserve image pull was rate-limited; running backfill tests without telemetry verification." >&2
  export CAO_BACKFILL_SKIP_OPENOBSERVE=1
else
  echo "OpenObserve image pull failed; see $report_dir/openobserve-pull.log" >&2
  exit 1
fi

redis_address=$("${compose[@]}" port redis 6379)
postgres_address=$("${compose[@]}" port postgres 5432)
export REDIS_URL="redis://$redis_address/0"
export CAO_POSTGRES_URL="postgres://cao_backfill:$CAO_LOCAL_POSTGRES_PASSWORD@$postgres_address/cao_backfill?sslmode=disable"
export CAO_BACKFILL_INTEGRATION=1
export DEBUG=cao:collect:backfill:summary,cao:collect:projector,cao:ingest
export DEBUG_COLORS=0
export OTEL_RESOURCE_ATTRIBUTES=deployment.environment.name=synthetic
unset GH_TOKEN GITHUB_TOKEN GH_ENTERPRISE_TOKEN CAO_COLLECT_PRIVATE_KEY CAO_COLLECT_PRIVATE_KEY_FILE
unset OTEL_EXPORTER_OTLP_TRACES_HEADERS OTEL_EXPORTER_OTLP_METRICS_HEADERS OTEL_EXPORTER_OTLP_LOGS_HEADERS

if [ "${CAO_BACKFILL_SKIP_OPENOBSERVE:-0}" = 1 ]; then
  unset CAO_BACKFILL_OPENOBSERVE_URL CAO_BACKFILL_OTEL_LOGS
  unset OTEL_EXPORTER_OTLP_ENDPOINT OTEL_EXPORTER_OTLP_HEADERS
  unset OTEL_EXPORTER_OTLP_TRACES_ENDPOINT OTEL_EXPORTER_OTLP_METRICS_ENDPOINT OTEL_EXPORTER_OTLP_LOGS_ENDPOINT
else
  observe_address=$("${compose[@]}" port openobserve 5080)
  export CAO_BACKFILL_OPENOBSERVE_URL="http://$observe_address"
  export OTEL_EXPORTER_OTLP_TRACES_ENDPOINT="$CAO_BACKFILL_OPENOBSERVE_URL/api/default/v1/traces"
  export OTEL_EXPORTER_OTLP_METRICS_ENDPOINT="$CAO_BACKFILL_OPENOBSERVE_URL/api/default/v1/metrics"
  export OTEL_EXPORTER_OTLP_LOGS_ENDPOINT="$CAO_BACKFILL_OPENOBSERVE_URL/api/default/v1/logs"
  export OTEL_EXPORTER_OTLP_HEADERS
  OTEL_EXPORTER_OTLP_HEADERS="Authorization=Basic%20$(printf '%s' "$CAO_LOCAL_OTEL_EMAIL:$CAO_LOCAL_OTEL_PASSWORD" | base64 | tr -d '\n')"
  export CAO_BACKFILL_OTEL_LOGS=1
  ready=false
  for ((attempt = 0; attempt < 60; attempt++)); do
    if curl --fail --silent --output /dev/null "$CAO_BACKFILL_OPENOBSERVE_URL/healthz"; then
      ready=true
      break
    fi
    sleep 1
  done
  if [ "$ready" != true ]; then
    echo "OpenObserve did not become ready" >&2
    exit 1
  fi
fi

if [ "${CAO_BACKFILL_STRESS:-0}" = 1 ]; then
  go -C server test ./internal/server -run '^TestPostgresBackfillStress$' \
    -count=1 -timeout=45m -v 2>&1 | tee "$report_dir/go-test.txt"
else
  go -C server test ./internal/server ./internal/simulator \
    -run '^Test(PostgresBackfill.*|Backfill.*|ProfilesProduceIdenticalCanonicalRecords|Synthetic.*|LocalTransport.*|FaultProxy.*)$' \
    -count=1 -timeout=10m -v 2>&1 | tee "$report_dir/go-test.txt"
  go -C server test ./internal/collect -count=1 -timeout=10m -v \
    2>&1 | tee -a "$report_dir/go-test.txt"
fi
