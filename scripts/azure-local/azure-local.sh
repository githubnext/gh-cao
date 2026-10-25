#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
STATE_DIR="${AZURE_LOCAL_STATE_DIR:-"$ROOT/.tmp/azure-local/default"}"
LOG_DIR="$STATE_DIR/logs"
APP_DIR="$STATE_DIR/function-app"
RUNTIME_ENV="$STATE_DIR/runtime.env"
RUNTIME_JSON="$STATE_DIR/runtime.json"
HARNESS_VERSION=4
CORE_TOOLS_VERSION=4.15.1
CORE_TOOLS_SHA256=9b982efb4047c717c9b1c26beb2269c2ac41b29bf8d21f4df1130c1586cc81cd
POSTGRES_IMAGE=postgres:16-alpine
AZURITE_IMAGE=mcr.microsoft.com/azure-storage/azurite:3.37.0@sha256:830430c1da1a2d537e08f3e6764dd1f5ae00cf0346bcaf625b968ec3f0971fd5
AZURITE_ACCOUNT=caoazurelocal
WAIT_TIMEOUT="${AZURE_LOCAL_WAIT_TIMEOUT:-120}"

fail() {
  printf 'azure-local: %s\n' "$*" >&2
  exit 1
}

require_linux() {
  [[ "$(uname -s)" == Linux ]] || fail "this harness supports Linux only"
}

require_commands() {
  local command
  for command in curl docker flock go node python3 setsid sha256sum unzip; do
    command -v "$command" >/dev/null 2>&1 || fail "required command not found: $command"
  done
  docker info >/dev/null 2>&1 || fail "Docker is not available"
}

load_runtime() {
  [[ -f "$RUNTIME_ENV" ]] || fail "no harness state found at $STATE_DIR; run start first"
  # shellcheck disable=SC1090
  source "$RUNTIME_ENV"
}

write_env_value() {
  printf '%s=%q\n' "$1" "$2" >>"$RUNTIME_ENV"
}

free_port() {
  python3 - <<'PY'
import socket
with socket.socket() as listener:
    listener.bind(("127.0.0.1", 0))
    print(listener.getsockname()[1])
PY
}

container_port() {
  docker port "$1" "$2/tcp" | awk -F: 'END { print $NF }'
}

install_core_tools() {
  local tools_root tools_dir archive temp_dir lock_fd
  tools_root="$ROOT/.tmp/azure-local/tools"
  tools_dir="$tools_root/core-tools-$CORE_TOOLS_VERSION"
  if [[ -x "$tools_dir/func" ]]; then
    FUNC_BIN="$tools_dir/func"
    return
  fi
  mkdir -p "$tools_root"
  exec {lock_fd}>"$tools_root/core-tools-$CORE_TOOLS_VERSION.lock"
  flock "$lock_fd"
  if [[ -x "$tools_dir/func" ]]; then
    FUNC_BIN="$tools_dir/func"
    exec {lock_fd}>&-
    return
  fi
  temp_dir="$(mktemp -d "$tools_root/.install.XXXXXX")"
  archive="$temp_dir/core-tools.zip"
  curl -fsSL \
    "https://github.com/Azure/azure-functions-core-tools/releases/download/$CORE_TOOLS_VERSION/Azure.Functions.Cli.linux-x64.$CORE_TOOLS_VERSION.zip" \
    -o "$archive" || {
      rm -rf "$temp_dir"
      fail "Azure Functions Core Tools download failed"
    }
  printf '%s  %s\n' "$CORE_TOOLS_SHA256" "$archive" | sha256sum --check --status ||
    {
      rm -rf "$temp_dir"
      fail "Azure Functions Core Tools checksum verification failed"
    }
  unzip -q "$archive" -d "$temp_dir"
  chmod +x "$temp_dir/func"
  rm -f "$archive"
  rm -rf "$tools_dir"
  mv "$temp_dir" "$tools_dir"
  FUNC_BIN="$tools_dir/func"
  exec {lock_fd}>&-
}

capture_container_logs() {
  local id name
  id="$1"
  name="$2"
  if [[ -n "$id" ]] && docker inspect "$id" >/dev/null 2>&1; then
    docker logs "$id" >"$LOG_DIR/$name.log" 2>&1 || true
  fi
}

process_matches() {
  local pid expected_start expected_executable actual_start actual_executable
  pid="$1"
  expected_start="$2"
  expected_executable="$3"
  [[ -n "$pid" && -n "$expected_start" && -n "$expected_executable" ]] || return 1
  [[ -r "/proc/$pid/stat" && -e "/proc/$pid/exe" ]] || return 1
  actual_start="$(awk '{print $22}' "/proc/$pid/stat")"
  actual_executable="$(readlink -f "/proc/$pid/exe")"
  [[ "$actual_start" == "$expected_start" ]] &&
    [[ "$actual_executable" == "$(readlink -f "$expected_executable")" ]]
}

process_start_matches() {
  local pid expected_start
  pid="$1"
  expected_start="$2"
  [[ -n "$pid" && -n "$expected_start" && -r "/proc/$pid/stat" ]] || return 1
  [[ "$(awk '{print $22}' "/proc/$pid/stat")" == "$expected_start" ]]
}

wait_for_process_group() {
  local pid expected_executable pgid executable
  pid="$1"
  expected_executable="$(readlink -f "$2")"
  for _ in {1..50}; do
    if [[ -r "/proc/$pid/stat" && -e "/proc/$pid/exe" ]]; then
      pgid="$(awk '{print $5}' "/proc/$pid/stat")"
      executable="$(readlink -f "/proc/$pid/exe")"
      if [[ "$pgid" == "$pid" && "$executable" == "$expected_executable" ]]; then
        FUNCTIONS_START_TIME="$(awk '{print $22}' "/proc/$pid/stat")"
        FUNCTIONS_PGID="$pgid"
        return 0
      fi
    fi
    sleep 0.1
  done
  fail "Azure Functions host did not establish its process group"
}

process_group_matches() {
  local process stat_pgid executable expected_handler
  [[ -n "${FUNCTIONS_PGID:-}" ]] || return 1
  expected_handler="$(readlink -f "$APP_DIR/cao-dashboard")"
  for process in /proc/[0-9]*; do
    [[ -r "$process/stat" && -e "$process/exe" ]] || continue
    stat_pgid="$(awk '{print $5}' "$process/stat")"
    [[ "$stat_pgid" == "$FUNCTIONS_PGID" ]] || continue
    executable="$(readlink -f "$process/exe")"
    if [[ "$executable" == "$expected_handler" ]]; then
      return 0
    fi
  done
  return 1
}

stop_internal() {
  mkdir -p "$LOG_DIR"
  if [[ -f "$RUNTIME_ENV" ]]; then
    # shellcheck disable=SC1090
    source "$RUNTIME_ENV"
  fi
  if process_start_matches "${FUNCTIONS_PID:-}" "${FUNCTIONS_START_TIME:-}" ||
    process_group_matches; then
    local quiet_checks=0
    for _ in {1..50}; do
      local active=false
      if process_start_matches "${FUNCTIONS_PID:-}" "${FUNCTIONS_START_TIME:-}"; then
        kill -TERM "$FUNCTIONS_PID" 2>/dev/null || true
        active=true
      fi
      if process_group_matches; then
        kill -TERM -- "-$FUNCTIONS_PGID" 2>/dev/null || true
        active=true
      fi
      if [[ "$active" == true ]]; then
        quiet_checks=0
      else
        quiet_checks=$((quiet_checks + 1))
        (( quiet_checks >= 5 )) && break
      fi
      sleep 0.1
    done
    if process_start_matches "${FUNCTIONS_PID:-}" "${FUNCTIONS_START_TIME:-}"; then
      kill -KILL "$FUNCTIONS_PID" 2>/dev/null || true
    fi
    if process_group_matches; then
      kill -KILL -- "-$FUNCTIONS_PGID" 2>/dev/null || true
    fi
  fi
  capture_container_logs "${AZURITE_CONTAINER_ID:-}" azurite
  capture_container_logs "${POSTGRES_CONTAINER_ID:-}" postgres
  [[ -z "${AZURITE_CONTAINER_ID:-}" ]] || docker rm -f "$AZURITE_CONTAINER_ID" >/dev/null 2>&1 || true
  [[ -z "${POSTGRES_CONTAINER_ID:-}" ]] || docker rm -f "$POSTGRES_CONTAINER_ID" >/dev/null 2>&1 || true
  rm -rf "$APP_DIR"
  rm -f "$RUNTIME_ENV" "$RUNTIME_JSON"
}

poll() {
  local label deadline
  label="$1"
  shift
  deadline=$((SECONDS + WAIT_TIMEOUT))
  while (( SECONDS < deadline )); do
    if "$@" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  printf 'azure-local: timed out waiting for %s\n' "$label" >&2
  logs >&2 || true
  return 1
}

postgres_ready() {
  docker exec "$1" pg_isready -U postgres -d cao
}

azurite_ready() {
  local status
  status="$(curl -sS -o /dev/null -w '%{http_code}' "$1")" || return 1
  [[ "$status" -lt 500 ]]
}

functions_ready() {
  curl -fsS "$1/api/readiness"
}

run_logged() {
  local label log_file status
  label="$1"
  log_file="$LOG_DIR/$2"
  shift 2
  if "$@" >"$log_file" 2>&1; then
    return 0
  else
    status=$?
  fi
  printf 'azure-local: %s failed (exit %s); log: %s\n' "$label" "$status" "$log_file" >&2
  cat "$log_file" >&2
  return "$status"
}

prepare_application() {
  mkdir -p "$APP_DIR/site" "$APP_DIR/.github/workflows" "$LOG_DIR"
  printf '<!doctype html><title>CAO local Azure simulation</title>\n' >"$APP_DIR/site/index.html"
  cp "$ROOT/.github/workflows/cao.json" "$APP_DIR/.github/workflows/cao.json"
  cp "$ROOT/.github/workflows/cao.azure.json" "$APP_DIR/.github/workflows/cao.azure.json"
  run_logged "dashboard server build" server-build.log \
    go -C "$ROOT/server" build -o "$APP_DIR/cao-dashboard" ./cmd/cao-dashboard
  cat >"$APP_DIR/host.json" <<'JSON'
{
  "version": "2.0",
  "customHandler": {
    "description": {
      "defaultExecutablePath": "cao-dashboard",
      "arguments": ["azure-functions"]
    },
    "enableForwardingHttpRequest": true
  },
  "extensions": {
    "http": {
      "routePrefix": ""
    }
  }
}
JSON
  mkdir -p "$APP_DIR/cao"
  cat >"$APP_DIR/cao/function.json" <<'JSON'
{
  "bindings": [
    {
      "authLevel": "anonymous",
      "direction": "in",
      "methods": ["delete", "get", "head", "options", "patch", "post", "put"],
      "name": "request",
      "route": "{*path}",
      "type": "httpTrigger"
    },
    {
      "direction": "out",
      "name": "$return",
      "type": "http"
    }
  ]
}
JSON
}

start() {
  require_linux
  require_commands
  mkdir -p "$STATE_DIR" "$LOG_DIR"
  if [[ -f "$RUNTIME_ENV" ]]; then
    # shellcheck disable=SC1090
    source "$RUNTIME_ENV"
    if [[ "${STATE_HARNESS_VERSION:-}" == "$HARNESS_VERSION" ]] &&
      process_matches \
        "${FUNCTIONS_PID:-}" "${FUNCTIONS_START_TIME:-}" "${FUNC_BIN:-}"; then
      printf 'azure-local: matching stack is already running at %s\n' "$STATE_DIR"
      return
    fi
    stop_internal
  fi

  : >"$RUNTIME_ENV"
  local previous_exit_trap
  previous_exit_trap="$(trap -p EXIT || true)"
  trap 'stop_internal' EXIT
  local run_id path_hash postgres_name azurite_name function_port postgres_port blob_port queue_port table_port namespace
  local azurite_key
  run_id="$(basename "$STATE_DIR" | tr -cd 'a-zA-Z0-9_.-' | cut -c1-40)"
  [[ -n "$run_id" ]] || run_id="run-$$"
  path_hash="$(printf '%s' "$STATE_DIR" | sha256sum | cut -c1-12)"
  run_id="$(printf '%s' "$run_id" | cut -c1-20)-$path_hash"
  postgres_name="cao-azure-local-$run_id-postgres"
  azurite_name="cao-azure-local-$run_id-azurite"
  namespace="azure-local-$path_hash"
  azurite_key="$(python3 -c 'import base64,hashlib; print(base64.b64encode(hashlib.sha256(b"cao-azure-local").digest()).decode())')"

  write_env_value STATE_HARNESS_VERSION "$HARNESS_VERSION"

  POSTGRES_CONTAINER_ID="$(docker run -d --name "$postgres_name" -p 127.0.0.1::5432 \
    -e POSTGRES_DB=cao -e POSTGRES_HOST_AUTH_METHOD=trust "$POSTGRES_IMAGE")"
  write_env_value POSTGRES_CONTAINER_ID "$POSTGRES_CONTAINER_ID"
  postgres_port="$(container_port "$POSTGRES_CONTAINER_ID" 5432)"

  AZURITE_CONTAINER_ID="$(docker run -d --name "$azurite_name" \
    -p 127.0.0.1::10000 -p 127.0.0.1::10001 -p 127.0.0.1::10002 \
    -e "AZURITE_ACCOUNTS=$AZURITE_ACCOUNT:$azurite_key" \
    "$AZURITE_IMAGE" azurite --blobHost 0.0.0.0 --queueHost 0.0.0.0 \
    --tableHost 0.0.0.0 --location /data --debug /data/debug.log)"
  write_env_value AZURITE_CONTAINER_ID "$AZURITE_CONTAINER_ID"
  blob_port="$(container_port "$AZURITE_CONTAINER_ID" 10000)"
  queue_port="$(container_port "$AZURITE_CONTAINER_ID" 10001)"
  table_port="$(container_port "$AZURITE_CONTAINER_ID" 10002)"
  function_port="$(free_port)"

  write_env_value POSTGRES_PORT "$postgres_port"
  write_env_value AZURITE_BLOB_PORT "$blob_port"
  write_env_value AZURITE_QUEUE_PORT "$queue_port"
  write_env_value AZURITE_TABLE_PORT "$table_port"
  write_env_value FUNCTIONS_PORT "$function_port"
  write_env_value REDIS_NAMESPACE "$namespace"

  poll Postgres postgres_ready "$POSTGRES_CONTAINER_ID"
  export CAO_POSTGRES_URL="postgres://postgres@127.0.0.1:$postgres_port/cao?sslmode=disable"
  prepare_application
  install_core_tools
  write_env_value FUNC_BIN "$FUNC_BIN"
  poll Postgres postgres_ready "$POSTGRES_CONTAINER_ID"
  poll Azurite azurite_ready "http://127.0.0.1:$blob_port/$AZURITE_ACCOUNT?comp=list"

  CAO_DATABASE_ADMIN_URL="$CAO_POSTGRES_URL" run_logged "canonical database bootstrap" database-bootstrap.log \
    "$APP_DIR/cao-dashboard" database bootstrap --store canonical --namespace "$namespace"

  CAO_DATABASE_ADMIN_URL="$CAO_POSTGRES_URL" run_logged "operational database bootstrap" operational-database-bootstrap.log \
    "$APP_DIR/cao-dashboard" database bootstrap --store operational --namespace "${namespace}-operational"

  node "$ROOT/tests/e2e/dashboard-server-fixture.mjs" "$STATE_DIR/fixture"
  run_logged "fixture ingestion" ingest.log "$APP_DIR/cao-dashboard" ingest \
    --source "$STATE_DIR/fixture" \
    --redis-namespace "$namespace" \
    --database-queries "$ROOT/dashboard/site/src/data/queries/database.json"

  local storage_connection
  storage_connection="DefaultEndpointsProtocol=http;AccountName=$AZURITE_ACCOUNT;AccountKey=$azurite_key;BlobEndpoint=http://127.0.0.1:$blob_port/$AZURITE_ACCOUNT;QueueEndpoint=http://127.0.0.1:$queue_port/$AZURITE_ACCOUNT;TableEndpoint=http://127.0.0.1:$table_port/$AZURITE_ACCOUNT;"
  cat >"$APP_DIR/local.settings.json" <<JSON
{
  "IsEncrypted": false,
  "Values": {
    "FUNCTIONS_WORKER_RUNTIME": "custom",
    "AzureWebJobsStorage": "$storage_connection",
    "CAO_AZURE_ALLOWED_HOSTS": "127.0.0.1,localhost",
    "CAO_AZURE_DASHBOARD_QUERIES": "$ROOT/dashboard/site/dashboard.json",
    "CAO_DATABASE_QUERIES": "$ROOT/dashboard/site/src/data/queries/database.json",
    "CAO_AZURE_LOCAL_SIMULATION": "1",
    "CAO_POLICY_PATH": "$APP_DIR/.github/workflows/cao.azure.json",
    "CAO_GITHUB_ALLOWED_ORGS": "example",
    "CAO_GITHUB_CLIENT_ID": "local-client",
    "CAO_GITHUB_CLIENT_SECRET": "local-client-secret",
    "CAO_GITHUB_REDIRECT_URL": "http://127.0.0.1:$function_port/auth/callback",
    "REDIS_NAMESPACE": "$namespace",
    "CAO_OPERATIONAL_NAMESPACE": "${namespace}-operational",
    "CAO_POSTGRES_URL": "$CAO_POSTGRES_URL",
    "CAO_SESSION_SECRET": "local-simulation-session-secret-0000000000000000",
    "OTEL_SDK_DISABLED": "true"
  }
}
JSON
  cat >"$RUNTIME_JSON" <<JSON
{
  "stateDirectory": "$STATE_DIR",
  "applicationDirectory": "$APP_DIR",
  "baseUrl": "http://127.0.0.1:$function_port"
}
JSON

  (
    cd "$APP_DIR"
    # setsid makes Core Tools the process-group leader tracked for exact teardown.
    setsid "$FUNC_BIN" start --port "$function_port" --verbose >"$LOG_DIR/functions.log" 2>&1 &
    FUNCTIONS_PID=$!
    FUNCTIONS_START_TIME="$(awk '{print $22}' "/proc/$FUNCTIONS_PID/stat")"
    FUNCTIONS_PGID="$FUNCTIONS_PID"
    write_env_value FUNCTIONS_PID "$FUNCTIONS_PID"
    write_env_value FUNCTIONS_START_TIME "$FUNCTIONS_START_TIME"
    write_env_value FUNCTIONS_PGID "$FUNCTIONS_PGID"
    wait_for_process_group "$FUNCTIONS_PID" "$FUNC_BIN"
  )
  if [[ -n "$previous_exit_trap" ]]; then
    eval "$previous_exit_trap"
  else
    trap - EXIT
  fi
  printf 'azure-local: started stack at %s\n' "$STATE_DIR"
}

wait_for_stack() {
  load_runtime
  poll Postgres postgres_ready "$POSTGRES_CONTAINER_ID"
  poll Azurite azurite_ready "http://127.0.0.1:$AZURITE_BLOB_PORT/$AZURITE_ACCOUNT?comp=list"
  poll "Azure Functions host" functions_ready "http://127.0.0.1:$FUNCTIONS_PORT"
  printf 'azure-local: stack is ready\n'
}

test_stack() {
  load_runtime
  set +e
  node "$ROOT/tests/integration/azure-local-functions.test.mjs" "$RUNTIME_JSON" \
    > >(tee "$LOG_DIR/integration-test.log") 2>&1
  local status=$?
  set -e
  return "$status"
}

logs() {
  mkdir -p "$LOG_DIR"
  if [[ -f "$RUNTIME_ENV" ]]; then
    # shellcheck disable=SC1090
    source "$RUNTIME_ENV"
    capture_container_logs "${AZURITE_CONTAINER_ID:-}" azurite
    capture_container_logs "${POSTGRES_CONTAINER_ID:-}" postgres
  fi
  local file
  for file in "$LOG_DIR"/*.log; do
    [[ -e "$file" ]] || continue
    printf '\n===== %s =====\n' "$(basename "$file")"
    cat "$file"
  done
  printf '\nazure-local: logs retained at %s\n' "$LOG_DIR"
}

stop() {
  stop_internal
  printf 'azure-local: stopped stack; logs retained at %s\n' "$LOG_DIR"
}

run_all() {
  if [[ -z "${AZURE_LOCAL_STATE_DIR:-}" ]]; then
    STATE_DIR="$ROOT/.tmp/azure-local/run-$(date -u +%Y%m%dT%H%M%SZ)-$$"
    LOG_DIR="$STATE_DIR/logs"
    APP_DIR="$STATE_DIR/function-app"
    RUNTIME_ENV="$STATE_DIR/runtime.env"
    RUNTIME_JSON="$STATE_DIR/runtime.json"
  fi
  trap 'status=$?; trap - EXIT INT TERM; stop_internal; exit "$status"' EXIT INT TERM
  start
  wait_for_stack
  test_stack
}

case "${1:-}" in
  start) start ;;
  wait) wait_for_stack ;;
  test) test_stack ;;
  logs) logs ;;
  stop) stop ;;
  run) run_all ;;
  *) fail "usage: $0 {start|wait|test|logs|stop|run}" ;;
esac
