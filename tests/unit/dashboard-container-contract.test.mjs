import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

const root = new URL("../../", import.meta.url);

async function text(path) {
  return readFile(new URL(path, root), "utf8");
}

test("server and native CLI builds require the CA-compromise-patched Go toolchain", async () => {
  for (const path of ["go.mod", "server/go.mod", "internal/dashboardquery/go.mod"]) {
    const module = await text(path);
    assert.match(module, /^go 1\.27\.3$/m, `${path} must reject unpatched Go 1.27 toolchains`);
  }
  for (const path of [
    ".github/workflows/cgo.yml",
    ".github/workflows/cao-package.yml",
    ".github/workflows/release-promote.yml",
    ".github/workflows/backfill-stress.yml",
    ".github/workflows/mcp-parity.yml",
    ".github/workflows/dashboard-query-parity.yml",
    ".github/workflows/azure-local-integration.yml",
    ".github/workflows/copilot-setup-steps.yml",
  ]) {
    const workflow = await text(path);
    const versions = [...workflow.matchAll(/go-version:\s*(\S+)/g)].map((match) => match[1]);
    assert.ok(versions.length > 0, `${path} must declare a Go toolchain`);
    assert.ok(versions.every((version) => version === "1.27.3"), `${path} must use patched Go`);
  }
});

test("dashboard image is multi-stage, non-root, versioned, and health checked", async () => {
  const dockerfile = await text("server/Dockerfile");

  assert.match(dockerfile, /FROM node:24-alpine@sha256:[0-9a-f]{64} AS dashboard-build/);
  assert.match(dockerfile, /FROM golang:1\.27\.3-alpine@sha256:[0-9a-f]{64} AS server-build/);
  assert.match(dockerfile, /FROM alpine:3\.22@sha256:[0-9a-f]{64} AS dashboard-runtime/);
  assert.match(dockerfile, /ARG CAO_PROFILE=cao\.json/);
  assert.match(dockerfile, /ARG VERSION=dev/);
  assert.match(dockerfile, /ARG REVISION=unknown/);
  assert.match(dockerfile, /org\.opencontainers\.image\.revision="\$\{REVISION\}"/);
  assert.match(
    dockerfile,
    /COPY --from=dashboard-build --chown=cao:cao \/workspace\/\.github\/workflows\/cao\.json \/app\/\.github\/workflows\/cao\.json/,
  );
  assert.match(dockerfile, /USER 65532:65532/);
  assert.match(dockerfile, /HEALTHCHECK/);
  assert.match(dockerfile, /CMD \["sh", "-c"/);
  assert.doesNotMatch(dockerfile, /CMD \["CMD-SHELL"/);
  assert.match(dockerfile, /ENTRYPOINT \["\/app\/cao-dashboard"\]/);
  assert.match(dockerfile, /FROM dashboard-runtime AS final/);
});

test("collector image contains the Go worker, reviewed policy and Git without a Node runtime or lake", async () => {
  const dockerfile = await text("server/Dockerfile");
  const collector = dockerfile.split(/(?=^FROM )/m).find((stage) => stage.includes(" AS collector-runtime"));
  assert.ok(collector);
  assert.match(collector, /^FROM alpine:3\.22@sha256:[0-9a-f]{64} AS collector-runtime$/m);
  assert.match(collector, /apk add --no-cache ca-certificates git/);
  assert.match(collector, /COPY --from=server-build --chown=cao:cao \/out\/cao-dashboard \/app\/cao-dashboard/);
  assert.match(collector, /\/workspace\/\.github\/workflows\/cao\.json \/app\/\.github\/workflows\/cao\.json/);
  assert.match(collector, /\/workspace\/\.github\/value-evidence\/daily-file-diet\.jsonl\.gz \/app\/\.github\/value-evidence\/daily-file-diet\.jsonl\.gz/);
  assert.match(collector, /\/workspace\/dashboard\/site\/src\/data\/queries\/database\.json \/app\/queries\/database\.json/);
  assert.match(collector, /USER 65532:65532/);
  assert.match(collector, /CMD \["collect", "--database-queries", "\/app\/queries\/database\.json"\]/);
  assert.doesNotMatch(collector, /\bnode\b|github-cli|gh-aw|\/app\/evidence|VOLUME|HEALTHCHECK|\/app\/site/);
});

test("Hosted CLI relay exports a separate source-bound static artifact", async () => {
  const dockerfile = await text("server/Dockerfile");
  const build = dockerfile.split("FROM server-build AS hosted-cli-relay-build\n")[1]
    ?.split("FROM scratch AS hosted-cli-relay-artifact\n")[0];
  assert.ok(build, "relay must reuse the pinned server build stage");
  assert.match(build, /ARG REVISION/);
  assert.ok(build.includes("grep -Eq '^[0-9a-f]{40}$'"));
  assert.match(build, /CGO_ENABLED=0 GOOS=linux GOARCH="\$\{TARGETARCH\}"/);
  assert.match(build, /\.\/cmd\/cao-cli-relay/);
  assert.match(build, /sha256sum cao-cli-relay > cao-cli-relay\.sha256/);
  assert.match(build, /"revision":"%s","platform":"linux\/%s","sha256":"%s"/);
  for (const file of ["cao-cli-relay", "cao-cli-relay.sha256", "cao-cli-relay.json"]) {
    assert.ok(dockerfile.includes(`COPY --from=hosted-cli-relay-build /out/${file} /${file}`));
  }
  assert.ok(dockerfile.trimEnd().endsWith("FROM dashboard-runtime AS final"),
    "the relay export must not change the default dashboard image");
});
