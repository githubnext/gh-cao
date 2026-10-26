import assert from "node:assert/strict";
import { lstat, readFile } from "node:fs/promises";
import { ARCHIVE_LIMITS, ArchiveReadError, createArchiveReader, linkArchiveProducers } from "./session-archive.mjs";

const limitation = "Exact retained session linkage only; not acceptance, webhook delivery, Git measurements or quality proof.";
const coordinate = /^[a-z0-9][a-z0-9-]*\/[a-z0-9][a-z0-9._-]*$/;

export function valueArchiveAccessFromEnvironment(env = process.env) {
  if (env.CAO_VALUE_ARCHIVE_READ_APPROVED !== "true") {
    assert.ok(!env.CAO_VALUE_ARCHIVE_READ_APPROVED || env.CAO_VALUE_ARCHIVE_READ_APPROVED === "false",
      "CAO_VALUE_ARCHIVE_READ_APPROVED must be true or false");
    return { approved: false };
  }
  const repositories = (env.CAO_VALUE_ARCHIVE_REPOSITORIES ?? "").split(",").map(value => value.trim());
  assert.ok(repositories.length > 0 && repositories.every(value => coordinate.test(value)),
    "Archive access requires exact approved repositories");
  assert.ok(env.CAO_VALUE_ARCHIVE_SERVER, "Approved archive access requires a server origin");
  assert.ok(Boolean(env.CAO_VALUE_ARCHIVE_HEADERS_FILE) !== Boolean(env.CAO_VALUE_ARCHIVE_TOKEN),
    "Provide one private archive authentication source");
  if (env.CAO_VALUE_ARCHIVE_TOKEN) assert.ok(env.CAO_VALUE_ARCHIVE_TOKEN.length <= 16384
    && env.CAO_VALUE_ARCHIVE_TOKEN.trim() && !/[\r\n]/.test(env.CAO_VALUE_ARCHIVE_TOKEN),
  "Invalid private archive token");
  return {
    approved: true, repositories, url: env.CAO_VALUE_ARCHIVE_SERVER,
    headers: async () => {
      if (env.CAO_VALUE_ARCHIVE_TOKEN) return { Authorization: `Bearer ${env.CAO_VALUE_ARCHIVE_TOKEN}` };
      const file = env.CAO_VALUE_ARCHIVE_HEADERS_FILE;
      const info = await lstat(file);
      assert.ok(info.isFile() && info.size <= 16384
        && (process.platform === "win32" || (info.mode & 0o077) === 0
          && info.uid === process.getuid()), "Archive credentials require a private owner-only regular file");
      let headers;
      try { headers = JSON.parse(await readFile(file, "utf8")); }
      catch { throw new Error("Cannot read private archive authentication headers"); }
      assert.ok(headers && !Array.isArray(headers) && Object.keys(headers).length > 0
        && Object.entries(headers).every(([key, value]) =>
          ["Authorization", "Cookie", "X-CSRF-Token"].includes(key)
          && typeof value === "string" && value.length > 0 && !/[\r\n]/.test(value)),
      "Invalid private archive authentication headers");
      return headers;
    },
  };
}

/** One invocation-owned capability; only linkage metadata leaves this provider. */
export function createValueArchiveProvider({ repository, access = { approved: false },
  signal, reader, now = () => new Date().toISOString() }) {
  assert.ok(coordinate.test(repository), "Exact normalized collector repository required");
  const deadline = AbortSignal.any([AbortSignal.timeout(120000), ...(signal ? [signal] : [])]);
  let busy = false;
  let status;
  let generation;
  let requests = 0;
  let bytes = 0;
  const cache = new Map();
  if (access.approved) {
    assert.ok(Array.isArray(access.repositories), "Exact archive approval scope is required");
    if (access.repositories.includes(repository)) {
      reader ??= createArchiveReader({ url: access.url, headers: access.headers, signal: deadline, now });
    }
  }
  const admit = () => {
    deadline.throwIfAborted();
    assert.ok(requests < 130 && bytes < 16 * 1024 * 1024, "Collector archive budget exhausted");
    requests++;
  };
  const bounded = {
    async status() {
      if (!status) {
        admit();
        status = await reader.status({ signal: deadline });
        bytes += status.bytes;
      }
      return status;
    },
    async query(query) {
      assert.equal(query.kind, "timeline", "Collectors cannot enumerate archive repositories");
      assert.equal(query.repository, repository, "Cross-repository archive query refused");
      const key = `${query.runId}:${query.attempt}`;
      if (cache.has(key)) {
        const cached = cache.get(key);
        if (cached instanceof Error) throw cached;
        return cached;
      }
      admit();
      let response;
      try {
        response = await reader.query(query, { signal: deadline,
          maxBytes: Math.min(ARCHIVE_LIMITS.bytes, 16 * 1024 * 1024 - bytes) });
      } catch (error) {
        if (error instanceof ArchiveReadError && error.status === 404) cache.set(key, error);
        throw error;
      }
      bytes += response.bytes;
      if (generation === undefined) generation = response.generation;
      assert.equal(response.generation, generation, "Archive generation changed during collector invocation");
      const events = response.payload.events ?? [];
      const retained = { ...response, payload: { kind: "timeline", generation,
        events: events.map(event => ({ ProjectionId: event.ProjectionId })) },
      hasGaps: events.some(event => Array.isArray(event.Gaps) && event.Gaps.length > 0) };
      cache.set(key, retained);
      return retained;
    },
  };
  return Object.freeze({
    async link(producers) {
      deadline.throwIfAborted();
      assert.ok(Array.isArray(producers) && producers.length <= 512 && producers.every(producer =>
        producer && typeof producer === "object"
        && Object.keys(producer).every(key => ["outputId", "runId", "attempt", "repository"].includes(key))
        && (!producer.repository || producer.repository === repository)),
      "Archive linkage requires bounded same-repository producer identities");
      if (!access.approved) return { source: "cao-session-archive", state: "disabled",
        checkedAt: now(), generation: null, links: [], requests: 0, bytes: 0, limitation };
      assert.ok(access.repositories.includes(repository), "Collector repository lacks archive read approval");
      assert.ok(!busy, "Concurrent archive linkage requests are deferred");
      busy = true;
      try {
        const result = await linkArchiveProducers({ reader: bounded, repository, producers });
        const partial = status.payload.archive?.gaps > 0 || status.payload.stage === "retrying"
          || result.links.some(link => link.state !== "retained-session")
          || [...cache.values()].some(value => !(value instanceof Error) && value.hasGaps);
        return { ...result, state: result.state === "disabled" ? "disabled" : partial ? "partial"
          : result.links.length > 0 ? "available" : "no-exact-producer",
        scope: "requested-producer-identities-only", requests, bytes, limitation };
      } catch (error) {
        if (!(error instanceof ArchiveReadError) || error.status !== 503) throw error;
        return { source: "cao-session-archive", state: "unavailable", checkedAt: now(),
          generation: null, links: [], requests, bytes, reason: error.code, limitation };
      } finally {
        busy = false;
      }
    },
  });
}
