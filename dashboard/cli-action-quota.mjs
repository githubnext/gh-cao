import { spawn } from "node:child_process";
import { fileURLToPath } from "node:url";
import { createInterface } from "node:readline";
import { existsSync } from "node:fs";
import { join } from "node:path";
import { parseDashboardCommand } from "./cli-actions.mjs";

const serverDirectory = fileURLToPath(new URL("../server/", import.meta.url));
const sourceCheckout = existsSync(join(serverDirectory, "go.mod"));
const missingHelperMessage =
  "GitHub quota helper is unavailable. Install cao-cli-quota on PATH or set CAO_CLI_QUOTA_EXECUTABLE.";
const unknownDecision = () => ({
  capacity: 0,
  reason: "unknown",
  retryAt: new Date(Date.now() + 15_000).toISOString(),
});

/** Only the resolved command, never an action label or ID, can bypass quota. */
export function cliActionRequiresGitHubQuota(candidate) {
  if (typeof candidate?.command !== "string") return true;
  let tokens;
  try {
    tokens = parseDashboardCommand(candidate.command);
  } catch {
    return true;
  }
  if (tokens[0] !== "gh" || tokens[1] !== "aw") return true;
  if (tokens.length === 3 && ["help", "--help", "version", "--version"].includes(tokens[2])) return false;
  if (tokens[2] !== "compile") return true;
  // Explicit remote contexts and update checks are not local compilation.
  return tokens.slice(3).some((token) =>
    /^(?:--repo(?:=|$)|-R|--check-update(?:=|$))/.test(token));
}

function requiredQuotaResources(candidate) {
  try {
    const tokens = parseDashboardCommand(candidate?.command);
    if (tokens[0] === "gh" && tokens[1] === "workflow" && tokens[2] === "run") {
      return ["core"];
    }
  } catch {
    // Unknown commands cannot narrow the resources required for admission.
  }
  return ["core", "graphql"];
}

/**
 * Local-admin-only quota admission. Construct this only for an active local
 * queue, never for an online dashboard. The persistent helper owns all meters.
 *
 * admission(candidate) is the queue adapter: returns {concurrency, reason,
 * retryAt?, reservationId?, onOutput?, release?, complete?}. The queue forwards
 * streaming chunks to onOutput, calls release when it cannot claim, or calls
 * complete(indicators) after the executor settles.
 *
 * admit(candidate) reserves one start and returns {capacity, reason, retryAt,
 * reservationId}; capacity is the total safe concurrency, not free slots.
 * Call complete(reservationId, {rateLimited, retryAfterSeconds}) after exit.
 * status() refreshes without reserving. A failed helper always pauses starts.
 * Source checkouts use go run; portable installs require cao-cli-quota on PATH
 * or CAO_CLI_QUOTA_EXECUTABLE pointing to the prebuilt executable.
 *
 * @param {{
 * workingDirectory?: string,
 * maxConcurrency?: number,
 * estimatedCost?: number,
 * minimumRemaining?: number,
 * helperExecutable?: string,
 * helperArguments?: string[],
 * ghExecutable?: string,
 * env?: NodeJS.ProcessEnv,
 * requestTimeoutMs?: number,
 * }} [options]
 */
export function createCliActionAdmission(options = {}) {
  const {
    workingDirectory = process.cwd(),
    maxConcurrency = 4,
    estimatedCost = 50,
    minimumRemaining = 100,
    helperExecutable = process.env.CAO_CLI_QUOTA_EXECUTABLE
      || (sourceCheckout ? "go" : "cao-cli-quota"),
    helperArguments = helperExecutable === "go"
      ? ["-C", serverDirectory, "run", "./cmd/cao-cli-quota"]
      : [],
    ghExecutable = "gh",
    env = process.env,
    requestTimeoutMs = 120_000,
  } = options;
  for (const [name, value] of Object.entries({
    maxConcurrency, estimatedCost, minimumRemaining, requestTimeoutMs,
  })) {
    if (!Number.isSafeInteger(value) || value <= 0) {
      throw new TypeError(`${name} must be a positive integer`);
    }
  }
  let child;
  let closed = false;
  let failed = false;
  let failureMessage;
  let nextID = 0;
  let nextLocalID = 0;
  const pending = new Map();
  let sequence = Promise.resolve();

  function pausedDecision() {
    return {
      ...unknownDecision(),
      ...(failureMessage ? { message: failureMessage } : {}),
    };
  }

  function fail(error) {
    if (error?.code === "ENOENT") failureMessage = missingHelperMessage;
    failed = true;
    for (const { resolve, timer } of pending.values()) {
      clearTimeout(timer);
      resolve(pausedDecision());
    }
    pending.clear();
    child?.stdin.destroy();
    child?.kill();
  }

  function start() {
    child = spawn(helperExecutable, helperArguments, {
      cwd: workingDirectory,
      env: {
        ...env,
        GH_PATH: ghExecutable,
        CAO_CLI_QUOTA_WORKING_DIRECTORY: workingDirectory,
      },
      stdio: ["pipe", "pipe", "ignore"],
    });
    child.on("error", fail);
    child.on("exit", fail);
    child.stdin.on("error", fail);
    const lines = createInterface({ input: child.stdout });
    lines.on("line", (line) => {
      if (line.length > 16_384) return fail();
      let result;
      try {
        result = JSON.parse(line);
      } catch {
        return fail();
      }
      const waiter = pending.get(result.id);
      if (!waiter) return fail();
      pending.delete(result.id);
      clearTimeout(waiter.timer);
      if (!Number.isInteger(result.capacity) || result.capacity < 0
        || result.capacity > maxConcurrency
        || !["available", "unknown", "exhausted", "parked", "concurrency"].includes(result.reason)
        || (result.retryAt !== undefined && !Number.isFinite(Date.parse(result.retryAt)))
        || (result.reservationId !== undefined && typeof result.reservationId !== "string")) {
        waiter.resolve(unknownDecision());
        return fail();
      }
      // Copy only public scheduling metadata, not arbitrary helper output.
      waiter.resolve({
        capacity: result.capacity,
        reason: result.reason,
        ...(result.retryAt ? { retryAt: result.retryAt } : {}),
        ...(result.reservationId ? { reservationId: result.reservationId } : {}),
      });
    });
  }

  function request(operation, extra = {}) {
    const execute = () => {
      if (closed || failed) return pausedDecision();
      if (!child) start();
      const id = ++nextID;
      return new Promise((resolve) => {
        const timer = setTimeout(fail, requestTimeoutMs);
        pending.set(id, { resolve, timer });
        child.stdin.write(`${JSON.stringify({
          id, operation, maxConcurrency, estimatedCost, minimumRemaining, ...extra,
        })}\n`, (error) => { if (error) fail(); });
      });
    };
    const result = sequence.then(execute, execute);
    sequence = result.then(() => {});
    return result;
  }

  function admit(candidate) {
    if (closed) return Promise.resolve(unknownDecision());
    if (!cliActionRequiresGitHubQuota(candidate)) {
      return Promise.resolve({
        capacity: maxConcurrency, reason: "available", reservationId: `local:${++nextLocalID}`,
      });
    }
    return request("admit", {
      running: Number.isSafeInteger(candidate?.running) ? Math.max(0, candidate.running) : 0,
      resources: requiredQuotaResources(candidate),
    });
  }

  function release(reservationId) {
    if (typeof reservationId === "string" && reservationId.startsWith("local:")) {
      return Promise.resolve(closed ? unknownDecision() : { capacity: maxConcurrency, reason: "available" });
    }
    return request("release", { reservationId });
  }

  function complete(reservationId, { rateLimited = false, retryAfterSeconds = 0 } = {}) {
    if (typeof reservationId === "string" && reservationId.startsWith("local:")) {
      return Promise.resolve(closed ? unknownDecision() : { capacity: maxConcurrency, reason: "available" });
    }
    return request("complete", {
      reservationId,
      rateLimited: rateLimited === true,
      retryAfterSeconds: Number.isSafeInteger(retryAfterSeconds)
        ? Math.min(86_400, Math.max(0, retryAfterSeconds)) : 0,
    });
  }

  return {
    /**
     * Queue adapter: candidate.running is the total current local worker count.
     * A reserved start must invoke exactly one of release() (never claimed) or
     * complete(indicators) (executor settled), including cancellation/errors.
     */
    async admission(candidate) {
      if (closed) {
        const { capacity: _capacity, ...metadata } = unknownDecision();
        return { ...metadata, concurrency: 0 };
      }
      if (Number.isSafeInteger(candidate?.running) && candidate.running >= maxConcurrency) {
        return { concurrency: 0, reason: "concurrency" };
      }
      if (!cliActionRequiresGitHubQuota(candidate)) {
        return { concurrency: maxConcurrency, reason: "available" };
      }
      const result = await admit(candidate);
      const { capacity, ...metadata } = result;
      if (!result.reservationId) return { ...metadata, concurrency: 0 };
      let settled = false;
      let rateLimited = false;
      let retryAfterSeconds = 0;
      const tails = { stdout: "", stderr: "" };
      const observeOutput = (chunk) => {
        if (settled || typeof chunk?.data !== "string") return;
        const stream = chunk.stream === "stderr" ? "stderr" : "stdout";
        const text = tails[stream] + chunk.data;
        const indicators = detectCliRateLimit(text);
        rateLimited ||= indicators.rateLimited;
        retryAfterSeconds = Math.max(retryAfterSeconds, indicators.retryAfterSeconds);
        tails[stream] = text.slice(-128);
      };
      const settle = (finished, indicators = {}) => {
        if (settled) return Promise.resolve();
        if (finished) {
          for (const stream of ["stdout", "stderr"]) {
            observeOutput({ stream, data: indicators.result?.[stream] });
          }
        }
        settled = true;
        tails.stdout = tails.stderr = "";
        return finished
          ? complete(result.reservationId, {
            rateLimited: rateLimited || indicators.rateLimited === true,
            retryAfterSeconds: Math.max(retryAfterSeconds,
              Number.isSafeInteger(indicators.retryAfterSeconds) ? indicators.retryAfterSeconds : 0),
          })
          : release(result.reservationId);
      };
      return {
        ...metadata, concurrency: capacity,
        onOutput: observeOutput,
        release: () => settle(false),
        complete: (indicators) => settle(indicators?.started !== false, indicators),
      };
    },
    admit,
    release,
    status: (candidate) => !closed && !cliActionRequiresGitHubQuota(candidate)
      ? Promise.resolve({ capacity: maxConcurrency, reason: "available" })
      : request("status", { resources: requiredQuotaResources(candidate) }),
    complete,
    close() {
      closed = true;
      fail();
    },
  };
}

/** Inspect output incrementally; never retain or forward raw command output. */
export function detectCliRateLimit(text) {
  const rateLimited = /secondary rate limit|abuse detection|HTTP 429|too many requests|retry-after\s*:/i.test(text);
  const seconds = /retry-after\s*:\s*(\d+)/i.exec(text)?.[1];
  return {
    rateLimited,
    retryAfterSeconds: rateLimited && seconds ? Math.min(86_400, Number(seconds)) : 0,
  };
}
