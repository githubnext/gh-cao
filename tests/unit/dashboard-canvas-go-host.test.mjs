import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { mkdir, mkdtemp, readFile, readdir, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { startCanvasServer } from "../../dashboard/canvas-server.mjs";
import { writeCanvasSnapshot } from "../helpers/canvas-snapshot.mjs";
import { normalize } from "../../dashboard/site/src/data/normalize/index.js";
import { queryDashboardSourceObservations } from "../../dashboard/site/src/data/queries/ingestion.js";

async function fixture(t, downloadData, options = {}) {
  const root = await mkdtemp(join(tmpdir(), "cao-canvas-go-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  await writeFile(join(root, "index.html"), "<html><head></head><body>preview</body></html>");
  await writeFile(join(root, "dashboard.json"), JSON.stringify({
    "language-version": "0.1.0",
    dashboard: { id: "test", title: "Test", navigation: [{ label: "Test", pages: ["test"] }],
      pages: [{ id: "test", title: "Test", kind: "custom", views: [] }] },
  }));
  return { root, start: () => startCanvasServer({
    workingDirectory: root, siteRoot: root, catalogRoot: null, port: 0, downloadData,
    output: () => {}, traceOutput: () => {}, requestOutput: () => {}, ...options,
  }) };
}

test("canvas serves canonical queries, pagination, diagnostics and memory through Go without source transport", async (t) => {
  const metadata = { "as-of": "2026-10-01T00:00:00Z" };
  const records = normalize(queryDashboardSourceObservations({
    repositories: { metadata, rows: [
      { organization: "acme", repository: "one" }, { organization: "acme", repository: "two" },
    ] },
  }).observations);
  const content = "bounded memory\n";
  const { root, start } = await fixture(t, async (destination) => {
    await writeCanvasSnapshot(destination, records);
    await writeFile(join(destination, "inventory-sources.json"), JSON.stringify({
      repositories: { metadata: { ...metadata, completeness: "partial", freshness: "fresh" }, rows: [] },
    }));
    await mkdir(join(destination, "memory", "dashboard"), { recursive: true });
    await writeFile(join(destination, "memory", "dashboard", "notes.md"), content);
    await writeFile(join(destination, "memory", "manifest.json"), JSON.stringify({
      version: 1, campaigns: [{
        campaign: "dashboard", branch: "memory/dashboard", commit: "a".repeat(40),
        files: [{ path: "notes.md", size: Buffer.byteLength(content), oid: "b".repeat(40),
          sha256: createHash("sha256").update(content).digest("hex") }],
      }],
    }));
  });
  const preview = await start();
  t.after(() => preview.close());
  const html = await (await fetch(preview.url)).text();
  assert.match(html, /name="dashboard-data-backend" content="server-http"/);
  assert.match(html, /name="dashboard-storage-backend" content="sqlite"/);
  assert.match(html, /name="dashboard-api-base"/);
  const query = async (pagination) => {
    const response = await fetch(`${preview.url}/api/v1/query`, {
      method: "POST", headers: { Origin: new URL(preview.url).origin, "Content-Type": "application/json" },
      body: JSON.stringify({
        sourceNames: ["repositories"], replacedSources: ["repositories"], aliases: ["selected"],
        compiledQueries: [{ name: "selected", from: "repositories", "order-by": [{ field: "repository" }] }],
        pagination: { selected: pagination },
      }),
    });
    assert.equal(response.status, 200, await response.clone().text());
    return response.json();
  };
  const first = await query({ limit: 1 });
  assert.equal(first.sources.selected.metadata.completeness, "partial");
  assert.deepEqual(Object.keys(first.sources), ["selected"]);
  assert.equal(first.sources.selected.rows[0].repository, "one");
  assert.equal(typeof first.sources.selected.continuationToken, "string");
  const second = await query({ limit: 1, continuationToken: first.sources.selected.continuationToken });
  assert.equal(second.sources.selected.rows[0].repository, "two");
  assert.ok(first.metrics);
  const diagnostics = await (await fetch(`${preview.url}/api/v1/diagnostics`)).json();
  assert.equal(diagnostics.counts.repositories, 2);
  assert.deepEqual(diagnostics.relationshipErrors, []);
  const memory = await (await fetch(`${preview.url}/api/v1/memory/dashboard/content?path=notes.md`)).json();
  assert.equal(memory.content, content);
  assert.equal((await (await fetch(`${preview.url}/api/v1/memory/dashboard`)).json()).files[0].path, "notes.md");
  for (const path of ["sources.json", "sources/manifest.json", "sources/repositories.json",
    "inventory-sources.json", "payload-hashes.json", "gh-aw-logs.sqlite"]) {
    assert.equal((await fetch(`${preview.url}/${path}`)).status, 404, path);
  }
  assert.equal((await fetch(`${new URL(preview.url).origin}/api/v1/diagnostics`)).status, 404);
  const lifetime = new AbortController();
  const events = await fetch(`${preview.url}/api/v1/events`, { signal: lifetime.signal });
  assert.match(events.headers.get("Content-Type"), /text\/event-stream/);
  const reader = events.body.getReader();
  assert.match(new TextDecoder().decode((await reader.read()).value), /"revision":/);
  lifetime.abort();
  await reader.cancel().catch((error) => assert.equal(error.name, "AbortError"));
  const directories = (await readdir(root)).filter((name) => name.startsWith(".cao-dashboard-preview-"));
  assert.equal(directories.length, 1);
  assert.ok((await readdir(join(root, directories[0]))).includes("canvas.json"));
  assert.ok(!(await readdir(join(root, directories[0]))).includes("dashboard.sqlite"));
  await preview.close();
  assert.equal((await readdir(root)).filter((name) => name.startsWith(".cao-dashboard-preview-")).length, 0);
  await assert.rejects(fetch(preview.url));
});

test("canvas resolves localhost and IPv6 to the numeric loopback Go listener", async (t) => {
  for (const host of ["localhost", "::1"]) {
    const { start } = await fixture(t, (destination) => writeCanvasSnapshot(destination), { host });
    const preview = await start();
    try {
      assert.ok(["127.0.0.1", "[::1]"].includes(new URL(preview.url).hostname));
      assert.equal((await fetch(preview.url)).status, 200);
    } finally { await preview.close(); }
  }
});

test("canvas rejects legacy, corrupt and invalid canonical evidence without fallback and cleans staging", async (t) => {
  for (const kind of ["legacy", "checksum", "invalid"]) {
    const { root, start } = await fixture(t, async (destination) => {
      if (kind === "legacy") {
        await mkdir(destination, { recursive: true });
        await writeFile(join(destination, "sources.json"), "{}");
      } else {
        await writeCanvasSnapshot(destination);
        if (kind === "invalid") {
          const content = Buffer.from("not sqlite");
          await writeFile(join(destination, "gh-aw-logs.sqlite"), content);
          await writeFile(join(destination, "payload-hashes.json"), JSON.stringify({
            "gh-aw-logs.sqlite": createHash("sha256").update(content).digest("hex"),
          }));
        } else await writeFile(join(destination, "gh-aw-logs.sqlite"), "corrupted snapshot");
      }
    });
    await assert.rejects(start(), kind === "legacy"
      ? /CAO Activity artifact requires gh-aw-logs\.sqlite and payload-hashes\.json at its root/
      : kind === "checksum" ? /checksum/ : /Go host exited/);
    assert.equal((await readdir(root)).filter((name) => name.startsWith(".cao-dashboard-preview-")).length, 0);
  }
});

test("canvas source contains no Node canonical query engine or legacy data routes", async () => {
  const source = await readFile(new URL("../../dashboard/canvas-server.mjs", import.meta.url), "utf8");
  assert.doesNotMatch(source, /startLocalSqliteBackend|ingestDashboardSources|loadDatabaseQuerySources|splitSourceContent/);
  for (const filename of ["local-sqlite-backend.mjs", "local-sqlite-worker.mjs"]) {
    await assert.rejects(readFile(new URL(`../../dashboard/${filename}`, import.meta.url)), { code: "ENOENT" });
  }
});
