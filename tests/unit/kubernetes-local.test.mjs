import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const script = fileURLToPath(new URL("../../server/kubernetes/local.sh", import.meta.url));

function fixture(t) {
  const directory = mkdtempSync(join(tmpdir(), "cao-kubernetes-"));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const log = join(directory, "calls");
  writeFileSync(log, "");
  const kubectl = join(directory, "kubectl");
  writeFileSync(kubectl, `#!/bin/sh
printf '%s\\n' "$*" >> "$CALL_LOG"
case "$*" in
  *jsonpath*) printf 'another-application' ;;
esac
`, { mode: 0o700 });
  const oauth = join(directory, "oauth.env");
  writeFileSync(oauth, [
    "CAO_GITHUB_CLIENT_ID=test-only-id",
    "CAO_GITHUB_CLIENT_SECRET=test-only-secret",
    "CAO_GITHUB_ALLOWED_USERS=test-user",
    "CAO_GITHUB_ADMIN_USERS=test-user",
    "",
  ].join("\n"), { mode: 0o600 });
  return {
    oauth,
    log,
    run: () => spawnSync("bash", [script, "serve", oauth], {
      encoding: "utf8",
      env: { ...process.env, PATH: `${directory}:${process.env.PATH}`, CALL_LOG: log },
    }),
  };
}

test("local deployment refuses foreign namespaces without making cluster writes", (t) => {
  const f = fixture(t);
  const result = f.run();
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /Refusing foreign namespace: cao-local/);
  const calls = readFileSync(f.log, "utf8").trim().split("\n");
  assert.equal(calls.length, 2);
  for (const call of calls) {
    assert.match(call, /^--context docker-desktop get namespace cao-local/);
    assert.doesNotMatch(call, /create|apply|delete|patch/);
  }
});

test("OAuth input cannot inject local identity, database authority or shell code", (t) => {
  const f = fixture(t);
  const original = readFileSync(f.oauth, "utf8");
  for (const extra of [
    "CAO_GITHUB_LOCAL_USER=test-user",
    "CAO_DATABASE_ADMIN_URL=postgresql://test-only",
    "CAO_GITHUB_CLIENT_SECRET=duplicate",
    "CAO_GITHUB_CLIENT_ID=REPLACE_WITH_YOUR_OAUTH_CLIENT_ID",
    "UNSUPPORTED=$(touch should-not-exist)",
  ]) {
    writeFileSync(f.oauth, `${original}${extra}\n`);
    const result = f.run();
    assert.notEqual(result.status, 0);
    assert.match(result.stderr, /OAuth file must contain exactly/);
    assert.equal(readFileSync(f.log, "utf8"), "");
  }
});

test("operator shell passes syntax and ShellCheck when available", () => {
  execFileSync("bash", ["-n", script]);
  const probe = spawnSync("shellcheck", ["--version"]);
  if (!probe.error) execFileSync("shellcheck", [script]);
});
