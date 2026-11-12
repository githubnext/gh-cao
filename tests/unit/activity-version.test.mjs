import assert from "node:assert/strict";
import test from "node:test";
import {
  compareVersions,
  compilerVersionFromLock,
  normalizeVersion,
  revisionUpdateState,
  updateState,
} from "../../dashboard/lib/version.mjs";

test("gh-aw versions normalize optional prefixes and build metadata", () => {
  assert.equal(normalizeVersion("0.88.2"), "v0.88.2");
  assert.equal(normalizeVersion(" v0.88.2+build.7 "), "v0.88.2");
  assert.equal(normalizeVersion("v0.89.0-rc.1"), "v0.89.0-rc.1");
  assert.equal(normalizeVersion("v0.89"), null);
  assert.equal(normalizeVersion("release-0.89.0"), null);
});

test("gh-aw version comparison follows semantic-version precedence", () => {
  assert.equal(compareVersions("v0.88.2", "0.88.2"), 0);
  assert.ok(compareVersions("v0.89.0", "v0.88.10") > 0);
  assert.ok(compareVersions("v0.89.0-rc.2", "v0.89.0-rc.10") < 0);
  assert.ok(compareVersions("v0.89.0-rc.1", "v0.89.0") < 0);
  assert.equal(compareVersions("not-a-version", "v0.89.0"), null);
});

test("gh-aw update state treats normalized current and newer versions as up to date", () => {
  assert.equal(updateState("0.88.2", "v0.88.2"), "up-to-date");
  assert.equal(updateState("v0.89.0-rc.1", "v0.89.0"), "update-available");
  assert.equal(updateState("v0.90.0", "v0.89.0"), "up-to-date");
  assert.equal(updateState(null, "v0.89.0"), "unknown");
});

test("compiler metadata parsing and revision update state fail closed", () => {
  assert.equal(
    compilerVersionFromLock('# gh-aw-metadata: {"compiler_version":"0.89.15"}\n'),
    "v0.89.15",
  );
  assert.equal(compilerVersionFromLock("# gh-aw-metadata: not-json\n"), null);
  assert.equal(revisionUpdateState("A".repeat(40), "a".repeat(40)), "up-to-date");
  assert.equal(revisionUpdateState("a".repeat(40), "b".repeat(40)), "update-available");
  assert.equal(revisionUpdateState("", "b".repeat(40)), "unknown");
});
