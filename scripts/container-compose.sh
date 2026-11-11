#!/usr/bin/env bash
set -euo pipefail

# shellcheck source=scripts/container-engine.sh
source "$(dirname "${BASH_SOURCE[0]}")/container-engine.sh"
cao_container_compose "$@"
