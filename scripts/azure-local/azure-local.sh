#!/usr/bin/env bash
set -euo pipefail
umask 077

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=scripts/container-engine.sh
source "$ROOT/scripts/container-engine.sh"
PROFILE="${AZURE_LOCAL_PROFILE:-synthetic}"
if [[ "${1:-}" == dev ]]; then
  [[ -z "${AZURE_LOCAL_PROFILE:-}" || "$PROFILE" == github ]] || {
    printf 'azure-local: dev requires the github profile\n' >&2
    exit 1
  }
  PROFILE=github
fi
[[ "$PROFILE" == synthetic || "$PROFILE" == github ]] || {
  printf 'azure-local: profile must be synthetic or github\n' >&2
  exit 1
}
DEFAULT_STATE=default
[[ "$PROFILE" != github ]] || DEFAULT_STATE=github
STATE_DIR="${AZURE_LOCAL_STATE_DIR:-"$ROOT/.tmp/azure-local/$DEFAULT_STATE"}"
[[ "$STATE_DIR" == /* ]] || {
  printf 'azure-local: AZURE_LOCAL_STATE_DIR must be absolute\n' >&2
  exit 1
}
LOG_DIR="$STATE_DIR/logs"
APP_DIR="$STATE_DIR/function-app"
RUNTIME_ENV="$STATE_DIR/runtime.env"
RUNTIME_JSON="$STATE_DIR/runtime.json"
HARNESS_VERSION=7
CORE_TOOLS_VERSION=4.15.1
PLATFORM_HELPER="$ROOT/scripts/azure-local/platform.py"
POSTGRES_IMAGE=docker.io/library/postgres:16-alpine
AZURITE_IMAGE=mcr.microsoft.com/azure-storage/azurite:3.37.0@sha256:830430c1da1a2d537e08f3e6764dd1f5ae00cf0346bcaf625b968ec3f0971fd5
AZURITE_ACCOUNT=caoazurelocal
WAIT_TIMEOUT="${AZURE_LOCAL_WAIT_TIMEOUT:-120}"

fail() {
  printf 'azure-local: %s\n' "$*" >&2
  exit 1
}

select_platform() {
  case "$(uname -s)/$(uname -m)" in
    Linux/x86_64)
      CORE_TOOLS_PLATFORM=linux-x64
      CORE_TOOLS_SHA256=9b982efb4047c717c9b1c26beb2269c2ac41b29bf8d21f4df1130c1586cc81cd ;;
    Linux/aarch64|Linux/arm64)
      CORE_TOOLS_PLATFORM=linux-arm64
      CORE_TOOLS_SHA256=d29b86958f42232ef4d7c03aa644a015c9dd3deccf70c34805670dc5155848a3 ;;
    Darwin/arm64)
      CORE_TOOLS_PLATFORM=osx-arm64
      CORE_TOOLS_SHA256=a1708a7b7415e79cbb2309c1eff9bf26d62e6982bff31ce06448f53686b71d49 ;;
    Darwin/x86_64)
      CORE_TOOLS_PLATFORM=osx-x64
      CORE_TOOLS_SHA256=0e3622b5efd91b86cfa5c51fa10e44375f956d794140a85d6bddee12a76a53c1 ;;
    *) fail "supported platforms are Linux and macOS on x64 or arm64" ;;
  esac
}

sha256() {
  python3 -c 'import hashlib,sys; print(hashlib.sha256(sys.stdin.buffer.read()).hexdigest())'
}

require_commands() {
  local command
  for command in curl go node python3 ps unzip; do
    command -v "$command" >/dev/null 2>&1 || fail "required command not found: $command"
  done
  cao_container_engine_init
  cao_container info >/dev/null 2>&1 || fail "$CAO_CONTAINER_ENGINE is not available"
}

check_state_profile() {
  local marker="$STATE_DIR/profile"
  if [[ -e "$marker" ]]; then
    [[ -f "$marker" && "$(cat "$marker")" == "$PROFILE" ]] ||
      fail "state belongs to a different or invalid profile; use its profile or a fresh state directory"
  elif [[ -d "$STATE_DIR" && -n "$(find "$STATE_DIR" -mindepth 1 -maxdepth 1 -print -quit)" ]]; then
    fail "existing unmarked state cannot be reused; review retained files and choose a fresh state directory"
  fi
}

prepare_state() {
  check_state_profile
  mkdir -p "$STATE_DIR"
  chmod 700 "$STATE_DIR"
  if [[ ! -e "$STATE_DIR/profile" ]]; then
    # Claim once; concurrent starts must never overwrite another profile's marker.
    (set -o noclobber; printf '%s\n' "$PROFILE" >"$STATE_DIR/profile") 2>/dev/null || true
  fi
  check_state_profile
  mkdir -p "$LOG_DIR"
  chmod 700 "$LOG_DIR"
}

load_runtime() {
  check_state_profile
  [[ -f "$RUNTIME_ENV" ]] || fail "no harness state found at $STATE_DIR; run start first"
  # Older harness state belongs to Docker, regardless of the caller's selection.
  CAO_CONTAINER_ENGINE=docker
  # shellcheck disable=SC1090
  source "$RUNTIME_ENV"
  [[ "${STATE_PROFILE:-synthetic}" == "$PROFILE" ]] ||
    fail "state belongs to a different profile; select its profile and stop it first"
  cao_container_engine_init
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
  cao_container port "$1" "$2/tcp" | awk -F: 'END { print $NF }'
}

install_core_tools_locked() {
  local tools_root tools_dir archive temp_dir
  tools_root="$ROOT/.tmp/azure-local/tools"
  tools_dir="$tools_root/core-tools-$CORE_TOOLS_VERSION-$CORE_TOOLS_PLATFORM"
  mkdir -p "$tools_root"
  if [[ -x "$tools_dir/func" ]]; then
    printf '%s\n' "$tools_dir/func"
    return
  fi
  temp_dir="$(python3 -c 'import sys,tempfile; print(tempfile.mkdtemp(prefix=".install.", dir=sys.argv[1]))' "$tools_root")"
  archive="$temp_dir/core-tools.zip"
  curl -fsSL \
    "https://github.com/Azure/azure-functions-core-tools/releases/download/$CORE_TOOLS_VERSION/Azure.Functions.Cli.$CORE_TOOLS_PLATFORM.$CORE_TOOLS_VERSION.zip" \
    -o "$archive" || {
      rm -rf "$temp_dir"
      fail "Azure Functions Core Tools download failed"
    }
  python3 "$PLATFORM_HELPER" verify "$archive" "$CORE_TOOLS_SHA256" ||
    {
      rm -rf "$temp_dir"
      fail "Azure Functions Core Tools checksum verification failed"
    }
  unzip -q "$archive" -d "$temp_dir"
  chmod +x "$temp_dir/func"
  rm -f "$archive"
  rm -rf "$tools_dir"
  mv "$temp_dir" "$tools_dir"
  printf '%s\n' "$tools_dir/func"
}

install_core_tools() {
  local tools_root="$ROOT/.tmp/azure-local/tools"
  mkdir -p "$tools_root"
  FUNC_BIN="$(python3 "$PLATFORM_HELPER" lock \
    "$tools_root/core-tools-$CORE_TOOLS_VERSION-$CORE_TOOLS_PLATFORM.lock" \
    bash "$ROOT/scripts/azure-local/azure-local.sh" install-core-tools)"
}

capture_container_logs() {
  local id name
  id="$1"
  name="$2"
  if [[ -n "$id" ]] && cao_container inspect "$id" >/dev/null 2>&1; then
    cao_container logs "$id" >"$LOG_DIR/$name.log" 2>&1 || true
  fi
}

process_matches() {
  [[ -n "$1" && -n "$2" && -n "$3" ]] || return 1
  python3 "$PLATFORM_HELPER" matches "$1" "$2" "$3"
}

process_start_matches() {
  python3 "$PLATFORM_HELPER" matches "$1" "$2"
}

wait_for_process_group() {
  local pid expected_executable pgid
  pid="$1"
  expected_executable="$2"
  for _ in {1..50}; do
    pgid="$(ps -p "$pid" -o pgid= | tr -d ' ')" || true
    if [[ "$pgid" == "$pid" ]]; then
      if process_matches "$pid" "$FUNCTIONS_START_TIME" "$expected_executable"; then
        FUNCTIONS_PGID="$pgid"
        return 0
      fi
    fi
    sleep 0.1
  done
  fail "Azure Functions host did not establish its process group"
}

process_group_matches() {
  python3 "$PLATFORM_HELPER" group "${FUNCTIONS_PGID:-}" "$APP_DIR/cao-dashboard"
}

remove_owned_container() {
  local id remaining
  id="$1"
  [[ -n "$id" ]] || return 0
  if cao_container rm -f "$id" --volumes >/dev/null 2>&1; then
    return 0
  fi
  # An inspect error is not absence: require successful exact-ID inventory.
  if remaining="$(cao_container ps --all --quiet --no-trunc --filter "id=$id")" &&
    [[ -z "$remaining" ]]; then
    return 0
  fi
  printf 'azure-local: cannot confirm removal of %s from %s; recovery state retained at %s\n' \
    "$id" "$CAO_CONTAINER_ENGINE" "$STATE_DIR" >&2
  return 1
}

stop_internal() {
  prepare_state
  if [[ -f "$RUNTIME_ENV" ]]; then
    load_runtime || return 1
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
  local removal_failed=0
  remove_owned_container "${AZURITE_CONTAINER_ID:-}" || removal_failed=1
  remove_owned_container "${POSTGRES_CONTAINER_ID:-}" || removal_failed=1
  (( removal_failed == 0 )) || return 1
  rm -rf "$APP_DIR"
  rm -f "$RUNTIME_ENV" "$RUNTIME_JSON"
  unset POSTGRES_CONTAINER_ID AZURITE_CONTAINER_ID FUNCTIONS_PID FUNCTIONS_START_TIME FUNCTIONS_PGID
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
  cao_container exec "$1" pg_isready -U postgres -d cao
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
  [[ "$PROFILE" == github ]] || cat "$log_file" >&2
  return "$status"
}

prepare_application() {
  mkdir -p "$APP_DIR/site" "$APP_DIR/.github/workflows" "$LOG_DIR"
  if [[ "$PROFILE" == github ]]; then
    run_logged "dashboard site build" site-build.log \
      npm --prefix "$ROOT/dashboard/site" run build -- "$APP_DIR/site" "$ROOT/.github/workflows/cao.json"
  else
    printf '<!doctype html><title>CAO local Azure simulation</title>\n' >"$APP_DIR/site/index.html"
  fi
  cp "$ROOT/.github/workflows/cao.json" "$APP_DIR/.github/workflows/cao.json"
  cp "$ROOT/.github/workflows/cao.azure.json" "$APP_DIR/.github/workflows/cao.azure.json"
  if [[ -n "${AZURE_LOCAL_TEST_BINARIES:-}" ]]; then
    [[ -x "$AZURE_LOCAL_TEST_BINARIES/cao-dashboard" ]] ||
      fail "explicit test binary is missing or not executable"
    cp "$AZURE_LOCAL_TEST_BINARIES/cao-dashboard" "$APP_DIR/cao-dashboard"
  else
    run_logged "dashboard server build" server-build.log \
      go -C "$ROOT/server" build -o "$APP_DIR/cao-dashboard" ./cmd/cao-dashboard
  fi
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
  [[ "$PROFILE" == synthetic || -z "${AZURE_LOCAL_TEST_BINARIES:-}" ]] ||
    fail "prebuilt test executables require the synthetic profile"
  check_state_profile
  select_platform
  if [[ "$PROFILE" == github ]]; then
    node "$ROOT/scripts/azure-local/profile.mjs" validate
    python3 - "${AZURE_LOCAL_FUNCTIONS_PORT:-7071}" <<'PY'
import socket, sys
try:
    with socket.socket() as listener:
        listener.bind(("127.0.0.1", int(sys.argv[1])))
except OSError:
    sys.exit("azure-local: requested Functions port is unavailable")
PY
  fi
  require_commands
  local requested_engine="$CAO_CONTAINER_ENGINE"
  prepare_state
  if [[ -f "$RUNTIME_ENV" ]]; then
    load_runtime || return 1
    [[ "$PROFILE" != github ]] ||
      fail "GitHub stack state already exists; stop it explicitly before restarting with new configuration"
    if [[ "${STATE_HARNESS_VERSION:-}" == "$HARNESS_VERSION" ]] &&
      process_matches \
        "${FUNCTIONS_PID:-}" "${FUNCTIONS_START_TIME:-}" "${FUNC_BIN:-}"; then
      printf 'azure-local: matching stack is already running at %s\n' "$STATE_DIR"
      return
    fi
    stop_internal || return 1
  fi
  CAO_CONTAINER_ENGINE="$requested_engine"
  cao_container_engine_init

  : >"$RUNTIME_ENV"
  chmod 600 "$RUNTIME_ENV"
  write_env_value CAO_CONTAINER_ENGINE "$CAO_CONTAINER_ENGINE"
  local previous_exit_trap
  previous_exit_trap="$(trap -p EXIT || true)"
  trap 'stop_internal' EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
  local run_id path_hash postgres_name azurite_name function_port postgres_port blob_port queue_port table_port namespace
  local azurite_key
  run_id="$(basename "$STATE_DIR" | tr -cd 'a-zA-Z0-9_.-' | cut -c1-40)"
  [[ -n "$run_id" ]] || run_id="run-$$"
  path_hash="$(printf '%s' "$STATE_DIR" | sha256 | cut -c1-12)"
  run_id="$(printf '%s' "$run_id" | cut -c1-20)-$path_hash"
  postgres_name="cao-azure-local-$run_id-postgres"
  azurite_name="cao-azure-local-$run_id-azurite"
  namespace="azure-local-$path_hash"
  azurite_key="$(python3 -c 'import base64,hashlib; print(base64.b64encode(hashlib.sha256(b"cao-azure-local").digest()).decode())')"

  write_env_value STATE_HARNESS_VERSION "$HARNESS_VERSION"
  write_env_value STATE_PROFILE "$PROFILE"

  local postgres_password=""
  if [[ "$PROFILE" == github ]]; then
    postgres_password="$(python3 -c 'import secrets; print(secrets.token_hex(32))')"
    POSTGRES_CONTAINER_ID="$(POSTGRES_PASSWORD="$postgres_password" cao_container run -d --name "$postgres_name" \
      -p 127.0.0.1::5432 -e POSTGRES_DB=cao -e POSTGRES_PASSWORD "$POSTGRES_IMAGE")"
  else
    POSTGRES_CONTAINER_ID="$(cao_container run -d --name "$postgres_name" -p 127.0.0.1::5432 \
      -e POSTGRES_DB=cao -e POSTGRES_HOST_AUTH_METHOD=trust "$POSTGRES_IMAGE")"
  fi
  write_env_value POSTGRES_CONTAINER_ID "$POSTGRES_CONTAINER_ID"
  postgres_port="$(container_port "$POSTGRES_CONTAINER_ID" 5432)"

  AZURITE_CONTAINER_ID="$(cao_container run -d --name "$azurite_name" \
    -p 127.0.0.1::10000 -p 127.0.0.1::10001 -p 127.0.0.1::10002 \
    -e "AZURITE_ACCOUNTS=$AZURITE_ACCOUNT:$azurite_key" \
    "$AZURITE_IMAGE" azurite --blobHost 0.0.0.0 --queueHost 0.0.0.0 \
    --tableHost 0.0.0.0 --location /data --debug /data/debug.log)"
  write_env_value AZURITE_CONTAINER_ID "$AZURITE_CONTAINER_ID"
  blob_port="$(container_port "$AZURITE_CONTAINER_ID" 10000)"
  queue_port="$(container_port "$AZURITE_CONTAINER_ID" 10001)"
  table_port="$(container_port "$AZURITE_CONTAINER_ID" 10002)"
  if [[ "$PROFILE" == github ]]; then
    function_port="$((10#${AZURE_LOCAL_FUNCTIONS_PORT:-7071}))"
  else
    function_port="$(free_port)"
  fi

  write_env_value POSTGRES_PORT "$postgres_port"
  write_env_value AZURITE_BLOB_PORT "$blob_port"
  write_env_value AZURITE_QUEUE_PORT "$queue_port"
  write_env_value AZURITE_TABLE_PORT "$table_port"
  write_env_value FUNCTIONS_PORT "$function_port"
  write_env_value REDIS_NAMESPACE "$namespace"

  poll Postgres postgres_ready "$POSTGRES_CONTAINER_ID"
  export CAO_POSTGRES_URL="postgres://postgres${postgres_password:+:$postgres_password}@127.0.0.1:$postgres_port/cao?sslmode=disable"
  prepare_application
  install_core_tools
  write_env_value FUNC_BIN "$FUNC_BIN"
  poll Postgres postgres_ready "$POSTGRES_CONTAINER_ID"
  poll Azurite azurite_ready "http://127.0.0.1:$blob_port/$AZURITE_ACCOUNT?comp=list"

  CAO_DATABASE_ADMIN_URL="$CAO_POSTGRES_URL" run_logged "canonical database bootstrap" database-bootstrap.log \
    "$APP_DIR/cao-dashboard" database bootstrap --store canonical --namespace "$namespace"

  CAO_DATABASE_ADMIN_URL="$CAO_POSTGRES_URL" run_logged "operational database bootstrap" operational-database-bootstrap.log \
    "$APP_DIR/cao-dashboard" database bootstrap --store operational --namespace "${namespace}-operational"

  local source_directory ingestion_label
  if [[ "$PROFILE" == github ]]; then
    source_directory="${AZURE_LOCAL_SOURCE_DIRECTORY:-"$ROOT/server/azure/empty-data"}"
    ingestion_label="development snapshot ingestion"
  else
    node "$ROOT/tests/e2e/dashboard-server-fixture.mjs" "$STATE_DIR/fixture"
    source_directory="$STATE_DIR/fixture"
    ingestion_label="fixture ingestion"
  fi
  run_logged "$ingestion_label" ingest.log "$APP_DIR/cao-dashboard" ingest \
    --source "$source_directory" \
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
    "CAO_AZURE_SITE_DIRECTORY": "$APP_DIR/site",
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
  if [[ "$PROFILE" == github ]]; then
    chmod 600 "$APP_DIR/local.settings.json"
    node "$ROOT/scripts/azure-local/profile.mjs" settings "$APP_DIR/local.settings.json" "$PROFILE"
  fi
  local public_host=127.0.0.1
  [[ "$PROFILE" != github ]] || public_host=localhost
  cat >"$RUNTIME_JSON" <<JSON
{
  "profile": "$PROFILE",
  "stateDirectory": "$STATE_DIR",
  "applicationDirectory": "$APP_DIR",
  "baseUrl": "http://$public_host:$function_port",
  "postgresUrl": "$CAO_POSTGRES_URL",
  "storage": {
    "account": "$AZURITE_ACCOUNT",
    "key": "$azurite_key",
    "blobEndpoint": "http://127.0.0.1:$blob_port/$AZURITE_ACCOUNT",
    "queueEndpoint": "http://127.0.0.1:$queue_port/$AZURITE_ACCOUNT",
    "tableEndpoint": "http://127.0.0.1:$table_port/$AZURITE_ACCOUNT"
  }
}
JSON

  local -a host_command
  mkdir -p "$APP_DIR/.home" "$APP_DIR/.scratch"
  host_command=(env -i "PATH=$PATH" "HOME=$APP_DIR/.home" "TMPDIR=$APP_DIR/.scratch"
    FUNCTIONS_CORE_TOOLS_TELEMETRY_OPTOUT=1
    "$FUNC_BIN" start --address 127.0.0.1 --port "$function_port")
  [[ "$PROFILE" == github ]] || host_command+=(--verbose)
  FUNCTIONS_PID="$(python3 "$PLATFORM_HELPER" launch "$APP_DIR" "$LOG_DIR/functions.log" "${host_command[@]}")"
  FUNCTIONS_START_TIME="$(python3 "$PLATFORM_HELPER" identity "$FUNCTIONS_PID")"
  FUNCTIONS_PGID="$FUNCTIONS_PID"
  write_env_value FUNCTIONS_PID "$FUNCTIONS_PID"
  write_env_value FUNCTIONS_START_TIME "$FUNCTIONS_START_TIME"
  write_env_value FUNCTIONS_PGID "$FUNCTIONS_PGID"
  wait_for_process_group "$FUNCTIONS_PID" "$FUNC_BIN"
  if [[ -n "$previous_exit_trap" ]]; then
    eval "$previous_exit_trap"
  else
    trap - EXIT
  fi
  trap - INT TERM
  printf 'azure-local: started stack at %s\n' "$STATE_DIR"
}

wait_for_stack() {
  load_runtime || return 1
  poll Postgres postgres_ready "$POSTGRES_CONTAINER_ID"
  poll Azurite azurite_ready "http://127.0.0.1:$AZURITE_BLOB_PORT/$AZURITE_ACCOUNT?comp=list"
  poll "Azure Functions host" functions_ready "http://127.0.0.1:$FUNCTIONS_PORT"
  printf 'azure-local: stack is ready\n'
}

test_stack() {
  [[ "$PROFILE" == synthetic ]] || fail "integration tests require the synthetic profile"
  load_runtime || return 1
  run_logged "Azure service integration" service-integration-test.log \
    node "$ROOT/tests/integration/azure-local-services.test.mjs" "$RUNTIME_JSON"
  run_logged "Azure Functions integration" integration-test.log \
    node "$ROOT/tests/integration/azure-local-functions.test.mjs" "$RUNTIME_JSON"
}

logs() {
  check_state_profile
  [[ -d "$LOG_DIR" ]] || {
    printf 'azure-local: no logs at %s\n' "$LOG_DIR"
    return
  }
  if [[ -f "$RUNTIME_ENV" ]]; then
    load_runtime || return 1
    capture_container_logs "${AZURITE_CONTAINER_ID:-}" azurite
    capture_container_logs "${POSTGRES_CONTAINER_ID:-}" postgres
  fi
  local file
  for file in "$LOG_DIR"/*.log; do
    [[ -e "$file" ]] || continue
    printf '\n===== %s =====\n' "$(basename "$file")"
    if [[ "$PROFILE" == github ]]; then
      printf 'Private development log: %s\n' "$file"
    else
      cat "$file"
    fi
  done
  printf '\nazure-local: logs retained at %s\n' "$LOG_DIR"
}

stop() {
  check_state_profile
  if [[ -f "$RUNTIME_ENV" ]]; then
    load_runtime || return 1
  fi
  stop_internal || return 1
  printf 'azure-local: stopped stack; logs retained at %s\n' "$LOG_DIR"
}

run_all() {
  [[ "$PROFILE" == synthetic ]] || fail "run requires the synthetic profile; use dev for GitHub.com"
  if [[ -z "${AZURE_LOCAL_STATE_DIR:-}" ]]; then
    STATE_DIR="$ROOT/.tmp/azure-local/run-$(date -u +%Y%m%dT%H%M%SZ)-$$"
    LOG_DIR="$STATE_DIR/logs"
    APP_DIR="$STATE_DIR/function-app"
    RUNTIME_ENV="$STATE_DIR/runtime.env"
    RUNTIME_JSON="$STATE_DIR/runtime.json"
  fi
  start
  trap 'status=$?; trap - EXIT INT TERM; stop_internal; exit "$status"' EXIT INT TERM
  wait_for_stack
  test_stack
}

dev() {
  start
  trap 'status=$?; trap - EXIT INT TERM; stop_internal; exit "$status"' EXIT
  trap 'exit 130' INT
  trap 'exit 143' TERM
  wait_for_stack
  printf 'azure-local: GitHub.com dashboard: http://localhost:%s/ (Ctrl+C to stop)\n' "$FUNCTIONS_PORT"
  if [[ -n "${AZURE_LOCAL_SOURCE_DIRECTORY:-}" ]]; then
    printf 'azure-local: data source: explicit snapshot (no automatic collection)\n'
  else
    printf 'azure-local: data source: empty (no collected evidence)\n'
  fi
  while process_matches "$FUNCTIONS_PID" "$FUNCTIONS_START_TIME" "$FUNC_BIN"; do
    sleep 1
  done
  fail "Azure Functions host exited; inspect private logs at $LOG_DIR"
}

case "${1:-}" in
  install-core-tools) select_platform; install_core_tools_locked ;;
  dev) dev ;;
  start) start ;;
  wait) wait_for_stack ;;
  test) test_stack ;;
  logs) logs ;;
  stop) stop ;;
  run) run_all ;;
  *) fail "usage: $0 {dev|start|wait|test|logs|stop|run}" ;;
esac
