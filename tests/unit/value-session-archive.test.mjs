import assert from "node:assert/strict";
import test from "node:test";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import path from "node:path";
import { createArchiveReader, linkArchiveProducers, validateArchiveQuery } from "../../.github/skills/add-operational-value/scripts/session-archive.mjs";
import { createValueArchiveProvider, valueArchiveAccessFromEnvironment } from "../../activity/value-session-archive.mjs";
import { collectWorkflowValue } from "../../activity/workflow-values.mjs";

const repository = "github/gh-aw";
const status = { enabled: true, stage: "active", archive: {
  enabled: true, generation: 7, activeTraces: 1, stagedTraces: 0, gaps: 0, archiveBytes: 100, stagingBytes: 0,
} };
const producers = [{ outputId: "I_1", runId: "123", attempt: 1 }, { outputId: "I_2", runId: "123", attempt: 1 }];
const timeline = { kind: "timeline", generation: 7, events: [{
  Repository: repository, RunId: "123", RunAttempt: 1, ProjectionId: "p1", Sequence: 0,
  PayloadJSON: '{"untrusted":"not a Git baseline or acceptance receipt"}',
}] };

function reader(respond, calls = []) {
  return createArchiveReader({ url: "http://127.0.0.1:8443", headers: () => ({ Authorization: "Bearer test-only" }),
    fetchImpl: async (url, options) => {
      assert.equal(options.redirect, "error");
      assert.ok(options.signal);
      calls.push({ path: url.pathname, method: options.method, query: options.body && JSON.parse(options.body) });
      const result = respond(calls.at(-1));
      return new Response(JSON.stringify(result.body ?? result), {
        status: result.httpStatus ?? 200, headers: { "Content-Type": "application/json" },
      });
    } });
}

test("archive producer linkage reuses one exact timeline without copying raw content or multiplying value", async () => {
  const calls = [];
  const result = await linkArchiveProducers({ repository, producers, reader: reader(
    call => call.method === "GET" ? { ...status, archive: { ...status.archive, activeTraces: 1000000 } } : timeline, calls) });
  assert.equal(result.requests, 2);
  assert.equal(result.eventRows, 1);
  assert.equal(result.links.length, 2);
  assert.ok(result.links.every(link => link.generation === 7 && link.projectionId === "p1"));
  assert.equal(calls.filter(call => call.query?.kind === "timeline").length, 1);
  assert.equal(calls.filter(call => call.query?.kind === "summary").length, 0, "Do not enumerate unrelated workflow traces");
  assert.ok(calls.filter(call => call.query).every(call => call.query.repository === repository));
  assert.ok(!JSON.stringify(result).includes("PayloadJSON"));
  assert.match(result.limitation, /not an applied-output receipt/);
});

test("disabled, missing attempts and absent membership remain explicit without recollection", async () => {
  const disabled = await linkArchiveProducers({ repository, producers, reader: reader(() => ({ enabled: false, stage: "disabled" })) });
  assert.equal(disabled.state, "disabled");
  assert.equal(disabled.generation, null);
  assert.equal(disabled.requests, 1);
  const result = await linkArchiveProducers({ repository, producers: [
    { outputId: "unknown" }, { outputId: "missing", runId: "456", attempt: 2 },
  ], reader: reader(call => call.method === "GET" ? status : { httpStatus: 404, body: {} }) });
  assert.equal(result.requests, 2);
  assert.deepEqual(result.links.map(link => link.state), ["unknown-producer-attempt", "not-retained"]);
});

test("archive generation, scope, ordering, empty results and read failures cannot silently become historical success", async () => {
  for (const mutate of [
    value => { value.events[0].Repository = "outside/repo"; },
    value => { value.events[0].RunAttempt = 2; },
    value => { value.events.push({ ...value.events[0], ProjectionId: "changed", Sequence: 1 }); },
    value => { value.events.push(value.events[0]); },
    value => { value.events = []; },
  ]) {
    const changed = structuredClone(timeline);
    mutate(changed);
    await assert.rejects(linkArchiveProducers({ repository, producers, reader: reader(
      call => call.method === "GET" ? status : changed) }));
  }
  let sequence = 0;
  await assert.rejects(linkArchiveProducers({ repository, producers: [
    producers[0], { outputId: "I_3", runId: "456", attempt: 1 },
  ], reader: reader(call => call.method === "GET" ? status : {
    ...timeline, generation: 7 + sequence++,
    events: [{ ...timeline.events[0], RunId: call.query.runId }],
  }) }), /generation changed/);
  for (const httpStatus of [401, 403, 404, 422, 503]) {
    await assert.rejects(linkArchiveProducers({ repository, producers,
      reader: reader(() => ({ httpStatus, body: { error: "private content" } })) }), error =>
      error.status === httpStatus && !error.message.includes("private content"));
  }
});

test("archive body budgets and invalid JSON fail without echoing private trace contents", async () => {
  const make = response => createArchiveReader({ url: "http://127.0.0.1:8443", fetchImpl: async () => response });
  await assert.rejects(make(new Response('private-invalid-body', {
    headers: { "Content-Type": "application/json" },
  })).status(), error => error.message === "Invalid archive JSON response");
  await assert.rejects(make(new Response(JSON.stringify(timeline), {
    headers: { "Content-Type": "application/json" },
  })).query({ kind: "timeline", repository, runId: "123", attempt: 1, limit: 4096 },
  { maxBytes: 16 }), /byte budget/);
});

test("archive service has bounded trusted selectors, no SQL or public cross-store fallback", () => {
  for (const extra of [{ sql: "select *" }, { kql: "externaldata" }, { path: "/tmp" }, { generation: 7 },
    { repository: "*" }, { limit: 4097 }, { runId: "00123" }, { attempt: 0 }]) {
    assert.throws(() => validateArchiveQuery({ kind: "timeline", repository, runId: "123", attempt: 1, limit: 4096, ...extra }));
  }
  for (const url of ["http://outside.test", "https://secret@host.test", "https://host.test/archive"]) {
    assert.throws(() => createArchiveReader({ url }));
  }
});

test("standard collector provider is opt-in, scoped, metadata-only and reuses exact producers", async () => {
  const calls = [];
  const access = { approved: true, repositories: [repository], url: "http://127.0.0.1:8443" };
  const provider = createValueArchiveProvider({ repository, access, reader: reader(
    call => call.method === "GET" ? status : timeline, calls) });
  const first = await provider.link(producers);
  const reused = await provider.link(producers);
  assert.equal(first.state, "available");
  assert.equal(reused.requests, 2);
  assert.equal(calls.length, 2);
  assert.ok(!JSON.stringify(reused).includes("PayloadJSON"));
  assert.match(reused.limitation, /not acceptance/);
  await assert.rejects(provider.link([{ ...producers[0], repository: "outside/repository" }]), /same-repository/);
  await assert.rejects(provider.link([{ ...producers[0], sql: "SELECT *" }]), /same-repository/);
  const outside = createValueArchiveProvider({ repository: "outside/repository", access });
  await assert.rejects(outside.link(producers), /approval/);
  const disabled = await createValueArchiveProvider({ repository, reader: reader(() => { throw new Error("No read authorized"); }) }).link(producers);
  assert.equal(disabled.state, "disabled");
  assert.equal(disabled.requests, 0);
});

test("standard provider distinguishes partial and unavailable archives and obeys cancellation", async () => {
  const access = { approved: true, repositories: [repository] };
  const partial = createValueArchiveProvider({ repository, access, reader: reader(
    call => call.method === "GET" ? status : { httpStatus: 404, body: {} }) });
  assert.equal((await partial.link(producers)).state, "partial");
  const unavailable = createValueArchiveProvider({ repository, access, reader: reader(
    () => ({ httpStatus: 503, body: {} })) });
  assert.equal((await unavailable.link(producers)).state, "unavailable");
  const controller = new AbortController();
  const cancelled = createValueArchiveProvider({ repository, access, signal: controller.signal, reader: reader(
    () => { throw new Error("Cancelled reads must not run"); }) });
  controller.abort(new Error("Collector cancelled"));
  await assert.rejects(cancelled.link(producers), /Collector cancelled/);
});

test("the production registered-collector context supplies scoped archive linkage without File Diet imports", async t => {
  const root = path.resolve(import.meta.dirname, "../..");
  mkdirSync(path.join(root, ".tmp"), { recursive: true });
  const directory = mkdtempSync(path.join(root, ".tmp", "collector-archive-"));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const envelope = JSON.parse(readFileSync(path.join(root, "dashboard/site/test/fixtures/operational-value-evidence.jsonl")));
  const packet = envelope.operational_value_evidence;
  for (const fact of packet.facts) if (fact["fact-kind"] === "coverage") fact["coverage-status"] = "complete";
  const header = packet.facts.find(fact => fact["fact-kind"] === "contract");
  writeFileSync(path.join(directory, "collector.mjs"), `
    import {writeFileSync} from 'node:fs';
    import path from 'node:path';
    export const definition = ${JSON.stringify({
      schemaVersion: 3, repository: packet.repository, slug: packet.workflow, sourcePath: packet.sourcePath,
      adoption: { adoptedAt: packet.windowStart },
      evidence: { repositories: [packet.repository], window: { cadenceDays: header["cadence-days"], maturationDays: 0 } },
    })};
    export async function collectValueEvidence({archive, workspace}) {
      const links = await archive.link([{outputId:'I_1',runId:'123',attempt:1}]);
      writeFileSync(path.join(workspace.directory,'private-links.json'), JSON.stringify(links));
      return ${JSON.stringify(envelope)};
    }
  `);
  const calls = [];
  const result = await collectWorkflowValue({
    root: directory, entry: { repository: packet.repository, workflow: packet.workflow, collector: "collector.mjs" },
    observedAt: packet.observedAt, workspace: path.join(directory, "proof"),
  }, { archiveAccess: { approved: true, repositories: [packet.repository] }, archiveReader: reader(
    call => call.method === "GET" ? status : {
      ...timeline, events: timeline.events.map(event => ({ ...event, Repository: packet.repository })),
    }, calls) });
  assert.deepEqual(result, envelope);
  const links = JSON.parse(readFileSync(path.join(directory, "proof/private-links.json")));
  assert.equal(links.state, "available");
  assert.equal(calls.length, 2);
  assert.ok(!JSON.stringify(links).includes("PayloadJSON"));
  assert.equal(calls[1].query.repository, packet.repository);
});

test("collector archive budgets and backpressure apply across repeated linkage calls", async () => {
  const access = { approved: true, repositories: [repository] };
  const calls = [];
  const provider = createValueArchiveProvider({ repository, access, reader: reader(call =>
    call.method === "GET" ? status : { ...timeline,
      events: [{ ...timeline.events[0], RunId: call.query.runId }] }, calls) });
  for (let index = 0; index < 129; index++) {
    await provider.link([{ outputId: `I_${index}`, runId: String(index + 1), attempt: 1 }]);
  }
  await assert.rejects(provider.link([{ outputId: "over-budget", runId: "1000", attempt: 1 }]), /budget exhausted/);
  assert.equal(calls.length, 130);

  let release;
  const pending = new Promise(resolve => { release = resolve; });
  const waiting = createValueArchiveProvider({ repository, access, reader: {
    status: async () => { await pending; return { payload: { enabled: false, stage: "disabled" }, bytes: 37, observedAt: "2026-10-01T00:00:00Z" }; },
  } });
  const first = waiting.link(producers);
  await assert.rejects(waiting.link(producers), /Concurrent archive linkage/);
  release();
  assert.equal((await first).state, "disabled");
});

test("archive cancellation interrupts an in-flight authorized request", async () => {
  const controller = new AbortController();
  const provider = createValueArchiveProvider({ repository, access: {
    approved: true, repositories: [repository],
  }, signal: controller.signal, reader: createArchiveReader({
    url: "http://127.0.0.1:8443",
    fetchImpl: async (_url, options) => new Promise((_resolve, reject) => {
      options.signal.addEventListener("abort", () => reject(options.signal.reason), { once: true });
    }),
  }) });
  const pending = provider.link(producers);
  await new Promise(resolve => setImmediate(resolve));
  controller.abort(new Error("Stop this collector"));
  await assert.rejects(pending, /Stop this collector/);
});

test("archive generation and aggregate bytes remain bounded across separate collector calls", async () => {
  const access = { approved: true, repositories: [repository] };
  let generation = 7;
  const provider = createValueArchiveProvider({ repository, access, reader: reader(call =>
    call.method === "GET" ? status : { ...timeline, generation: generation++,
      events: [{ ...timeline.events[0], RunId: call.query.runId }] }) });
  await provider.link(producers);
  await assert.rejects(provider.link([{ outputId: "other", runId: "456", attempt: 1 }]), /generation changed/);
  const bounded = createValueArchiveProvider({ repository, access, reader: reader(call =>
    call.method === "GET" ? status : { ...timeline, events: [{
      ...timeline.events[0], RunId: call.query.runId, PayloadJSON: JSON.stringify("x".repeat(6 * 1024 * 1024)),
    }] }) });
  for (const runId of ["1", "2"]) await bounded.link([{ outputId: runId, runId, attempt: 1 }]);
  await assert.rejects(bounded.link([{ outputId: "3", runId: "3", attempt: 1 }]), /byte budget/);
});

test("archive configuration requires explicit scope and private authentication without enabling capture", async t => {
  assert.deepEqual(valueArchiveAccessFromEnvironment({}), { approved: false });
  assert.throws(() => valueArchiveAccessFromEnvironment({ CAO_VALUE_ARCHIVE_READ_APPROVED: "yes" }), /true or false/);
  const env = { CAO_VALUE_ARCHIVE_READ_APPROVED: "true", CAO_VALUE_ARCHIVE_REPOSITORIES: repository,
    CAO_VALUE_ARCHIVE_SERVER: "http://127.0.0.1:8443" };
  assert.throws(() => valueArchiveAccessFromEnvironment(env), /one private/);
  assert.throws(() => valueArchiveAccessFromEnvironment({ ...env, CAO_VALUE_ARCHIVE_REPOSITORIES: "*" }), /exact approved/);
  const root = path.resolve(import.meta.dirname, "../..");
  const directory = mkdtempSync(path.join(root, ".tmp", "archive-auth-test-"));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const file = path.join(directory, "headers.json");
  writeFileSync(file, JSON.stringify({ Cookie: "synthetic-test-session", "X-CSRF-Token": "synthetic-test-csrf" }), { mode: 0o600 });
  const access = valueArchiveAccessFromEnvironment({ ...env, CAO_VALUE_ARCHIVE_HEADERS_FILE: file });
  assert.deepEqual(Object.keys(await access.headers()).sort(), ["Cookie", "X-CSRF-Token"]);
  writeFileSync(file, JSON.stringify({ "X-Repository-Override": "outside/repository" }));
  await assert.rejects(access.headers(), /Invalid private archive/);
  assert.throws(() => valueArchiveAccessFromEnvironment({ ...env,
    CAO_VALUE_ARCHIVE_HEADERS_FILE: file, CAO_VALUE_ARCHIVE_TOKEN: "synthetic-test-token" }), /one private/);
});
