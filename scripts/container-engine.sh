#!/usr/bin/env bash
# Shared explicit engine selection for local development services.
cao_container_engine_select() {
  CAO_CONTAINER_ENGINE="${CAO_CONTAINER_ENGINE-docker}"
  case "$CAO_CONTAINER_ENGINE" in
    docker|podman) export CAO_CONTAINER_ENGINE ;;
    *)
      echo "CAO_CONTAINER_ENGINE must be docker or podman (got '$CAO_CONTAINER_ENGINE')." >&2
      return 1
      ;;
  esac
}

cao_container_engine_init() {
  cao_container_engine_select || return
  if ! command -v "$CAO_CONTAINER_ENGINE" >/dev/null 2>&1; then
    echo "Selected container engine '$CAO_CONTAINER_ENGINE' is not installed; no alternate engine will be used." >&2
    return 1
  fi
}

cao_container() {
  "$CAO_CONTAINER_ENGINE" "$@"
}

cao_container_compose() {
  cao_container_engine_init || return
  if [[ "$CAO_CONTAINER_ENGINE" == podman ]]; then
    if ! podman compose version >/dev/null 2>&1; then
      echo "Podman Compose is unavailable; install a Compose provider (docker-compose or podman-compose) and configure PODMAN_COMPOSE_PROVIDER if needed." >&2
      return 1
    fi
    podman compose "$@"
  elif docker compose version >/dev/null 2>&1; then
    docker compose "$@"
  elif command -v docker-compose >/dev/null 2>&1; then
    docker-compose "$@"
  else
    echo "Docker Compose is unavailable; install docker compose or docker-compose." >&2
    return 1
  fi
}
