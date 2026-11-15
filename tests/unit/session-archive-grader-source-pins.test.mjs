import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { verifyPinnedSource } from "../../scripts/replay-session-archive-graders.mjs";

const root = fileURLToPath(new URL("../../", import.meta.url));
const pins = JSON.parse(readFileSync(new URL("../../server/internal/sessionarchive/testdata/grader-source-pins.json", import.meta.url), "utf8"));

function source(content) {
  const bytes = Buffer.from(content);
  const blob = createHash("sha1").update(`blob ${bytes.length}\0`).update(bytes).digest("hex");
  const sha256 = createHash("sha256").update(bytes).digest("hex");
  return {
    metadata: { encoding: "base64", content: bytes.toString("base64"), sha: blob },
    expected: { blob, sha256 },
    bytes,
  };
}

test("source verification accepts matching independent blob and sha256 pins", () => {
  const { metadata, expected, bytes } = source("module.exports = {};\n");
  assert.deepEqual(verifyPinnedSource("fixture.cjs", metadata, expected), { content: bytes, ...expected });
});

test("self-consistent changed API content cannot replace any committed grader source", () => {
  const { metadata } = source("module.exports = { changed: true };\n");
  for (const pin of pins.sources) {
    assert.throws(
      () => verifyPinnedSource(pin.name, metadata, pin),
      { message: new RegExp(`^pinned upstream blob integrity: ${pin.name.replaceAll(".", "\\.")}`) },
    );
  }
});

test("sha256 and API metadata are independently checked against fixed pins", () => {
  const { metadata, expected } = source("module.exports = {};\n");
  assert.throws(
    () => verifyPinnedSource("fixture.cjs", metadata, { ...expected, sha256: "0".repeat(64) }),
    { message: /^pinned upstream sha256 integrity: fixture\.cjs/ },
  );
  assert.throws(
    () => verifyPinnedSource("fixture.cjs", { ...metadata, sha: "0".repeat(40) }, expected),
    { message: /^upstream metadata blob integrity: fixture\.cjs/ },
  );
  assert.throws(
    () => verifyPinnedSource("fixture.cjs", metadata, undefined),
    { message: "missing upstream source pin: fixture.cjs" },
  );
});

test("replay rejects changed upstream bytes before Go probes or grader execution", () => {
  const scratchRoot = join(root, ".tmp");
  mkdirSync(scratchRoot, { recursive: true });
  const scratch = mkdtempSync(join(scratchRoot, "grader-source-pins-test-"));
  try {
    const bin = join(scratch, "bin");
    const output = join(scratch, "report");
    const graderExecuted = join(scratch, "grader-executed");
    const goExecuted = join(scratch, "go-executed");
    mkdirSync(bin);
    const { metadata } = source(`require("node:fs").writeFileSync(${JSON.stringify(graderExecuted)}, "executed");\n`);
    writeFileSync(join(bin, "gh"), `#!/bin/sh\nprintf '%s\\n' '${JSON.stringify(metadata)}'\n`, { mode: 0o700 });
    writeFileSync(join(bin, "go"), `#!/bin/sh\nprintf executed > '${goExecuted}'\nexit 1\n`, { mode: 0o700 });
    const result = spawnSync(process.execPath, [join(root, "scripts/replay-session-archive-graders.mjs"), output], {
      cwd: root,
      encoding: "utf8",
      timeout: 30000,
      env: { ...process.env, PATH: `${bin}:${process.env.PATH}` },
    });
    assert.equal(result.error, undefined);
    assert.notEqual(result.status, 0);
    assert.match(result.stderr, /pinned upstream blob integrity: trace_graders\.cjs/);
    assert.equal(existsSync(goExecuted), false);
    assert.equal(existsSync(graderExecuted), false);
    assert.equal(existsSync(join(output, "report.json")), false);
  } finally {
    rmSync(scratch, { recursive: true, force: true });
  }
});
