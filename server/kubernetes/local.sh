#!/usr/bin/env bash
set -euo pipefail
umask 077

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
assets="$root/server/kubernetes"
policy=${CAO_LOCAL_POLICY:-$assets/policy.json}
state="$root/.tmp/cao-kubernetes-local"
db_host=cao-postgres.cao-local-system.svc.cluster.local

k() { kubectl --context docker-desktop "$@"; }
fail() { printf '%s\n' "$*" >&2; exit 1; }

namespace() {
  local name=$1 owner
  if k get namespace "$name" >/dev/null 2>&1; then
    owner=$(k get namespace "$name" -o 'jsonpath={.metadata.labels.app\.kubernetes\.io/part-of}')
    test "$owner" = cao-local || fail "Refusing foreign namespace: $name"
  else
    k create namespace "$name"
    k label namespace "$name" app.kubernetes.io/part-of=cao-local \
      pod-security.kubernetes.io/enforce=restricted
  fi
}

prepare() {
  test ! -e "$state" || fail "State already exists; preserve its credentials and use database/serve, not prepare."
  mkdir -p "$state"
  local admin owner runtime
  admin=$(openssl rand -hex 32)
  owner=$(openssl rand -hex 32)
  runtime=$(openssl rand -hex 32)
  openssl req -x509 -newkey rsa:3072 -nodes -days 30 \
    -subj /CN=CAO-local-development-CA \
    -addext basicConstraints=critical,CA:TRUE \
    -keyout "$state/ca.key" -out "$state/ca.crt" 2>"$state/openssl.log"
  certificate postgres "$db_host"
  certificate web localhost
  printf 'POSTGRES_DB=cao\nPOSTGRES_PASSWORD=%s\n' "$admin" > "$state/postgres.env"
  printf 'CAO_DATABASE_ADMIN_URL=postgresql://postgres:%s@%s/cao\nCAO_POSTGRES_OWNER_PASSWORD=%s\nCAO_POSTGRES_RUNTIME_PASSWORD=%s\n' \
    "$admin" "$db_host" "$owner" "$runtime" > "$state/bootstrap.env"
  printf 'CAO_DATABASE_ADMIN_URL=postgresql://\nPGUSER=cao_bootstrap_login\nPGPASSWORD=%s\nPGOPTIONS=-c role=cao_owner -c search_path=cao\n' \
    "$owner" > "$state/owner.env"
  printf 'CAO_POSTGRES_URL=postgresql://\nPGUSER=cao_runtime_login\nPGPASSWORD=%s\nPGOPTIONS=-c search_path=cao\n' \
    "$runtime" > "$state/runtime.env"
  printf 'CAO_SESSION_SECRET=%s\nCAO_GITHUB_WEBHOOK_SECRET=%s\n' \
    "$(openssl rand -hex 32)" "$(openssl rand -hex 32)" > "$state/web-keys.env"
  printf 'PGHOST=%s\nPGDATABASE=cao\nPGSSLMODE=verify-full\nPGSSLROOTCERT=/ca/ca.crt\nREDIS_NAMESPACE=local-kubernetes\nCAO_OPERATIONAL_NAMESPACE=local-kubernetes-operational\n' \
    "$db_host" > "$state/database.env"
  printf 'Private local state: %s\n' "$state"
}

certificate() {
  local name=$1 dns=$2
  openssl req -new -newkey rsa:3072 -nodes -subj "/CN=$dns" \
    -keyout "$state/$name.key" -out "$state/$name.csr" 2>>"$state/openssl.log"
  printf 'subjectAltName=DNS:%s\nextendedKeyUsage=serverAuth\nbasicConstraints=critical,CA:FALSE\n' \
    "$dns" > "$state/$name.ext"
  openssl x509 -req -days 30 -in "$state/$name.csr" \
    -CA "$state/ca.crt" -CAkey "$state/ca.key" -CAcreateserial \
    -extfile "$state/$name.ext" -out "$state/$name.crt" 2>>"$state/openssl.log"
}

build() {
  test "$(docker context show)" = desktop-linux || fail "Select Docker context desktop-linux before building."
  test -s "$state/ca.crt" || fail "Run prepare first."
  mkdir -p "$state/build-policy"
  cp "$policy" "$state/build-policy/cao.json"
  cp "$policy" "$state/build-policy/cao.azure.json"
  local target image
  for target in kubernetes-runtime kubernetes-db-maintenance; do
    image=cao-dashboard
    if test "$target" = kubernetes-db-maintenance; then image=cao-db-maintenance; fi
    docker build --target "$target" \
      --build-arg CAO_NPM_REGISTRY=https://packagefeedproxy.microsoft.io/npm/ \
      --build-arg "REVISION=$(git -C "$root" rev-parse HEAD)" \
      --build-context "cao-policy=$state/build-policy" \
      -t "$image:local" -f "$root/server/Dockerfile" "$root"
  done
  load
}

load() {
  test "$(docker context show)" = desktop-linux || fail "Select Docker context desktop-linux before loading."
  test "$(k get node desktop-control-plane -o jsonpath='{.spec.providerID}')" = \
    kind://docker/desktop/desktop-control-plane || fail "Expected the Docker Desktop kind node."
  docker image save cao-dashboard:local cao-db-maintenance:local |
    docker exec -i desktop-control-plane ctr --namespace k8s.io images import -
}

shared_config() {
  local ns=$1
  k -n "$ns" create configmap cao-database --from-env-file="$state/database.env" \
    --dry-run=client -o yaml | k apply -f -
  k -n "$ns" create configmap cao-postgres-ca --from-file=ca.crt="$state/ca.crt" \
    --dry-run=client -o yaml | k apply -f -
}

secret() {
  local ns=$1 name=$2 file=$3
  k -n "$ns" create secret generic "$name" --from-env-file="$file" \
    --dry-run=client -o yaml | k apply -f -
}

database() {
  test -s "$state/bootstrap.env" || fail "Run prepare first."
  namespace cao-local-system
  namespace cao-local
  shared_config cao-local-system
  shared_config cao-local
  secret cao-local-system cao-postgres-admin "$state/postgres.env"
  secret cao-local-system cao-bootstrap "$state/bootstrap.env"
  secret cao-local-system cao-owner "$state/owner.env"
  secret cao-local cao-runtime "$state/runtime.env"
  k -n cao-local-system create secret tls cao-postgres-tls \
    --cert="$state/postgres.crt" --key="$state/postgres.key" \
    --dry-run=client -o yaml | k apply -f -
  k apply -f "$assets/postgres.yaml"
  k -n cao-local-system rollout status statefulset/cao-postgres --timeout=180s
  local job
  job=$(k create -f "$assets/bootstrap.yaml" -o name)
  wait_job cao-local-system "$job"
  k apply -f "$assets/maintenance.yaml"
}

wait_job() {
  local ns=$1 job=$2 status attempt
  for ((attempt=0; attempt<120; attempt++)); do
    status=$(k -n "$ns" get "$job" -o 'jsonpath={.status.conditions[?(@.status=="True")].type}')
    case "$status" in
      *Complete*) k -n "$ns" logs "$job"; return ;;
      *Failed*) k -n "$ns" logs "$job" --all-containers=true >&2; fail "$job failed; inspect the retained Job before retrying." ;;
    esac
    sleep 5
  done
  fail "$job timed out; inspect its Pods and events before retrying."
}

serve() {
  local oauth=${1:?Supply the absolute path to the private OAuth env file}
  test -f "$oauth" || fail "OAuth file not found."
  # Never source operator input: accept only the four native OAuth inputs.
  awk -F= '
    /^[[:space:]]*#/ || /^[[:space:]]*$/ { next }
    $1 !~ /^CAO_GITHUB_(CLIENT_ID|CLIENT_SECRET|ALLOWED_USERS|ADMIN_USERS)$/ || length($0) <= length($1)+1 { exit 1 }
    /=REPLACE_WITH_/ { exit 1 }
    { if (seen[$1]++) exit 1; count++ }
    END { if (count != 4) exit 1 }
  ' "$oauth" || fail "OAuth file must contain exactly CLIENT_ID, CLIENT_SECRET, ALLOWED_USERS and ADMIN_USERS (CAO_GITHUB_ prefix), with all placeholders replaced."
  namespace cao-local
  secret cao-local cao-oauth "$oauth"
  secret cao-local cao-web-keys "$state/web-keys.env"
  k -n cao-local create secret tls cao-web-tls --cert="$state/web.crt" --key="$state/web.key" \
    --dry-run=client -o yaml | k apply -f -
  k -n cao-local create configmap cao-policy --from-file=cao.json="$policy" \
    --dry-run=client -o yaml | k apply -f -
  k apply -f "$assets/web.yaml"
  if k -n cao-local get configmap cao-native >/dev/null 2>&1; then
    native_web
  fi
  k -n cao-local rollout status deployment/cao-web --timeout=180s
}

native_web() {
  k -n cao-local set env deployment/cao-web --from=configmap/cao-native \
    CAO_COLLECT_NATIVE=true CAO_COLLECT_ADMIT_ONLY=true CAO_EXTERNAL_INGESTION=false
}

native() {
  local source=${1:?Supply the reviewed inventory file}
  local config=${2:?Supply the non-secret native collector env file}
  local key=${3:?Supply the private GitHub App key file}
  local digest expected job active
  test -n "${CAO_LOCAL_POLICY:-}" || fail "Native collection requires an explicit reviewed CAO_LOCAL_POLICY, also selected when building."
  test -f "$source" && test -f "$config" && test -s "$key" || fail "Inventory, collector configuration and App key files are required."
  test "$(wc -c < "$source")" -le 900000 || fail "Local inventory ConfigMap is limited to 900000 bytes."
  awk -F= '
    /^[[:space:]]*#/ || /^[[:space:]]*$/ { next }
    $1 !~ /^CAO_COLLECT_(APP_ID|CONTROL_REPOSITORY|NATIVE_INVENTORY_DIGEST|INVENTORY_LIMIT)$/ || length($0) <= length($1)+1 { exit 1 }
    { if (seen[$1]++) exit 1; count++ }
    END { if (count != 4) exit 1 }
  ' "$config" || fail "Collector configuration requires exactly App ID, control repository, inventory digest and repository limit; no credentials or other features."
  expected=$(awk -F= '$1=="CAO_COLLECT_NATIVE_INVENTORY_DIGEST" { print $2 }' "$config")
  digest=$(openssl dgst -sha256 "$source" | awk '{ print $NF }')
  test "$digest" = "$expected" || fail "Reviewed inventory digest mismatch."
  namespace cao-local
  for active in $(k -n cao-local get jobs -l cao-profile=snapshot -o 'jsonpath={.items[*].status.active}'); do
    test "$active" = 0 || fail "Wait for the active snapshot ingestion Job before selecting native collection."
  done
  k -n cao-local create configmap cao-policy --from-file=cao.json="$policy" \
    --dry-run=client -o yaml | k apply -f -
  k -n cao-local create configmap cao-native --from-env-file="$config" \
    --dry-run=client -o yaml | k apply -f -
  k -n cao-local create configmap cao-native-inventory --from-file=inventory.json="$source" \
    --dry-run=client -o yaml | k apply -f -
  job=$(k create -f "$assets/inventory.yaml" -o name)
  wait_job cao-local "$job"
  k -n cao-local create secret generic cao-collector-key --from-file=private-key.pem="$key" \
    --dry-run=client -o yaml | k apply -f -
  native_web
  k apply -f "$assets/collector.yaml"
  k -n cao-local rollout status deployment/cao-collector --timeout=300s
  k -n cao-local rollout status deployment/cao-web --timeout=180s
}

ingest() {
  local source=${1:?Supply the absolute path to a verified operational artifact} job pod
  test -f "$source/payload-hashes.json" || fail "Explicit operational payload-hashes.json is required."
  test ! -e "$source/.ready" || fail "Source must not contain the upload completion marker."
  if k -n cao-local get configmap cao-native >/dev/null 2>&1; then
    fail "Native collection and snapshot ingestion are exclusive; stop and explicitly retire the native profile before ingesting snapshots."
  fi
  job=$(k create -f "$assets/ingest.yaml" -o name)
  k -n cao-local wait --for=condition=Ready pod -l "job-name=${job#job.batch/}" --timeout=180s
  pod=$(k -n cao-local get pods -l "job-name=${job#job.batch/}" -o jsonpath='{.items[0].metadata.name}')
  k -n cao-local cp "$source/." "$pod:/data" -c ingest
  k -n cao-local exec "$pod" -c ingest -- touch /data/.ready
  wait_job cao-local "$job"
}

case "${1:-}" in
  prepare) prepare ;;
  build) build ;;
  load) load ;;
  database) database ;;
  serve) serve "${2:-}" ;;
  native) native "${2:-}" "${3:-}" "${4:-}" ;;
  ingest) ingest "${2:-}" ;;
  forward) exec kubectl --context docker-desktop -n cao-local port-forward --address 127.0.0.1 service/cao-web 8443:8443 ;;
  *) fail "Usage: bash server/kubernetes/local.sh {prepare|build|load|database|serve OAUTH_ENV|native INVENTORY_JSON COLLECTOR_ENV APP_KEY|ingest ARTIFACT_DIRECTORY|forward}" ;;
esac
