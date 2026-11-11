import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { parse } from "yaml";

const root = new URL("../../", import.meta.url);

test("Podman CI runs real rootless boundaries and Azure services on pull requests", async () => {
  const workflow = parse(await readFile(new URL(".github/workflows/podman.yml", root), "utf8"));
  for (const event of ["pull_request", "push"]) {
    for (const path of [".github/workflows/podman.yml", "server/**", "scripts/container-*.sh", "scripts/azure-local/**"]) {
      assert.ok(workflow.on[event].paths.includes(path), `${event} must cover ${path}`);
    }
  }
  assert.deepEqual(workflow.permissions, { contents: "read" });
  const job = workflow.jobs.podman;
  assert.equal(job.if, undefined, "pull requests must not skip the integration job");
  assert.equal(job.env.CAO_CONTAINER_ENGINE, "podman");
  assert.equal(job.env.CAO_TEST_PODMAN, "1");
  assert.ok(job["timeout-minutes"] > 0);
  assert.equal(job.steps.find((step) => step.uses?.startsWith("actions/checkout@")).with["persist-credentials"], false);
  assert.ok(job.steps.every((step) => !step["continue-on-error"]));
  const commands = job.steps.map((step) => step.run ?? "").join("\n");
  assert.match(commands, /loginctl enable-linger/);
  assert.match(commands, /systemd-run --user --scope --property=Delegate=yes/);
  assert.match(commands, /podman --remote=false pull "\$image"/);
  assert.match(commands, /container_runner\.go/);
  assert.match(commands, /go -C server test -race \.\/internal\/hostedcli/);
  assert.match(commands, /-run '\^TestPodmanRealBoundary\$' -count=1/);
  assert.match(commands, /npm run test:integration:azure-services/);
  assert.doesNotMatch(commands, /sudo (?:podman|go)|\|\| true/);
});

test("explicit real Podman validation cannot pass by skipping unavailable prerequisites", async () => {
  const source = await readFile(new URL("server/internal/hostedcli/podman_integration_test.go", root), "utf8");
  assert.equal((source.match(/t\.Skip\(/g) ?? []).length, 1, "only the opt-out may skip");
  assert.match(source, /if os\.Getenv\("CAO_TEST_PODMAN"\) != "1" \{\s*t\.Skip/);
  for (const reason of ["real Podman boundary requires Linux", "Podman is unavailable", "required rootless delegated", "the pinned test image is absent"]) {
    assert.ok(source.includes(`t.Fatal("${reason}`));
  }
});
