import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { createServer } from "node:http";
import test from "node:test";
import { createCliActionApi, resolveQueuedCliAction } from "../../dashboard/cli-action-api.mjs";
import { parseDashboardCommand } from "../../dashboard/cli-actions.mjs";
import { cliActionTemplateContext } from "../../dashboard/site/src/cli-action-template.js";

const dashboard = { dashboard: { "cli-actions": [
  { id: "compile", command: "gh aw compile", arguments: [{ id: "strict", flag: "--strict", default: true }] },
  { id: "update", command: "gh cao update --repo {repository}" },
  { id: "copy", command: "gh aw compile", "copy-only": true },
] } };

test("queue admission resolves only declared actions and validated arguments", () => {
  assert.equal(resolveQueuedCliAction(dashboard, { id: "compile" }).command, "gh aw compile --strict");
  assert.equal(resolveQueuedCliAction(dashboard, { id: "compile", arguments: { strict: false } }).command, "gh aw compile");
  for (const payload of [
    { id: "compile", command: "sh -c whoami" },
    { id: "compile", arguments: { unsafe: true } },
    { id: "compile", input: "unexpected" },
    { id: "update", values: { repository: "octo/repo; whoami" } },
  ]) assert.throws(() => resolveQueuedCliAction(dashboard, payload), { status: 400 });
  assert.throws(() => resolveQueuedCliAction(dashboard, { id: "missing" }), { status: 404 });
  assert.throws(() => resolveQueuedCliAction(dashboard, { id: "copy" }), { status: 403 });
});

test("built-in update and upgrade actions carry CAO attribution as one PR message argument", () => {
  const document = JSON.parse(readFileSync(new URL("../../dashboard/site/dashboard.json", import.meta.url), "utf8"));
  const context = cliActionTemplateContext({ repository: "acme/control", caoUrl: "https://ops.example.com/central/" });
  const cases = [
    ...["update-repository", "update-campaign"].map((id) => ({
      id,
      values: { "campaign-name": "Activity", "campaign-source": "githubnext/gh-cao/activity" },
      prefix: ["gh", "aw", "update", "https://github.com/githubnext/gh-cao/activity", "--repo", "acme/control"],
      message: "This pull request was requested through [Central Agentic Ops (CAO)](https://github.com/acme/control) to update the Activity campaign package from its retained upstream source. Return to the [CAO dashboard](https://ops.example.com/central/#page-maintenance-campaigns).",
    })),
    {
      id: "update-workflow-source",
      values: { "workflow-name": "Review", "workflow-source": "acme/workflows/review" },
      prefix: ["gh", "aw", "update", "https://github.com/acme/workflows/review", "--repo", "acme/control"],
      message: "This pull request was requested through [Central Agentic Ops (CAO)](https://github.com/acme/control) to update the Review standalone workflow from its retained upstream source. Return to the [CAO dashboard](https://ops.example.com/central/#page-maintenance-campaigns).",
    },
    ...["upgrade-repository", "upgrade-target-repository"].map((id) => ({
      id,
      values: { repository: "octo/example" },
      prefix: ["gh", "aw", "upgrade", "--repo", "octo/example"],
      message: "This pull request was requested through [Central Agentic Ops (CAO)](https://github.com/acme/control) to upgrade GitHub Agentic Workflows in octo/example, refreshing workflow tooling, action versions, and generated lock files. Return to the [CAO dashboard](https://ops.example.com/central/#page-maintenance).",
    })),
  ];
  for (const { id, values, prefix, message } of cases) {
    for (const createPr of [true, false]) {
      const invocation = resolveQueuedCliAction(document, {
        id, values, arguments: { "create-pull-request": createPr },
      }, context);
      const tokens = parseDashboardCommand(invocation.command);
      assert.deepEqual(tokens, [
        ...prefix,
        "--pr-message", message, ...(createPr ? ["--create-pull-request"] : []),
      ]);
    }
  }
});

test("queue resolution rejects browser overrides of host identity and missing CAO context", () => {
  const document = { dashboard: { "cli-actions": [{
    id: "upgrade", command: "gh aw upgrade --repo {{repository}} --pr-message '{{cao-url}}'",
  }] } };
  const context = { "cao-url": "https://ops.example.com/central/" };
  for (const field of ["cao-url", "cao-repository", "cao-repository-url"]) {
    assert.throws(() => resolveQueuedCliAction(document, {
      id: "upgrade", values: { repository: "octo/target", [field]: "https://attacker.example/" },
    }, context), { status: 400 });
  }
  assert.throws(() => resolveQueuedCliAction(document, {
    id: "upgrade", values: { repository: "octo/target" },
  }), { status: 400 });
});

async function fixture(queue, approve = async () => true, { getDashboard = () => dashboard, getTemplateContext = () => ({}) } = {}) {
  const handler = createCliActionApi({
    queue, getDashboard, getTemplateContext, approve,
    isAllowedOrigin: (origin) => origin === "http://trusted",
    readJsonRequest: async (request) => {
      let body = "";
      for await (const chunk of request) body += chunk;
      return JSON.parse(body);
    },
    sendJson: (response, status, value) => {
      response.writeHead(status, { "Content-Type": "application/json" });
      response.end(JSON.stringify(value));
    },
  });
  const server = createServer(async (request, response) => {
    const url = new URL(request.url, "http://localhost");
    if (!await handler(request, response, url.pathname, url)) response.writeHead(404).end();
  });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  return {
    url: `http://127.0.0.1:${server.address().port}`,
    close: () => { server.closeAllConnections(); return new Promise((resolve) => server.close(resolve)); },
  };
}

const headers = { "Content-Type": "application/json", Origin: "http://trusted" };

test("queue API rejects cross-origin mutations and preserves trusted approval", async () => {
  let enqueued = 0;
  const queue = { enqueue: () => { enqueued++; } };
  const server = await fixture(queue, async () => false);
  try {
    const post = (customHeaders) => fetch(`${server.url}/__cli_commands`, {
      method: "POST", headers: customHeaders, body: JSON.stringify({ id: "compile" }),
    });
    assert.equal((await post({ ...headers, Origin: "http://attacker" })).status, 403);
    assert.equal((await post({ "Content-Type": "application/json" })).status, 403);
    assert.equal((await post(headers)).status, 403);
    assert.equal(enqueued, 0);
  } finally { await server.close(); }
});

test("queue API durably enqueues with an idempotency key and validates pagination", async () => {
  const calls = [];
  const server = await fixture({
    enqueue: (invocation, key) => { calls.push({ invocation, key }); return { id: "command-1", status: "queued" }; },
    list: (options) => ({ commands: [], total: 0, running: 0, options }),
  });
  try {
    const response = await fetch(`${server.url}/__cli_commands`, {
      method: "POST", headers: { ...headers, "Idempotency-Key": "update-batch-1" },
      body: JSON.stringify({ id: "compile" }),
    });
    assert.equal(response.status, 202);
    assert.equal((await response.json()).id, "command-1");
    assert.equal(calls[0].key, "update-batch-1");
    assert.equal(calls[0].invocation.actionId, "compile");
    for (const query of ["limit=-1", "offset=1e9", "limit=301", "offset=nope"]) {
      assert.equal((await fetch(`${server.url}/__cli_commands?${query}`)).status, 400);
    }
  } finally { await server.close(); }
});

test("output replay follows a persistent cursor and emits terminal state", async () => {
  let unsubscribed = 0;
  const chunks = [
    { sequence: 1, stream: "stdout", data: "earlier\n" },
    { sequence: 2, stream: "stderr", data: "last\n" },
  ];
  const server = await fixture({
    detail: () => ({ id: "command-1", status: "failed", exitCode: 7 }),
    output: (_id, after, limit) => chunks.filter((chunk) => chunk.sequence > after).slice(0, limit),
    subscribe: () => () => { unsubscribed++; },
  });
  try {
    const response = await fetch(`${server.url}/__cli_commands/command-1/output?after=1`);
    const events = (await response.text()).trim().split("\n").map((line) => JSON.parse(line));
    assert.deepEqual(events[0], { type: "output", ...chunks[1] });
    assert.equal(events.length, 2);
    assert.equal(events[1].result.ok, false);
    assert.equal(events[1].result.exitCode, 7);
    assert.equal(unsubscribed, 1);
  } finally { await server.close(); }
});

test("output completion includes the durable enterprise pull request result", async () => {
  const pullRequestUrl = "https://ghe.example.test:8443/github/team/project/pull/42";
  const server = await fixture({
    detail: () => ({
      id: "command-1", status: "completed", exitCode: 0,
      result: { ok: true, exitCode: 0, pullRequestUrl },
    }),
    output: () => [],
    subscribe: () => () => {},
  });
  try {
    const response = await fetch(`${server.url}/__cli_commands/command-1/output`);
    assert.deepEqual(JSON.parse(await response.text()).result, { ok: true, exitCode: 0, pullRequestUrl });
  } finally { await server.close(); }
});

test("history polling excludes stdin and output bodies from summary rows", async () => {
  const server = await fixture({
    list: () => ({
      commands: [{
        id: "command-1", actionId: "compile", status: "completed", input: "approved prompt",
        stdout: "retained output", stderr: "", arguments: {}, values: {
          repository: "octo/repo", campaign: "maintenance", "package-source": "owner/catalog/pkg",
          unrelated: "not part of the summary",
        },
      }],
      total: 300, running: 4, revision: 7,
    }),
  });
  try {
    const response = await fetch(`${server.url}/__cli_commands`);
    assert.deepEqual(await response.json(), {
      commands: [{
        id: "command-1", actionId: "compile", status: "completed",
        repository: "octo/repo", campaign: "maintenance", "package-source": "owner/catalog/pkg",
      }],
      total: 300, running: 4, revision: 7,
    });
  } finally { await server.close(); }
});

test("retry revalidates the current declaration and approval before creating another entry", async () => {
  let retries = 0;
  const server = await fixture({
    detail: () => ({ status: "failed", actionId: "compile", command: "gh aw compile --old", arguments: {}, values: {} }),
    retry: () => { retries++; },
  });
  try {
    const response = await fetch(`${server.url}/__cli_commands/command-1/retry`, {
      method: "POST", headers, body: "{}",
    });
    assert.equal(response.status, 409);
    assert.equal(retries, 0);
  } finally { await server.close(); }
});

test("retry re-resolves host context and requires the same approved command", async () => {
  const document = { dashboard: { "cli-actions": [{
    id: "update", command: "gh aw update --pr-message '{{cao-url}}'",
  }] } };
  let url = "https://ops.example.com/central/";
  let retries = 0;
  const server = await fixture({
    detail: () => ({
      status: "failed", actionId: "update",
      command: "gh aw update --pr-message 'https://ops.example.com/central/'", arguments: {}, values: {},
    }),
    retry: (id) => { retries++; return { id: "new-command", status: "queued", retryOf: id }; },
  }, async () => true, {
    getDashboard: () => document, getTemplateContext: () => ({ "cao-url": url }),
  });
  try {
    const retry = () => fetch(`${server.url}/__cli_commands/command-1/retry`, {
      method: "POST", headers, body: "{}",
    });
    assert.equal((await retry()).status, 202);
    url = "https://other.example.com/central/";
    assert.equal((await retry()).status, 409);
    assert.equal(retries, 1);
  } finally { await server.close(); }
});

test("manual retries create entries only for failed or timed-out commands", async () => {
  let status = "failed";
  const retries = [];
  const server = await fixture({
    detail: () => ({
      status, actionId: "compile", command: "gh aw compile --strict", arguments: {}, values: {},
    }),
    retry: (id, key) => { retries.push({ id, key }); return { id: "new-command", status: "queued", retryOf: id }; },
  });
  try {
    for (status of ["queued", "running", "completed", "canceled", "failed", "timed-out"]) {
      const response = await fetch(`${server.url}/__cli_commands/command-1/retry`, {
        method: "POST", headers, body: JSON.stringify({ idempotencyKey: `retry-${status}` }),
      });
      assert.equal(response.status, status === "failed" || status === "timed-out" ? 202 : 409);
    }
    assert.deepEqual(retries, [
      { id: "command-1", key: "retry-failed" },
      { id: "command-1", key: "retry-timed-out" },
    ]);
  } finally { await server.close(); }
});

test("online queue endpoints are no-ops", async () => {
  const server = await fixture(undefined);
  try {
    assert.equal((await fetch(`${server.url}/__cli_commands`)).status, 404);
    assert.equal((await fetch(`${server.url}/__cli_action`, { method: "POST", headers, body: "{}" })).status, 404);
  } finally { await server.close(); }
});

test("retry cannot reuse an enqueue idempotency key to return its failed original", async () => {
  const original = {
    id: "command-1", status: "failed", actionId: "compile",
    command: "gh aw compile --strict", arguments: {}, values: {}, retryOf: null,
  };
  const server = await fixture({ detail: () => original, retry: () => original });
  try {
    const response = await fetch(`${server.url}/__cli_commands/command-1/retry`, {
      method: "POST", headers, body: JSON.stringify({ idempotencyKey: "original-enqueue-key" }),
    });
    assert.equal(response.status, 409);
    assert.match((await response.json()).error, /idempotency key conflicts/);
  } finally { await server.close(); }
});

test("unknown command IDs do not open a stream or permit lifecycle operations", async () => {
  const server = await fixture({ detail: () => null });
  try {
    for (const suffix of ["", "/output"]) {
      const response = await fetch(`${server.url}/__cli_commands/missing${suffix}`);
      assert.equal(response.status, 404);
      assert.match((await response.json()).error, /not found/);
    }
    const response = await fetch(`${server.url}/__cli_commands/missing/cancel`, {
      method: "POST", headers, body: "{}",
    });
    assert.equal(response.status, 404);
  } finally { await server.close(); }
});

test("detaching an output stream unsubscribes without canceling execution", async () => {
  let cancelCount = 0;
  let unsubscribeCount = 0;
  const server = await fixture({
    detail: () => ({ id: "command-1", status: "running", exitCode: null }),
    output: () => [{ sequence: 1, stream: "stdout", data: "running\n" }],
    subscribe: () => () => { unsubscribeCount++; },
    cancel: () => { cancelCount++; },
  });
  try {
    const controller = new AbortController();
    const response = await fetch(`${server.url}/__cli_commands/command-1/output`, { signal: controller.signal });
    const reader = response.body.getReader();
    assert.match(new TextDecoder().decode((await reader.read()).value), /running/);
    controller.abort();
    for (let attempt = 0; attempt < 30 && !unsubscribeCount; attempt++) {
      await new Promise((resolve) => setTimeout(resolve, 5));
    }
    assert.equal(unsubscribeCount, 1);
    assert.equal(cancelCount, 0);
  } finally { await server.close(); }
});
