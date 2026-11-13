import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { join } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { parse } from "yaml";

const root = new URL("../../", import.meta.url);
const execute = promisify(execFile);
const workflow = parse(await readFile(new URL(".github/workflows/podman.yml", root), "utf8"));

test("Podman CI runs real rootless boundaries on pull requests without retired Azure services", () => {
  for (const event of ["pull_request", "push"]) {
    for (const path of [".github/workflows/podman.yml", "server/**", "scripts/container-*.sh"]) {
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
  assert.match(commands, /podman-bin\/server\/hostedcli\.test/);
  assert.match(commands, /-test.run '\^TestPodmanRealBoundary\$' -test.count=1/);
  assert.deepEqual(Object.keys(workflow.jobs), ["podman"]);
  assert.match(commands, /node --test tests\/unit\/container-engine\.test\.mjs/);
  assert.doesNotMatch(JSON.stringify(workflow), /azure-local|Core Tools|azure-services|azure-protocols/);
  assert.doesNotMatch(commands, /sudo (?:podman|go)|\|\| true/);
});

test("Podman CI reuses only exact-source race-instrumented executables", () => {
  const steps = workflow.jobs.podman.steps;
  const go = steps.find((step) => step.uses?.startsWith("actions/setup-go@"));
  assert.equal(go.if, "steps.binaries.outputs.cache-hit != 'true'");
  assert.equal(go.with.cache, true);
  assert.deepEqual(go.with["cache-dependency-path"].trim().split("\n"), ["go.sum", "server/go.sum"]);
  const binaries = steps.find((step) => step.id === "binaries");
  assert.equal(binaries.with.path, ".tmp/podman-bin");
  for (const input of ["runner.os", "runner.arch", "'server/**'", "'internal/**'", "'go.mod'", "'go.sum'", "'.github/workflows/podman.yml'"]) {
    assert.ok(binaries.with.key.includes(input), `binary cache must bind ${input}`);
  }
  assert.equal(binaries.with["restore-keys"], undefined, "stale-source executables must never be reused");
  const build = steps.find((step) => step.name === "Build current-source executable on cache miss").run;
  assert.match(build, /test -race -c/);
  assert.match(build, /\.\/internal\/hostedcli/);
});

for (const failure of ["", "boundary", "contracts"]) {
  test(`Podman concurrent checks wait for every lane and preserve ${failure || "success"}`, async () => {
    const directory = await mkdtemp(join(fileURLToPath(root), ".podman-ci-"));
    const trace = join(directory, "trace");
    try {
      for (const [command, lane] of [["podman", "boundary"], ["node", "contracts"]]) {
        await writeFile(join(directory, command), `#!/bin/bash
set -eu
touch "$CHECK_DIRECTORY/${lane}"
for (( attempt=0; attempt<100; attempt++ )); do
  if [[ -f "$CHECK_DIRECTORY/boundary" && -f "$CHECK_DIRECTORY/contracts" ]]; then
    printf '${lane}-done\\n' >> "$CHECK_TRACE"
    if [[ "$CHECK_FAILURE" == '${lane}' ]]; then exit 42; fi
    exit 0
  fi
  sleep 0.02
done
echo "checks did not run concurrently" >&2
exit 99
`, { mode: 0o700 });
      }
      await writeFile(join(directory, "systemd-run"), '#!/bin/bash\nprintf "boundary-run\\n" >> "$CHECK_TRACE"\n', { mode: 0o700 });
      const script = workflow.jobs.podman.steps.find((step) => step.name === "Run independent rootless and container checks concurrently").run;
      const result = await execute("/bin/bash", ["-e", "-c", script], {
        cwd: fileURLToPath(root),
        env: {
          ...process.env,
          PATH: `${directory}:${process.env.PATH}`,
          CHECK_DIRECTORY: directory,
          CHECK_TRACE: trace,
          CHECK_FAILURE: failure,
          GITHUB_WORKSPACE: fileURLToPath(root),
        },
        timeout: 5000,
      }).then(() => 0, (error) => error.code);
      assert.equal(result, failure ? 42 : 0);
      const lines = (await readFile(trace, "utf8")).trim().split("\n");
      for (const lane of ["boundary", "contracts"]) assert.ok(lines.includes(`${lane}-done`));
      assert.equal(lines.includes("boundary-run"), failure !== "boundary", "failed image preload must not run the workload");
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  });
}

test("explicit real Podman validation cannot pass by skipping unavailable prerequisites", async () => {
  const source = await readFile(new URL("server/internal/hostedcli/podman_integration_test.go", root), "utf8");
  assert.equal((source.match(/t\.Skip\(/g) ?? []).length, 1, "only the opt-out may skip");
  assert.match(source, /if os\.Getenv\("CAO_TEST_PODMAN"\) != "1" \{\s*t\.Skip/);
  for (const reason of ["real Podman boundary requires Linux", "Podman is unavailable", "required rootless delegated", "the pinned test image is absent"]) {
    assert.ok(source.includes(`t.Fatal("${reason}`));
  }
});
