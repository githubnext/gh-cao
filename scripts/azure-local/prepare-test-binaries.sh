#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
OUTPUT="$ROOT/.tmp/podman-bin"
mkdir -p "$OUTPUT/server" "$OUTPUT/root"
go -C "$ROOT" mod download
go -C "$ROOT/server" mod download
go -C "$ROOT/server" build -o "$OUTPUT/cao-dashboard" ./cmd/cao-dashboard
go -C "$ROOT/server" test -race -c -o "$OUTPUT/server/" \
  ./internal/hostedcli ./internal/operational ./internal/postgresx \
  ./internal/sessionarchiveblob ./internal/tokenbroker ./internal/telemetry
go -C "$ROOT" test -race -c -o "$OUTPUT/root/" ./internal/cli
cp "$(go -C "$ROOT" env GOTOOLDIR)/test2json" "$OUTPUT/test2json"
