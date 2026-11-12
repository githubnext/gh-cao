#!/usr/bin/env bash

set -euo pipefail

# shellcheck source=scripts/container-engine.sh
source "$(dirname "${BASH_SOURCE[0]}")/container-engine.sh"

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
brew_install_formula podman
brew_install_formula podman-compose

node_prefix="$(brew --prefix node@24)"
export PATH="$node_prefix/bin:$PATH"

cao_container_engine_init

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

GOTOOLCHAIN=auto go -C server env GOVERSION >/dev/null

printf '\nSetup complete. Start the local stack with:\n\n'
cat <<'EOF'
  podman machine init   # once, if no machine exists
  podman machine start
  export PODMAN_COMPOSE_PROVIDER=podman-compose
  npm run dashboard:server:redis-up
  npm run dashboard:server:build
  go -C server run ./cmd/cao-dashboard serve \
    --site ../dashboard/site/dist \
    --source testdata/deployed-subset
EOF
