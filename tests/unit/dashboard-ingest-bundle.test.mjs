import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { copyFileSync, mkdirSync, mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";

test("native ingestion embeds the current production gh-aw adapter and canonical normalizer", () => {
  execFileSync(process.execPath, ["scripts/generate-dashboard-ingest.mjs", "--check"], {
    cwd: new URL("../..", import.meta.url),
    timeout: 30_000,
    stdio: "pipe",
  });
});

test("native ingestion accepts Windows schema line endings but rejects stale schema content", (t) => {
  const root = mkdtempSync(join(tmpdir(), "cao-ingest-check-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  for (const directory of ["scripts", "internal/dashboardingest"]) {
    mkdirSync(join(root, directory), { recursive: true });
  }
  for (const directory of ["node_modules", "dashboard"]) {
    symlinkSync(fileURLToPath(new URL(`../../${directory}`, import.meta.url)), join(root, directory), "junction");
  }
  for (const file of ["scripts/generate-dashboard-ingest.mjs", "internal/dashboardingest/normalize.generated.js.gz"]) {
    copyFileSync(new URL(`../../${file}`, import.meta.url), join(root, file));
  }
  const schema = readFileSync(new URL("../../internal/dashboardingest/schema.generated.go", import.meta.url), "utf8");
  const schemaPath = join(root, "internal/dashboardingest/schema.generated.go");
  writeFileSync(schemaPath, schema.replace(/\r?\n/g, "\r\n"));
  const check = () => execFileSync(process.execPath, ["scripts/generate-dashboard-ingest.mjs", "--check"], {
    cwd: root,
    timeout: 30_000,
    stdio: "pipe",
  });
  check();
  writeFileSync(schemaPath, schema.replace(/const SchemaVersion = \d+/, "const SchemaVersion = 0"));
  assert.throws(check, /Embedded canonical schema version is stale/);
});
