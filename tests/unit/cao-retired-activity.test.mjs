import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { existsSync, readFileSync } from "node:fs";
import test from "node:test";

test("the obsolete Activity runtime is absent from source and runtime packaging", () => {
  assert.equal(existsSync("activity"), false);
  const root = JSON.parse(readFileSync("package.json", "utf8"));
  const runtime = JSON.parse(readFileSync("runtime/package.json", "utf8"));
  assert.equal(root.bin, undefined);
  assert.ok(runtime.files.every(file => !file.startsWith("activity/")));
  for (const command of Object.values(root.scripts)) {
    assert.doesNotMatch(command, /activity\/cao\.mjs/);
  }
});

test("native runtime materialization does not install Activity", () => {
  const materializer = readFileSync("internal/cli/native_management_runtime.go", "utf8");
  assert.doesNotMatch(materializer, /activity\/|cao\.mjs/);
});

test("guidance does not direct users to the removed cao-activity-index artifact", () => {
  const historical = new Set([
    "docs/activity-cache-compression-analysis.md",
    "specs/data-acquisition-audit-history.md",
    "tests/unit/cao-ci-tiers.test.mjs",
    "tests/unit/cao-retired-activity.test.mjs",
    "tests/unit/dashboard-query-cost.test.mjs",
  ]);
  let output = "";
  try {
    output = execFileSync("git", ["grep", "-l", "cao-activity-index"], { encoding: "utf8" });
  } catch (error) {
    if (error.status !== 1) throw error;
  }
  const files = output.split("\n").filter(file => file && !historical.has(file) && !file.endsWith(".lock.yml"));
  assert.deepEqual(files, []);
});
