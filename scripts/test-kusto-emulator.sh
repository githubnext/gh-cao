#!/usr/bin/env bash
set -euo pipefail

# Functional correctness only. Microsoft's emulator license forbids benchmarks.
if [[ "${ACCEPT_EULA:-}" != Y ]]; then
  echo "Review https://aka.ms/adx.emulator.license, then run with ACCEPT_EULA=Y." >&2
  exit 1
fi

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=scripts/container-engine.sh
source "$root/scripts/container-engine.sh"
cao_container_engine_init
image="mcr.microsoft.com/azuredataexplorer/kustainer-linux@sha256:230588624dbd95b8575f2493c0a7599d42d099b01b5875c239704f7cdfc65192"
if [[ "$(uname -s)" != Linux || -n "${CONTAINER_HOST:-}${CONTAINER_CONNECTION:-}${CONTAINER_SSHKEY:-}" ]]; then
  echo "Kusto acceptance requires Linux Podman --remote=false with no remote engine environment; Podman machines are not supported." >&2
  exit 1
fi
podman_local=(podman --remote=false)
container=""
cleanup() {
  if [[ -n "$container" ]]; then
    "${podman_local[@]}" rm --force "$container" >/dev/null
  fi
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

if ! "${podman_local[@]}" image inspect "$image" >/dev/null 2>&1; then
  "${podman_local[@]}" pull --platform linux/amd64 "$image"
fi
echo "Official emulator image: $image"
"${podman_local[@]}" image inspect "$image" --format 'Image ID: {{.Id}}; architecture: {{.Architecture}}'
container="$("${podman_local[@]}" run --detach --platform linux/amd64 \
  --env ACCEPT_EULA=Y --memory 3g --cpus 2 --publish 127.0.0.1::8080 "$image")"
address="$("${podman_local[@]}" port "$container" 8080/tcp)"
if [[ ! "$address" =~ ^127\.0\.0\.1:[0-9]+$ ]]; then
  echo "Emulator did not publish exactly one numeric-loopback endpoint: $address" >&2
  exit 1
fi
endpoint="http://$address"
ready=false
for ((attempt=0; attempt<180; attempt++)); do
  if [[ "$("${podman_local[@]}" inspect "$container" --format '{{.State.Running}}' 2>/dev/null)" != true ]]; then
    echo "Kusto emulator exited before readiness (x86 SSE4.2/AVX2 required; ARM is unsupported)." >&2
    "${podman_local[@]}" logs "$container" >&2 || true
    exit 1
  fi
  if curl --noproxy '*' --fail --silent --max-time 2 \
    --header 'Content-Type: application/json' --data '{"csl":".show version"}' \
    "$endpoint/v1/rest/mgmt" >/dev/null; then
    ready=true
    break
  fi
  sleep 2
done
if [[ "$ready" != true ]]; then
  echo "Real Kusto engine did not become ready; no emulator acceptance evidence." >&2
  "${podman_local[@]}" logs "$container" >&2
  exit 1
fi
curl --noproxy '*' --fail --silent --show-error --max-time 10 \
  --header 'Content-Type: application/json' --data '{"csl":".show version"}' \
  "$endpoint/v1/rest/mgmt"
printf '\nReal engine queries; simulated streaming transport; no authentication/retention acceptance.\n'
CAO_KUSTO_EMULATOR_URL="$endpoint" go -C "$root/server" test -race ./internal/adxingest \
  -run '^TestKustoEmulator' -count=1 -timeout=15m -v
go -C "$root/server" test ./internal/sessionarchiveblob -list '^TestKustoArchiveMembership$' \
  | grep -Fx TestKustoArchiveMembership
CAO_KUSTO_EMULATOR_URL="$endpoint" CAO_SESSION_ARCHIVE_REQUIRE_KUSTO=1 \
  go -C "$root/server" test -race ./internal/sessionarchiveblob \
  -run '^TestKustoArchiveMembership$' -count=1 -timeout=5m -v
