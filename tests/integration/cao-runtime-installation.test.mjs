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
    run("git", ["add", ".github", "activity", "dashboard"], installed);
    run("git", ["diff", "--cached", "--check"], installed);
    assert.equal(run("git", ["ls-files", "--others", "--exclude-standard"], installed), "");
    run("git", [
      "-c", "user.name=CAO runtime test", "-c", "user.email=cao-test@example.invalid",
      "-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null",
      "commit", "--quiet", "-m", "Install CAO runtime",
    ], installed);
    run("git", ["clone", "--quiet", "--no-local", installed, checkout], temporary);
    for (const bundle of ["activity", "dashboard"]) {
      run(process.execPath, [".github/workflows/shared/materialize-cao.mjs", "verify", bundle], checkout);
    }
    const help = run(process.execPath, ["activity/cao.mjs", "--help"], checkout);
    assert.doesNotMatch(help, /cao add --repository/);
    assert.equal(existsSync(path.join(checkout, "activity/cli/repositories.mjs")), false);
    for (const arguments_ of [
      ...["init", "setup", "setup-auth", "add", "mode", "enable", "disable", "update", "upgrade-gh-aw"]
        .flatMap(command => [[command, "must-not-exist.sqlite"], [command, "--help"]]),
      ["add", "--repository", "owner/repo"],
      ["add", "--repository=owner/repo"],
      ["add", "catalog/repo/demo", "--repository", "owner/repo"],
      ...["github-app", "enterprise-app", "token"].map(profile => ["setup-auth", profile, "--dry-run"]),
    ]) {
      const result = spawnSync(process.execPath, ["activity/cao.mjs", ...arguments_], {
        cwd: checkout, encoding: "utf8", timeout: 30_000, env: { ...process.env, PATH: "" },
      });
      assert.equal(result.status, 1, result.error?.message);
      assert.equal(result.stdout, "");
      assert.ok(result.stderr.startsWith(`Error: Node cao ${arguments_[0]} has been retired; use gh cao `));
      assert.equal(existsSync(path.join(checkout, "must-not-exist.sqlite")), false);
    }
    run("git", ["diff", "--exit-code"], checkout);
    assert.match(help, /cao repair-database\b/);
    assert.doesNotMatch(help, /cao doctor\b/);
    assert.equal(existsSync(path.join(checkout, "activity/commands/repair-database.mjs")), true);
    assert.equal(existsSync(path.join(checkout, "activity/commands/doctor.mjs")), false);
    const database = path.join(temporary, "snapshot.sqlite");
    const shards = path.join(temporary, "shards");
    mkdirSync(shards);
    run(process.execPath, ["activity/cao.mjs", "ingest-jsonl", "--input-dir", shards, "--database", database], checkout);
    const repair = JSON.parse(run(process.execPath, [
      "activity/cao.mjs", "repair-database", "--database", database,
      "--ttl-days", "all", "--run-ttl-days", "all",
    ], checkout));
    assert.equal(repair.command, "repair-database");
    assert.equal(repair.healthy, true);
    const retired = spawnSync(process.execPath, ["activity/cao.mjs", "doctor", database], { cwd: checkout, encoding: "utf8" });
    assert.equal(retired.status, 1);
    assert.match(retired.stderr, /Unknown command: doctor; use repair-database/);
    const retiredAuth = spawnSync(process.execPath, [
      "activity/cao.mjs", "setup-auth", "hosted-cli", "--repo", "example/control",
      "--private-key-out", path.join(checkout, "must-not-exist.pem"),
    ], { cwd: checkout, encoding: "utf8", env: { ...process.env, PATH: "" } });
    assert.equal(retiredAuth.status, 1, retiredAuth.stderr);
    assert.match(retiredAuth.stderr, /Node cao setup-auth has been retired; use gh cao setup auth/);
    assert.equal(existsSync(path.join(checkout, "must-not-exist.pem")), false);
    assert.doesNotMatch(help, /cao setup-auth hosted-cli/);
    assert.equal(existsSync(path.join(checkout, ".github/workflows/shared/setup-hosted-cli-app.mjs")), false);
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
      "agent-summary.json": '{"schemaVersion":1}\n',
      "inventory-sources.json": "{}\n",
      "payload-hashes.json": "{}\n",
      "gh-aw-logs.sqlite": "sqlite fixture",
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
