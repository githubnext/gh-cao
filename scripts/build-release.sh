#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 1 || $# -gt 2 ]]; then
  echo "Usage: bash scripts/build-release.sh VERSION [OUTPUT_DIRECTORY]" >&2
  exit 1
fi

version="$1"
if [[ ! "$version" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
  echo "Release version must be a stable vMAJOR.MINOR.PATCH tag." >&2
  exit 1
fi

root="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"
output_dir="${2-dist/gh-cao}"
if [[ -z "$output_dir" || -L "$output_dir" ]]; then
  echo "Release output must be a nonempty directory path, not a symbolic link." >&2
  exit 1
fi
mkdir -p -- "$output_dir"
if [[ -n "$(find "$output_dir" -mindepth 1 -maxdepth 1 -print -quit)" ]]; then
  echo "Release output directory must be empty: $output_dir" >&2
  exit 1
fi

platforms=(
  darwin-amd64
  darwin-arm64
  linux-amd64
  linux-arm64
  windows-amd64
  windows-arm64
)
assets=()
bundle="$root/dashboard/local-assets/bundle"
if [[ -e "$bundle" || -L "$bundle" ]]; then
  echo "Release bundle staging directory already exists: $bundle" >&2
  exit 1
fi
mkdir -p "$bundle"
trap 'rm -rf -- "$bundle"' EXIT
node scripts/generate-dashboard-ingest.mjs --check
CAO_VERSION="$version" CAO_DASHBOARD_PROFILE=immediate npm --prefix dashboard/site run build -- "$bundle/site" "$root/.github/workflows/cao.json"
for platform in "${platforms[@]}"; do
  goos="${platform%-*}"
  goarch="${platform#*-}"
  suffix=""
  if [[ "$goos" == windows ]]; then
    suffix=".exe"
  fi
  asset="gh-cao-${platform}${suffix}"
  # Each extension embeds only its own platform's existing server executable.
  rm -f -- "$bundle/cao-dashboard" "$bundle/cao-dashboard.exe"
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go -C server build \
    -mod=readonly \
    -trimpath \
    -ldflags="-s -w -X main.version=${version}" \
    -o "$bundle/cao-dashboard${suffix}" \
    ./cmd/cao-dashboard
  echo "Building gh-cao ${version} for ${platform}"
  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build \
    -mod=readonly \
    -trimpath \
    -ldflags="-s -w -X main.version=${version}" \
    -o "${output_dir}/${asset}" \
    ./cmd/gh-cao
  assets+=("$asset")
done

(
  cd "$output_dir"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "${assets[@]}" > checksums.txt
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "${assets[@]}" > checksums.txt
  else
    echo "Release checksums require sha256sum or shasum." >&2
    exit 1
  fi
)
echo "Release binaries and checksums written to ${output_dir}"
