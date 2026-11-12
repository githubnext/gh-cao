import assert from "node:assert/strict";
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { startCanvasServer } from "../helpers/canvas-snapshot.mjs";
import { writeCanvasSnapshot } from "../helpers/canvas-snapshot.mjs";
import { maximumCliActionInputCharacters, maximumCliActionRequestBytes } from "../../dashboard/cli-action-contract.mjs";
import { executeDashboardCommand } from "../../dashboard/cli-actions.mjs";
import { approveDashboardCommand } from "../../com.github.copilot/extensions/cao-dashboard/approval.mjs";

async function fixture(options) {
  const root = await mkdtemp(join(tmpdir(), "cao-cli-approval-"));
  await writeFile(join(root, "index.html"), "<!doctype html><body>preview</body>");
  await writeFile(join(root, "dashboard.json"), JSON.stringify({
    "language-version": "0.1.0",
    dashboard: {
      id: "preview",
      title: "Preview",
      "cli-actions": [
        { id: "compile", label: "Compile", icon: "play", command: "gh aw compile" },
        { id: "create-task", label: "Start task", icon: "copilot", command: "gh agent-task create --from-file -" },
        { id: "add-repository", label: "Add repository", icon: "plus", command: "gh cao repos add {{repository}}",
          selector: { label: "Repository", "debounce-ms": 300,
            query: { command: "gh search repos", fields: ["fullName", "description"], limit: 20 },
            mapping: { value: "fullName", label: "fullName", description: "description", target: "repository" } } },
      ],
      navigation: [{ label: "Preview", pages: ["preview"] }],
      pages: [{
        id: "preview", title: "Preview", kind: "custom",
        views: [{ id: "summary", title: "Summary", mark: "element", element: "summary-grid", data: { sources: ["repositories"] } }],
      }],
    },
  }));
  try {
    const preview = await startCanvasServer({
      workingDirectory: root, siteRoot: root, catalogRoot: null, port: 0,
      cliQueueAdmission: async () => ({ concurrency: 4 }),
      output: () => {}, requestOutput: () => {}, traceOutput: () => {},
      prepareSnapshot: async (destination) => { await writeCanvasSnapshot(destination); },
      ...options,
    });
    const base = new URL(`${preview.url}/`);
    return {
      preview,
      post: (payload) => fetch(new URL("__cli_action", base), {
        method: "POST",
        headers: { "Content-Type": "application/json", Origin: base.origin },
        body: JSON.stringify(payload),
      }),
      lookup: (payload, origin = base.origin) => fetch(new URL("__dialog_selector", base), {
        method: "POST", headers: { "Content-Type": "application/json", Origin: origin },
        body: JSON.stringify(payload),
      }),
      close: async () => { await preview.close(); await rm(root, { recursive: true, force: true }); },
    };
  } catch (error) {
    await rm(root, { recursive: true, force: true });
    throw error;
  }
}

test("canvas mode cannot start without a trusted approval adapter", async () => {
  await assert.rejects(fixture({ executeCliAction: async () => {} }), /requires trusted CLI action approval/);
});

test("canvas-reviewed actions execute through command validation without host elicitation", async () => {
  const executions = [];
  const testCase = await fixture({
    approveCliAction: approveDashboardCommand,
    executeCliAction: async ({ command }) => { executions.push(command); return { ok: true }; },
  });
  try {
    const response = await testCase.post({ id: "compile" });
    assert.equal(response.status, 200);
    await response.text();
    assert.deepEqual(executions, ["gh aw compile"]);
    assert.equal((await testCase.post({ id: "undeclared-action" })).status, 404);
    assert.equal(executions.length, 1);
  } finally { await testCase.close(); }
});

test("the cli-actions protocol queues and streams native gh cao through the configured GitHub CLI", async () => {
  const ghExecutable = "/trusted tools/custom-gh";
  const preparations = [];
  const executions = [];
  const testCase = await fixture({
    approveCliAction: approveDashboardCommand,
    executeCliAction: (invocation) => executeDashboardCommand({
      ...invocation, ghExecutable, workingDirectory: process.cwd(), githubToken: "test-fixture",
      execute: async (executable, args, options) => {
        assert.equal(executable, ghExecutable);
        assert.equal(options.env.GH_TOKEN, "test-fixture");
        preparations.push(args);
        if (args[0] === "aw") {
          assert.deepEqual(args, ["aw", "--help"]);
          return { stdout: "usage", stderr: "" };
        }
        assert.deepEqual(args, ["api", "user"]);
        return { stdout: JSON.stringify({ login: "octocat", id: 1 }), stderr: "" };
      },
      streamCommand: async (options) => {
        executions.push(options.args);
        assert.equal(options.ghExecutable, ghExecutable);
        assert.equal(options.workingDirectory, process.cwd());
        assert.equal(options.githubToken, "test-fixture");
        assert.equal(options.gitIdentity.name, "octocat");
        assert.equal(options.terminal, true);
        await options.onOutput({ stream: "stdout", data: "Repository staged.\n" });
        return { ok: true, exitCode: 0 };
      },
    }),
  });
  try {
    const response = await testCase.post({ id: "add-repository", values: { repository: "octo/repo" } });
    assert.equal(response.status, 200);
    const events = (await response.text()).trim().split("\n").map((line) => JSON.parse(line));
    assert.deepEqual(preparations, [["aw", "--help"], ["api", "user"]]);
    assert.deepEqual(executions, [["cao", "repos", "add", "octo/repo"]]);
    assert.ok(events.some((event) => event.type === "output" && event.data === "Repository staged.\n"));
    assert.deepEqual(events.at(-1).result, { ok: true, exitCode: 0 });
    assert.equal((await testCase.post({
      id: "add-repository", values: { repository: "octo/repo" }, command: "gh cao campaigns enable other",
    })).status, 400);
    assert.equal(executions.length, 1);
  } finally { await testCase.close(); }
});

test("selector lookup accepts only declared action identity and bounded text in canvas mode", async () => {
  const calls = [];
  const testCase = await fixture({
    approveCliAction: async () => { assert.fail("Search must not ask for mutation approval."); },
    executeCliAction: async () => { assert.fail("Search must not execute an action."); },
    lookupSelector: async (action, text) => {
      calls.push({ id: action.id, text });
      return [{ value: "octo/repo", label: "octo/repo", description: "Example" }];
    },
  });
  try {
    const response = await testCase.lookup({ id: "add-repository", text: "octo" });
    assert.equal(response.status, 200);
    assert.deepEqual(await response.json(), { options: [{ value: "octo/repo", label: "octo/repo", description: "Example" }] });
    assert.equal((await testCase.lookup({ id: "compile", text: "octo" })).status, 404);
    assert.equal((await testCase.lookup({ id: "add-repository", text: "x" })).status, 400);
    assert.equal((await testCase.lookup({ id: "add-repository", text: "octo", command: "gh api" })).status, 400);
    assert.equal((await testCase.lookup({ id: "add-repository", text: "octo" }, "https://untrusted.example")).status, 403);
    assert.deepEqual(calls, [{ id: "add-repository", text: "octo" }]);
  } finally { await testCase.close(); }
});

test("selected repository is bound to fresh trusted approval before cao add runs", async () => {
  const approvals = [];
  const executions = [];
  const testCase = await fixture({
    approveCliAction: async (request) => { approvals.push(request); return true; },
    executeCliAction: async (request) => { executions.push(request.command); return { ok: true }; },
  });
  try {
    const response = await testCase.post({ id: "add-repository", values: { repository: "octo/repo" } });
    assert.equal(response.status, 200);
    await response.text();
    assert.equal(approvals[0].command, "gh cao repos add octo/repo");
    assert.deepEqual(executions, ["gh cao repos add octo/repo"]);
    assert.equal((await testCase.post({ id: "add-repository", values: { repository: "octo/repo;evil" } })).status, 400);
    assert.equal(executions.length, 1);
  } finally { await testCase.close(); }
});

test("direct same-origin requests cannot execute without strict trusted approval", async () => {
  for (const approval of [false, undefined, "true"]) {
    let executions = 0;
    const testCase = await fixture({
      approveCliAction: async () => approval,
      executeCliAction: async () => { executions += 1; },
    });
    try {
      const response = await testCase.post({ id: "compile" });
      assert.equal(response.status, 403);
      assert.match((await response.json()).error, /not approved/);
      assert.equal(executions, 0);
    } finally { await testCase.close(); }
  }
});

test("unavailable approval is explicit and does not execute commands", async () => {
  let executions = 0;
  const testCase = await fixture({
    approveCliAction: async () => { throw new Error("Trusted approval is unavailable."); },
    executeCliAction: async () => { executions += 1; },
  });
  try {
    const response = await testCase.post({ id: "compile" });
    assert.equal(response.status, 503);
    assert.match((await response.json()).error, /Trusted approval is unavailable/);
    assert.equal(executions, 0);
  } finally { await testCase.close(); }
});

test("every command has a fresh bound approval and concurrent requests are rejected", { timeout: 10_000 }, async () => {
  let approve;
  let entered;
  const waiting = new Promise((resolve) => { entered = resolve; });
  let secondEntered;
  const secondWaiting = new Promise((resolve) => { secondEntered = resolve; });
  const approvals = [];
  const executions = [];
  const testCase = await fixture({
    approveCliAction: async (request) => {
      approvals.push(request);
      assert.ok(Object.isFrozen(request));
      entered();
      if (approvals.length === 2) secondEntered();
      return new Promise((resolve) => { approve = resolve; });
    },
    executeCliAction: async ({ onOutput, signal, ...request }) => {
      assert.ok(signal instanceof AbortSignal);
      assert.equal(signal.aborted, false);
      executions.push(request);
      return { ok: true };
    },
  });
  try {
    const first = testCase.post({ id: "create-task", input: "exact approved input" });
    await waiting;
    const concurrent = await testCase.post({ id: "compile" });
    assert.equal(concurrent.status, 409);
    assert.equal(executions.length, 0);
    approve(true);
    const response = await first;
    assert.equal(response.status, 200);
    await response.text();
    assert.deepEqual(executions[0], approvals[0]);
    const second = testCase.post({ id: "compile" });
    await secondWaiting;
    approve(false);
    assert.equal((await second).status, 403);
    assert.equal(executions.length, 1);
  } finally { approve?.(false); await testCase.close(); }
});

test("large valid prompts, including worst-case JSON escaping, reach approval intact", async () => {
  const received = [];
  const testCase = await fixture({
    approveCliAction: async () => true,
    executeCliAction: async ({ input }) => { received.push(input); return { ok: true }; },
  });
  try {
    for (const prompt of ["x".repeat(23_898), "\u0001".repeat(maximumCliActionInputCharacters)]) {
      const response = await testCase.post({ id: "create-task", input: prompt });
      assert.equal(response.status, 200);
      await response.text();
      assert.equal(received.at(-1), prompt);
    }
    assert.equal((await testCase.post({ id: "create-task", input: "x".repeat(maximumCliActionInputCharacters + 1) })).status, 400);
    assert.equal((await testCase.post({ id: "create-task", input: "x".repeat(maximumCliActionRequestBytes + 1) })).status, 413);
    assert.equal(received.length, 2);
  } finally { await testCase.close(); }
});

test("closing a preview cancels the execution authority of pending approval", { timeout: 10_000 }, async () => {
  let approve;
  let entered;
  const waiting = new Promise((resolve) => { entered = resolve; });
  let executions = 0;
  const testCase = await fixture({
    approveCliAction: async () => {
      entered();
      return new Promise((resolve) => { approve = resolve; });
    },
    executeCliAction: async () => { executions += 1; },
  });
  try {
    const request = testCase.post({ id: "compile" }).catch((error) => error);
    await waiting;
    await testCase.preview.close();
    approve(true);
    const response = await request;
    assert.ok(response instanceof Error);
    await new Promise((resolve) => setImmediate(resolve));
    assert.equal(executions, 0);
  } finally { approve?.(false); await testCase.close(); }
});
