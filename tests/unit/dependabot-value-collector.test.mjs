import assert from "node:assert/strict";
import test from "node:test";
import { collectDependabotValue, dependabotContract, dependabotNext } from "../../.github/value-collectors/dependabot-update-planner.mjs";
import { definition, buildEvidence, scoreMetric } from "../../dependabot/operational-value/dependabot-update-planner.mjs";

const request = {
  repository: "github/gh-aw", workflow: "dependabot-update-planner",
  observedAt: "2026-10-02T00:00:00Z", previous: [],
};
const collect = (read, previous = [], observedAt = request.observedAt) =>
  collectDependabotValue({ ...request, previous, observedAt }, definition, buildEvidence, scoreMetric, read);
const empty = endpoint => ({ body: endpoint.startsWith("search/")
  ? { total_count: 0, incomplete_results: false, items: [] } : [], link: "" });
const values = envelopes => envelopes.map(envelope => envelope.operational_value_evidence.facts
  .filter(fact => fact["fact-kind"] === "snapshot").at(-1).value);

test("Actions Dependabot packets retain the existing hosted contract fingerprints", () => {
  assert.deepEqual(definition.metrics.map(metric => dependabotContract(metric).digest), [
    "d73a2676ebb04ebf34093873dc5eb72b8e54a461c8b5f068b7b3bd100fc2507a",
    "c0bcc97030ca99e4ed5bbe1b89b64ebde02bc7b4659682058e407cf155603ed2",
    "774112c80afca79b272669e30a75dc5b1216191f9345e6e14970863bb617652a",
  ]);
});

test("complete empty lifecycles are zero; same-cutoff cadence makes no requests", async () => {
  const endpoints = [];
  const packets = (await collect(endpoint => { endpoints.push(endpoint); return empty(endpoint); }))
    .map(envelope => envelope.operational_value_evidence);
  assert.equal(endpoints.length, 2);
  assert.equal(packets.length, 3);
  assert.ok(packets.every(packet => packet.windowEnd === "2026-10-01T08:48:00Z"));
  assert.deepEqual(packets.map(packet => packet.facts[1].value), [0, 0, 0]);
  assert.equal(await collect(() => assert.fail("cadence should reuse accepted evidence"), packets), null);
  const later = await collect(empty, packets, "2026-10-09T00:00:00Z");
  assert.deepEqual(values(later), [0, 0, 0]);
  assert.ok(later.every((envelope, index) => {
    assert.deepEqual(envelope.operational_value_evidence.facts.slice(0, 3), packets[index].facts);
    return envelope.operational_value_evidence.windowEnd === "2026-10-08T08:48:00Z";
  }));
});

test("native measurements reconstruct the frozen cutoff independently of actor", async () => {
  const packets = await collect(endpoint => endpoint.startsWith("search/") ? {
    body: { total_count: 1, incomplete_results: false, items: [{
      number: 2, created_at: "2026-09-20T00:00:00Z", closed_at: "2026-10-01T12:00:00Z",
      state: "closed", user: { login: "dependabot[bot]" }, pull_request: {},
    }] },
  } : {
    body: [
      { number: 1, created_at: "2026-09-20T00:00:00Z", fixed_at: "2026-10-01T12:00:00Z",
        state: "fixed", security_advisory: { severity: "critical" } },
      { number: 2, created_at: "2026-09-20T00:00:00Z", dismissed_at: "2026-10-01T08:48:00Z",
        state: "dismissed", security_advisory: { severity: "low" } },
      { number: 3, created_at: "2026-10-01T12:00:00Z", state: "open", security_advisory: { severity: "high" } },
    ], link: "",
  });
  assert.deepEqual(values(packets), [1, 1, 1]);
});

test("incomplete or contradictory lifecycle acquisition cannot become zero", async () => {
  for (const body of [
    null,
    [{ number: 1, created_at: "invalid", state: "open", security_advisory: { severity: "high" } }],
    [{ number: 1, created_at: "2026-09-20T00:00:00Z", state: "fixed", security_advisory: { severity: "high" } }],
    Array(100).fill({ number: 1 }),
  ]) await assert.rejects(collect(() => ({ body, link: "" })));
  for (const body of [
    { total_count: 1, incomplete_results: false, items: [] },
    { total_count: 0, incomplete_results: true, items: [] },
    { total_count: 1001, incomplete_results: false, items: [] },
    { total_count: 1, incomplete_results: false, items: [{ number: 2, created_at: "2026-09-20T00:00:00Z",
      state: "open", pull_request: {}, user: { login: "another-actor" } }] },
  ]) await assert.rejects(collect(endpoint => endpoint.startsWith("search/") ? { body } : empty(endpoint)));
});

test("Dependabot cursor continuation stays within the reviewed endpoint and selection", () => {
  const endpoint = "repos/github/gh-aw/dependabot/alerts?state=auto_dismissed,dismissed,fixed,open&per_page=100";
  const link = `<https://api.github.com/${endpoint}&after=cursor>; rel="next"`;
  assert.equal(dependabotNext(link, endpoint), `${endpoint}&after=cursor`);
  assert.equal(dependabotNext(link.replace("repos/github/gh-aw", "repositories/1036865607"), endpoint),
    `${endpoint}&after=cursor`);
  assert.equal(dependabotNext("", endpoint), null);
  for (const changed of [
    link.replace("api.github.com", "elsewhere.example"),
    link.replace("github/gh-aw", "another/repo"),
    link.replace("per_page=100", "per_page=50"),
    link.replace("repos/github/gh-aw", "repositories/not-an-id"),
    link.replace("&after=cursor", ""),
    `${link}, ${link}`,
  ]) assert.throws(() => dependabotNext(changed, endpoint));
});

test("partial metric batches, changed contracts and incomplete cutoffs reject before acquisition", async () => {
  const previous = (await collect(empty)).map(envelope => envelope.operational_value_evidence);
  for (const change of [
    packets => packets.pop(),
    packets => { packets[1] = structuredClone(packets[0]); },
    packets => { packets[0].contractDigest = "0".repeat(64); },
    packets => { packets[0].facts[2]["coverage-status"] = "partial"; },
    packets => { packets[0].facts[1].value = -1; },
  ]) {
    const changed = structuredClone(previous);
    change(changed);
    await assert.rejects(collect(() => assert.fail("must reject before acquisition"), changed));
  }
});
