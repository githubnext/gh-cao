import assert from "node:assert/strict";
import { createHash } from "node:crypto";
import path from "node:path";
import { githubReader } from "../skills/add-operational-value/scripts/github-evidence.mjs";
import { validateOperationalValueEvidence } from "../../dashboard/site/src/data/model/operational-value-evidence.js";
import { definition as frozenDefinition, buildEvidence, scoreMetric } from "../../dependabot/operational-value/dependabot-update-planner.mjs";

const repository = "github/gh-aw";
const workflow = "dependabot-update-planner";
const version = "dependabot-lifecycle-v1";
const week = 7 * 86400000;
const iso = milliseconds => new Date(milliseconds).toISOString().replace(".000Z", "Z");
const packetId = metric => `operational-value-evidence:${encodeURIComponent(`${repository}:${workflow}:${metric.id}:${version}`)}`;

export const definition = { ...frozenDefinition, repository };

export async function collectValueEvidenceBatch(request) {
  return collectDependabotValue(request, frozenDefinition, buildEvidence, scoreMetric);
}

// Keep these factual packets identical to the existing hosted native collector.
export function dependabotContract(metric) {
  const fact = {
    "fact-kind": "contract", "fact-id": "contract",
    goal: "Reduce the open Dependabot security-alert backlog in github/gh-aw.",
    acceptance: "Complete alert and Dependabot-authored pull-request lifecycle evidence at a seven-day cutoff.",
    "workflow-name": "Dependabot / Update Planner",
    "metric-id": metric.id, "metric-name": metric.name, "metric-unit": metric.unit,
    "metric-version": version, "metric-formula": metric.formula,
    "metric-direction": "decrease", "measurement-basis": "repository-outcome",
    "result-label": "open Dependabot lifecycle count",
    "outputs-available": false, "repository-state-available": true, "checks-available": false,
    "snapshot-id": metric.id, "snapshot-name": metric.name, "snapshot-unit": metric.unit,
    "snapshot-version": version, "snapshot-formula": metric.formula,
    "snapshot-direction": "decrease", "cadence-days": 7, "maturation-days": 0,
  };
  return { fact, digest: createHash("sha256").update(JSON.stringify(fact, Object.keys(fact).sort())).digest("hex") };
}

function timestamp(value, observedAt) {
  const at = Date.parse(value);
  assert.ok(typeof value === "string" && /^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d(?:\.\d{1,9})?Z$/.test(value)
    && Number.isFinite(at) && new Date(at).toISOString().slice(0, 19) === value.slice(0, 19)
    && at <= Date.parse(observedAt), "Dependabot lifecycle timestamp is invalid");
  return at;
}

function lifecycle(row, observedAt, pull) {
  assert.ok(Number.isSafeInteger(row?.number) && row.number > 0, "Dependabot lifecycle identity is missing");
  const created = timestamp(row.created_at, observedAt);
  if (pull) {
    assert.ok(row.user?.login === "dependabot[bot]" && row.pull_request
      && ["open", "closed"].includes(row.state), "Search result is not a Dependabot pull request");
  } else {
    assert.ok(["critical", "high", "medium", "low"].includes(row.security_advisory?.severity)
      && ["open", "fixed", "dismissed", "auto_dismissed"].includes(row.state),
    "Dependabot alert severity or state is invalid");
  }
  const terminal = (pull ? [row.closed_at] : [row.fixed_at, row.dismissed_at, row.auto_dismissed_at])
    .filter(value => value != null).map(value => timestamp(value, observedAt));
  assert.ok(terminal.every(at => at >= created) && (row.state === "open") === (terminal.length === 0),
    "Dependabot lifecycle state contradicts its terminal timestamps");
  return { created_at: row.created_at, severity: row.security_advisory?.severity,
    ...(terminal.length ? { closed_at: iso(Math.min(...terminal)) } : {}) };
}

export function dependabotNext(link, endpoint) {
  if (!link) return null;
  const current = new URL(endpoint, "https://api.github.com/");
  let next = null;
  for (const part of link.split(/,(?=\s*<)/)) {
    const match = /^\s*<([^>]+)>\s*;\s*(.+)$/.exec(part);
    assert.ok(match, "Malformed Dependabot pagination link");
    if (!match[2].includes('rel="next"')) continue;
    const target = new URL(match[1]);
    const numericAlias = /^\/repositories\/[1-9]\d*\/dependabot\/alerts$/.test(target.pathname);
    assert.ok(!next && target.origin === current.origin && (target.pathname === current.pathname || numericAlias)
      && !target.username && !target.password && !target.hash,
    "Dependabot pagination escaped the reviewed endpoint");
    assert.ok(target.searchParams.getAll("after").length === 1 && target.searchParams.get("after")
      && target.searchParams.get("after") !== current.searchParams.get("after"),
    "Dependabot pagination lacks a new cursor");
    const actual = new URLSearchParams(target.search);
    const expected = new URLSearchParams(current.search);
    actual.delete("after"); expected.delete("after");
    actual.sort(); expected.sort();
    assert.equal(actual.toString(), expected.toString(), "Dependabot pagination changed the reviewed selection");
    // GitHub emits numeric repository aliases. Reuse only the cursor, never its target authority.
    next = current.pathname.slice(1) + target.search;
  }
  return next;
}

function acquire(read, observedAt, cutoff) {
  const alerts = [], pulls = [], seenAlerts = new Set(), seenPulls = new Set(), cursors = new Set();
  let endpoint = `repos/${repository}/dependabot/alerts?state=auto_dismissed,dismissed,fixed,open&per_page=100`;
  for (let page = 1; endpoint; page++) {
    assert.ok(page <= 100 && !cursors.has(endpoint), "Dependabot alert pagination exceeds its bound");
    cursors.add(endpoint);
    const { body, link } = read(endpoint);
    assert.ok(Array.isArray(body) && body.length <= 100, "Malformed Dependabot alert page");
    const next = dependabotNext(link, endpoint);
    assert.ok((next !== null) === (body.length === 100), "Incomplete Dependabot alert pagination");
    for (const row of body) {
      assert.ok(!seenAlerts.has(row.number), "Duplicate Dependabot alert");
      seenAlerts.add(row.number);
      alerts.push(lifecycle(row, observedAt, false));
    }
    endpoint = next;
  }
  const query = `repo:${repository} is:pr author:app/dependabot created:<=${iso(cutoff)}`;
  let total;
  for (let page = 1; page <= 10; page++) {
    const { body } = read(`search/issues?q=${encodeURIComponent(query)}&per_page=100&page=${page}`);
    assert.ok(Number.isSafeInteger(body?.total_count) && body.total_count >= 0 && body.total_count <= 1000
      && body.incomplete_results === false && Array.isArray(body.items), "Incomplete Dependabot PR search");
    total ??= body.total_count;
    assert.equal(body.total_count, total, "Dependabot PR population changed during pagination");
    assert.equal(body.items.length, Math.min(100, total - pulls.length), "Truncated Dependabot PR page");
    for (const row of body.items) {
      assert.ok(!seenPulls.has(row.number), "Duplicate Dependabot PR");
      seenPulls.add(row.number);
      const event = lifecycle(row, observedAt, true);
      assert.ok(Date.parse(event.created_at) <= cutoff, "Dependabot PR is outside the cutoff");
      pulls.push(event);
    }
    if (pulls.length === total) return { alerts, pullRequests: pulls };
  }
  throw new Error("Dependabot PR search exceeded ten pages");
}

export async function collectDependabotValue(request, definition, buildEvidence, scoreMetric, read) {
  assert.equal(request.repository, repository);
  assert.equal(request.workflow, workflow);
  const observed = timestamp(request.observedAt, new Date().toISOString());
  const adoption = Date.parse(definition.adoption.adoptedAt);
  const cutoff = adoption + Math.floor((observed - adoption) / week) * week;
  assert.ok(Array.isArray(request.previous) && [0, definition.metrics.length].includes(request.previous.length),
    "Incomplete retained Dependabot metric batch");
  if (cutoff < adoption + week) {
    assert.equal(request.previous.length, 0, "Retained Dependabot evidence follows the requested cutoff");
    return null;
  }
  const retained = new Map();
  let retainedCutoff;
  for (const input of request.previous) {
    const packet = validateOperationalValueEvidence(input);
    const contract = packet.facts.find(fact => fact["fact-kind"] === "contract");
    const metric = definition.metrics.find(metric => metric.id === contract["metric-id"]);
    assert.ok(metric && !retained.has(metric.id), "Unknown or duplicate retained Dependabot metric");
    const frozen = dependabotContract(metric);
    assert.equal(packet.id, packetId(metric));
    assert.equal(packet.repository, repository);
    assert.equal(packet.workflow, workflow);
    assert.equal(packet.contractDigest, frozen.digest, "Retained Dependabot contract changed");
    assert.deepEqual(contract, frozen.fact, "Retained Dependabot contract changed");
    assert.equal(Date.parse(packet.windowStart), adoption);
    const end = Date.parse(packet.windowEnd);
    assert.ok(end <= cutoff && (retainedCutoff === undefined || retainedCutoff === end),
      "Retained Dependabot cutoffs disagree");
    retainedCutoff = end;
    const snapshotId = `cutoff:${iso(end)}`;
    assert.ok(packet.facts.some(fact => fact["fact-kind"] === "snapshot" && fact["fact-id"] === snapshotId
      && Date.parse(fact.at) === end && fact["items-available"] === false
      && Number.isSafeInteger(fact.value) && fact.value >= 0)
      && packet.facts.some(fact => fact["fact-kind"] === "coverage" && fact["fact-id"] === snapshotId
        && Date.parse(fact["end-at"]) === end && Date.parse(fact.at) === end - week
        && fact["coverage-status"] === "complete"), "Incomplete retained Dependabot cutoff");
    retained.set(metric.id, packet);
  }
  if (retainedCutoff === cutoff) return null;
  read ??= githubReader(path.join(request.workspace.scratch, "requests"), undefined, {
    maxRequests: 111, includeHeaders: true,
    requirementFor: endpoint => {
      if (endpoint.startsWith(`repos/${repository}/dependabot/alerts?`)) return definition.acquisition.requirements[0];
      if (endpoint.startsWith("search/issues?")) return definition.acquisition.requirements[1];
      throw new Error("Unreviewed Dependabot acquisition endpoint");
    },
  });
  const evidence = buildEvidence({ ...acquire(read, request.observedAt, cutoff), repository,
    request: { windowStart: iso(cutoff - week), windowEnd: iso(cutoff), observedAt: request.observedAt } });
  return definition.metrics.map(metric => {
    const count = scoreMetric(metric.id, evidence);
    assert.ok(Number.isSafeInteger(count) && count >= 0, "Dependabot measurement is unavailable");
    const { fact, digest } = dependabotContract(metric);
    const at = iso(cutoff), id = `cutoff:${at}`;
    const packet = {
      schemaVersion: 1, id: packetId(metric), repository, workflow,
      sourcePath: definition.sourcePath, observedAt: request.observedAt,
      windowStart: definition.adoption.adoptedAt, windowEnd: at, contractDigest: digest,
      facts: [fact, ...(retained.get(metric.id)?.facts.filter(fact => fact["fact-kind"] !== "contract") ?? []),
        { "fact-kind": "snapshot", "fact-id": id, at, value: count, ref: id, "items-available": false },
        { "fact-kind": "coverage", "fact-id": id, at: iso(cutoff - week), "end-at": at, "coverage-status": "complete" }],
    };
    validateOperationalValueEvidence(packet);
    return { schema_version: 2, kind: "operational_value_evidence", operational_value_evidence: packet };
  });
}
