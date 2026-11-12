#!/usr/bin/env bash
# Podman is the only container engine for local development services.
cao_container_engine_init() {
  if ! command -v podman >/dev/null 2>&1; then
    echo "Podman is required for local container services but is not installed; no other engine will be used." >&2
    return 1
  fi
}

cao_container() {
  podman "$@"
}

cao_container_compose() {
  cao_container_engine_init || return
  if ! podman compose version >/dev/null 2>&1; then
    echo "Podman Compose is unavailable; install a Compose provider (for example podman-compose) and configure PODMAN_COMPOSE_PROVIDER if needed." >&2
    return 1
  fi
  podman compose "$@"
}
