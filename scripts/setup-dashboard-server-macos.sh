#!/usr/bin/env bash

set -euo pipefail

# shellcheck source=scripts/container-engine.sh
source "$(dirname "${BASH_SOURCE[0]}")/container-engine.sh"
cao_container_engine_select

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "This setup command supports macOS only." >&2
  exit 1
fi

if ! command -v brew >/dev/null 2>&1; then
  cat >&2 <<'EOF'
Homebrew is required. Install it from https://brew.sh, then rerun:

  npm run dashboard:server:setup:macos
EOF
  exit 1
fi

brew_install_formula() {
  local formula=$1
  if brew list --formula "$formula" >/dev/null 2>&1; then
    echo "Homebrew formula already installed: $formula"
  else
    brew install "$formula"
  fi
}

brew_install_cask() {
  local cask=$1
  if brew list --cask "$cask" >/dev/null 2>&1; then
    echo "Homebrew cask already installed: $cask"
  else
    brew install --cask "$cask"
  fi
}

brew_install_formula go
brew_install_formula node@24
if [[ "$CAO_CONTAINER_ENGINE" == podman ]]; then
  brew_install_formula podman
  brew_install_formula podman-compose
else
  brew_install_formula docker
  brew_install_formula docker-compose
  brew_install_formula colima
fi

node_prefix="$(brew --prefix node@24)"
export PATH="$node_prefix/bin:$PATH"

cao_container_engine_init

if [[ "$CAO_CONTAINER_ENGINE" == docker ]] && ! command -v colima >/dev/null 2>&1; then
  echo "Colima was installed but is not available on PATH." >&2
  exit 1
fi

if ! command -v go >/dev/null 2>&1; then
  echo "Go was installed but is not available on PATH." >&2
  exit 1
fi

if ! command -v npm >/dev/null 2>&1; then
  echo "Node.js 24 was installed but npm is not available on PATH." >&2
  exit 1
fi

npm --prefix dashboard/site ci

echo
echo "Installed dashboard server dependencies:"
echo "  $(go version)"
echo "  node $(node --version)"
echo "  npm $(npm --version)"
echo "  $(cao_container --version)"
if [[ "$CAO_CONTAINER_ENGINE" == docker ]]; then
  echo "  Colima $(colima version | head -1)"
fi

GOTOOLCHAIN=auto go -C server env GOVERSION >/dev/null

printf '\nSetup complete. Start the local stack with:\n\n'
if [[ "$CAO_CONTAINER_ENGINE" == podman ]]; then
  cat <<'EOF'
  podman machine init   # once, if no machine exists
  podman machine start
  export CAO_CONTAINER_ENGINE=podman
  export PODMAN_COMPOSE_PROVIDER=podman-compose
EOF
else
  echo "  colima start"
fi
cat <<'EOF'
  npm run dashboard:server:redis-up
  npm run dashboard:server:build
  go -C server run ./cmd/cao-dashboard serve \
    --source testdata/deployed-subset
EOF
