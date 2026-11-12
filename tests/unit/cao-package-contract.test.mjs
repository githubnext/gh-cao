import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { parse } from "yaml";

const root = new URL("../../", import.meta.url);

async function text(path) {
  return readFile(new URL(path, root), "utf8");
}

function maxEchoedDetailsDepth(source) {
  let depth = 0;
  let maximum = 0;
  for (const line of source.split(/\r?\n/)) {
    if (line.includes('echo "<details')) {
      depth += 1;
      maximum = Math.max(maximum, depth);
    }
    if (line.includes('echo "</details>"')) {
      depth -= 1;
      assert.ok(depth >= 0, "summary details are unbalanced");
    }
  }
  assert.equal(depth, 0, "summary details are unbalanced");
  return maximum;
}

test("CAO server package workflow publishes immutable Compose-ready images", async () => {
  const source = await text(".github/workflows/cao-package.yml");
  const publisherSource = await text(".github/workflows/cao-package-publish.yml");
  const workflow = parse(source);
  const publisher = parse(publisherSource);

  assert.deepEqual(workflow.on.push.branches, ["main"]);
  assert.deepEqual(workflow.on.release.types, ["released"]);
  assert.equal(workflow.on.workflow_dispatch.inputs.source_branch.required, true);
  assert.equal(workflow.on.pull_request, undefined);
  assert.equal(workflow.permissions.contents, "read");
  assert.deepEqual(workflow.jobs.build.permissions, { contents: "read" });
  assert.equal(workflow.jobs.publish.permissions.packages, "write");
  assert.equal(workflow.jobs.publish.permissions.attestations, "write");
  assert.equal(workflow.jobs.publish.permissions["id-token"], "write");
  assert.equal(
    workflow.jobs.publish.uses,
    "githubnext/gh-cao/.github/workflows/cao-package-publish.yml@main",
  );
  assert.deepEqual(workflow.jobs.publish.needs, ["source", "lint", "test", "build"]);
  assert.equal(publisher.on.workflow_call.inputs.revision.required, true);
  assert.equal(publisher.jobs.publish.permissions.packages, "write");
  assert.equal(publisher.jobs.publish.permissions.attestations, "write");
  assert.equal(publisher.jobs.publish.permissions["id-token"], "write");
  assert.deepEqual(workflow.jobs.build.needs, ["source", "lint", "test"]);
  assert.equal(workflow.jobs.lint.name, "Lint container and workflow sources");
  assert.equal(workflow.jobs.test.name, "Test package sources");
  assert.equal(workflow.jobs.build.name, "Build, scan, and seal candidate");
  assert.deepEqual(workflow.jobs.test.permissions, { contents: "read" });
  assert.equal(workflow.jobs.source.steps[0].id, "repository-origin");
  assert.equal(workflow.jobs.source.steps[0].name, "Reject fork package sources");
  assert.doesNotMatch(
    workflow.jobs.build.steps.map((step) => step.run ?? "").join("\n"),
    /npm ci|go -C server test|dashboard:server:build/,
  );

  assert.match(publisherSource, /ghcr\.io\/\$\{REPOSITORY,,\}\/cao-server/);
  assert.match(source, /identity="sha-\$\{SHA\}"/);
  assert.match(source, /identity="dispatch-\$\{DISPATCH_REVISION\}"/);
  assert.match(source, /version="0\.0\.0-main\.\$\{SHA:0:12\}"/);
  assert.match(source, /version="0\.0\.0-dispatch\.\$\{DISPATCH_REVISION:0:12\}"/);
  assert.match(source, /Manual package publication requires maintain or admin repository permission/);
  assert.match(source, /Manual package publication must run from the current default-branch workflow/);
  assert.match(source, /Manual package publication requires a source branch/);
  assert.match(source, /Requested source branch resolved to its current head/);
  assert.match(source, /CAO server packages cannot be published from forks/);
  assert.doesNotMatch(source, /context\.payload\.repository\.fork/);
  assert.ok((source.match(/core\.info\(/g) ?? []).length >= 12);
  assert.match(source, /Manual package authorization completed/);
  assert.match(source, /Stable release source resolution completed/);
  assert.match(source, /Published release tag must be an exact stable version vX\.Y\.Z/);
  assert.doesNotMatch(source, /prereleaseTag|vX\.Y\.Z-prerelease/);
  assert.match(source, /--file server\/Dockerfile/);
  assert.match(source, /--build-arg "CAO_PROFILE=cao\.json"/);
  assert.match(source, /podman build \\\n\s+--format docker \\\n\s+--file server\/Dockerfile/);
  assert.match(source, /podman save --format docker-archive --output "\$\{RUNNER_TEMP\}\/cao-server\.tar"/);
  assert.match(source, /input: \$\{\{ runner\.temp \}\}\/cao-server\.tar/);
  assert.equal((source.match(/image: docker-archive:\$\{\{ runner\.temp \}\}\/cao-server\.tar/g) ?? []).length, 2);
  assert.match(source, /--input \/candidate\/cao-server\.tar/);
  assert.match(publisherSource, /podman load --input cao-server\.tar/);
  assert.match(publisherSource, /podman login --authfile "\$\{auth_file\}" --username "\$\{REGISTRY_USERNAME\}" --password-stdin ghcr\.io/);
  assert.match(publisherSource, /skopeo inspect --raw/);
  assert.match(publisherSource, /skopeo copy --quiet --all --preserve-digests/);
  for (const content of [source, publisherSource]) {
    assert.doesNotMatch(content, /\bdocker (?:run|build|image|tag|push|buildx)\b|docker\.sock|docker\/login-action/);
  }
  assert.match(source, /go -C server test \.\/\.\.\./);
  assert.match(source, /npm run dashboard:server:build/);
  assert.match(source, /aquasecurity\/trivy-action@[0-9a-f]{40}/);
  assert.match(source, /severity: CRITICAL,HIGH/);
  assert.match(source, /anchore\/scan-action@[0-9a-f]{40}/);
  assert.match(source, /anchore\/sbom-action@[0-9a-f]{40}/);
  assert.match(source, /hadolint\/hadolint:v2\.15\.1-alpine@sha256:[0-9a-f]{64}/);
  assert.match(source, /goodwithtech\/dockle:v0\.4\.15@sha256:[0-9a-f]{64}/);
  assert.match(source, /ghcr\.io\/zizmorcore\/zizmor:1\.30\.1@sha256:[0-9a-f]{64}/);
  assert.match(source, /rhysd\/actionlint:1\.7\.12@sha256:[0-9a-f]{64}/);
  assert.match(source, /cao-server-static-checks-\$\{\{ github\.run_id \}\}-\$\{\{ github\.run_attempt \}\}/);
  assert.match(source, /static-check-results\/hadolint\.txt/);
  assert.match(source, /static-check-results\/actionlint\.txt/);
  assert.match(source, /static-check-results\/zizmor\.txt/);
  assert.match(source, /retention-days: 7/);
  for (const id of ["hadolint", "actionlint", "zizmor"]) {
    const step = workflow.jobs.lint.steps.find((candidate) => candidate.id === id);
    assert.equal(step.if, "always()", `${id} must run even if an earlier static check fails`);
  }
  assert.match(source, /release package source is not reachable from protected main/);
  assert.match(source, /published release tag no longer matches the tested package source/);
  assert.match(publisherSource, /sha256sum --check cao-server\.sha256/);
  assert.match(source, /cao-server-metadata\.json/);
  assert.match(publisherSource, /actions\/attest@[0-9a-f]{40}/);
  assert.match(publisherSource, /push-to-registry: true/);
  assert.match(publisherSource, /Privileged publication requires the current default-branch caller workflow/);
  assert.match(publisherSource, /Manual package metadata does not match the requested branch head/);
  assert.ok((publisherSource.match(/core\.info\(/g) ?? []).length >= 25);
  assert.match(publisherSource, /Protected-main release ancestry check passed/);
  assert.match(source, /<details>/);
  assert.match(source, /Trivy CVE scan\|\$\{TRIVY_STATUS\}/);
  assert.match(source, /findings and inventory are not copied to the summary/);
  assert.match(publisherSource, /Publisher is loaded from protected main/);
  assert.match(publisherSource, /candidate-\$\{RUN_ID\}-\$\{RUN_ATTEMPT\}/);
  assert.match(publisherSource, /refusing to redefine an existing immutable package identity/);
  assert.match(publisherSource, /canonical package identity does not equal the scanned candidate digest/);
  assert.doesNotMatch(source, /cao-server:(?:latest|main|stable|beta|alpha)\b/);
  assert.equal(maxEchoedDetailsDepth(source), 1);
  assert.equal(maxEchoedDetailsDepth(publisherSource), 1);

  assert.doesNotMatch(publisherSource, /actions\/checkout@/);
  assert.doesNotMatch(publisherSource, /npm ci|go -C server test|(?:docker|podman) build\s|trivy-action|scan-action/);

  for (const match of `${source}\n${publisherSource}`.matchAll(/uses:\s+[^@\s]+@([^\s#]+)/g)) {
    if (match[0].includes("cao-package-publish.yml@main")) continue;
    assert.match(match[1], /^[0-9a-f]{40}$/, `action is not pinned: ${match[0]}`);
  }
});

test("CAO server image supports downstream Compose role selection", async () => {
  const dockerfile = await text("server/Dockerfile");
  const runtimeStage = (name) =>
    dockerfile.split(/(?=^FROM )/m).find((stage) => stage.includes(` AS ${name}`));
  const dashboardRuntime = runtimeStage("dashboard-runtime");
  const collectorRuntime = runtimeStage("collector-runtime");

  assert.ok(dashboardRuntime);
  assert.ok(collectorRuntime);
  assert.match(dashboardRuntime, /RUN apk upgrade --no-cache \\\n    && apk add --no-cache ca-certificates/);
  assert.match(collectorRuntime, /RUN apk upgrade --no-cache \\\n    && apk add --no-cache ca-certificates git/);
  assert.match(dockerfile, /ENTRYPOINT \["\/app\/cao-dashboard"\]/);
  assert.match(dockerfile, /CMD \["serve-hosted"/);
  assert.match(dockerfile, /VOLUME \["\/app\/source"\]/);
  assert.match(dockerfile, /USER 65532:65532/);
});

test("workflow linters allow read-only cache authority unsupported by the pinned actionlint", async () => {
  const workflows = await Promise.all([
    text(".github/workflows/action-lint.yml"),
    text(".github/workflows/cao-package.yml"),
  ]);
  for (const source of workflows) {
    const workflow = parse(source);
    const step = workflow.jobs.lint.steps.find((candidate) =>
      candidate.run?.includes("rhysd/actionlint:"),
    );
    assert.ok(step, `${workflow.name} must run actionlint`);
    assert.ok(
      step.run.includes(`-ignore '^unexpected key "cache-mode" for "workflow" section\\.'`),
      `${workflow.name} must allow the cache-mode syntax checked by release promotion contracts`,
    );
  }
});

test("CAO server package installs build dependencies before verifying API artifacts", async () => {
  const workflow = parse(await text(".github/workflows/cao-package.yml"));
  const build = workflow.jobs.test.steps.find((step) => step.id === "dashboard-build");
  assert.ok(build);
  const commands = build.run.trim().split("\n").map((line) => line.trim());
  const buildIndex = commands.indexOf("npm run dashboard:server:build");
  assert.ok(buildIndex >= 0);
  for (const install of [
    "npm --prefix dashboard/site ci",
    "npm --prefix server/spec ci",
  ]) {
    const index = commands.indexOf(install);
    assert.ok(index >= 0 && index < buildIndex, `${install} must precede the dashboard build`);
  }
});

test("Docker dashboard builder installs production tooling without shipping build dependencies", async () => {
  const dockerfile = await text("server/Dockerfile");
  const stages = dockerfile.split(/(?=^FROM )/m);
  const builder = stages.find((stage) => stage.includes(" AS dashboard-build"));
  assert.ok(builder);
  const install = builder.indexOf("npm --prefix dashboard/site ci --omit=dev --ignore-scripts");
  assert.ok(install >= 0, "the builder must install esbuild even with NODE_ENV=production");
  const manifest = JSON.parse(await text("dashboard/site/package.json"));
  assert.ok(manifest.dependencies.esbuild, "esbuild is required by the production build");
  assert.doesNotMatch(builder, /--include=dev/);
  for (const name of ["dashboard-runtime", "azure-functions-runtime"]) {
    const runtime = stages.find((stage) => stage.includes(` AS ${name}`));
    assert.ok(runtime);
    assert.doesNotMatch(runtime, /node_modules|control-plane-inventory\.json/);
    assert.match(runtime, /\/workspace\/dashboard\/site\/dist\/ \/app\/site\//);
  }
  const collector = stages.find((stage) => stage.includes(" AS collector-runtime"));
  assert.match(collector, /\/workspace\/\.github\/workflows\/cao\.json \/app\/\.github\/workflows\/cao\.json/);
  assert.doesNotMatch(collector, /node_modules|control-plane-inventory\.json|\/app\/catalog\//);
  assert.doesNotMatch(collector, /COPY .*\/workspace\/dashboard\/site\/node_modules\/ \/app\//);
});
