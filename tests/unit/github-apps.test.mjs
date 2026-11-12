import assert from "node:assert/strict";
import { existsSync, readFileSync, statSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";

import {
  APP_PROFILES,
  repositoryState,
  repositoryVariableValue,
} from "../../.github/workflows/shared/github-apps.mjs";

const root = process.cwd();
const helpers = join(root, ".github", "workflows", "shared", "github-apps.mjs");

test("GitHub App profiles preserve separate permission ceilings", () => {
  const read = APP_PROFILES.find((profile) => profile.role === "read");
  const write = APP_PROFILES.find((profile) => profile.role === "write");

  assert.ok(read);
  assert.ok(write);
  assert.equal(read.variable, "GH_AW_GITHUB_READ_APP_ID");
  assert.equal(read.secret, "GH_AW_GITHUB_READ_APP_PRIVATE_KEY");
  assert.equal(write.variable, "GH_AW_GITHUB_WRITE_APP_ID");
  assert.equal(write.secret, "GH_AW_GITHUB_WRITE_APP_PRIVATE_KEY");
  assert.ok(Object.values(read.permissions).every((level) => level === "read"));
  assert.equal(write.permissions.actions, "write");
  assert.equal(write.permissions.administration, "read");
  assert.equal(write.permissions.contents, "write");
  assert.equal(write.permissions.issues, "write");
  assert.equal(write.permissions.pull_requests, "write");
  for (const profile of APP_PROFILES) {
    assert.equal(profile.permissions.repository_creation, undefined);
    assert.equal(profile.permissions.installation_repositories, undefined);
    assert.equal(profile.permissions.campaigns, undefined);
  }
});

test("repository credential inspection reads paginated names and individual variables", () => {
  const calls = [];
  const state = repositoryState("octo/control", (args) => {
    calls.push(args);
    return args[1].includes("/variables?") ? "GH_AW_GITHUB_READ_APP_ID\n" : "GH_AW_GITHUB_READ_APP_PRIVATE_KEY\n";
  });
  assert.deepEqual(state.variables, new Set(["GH_AW_GITHUB_READ_APP_ID"]));
  assert.deepEqual(state.secrets, new Set(["GH_AW_GITHUB_READ_APP_PRIVATE_KEY"]));
  assert.equal(repositoryVariableValue("octo/control", "GH_AW_GITHUB_READ_APP_ID", (args) => {
    calls.push(args);
    return "Iv1.read";
  }), "Iv1.read");
  assert.deepEqual(calls, [
    ["api", "/repos/octo/control/actions/variables?per_page=100", "--paginate", "--jq", ".variables[].name"],
    ["api", "/repos/octo/control/actions/secrets?per_page=100", "--paginate", "--jq", ".secrets[].name"],
    ["api", "/repos/octo/control/actions/variables/GH_AW_GITHUB_READ_APP_ID", "--jq", ".value"],
  ]);
});

test("runtime retains credential inspection, not obsolete App administration", () => {
  const campaignJson = JSON.parse(readFileSync(join(root, "package.json"), "utf8"));
  const lock = JSON.parse(readFileSync(join(root, "package-lock.json"), "utf8"));
  assert.equal(Object.hasOwn(campaignJson.bin ?? {}, "cao-setup"), false);
  assert.equal(Object.hasOwn(campaignJson.scripts, "setup:github-apps"), false);
  assert.deepEqual(lock.packages[""].bin, campaignJson.bin);
  for (const name of ["setup-github-apps.mjs", "setup-hosted-cli-app.mjs"]) {
    assert.equal(existsSync(join(root, ".github", "workflows", "shared", name)), false);
  }
  assert.doesNotMatch(readFileSync(helpers, "utf8"), /^#!|createGitHubApp|createAppJwt|ensureInstallations|createServer|createSign/m);
  if (process.platform !== "win32") {
    assert.equal(statSync(helpers).mode & 0o111, 0);
  }
});
