# Policy and ingestion snapshots are adopter inputs. They default to the main
# build context; replace them with --build-context cao-policy=DIR or
# --build-context cao-ingest-source=DIR when building from a release asset bundle.
FROM scratch AS cao-policy
COPY .github/workflows/cao.json .github/workflows/cao.azure.json /

FROM scratch AS cao-ingest-source
ARG CAO_INGEST_SOURCE
COPY ${CAO_INGEST_SOURCE:-.cao-ingest-source-required}/ /

FROM node:24-alpine@sha256:ebfe2f90462722a7a4de65e91990e97fe0d401c70e0e762c5b53302f905ec1c1 AS dashboard-build

SHELL ["/bin/ash", "-o", "pipefail", "-c"]

ARG REVISION
ARG VERSION=dev
ARG CAO_PROFILE=cao.json
ENV CAO_VERSION=${VERSION}
WORKDIR /workspace
COPY dashboard/site/package.json dashboard/site/package-lock.json ./dashboard/site/
RUN sed -i \
      's#https://ms-feed-25.pkgs.visualstudio.com/1es-public/_packaging/npm-public/npm/registry/#https://registry.npmjs.org/#' \
      dashboard/site/package-lock.json \
    && npm --prefix dashboard/site ci --omit=dev --ignore-scripts
COPY . .
COPY --from=cao-policy / .github/workflows/
RUN printf '%s' "${CAO_PROFILE}" | grep -Eq '^cao(\.[a-z0-9-]+)?\.json$' \
    && policy="../../.github/workflows/${CAO_PROFILE}" \
    && if echo "${REVISION}" | grep -Eq '^[0-9a-f]{40}$'; then \
      npm --prefix dashboard/site run build -- dist "${policy}" "${REVISION}"; \
    else \
      npm --prefix dashboard/site run build -- dist "${policy}"; \
    fi

FROM golang:1.27.3-alpine@sha256:f2c872bcee949b7be9c490efd02b48a8a2fbf947cdf533f1c58525ec9d8170fd AS server-build

ARG VERSION=dev
ARG REVISION=unknown
WORKDIR /workspace/server
COPY server/go.mod server/go.sum ./
COPY internal/dashboardquery/go.mod internal/dashboardquery/go.sum /workspace/internal/dashboardquery/
RUN go mod download
COPY server/ ./
COPY internal/dashboardquery/ /workspace/internal/dashboardquery/
RUN CGO_ENABLED=0 go build \
    -trimpath \
    -ldflags="-s -w -X main.version=${VERSION} -X main.revision=${REVISION}" \
    -o /out/cao-dashboard \
    ./cmd/cao-dashboard \
    && CGO_ENABLED=0 go build \
    -trimpath \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o /out/cao-webhook-gateway \
    ./cmd/cao-webhook-gateway

FROM alpine:3.22@sha256:5291449c3df73caf6ed85e649dec1b9e818b39a5d8c871e97afc13e9cd5e8fa8 AS dashboard-runtime

ARG VERSION=dev
ARG REVISION=unknown
ARG CREATED=unknown
LABEL org.opencontainers.image.title="Central Agentic Ops dashboard server" \
      org.opencontainers.image.description="Hosted CAO dashboard and canonical query service" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${REVISION}" \
      org.opencontainers.image.created="${CREATED}" \
      org.opencontainers.image.source="https://github.com/githubnext/gh-cao"

# Keep Alpine packages and CA roots current on rebuilds.
# hadolint ignore=DL3018
RUN apk upgrade --no-cache \
    && apk add --no-cache ca-certificates \
    && addgroup -S -g 65532 cao \
    && adduser -S -D -H -u 65532 -G cao cao \
    && mkdir -p /app/site /app/source /app/queries /app/agent \
    && chown -R cao:cao /app

COPY --from=server-build --chown=cao:cao /out/cao-dashboard /app/cao-dashboard
COPY --from=dashboard-build --chown=cao:cao /workspace/dashboard/site/dist/ /app/site/
COPY --from=dashboard-build --chown=cao:cao /workspace/dashboard/site/dashboard.json /app/dashboard.json
COPY --from=dashboard-build --chown=cao:cao /workspace/dashboard/site/src/data/queries/database.json /app/queries/database.json
COPY --from=dashboard-build --chown=cao:cao /workspace/dashboard/site/dist/src/agent/catalog.generated.json /app/agent/catalog.json
COPY --from=dashboard-build --chown=cao:cao /workspace/dashboard/site/dist/src/agent/queries.generated.json /app/agent/queries.json
COPY --from=dashboard-build --chown=cao:cao /workspace/dashboard/site/src/agent/mcp-contract.json /app/agent/mcp-contract.json
COPY --from=dashboard-build --chown=cao:cao /workspace/.github/workflows/cao.json /app/.github/workflows/cao.json
COPY --from=dashboard-build --chown=cao:cao /workspace/.github/workflows/cao.azure.json /app/.github/workflows/cao.azure.json

WORKDIR /app
USER 65532:65532
EXPOSE 8080
VOLUME ["/app/source"]
HEALTHCHECK --interval=30s --timeout=5s --start-period=30s --retries=3 \
  CMD ["sh", "-c", "host=\"${CAO_ALLOWED_HOSTS%%,*}\"; test -n \"${host}\" && wget -q -T 4 -O /dev/null --header=\"X-Forwarded-Host: ${host}\" --header=\"X-Forwarded-Proto: https\" http://127.0.0.1:8080/api/readiness"]

ENTRYPOINT ["/app/cao-dashboard"]
CMD ["serve-hosted", "--listen", "0.0.0.0:8080", "--site", "/app/site", "--dashboard-queries", "/app/agent/queries.json", "--database-queries", "/app/queries/database.json"]

FROM alpine:3.22@sha256:5291449c3df73caf6ed85e649dec1b9e818b39a5d8c871e97afc13e9cd5e8fa8 AS azure-functions-runtime

# hadolint ignore=DL3018
RUN apk upgrade --no-cache \
    && apk add --no-cache ca-certificates \
    && addgroup -S -g 65532 cao \
    && adduser -S -D -H -u 65532 -G cao cao \
    && mkdir -p /app/site /app/.github/workflows \
    && chown -R cao:cao /app

COPY --from=server-build --chown=cao:cao /out/cao-dashboard /app/cao-dashboard
COPY --from=dashboard-build --chown=cao:cao /workspace/dashboard/site/dist/ /app/site/
COPY --from=dashboard-build --chown=cao:cao /workspace/dashboard/site/src/data/queries/database.json /app/queries/database.json
COPY --from=dashboard-build --chown=cao:cao /workspace/.github/workflows/cao.json /app/.github/workflows/cao.json
COPY --from=dashboard-build --chown=cao:cao /workspace/.github/workflows/cao.azure.json /app/.github/workflows/cao.azure.json

ENV FUNCTIONS_CUSTOMHANDLER_HOST=0.0.0.0 \
    FUNCTIONS_CUSTOMHANDLER_PORT=8080 \
    CAO_DATABASE_QUERIES=/app/queries/database.json
WORKDIR /app
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/app/cao-dashboard"]
CMD ["azure-functions"]

FROM dashboard-runtime AS azure-ingest-runtime

COPY --from=cao-ingest-source --chown=cao:cao / /data/

ENTRYPOINT ["/app/cao-dashboard"]
CMD ["ingest", "--source", "/data", "--database-queries", "/app/queries/database.json"]

FROM dashboard-runtime AS azure-empty-ingest-runtime

COPY --chown=cao:cao server/azure/empty-data/ /data/

ENTRYPOINT ["/app/cao-dashboard"]
CMD ["ingest", "--source", "/data", "--database-queries", "/app/queries/database.json"]

FROM postgres:16-alpine@sha256:721873c34ceb9f8d8fc265984940dc982404c105f19ad51be9fdc5970a6080ea AS azure-db-provision

RUN addgroup -S -g 65532 cao && adduser -S -D -H -u 65532 -G cao cao
COPY --from=server-build /out/cao-dashboard /app/cao-dashboard
COPY server/postgres/*.sql /app/postgres/
COPY server/azure/provision.sh server/azure/provision.sql /app/azure/
WORKDIR /app
USER 65532:65532
ENTRYPOINT ["/bin/sh", "/app/azure/provision.sh"]

FROM dashboard-build AS control-plane-inventory

RUN REPORT_ROOT=/workspace \
    REPORT_INVENTORY=/tmp/control-plane-inventory.json \
    REPORT_INVENTORY_STATIC=true \
    node activity/inventory.mjs

FROM node:24-alpine@sha256:ebfe2f90462722a7a4de65e91990e97fe0d401c70e0e762c5b53302f905ec1c1 AS collector-runtime

SHELL ["/bin/ash", "-o", "pipefail", "-c"]

ARG VERSION=dev
ARG REVISION=unknown
ARG CREATED=unknown
ARG GH_AW_VERSION=v0.93.5
ARG TARGETARCH
LABEL org.opencontainers.image.title="Central Agentic Ops collector" \
      org.opencontainers.image.description="Hosted CAO collection worker and backfill runtime" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${REVISION}" \
      org.opencontainers.image.created="${CREATED}" \
      org.opencontainers.image.source="https://github.com/githubnext/gh-cao"

# Keep Alpine packages, CA roots, and the GitHub CLI current on rebuilds.
# hadolint ignore=DL3018
RUN apk upgrade --no-cache \
    && apk add --no-cache ca-certificates github-cli \
    && addgroup -S -g 65532 cao \
    && adduser -S -D -H -u 65532 -G cao cao \
    && mkdir -p /app/catalog /app/evidence /app/.local/share/gh/extensions/gh-aw \
    && case "${TARGETARCH}" in \
      amd64) checksum="1888d9faa40e102cbaa784ecb2e646c0e81ac3f1003d1cb3460d3a23908d4938" ;; \
      arm64) checksum="2bc9a5e212cab7fb02efa91ea362a27c381b1760af37f0effa3e5b3c5e8a6393" ;; \
      *) echo "Unsupported collector architecture: ${TARGETARCH}" >&2; exit 1 ;; \
    esac \
    && wget -q -O /usr/local/bin/gh-aw \
      "https://github.com/github/gh-aw/releases/download/${GH_AW_VERSION}/linux-${TARGETARCH}" \
    && echo "${checksum}  /usr/local/bin/gh-aw" | sha256sum -c - \
    && chmod 0755 /usr/local/bin/gh-aw \
    && ln -s /usr/local/bin/gh-aw /app/.local/share/gh/extensions/gh-aw/gh-aw \
    && chown -R cao:cao /app

COPY --from=dashboard-runtime --chown=cao:cao /app/ /app/
COPY --from=dashboard-build --chown=cao:cao /workspace/activity/ /app/catalog/activity/
COPY --from=dashboard-build --chown=cao:cao /workspace/.github/workflows/shared/control.mjs /app/catalog/.github/workflows/shared/control.mjs
COPY --from=dashboard-build --chown=cao:cao /workspace/.github/workflows/shared/policy.mjs /app/catalog/.github/workflows/shared/policy.mjs
COPY --from=dashboard-build --chown=cao:cao /workspace/.github/workflows/shared/authority.mjs /app/catalog/.github/workflows/shared/authority.mjs
COPY --from=dashboard-build --chown=cao:cao /workspace/.github/workflows/shared/operations.mjs /app/catalog/.github/workflows/shared/operations.mjs
COPY --from=control-plane-inventory --chown=cao:cao /tmp/control-plane-inventory.json /app/catalog/control-plane-inventory.json
# gh aw logs discovers agentic workflow names from local compiled workflows
# before it queries runs, so the collection catalog must retain the lock files.
COPY --from=dashboard-build --chown=cao:cao /workspace/.github/workflows/*.lock.yml /app/catalog/.github/workflows/
COPY --from=dashboard-build --chown=cao:cao /workspace/dashboard/report/ /app/catalog/dashboard/report/
COPY --from=dashboard-build --chown=cao:cao /workspace/dashboard/site/package.json /app/catalog/dashboard/site/package.json
COPY --from=dashboard-build --chown=cao:cao /workspace/dashboard/site/src/ /app/catalog/dashboard/site/src/
COPY --from=dashboard-build --chown=cao:cao /workspace/dashboard/site/node_modules/yaml/ /app/catalog/node_modules/yaml/
COPY --from=dashboard-build --chown=cao:cao /workspace/dependabot/ /app/catalog/dependabot/
COPY --from=dashboard-build --chown=cao:cao /workspace/optimization/ /app/catalog/optimization/

ENV HOME=/app \
    XDG_CACHE_HOME=/tmp/.cache \
    XDG_CONFIG_HOME=/tmp/.config
WORKDIR /app
USER 65532:65532
VOLUME ["/app/evidence"]
ENTRYPOINT ["/app/cao-dashboard"]
CMD ["collect", "--database-queries", "/app/queries/database.json"]

FROM alpine:3.22@sha256:5291449c3df73caf6ed85e649dec1b9e818b39a5d8c871e97afc13e9cd5e8fa8 AS webhook-gateway-runtime

# hadolint ignore=DL3018
RUN apk upgrade --no-cache \
    && apk add --no-cache ca-certificates \
    && addgroup -S -g 65532 cao \
    && adduser -S -D -H -u 65532 -G cao cao
COPY --from=server-build --chown=cao:cao /out/cao-webhook-gateway /app/cao-webhook-gateway
WORKDIR /app
USER 65532:65532
EXPOSE 8081
ENTRYPOINT ["/app/cao-webhook-gateway"]
CMD ["--policy", "/policy/cao.json"]

# Export only this target with BuildKit --output for a reviewed executor release.
FROM server-build AS hosted-cli-relay-build
ARG TARGETARCH
ARG REVISION
SHELL ["/bin/ash", "-o", "pipefail", "-c"]
WORKDIR /out
RUN printf '%s' "${REVISION}" | grep -Eq '^[0-9a-f]{40}$' \
    && CGO_ENABLED=0 GOOS=linux GOARCH="${TARGETARCH}" go -C /workspace/server build \
    -trimpath -buildvcs=false -ldflags="-s -w" \
    -o /out/cao-cli-relay ./cmd/cao-cli-relay \
    && sha256sum cao-cli-relay > cao-cli-relay.sha256 \
    && printf '{"source":"https://github.com/githubnext/gh-cao","revision":"%s","platform":"linux/%s","sha256":"%s"}\n' \
      "${REVISION}" "${TARGETARCH}" "$(cut -d ' ' -f1 cao-cli-relay.sha256)" > cao-cli-relay.json

FROM scratch AS hosted-cli-relay-artifact
COPY --from=hosted-cli-relay-build /out/cao-cli-relay /cao-cli-relay
COPY --from=hosted-cli-relay-build /out/cao-cli-relay.sha256 /cao-cli-relay.sha256
COPY --from=hosted-cli-relay-build /out/cao-cli-relay.json /cao-cli-relay.json

FROM dashboard-runtime AS final
