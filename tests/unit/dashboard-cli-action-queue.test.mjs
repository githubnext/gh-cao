import assert from "node:assert/strict";
import test from "node:test";
import { setTimeout as delay } from "node:timers/promises";
import { createCliActionQueue } from "../../dashboard/cli-action-queue.mjs";
import { operationalPostgres } from "../helpers/postgres-operational.mjs";

const invocation = { actionId: "test-action", command: "gh aw upgrade --repo octo/repo", arguments: {}, values: {} };

async function terminal(queue, id) {
  for (let attempt = 0; attempt < 100; attempt++) {
    const value = await queue.detail(id);
    if (["completed", "failed", "canceled", "timed-out"].includes(value.status)) return value;
    await delay(50);
  }
  throw new Error("PostgreSQL command did not finish.");
}

test("PostgreSQL queue retains idempotency, framed redaction and completed output across restart", async (t) => {
  if (!operationalPostgres(t)) return;
  const secret = "local-secret-never-store";
  const execute = async ({ onOutput }) => {
    await onOutput({ stream: "stdout", data: secret.slice(0, 10) });
    await onOutput({ stream: "stdout", data: secret.slice(10) + "\nhello\n" });
    return { ok: true, exitCode: 0 };
  };
  let queue = await createCliActionQueue({ execute, sensitiveValues: [secret] });
  t.after(() => queue.close());
  const created = await queue.enqueue(invocation, "same-command");
  const result = await terminal(queue, created.id);
  assert.equal(result.status, "completed");
  assert.equal(result.stdout, "[REDACTED]\nhello\n");
  assert.equal((await queue.enqueue(invocation, "same-command")).id, created.id);
  await assert.rejects(queue.enqueue({ ...invocation, actionId: "other-action" }, "same-command"), /input|conflict|superseded/i);
  await queue.close();
  queue = await createCliActionQueue({ execute });
  assert.equal((await queue.detail(created.id)).stdout, result.stdout);
});

test("PostgreSQL queue has one fenced owner and preserves cancellation", async (t) => {
  if (!operationalPostgres(t)) return;
  const execute = async ({ signal }) => {
    if (!signal.aborted) await new Promise(resolve => signal.addEventListener("abort", resolve, { once: true }));
    return { ok: false, exitCode: 130 };
  };
  const queue = await createCliActionQueue({ execute });
  t.after(() => queue.close());
  await assert.rejects(createCliActionQueue({ execute }), /stopped|ready|owned/i);
  const value = await queue.enqueue(invocation);
  await delay(100);
  await queue.cancel(value.id);
  assert.equal((await terminal(queue, value.id)).status, "canceled");
  await assert.rejects(queue.retry(value.id, "retry-once"), /Only failed or timed-out/);
});

test("PostgreSQL queue admits failed-command retries as new commands", async (t) => {
  if (!operationalPostgres(t)) return;
  const queue = await createCliActionQueue({ execute: async () => ({ ok: false, exitCode: 1 }) });
  t.after(() => queue.close());
  const original = await queue.enqueue(invocation);
  assert.equal((await terminal(queue, original.id)).status, "failed");
  const retry = await queue.retry(original.id, "retry-once");
  assert.equal(retry.retryOf, original.id);
  assert.notEqual(retry.id, original.id);
  await terminal(queue, retry.id);
});
test("local queues fail closed without PostgreSQL configuration", async () => {
  await assert.rejects(createCliActionQueue({ executable: "", execute: async () => ({}) }), /requires PostgreSQL/);
});

test("unconfirmed execution never becomes a completed command or releases admission", async (t) => {
  if (!operationalPostgres(t)) return;
  let reconciled = false;
  const queue = await createCliActionQueue({
    admission: async () => ({ concurrency: 1, complete: async () => { reconciled = true; } }),
    execute: async () => ({ ok: true }),
  });
  t.after(() => queue.close());
  const created = await queue.enqueue(invocation);
  let state;
  for (let attempt = 0; attempt < 100; attempt++) {
    state = await queue.list();
    if (state.pausedReason?.includes("unconfirmed")) break;
    await delay(50);
  }
  assert.match(state.pausedReason, /exit is unconfirmed/);
  assert.equal((await queue.detail(created.id)).status, "running");
  assert.equal(reconciled, false);
  await assert.rejects(queue.retry(created.id), /Only failed or timed-out/);
});
