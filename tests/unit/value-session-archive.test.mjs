import assert from "node:assert/strict";
import test from "node:test";
import { createArchiveReader, linkArchiveProducers, validateArchiveQuery } from "../../.github/skills/add-operational-value/scripts/session-archive.mjs";

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
