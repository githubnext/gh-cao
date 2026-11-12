import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { once } from "node:events";
import { cp, mkdir, mkdtemp, realpath, rm, symlink, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, relative, sep } from "node:path";
import test from "node:test";
import { fileURLToPath, pathToFileURL } from "node:url";
import { createInterface } from "node:readline";
import { executeDashboardQueryRequest, readDashboardDataSpecification } from "../../com.github.copilot/extensions/cao-dashboard/dashboard-agent-tools.mjs";
import { prepareCanvasPostgres, writeCanvasSnapshot } from "../helpers/canvas-snapshot.mjs";

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
    assert.equal(await resources.resolveBundledResource("canvasServer"), join(plugin, "dashboard/canvas-server.mjs"));
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

test("a relocated plugin builds its own Go host and child lifetime follows its owner", { timeout: 180_000 }, async (t) => {
  const root = await mkdtemp(join(tmpdir(), "cao-plugin-go-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const plugin = await relocatedPlugin(root);
  for (const path of ["server/go.mod", "server/go.sum", "server/cmd/cao-dashboard", "server/internal", "server/postgres", "internal/dashboardquery"]) {
    await mkdir(join(plugin, path, ".."), { recursive: true });
    await cp(join(repositoryRoot, path), join(plugin, path), { recursive: true });
  }
  const resources = await import(pathToFileURL(join(plugin, "com.github.copilot/extensions/cao-dashboard/bundled-resources.mjs")).href);
  for (const name of ["canvasGoHost", "serverModule", "serverCommand", "nativeQueryModule"]) {
    assert.ok((await resources.resolveBundledResource(name)).startsWith(plugin));
  }
  const directory = join(root, "preview");
  await writeCanvasSnapshot(directory);
  const postgres = await prepareCanvasPostgres();
  t.after(() => postgres.close());
  const site = join(directory, "site");
  await mkdir(site);
  await writeFile(join(site, "index.html"), "<html><head></head><body>relocated</body></html>");
  const config = join(directory, "canvas.json");
  await writeFile(config, JSON.stringify({
    prefix: `/${"a".repeat(48)}`, adapterUrl: `http://127.0.0.1:1/${"a".repeat(48)}`,
  }));
  const moduleUrl = pathToFileURL(join(plugin, "dashboard/canvas-go-host.mjs")).href;
  const environment = { ...process.env };
  delete environment.CAO_DASHBOARD_EXECUTABLE;
  const owner = spawn(process.execPath, ["--input-type=module", "-e", `
    import { startCanvasGoHost } from ${JSON.stringify(moduleUrl)};
    const host = await startCanvasGoHost(${JSON.stringify({ directory, site, source: directory, postgresURL: postgres.postgresURL, namespace: postgres.namespace, config, host: "127.0.0.1", port: 0 })});
    console.log(host.url);
  `], { env: environment, stdio: ["ignore", "pipe", "pipe"] });
  let stderr = "";
  owner.stderr.on("data", (chunk) => { stderr += chunk; });
  const lines = createInterface({ input: owner.stdout });
  t.after(() => { owner.kill("SIGKILL"); lines.close(); });
  const [url] = await Promise.race([
    once(lines, "line"),
    once(owner, "exit").then(([code]) => { throw new Error(`relocated host failed (${code}): ${stderr}`); }),
  ]);
  assert.equal((await fetch(url)).status, 200);
  owner.kill("SIGKILL");
  await once(owner, "exit");
  let stopped = false;
  for (let attempt = 0; attempt < 50; attempt++) {
    try { await fetch(url, { signal: AbortSignal.timeout(1000) }); }
    catch { stopped = true; break; }
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  assert.ok(stopped, "Go host remained alive after losing its owning adapter");
});
