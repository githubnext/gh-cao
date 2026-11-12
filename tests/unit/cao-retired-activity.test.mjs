import assert from "node:assert/strict";
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
