import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { pathToFileURL } from "node:url";
import test from "node:test";
import { parse } from "yaml";
import { buildCaoRuntime, runtimeResources, validateCaoRuntimeArtifact } from "../../scripts/build-cao-runtime.mjs";

const catalog = path.resolve(import.meta.dirname, "../..");
const revision = "1".repeat(40);
const temporary = mkdtempSync(path.join(tmpdir(), "cao-runtime-artifact-"));
const artifact = buildCaoRuntime(catalog, path.join(temporary, "built"), revision);
test.after(() => rmSync(temporary, { recursive: true, force: true }));

function unpack(t) {
  const destination = mkdtempSync(path.join(temporary, "unpacked-"));
  t.after(() => rmSync(destination, { recursive: true, force: true }));
  execFileSync("tar", ["-xzf", artifact.archive, "-C", destination]);
  return path.join(destination, "package");
}

test("producer artifact binds its production inventory to an immutable revision", (t) => {
  const root = unpack(t);
  validateCaoRuntimeArtifact(root, revision);
  const metadata = JSON.parse(readFileSync(path.join(root, "cao-runtime.json"), "utf8"));
  assert.equal(metadata.revision, revision);
  assert.ok(metadata.files["dashboard/site/src/vendor/yaml.js"]);
  assert.ok(metadata.files["dashboard/site/src/vendor/yaml.LICENSE"]);
  assert.equal(metadata.files["activity/setup-prompt.mjs"], undefined);
  for (const file of [
    "activity/setup.mjs", "activity/upgrade-gh-aw.mjs",
    ...["authentication", "campaigns", "gh-aw", "packages", "policy"].map(name => `activity/cli/${name}.mjs`),
    ...["init", "setup", "setup-auth", "add", "mode", "enable", "disable", "update", "upgrade-gh-aw"]
      .map(name => `activity/commands/${name}.mjs`),
  ]) {
    assert.equal(metadata.files[file], undefined, file);
    assert.equal(existsSync(path.join(root, file)), false, file);
  }
  assert.ok(metadata.files["dashboard/report/jsonl.mjs"]);
  assert.equal(Object.keys(metadata.files).some(file => file.startsWith("activity/")), false);
  assert.equal(metadata.files["activity/cli/repositories.mjs"], undefined);
  assert.equal(existsSync(path.join(root, "activity/cli/repositories.mjs")), false);
  for (const file of [
    "jsonl_helpers.cjs", "json_schema_validation.cjs", "mcp_scripts_validation.cjs",
    "memory_schema_contract.cjs", "LICENSE", "source.json",
  ]) assert.ok(metadata.files[`dashboard/report/vendor/gh-aw/${file}`]);
  assert.ok(metadata.files["dashboard/server-process.mjs"]);
  for (const file of [
    "dashboard/cli-terminal-process.mjs",
    "dashboard/cli-terminal-child.mjs",
    "dashboard/site/scripts/prepare-terminal.mjs",
  ]) assert.ok(metadata.files[file], `missing terminal runtime file: ${file}`);
  for (const file of [
    "dashboard/environment-defaults.mjs",
    "dashboard/site/src/environment-defaults.js",
  ]) assert.ok(metadata.files[file], `missing Governance runtime file: ${file}`);
  const dashboardManifest = JSON.parse(readFileSync(path.join(root, "dashboard/site/package.json"), "utf8"));
  const dashboardLock = JSON.parse(readFileSync(path.join(root, "dashboard/site/package-lock.json"), "utf8"));
  assert.ok(dashboardManifest.dependencies["node-pty"]);
  assert.equal(dashboardManifest.scripts.postinstall, "node scripts/prepare-terminal.mjs");
  assert.equal(dashboardLock.packages[""].dependencies["node-pty"], dashboardManifest.dependencies["node-pty"]);
  assert.ok(dashboardLock.packages["node_modules/node-pty"].hasInstallScript);
  assert.equal(existsSync(path.join(root, "node_modules")), false);
  assert.match(readFileSync(path.join(root, "dashboard/site/src/validator-dashboard.js"), "utf8"), /from '\.\/vendor\/yaml\.js'/);
  assert.doesNotMatch(readFileSync(path.join(root, "dashboard/site/src/environment-defaults.js"), "utf8"), /from ['"].*yaml/);
  assert.equal(existsSync(path.join(root, "dashboard/site/node_modules")), false);
  assert.throws(() => validateCaoRuntimeArtifact(root, "2".repeat(40)), /provenance does not match/);
});

test("the prebuilt archive loads the Governance CLI transport with dependency-free defaults parsing", async (t) => {
  const root = unpack(t);
  const { executeDashboardCommand } = await import(pathToFileURL(path.join(root, "dashboard/cli-actions.mjs")));
  const yaml = 'default_max_turns: "20"\n';
  const result = await executeDashboardCommand({
    command: "gh aw env get cao-governance.yml --scope org --org acme",
    workingDirectory: root,
    githubToken: "test-fixture",
    execute: async (_executable, args, options) => {
      if (!args.includes("--help")) writeFileSync(path.join(options.cwd, "cao-governance.yml"), yaml);
      return { stdout: "", stderr: "" };
    },
  });
  assert.equal(result.ok, true);
  assert.equal(result.stdout, yaml);
});

test("producer archives are reproducible across temporary build directories", () => {
  const rebuilt = buildCaoRuntime(catalog, path.join(temporary, "rebuilt"), revision);
  assert.equal(rebuilt.digest, artifact.digest);
  assert.deepEqual(readFileSync(rebuilt.archive), readFileSync(artifact.archive));
});

test("artifact verification rejects altered, missing and unowned runtime files", (t) => {
  const root = unpack(t);
  const cli = path.join(root, "dashboard/report/configure-site.mjs");
  const source = readFileSync(cli);
  writeFileSync(cli, "altered");
  assert.throws(() => validateCaoRuntimeArtifact(root, revision), /checksum mismatch: dashboard\/report\/configure-site.mjs/);
  rmSync(cli);
  assert.throws(() => validateCaoRuntimeArtifact(root, revision), /file inventory does not match/);
  writeFileSync(cli, source);
  writeFileSync(path.join(root, "plugin.json"), "{}");
  const metadataPath = path.join(root, "cao-runtime.json");
  const metadata = JSON.parse(readFileSync(metadataPath, "utf8"));
  metadata.files["plugin.json"] = "0".repeat(64);
  writeFileSync(metadataPath, JSON.stringify(metadata));
  assert.throws(() => validateCaoRuntimeArtifact(root, revision), /unowned path: plugin.json/);
});

test("the runtime package ships only owned runtime resources without development or plugin files", () => {
  assert.ok(artifact.files.includes("dashboard/report/configure-site.mjs"));
  assert.ok(artifact.files.includes("dashboard/site/scripts/build.mjs"));
  assert.ok(artifact.files.includes("dashboard/site/src/data-worker.js"));
  assert.ok(artifact.files.includes("dashboard/site/package-lock.json"));
  for (const file of artifact.files) {
    assert.doesNotMatch(file, /(?:^|\/)(?:test|tests|fixtures|screenshots|skills|com\.github\.copilot)(?:\/|$)/);
    assert.doesNotMatch(file, /(?:^|\/)(?:PLAN\.md|eslint\.config\.mjs|vitest\.config\.mjs|playwright\.config\.mjs|plugin\.json)$/);
  }
});

test("root installation provides every local action used by its installed workflows", () => {
  const installed = new Set();
  const workflows = [];
  const visit = (manifestPath) => {
    const manifest = parse(readFileSync(path.join(catalog, manifestPath), "utf8"));
    const manifestDirectory = path.posix.dirname(manifestPath);
    for (const { destination } of manifest.resources ?? []) installed.add(destination);
    for (const include of manifest.includes ?? []) {
      const source = typeof include === "string" ? include : include.source;
      const resolved = source.startsWith(".github/")
        ? source
        : path.posix.normalize(path.posix.join(manifestDirectory, source));
      if (path.posix.basename(resolved) === "aw.yml") visit(resolved);
      else workflows.push(resolved);
    }
  };
  visit("aw.yml");
  const provided = (file) =>
    installed.has(file) || runtimeResources.some((resource) => file === resource || file.startsWith(`${resource}/`));

  assert.ok(workflows.length > 0);
  for (const workflow of workflows) {
    const source = readFileSync(path.join(catalog, workflow), "utf8");
    for (const [, action] of source.matchAll(/^\s*(?:-\s+)?uses:\s*\.\/([^\s#]+)/gm)) {
      const directory = action.replace(/\/+$/, "");
      assert.ok(
        provided(`${directory}/action.yml`) || provided(`${directory}/action.yaml`),
        `${workflow} uses ./${directory}, which root installation does not provide`,
      );
    }
  }
});

test("the trusted runtime pipeline separates building and attestation from draft asset publication", () => {
  const source = readFileSync(path.join(catalog, ".github/workflows/cao-runtime.yml"), "utf8");
  const workflow = parse(source);
  assert.deepEqual(workflow.on.push.branches, ["main"]);
  assert.equal(workflow.on.release, undefined);
  assert.equal(workflow.on.pull_request, undefined);
  assert.equal(workflow.on.workflow_dispatch, undefined);
  assert.equal(workflow.jobs.build.permissions.contents, "read");
  assert.equal(workflow.jobs.build.permissions["id-token"], undefined);
  const setupNode = workflow.jobs.build.steps.find((step) => step.uses?.startsWith("actions/setup-node@"));
  assert.ok(setupNode);
  assert.equal(setupNode.with.cache, undefined);
  assert.equal(setupNode.with["package-manager-cache"], false);
  assert.equal(workflow.jobs.attest.permissions["id-token"], "write");
  assert.equal(workflow.jobs.publish, undefined);
  assert.match(source, /github\.repository == 'githubnext\/gh-cao'/);
  assert.match(source, /retention-days: 90/);
  assert.doesNotMatch(source, /gh release upload|--clobber|npm publish/);
  const attest = workflow.jobs.attest;
  assert.ok(!attest.steps.some((step) => step.uses?.startsWith("actions/checkout")));
  assert.doesNotMatch(attest.steps.map((step) => step.run ?? "").join("\n"), /npm|node |tar |bash .*\.sh/);
  for (const match of source.matchAll(/uses:\s+[^@\s]+@([^\s#]+)/g)) assert.match(match[1], /^[0-9a-f]{40}$/);
});

test("the trusted runtime pipeline seals and attests the Azure deployment assets at the exact SHA", () => {
  const workflow = parse(readFileSync(path.join(catalog, ".github/workflows/cao-runtime.yml"), "utf8"));
  const build = workflow.jobs.build.steps;
  const azureBuild = build.findIndex((step) => step.run === 'npm run build:azure-assets -- "${RUNNER_TEMP}/azure" "${REVISION}"');
  assert.ok(azureBuild > 0);
  assert.equal(build[azureBuild].env.REVISION, "${{ github.sha }}");
  assert.equal(build[azureBuild + 1].with.name, "cao-azure-${{ github.sha }}");
  assert.equal(build[azureBuild + 1].with["if-no-files-found"], "error");
  const attest = workflow.jobs.attest.steps;
  const download = attest.findIndex((step) => step.with?.name === "cao-azure-${{ github.sha }}");
  assert.ok(download > 0);
  assert.equal(attest[download + 1].run, 'sha256sum --check "cao-azure-${REVISION}.tgz.sha256"');
  assert.match(attest[download + 2].uses, /^actions\/attest@[0-9a-f]{40}$/);
  assert.equal(attest[download + 2].with["subject-path"], "${{ runner.temp }}/azure/cao-azure-${{ github.sha }}.tgz");
});
