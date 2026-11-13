import assert from "node:assert/strict";
import { cp, mkdir, mkdtemp, realpath, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, relative, sep } from "node:path";
import test from "node:test";
import { fileURLToPath, pathToFileURL } from "node:url";
import { executeDashboardQueryRequest, readDashboardDataSpecification } from "../../com.github.copilot/extensions/cao-dashboard/dashboard-agent-tools.mjs";

const repositoryRoot = fileURLToPath(new URL("../../", import.meta.url));

async function relocatedPlugin(root) {
  const plugin = join(root, "plugin");
  await mkdir(join(plugin, "specs"), { recursive: true });
  await cp(join(repositoryRoot, "plugin.json"), join(plugin, "plugin.json"));
  await cp(join(repositoryRoot, "specs/dashboard-data.md"), join(plugin, "specs/dashboard-data.md"));
  await cp(join(repositoryRoot, "com.github.copilot"), join(plugin, "com.github.copilot"), { recursive: true });
  const dashboard = join(repositoryRoot, "dashboard");
  await cp(dashboard, join(plugin, "dashboard"), {
    recursive: true,
    filter: (path) => !relative(dashboard, path).split(sep)
      .some((part) => ["node_modules", "dist", ".tmp", "test", "test-results"].includes(part)),
  });
  return realpath(plugin);
}

test("agent tools ignore executable modules and specifications from the active checkout", async () => {
  const workspace = await mkdtemp(join(tmpdir(), "cao-hostile-checkout-"));
  try {
    for (const layout of ["dashboard/site/src/data/queries", ".github/aw/dashboard/site/src/data/queries"]) {
      await mkdir(join(workspace, layout), { recursive: true });
      await writeFile(join(workspace, layout, "declarative.js"), 'throw new Error("Workspace query code executed.");');
    }
    await mkdir(join(workspace, "specs"));
    await writeFile(join(workspace, "specs/dashboard-data.md"), "Workspace specification must not be used.");
    const output = JSON.parse(await executeDashboardQueryRequest({
      workingDirectory: workspace,
      queries: [{ name: "runs", from: "input", limit: 1 }],
      sources: { input: [{ conclusion: "success" }] },
    }));
    assert.deepEqual(output.runs.rows, [{ conclusion: "success" }]);
    const specification = JSON.parse(await readDashboardDataSpecification({ workingDirectory: workspace, endLine: 3 }));
    assert.doesNotMatch(specification.content, /Workspace specification/);
    assert.match(specification.path, /specs\/dashboard-data\.md$/);
  } finally { await rm(workspace, { recursive: true, force: true }); }
});
test("a relocated complete plugin resolves its own executable and specification resources", async () => {
  const root = await mkdtemp(join(tmpdir(), "cao-plugin-package-"));
  try {
    const plugin = await relocatedPlugin(root);
    const tools = await import(pathToFileURL(join(plugin, "com.github.copilot/extensions/cao-dashboard/dashboard-agent-tools.mjs")).href);
    const output = JSON.parse(await tools.executeDashboardQueryRequest({
      workingDirectory: join(root, "target"),
      queries: [{ name: "result", from: "input" }],
      sources: { input: [{ repository: "acme/control" }] },
    }));
    assert.deepEqual(output.result.rows, [{ repository: "acme/control" }]);
    const specification = JSON.parse(await tools.readDashboardDataSpecification({ startLine: 1, endLine: 3 }));
    assert.ok(specification.path.startsWith(plugin));
    const resources = await import(pathToFileURL(join(plugin, "com.github.copilot/extensions/cao-dashboard/bundled-resources.mjs")).href);
    await assert.rejects(resources.resolveBundledResource("canvasServer"), /Unknown bundled CAO dashboard resource/);
  } finally { await rm(root, { recursive: true, force: true }); }
});

test("bundled-resource symlinks cannot escape the trusted package", async () => {
  const root = await mkdtemp(join(tmpdir(), "cao-plugin-containment-"));
  try {
    const plugin = await relocatedPlugin(root);
    const target = join(plugin, "dashboard/site/src/data/queries/declarative.js");
    const outside = join(root, "outside.js");
    await writeFile(outside, 'throw new Error("Outside code executed.");');
    await rm(target);
    await symlink(outside, target);
    const resources = await import(pathToFileURL(join(plugin, "com.github.copilot/extensions/cao-dashboard/bundled-resources.mjs")).href);
    await assert.rejects(resources.resolveBundledResource("queryEngine"), /outside the plugin root/);
  } finally { await rm(root, { recursive: true, force: true }); }
});
