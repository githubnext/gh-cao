import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { cpSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import { pathToFileURL } from "node:url";
import test from "node:test";
import { parse } from "yaml";
import { extractCaoArchive, planCaoMaterialization, validateCaoRuntimeArtifact } from "../../.github/workflows/shared/materialize-cao.mjs";
import { buildCaoRuntime } from "../../scripts/build-cao-runtime.mjs";

const catalog = path.resolve(import.meta.dirname, "../..");

function run(command, arguments_, cwd) {
  if (process.platform === "win32" && command === "npm") {
    arguments_ = [
      process.env.npm_execpath ?? path.join(path.dirname(process.execPath), "node_modules", "npm", "bin", "npm-cli.js"),
      ...arguments_,
    ];
    command = process.execPath;
  }
  const result = spawnSync(command, arguments_, {
    cwd,
    encoding: "utf8",
    maxBuffer: 16 * 1024 * 1024,
    timeout: 120_000,
  });
  assert.equal(result.status, 0, result.stderr || result.error?.message || result.stdout);
  return result.stdout;
}

test("a producer archive runs and builds the dashboard from a fresh documented installation commit", { timeout: 180_000 }, async () => {
  const temporary = mkdtempSync(path.join(tmpdir(), "cao-runtime-installation-"));
  const installed = path.join(temporary, "installed");
  const checkout = path.join(temporary, "checkout");
  try {
    mkdirSync(installed);
    const revision = "1".repeat(40);
    const artifact = buildCaoRuntime(catalog, path.join(temporary, "artifacts"), revision);
    const unpacked = path.join(temporary, "unpacked");
    mkdirSync(unpacked);
    extractCaoArchive(artifact.archive, unpacked, true);
    const packedRoot = path.join(unpacked, "package");
    validateCaoRuntimeArtifact(packedRoot, revision);
    const manifest = parse(readFileSync(path.join(catalog, "aw.yml"), "utf8"));
    for (const { source, destination } of manifest.resources) {
      const target = path.join(installed, destination);
      mkdirSync(path.dirname(target), { recursive: true });
      cpSync(path.join(catalog, source), target);
    }
    cpSync(
      path.join(catalog, ".github/workflows/aw.json"),
      path.join(installed, ".github/workflows/aw.json"),
    );
    const [{ resources }] = planCaoMaterialization([{
      name: "githubnext/gh-cao", record: { resolvedCommit: revision },
    }], "root");
    for (const resource of resources) {
      const target = path.join(installed, resource);
      mkdirSync(path.dirname(target), { recursive: true });
      cpSync(path.join(packedRoot, resource), target, { recursive: true });
    }
    for (const workflow of ["cao-validate.yml"]) {
      cpSync(path.join(catalog, ".github/workflows", workflow), path.join(installed, ".github/workflows", workflow));
    }
    writeFileSync(path.join(installed, ".github/workflows/cao.json"), JSON.stringify({version: 1, "gh-aw-version": manifest["min-version"], "control-plane": {scope: {"allowed-owners": ["example"], "allowed-repositories": ["example/control"]}, campaigns: {}}}));
    run("git", ["init", "--quiet"], installed);
    run("git", ["add", ".github", "dashboard"], installed);
    run("git", ["diff", "--cached", "--check"], installed);
    assert.equal(run("git", ["ls-files", "--others", "--exclude-standard"], installed), "");
    run("git", [
      "-c", "user.name=CAO runtime test", "-c", "user.email=cao-test@example.invalid",
      "-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null",
      "commit", "--quiet", "-m", "Install CAO runtime",
    ], installed);
    run("git", ["clone", "--quiet", "--no-local", installed, checkout], temporary);
    for (const bundle of ["dashboard"]) {
      run(process.execPath, [".github/workflows/shared/materialize-cao.mjs", "verify", bundle], checkout);
    }
    assert.equal(existsSync(path.join(checkout, "activity")), false);
    assert.equal(existsSync(path.join(checkout, "dashboard/site/src/data/storage/sqlite-indexeddb.js")), false);
    assert.equal(existsSync(path.join(checkout, "cao.sh")), false);
    const appHelpers = await import(pathToFileURL(path.join(checkout, ".github/workflows/shared/github-apps.mjs")));
    assert.equal(appHelpers.APP_PROFILES.length, 2);
    assert.equal(typeof appHelpers.repositoryState, "function");
    assert.equal(typeof appHelpers.repositoryVariableValue, "function");
    assert.equal(appHelpers.createGitHubApp, undefined);
    assert.equal(existsSync(path.join(checkout, "cao.sh")), false);
    const { parseDocuments } = await import(pathToFileURL(path.join(checkout, "dashboard/site/src/validator-dashboard.js")));
    const errors = [];
    const documents = parseDocuments("example: true\n", errors);
    assert.equal(errors.length, 0);
    assert.equal(documents[0].toJS().example, true);
    run(process.execPath, [".github/workflows/shared/control.mjs", "validate-policy", ".github/workflows/cao.json"], checkout);
    assert.equal(existsSync(path.join(checkout, "dashboard/site/node_modules")), false);
    run("npm", ["ci", "--prefix", "dashboard/site", "--omit=dev", "--ignore-scripts"], checkout);
    const output = path.join(temporary, "dashboard-output");
    run("npm", ["run", "--prefix", "dashboard/site", "build", "--", output, path.join(checkout, ".github/workflows/cao.json")], checkout);
    for (const asset of ["index.html", "dashboard.json", "src/main.js", "src/data-worker.js", "llms.txt"]) {
      assert.ok(existsSync(path.join(output, asset)), `missing built asset: ${asset}`);
    }
    for (const [name, content] of Object.entries({
      "inventory-sources.json": "{}\n",
      "payload-hashes.json": "{}\n",
    })) writeFileSync(path.join(output, name), content);
    run(process.execPath, ["dashboard/site/scripts/llms.mjs", "generate", path.join(output, "llms.txt"), output], checkout);
    run(process.execPath, ["dashboard/site/scripts/llms.mjs", "validate", output], checkout);
    for (const dependency of ["vitest", "lighthouse", "jsdom", "eslint", "puppeteer-core"]) {
      assert.equal(existsSync(path.join(checkout, "dashboard/site/node_modules", dependency)), false);
    }
    const files = run("git", ["ls-files"], checkout).trim().split("\n");
    for (const file of files) {
      assert.doesNotMatch(file, /(?:^|\/)(?:tests?|fixtures|screenshots|skills|com\.github\.copilot)(?:\/|$)/);
    }
    run("git", ["diff", "--exit-code"], checkout);
  } finally {
    rmSync(temporary, { recursive: true, force: true });
  }
});
