import assert from "node:assert/strict";
import { createHash } from "node:crypto";

export const ARCHIVE_LIMITS = Object.freeze({ rows: 4096, bytes: 8 * 1024 * 1024, timeoutMs: 30000 });
const natural = value => Number.isSafeInteger(value) && value >= 0;
const runId = value => typeof value === "string" && /^[1-9][0-9]{0,19}$/.test(value)
  && BigInt(value) <= 18446744073709551615n;
const coordinate = value => typeof value === "string"
  && /^[a-z0-9][a-z0-9-]*\/[a-z0-9][a-z0-9._-]*$/.test(value);
const digest = bytes => createHash("sha256").update(bytes).digest("hex");

export class ArchiveReadError extends Error {
  constructor(status) {
    const code = ({ 401: "authentication-required", 403: "read-denied-or-withdrawn",
      404: "trace-not-found", 422: "complete-result-budget-exceeded",
      503: "archive-unavailable" })[status] ?? "archive-request-failed";
    super(`CAO session archive: ${code} (HTTP ${status}); no fallback or recollection attempted`);
    this.name = "ArchiveReadError";
    this.status = status;
    this.code = code;
  }
}

export function validateArchiveQuery(query) {
  assert.ok(query && typeof query === "object" && !Array.isArray(query), "An exact archive query is required");
  assert.ok(Object.keys(query).every(key => ["kind", "repository", "runId", "attempt", "limit"].includes(key)),
    "Archive accepts trusted selectors only, not SQL, KQL, paths or generation overrides");
  assert.ok(["summary", "timeline"].includes(query.kind) && coordinate(query.repository),
    "Exact normalized repository and trusted query kind required");
  assert.ok(Number.isSafeInteger(query.limit) && query.limit >= 1 && query.limit <= ARCHIVE_LIMITS.rows,
    "Archive query requires an explicit portable row limit (1..4096)");
  if (query.kind === "summary") {
    assert.ok(!Object.hasOwn(query, "runId") && !Object.hasOwn(query, "attempt"),
      "Summary rejects run and attempt selectors");
  } else {
    assert.ok(runId(query.runId) && natural(query.attempt) && query.attempt > 0,
      "Timeline requires an exact positive run ID and attempt");
  }
  return query;
}

async function readResponse(response, maxBytes = ARCHIVE_LIMITS.bytes) {
  if (!response.ok) {
    await response.body?.cancel();
    throw new ArchiveReadError(response.status);
  }
  const declared = response.headers.get("content-length");
  if (declared !== null && (!/^[0-9]+$/.test(declared) || Number(declared) > maxBytes)) {
    await response.body?.cancel();
    throw new Error("Archive response exceeds byte budget");
  }
  if (!/^application\/json(?:;|$)/i.test(response.headers.get("content-type") ?? "")) {
    await response.body?.cancel();
    throw new Error("Expected archive JSON, not a login page or another source");
  }
  assert.ok(response.body, "Archive response has no body");
  const reader = response.body.getReader();
  const chunks = [];
  let size = 0;
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      size += value.byteLength;
      assert.ok(size <= maxBytes, "Archive response exceeds byte budget");
      chunks.push(value);
    }
  } finally {
    await reader.cancel();
  }
  const bytes = Buffer.concat(chunks);
  let payload;
  try { payload = JSON.parse(bytes.toString("utf8")); }
  catch { throw new TypeError("Invalid archive JSON response"); }
  return { payload, sha256: digest(bytes), bytes: bytes.length };
}

function validateResult(result, query) {
  assert.ok(result && result.kind === query.kind && natural(result.generation),
    "Archive result lacks its trusted kind or pinned generation");
  const key = query.kind === "summary" ? "summary" : "events";
  assert.ok(!Object.hasOwn(result, query.kind === "summary" ? "events" : "summary"),
    "Archive returned an unexpected result kind");
  const rows = Object.hasOwn(result, key) ? result[key] : [];
  assert.ok(Array.isArray(rows) && rows.length <= query.limit, "Archive complete result exceeds row budget");
  const seen = new Set();
  let previousSequence = -1;
  let projection;
  for (const row of rows) {
    if (query.kind === "summary") {
      assert.ok(row.repository === query.repository && runId(row.runId)
        && natural(row.attempt) && row.attempt > 0 && natural(row.events)
        && typeof row.projectionId === "string" && row.projectionId.length > 0,
      "Archive summary scope or identity mismatch");
      const id = `${row.runId}:${row.attempt}`;
      assert.ok(!seen.has(id), "Duplicate archive trace");
      seen.add(id);
    } else {
      assert.ok(row.Repository === query.repository && row.RunId === query.runId && row.RunAttempt === query.attempt,
        "Archive timeline scope or identity mismatch");
      assert.ok(natural(row.Sequence) && row.Sequence > previousSequence
        && typeof row.ProjectionId === "string" && row.ProjectionId.length > 0
        && typeof row.PayloadJSON === "string", "Archive timeline ordering or projection mismatch");
      assert.ok(projection === undefined || row.ProjectionId === projection, "Archive mixed trace projections");
      projection = row.ProjectionId;
      previousSequence = row.Sequence;
    }
  }
}

/** The same trusted service fronts local DuckDB and hosted Blob/ADX. No direct file or SQL access. */
export function createArchiveReader({ url, headers = () => ({}), fetchImpl = fetch,
  now = () => new Date().toISOString(), signal }) {
  const endpoint = new URL(url);
  assert.ok(endpoint.protocol === "https:" || (endpoint.protocol === "http:"
    && ["127.0.0.1", "localhost", "[::1]"].includes(endpoint.hostname)), "Archive requires HTTPS or loopback HTTP");
  assert.ok(!endpoint.username && !endpoint.password && !endpoint.search && !endpoint.hash
    && endpoint.pathname === "/", "Archive URL must be an origin without credentials, query or path");
  const request = async (route, query, { signal: requestSignal, maxBytes = ARCHIVE_LIMITS.bytes } = {}) => {
    assert.ok(Number.isSafeInteger(maxBytes) && maxBytes > 0 && maxBytes <= ARCHIVE_LIMITS.bytes,
      "Invalid archive response budget");
    const authorization = await headers();
    const response = await fetchImpl(new URL(`/api/v1/session-archive/${route}`, endpoint), {
      method: query ? "POST" : "GET",
      headers: { ...authorization, Accept: "application/json", ...(query ? { "Content-Type": "application/json" } : {}) },
      ...(query ? { body: JSON.stringify(query) } : {}),
      redirect: "error",
      signal: AbortSignal.any([AbortSignal.timeout(ARCHIVE_LIMITS.timeoutMs),
        ...(signal ? [signal] : []), ...(requestSignal ? [requestSignal] : [])]),
    });
    return readResponse(response, maxBytes);
  };
  return {
    async status(options) {
      const result = await request("status", undefined, options);
      const payload = result.payload;
      assert.ok(typeof payload?.enabled === "boolean", "Invalid archive availability");
      if (!payload.enabled) assert.equal(payload.stage, "disabled");
      else {
        assert.ok(["active", "retrying"].includes(payload.stage), "Invalid archive lifecycle stage");
        assert.equal(payload.archive?.enabled, true, "Contradictory archive status");
        for (const key of ["generation", "activeTraces", "stagedTraces", "gaps", "archiveBytes", "stagingBytes"]) {
          assert.ok(natural(payload.archive[key]), `Invalid archive status counter: ${key}`);
        }
      }
      return { ...result, observedAt: now(), coverage: "not-established-by-lifecycle-status" };
    },
    async query(input, options) {
      const query = structuredClone(validateArchiveQuery(input));
      const result = await request("query", query, options);
      validateResult(result.payload, query);
      return { ...result, source: "cao-session-archive", observedAt: now(), request: query,
        generation: result.payload.generation };
    },
  };
}

/** Join already identified producers, not output discovery or acceptance inference.
 * Keep this private provenance separate from the public canonical value packet. */
export async function linkArchiveProducers({ reader, repository, producers }) {
  assert.ok(coordinate(repository) && Array.isArray(producers) && producers.length <= 512,
    "Bounded exact producer scope required");
  const deadline = AbortSignal.timeout(120000);
  const status = await reader.status({ signal: deadline });
  const result = { source: "cao-session-archive", checkedAt: status.observedAt,
    state: status.payload.enabled ? "no-exact-producer" : "disabled", generation: null, links: [],
    requests: 1, bytes: status.bytes, eventRows: 0,
    limitation: "Session identity only: not an applied-output receipt, Git measurement, webhook delivery or quality acceptance." };
  if (!status.payload.enabled) return result;
  const queried = new Map();
  for (const producer of producers) {
    assert.ok(typeof producer.outputId === "string" && producer.outputId.length > 0, "Output identity required");
    if (!runId(producer.runId) || !natural(producer.attempt) || producer.attempt === 0) {
      result.links.push({ outputId: producer.outputId, state: "unknown-producer-attempt" });
      continue;
    }
    const key = `${producer.runId}:${producer.attempt}`;
    const identity = { outputId: producer.outputId, repository, runId: producer.runId, attempt: producer.attempt };
    if (!queried.has(key)) {
      assert.ok(result.requests < 130, "Archive producer query budget exceeded");
      const maxBytes = Math.min(ARCHIVE_LIMITS.bytes, 16 * 1024 * 1024 - result.bytes);
      assert.ok(maxBytes > 0, "Archive producer byte budget exceeded");
      result.state = "queried";
      result.requests++;
      let timeline;
      try {
        timeline = await reader.query({ kind: "timeline", repository,
          runId: producer.runId, attempt: producer.attempt, limit: ARCHIVE_LIMITS.rows },
        { signal: deadline, maxBytes });
      } catch (error) {
        if (!(error instanceof ArchiveReadError) || error.status !== 404) throw error;
        queried.set(key, { state: "not-retained", generation: null });
        result.links.push({ ...identity, ...queried.get(key) });
        continue;
      }
      result.bytes += timeline.bytes;
      if (result.generation === null) result.generation = timeline.generation;
      assert.equal(timeline.generation, result.generation, "Archive generation changed; retry explicitly");
      const events = timeline.payload.events ?? [];
      assert.ok(events.length > 0, "Successful archive timeline is empty");
      result.eventRows += events.length;
      queried.set(key, { state: "retained-session", projectionId: events[0].ProjectionId, generation: timeline.generation,
        responseSha256: timeline.sha256, eventRows: events.length, readAt: timeline.observedAt });
    }
    result.links.push({ ...identity, ...queried.get(key) });
  }
  return result;
}
