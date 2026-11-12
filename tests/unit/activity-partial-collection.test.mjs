import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { promisify } from "node:util";
import { discoverWorkflows } from "../../activity/cli/dashboard.mjs";

const execute = promisify(execFile);
const repositories = ["acme/one", "acme/two", "acme/three"];

test("offline workflow discovery makes no API requests and never claims live registry state", async (t) => {
  const root = await mkdtemp(path.join(os.tmpdir(), "cao-offline-inventory-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  t.mock.method(globalThis, "fetch", () => { throw new Error("Offline discovery must not request GitHub"); });
  await mkdir(path.join(root, ".github", "workflows"), { recursive: true });
  await writeFile(path.join(root, ".github", "workflows", "example.md"), "---\nname: Example\non: workflow_dispatch\n---\nInspect the repository.\n");
  const settings = path.join(root, "settings.json");
  await writeFile(settings, JSON.stringify({ allowed_repositories: repositories }));
  const outputPath = path.join(root, "sources.json");
  await discoverWorkflows({
    root, controlSettingsPath: settings, inventoryPath: path.join(root, "inventory.json"),
    outputPath, repository: "acme/control", offline: true,
  });
  const sources = JSON.parse(await readFile(outputPath, "utf8"));
  assert.equal(sources.repositories.rows.length, 4);
  assert.ok(sources.workflows.rows.length > 0);
  assert.ok(sources.workflows.rows.every((row) => row["workflow-active"] === "unknown"));
  assert.ok(Object.values(sources).every((source) => source.metadata.completeness === "partial"));
  await execute(process.execPath, [
    "activity/cao.mjs", "discover-workflows", "--offline", "--root", root,
    "--control-settings", settings, "--inventory", path.join(root, "cli-inventory.json"),
    "--output", path.join(root, "cli-sources.json"), "--repo", "acme/control",
  ], { env: { ...process.env, GH_TOKEN: "", GITHUB_TOKEN: "" } });
});
