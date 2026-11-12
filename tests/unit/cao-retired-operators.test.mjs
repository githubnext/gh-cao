import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdir, mkdtemp, readFile, readdir, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import * as cao from "../../activity/cao.mjs";
import { commandHandlers } from "../../activity/commands/index.mjs";
import { UsageError } from "../../activity/cli/options.mjs";

const executable = fileURLToPath(new URL("../../activity/cao.mjs", import.meta.url));
const replacements = new Map([
  ["init", "gh cao init"],
  ["setup", "gh cao setup"],
  ["setup-auth", "gh cao setup auth"],
  ["add", "gh cao add"],
  ["mode", "gh cao campaigns mode"],
  ["enable", "gh cao campaigns enable"],
  ["disable", "gh cao campaigns disable"],
  ["update", "gh cao update"],
  ["upgrade-gh-aw", "gh cao upgrade-gh-aw"],
]);

test("retired operators reject before option parsing, stdin consumption, or legacy ingestion", async () => {
  const input = { [Symbol.asyncIterator]() { assert.fail("retired operators must not read input"); } };
  const help = await cao.runCli(["--help"]);
  for (const [command, replacement] of replacements) {
    assert.equal(commandHandlers.has(command), false);
    assert.doesNotMatch(help, new RegExp(`^  cao ${command}(?:\\s|$)`, "m"));
    for (const args of [[], ["--help"], ["must-not-be-a-database.sqlite"], ["--unknown", "--stdin"], ["--", "campaign"]]) {
      await assert.rejects(cao.runCli([command, ...args], input), error => {
        assert.ok(error instanceof UsageError);
        assert.ok(error.message.startsWith(`Node cao ${command} has been retired; use ${replacement}`));
        return true;
      });
    }
  }
});

test("retired executable operators leave policy, ownership, credentials and database bytes untouched", async (t) => {
  const root = await mkdtemp(path.join(tmpdir(), "cao-retired-operators-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const originals = new Map([
    [".github/workflows/cao.json", '{"version":1,"control-plane":{"scope":{"allowed-repositories":["acme/control"]}}}\n'],
    [".github/aw/packages/demo.json", '{"source":"catalog/repo/demo","files":["demo/cao.json"]}\n'],
    ["snapshot.sqlite", "not a database; must not be opened"],
    ["private-key.pem", "existing key placeholder; must not be replaced"],
  ]);
  for (const [name, value] of originals) {
    await mkdir(path.dirname(path.join(root, name)), { recursive: true });
    await writeFile(path.join(root, name), value);
  }
  const before = (await readdir(root, { recursive: true })).sort();
  const cases = [...replacements.keys()].flatMap(command => [
    [command], [command, "snapshot.sqlite"], [command, "--help"],
    [command, "--database", "snapshot.sqlite", "--policy", ".github/workflows/cao.json"],
  ]);
  cases.push(
    ...["github-app", "enterprise-app", "token", "hosted-cli"].map(profile =>
      ["setup-auth", profile, "--repo", "acme/control", "--private-key-out", "private-key.pem"]),
    ["add", "--repository", "acme/repo"],
    ["add", "--repository=acme/repo"],
    ["add", "catalog/repo/demo", "--", "--repository", "acme/repo"],
    ["mode", "live", "demo"],
    ["setup", "--repository", "acme/*"],
    ["update", "main", "--force"],
  );
  for (const args of cases) {
    const result = spawnSync(process.execPath, [executable, ...args], {
      cwd: root, encoding: "utf8", timeout: 30_000,
      env: { ...process.env, PATH: "", CAO_DEBUG: "", NODE_OPTIONS: "" },
      input: "must not consume input",
    });
    assert.equal(result.status, 1, `${args}: ${result.error ?? result.stderr}`);
    assert.equal(result.stdout, "");
    assert.ok(result.stderr.startsWith(`Error: Node cao ${args[0]} has been retired; use ${replacements.get(args[0])}`));
    assert.doesNotMatch(result.stderr, /\n\s+at /);
    if (args[0] === "add" && (args.includes("--repository=acme/repo") || args.includes("--repository"))) {
      assert.match(result.stderr, /gh cao repos add OWNER\/REPO/);
    }
    for (const [name, value] of originals) {
      assert.equal(await readFile(path.join(root, name), "utf8"), value, `${args}: changed ${name}`);
    }
    assert.deepEqual((await readdir(root, { recursive: true })).sort(), before, `${args}: wrote files`);
  }
});

test("Node has no administration API or obsolete implementation modules", async () => {
  for (const name of [
    "setupCaoControlPlane", "formatSetupAuthenticationSummary", "initializeCaoPolicy",
    "ensureGhAwMinimumVersion", "upgradeGhAw", "fineGrainedTokenSetups", "setupCaoAuthentication",
    "addCaoCampaign", "addCaoRepository", "updateCaoCampaigns", "setCaoCampaignMode", "setCaoCampaignWorkflowsEnabled",
  ]) assert.equal(name in cao, false, name);
  for (const relative of [
    ...[...replacements.keys()].map(name => `commands/${name}.mjs`),
    ...["authentication", "campaigns", "gh-aw", "packages", "policy"].map(name => `cli/${name}.mjs`),
    "setup.mjs", "setup-prompt.mjs", "upgrade-gh-aw.mjs",
  ]) await assert.rejects(readFile(new URL(`../../activity/${relative}`, import.meta.url)), { code: "ENOENT" });
});
