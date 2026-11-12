import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import { chmod, mkdir, mkdtemp, readFile, realpath, rm, stat, symlink, writeFile } from "node:fs/promises";
import { request } from "node:http";
import { tmpdir } from "node:os";
import path from "node:path";
import test from "node:test";
import { startCanvasServer } from "../helpers/canvas-snapshot.mjs";
import { writeCanvasSnapshot } from "../helpers/canvas-snapshot.mjs";
import { refreshDashboardCompilerRelease } from "../../dashboard/local-release-metadata.mjs";

test("local release refresh preserves snapshot evidence and recomputes upgrade state", () => {
  const snapshot = {
    workflows: {
      metadata: { "as-of": "2026-10-01T00:00:00Z" },
      rows: ["v0.91.5", "v0.92.0", "unknown"].map((version) => ({
        "gh-aw-version": version, "gh-aw-current-version": "v0.89.21",
        "gh-aw-update-state": "up-to-date", "observed-at": "2026-10-01T00:00:00Z",
      })),
    },
  };
  const refreshed = refreshDashboardCompilerRelease(snapshot, "v0.92.0");
  assert.deepEqual(refreshed.workflows.rows.map((row) => row["gh-aw-current-version"]), ["v0.92.0", "v0.92.0", "v0.92.0"]);
  assert.deepEqual(refreshed.workflows.rows.map((row) => row["gh-aw-update-state"]), ["update-available", "up-to-date", "unknown"]);
  assert.deepEqual(refreshed.workflows.rows.map((row) => row["gh-aw-version"]), ["v0.91.5", "v0.92.0", "unknown"]);
  assert.deepEqual(refreshed.workflows.metadata, snapshot.workflows.metadata);
  assert.equal(refreshed.workflows.rows[0]["observed-at"], snapshot.workflows.rows[0]["observed-at"]);
  assert.equal(snapshot.workflows.rows[0]["gh-aw-current-version"], "v0.89.21");
  assert.throws(() => refreshDashboardCompilerRelease(snapshot, ""), /invalid latest stable/);
  assert.throws(() => refreshDashboardCompilerRelease(snapshot, "v0.93.0-rc.1"), /invalid latest stable/);
});

const dashboard = (pageId, cliActions) => JSON.stringify({
  "language-version": "0.1.0",
  dashboard: {
    id: "preview",
    title: "Preview",
    ...(cliActions ? { "cli-actions": cliActions } : {}),
    navigation: [{ label: "Preview", pages: [pageId] }],
    pages: [{
      id: pageId,
      title: pageId,
      kind: "custom",
      views: [{
        id: "summary",
        title: "Summary",
        mark: "element",
        element: "summary-grid",
        data: { sources: ["repositories"] },
      }],
    }],
  },
}, null, 2);

async function openDashboardSocket(previewUrl) {
  const url = new URL(previewUrl);
  url.protocol = "ws:";
  url.pathname += "/__dashboard_socket";
  const socket = new WebSocket(url);
  await new Promise((resolve, reject) => {
    socket.addEventListener("open", resolve, { once: true });
    socket.addEventListener("error", reject, { once: true });
  });
  return socket;
}

async function nextDashboard(socket) {
  return new Promise((resolve, reject) => {
    socket.addEventListener("message", (event) => resolve(JSON.parse(event.data)), { once: true });
    socket.addEventListener("error", reject, { once: true });
  });
}

async function requestWithHost(url, host) {
  return new Promise((resolve, reject) => {
    const target = new URL(url);
    const outgoing = request({
      hostname: target.hostname, port: target.port, path: target.pathname, headers: { Host: host },
    }, (response) => {
      response.resume();
      response.on("end", () => resolve(response.statusCode));
    });
    outgoing.on("error", reject);
    outgoing.end();
  });
}

test("local GitHub routes enforce read-only same-origin access and advertise their worker API base", async () => {
  const root = await mkdtemp(path.join(tmpdir(), "dashboard-local-github-http-"));
  const siteRoot = path.join(root, "site");
  const catalogRoot = path.join(root, "catalog");
  await mkdir(siteRoot);
  await mkdir(catalogRoot);
  await writeFile(path.join(siteRoot, "index.html"), "<!doctype html><html><head></head><body>preview</body></html>");
  await writeFile(path.join(siteRoot, "dashboard.json"), dashboard("preview"));
  const preview = await startCanvasServer({
    siteRoot, catalogRoot, workingDirectory: root, port: 0,
    prepareSnapshot: async (destination) => {
      await writeCanvasSnapshot(destination);
    },
    output: () => {},
    requestOutput: () => {},
  });
  try {
    const html = await (await fetch(`${preview.url}/`)).text();
    assert.match(html, /<meta name="dashboard-github-api-base" content="[^"]*\/">/);
    const resource = `${preview.url}/api/v1/github/repos/githubnext/gh-cao`;
    for (const endpoint of [resource, `${resource}/releases/latest`]) {
      for (const method of ["POST", "PUT", "PATCH", "DELETE", "OPTIONS", "HEAD"]) {
        const rejected = await fetch(endpoint, { method });
        assert.equal(rejected.status, 405, `${method} ${endpoint}`);
        assert.equal(rejected.headers.get("Allow"), "GET");
      }
    }
    assert.equal((await fetch(resource, { headers: { Origin: "https://untrusted.example" } })).status, 403);
    assert.equal((await fetch(resource, { headers: { "Sec-Fetch-Site": "cross-site" } })).status, 403);
    assert.equal((await fetch(`${resource}/issues`)).status, 400);
  } finally {
    await preview.close();
    await rm(root, { recursive: true, force: true });
  }
});

test("local dashboard server composes campaign dashboards and reloads after updates", async () => {
  const root = await mkdtemp(path.join(tmpdir(), "dashboard-local-server-"));
  const siteRoot = path.join(root, "site");
  const campaignRoot = path.join(root, "campaigns");
  const campaignDirectory = path.join(campaignRoot, "example");
  const campaignFeatureDirectory = path.join(campaignDirectory, "features");
  const stalePreviewDirectory = path.join(campaignRoot, ".cao-dashboard-preview-stale");
  await mkdir(campaignDirectory, { recursive: true });
  await mkdir(campaignFeatureDirectory, { recursive: true });
  await mkdir(stalePreviewDirectory, { recursive: true });
  await mkdir(siteRoot, { recursive: true });
  const syntheticToken = ["ghp", "abcdefghijklmnopqrstuvwxyz123456"].join("_");
  await writeFile(path.join(siteRoot, "index.html"), `<!doctype html><body>preview ${syntheticToken}</body>`);
  await writeFile(path.join(siteRoot, "guide.md"), `# Guide\n\n${syntheticToken}\n`);
  await writeFile(path.join(siteRoot, "image.png"), Buffer.from([0x89, 0x50, 0x4e, 0x47]));
  await writeFile(path.join(siteRoot, "private.txt"), "must not be served");
  await writeFile(path.join(siteRoot, "private"), "must not be served");
  const defaultsSource = await readFile(new URL("../../dashboard/site/src/environment-defaults.js", import.meta.url), "utf8");
  await mkdir(path.join(siteRoot, "src"), { recursive: true });
  await writeFile(path.join(siteRoot, "src/environment-defaults.js"), defaultsSource);
  const builtInDashboard = JSON.parse(dashboard("built-in"));
  builtInDashboard.dashboard.description = syntheticToken;
  await writeFile(path.join(siteRoot, "dashboard.json"), JSON.stringify(builtInDashboard));
  await writeFile(path.join(campaignDirectory, "dashboard.json"), JSON.stringify({
    "language-version": "0.1.0",
    fragments: ["features/campaign.json"],
    dashboard: { id: "campaign", title: "Campaign" },
  }));
  const campaignOne = JSON.parse(dashboard("campaign-one")).dashboard;
  delete campaignOne.id;
  delete campaignOne.title;
  await writeFile(
    path.join(campaignFeatureDirectory, "campaign.json"),
    JSON.stringify(campaignOne),
  );
  await writeFile(path.join(stalePreviewDirectory, "dashboard.json"), dashboard("built-in"));
  const prepareSnapshot = async (destination) => {
    await writeCanvasSnapshot(destination);
  };
  const requestLogs = [];

  const preview = await startCanvasServer({
    siteRoot,
    catalogRoot: campaignRoot,
    prepareSnapshot,
    allowMissingOrigin: true,
    workingDirectory: root,
    requestOutput: (message) => requestLogs.push(message),
    port: 0,
  });
  try {
    const indexResponse = await fetch(`${preview.url}/`);
    assert.equal(indexResponse.status, 200);
    assert.equal(new URL(indexResponse.url).searchParams.get("local-preview"), "canvas");
    const index = await indexResponse.text();
    assert.equal((await fetch(`${preview.url}/__cli_action`, {
      method: "POST",
      headers: { "Content-Type": "application/json", Origin: new URL(preview.url).origin },
      body: JSON.stringify({ id: "compile-workflows" }),
    })).status, 404);
    assert.doesNotMatch(index, /location\.reload/);
    assert.doesNotMatch(index, /<script[^>]+src=.*copilot-prompt/);
    assert.doesNotMatch(index, new RegExp(syntheticToken));
    assert.match(index, /\[REDACTED\]/);
    const markdownResponse = await fetch(`${preview.url}/guide.md`);
    assert.equal(markdownResponse.status, 200);
    assert.equal(markdownResponse.headers.get("content-type"), "text/plain; charset=utf-8");
    assert.doesNotMatch(await markdownResponse.text(), new RegExp(syntheticToken));
    const markdownHeadResponse = await fetch(`${preview.url}/guide.md`, { method: "HEAD" });
    assert.equal(markdownHeadResponse.status, 200);
    assert.equal(markdownHeadResponse.headers.get("content-type"), "text/plain; charset=utf-8");
    assert.equal(await markdownHeadResponse.text(), "");
    const imageResponse = await fetch(`${preview.url}/image.png`);
    assert.equal(imageResponse.status, 200);
    assert.equal(imageResponse.headers.get("content-type"), "image/png");
    assert.deepEqual(Buffer.from(await imageResponse.arrayBuffer()), Buffer.from([0x89, 0x50, 0x4e, 0x47]));
    assert.equal((await fetch(`${preview.url}/private.txt`)).status, 404);
    assert.equal((await fetch(`${preview.url}/private`)).status, 404);
    const defaultsResponse = await fetch(`${preview.url}/src/environment-defaults.js`);
    assert.equal(defaultsResponse.status, 200);
    assert.equal(await defaultsResponse.text(), defaultsSource);

    const dashboardResponse = await fetch(`${preview.url}/dashboard.json`);
    assert.equal(dashboardResponse.headers.get("cache-control"), "no-store");
    const browserDashboard = await dashboardResponse.json();
    assert.deepEqual(
      browserDashboard.dashboard.pages.map(({ id }) => id),
      ["built-in", "campaign-one"],
    );
    assert.equal(browserDashboard.dashboard.description, "[REDACTED]");
    assert.equal((await fetch(`${preview.url}/sources.json`)).status, 404);
    assert.equal((await fetch(`${preview.url}/sources/repositories.json`)).status, 404);
    const traversalResponse = await fetch(`${preview.url}/..%2Foutside.txt`);
    assert.equal(traversalResponse.status, 404);
    assert.equal(await requestWithHost(`${preview.url}/sources.json`, "attacker.example"), 400);
    const unprotectedUrl = new URL(preview.url);
    unprotectedUrl.pathname = "/sources.json";
    assert.equal((await fetch(unprotectedUrl)).status, 404);

    const socket = await openDashboardSocket(preview.url);
    const update = nextDashboard(socket);
    const campaignTwo = JSON.parse(dashboard("campaign-two")).dashboard;
    delete campaignTwo.id;
    delete campaignTwo.title;
    await writeFile(
      path.join(campaignFeatureDirectory, "campaign.json"),
      JSON.stringify(campaignTwo),
    );
    assert.deepEqual(
      (await update).dashboard.pages.map(({ id }) => id),
      ["built-in", "campaign-two"],
    );
    socket.close();

    const updatedResponse = await fetch(`${preview.url}/dashboard.json`);
    assert.deepEqual(
      (await updatedResponse.json()).dashboard.pages.map(({ id }) => id),
      ["built-in", "campaign-two"],
    );
  } finally {
    await preview.close();
    await rm(root, { recursive: true, force: true });
  }
});

test("canvas actions stream incrementally, reject untrusted requests, and survive disconnect", async () => {
  const root = await mkdtemp(path.join(tmpdir(), "dashboard-admin-actions-"));
  await writeFile(path.join(root, "index.html"), "<!doctype html><body>preview</body>");
  await writeFile(path.join(root, "dashboard.json"), dashboard("built-in", [{
    id: "compile-workflows", label: "Compile", icon: "play", command: "gh aw compile",
  }]));
  const calls = [];
  let finish;
  let cancelled;
  const preview = await startCanvasServer({
    approveCliAction: async () => true, siteRoot: root, catalogRoot: null, workingDirectory: root, port: 0,
    cliQueueAdmission: async () => ({ concurrency: 1 }),
    output: () => {}, traceOutput: () => {}, requestOutput: () => {},
    prepareSnapshot: async (destination) => {
      await writeCanvasSnapshot(destination);
    },
    executeCliAction: async ({ command, onOutput, signal }) => {
      calls.push(command);
      onOutput({ stream: "stdout", data: "\u001b[32mRunning\u001b[0m\r\n" });
      await new Promise((resolve) => {
        finish = resolve;
        signal.addEventListener("abort", () => { cancelled = true; resolve(); }, { once: true });
      });
      return { ok: true, exitCode: 0 };
    },
  });
  try {
    const page = await fetch(`${preview.url}/`);
    assert.equal(new URL(page.url).searchParams.get("local-preview"), "canvas");
    const html = await page.text();
    assert.match(html, /globalThis\.__CLI_QUEUE__ = true/);
    assert.doesNotMatch(html, /globalThis\.__DASHBOARD_ADMIN__/);
    const url = `${preview.url}/__cli_action`;
    const headers = { "Content-Type": "application/json", Origin: new URL(url).origin };
    const request = (body, customHeaders = headers) => fetch(url, {
      method: "POST", headers: customHeaders, body: JSON.stringify(body),
    });
    assert.equal((await request({ id: "compile-workflows" }, { ...headers, Origin: "https://attacker.example" })).status, 403);
    assert.equal((await request({ id: "compile-workflows" }, { "Content-Type": "application/json" })).status, 403);
    assert.equal((await request({ id: "unknown" })).status, 404);
    assert.equal((await request({ id: "compile-workflows", command: "whoami" })).status, 400);
    const lifetime = new AbortController();
    const response = await fetch(url, {
      method: "POST", headers, body: JSON.stringify({ id: "compile-workflows" }),
      signal: lifetime.signal,
    });
    const reader = response.body.getReader();
    const first = new TextDecoder().decode((await reader.read()).value);
    assert.match(first, /Running/);
    assert.equal(calls.length, 1);
    const enqueued = await fetch(`${preview.url}/__cli_commands`, {
      method: "POST", headers, body: JSON.stringify({ id: "compile-workflows" }),
    });
    assert.equal(enqueued.status, 202);
    const queued = await enqueued.json();
    assert.equal(queued.status, "queued");
    lifetime.abort();
    await new Promise((resolve) => setTimeout(resolve, 50));
    assert.equal(cancelled, undefined);
    const list = await (await fetch(`${preview.url}/__cli_commands`)).json();
    assert.equal(list.running, 1);
    const running = list.commands.find((command) => command.status === "running");
    const cancel = await fetch(`${preview.url}/__cli_commands/${running.id}/cancel`, {
      method: "POST", headers, body: "{}",
    });
    assert.equal(cancel.status, 200);
    await new Promise((resolve) => setTimeout(resolve, 50));
    assert.equal(cancelled, true);
    await fetch(`${preview.url}/__cli_commands/${queued.id}/cancel`, {
      method: "POST", headers, body: "{}",
    });
    assert.equal((await fetch(`${preview.url}/src/components/cli-terminal-runtime.js`)).status, 200);
  } finally {
    finish?.();
    await preview.close();
    await rm(root, { recursive: true, force: true });
  }
});

test("queued commands retain IDs across server restart and replay output by cursor", { timeout: 15_000 }, async () => {
  const root = await mkdtemp(path.join(tmpdir(), "dashboard-durable-cli-"));
  await writeFile(path.join(root, "index.html"), "<!doctype html><head></head><body>preview</body>");
  await writeFile(path.join(root, "dashboard.json"), dashboard("built-in", [
    { id: "compile", label: "Compile", icon: "play", command: "gh aw compile" },
  ]));
  const options = {
    approveCliAction: async () => true, siteRoot: root, catalogRoot: null, workingDirectory: root, port: 0, output: () => {}, traceOutput: () => {}, requestOutput: () => {},
    prepareSnapshot: async (destination) => {
      await writeCanvasSnapshot(destination);
    },
    executeCliAction: async ({ onOutput }) => {
      await onOutput({ stream: "stdout", data: "first\n" });
      await onOutput({ stream: "stderr", data: "last\n" });
      return { ok: true, exitCode: 0 };
    },
  };
  let preview = await startCanvasServer({
    ...options, cliQueueAdmission: async () => ({ concurrency: 0, reason: "test-pause" }),
  });
  try {
    const post = () => fetch(`${preview.url}/__cli_commands`, {
      method: "POST",
      headers: { "Content-Type": "application/json", Origin: new URL(preview.url).origin },
      body: JSON.stringify({ id: "compile", idempotencyKey: "durable-command-1" }),
    });
    const enqueued = await post();
    assert.equal(enqueued.status, 202);
    const original = await enqueued.json();
    assert.equal((await (await post()).json()).id, original.id);
    await preview.close();
    preview = await startCanvasServer({
      ...options, cliQueueAdmission: async () => ({ concurrency: 1 }),
    });
    const stream = await fetch(`${preview.url}/__cli_commands/${original.id}/output`);
    const events = (await stream.text()).trim().split("\n").map((line) => JSON.parse(line));
    assert.equal(events.at(-1).command.id, original.id);
    assert.equal(events.at(-1).command.status, "completed");
    assert.equal(events.filter((event) => event.type === "output").map((event) => event.data).join(""), "first\nlast\n");
    const resumed = await fetch(`${preview.url}/__cli_commands/${original.id}/output?after=1`);
    const replay = (await resumed.text()).trim().split("\n").map((line) => JSON.parse(line));
    assert.equal(replay[0].sequence, 2);
    assert.equal(replay[0].stream, "stderr");
    assert.equal(replay.at(-1).result.exitCode, 0);
    assert.equal((await (await fetch(`${preview.url}/__cli_commands`)).json()).total, 1);
  } finally {
    await preview.close();
    await rm(root, { recursive: true, force: true });
  }
});

test("canvas queue refuses a persistence directory symlink outside the workspace", async () => {
  const root = await mkdtemp(path.join(tmpdir(), "dashboard-queue-path-"));
  const outside = await mkdtemp(path.join(tmpdir(), "dashboard-queue-outside-"));
  try {
    await writeFile(path.join(root, "index.html"), "<!doctype html><body>preview</body>");
    await writeFile(path.join(root, "dashboard.json"), dashboard("built-in"));
    await symlink(outside, path.join(root, ".queue"), "dir");
    await assert.rejects(startCanvasServer({
      approveCliAction: async () => true, executeCliAction: async () => {}, siteRoot: root, catalogRoot: null, workingDirectory: root, port: 0,
      cliQueuePath: path.join(root, ".queue", "cli-actions.sqlite"), output: () => {}, traceOutput: () => {}, requestOutput: () => {},
      prepareSnapshot: async (destination) => {
        await writeCanvasSnapshot(destination);
      },
      cliQueueAdmission: async () => ({ concurrency: 1 }),
    }), /queue database must remain within the workspace/);
    await assert.rejects(stat(path.join(outside, "cli-actions.sqlite")), { code: "ENOENT" });
  } finally {
    await rm(root, { recursive: true, force: true });
    await rm(outside, { recursive: true, force: true });
  }
});

test("local dashboard server fails when dashboard data cannot be downloaded", async () => {
  const root = await mkdtemp(path.join(tmpdir(), "dashboard-local-server-"));
  await writeFile(path.join(root, "index.html"), "<!doctype html><body>preview</body>");
  await writeFile(path.join(root, "dashboard.json"), dashboard("built-in"));

  try {
    await assert.rejects(
      startCanvasServer({
        siteRoot: root,
        catalogRoot: null,
        prepareSnapshot: async () => {
          throw new Error("artifact unavailable");
        },
        workingDirectory: root,
        port: 0,
      }),
      /artifact unavailable/,
    );
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test("canvas does not discover operational data inside a Pages artifact", async () => {
  const root = await mkdtemp(path.join(tmpdir(), "dashboard-canvas-pages-rejected-"));
  await writeFile(path.join(root, "index.html"), "<!doctype html><body>preview</body>");
  await writeFile(path.join(root, "dashboard.json"), dashboard("built-in"));
  try {
    await assert.rejects(startCanvasServer({
      siteRoot: root, catalogRoot: null, workingDirectory: root,
      prepareSnapshot: (destination) => writeCanvasSnapshot(path.join(destination, "cao")),
    }), /Prepare a checksum-verified operational snapshot/);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test("local dashboard server serves repository memory from canonical dashboard data", async () => {
  const root = await mkdtemp(path.join(tmpdir(), "dashboard-local-server-memory-"));
  const siteRoot = path.join(root, "site");
  await mkdir(siteRoot, { recursive: true });
  await writeFile(path.join(siteRoot, "index.html"), "<!doctype html><body>preview</body>");
  await writeFile(path.join(siteRoot, "dashboard.json"), dashboard("built-in"));
  const preview = await startCanvasServer({
    siteRoot,
    catalogRoot: null,
    prepareSnapshot: async (destination) => {
      const dataRoot = destination;
      await writeCanvasSnapshot(dataRoot);
      await mkdir(path.join(dataRoot, "memory", "ambient-context"), { recursive: true });
      const content = "bounded memory";
      await writeFile(path.join(dataRoot, "memory", "manifest.json"), JSON.stringify({
        version: 1, campaigns: [{
          campaign: "ambient-context", branch: "memory/ambient-context", commit: "a".repeat(40),
          files: [{ path: "notes.md", size: Buffer.byteLength(content), oid: "b".repeat(40),
            sha256: createHash("sha256").update(content).digest("hex") }],
        }],
      }));
      await writeFile(
        path.join(dataRoot, "memory", "ambient-context", "notes.md"),
        content,
      );
    },
    workingDirectory: root,
    port: 0,
  });
  try {
    assert.equal((await fetch(`${preview.url}/memory/manifest.json`)).status, 404);
    assert.equal(
      (await (await fetch(`${preview.url}/api/v1/memory/ambient-context/content?path=notes.md`)).json()).content,
      "bounded memory",
    );
  } finally {
    await preview.close();
    await rm(root, { recursive: true, force: true });
  }
});

test("canvas dashboard executes only declared CLI actions through the provided executor", async () => {
  const root = await mkdtemp(path.join(tmpdir(), "dashboard-canvas-actions-"));
  const calls = [];
  await writeFile(path.join(root, "index.html"), "<!doctype html><body>preview</body>");
  await writeFile(path.join(root, "dashboard.json"), dashboard("built-in", [
    {
      id: "compile-workflows",
      label: "Compile workflows",
      icon: "play",
      command: "gh aw compile --strict",
      arguments: [{
        id: "pre-releases",
        label: "Include pre-releases",
        type: "boolean",
        flag: "--pre-releases",
        default: false,
      }],
    },
    {
      id: "upgrade-repository",
      label: "Upgrade repository",
      icon: "download",
      command: "gh aw upgrade --repo {{repository}}",
      arguments: [{
        id: "create-pull-request",
        label: "Create pull request",
        type: "boolean",
        flag: "--create-pull-request",
        default: true,
      }],
    },
    {
      id: "update-target-repository",
      label: "Update repository",
      icon: "sync",
      command: "gh aw update --repo {{repository}}",
      placement: "row",
      arguments: [{
        id: "create-pull-request",
        label: "Create pull request",
        type: "boolean",
        flag: "--create-pull-request",
        default: true,
      }],
    },
    {
      id: "upgrade-target-repository",
      label: "Upgrade repository",
      icon: "download",
      command: "gh aw upgrade --repo {{repository}}",
      placement: "row",
      arguments: [{
        id: "create-pull-request",
        label: "Create pull request",
        type: "boolean",
        flag: "--create-pull-request",
        default: true,
      }],
    },
    {
      id: "create-agent-task",
      label: "Start agent task",
      icon: "copilot",
      command: "gh agent-task create --from-file -",
      placement: "row",
    },
    {
      id: "copy-package-command",
      label: "Copy add command",
      icon: "copy",
      command: "gh cao add octo/packages/demo@abc123",
      placement: "row",
      "copy-only": true,
    },
  ]));

  const preview = await startCanvasServer({
    siteRoot: root,
    catalogRoot: null,
    prepareSnapshot: async (destination) => {
      await writeCanvasSnapshot(destination);
    },

    cliQueueAdmission: async () => ({ concurrency: 4 }),
    approveCliAction: async () => true,
    executeCliAction: async ({ onOutput, signal, ...action }) => {
      assert.ok(signal instanceof AbortSignal);
      calls.push(action);
      onOutput({ stream: "stdout", data: "comp" });
      onOutput({ stream: "stdout", data: "iled\n" });
      return { ok: true, exitCode: 0, stdout: "compiled\n", stderr: "" };
    },
    allowMissingOrigin: true,
    workingDirectory: root,
    repository: "octo/example",
    port: 0,
  });
  try {
    const previewUrl = new URL(`${preview.url}/`);
    const origin = previewUrl.origin;
    const servedDashboard = await fetch(new URL("dashboard.json", previewUrl));
    assert.equal(servedDashboard.status, 200);
    assert.equal((await servedDashboard.json()).dashboard.repository, "octo/example");
    const response = await fetch(new URL("__cli_action", previewUrl), {
      method: "POST",
      headers: { "Content-Type": "application/json", Origin: origin },
      body: JSON.stringify({
        id: "compile-workflows",
        arguments: { "pre-releases": true },
      }),
    });
    assert.equal(response.status, 200);
    const events = (await response.text()).trim().split("\n").map((line) => JSON.parse(line));
    const outputEvents = events.filter((event) => event.type === "output");
    assert.equal(outputEvents.map((event) => event.data).join(""), "compiled\n");
    assert.ok(outputEvents.every((event) => event.stream === "stdout" && Number.isInteger(event.sequence)));
    assert.deepEqual(events.at(-1).result, { ok: true, exitCode: 0 });
    assert.equal(events.at(-1).command.status, "completed");
    assert.ok(events.at(-1).command.id);
    assert.deepEqual(calls, [{
      id: "compile-workflows",
      command: "gh aw compile --strict --pre-releases",
    }]);

    const upgrade = await fetch(new URL("__cli_action", previewUrl), {
      method: "POST",
      headers: { "Content-Type": "application/json", Origin: origin },
      body: JSON.stringify({
        id: "upgrade-repository",
        values: { repository: "octo/example" },
      }),
    });
    assert.equal(upgrade.status, 200);
    await upgrade.text();
    assert.deepEqual(calls.at(-1), {
      id: "upgrade-repository",
      command: "gh aw upgrade --repo octo/example --create-pull-request",
    });

    const templated = await fetch(new URL("__cli_action", previewUrl), {
      method: "POST",
      headers: { "Content-Type": "application/json", Origin: origin },
      body: JSON.stringify({
        id: "update-target-repository",
        values: { repository: "octo/example" },
      }),
    });
    assert.equal(templated.status, 200);
    await templated.text();
    assert.deepEqual(calls.at(-1), {
      id: "update-target-repository",
      command: "gh aw update --repo octo/example --create-pull-request",
    });

    const targetUpgrade = await fetch(new URL("__cli_action", previewUrl), {
      method: "POST",
      headers: { "Content-Type": "application/json", Origin: origin },
      body: JSON.stringify({
        id: "upgrade-target-repository",
        values: { repository: "octo/example" },
      }),
    });
    assert.equal(targetUpgrade.status, 200);
    await targetUpgrade.text();
    assert.deepEqual(calls.at(-1), {
      id: "upgrade-target-repository",
      command: "gh aw upgrade --repo octo/example --create-pull-request",
    });

    const prompt = "Investigate this failure.\n\nUntrusted context: {}";
    const agentTask = await fetch(new URL("__cli_action", previewUrl), {
      method: "POST",
      headers: { "Content-Type": "application/json", Origin: origin },
      body: JSON.stringify({
        id: "create-agent-task",
        arguments: {},
        input: prompt,
      }),
    });
    assert.equal(agentTask.status, 200);
    await agentTask.text();
    assert.deepEqual(calls.at(-1), {
      id: "create-agent-task",
      command: "gh agent-task create --from-file -",
      input: prompt,
    });

    const copyOnly = await fetch(new URL("__cli_action", previewUrl), {
      method: "POST",
      headers: { "Content-Type": "application/json", Origin: origin },
      body: JSON.stringify({ id: "copy-package-command" }),
    });
    assert.equal(copyOnly.status, 403);
    assert.deepEqual(await copyOnly.json(), { error: "CLI action is copy-only." });
    assert.equal(calls.length, 5);

    const rejectedInput = await fetch(new URL("__cli_action", previewUrl), {
      method: "POST",
      headers: { "Content-Type": "application/json", Origin: origin },
      body: JSON.stringify({
        id: "compile-workflows",
        arguments: {},
        input: prompt,
      }),
    });
    assert.equal(rejectedInput.status, 400);

    const unsafeTemplate = await fetch(new URL("__cli_action", previewUrl), {
      method: "POST",
      headers: { "Content-Type": "application/json", Origin: origin },
      body: JSON.stringify({
        id: "update-target-repository",
        values: { repository: "octo/example --force" },
      }),
    });
    assert.equal(unsafeTemplate.status, 400);
    assert.equal(calls.length, 5);

    const undeclared = await fetch(new URL("__cli_action", previewUrl), {
      method: "POST",
      headers: { "Content-Type": "application/json", Origin: origin },
      body: JSON.stringify({ id: "not-declared" }),
    });
    assert.equal(undeclared.status, 404);
    assert.equal(calls.length, 5);

    const invalidArgument = await fetch(new URL("__cli_action", previewUrl), {
      method: "POST",
      headers: { "Content-Type": "application/json", Origin: origin },
      body: JSON.stringify({
        id: "compile-workflows",
        arguments: { arbitrary: true },
      }),
    });
    assert.equal(invalidArgument.status, 400);
    assert.equal(calls.length, 5);
  } finally {
    await preview.close();
    await rm(root, { recursive: true, force: true });
  }
});

test("local dashboard server rejects paths outside its workspace", async () => {
  const root = await mkdtemp(path.join(tmpdir(), "dashboard-local-server-"));
  const workspace = path.join(root, "workspace");
  const siteRoot = path.join(workspace, "site");
  const outside = path.join(root, "outside");
  await mkdir(siteRoot, { recursive: true });
  await mkdir(outside);
  await writeFile(path.join(root, "dashboard.json"), dashboard("built-in"));
  await writeFile(path.join(siteRoot, "dashboard.json"), dashboard("built-in"));
  try {
    await assert.rejects(
      startCanvasServer({
        siteRoot: root,
        catalogRoot: null,
        workingDirectory: workspace,
      }),
      /paths must remain within the workspace/,
    );
    await writeFile(path.join(outside, "dashboard.json"), dashboard("outside"));
    await symlink(outside, path.join(workspace, "example"));
    await assert.rejects(
      startCanvasServer({
        siteRoot,
        catalogRoot: workspace,
        workingDirectory: workspace,
      }),
      /paths must remain within the workspace/,
    );
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test("local dashboard server uses a prepared snapshot without artifact or Pages discovery", {
  skip: process.platform === "win32",
}, async () => {
  const root = await mkdtemp(path.join(tmpdir(), "dashboard-local-server-"));
  const ghExecutable = path.join(root, "gh");
  const commands = [];
  const artifact = path.join(root, ".cao");
  await writeCanvasSnapshot(artifact);
  await writeFile(path.join(root, "index.html"), "<!doctype html><head></head><body>preview</body>");
  await writeFile(path.join(root, "dashboard.json"), dashboard("built-in", [{
    id: "update", command: "gh aw update --repo {{repository}} --pr-message '{{cao-repository}} {{cao-repository-url}} {{cao-url}}#page-maintenance'",
  }]));
  await writeFile(ghExecutable, `#!/bin/sh
[ "$(pwd -P)" = ${JSON.stringify(await realpath(root))} ] || exit 4
if [ "$1" = "api" ]; then
  if [ "$2" = "repos/github/gh-aw/releases/latest" ]; then
    [ ! -f ${JSON.stringify(path.join(root, "release-unavailable"))} ] || exit 5
    printf 'v0.92.0\\n'
    exit
  fi
  touch ${JSON.stringify(path.join(root, "unexpected-discovery"))}
  exit 2
fi
touch ${JSON.stringify(path.join(root, "unexpected-discovery"))}
exit 3
`);
  await chmod(ghExecutable, 0o755);

  const preview = await startCanvasServer({
    siteRoot: root,
    catalogRoot: null,
    repository: "acme/control",
    caoUrl: "https://ops.example.com/central/",
    approveCliAction: async () => true,
    cliQueueAdmission: async () => ({ concurrency: 1 }),
    executeCliAction: async ({ command }) => { commands.push(command); return { ok: true, exitCode: 0 }; },
    ghExecutable,
    workingDirectory: root,
    port: 0,
  });
  try {
    const html = await (await fetch(`${preview.url}/`)).text();
    assert.match(html, /<meta name="cao-url" content="https:\/\/ops\.example\.com\/central\/">/);
    const response = await fetch(`${preview.url}/__cli_action`, {
      method: "POST",
      headers: { "Content-Type": "application/json", Origin: new URL(preview.url).origin },
      body: JSON.stringify({ id: "update", values: { repository: "octo/target" } }),
    });
    assert.equal(response.status, 200);
    await response.text();
    assert.deepEqual(commands, [
      "gh aw update --repo octo/target --pr-message 'acme/control https://github.com/acme/control https://ops.example.com/central/#page-maintenance'",
    ]);
    for (const pathname of ["gh-aw-logs-runs/logs-1.jsonl", "payload-hashes.json", "inventory-sources.json"]) {
      assert.equal((await fetch(`${preview.url}/${pathname}`)).status, 404);
    }
    const executionOptions = {
      siteRoot: root, catalogRoot: null, repository: "acme/control", approveCliAction: async () => true,
      ghExecutable, workingDirectory: root, port: 0,
      cliQueueAdmission: async () => ({ concurrency: 1 }),
      executeCliAction: async () => ({ ok: true, exitCode: 0 }),
    };
    const missing = await startCanvasServer({
      ...executionOptions, cliQueuePath: path.join(root, ".cao", "missing.sqlite"),
    });
    try {
      const missingHtml = await (await fetch(`${missing.url}/`)).text();
      assert.doesNotMatch(missingHtml, /<meta name="cao-url"/);
      const rejected = await fetch(`${missing.url}/__cli_commands`, {
        method: "POST",
        headers: { "Content-Type": "application/json", Origin: new URL(missing.url).origin },
        body: JSON.stringify({ id: "update", values: { repository: "octo/target" } }),
      });
      assert.equal(rejected.status, 400);
    } finally { await missing.close(); }
    const hosted = await startCanvasServer({
      ...executionOptions, caoUrl: "https://custom.example.com/ops",
      cliQueuePath: path.join(root, ".cao", "override.sqlite"),
    });
    try {
      const hostedHtml = await (await fetch(`${hosted.url}/`)).text();
      assert.match(hostedHtml, /<meta name="cao-url" content="https:\/\/custom\.example\.com\/ops\/">/);
    } finally { await hosted.close(); }
    await assert.rejects(readFile(path.join(root, "unexpected-discovery")), { code: "ENOENT" });
    await rm(path.join(artifact, "payload-hashes.json"));
    await assert.rejects(startCanvasServer(executionOptions), /Prepare a checksum-verified operational snapshot/);
  } finally {
    await preview.close();
    await rm(root, { recursive: true, force: true });
  }
});
