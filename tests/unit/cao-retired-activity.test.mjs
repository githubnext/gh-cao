import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";
import { parse } from "yaml";
import { runCli } from "../../activity/cao.mjs";

test("the root package no longer installs the legacy Activity publication chain", async () => {
  const manifest = parse(await readFile(new URL("../../aw.yml", import.meta.url), "utf8"));
  assert.deepEqual(manifest.includes, [".github/workflows/cao-validate.yml"]);
  for (const filename of [
    ".github/workflows/cao-activity.yml",
    ".github/workflows/cao-dashboard.yml",
    ".github/workflows/cao-value-local.yml",
    ".github/workflows/shared/activity-cache.md",
    "activity/aw.yml",
    "dashboard/aw.yml",
    "dashboard/bootstrap-activity.mjs",
    "activity/local-values.mjs",
  ]) {
    await assert.rejects(readFile(new URL(`../../${filename}`, import.meta.url)), { code: "ENOENT" });
  }
});

test("legacy pipeline commands are absent rather than redirected through compatibility handlers", async () => {
  const input = { [Symbol.asyncIterator]() { assert.fail("unknown commands must not consume input"); } };
  for (const command of ["activity-stats", "validate-activity-data"]) {
    await assert.rejects(
      runCli([command, "--database", "must-not-open.sqlite", "--stdin"], input),
      { message: `Unknown command: ${command}` },
    );
  }
});
