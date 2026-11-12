import assert from "node:assert/strict";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { startCanvasServer } from "../helpers/canvas-snapshot.mjs";
import { writeCanvasSnapshot } from "../helpers/canvas-snapshot.mjs";

const document = await readFile(new URL("../../dashboard/site/test/fixtures/query-editor.json", import.meta.url), "utf8");
const intent = { intent: "Find failing workflows", subject: "Workflow failures", acceptance: "Show native conclusions." };

async function preview(t, options = {}) {
  const { dashboardDocument = document, ...serverOptions } = options;
  const root = await mkdtemp(join(tmpdir(), "cao-query-editor-test-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  await mkdir(join(root, "site"));
  await writeFile(join(root, "site", "index.html"), "<!doctype html><html><head></head><body>CAO preview</body></html>");
  await writeFile(join(root, "site", "dashboard.json"), dashboardDocument);
  const server = await startCanvasServer({
    siteRoot: join(root, "site"), catalogRoot: null, workingDirectory: root, port: 0,
    output: () => {}, requestOutput: () => {}, traceOutput: () => {},
    executeCliAction: async () => ({}), approveCliAction: async () => true,
    prepareSnapshot: async (destination) => { await writeCanvasSnapshot(destination); },
    ...serverOptions,
  });
  t.after(() => server.close());
  return { ...server, root };
}

const requestOptions = (server, payload = intent, origin = new URL(server.url).origin) => ({
  method: "POST", headers: { "Content-Type": "application/json", Origin: origin }, body: JSON.stringify(payload),
});

test("Copilot session handoff is capability-scoped, same-origin, bounded and single-flight", async (t) => {
  const normal = await preview(t);
  assert.equal((await fetch(`${normal.url}/__copilot_session`, requestOptions(normal, { prompt: "Task" }))).status, 404);
  assert.doesNotMatch(await (await fetch(normal.url)).text(), /dashboard-copilot-session/);
  let submitted;
  let finish;
  const server = await preview(t, {
    startSession: async (prompt) => {
      submitted = prompt;
      await new Promise((resolve) => { finish = resolve; });
    },
  });
  const endpoint = `${server.url}/__copilot_session`;
  assert.match(await (await fetch(server.url)).text(), /name="dashboard-copilot-session"/);
  assert.equal((await fetch(endpoint)).status, 405);
  assert.equal((await fetch(endpoint, requestOptions(server, { prompt: "Task" }, "https://untrusted.test"))).status, 403);
  assert.equal((await fetch(endpoint, { method: "POST", body: "{}" })).status, 403);
  for (const body of [{ prompt: "" }, { prompt: " " }, { prompt: "Task", command: "extra" }, { prompt: "x".repeat(100001) }]) {
    assert.equal((await fetch(endpoint, requestOptions(server, body))).status, 400);
  }
  assert.equal((await fetch(`${new URL(server.url).origin}/__copilot_session`, requestOptions(server, { prompt: "Task" }))).status, 404);
  const pending = fetch(endpoint, requestOptions(server, { prompt: "Create a Pull Request." }));
  while (!submitted) await new Promise((resolve) => setTimeout(resolve, 5));
  assert.equal(submitted, "Create a Pull Request.");
  assert.equal((await fetch(endpoint, requestOptions(server, { prompt: "Duplicate" }))).status, 409);
  finish();
  assert.deepEqual(await (await pending).json(), { ok: true });
});

test("Copilot session failures are explicit and never disclose prompt content", async (t) => {
  const server = await preview(t, { startSession: async () => { throw new Error("private prompt"); } });
  const response = await fetch(`${server.url}/__copilot_session`, requestOptions(server, { prompt: "Task" }));
  assert.equal(response.status, 503);
  assert.match((await response.json()).error, /Could not start/);
});

test("query editor is injected only when the canvas host supplies a query generator", async (t) => {
  const normal = await preview(t);
  assert.equal((await fetch(`${normal.url}/__query_designer`, requestOptions(normal))).status, 404);
  assert.equal((await fetch(`${normal.url}/?local-preview=canvas`)).status, 200);
  const normalDashboard = await (await fetch(`${normal.url}/dashboard.json`)).json();
  assert.ok(!normalDashboard.dashboard.pages.some(({ id }) => id === "query-editor"));

  const canvas = await preview(t, { generateQuery: async () => ({ document }) });
  const dashboard = await (await fetch(`${canvas.url}/dashboard.json`)).json();
  const page = dashboard.dashboard.pages.find(({ id }) => id === "query-editor");
  assert.ok(page);
  assert.deepEqual(dashboard.dashboard.navigation, [
    { label: "Experimental", experimental: true, pages: ["query-editor"] },
  ]);
  const chunk = await (await fetch(`${canvas.url}/${page.chunk}`)).json();
  assert.equal(chunk.page.views[0].element, "query-editor");
  assert.deepEqual(chunk.page.views[0].data.sources, []);
});

test("query editor joins the existing Experimental section without duplicating navigation", async (t) => {
  const base = JSON.parse(document);
  base.dashboard.navigation = [{ label: "Experimental", experimental: true, pages: ["query-preview"] }];
  const canvas = await preview(t, {
    generateQuery: async () => ({ document }), dashboardDocument: JSON.stringify(base),
  });
  const dashboard = await (await fetch(`${canvas.url}/dashboard.json`)).json();
  assert.deepEqual(dashboard.dashboard.navigation, [
    { label: "Experimental", experimental: true, pages: ["query-preview", "query-editor"] },
  ]);
});

test("query generation enforces capability paths, Origin, method, bounded intent and one active request", async (t) => {
  let submitted;
  let finish;
  const server = await preview(t, {
    generateQuery: async (input) => {
      submitted = input;
      return new Promise((resolve) => { finish = resolve; });
    },
  });
  const endpoint = `${server.url}/__query_designer`;
  assert.equal((await fetch(endpoint)).status, 405);
  assert.equal((await fetch(endpoint, requestOptions(server, intent, "https://untrusted.test"))).status, 403);
  assert.equal((await fetch(endpoint, requestOptions(server, { ...intent, model: "custom" }))).status, 400);
  const oversized = await fetch(endpoint, requestOptions(server, { ...intent, acceptance: "x".repeat(512) }));
  assert.equal(oversized.status, 400);
  assert.match((await oversized.json()).error, /at most 512 characters/);
  assert.equal((await fetch(endpoint, requestOptions(server, { ...intent, intent: "x".repeat(200000) }))).status, 413);
  assert.equal((await fetch(`${new URL(server.url).origin}/__query_designer`, requestOptions(server))).status, 404);
  const pending = fetch(endpoint, requestOptions(server));
  while (!submitted) await new Promise((resolve) => setTimeout(resolve, 5));
  assert.deepEqual(submitted, intent);
  assert.equal((await fetch(endpoint, requestOptions(server))).status, 409);
  finish({ document });
  assert.deepEqual(await (await pending).json(), { document });
});

test("disconnect cancels generation and failures do not expose SDK prompt content", async (t) => {
  let started;
  let cancelled;
  const starting = new Promise((resolve) => { started = resolve; });
  const cancellation = new Promise((resolve) => { cancelled = resolve; });
  let calls = 0;
  const server = await preview(t, {
    generateQuery: async (_intent, { signal }) => {
      if (++calls > 1) throw new Error("private model prompt content");
      started();
      return new Promise((_resolve, reject) => signal.addEventListener("abort", () => {
        cancelled();
        reject(new DOMException("Cancelled", "AbortError"));
      }, { once: true }));
    },
  });

  const controller = new AbortController();
  const pending = fetch(`${server.url}/__query_designer`, { ...requestOptions(server), signal: controller.signal });
  await starting;
  controller.abort();
  await assert.rejects(pending, { name: "AbortError" });
  await cancellation;
  await new Promise((resolve) => setTimeout(resolve, 10));
  const response = await fetch(`${server.url}/__query_designer`, requestOptions(server));
  assert.equal(response.status, 503);
  assert.doesNotMatch(await response.text(), /private model prompt content/);
});

test("all-field improvement accepts partial intent but requires an authoring adapter and cannot request tools", async (t) => {
  const request = { intent: "Find failing workflows" };
  const improved = { ...intent, objective: "Prioritize investigation" };
  let submitted;
  const server = await preview(t, {
    generateQuery: async () => ({ document }),
    enhanceQueryIntent: async (input) => { submitted = input; return improved; },
  });
  const endpoint = `${server.url}/__query_designer/enhance`;
  assert.equal((await fetch(endpoint, requestOptions(server, { ...request, tools: ["shell"] }))).status, 400);
  assert.equal((await fetch(endpoint, requestOptions(server, request, "https://untrusted.test"))).status, 403);
  assert.deepEqual(await (await fetch(endpoint, requestOptions(server, request))).json(), improved);
  assert.deepEqual(submitted, request);
  const normal = await preview(t);
  assert.equal((await fetch(`${normal.url}/__query_designer/enhance`, requestOptions(normal, request))).status, 404);
});

test("field improvement can condense overlong input but rejects over-budget callback output", async (t) => {
  const request = { ...intent, acceptance: "x".repeat(512) };
  const improved = { ...intent, objective: "Prioritize investigation." };
  let valid = true;
  const server = await preview(t, {
    enhanceQueryIntent: async () => valid ? improved : { ...improved, acceptance: "x".repeat(512) },
  });
  const endpoint = `${server.url}/__query_designer/enhance`;
  const accepted = await fetch(endpoint, requestOptions(server, request));
  assert.equal(accepted.status, 200);
  assert.deepEqual(await accepted.json(), improved);
  valid = false;
  const rejected = await fetch(endpoint, requestOptions(server, request));
  assert.equal(rejected.status, 503);
  assert.doesNotMatch(await rejected.text(), /x{512}/);
});

test("custom view saves require a valid canvas document and populate isolated local navigation", async (t) => {
    const server = await preview(t, { generateQuery: async () => ({ document }) });
    const endpoint = `${server.url}/__custom_views`;
    assert.equal((await fetch(endpoint)).status, 405);
    assert.equal((await fetch(endpoint, requestOptions(server, { document }, "https://untrusted.test"))).status, 403);
    assert.equal((await fetch(endpoint, requestOptions(server, { document, path: "../outside.json" }))).status, 400);
    assert.equal((await fetch(endpoint, requestOptions(server, { document: "[invalid" }))).status, 400);
    const response = await fetch(endpoint, requestOptions(server, { document }));
    assert.equal(response.status, 201);
    const saved = await response.json();
    assert.match(saved.id, /^local-[a-f0-9]{24}$/);
    assert.deepEqual(JSON.parse(await readFile(join(server.root, saved.path), "utf8")), JSON.parse(document));
    assert.ok(saved.dashboard.dashboard.navigation.some((section) => section.label === "Custom views" && section.pages.includes(saved.id)));
    const page = saved.dashboard.dashboard.pages.find((page) => page.id === saved.id);
    const chunk = await (await fetch(`${server.url}/${page.chunk}`)).json();
    assert.equal(chunk.page.views[0].data.source, `${saved.id}-failure-counts`);
    assert.equal((await (await fetch(endpoint, requestOptions(server, { document }))).json()).id, saved.id);
    await server.close();
    const restarted = await preview(t, {
      generateQuery: async () => ({ document }),
      workingDirectory: server.root, siteRoot: join(server.root, "site"),
    });
    const restored = await (await fetch(`${restarted.url}/dashboard.json`)).json();
    const restoredPage = restored.dashboard.pages.find((page) => page.id === saved.id);
    assert.ok(restoredPage);
    const restoredChunk = await (await fetch(`${restarted.url}/${restoredPage.chunk}`)).json();
    assert.equal(restoredChunk.page.views[0].data.source, `${saved.id}-failure-counts`);
    await writeFile(join(server.root, saved.path), "[invalid");
    const failed = await fetch(`${restarted.url}/__custom_views`, requestOptions(restarted, { document }));
    assert.equal(failed.status, 503);
    assert.doesNotMatch(await failed.text(), new RegExp(server.root));
    const normal = await preview(t);
    assert.equal((await fetch(`${normal.url}/__custom_views`, requestOptions(normal, { document }))).status, 404);
});
