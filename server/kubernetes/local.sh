#!/usr/bin/env bash
set -euo pipefail
umask 077

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
assets="$root/server/kubernetes"
policy=${CAO_LOCAL_POLICY:-$assets/policy.json}
state="$root/.tmp/cao-kubernetes-local"
db_host=cao-postgres.cao-local-system.svc.cluster.local

fail() { printf '%s\n' "$*" >&2; exit 1; }

# Optional standalone kind cluster, for example from server/kubernetes/aspire.
kind_cluster=${CAO_LOCAL_KIND_CLUSTER:-}
if test -n "$kind_cluster"; then
  [[ "$kind_cluster" =~ ^[a-z0-9]([-a-z0-9]{0,30}[a-z0-9])?$ ]] || fail "Invalid CAO_LOCAL_KIND_CLUSTER name."
  kube_context="kind-$kind_cluster"
  kind_node="$kind_cluster-control-plane"
else
  kube_context=docker-desktop
  kind_node=desktop-control-plane
fi
kind_provider="kind://docker/${kind_cluster:-desktop}/$kind_node"

k() { kubectl --context "$kube_context" "$@"; }

# Images must be built in the same Docker daemon that runs the kind node.
check_docker() {
  if test -n "$kind_cluster"; then
    test "$(docker container inspect "$kind_node" --format '{{index .Config.Labels "io.x-k8s.kind.cluster"}}' 2>/dev/null)" = "$kind_cluster" ||
      fail "Docker does not run kind node $kind_node; select the Docker context that hosts it."
  else
    test "$(docker context show)" = desktop-linux || fail "Select Docker context desktop-linux before $1."
  fi
}

check_namespaces() {
  local name identity
  for name in cao-local-system cao-local; do
    identity=$(k get namespace "$name" --ignore-not-found \
      -o 'jsonpath={.metadata.name}{"|"}{.metadata.labels.app\.kubernetes\.io/part-of}')
    test -z "$identity" || test "$identity" = "$name|cao-local" || fail "Refusing foreign namespace: $name"
  done
}

namespace() {
  local name=$1
  if test -z "$(k get namespace "$name" --ignore-not-found -o name)"; then
    printf '{"apiVersion":"v1","kind":"Namespace","metadata":{"name":"%s","labels":{"app.kubernetes.io/part-of":"cao-local","pod-security.kubernetes.io/enforce":"restricted"}}}\n' "$name" |
      k create -f -
  fi
}

required_files() {
  local file
  for file in "$@"; do test -s "$file" || fail "Required input missing: $file"; done
}

read_images() {
  node "$assets/deployment.mjs" images "$state/images.env" "$policy"
  runtime_image=$(awk -F= '$1=="RUNTIME_IMAGE" { print $2 }' "$state/images.env")
  maintenance_image=$(awk -F= '$1=="MAINTENANCE_IMAGE" { print $2 }' "$state/images.env")
  policy_digest=$(awk -F= '$1=="POLICY_SHA256" { print $2 }' "$state/images.env")
  policy_config="cao-policy-${policy_digest:0:24}"
  local image identity
  for image in "$runtime_image" "$maintenance_image"; do
    identity=$(docker image inspect "$image" --format '{{.Id}} {{index .Config.Labels "io.github.cao.policy-sha256"}}')
    test "$identity" = "sha256:${image##*:sha256-} $policy_digest" || fail "Built image identity or policy label mismatch."
  done
}

render() {
  local name=$1 profile=${2:-snapshot} config=${3:-}
  mkdir -p "$state/rendered/$profile"
  node "$assets/deployment.mjs" render "$assets/$name.yaml" "$state/images.env" "$policy" "$profile" "$config" \
    > "$state/rendered/$profile/$name.json"
}

active_profile() {
  local legacy
  profile=$(k -n cao-local get deployment cao-web --ignore-not-found -o 'jsonpath={.metadata.annotations.cao-local-profile}')
  native_config=$(k -n cao-local get deployment cao-web --ignore-not-found -o 'jsonpath={.metadata.annotations.cao-local-native-config}')
  case "$profile" in
    native)
      [[ "$native_config" =~ ^cao-native-[a-f0-9]{24}$ ]] || fail "Invalid active native configuration."
      ;;
    ""|snapshot)
      legacy=$(k -n cao-local get deployment cao-web --ignore-not-found \
        -o 'jsonpath={.spec.template.spec.containers[?(@.name=="web")].env[?(@.name=="CAO_COLLECT_NATIVE")].value}')
      test "$legacy" != true || fail "Legacy native deployment: re-run native with reviewed inputs before serve."
      profile=snapshot
      native_config=
      ;;
    *) fail "Unknown active ingestion profile." ;;
  esac
}

prepare() {
  test ! -e "$state" || fail "State already exists; preserve its credentials and use database/serve, not prepare."
  mkdir -p "$state"
  local admin owner runtime
  admin=$(openssl rand -hex 32)
  owner=$(openssl rand -hex 32)
  runtime=$(openssl rand -hex 32)
  MSYS2_ARG_CONV_EXCL="${MSYS2_ARG_CONV_EXCL:+$MSYS2_ARG_CONV_EXCL;}/CN=" \
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
  printf 'PGHOST=%s\nPGDATABASE=cao\nPGSSLMODE=verify-full\nPGSSLROOTCERT=/ca/ca.crt\nCAO_DATABASE_NAMESPACE=local-kubernetes\nCAO_OPERATIONAL_NAMESPACE=local-kubernetes-operational\n' \
    "$db_host" > "$state/database.env"
  printf 'Private local state: %s\n' "$state"
}

certificate() {
  local name=$1 dns=$2
  MSYS2_ARG_CONV_EXCL="${MSYS2_ARG_CONV_EXCL:+$MSYS2_ARG_CONV_EXCL;}/CN=" \
    openssl req -new -newkey rsa:3072 -nodes -subj "/CN=$dns" \
    -keyout "$state/$name.key" -out "$state/$name.csr" 2>>"$state/openssl.log"
  printf 'subjectAltName=DNS:%s\nextendedKeyUsage=serverAuth\nbasicConstraints=critical,CA:FALSE\n' \
    "$dns" > "$state/$name.ext"
  openssl x509 -req -days 30 -in "$state/$name.csr" \
    -CA "$state/ca.crt" -CAkey "$state/ca.key" -CAcreateserial \
    -extfile "$state/$name.ext" -out "$state/$name.crt" 2>>"$state/openssl.log"
}

build() {
  local go_proxy=${CAO_LOCAL_GO_PROXY:-}
  case "$go_proxy" in
    *"@"*|*"?"*|*"#"*) fail "CAO_LOCAL_GO_PROXY must not contain credentials or query parameters." ;;
  esac
  check_docker building
  test -s "$state/ca.crt" || fail "Run prepare first."
  mkdir -p "$state/build-policy"
  cp "$policy" "$state/build-policy/cao.json"
  cp "$policy" "$state/build-policy/cao.azure.json"
  local target image identity runtime maintenance digest
  digest=$(openssl dgst -sha256 "$policy" | awk '{ print $NF }')
  for target in kubernetes-runtime kubernetes-db-maintenance; do
    image=cao-dashboard
    if test "$target" = kubernetes-db-maintenance; then image=cao-db-maintenance; fi
    docker build --target "$target" \
      --build-arg CAO_NPM_REGISTRY=https://packagefeedproxy.microsoft.io/npm/ \
      --build-arg "GOPROXY=$go_proxy" \
      --build-arg "REVISION=$(git -C "$root" rev-parse HEAD)" \
      --label "io.github.cao.policy-sha256=$digest" \
      --build-context "cao-policy=$state/build-policy" \
      -t "$image:build" -f "$root/server/Dockerfile" "$root"
    identity=$(docker image inspect "$image:build" --format '{{.Id}}')
    [[ "$identity" =~ ^sha256:[a-f0-9]{64}$ ]] || fail "Invalid built image identity."
    image="$image:sha256-${identity#sha256:}"
    docker image tag "$identity" "$image"
    if test "$target" = kubernetes-runtime; then runtime=$image; else maintenance=$image; fi
  done
  printf 'RUNTIME_IMAGE=%s\nMAINTENANCE_IMAGE=%s\nPOLICY_SHA256=%s\n' "$runtime" "$maintenance" "$digest" > "$state/images.env.tmp"
  mv "$state/images.env.tmp" "$state/images.env"
  load
}

load() {
  read_images
  check_docker loading
  test "$(k get node "$kind_node" -o jsonpath='{.spec.providerID}')" = "$kind_provider" ||
    fail "Expected the kind node $kind_node."
  docker image save "$runtime_image" "$maintenance_image" |
    docker exec -i "$kind_node" ctr --namespace k8s.io images import -
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
  required_files "$state/bootstrap.env" "$state/database.env" "$state/postgres.env" \
    "$state/owner.env" "$state/runtime.env" "$state/postgres.crt" "$state/postgres.key" "$state/ca.crt"
  read_images
  check_namespaces
  render postgres
  render bootstrap
  render maintenance
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
  k apply -f "$state/rendered/snapshot/postgres.json"
  k -n cao-local-system rollout status statefulset/cao-postgres --timeout=180s
  local job
  job=$(k create -f "$state/rendered/snapshot/bootstrap.json" -o name)
  wait_job cao-local-system "$job"
  k apply -f "$state/rendered/snapshot/maintenance.json"
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
  check_namespaces
  required_files "$state/web-keys.env" "$state/web.crt" "$state/web.key" "$state/database.env" "$state/ca.crt"
  read_images
  active_profile
  if test "$profile" = native; then
    required_files "$state/active-native.env"
    test "$(k -n cao-local get deployment cao-web -o 'jsonpath={.spec.template.metadata.annotations.cao-local-policy-sha256}')" = "$policy_digest" ||
      fail "Native policy changed; re-run native with reviewed inputs before serve."
    test "$(k -n cao-local get configmap "$native_config" -o 'jsonpath={.data.CAO_COLLECT_NATIVE_INVENTORY_DIGEST}')" = \
      "$(awk -F= '$1=="CAO_COLLECT_NATIVE_INVENTORY_DIGEST" { print $2 }' "$state/active-native.env")" ||
      fail "Active native configuration does not match local reviewed state."
  fi
  render web "$profile" "$native_config"
  namespace cao-local
  shared_config cao-local
  secret cao-local cao-oauth "$oauth"
  secret cao-local cao-web-keys "$state/web-keys.env"
  k -n cao-local create secret tls cao-web-tls --cert="$state/web.crt" --key="$state/web.key" \
    --dry-run=client -o yaml | k apply -f -
  k -n cao-local create configmap "$policy_config" --from-file=cao.json="$policy" \
    --dry-run=client -o yaml | k apply -f -
  k apply -f "$state/rendered/$profile/web.json"
  k -n cao-local rollout status deployment/cao-web --timeout=180s
}

native() {
  local source=${1:?Supply the reviewed inventory file}
  local config=${2:?Supply the non-secret native collector env file}
  local key=${3:?Supply the private GitHub App key file}
  local job active config_name
  test -n "${CAO_LOCAL_POLICY:-}" || fail "Native collection requires an explicit reviewed CAO_LOCAL_POLICY, also selected when building."
  required_files "$source" "$config" "$key"
  read_images
  check_namespaces
  k -n cao-local get deployment cao-web -o name >/dev/null
  for active in $(k -n cao-local get jobs -l cao-profile=snapshot \
    -o 'jsonpath={range .items[*]}{.metadata.name}{":"}{.status.conditions[?(@.status=="True")].type}{"\n"}{end}'); do
    case "$active" in
      *:Complete|*:Failed) ;;
      *) fail "Wait for the nonterminal snapshot ingestion Job before selecting native collection." ;;
    esac
  done
  config_name=$(node "$assets/deployment.mjs" verify-native "$source" "$config" "$policy" "$key")
  render inventory native "$config_name"
  render collector native "$config_name"
  render web native "$config_name"
  namespace cao-local
  k -n cao-local create configmap "$policy_config" --from-file=cao.json="$policy" \
    --dry-run=client -o yaml | k apply -f -
  k -n cao-local create configmap "$config_name" --from-env-file="$config" \
    --dry-run=client -o yaml | k apply -f -
  k -n cao-local create configmap "$config_name-inventory" --from-file=inventory.json="$source" \
    --dry-run=client -o yaml | k apply -f -
  job=$(k create -f "$state/rendered/native/inventory.json" -o name)
  wait_job cao-local "$job"
  k -n cao-local create secret generic "$config_name-key" --from-file=private-key.pem="$key" \
    --dry-run=client -o yaml | k apply -f -
  k apply -f "$state/rendered/native/collector.json"
  k -n cao-local rollout status deployment/cao-collector --timeout=300s
  k apply -f "$state/rendered/native/web.json"
  cp "$config" "$state/active-native.env"
  k -n cao-local rollout status deployment/cao-web --timeout=180s
}

ingest() {
  local source=${1:?Supply the absolute path to a verified operational artifact} job pod collector_replicas
  test -f "$source/payload-hashes.json" || fail "Explicit operational payload-hashes.json is required."
  test ! -e "$source/.ready" || fail "Source must not contain the upload completion marker."
  read_images
  check_namespaces
  active_profile
  collector_replicas=$(k -n cao-local get deployment cao-collector --ignore-not-found -o 'jsonpath={.spec.replicas}')
  if test "$profile" = native || { test -n "$collector_replicas" && test "$collector_replicas" != 0; }; then
    fail "Native collection and snapshot ingestion are exclusive; stop and explicitly retire the native profile before ingesting snapshots."
  fi
  render ingest
  job=$(k create -f "$state/rendered/snapshot/ingest.json" -o name)
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
  forward) exec kubectl --context "$kube_context" -n cao-local port-forward --address 127.0.0.1 service/cao-web 8443:8443 ;;
  *) fail "Usage: bash server/kubernetes/local.sh {prepare|build|load|database|serve OAUTH_ENV|native INVENTORY_JSON COLLECTOR_ENV APP_KEY|ingest ARTIFACT_DIRECTORY|forward}" ;;
esac
