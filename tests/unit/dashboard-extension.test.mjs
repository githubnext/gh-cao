import assert from "node:assert/strict";
import { test } from "node:test";
import { fileURLToPath } from "node:url";
import { Canvas, CanvasError } from "@github/copilot-sdk/extension";
import { createDashboardExtension } from "../../com.github.copilot/extensions/cao-dashboard/dashboard-extension.mjs";

const workingDirectory = fileURLToPath(new URL("../../", import.meta.url));
const context = (instanceId, directory) => ({
  sessionId: "test-session",
  extensionId: "plugin:central-agentic-ops:cao-dashboard",
  canvasId: "cao-dashboard",
  instanceId,
  ...(directory ? { session: { workingDirectory: directory } } : {}),
});

test("dashboard extension registers executable tools and an SDK canvas", async () => {
  const { config } = createDashboardExtension({ workingDirectory });
  const canvas = config.canvases[0];
  assert.ok(canvas instanceof Canvas);
  assert.equal(canvas.declaration.id, "cao-dashboard");
  assert.equal(canvas.declaration.inputSchema.additionalProperties, false);
  assert.equal(canvas.declaration.inputSchema.properties.dataBackend, undefined);
  assert.deepEqual(config.requestedEnvironmentVariables, ["GH_TOKEN", "GITHUB_TOKEN"]);

  const queryTool = config.tools.find(({ name }) => name === "cao_dashboard_execute_query");
  const output = JSON.parse(await queryTool.handler({
    queries: [{ name: "failed", from: "runs", filter: { predicates: [{ field: "conclusion", equals: "failure" }] } }],
    sources: { runs: [{ conclusion: "failure" }, { conclusion: "success" }] },
  }));
  assert.deepEqual(output.failed.rows, [{ conclusion: "failure" }]);

  const specTool = config.tools.find(({ name }) => name === "cao_dashboard_read_data_specification");
  const spec = JSON.parse(await specTool.handler({ startLine: 1, endLine: 5 }));
  assert.equal(spec.startLine, 1);
  assert.equal(spec.endLine, 5);
});

test("dashboard canvas shares concurrent opens and closes pending startup", async () => {
  let starts = 0;
  let closes = 0;
  let finish;
  const { config } = createDashboardExtension({
    startPreview: () => {
      starts += 1;
      return new Promise((resolve) => { finish = resolve; });
    },
  });
  const canvas = config.canvases[0];
  const first = canvas.open(context("preview"));
  const second = canvas.open(context("preview"));
  const closing = canvas.onClose(context("preview"));
  assert.equal(starts, 1);
  finish({ url: "http://127.0.0.1:1234/preview", close: async () => { closes += 1; } });
  assert.deepEqual(await first, await second);
  await closing;
  await canvas.onClose(context("preview"));
  assert.equal(closes, 1);
});

test("dashboard canvas uses hook context and retains each preview's working directory", async () => {
  const previews = [];
  const { config, closePreviews } = createDashboardExtension({
    workingDirectory: "/initial",
    startPreview: async (options) => {
      previews.push(options);
      return { url: `http://127.0.0.1:1234/${previews.length}`, close: async () => {} };
    },
  });
  const canvas = config.canvases[0];
  const guidance = await config.hooks.onSessionStart({ workingDirectory: "/first" });
  assert.match(guidance.additionalContext, /cao_dashboard_execute_query/);
  await canvas.open({ ...context("first"), input: { repository: "acme/control" } });
  await config.hooks.onUserPromptSubmitted({ workingDirectory: "/second" });
  await canvas.open(context("second"));
  await canvas.open(context("third", "/third"));
  assert.deepEqual(previews.map(({ workingDirectory }) => workingDirectory), ["/first", "/second", "/third"]);
  assert.equal(previews[0].repository, "acme/control");
  assert.deepEqual(Object.keys(previews[0]), ["workingDirectory", "repository"]);
  await closePreviews();
});

test("dashboard canvas reports startup errors and allows a fresh retry", async () => {
  let starts = 0;
  const { config, closePreviews } = createDashboardExtension({
    startPreview: async () => {
      starts += 1;
      if (starts === 1) throw new Error("artifact unavailable");
      return { url: "http://127.0.0.1:1234/preview", close: async () => {} };
    },
  });

  const canvas = config.canvases[0];
  await assert.rejects(canvas.open(context("preview")), (error) =>
    error instanceof CanvasError
    && error.code === "cao_dashboard_preview_unavailable"
    && error.message === "artifact unavailable");
  assert.equal((await canvas.open(context("preview"))).url, "http://127.0.0.1:1234/preview");
  assert.equal(starts, 2);
  await closePreviews();
});

test("dashboard extension waits for all preview cleanup and reports failures", async () => {
  const closed = [];
  const { config, closePreviews } = createDashboardExtension({
    startPreview: async ({ repository }) => ({
      url: "http://127.0.0.1:1234/preview",
      close: async () => {
        closed.push(repository);
        if (repository === "acme/first") throw new Error("cleanup failed");
      },
    }),
  });
  await config.canvases[0].open({ ...context("first"), input: { repository: "acme/first" } });
  await config.canvases[0].open({ ...context("second"), input: { repository: "acme/second" } });
  await assert.rejects(config.hooks.onSessionEnd({ reason: "user_exit" }), (error) =>
    error instanceof AggregateError && error.errors[0].message === "cleanup failed");
  assert.deepEqual(closed, ["acme/first", "acme/second"]);
  await closePreviews();
  assert.equal(closed.length, 2);
});

test("dashboard previews survive completed and interrupted agent turns", async () => {
  let starts = 0;
  let closes = 0;
  const { config, closePreviews } = createDashboardExtension({
    startPreview: async () => {
      starts += 1;
      return { url: `http://127.0.0.1:1234/${starts}`, close: async () => { closes += 1; } };
    },
  });
  const canvas = config.canvases[0];
  const initial = await canvas.open(context("preview"));
  try {
    for (const reason of ["complete", "error", "abort", "timeout"]) {
      await config.hooks.onSessionEnd({ reason });
      assert.equal(closes, 0, `${reason} must not stop an open preview`);
      assert.deepEqual(await canvas.open(context("preview")), initial);
    }
    assert.equal(starts, 1);
    await config.hooks.onUserPromptSubmitted({ workingDirectory });
    await canvas.open(context("another-preview"));
    assert.equal(starts, 2);
    await canvas.onClose(context("preview"));
    assert.equal(closes, 1);
  } finally {
    await closePreviews();
  }
  assert.equal(closes, 2);
});

test("explicit session exit closes previews and rejects subsequent opens", async () => {
  let closes = 0;
  const { config } = createDashboardExtension({
    startPreview: async () => ({
      url: "http://127.0.0.1:1234/preview",
      close: async () => { closes += 1; },
    }),
  });
  await config.canvases[0].open(context("preview"));
  await config.hooks.onSessionEnd({ reason: "user_exit" });
  assert.equal(closes, 1);
  await assert.rejects(config.canvases[0].open(context("new-preview")), /shutting down/);
});

test("concurrent extension shutdown waits for the same cleanup", async () => {
  let finish;
  let closes = 0;
  const { config, closePreviews } = createDashboardExtension({
    startPreview: async () => ({
      url: "http://127.0.0.1:1234/preview",
      close: () => {
        closes += 1;
        return new Promise((resolve) => { finish = resolve; });
      },
    }),
  });
  await config.canvases[0].open(context("preview"));
  const first = closePreviews();
  const second = closePreviews();
  assert.equal(first, second);
  await Promise.resolve();
  assert.equal(closes, 1);
  finish();
  await second;
});

test("extension shutdown waits for a canvas close already in progress", async () => {
  let finish;
  const { config, closePreviews } = createDashboardExtension({
    startPreview: async () => ({
      url: "http://127.0.0.1:1234/preview",
      close: () => new Promise((resolve) => { finish = resolve; }),
    }),
  });
  await config.canvases[0].open(context("preview"));
  const closingCanvas = config.canvases[0].onClose(context("preview"));
  const shutdown = closePreviews();
  let finished = false;
  void shutdown.then(() => { finished = true; });
  await Promise.resolve();
  assert.equal(finished, false);
  finish();
  await Promise.all([closingCanvas, shutdown]);
  assert.equal(finished, true);
});

test("shutdown rejects new opens while cleanup is pending and after it completes", async () => {
  let finish;
  let starts = 0;
  const { config, closePreviews } = createDashboardExtension({
    startPreview: async () => {
      starts += 1;
      return {
        url: "http://127.0.0.1:1234/preview",
        close: () => new Promise((resolve) => { finish = resolve; }),
      };
    },
  });
  await config.canvases[0].open(context("first"));
  const closing = closePreviews();
  await Promise.resolve();
  await assert.rejects(config.canvases[0].open(context("second")), /shutting down/);
  assert.equal(starts, 1);
  finish();
  await closing;
  await assert.rejects(config.canvases[0].open(context("third")), /shutting down/);
  assert.equal(starts, 1);
});
