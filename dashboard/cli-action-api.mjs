import { once } from "node:events";
import { cliActionTemplateFields, renderCliActionCommand } from "./site/src/cli-action-template.js";
import { maximumCliActionInputCharacters, maximumCliActionRequestBytes } from "./cli-action-contract.mjs";
import { environmentDefaultsCommand, parseEnvironmentDefaults } from "./site/src/environment-defaults.js";

const terminalStatuses = new Set(["completed", "failed", "canceled", "timed-out"]);

function failure(status, message) {
  return Object.assign(new Error(message), { status });
}

/** Resolve only dashboard-declared actions, never browser-supplied executable text. */
export function resolveQueuedCliAction(dashboard, payload, context = {}) {
  if (!payload || typeof payload !== "object" || Array.isArray(payload)
      || Object.keys(payload).some((key) => !["id", "arguments", "values", "input", "idempotencyKey"].includes(key))
      || typeof payload.id !== "string" || !/^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$/.test(payload.id)
      || (payload.arguments !== undefined
        && (!payload.arguments || typeof payload.arguments !== "object" || Array.isArray(payload.arguments)))
      || (payload.values !== undefined
        && (!payload.values || typeof payload.values !== "object" || Array.isArray(payload.values)))
      || (payload.input !== undefined && (typeof payload.input !== "string" || !payload.input.length
        || payload.input.length > maximumCliActionInputCharacters))) {
    throw failure(400, "Invalid CLI action request.");
  }
  const action = dashboard.dashboard?.["cli-actions"]?.find((candidate) => candidate?.id === payload.id);
  if (!action || typeof action.command !== "string") {
    throw failure(404, "CLI action is not declared by this dashboard.");
  }
  if (action["copy-only"] === true) throw failure(403, "CLI action is copy-only.");
  const declaredArguments = Array.isArray(action.arguments) ? action.arguments : [];
  const argumentsValue = payload.arguments ?? {};
  const fields = cliActionTemplateFields(action.command);
  const values = payload.values ?? {};
  if (Object.keys(argumentsValue).some((id) =>
    !declaredArguments.some((argument) => argument?.id === id) || typeof argumentsValue[id] !== "boolean")) {
    throw failure(400, "Invalid CLI action arguments.");
  }
  if (Object.keys(values).some((field) => !fields.includes(field))
      || fields.some((field) => typeof values[field] !== "string")) {
    throw failure(400, "Invalid CLI action template values.");
  }
  let rendered;
  try {
    rendered = renderCliActionCommand(action.command, values, context);
  } catch {
    throw failure(400, "Invalid CLI action template values.");
  }
  const command = [
    rendered,
    ...declaredArguments.filter((argument) => argumentsValue[argument.id] ?? argument.default === true)
      .map((argument) => argument.flag),
  ].join(" ");
  const defaults = environmentDefaultsCommand(command);
  if ((payload.input !== undefined) !== (command === "gh agent-task create --from-file -" || defaults?.operation === "update")) {
    throw failure(400, "CLI action input is not valid for this command.");
  }
  if (defaults?.operation === "update") {
    try { parseEnvironmentDefaults(payload.input); }
    catch { throw failure(400, "Invalid compiler defaults YAML. Use default_ keys with string or null values."); }
  }
  return {
    actionId: action.id, command, arguments: argumentsValue, values,
    ...(payload.input === undefined ? {} : { input: payload.input }),
  };
}

/** Replay and live delivery share a persistent cursor; detaching owns no execution lifetime. */
async function streamCommand(queue, id, after, response) {
  if (!await queue.detail(id)) throw failure(404, "CLI command was not found.");
  response.writeHead(200, {
    "Cache-Control": "no-store", "Content-Type": "application/x-ndjson; charset=utf-8",
    "X-Content-Type-Options": "nosniff",
  });
  response.flushHeaders();
  let cursor = after;
  let status;
  let wake;
  let changed = true;
  let detached = false;
  const notify = () => { changed = true; wake?.(); };
  const unsubscribe = queue.subscribe(notify);
  const detach = () => { detached = true; notify(); };
  response.once("close", detach);
  const write = async (event) => {
    if (detached || response.destroyed) return;
    if (!response.write(`${JSON.stringify(event)}\n`)) {
      const wait = new AbortController();
      try {
        await Promise.race([
          once(response, "drain", { signal: wait.signal }),
          once(response, "close", { signal: wait.signal }),
        ]);
      } finally {
        wait.abort();
      }
    }
  };
  try {
    while (!detached && !response.destroyed) {
      changed = false;
      const chunks = await queue.output(id, cursor, 128);
      for (const chunk of chunks) {
        if (detached) return;
        await write({ type: "output", ...chunk });
        cursor = chunk.sequence;
      }
      if (chunks.length === 128) continue;
      const command = await queue.detail(id);
      if (terminalStatuses.has(command.status)) {
        // A final output batch may have committed while the previous batch was read.
        const remaining = await queue.output(id, cursor, 1);
        if (remaining.length) continue;
        await write({
          type: "complete", command,
          result: {
            ok: command.status === "completed", exitCode: command.exitCode,
            ...(command.error ? { error: command.error } : {}),
            ...(command.result?.pullRequestUrl ? { pullRequestUrl: command.result.pullRequestUrl } : {}),
          },
        });
        response.end();
        return;
      }
      if (command.status !== status) {
        status = command.status;
        await write({ type: "status", command: { id: command.id, status } });
      }
      if (!changed && !detached) {
        await new Promise((resolve) => { wake = resolve; });
        wake = undefined;
      }
    }
  } finally {
    unsubscribe();
    response.removeListener("close", detach);
  }
}

function integer(value, fallback, maximum) {
  if (value === null) return fallback;
  if (!/^\d+$/.test(value)) throw failure(400, "Invalid queue pagination.");
  const number = Number(value);
  if (!Number.isSafeInteger(number) || number > maximum) throw failure(400, "Invalid queue pagination.");
  return number;
}

/** Capability-path and host validation remain owned by the local server. */
export function createCliActionApi({ queue, getDashboard, getTemplateContext = () => ({}), approve, isAllowedOrigin, readJsonRequest, sendJson }) {
  let approvalPending = false;
  const read = async (request) => {
    if (!isAllowedOrigin(request.headers.origin)
        || !String(request.headers["content-type"] ?? "").toLowerCase().startsWith("application/json")) {
      throw failure(403, "Forbidden");
    }
    try {
      return await readJsonRequest(request, maximumCliActionRequestBytes);
    } catch (error) {
      throw failure(error?.code === "REQUEST_TOO_LARGE" ? 413 : 400, "Invalid CLI action request.");
    }
  };
  const authorize = async (invocation) => {
    if (approvalPending) throw failure(409, "Another CLI action is awaiting approval.");
    approvalPending = true;
    try {
      let approved;
      try {
        approved = await approve(Object.freeze({
          id: invocation.actionId, command: invocation.command,
          ...(invocation.input === undefined ? {} : { input: invocation.input }),
        }));
      } catch {
        throw failure(503, "Trusted approval is unavailable.");
      }
      if (approved !== true) throw failure(403, "CLI action was not approved by the trusted host.");
    } finally {
      approvalPending = false;
    }
  };
  const enqueue = async (request) => {
    const payload = await read(request);
    const invocation = resolveQueuedCliAction(getDashboard(), payload, getTemplateContext());
    await authorize(invocation);
    return queue.enqueue(invocation, payload.idempotencyKey ?? request.headers["idempotency-key"]);
  };
  return async (request, response, pathname, url) => {
    const legacy = pathname === "/__cli_action";
    if (!legacy && pathname !== "/__cli_commands" && !pathname.startsWith("/__cli_commands/")) return false;
    if (!queue) {
      response.writeHead(404).end("Not found\n");
      return true;
    }
    try {
      if (legacy || pathname === "/__cli_commands") {
        if (request.method === "POST") {
          const command = await enqueue(request);
          if (legacy) await streamCommand(queue, command.id, 0, response);
          else sendJson(response, 202, command);
        } else if (!legacy && request.method === "GET") {
          const result = await queue.list({
            status: url.searchParams.get("status") || undefined,
            limit: integer(url.searchParams.get("limit"), 50, 300),
            offset: integer(url.searchParams.get("offset"), 0, 1_000_000),
          });
          sendJson(response, 200, {
            ...result,
            commands: result.commands.map(({ input: _input, stdout: _stdout, stderr: _stderr,
              arguments: _arguments, values = {}, ...summary }) => ({
              ...summary,
              repository: values.repository,
              campaign: values.campaign,
              "package-source": values["package-source"],
            })),
          });
        } else response.writeHead(405, { Allow: legacy ? "POST" : "GET, POST" }).end();
        return true;
      }
      const match = /^\/__cli_commands\/([a-zA-Z0-9-]+)(?:\/(output|cancel|retry))?$/.exec(pathname);
      if (!match) throw failure(404, "CLI command was not found.");
      const [, id, operation] = match;
      const existing = await queue.detail(id);
      if (!existing) throw failure(404, "CLI command was not found.");
      if (request.method === "GET" && !operation) {
        sendJson(response, 200, existing);
      } else if (request.method === "GET" && operation === "output") {
        await streamCommand(queue, id, integer(url.searchParams.get("after"), 0, Number.MAX_SAFE_INTEGER), response);
      } else if (request.method === "POST" && (operation === "cancel" || operation === "retry")) {
        const payload = await read(request);
        if (!payload || typeof payload !== "object" || Array.isArray(payload)
            || Object.keys(payload).some((key) => key !== "idempotencyKey")) {
          throw failure(400, "Invalid CLI command request.");
        }
        if (operation === "cancel") sendJson(response, 200, await queue.cancel(id));
        else {
          const original = existing;
          if (original.status !== "failed" && original.status !== "timed-out") {
            throw failure(409, "Only failed or timed-out CLI actions can be retried.");
          }
          const invocation = resolveQueuedCliAction(getDashboard(), {
            id: original.actionId, arguments: original.arguments, values: original.values, input: original.input,
          }, getTemplateContext());
          if (invocation.command !== original.command) throw failure(409, "CLI action declaration has changed.");
          await authorize(invocation);
          const retried = await queue.retry(id, payload.idempotencyKey ?? request.headers["idempotency-key"]);
          if (String(retried.retryOf) !== String(id)) {
            throw failure(409, "Retry idempotency key conflicts with another command.");
          }
          sendJson(response, 202, retried);
        }
      } else response.writeHead(405, { Allow: operation === "cancel" || operation === "retry" ? "POST" : "GET" }).end();
    } catch (error) {
      if (!response.headersSent && !response.destroyed) {
        sendJson(response, error.status ?? error.statusCode ?? 400, { error: error.message || "CLI command request failed." });
      } else if (!response.destroyed) response.end();
    }
    return true;
  };
}
