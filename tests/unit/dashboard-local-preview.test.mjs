import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { once } from "node:events";
import { chmod, mkdir, mkdtemp, readFile, readdir, realpath, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createInterface } from "node:readline";
import { after, before, describe, test } from "node:test";
import { fileURLToPath } from "node:url";
import { startLocalDashboardPreview } from "../../com.github.copilot/extensions/cao-dashboard/local-preview.mjs";
import { prepareCanvasPostgres, writeCanvasSnapshot } from "../helpers/canvas-snapshot.mjs";
import { normalize } from "../../dashboard/site/src/data/normalize/index.js";
import { queryDashboardSourceObservations } from "../../dashboard/site/src/data/queries/ingestion.js";

const extensionUrl = new URL("../../com.github.copilot/extensions/cao-dashboard/extension.mjs", import.meta.url);

async function fixture(t, postgres) {
  postgres ??= await prepareCanvasPostgres();
  const previousURL = process.env.CAO_POSTGRES_URL;
  const previousNamespace = process.env.CAO_DATABASE_NAMESPACE;
  process.env.CAO_POSTGRES_URL = postgres.postgresURL;
  process.env.CAO_DATABASE_NAMESPACE = postgres.namespace;
  t.after(async () => {
    if (previousURL === undefined) delete process.env.CAO_POSTGRES_URL;
    else process.env.CAO_POSTGRES_URL = previousURL;
    if (previousNamespace === undefined) delete process.env.CAO_DATABASE_NAMESPACE;
    else process.env.CAO_DATABASE_NAMESPACE = previousNamespace;
    await postgres.close();
  });
  const workspace = await realpath(await mkdtemp(join(tmpdir(), "cao-canvas-test-")));
  const ghExecutable = join(workspace, "gh");
  const artifact = join(workspace, ".cao");
  await writeCanvasSnapshot(artifact, normalize(queryDashboardSourceObservations({
    repositories: {
      metadata: { "as-of": "2026-10-10T00:00:00Z" },
      rows: [{ organization: "acme", repository: "control" }],
    },
  }).observations));
  await writeFile(ghExecutable, `#!${process.execPath}
if (process.cwd() !== ${JSON.stringify(workspace)}) process.exit(4);
const args = process.argv.slice(2);
if (args[0] === "api" && args[1] === "repos/github/gh-aw/releases/latest") {
  process.stdout.write("v0.92.0\\n");
} else {
  process.exit(3);
}
`);
  // Node treats an extensionless executable as CommonJS unless the fixture declares ESM.
  await writeFile(join(workspace, "package.json"), '{"type":"module"}');
  await chmod(ghExecutable, 0o755);
  return { workspace, ghExecutable };
}

async function previewDirectories(workspace) {
  return (await readdir(workspace)).filter((name) => name.startsWith(".cao-dashboard-preview-"));
}

test("plugin canvas stages its site inside a foreign workspace and cleans up", {
  skip: process.platform === "win32",
}, async (t) => {
  const { workspace, ghExecutable } = await fixture(t);
  let preview;
  try {
    preview = await startLocalDashboardPreview({
      workingDirectory: workspace,
      repository: "acme/control",
      executeCliAction: async () => {},
      approveCliAction: async () => false,
      ghExecutable,
    });
    assert.equal(new URL(preview.url).hostname, "127.0.0.1");
    const response = await fetch(preview.url);
    assert.equal(response.status, 200);
    assert.equal(new URL(response.url).searchParams.get("local-preview"), "canvas");
    const html = await response.text();
    assert.match(html, /src\/main\.js/);
    assert.match(html, /name="dashboard-data-backend" content="server-http"/);
    assert.match(html, /name="dashboard-api-base"/);
    const base = new URL(`${preview.url}/`);
    assert.equal((await fetch(new URL("src/main.js", base))).status, 200);
    const document = await (await fetch(new URL("dashboard.json", base))).json();
    assert.equal(document.dashboard.repository, "acme/control");
    assert.ok(document.dashboard.pages.length > 0);
    assert.equal((await fetch(new URL("sources/repositories.json", base))).status, 404);
    const post = (pathname, payload) => fetch(new URL(pathname, base), {
      method: "POST",
      headers: { Origin: base.origin, "Content-Type": "application/json" },
      ...(payload ? { body: JSON.stringify(payload) } : {}),
    });
    const refresh = await post("api/v1/refresh");
    assert.equal(refresh.status, 200);
    const status = await refresh.json();
    const query = await post("api/v1/query", {
      sourceNames: ["repository-count"],
      queries: [{
        name: "repository-count", from: "repositories",
        aggregate: { values: [{ field: "repository", as: "count", reducer: "count" }] },
      }],
    });
    assert.equal(query.status, 200);
    const result = await query.json();
    assert.equal(result.revision, status.revision);
    assert.deepEqual(result.sources["repository-count"].rows, [{ count: 1 }]);
    const diagnostics = await (await fetch(new URL("api/v1/diagnostics", base))).json();
    assert.equal(diagnostics.counts.repositories, 1);
    assert.deepEqual(diagnostics.relationshipErrors, []);
    assert.deepEqual(diagnostics.duplicateRecordIds.repositories, []);
    assert.equal((await post("api/v1/query", { sourceNames: "repositories" })).status, 400);
    assert.equal((await fetch(new URL("api/v1/query", base), {
      method: "POST", headers: { "Content-Type": "application/json", Origin: "https://untrusted.example" },
      body: JSON.stringify({ sourceNames: ["repositories"] }),
    })).status, 403);
    assert.equal((await fetch(new URL("/api/v1/diagnostics", base))).status, 404);
    const events = await fetch(new URL("api/v1/events", base));
    assert.equal(events.headers.get("Content-Type"), "text/event-stream");
    const reader = events.body.getReader();
    assert.match(new TextDecoder().decode((await reader.read()).value), /"revision":/);
    await reader.cancel();
    assert.equal((await previewDirectories(workspace)).length, 2);
    for (const directory of await previewDirectories(workspace)) {
      await assert.rejects(readdir(join(workspace, directory, "site", ".tmp")), { code: "ENOENT" });
    }
    await preview.close();
    await preview.close();
    assert.deepEqual(await previewDirectories(workspace), []);
    await assert.rejects(fetch(preview.url));
  } finally {
    await preview?.close();
    await rm(workspace, { recursive: true, force: true });
  }
});

for (const invalid of ["corrupt", "synthetic"]) test(`canvas fails closed when downloaded canonical data is ${invalid}`, {
  skip: process.platform === "win32",
}, async (t) => {
  const { workspace, ghExecutable } = await fixture(t);
  try {
    if (invalid === "corrupt") {
      await writeFile(join(workspace, ".cao", "gh-aw-logs-runs", "canonical.jsonl"), "invalid snapshot");
    } else {
      const manifestPath = join(workspace, ".cao", "payload-hashes.json");
      const manifest = JSON.parse(await readFile(manifestPath, "utf8"));
      await writeFile(manifestPath, JSON.stringify({ ...manifest, synthetic: true }));
    }
    await assert.rejects(startLocalDashboardPreview({
      workingDirectory: workspace,
      executeCliAction: async () => {},
      approveCliAction: async () => false,
      ghExecutable,
    }), invalid === "corrupt" ? /checksum/ : /Synthetic Pages demo/);
    assert.deepEqual(await previewDirectories(workspace), []);
  } finally {
    await rm(workspace, { recursive: true, force: true });
  }
});

for (const layout of ["dashboard", ".github/aw/dashboard"]) {
  test(`canvas ignores executable code and site assets in the ${layout} workspace layout`, {
    skip: process.platform === "win32",
  }, async (t) => {
    const { workspace, ghExecutable } = await fixture(t);
    const dashboardDirectory = join(workspace, layout);
    let preview;
    try {
      await mkdir(dashboardDirectory, { recursive: true });
      await mkdir(join(dashboardDirectory, "site"), { recursive: true });
      await writeFile(join(dashboardDirectory, "site", "index.html"), "workspace site must not be served");
      await writeFile(join(dashboardDirectory, "canvas-server.mjs"), `
throw new Error("Workspace server must never execute.");
`);
      preview = await startLocalDashboardPreview({
        workingDirectory: workspace,
        executeCliAction: async () => {},
        approveCliAction: async () => false,
        ghExecutable,
      });
      const response = await fetch(preview.url);
      assert.equal(response.status, 200);
      assert.doesNotMatch(await response.text(), /workspace site must not be served/);
      assert.equal((await previewDirectories(workspace)).length, 2);
      await preview.close();
      assert.deepEqual(await previewDirectories(workspace), []);
    } finally {
      await preview?.close();
      await rm(workspace, { recursive: true, force: true });
    }
  });
}

test("failed plugin canvas startup removes staged assets", {
  skip: process.platform === "win32",
}, async (t) => {
  const { workspace, ghExecutable } = await fixture(t);
  try {
    await rm(join(workspace, ".cao", "payload-hashes.json"));
    await assert.rejects(startLocalDashboardPreview({
      workingDirectory: workspace,
      executeCliAction: async () => {},
      approveCliAction: async () => false,
      ghExecutable,
    }), /Prepare a checksum-verified canonical JSONL artifact/);
    assert.deepEqual(await previewDirectories(workspace), []);
  } finally {
    await rm(workspace, { recursive: true, force: true });
  }
});

describe("extension lifecycle with provisioned PostgreSQL", { skip: process.platform === "win32" }, () => {
  let postgres;
  before(async () => { postgres = await prepareCanvasPostgres(); });
  after(async () => { await postgres?.close(); });

  test("extension SIGTERM closes live previews before exiting", {
    skip: process.platform === "win32",
    timeout: 30_000,
  }, async (t) => {
    const { workspace } = await fixture(t, postgres);
    const sdkUrl = import.meta.resolve("@github/copilot-sdk/extension");
    const shim = `export { CanvasError, createCanvas } from ${JSON.stringify(sdkUrl)};
export async function joinSession(config) {
  const preview = await config.canvases[0].open({
    sessionId: "test", extensionId: "test:cao", canvasId: "cao-dashboard",
    instanceId: "preview", session: { workingDirectory: process.cwd() }
  });
  setImmediate(() => process.stdout.write(JSON.stringify(preview) + "\\n"));
}`;
    const shimUrl = `data:text/javascript,${encodeURIComponent(shim)}`;
    const preload = `import { registerHooks } from "node:module";
registerHooks({ resolve(specifier, context, next) {
  return specifier === "@github/copilot-sdk/extension"
    ? { url: ${JSON.stringify(shimUrl)}, shortCircuit: true }
    : next(specifier, context);
} });`;
    const child = spawn(process.execPath, [
      "--import", `data:text/javascript,${encodeURIComponent(preload)}`,
      fileURLToPath(extensionUrl),
    ], {
      cwd: workspace,
      env: { ...process.env, PATH: `${workspace}:${process.env.PATH}` },
      stdio: ["ignore", "pipe", "pipe"],
    });
    let stderr = "";
    child.stderr.setEncoding("utf8").on("data", (chunk) => { stderr += chunk; });
    const lines = createInterface({ input: child.stdout });
    let exited = false;
    const exit = once(child, "exit").then((result) => { exited = true; return result; });
    try {
      const line = await Promise.race([
        once(lines, "line").then(([line]) => line),
        exit.then(() => { throw new Error(`Extension exited before opening: ${stderr}`); }),
      ]);
      const { url } = JSON.parse(line);
      assert.equal((await fetch(url)).status, 200);
      assert.equal((await previewDirectories(workspace)).length, 2);
      child.kill("SIGTERM");
      const [code, signal] = await exit;
      assert.equal(code, 0, stderr);
      assert.equal(signal, null);
      assert.deepEqual(await previewDirectories(workspace), []);
    } finally {
      lines.close();
      if (!exited) {
        child.kill("SIGTERM");
        await exit;
      }
      await rm(workspace, { recursive: true, force: true });
    }
  });
});
