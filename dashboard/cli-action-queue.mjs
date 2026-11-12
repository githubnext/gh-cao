import { parseDashboardCommand } from "./cli-actions.mjs";
import { lastPullRequestUrl } from "./cli-command-result.mjs";
import { openServerProcess } from "./server-process.mjs";
import { maximumCliActionRequestBytes } from "./cli-action-contract.mjs";

export const dashboardQueueLimits = Object.freeze({
  maxConcurrency: 32, maxJobs: 10000, maxInputBytes: maximumCliActionRequestBytes,
  maxChunkBytes: 16 * 1024, maxOutputBytes: 1024 * 1024, maxDatabaseOutputBytes: 64 * 1024 * 1024,
  maxOutputChunks: 4096, maxPageSize: 500, maxPreviewBytes: 64 * 1024, retentionDays: 30,
});
const terminal = new Set(["completed", "failed", "canceled", "timed-out"]);
const sensitiveKey = /token|secret|password|credential|authorization|private[-_]?key/i;

function command(value) {
  if (!value) return null;
  const stamp = (value) => value ? new Date(value).getTime() : null;
  return {
    id: value.ID, actionId: value.actionId, command: value.command,
    arguments: value.arguments ?? {}, values: value.values ?? {},
    ...(value.input === undefined ? {} : { input: value.input }),
    status: value.Status, createdAt: stamp(value.CreatedAt), startedAt: stamp(value.StartedAt),
    completedAt: stamp(value.CompletedAt), deadline: stamp(value.Deadline),
    completionWeek: value.CompletionWeek, idempotencyKey: value.IdempotencyKey,
    retryOf: value.RetryOf || null, exitCode: value.ExitCode, error: value.Error || null,
    outputBytes: value.OutputBytes, outputChunks: value.OutputChunks,
    outputTruncated: value.OutputTruncated, ok: terminal.has(value.Status) ? value.Status === "completed" : null,
  };
}

/** PostgreSQL owns persistence, leases, capacity, recovery and atomic audit. */
export async function createCliActionQueue({
  executable = process.env.CAO_DASHBOARD_EXECUTABLE,
  postgresURL = process.env.CAO_POSTGRES_URL,
  namespace = process.env.CAO_OPERATIONAL_NAMESPACE,
  execute, admission = async () => ({ concurrency: maxConcurrency }),
  maxConcurrency = 1, timeoutMs = 15 * 60 * 1000, admissionPollMs = 1000,
  acknowledgeRecovery = false, sensitiveValues = [],
  ...unsupportedOptions
} = {}) {
  if (Object.keys(unsupportedOptions).length) throw new Error("Unsupported queue configuration; PostgreSQL owns persistence and clock settings.");
  if (!executable || !postgresURL || typeof execute !== "function") {
    throw new Error("The CLI queue requires PostgreSQL, a cao-dashboard executable, and an executor.");
  }
  if (!Number.isInteger(maxConcurrency) || maxConcurrency < 1 || maxConcurrency > 32
      || !Number.isInteger(timeoutMs) || timeoutMs < 1 || timeoutMs > 15 * 60 * 1000
      || !Number.isInteger(admissionPollMs) || admissionPollMs < 1 || admissionPollMs > 60_000
      || typeof admission !== "function" || typeof acknowledgeRecovery !== "boolean"
      || !Array.isArray(sensitiveValues)) {
    throw new Error("Invalid queue concurrency or timeout.");
  }
  const secrets = [...new Set([...sensitiveValues, ...Object.entries(process.env)
    .filter(([name, value]) => sensitiveKey.test(name) && value?.length >= 8)
    .map(([, value]) => value)])];
  if (secrets.some((value) => typeof value !== "string" || value.length < 4)) throw new Error("Invalid sensitive values.");
  const adapter = await openServerProcess(executable, ["local-queue",
    `--concurrency=${maxConcurrency}`, `--timeout=${timeoutMs}ms`,
    ...(acknowledgeRecovery ? ["--acknowledge-recovery"] : []),
  ], { env: { ...process.env, CAO_POSTGRES_URL: postgresURL,
    ...(namespace ? { CAO_OPERATIONAL_NAMESPACE: namespace } : {}) } });
  const listeners = new Set();
  const active = new Map();
  let closed = false;
  let pausedReason;
  let timer;
  let pumping = Promise.resolve();
  let closePromise;
  const emit = () => {
    for (const listener of listeners) {
      try { listener(); } catch { /* Observers own their failures. */ }
    }
  };
  const request = async (operation, values = {}) => {
    const result = await adapter.request({ operation, ...values });
    if (["enqueue", "cancel", "append", "finish", "settle", "retain"].includes(operation)) emit();
    return result;
  };
  const sanitize = (text) => {
    for (const secret of secrets) text = text.replaceAll(secret, "[REDACTED]");
    return text;
  };
  const detail = async (id) => {
    const value = command(await request("detail", { id: Number(id) }));
    if (!value) return null;
    const chunks = await request("output", { id: value.id, limit: 500 });
    value.stdout = "";
    value.stderr = "";
    let remaining = dashboardQueueLimits.maxPreviewBytes;
    let pullRequestUrl;
    for (const chunk of chunks ?? []) {
      if (Buffer.byteLength(chunk.Data) > remaining) break;
      value[chunk.Stream] += chunk.Data;
      remaining -= Buffer.byteLength(chunk.Data);
      pullRequestUrl = lastPullRequestUrl(chunk.Data) ?? pullRequestUrl;
    }
    value.result = value.ok === null ? null : {
      ok: value.ok, exitCode: value.exitCode, ...(value.error ? { error: value.error } : {}),
      ...(pullRequestUrl ? { pullRequestUrl } : {}),
    };
    if (value.status === "queued" && pausedReason) value.reason = pausedReason;
    return value;
  };
  const schedule = (delay = admissionPollMs) => {
    if (closed || timer) return;
    timer = setTimeout(() => {
      timer = undefined;
      pumping = pump();
    }, delay);
    timer.unref();
  };
  async function run(value, decision, controller) {
    const buffers = { stdout: "", stderr: "" };
    const streamed = { stdout: false, stderr: false };
    const privateKey = { stdout: false, stderr: false };
    let output = Promise.resolve();
    let executionResult;
    const append = (stream, data) => {
      output = output.then(() => request("append", { id: value.id, stream, data: sanitize(data) }));
    };
    const consume = ({ stream, data }) => {
      if (!["stdout", "stderr"].includes(stream) || typeof data !== "string") throw new Error("Invalid CLI output.");
      buffers[stream] += data;
      const lines = buffers[stream].split("\n");
      buffers[stream] = lines.pop();
      for (const line of lines) {
        if (/-----BEGIN .*PRIVATE KEY-----/.test(line)) privateKey[stream] = true;
        if (privateKey[stream]) {
          append(stream, "[REDACTED]\n");
          if (/-----END .*PRIVATE KEY-----/.test(line)) privateKey[stream] = false;
        } else if (Buffer.byteLength(line) < dashboardQueueLimits.maxChunkBytes) append(stream, line + "\n");
      }
      if (Buffer.byteLength(buffers[stream]) > dashboardQueueLimits.maxChunkBytes) buffers[stream] = "[Overlong output suppressed.]";
    };
    const deadline = setTimeout(() => controller.abort(new Error("CLI command timed out.")), timeoutMs);
    const monitor = setInterval(async () => {
      try {
        const latest = command(await request("detail", { id: value.id }));
        if (!latest || terminal.has(latest.status)) controller.abort();
      } catch {
        pausedReason = "CLI queue ownership is unavailable.";
        controller.abort();
      }
    }, 1000);
    try {
      executionResult = await execute({
        ...value, signal: controller.signal, timeoutMs, deadline: value.deadline,
        onOutput: async (chunk) => {
          await decision.onOutput?.(chunk);
          consume(chunk);
          streamed[chunk.stream] = true;
          await output;
        },
      });
      for (const stream of ["stdout", "stderr"]) {
        if (executionResult?.[stream] && !streamed[stream]) consume({ stream, data: executionResult[stream] });
        if (buffers[stream]) consume({ stream, data: "\n" });
      }
      await output;
    } catch (error) {
      executionResult = { ok: false, exitCode: Number.isInteger(error?.code) ? error.code : null };
    } finally {
      clearTimeout(deadline);
      clearInterval(monitor);
    }
    const exitCode = Number.isInteger(executionResult?.exitCode) ? executionResult.exitCode : null;
    try {
      const latest = command(await request("detail", { id: value.id }));
      if (exitCode === null) {
        pausedReason = "Command exit is unconfirmed; operator recovery is required.";
        return;
      }
      if (!terminal.has(latest.status)) {
        await request("finish", { id: value.id, result: {
          Status: executionResult?.ok && exitCode === 0 ? "completed" : "failed", ExitCode: exitCode,
          Error: executionResult?.ok && exitCode === 0 ? "" : "The CLI action did not complete successfully.",
        } });
      }
      await request("settle", { id: value.id, result: { ExitCode: exitCode } });
      await decision.complete?.({ started: true, result: executionResult });
    } catch {
      pausedReason = "CLI command reconciliation is unavailable.";
    } finally {
      active.delete(value.id);
      schedule(0);
    }
  }
  async function pump() {
    if (closed || pausedReason === "Command exit is unconfirmed; operator recovery is required.") return;
    try {
      await request("retain");
      while (!closed && active.size < maxConcurrency) {
        let page = await request("list", { statuses: ["queued"], limit: 1, offset: 0 });
        if (!page.Commands?.length) break;
        if (page.Total > 1) page = await request("list", { statuses: ["queued"], limit: 1, offset: page.Total - 1 });
        const next = command(page.Commands[0]);
        const decision = await admission({ ...next, running: active.size });
        const concurrency = Number.isInteger(decision?.concurrency)
          ? Math.max(0, Math.min(maxConcurrency, decision.concurrency)) : 0;
        if (active.size >= concurrency || closed) {
          await decision?.release?.();
          pausedReason = "CLI action admission is currently blocked.";
          break;
        }
        const claimed = command(await request("claim"));
        if (!claimed) { await decision?.release?.(); break; }
        if (claimed.id !== next.id) throw new Error("CLI command changed during admission.");
        pausedReason = undefined;
        const controller = new AbortController();
        const state = { controller, promise: null };
        active.set(claimed.id, state);
        state.promise = run(claimed, decision, controller);
      }
    } catch {
      pausedReason = "CLI queue execution is unavailable.";
      emit();
    } finally { schedule(); }
  }
  const enqueue = async (invocation, key, retryOf = 0) => {
    parseDashboardCommand(invocation.command);
    const content = JSON.stringify(invocation);
    if (secrets.some((value) => content.includes(value))) throw new Error("Secret-bearing CLI input cannot be persisted.");
    const value = command(await request("enqueue", { invocation, key, retryOf }));
    schedule(0);
    return detail(value.id);
  };
  schedule(0);
  return {
    enqueue, detail,
    async list({ status, limit = 50, offset = 0 } = {}) {
      const page = await request("list", { statuses: status?.split(","), limit, offset });
      return { commands: await Promise.all((page.Commands ?? []).map((value) => detail(value.ID))),
        total: page.Total, running: page.Running, revision: page.Revision,
        pausedReason: page.RecoveryBlocked ? "Operator recovery is required." : pausedReason ?? null };
    },
    async output(id, after = 0, limit = 100) {
      const values = await request("output", { id: Number(id), after, limit });
      return (values ?? []).map((value) => ({ sequence: value.Sequence, stream: value.Stream, data: value.Data }));
    },
    async cancel(id) {
      await request("cancel", { id: Number(id) });
      active.get(Number(id))?.controller.abort();
      return detail(id);
    },
    async retry(id, key) {
      const value = await detail(id);
      if (!value || !["failed", "timed-out"].includes(value.status)) throw new Error("Only failed or timed-out CLI actions can be retried.");
      await active.get(Number(id))?.promise;
      return enqueue(value, key, value.id);
    },
    subscribe(listener) { listeners.add(listener); return () => listeners.delete(listener); },
    close() {
      if (closePromise) return closePromise;
      closed = true;
      clearTimeout(timer);
      for (const { controller } of active.values()) controller.abort();
      closePromise = (async () => {
        await pumping;
        await Promise.all([...active.values()].map(({ promise }) => promise));
        listeners.clear();
        await adapter.close();
      })();
      return closePromise;
    },
  };
}
