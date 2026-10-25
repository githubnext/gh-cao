import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { cpSync, mkdirSync, mkdtempSync, readFileSync, rmSync } from "node:fs";
import { homedir, tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { parse } from "yaml";
import { root } from "../helpers/control-precompute.mjs";

test("the pinned compiler integrates standalone admission without a campaign or worker declaration", () => {
  const directory = mkdtempSync(join(tmpdir(), "cao-standalone-compile-"));
  const workflows = join(directory, ".github", "workflows");
  const env = {
    PATH: process.env.PATH,
    HOME: directory,
    GH_CONFIG_DIR: join(directory, "gh-config"),
    XDG_DATA_HOME: process.env.XDG_DATA_HOME || join(homedir(), ".local", "share"),
    GH_NO_UPDATE_NOTIFIER: "1",
    GH_PROMPT_DISABLED: "1",
    GIT_CONFIG_NOSYSTEM: "1",
    GIT_CONFIG_GLOBAL: "/dev/null",
    GIT_TERMINAL_PROMPT: "0",
  };
  const run = (command, args) => {
    const result = spawnSync(command, args, { cwd: directory, env, encoding: "utf8", maxBuffer: 16 * 1024 * 1024 });
    assert.equal(result.status, 0, `${command} ${args.join(" ")}: ${result.error?.message ?? ""}\n${result.stderr}`);
    return result;
  };
  try {
    mkdirSync(workflows, { recursive: true });
    mkdirSync(join(directory, ".github", "aw"), { recursive: true });
    cpSync(join(root, ".github", "aw", "actions-lock.json"), join(directory, ".github", "aw", "actions-lock.json"));
    cpSync(join(root, ".github", "workflows", "shared"), join(workflows, "shared"), { recursive: true });
    cpSync(join(root, "tests", "fixtures", "workflows", "fleet-audit.md"), join(workflows, "fleet-audit.md"));
    cpSync(join(root, "tests", "fixtures", "workflows", "direct-audit.md"), join(workflows, "direct-audit.md"));
    run("git", ["init", "--quiet"]);
    run("git", ["add", "--all"]);
    run("git", ["-c", "user.name=CAO Test", "-c", "user.email=cao-test@localhost",
      "commit", "--quiet", "--no-gpg-sign", "-m", "Standalone compilation fixture"]);
    const expectedVersion = JSON.parse(readFileSync(join(root, ".github", "workflows", "cao.json"), "utf8"))["gh-aw-version"];
    assert.ok(run("gh", ["aw", "version"]).stdout.includes(expectedVersion));
    run("gh", ["aw", "compile", "--strict", "--no-check-update", "--schedule-seed", "githubnext/gh-cao"]);
    const compiled = parse(readFileSync(join(workflows, "fleet-audit.lock.yml"), "utf8"));
    assert.equal(compiled.env.CAO_ROLE, "standalone");
    assert.equal(compiled.env.CAO_WORKFLOW, "fleet-audit");
    assert.equal(compiled.env.CAO_CAMPAIGN, "__none__");
    const steps = compiled.jobs.pre_activation.steps;
    for (const id of ["cao_admission", "cao_precompute", "cao_precompute_validation"]) {
      const step = steps.find(step => step.id === id);
      assert.ok(step, `missing compiled ${id} step`);
      assert.equal(step.env.CAO_ROLE, "standalone");
      assert.equal(step.env.CAO_WORKFLOW, "fleet-audit");
      assert.equal(step.env.CAO_CAMPAIGN, "__none__");
    }
    assert.match(steps.find(step => step.id === "cao_precompute").env.CAO_SAFE_OUTPUT_REPOSITORY, /env.CAO_ROLE == 'standalone'/);
    assert.match(compiled.jobs.activation.if, /needs.pre_activation.outputs.cao_authorized/);
    const direct = parse(readFileSync(join(workflows, "direct-audit.lock.yml"), "utf8"));
    assert.equal(direct.env.CAO_ROLE, "direct");
    assert.equal(direct.env.CAO_WORKFLOW, "direct-audit");
    assert.equal(direct.env.CAO_CAMPAIGN, "__none__");
    assert.match(direct.jobs.activation.if, /needs.pre_activation.outputs.activated == 'true'/);
    assert.match(direct.jobs.activation.if, /needs.pre_activation.outputs.cao_authorized == 'true'/);
    for (const id of ["cao_admission", "cao_precompute"]) {
      const step = direct.jobs.pre_activation.steps.find(step => step.id === id);
      assert.equal(step.env.CAO_ROLE, "direct");
      assert.equal(step.env.CAO_WORKFLOW, "direct-audit");
      assert.match(step.env.CAO_TARGET_REPOSITORIES, /inputs.target_repos/);
    }
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});
