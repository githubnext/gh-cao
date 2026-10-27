import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { createHash } from "node:crypto";
import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import path from "node:path";
import { gzipSync } from "node:zlib";
import { githubApiBudgetConsumerFromEnvironment } from "../../../../activity/github-api-budget.mjs";
import { readJsonArchive } from "./json-archive.mjs";

export const digest = value => createHash("sha256").update(JSON.stringify(value)).digest("hex");
export const bytesDigest = value => createHash("sha256").update(value).digest("hex");
const instant = value => typeof value === "string" && Number.isFinite(Date.parse(value));
const repositoryPattern = /^[\w.-]+\/[\w.-]+$/;
const escape = value => value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");

export function writeEvidence(file, value) {
  const bytes = Buffer.from(`${JSON.stringify(value)}\n`);
  assert.ok(bytes.length <= 16 * 1024 * 1024, "Evidence exceeds decoded size budget");
  writeFileSync(file, gzipSync(bytes));
}

export function githubReader(directory, execute = execFileSync, options = {}) {
  mkdirSync(directory, { recursive: true });
  let requests = 0;
  const consume = options.consume ?? githubApiBudgetConsumerFromEnvironment();
  const maxRequests = options.maxRequests ?? 1000;
  assert.ok(Number.isSafeInteger(maxRequests) && maxRequests > 0 && maxRequests <= 1000,
    "Evidence API request budget is invalid");
  return (endpoint, query, binary = false) => {
    const key = binary ? [endpoint, query ?? null, "binary"] : [endpoint, query ?? null];
    const cache = path.join(directory, `${digest(key)}${binary ? ".zip" : ".json.gz"}`);
    if (existsSync(cache)) return binary ? readFileSync(cache) : readJsonArchive(cache);
    assert.ok(++requests <= maxRequests, "Evidence API request budget exceeded");
    consume?.();
    let value;
    try {
      const bytes = execute("gh", query ? ["api", "graphql", "-f", `query=${query}`] : ["api", endpoint],
        { encoding: binary ? null : "utf8", timeout: 60_000, maxBuffer: 16 * 1024 * 1024,
          stdio: ["pipe", "pipe", "pipe"] });
      value = binary ? bytes : JSON.parse(bytes);
      if (query) assert.ok(!value.errors?.length && value.data, "Incomplete GitHub graph response");
    } catch (error) {
      if (!query && /^repos\/[\w.-]+\/[\w.-]+\/actions\/runs\/\d+$/.test(endpoint)
        && /\(HTTP 404\)/.test(String(error.stderr))) {
        value = { unavailable: { status: 404, endpoint, reason: "Producer run is no longer available" } };
        console.error(`Evidence unavailable: ${endpoint} (404)`);
      } else throw error;
    }
    if (binary) writeFileSync(cache, value);
    else writeEvidence(cache, value);
    return value;
  };
}

export function workflowReference(body, { repository, aliases, slug, name, trackerIds = [slug] }) {
  assert.equal(typeof body, "string");
  const comments = [...body.matchAll(/<!--([\s\S]*?)-->/g)].map(match => match[1].trim());
  const ids = key => [...new Set(comments.flatMap(comment =>
    comment.match(new RegExp(`^gh-aw-${key}:\\s*([\\w-]+)$`))?.slice(1) ?? []))];
  const workflows = ids("workflow-id"), trackers = ids("tracker-id");
  assert.ok(workflows.length <= 1 && trackers.length <= 1, "Conflicting tracking identities");
  if (workflows.length && workflows[0] !== slug) return null;
  const candidates = [];
  for (const comment of comments) {
    if (comment.startsWith("gh-aw-execution:")) {
      const marker = JSON.parse(comment.slice("gh-aw-execution:".length));
      assert.ok(marker.version === 1 && typeof marker.run_url === "string", "Invalid execution marker");
      if (marker.aw_workflow_id !== slug) continue;
      const match = marker.run_url.match(/^https:\/\/github\.com\/([\w.-]+\/[\w.-]+)\/actions\/runs\/(\d+)(?:\/attempts\/(\d+))?$/);
      assert.ok(match && marker.repository?.toLowerCase() === match[1].toLowerCase()
        && String(marker.run_id) === match[2] && Number.isSafeInteger(Number(marker.run_attempt))
        && Number(marker.run_attempt) > 0
        && (!match[3] || Number(match[3]) === Number(marker.run_attempt)), "Conflicting execution identity");
      candidates.push({ repository: match[1], runId: match[2], attempt: Number(marker.run_attempt),
        marker: "gh-aw-execution-v1" });
    } else if (new RegExp(`^gh-aw-agentic-workflow:\\s*${escape(name)}(?:\\s*,|\\s*$)`).test(comment)) {
      const run = comment.match(/https:\/\/github\.com\/([\w.-]+\/[\w.-]+)\/actions\/runs\/(\d+)/);
      assert.ok(run, "Legacy workflow marker lacks a run reference");
      const declared = comment.match(/(?:^|,\s*)workflow_id:\s*([\w-]+)/)?.[1];
      const id = comment.match(/(?:^|,\s*)id:\s*(\d+)/)?.[1];
      assert.ok((!declared || declared === slug) && (!id || id === run[2]), "Conflicting legacy identity");
      candidates.push({ repository: run[1], runId: run[2], attempt: null, marker: "gh-aw-agentic-workflow" });
    }
  }
  const footer = new RegExp(`AI generated by \\[${escape(name)}\\]\\(https://github\\.com/([\\w.-]+/[\\w.-]+)/actions/runs/(\\d+)\\)`, "g");
  for (const match of body.matchAll(footer)) {
    candidates.push({ repository: match[1], runId: match[2], attempt: null, marker: "named-run-footer" });
  }
  const allowed = new Set([repository, ...aliases].map(value => value.toLowerCase()));
  assert.ok(candidates.every(item => allowed.has(item.repository.toLowerCase())), "Producer outside declared repository scope");
  assert.ok(new Set(candidates.map(item => item.runId)).size <= 1
    && new Set(candidates.flatMap(item => item.attempt === null ? [] : [item.attempt])).size <= 1,
  "Conflicting producer runs or attempts");
  const selected = candidates.find(item => item.marker === "gh-aw-execution-v1") ?? candidates[0];
  if (!selected && workflows[0] !== slug && !trackerIds.includes(trackers[0])) return null;
  return { ...(selected ?? { repository, runId: null, attempt: null, marker: "tracking-only" }),
    workflowId: slug, trackerId: trackers[0] ?? null };
}

export function validateProducer(reference, run, sourcePath, repository, aliases) {
  assert.ok(String(run.id) === reference.runId && [repository, ...aliases]
    .some(value => value.toLowerCase() === run.repository?.full_name?.toLowerCase())
    && String(run.path).split("@")[0] === sourcePath.replace(/\.md$/, ".lock.yml")
    && /^[a-f0-9]{40}$/.test(run.head_sha ?? "")
    && (reference.attempt === null || run.run_attempt === reference.attempt),
  "Producer run does not match workflow, repository, revision or attempt");
  return { status: "verified-run-reference", headSha: run.head_sha, attempt: run.run_attempt,
    sourcePath, createdAt: run.created_at, url: run.html_url,
    limitation: "Tracking and run references are not applied safe-output receipts." };
}

const referenceFields = "id number url repository { nameWithOwner }";
export function graphQuery(repository, numbers) {
  assert.ok(repositoryPattern.test(repository) && numbers.length > 0 && numbers.length <= 10
    && numbers.every(number => Number.isSafeInteger(number) && number > 0), "Invalid bounded graph scope");
  const [owner, name] = repository.split("/");
  const timeline = pull => `timelineItems(first:100,itemTypes:[CLOSED_EVENT,REOPENED_EVENT,CROSS_REFERENCED_EVENT${pull ? ",MERGED_EVENT" : ""}]) {
    nodes { __typename ... on ClosedEvent { createdAt closer { __typename ... on PullRequest { ${referenceFields} } } }
      ... on ReopenedEvent { createdAt } ${pull ? "... on MergedEvent { createdAt }" : ""}
      ... on CrossReferencedEvent { createdAt source { __typename
        ... on PullRequest { ${referenceFields} } ... on Issue { ${referenceFields} } } } }
    pageInfo { hasNextPage } }`;
  return `query { repository(owner:${JSON.stringify(owner)},name:${JSON.stringify(name)}) {
    ${numbers.map(number => `n${number}: issueOrPullRequest(number:${number}) { __typename
      ... on Issue { ${referenceFields} state stateReason createdAt updatedAt closedAt ${timeline(false)} }
      ... on PullRequest { ${referenceFields} state createdAt updatedAt closedAt mergedAt headRefOid
        mergeCommit { oid } closingIssuesReferences(first:100) { nodes { ${referenceFields} } pageInfo { hasNextPage } }
        ${timeline(true)} } }`).join("\n")} } }`;
}

export function normalizeGraph(value, observedAt, repository) {
  const reference = entity => {
    assert.ok(entity?.id && Number.isSafeInteger(entity.number) && entity.number > 0
      && repositoryPattern.test(entity.repository?.nameWithOwner ?? "")
      && ["issues", "pull"].some(kind =>
        entity.url === `https://github.com/${entity.repository.nameWithOwner}/${kind}/${entity.number}`),
    "Invalid graph entity identity");
    return { id: entity.id, number: entity.number, repository: entity.repository.nameWithOwner, url: entity.url };
  };
  const identity = reference(value);
  assert.ok(identity.repository.toLowerCase() === repository.toLowerCase()
    && ["Issue", "PullRequest"].includes(value.__typename) && instant(observedAt)
    && instant(value.createdAt) && instant(value.updatedAt)
    && [value.closedAt, value.mergedAt].every(at => at == null || instant(at))
    && Array.isArray(value.timelineItems?.nodes) && typeof value.timelineItems.pageInfo?.hasNextPage === "boolean",
  "Incomplete GitHub entity graph");
  const edges = [], events = [];
  for (const event of value.timelineItems.nodes) {
    assert.ok(instant(event.createdAt), "Graph event lacks a timestamp");
    const type = { ClosedEvent: "closed", ReopenedEvent: "reopened", MergedEvent: "merged", CrossReferencedEvent: "referenced" }[event.__typename];
    assert.ok(type, "Unexpected graph event");
    const projected = { type, at: event.createdAt };
    if (type === "closed" && event.closer?.__typename === "PullRequest") {
      const target = reference(event.closer);
      edges.push({ relation: "closed-by", at: event.createdAt, target });
      projected.closerId = target.id;
    }
    if (type === "referenced") edges.push({ relation: "referenced-by", at: event.createdAt, target: reference(event.source) });
    events.push(projected);
  }
  let closingLinksComplete = true;
  if (value.__typename === "PullRequest") {
    assert.ok(Array.isArray(value.closingIssuesReferences?.nodes)
      && typeof value.closingIssuesReferences.pageInfo?.hasNextPage === "boolean", "Missing closing relationship coverage");
    for (const issue of value.closingIssuesReferences.nodes) edges.push({ relation: "declares-closing", target: reference(issue) });
    closingLinksComplete = !value.closingIssuesReferences.pageInfo.hasNextPage;
  }
  return { ...identity, kind: value.__typename, state: value.state, stateReason: value.stateReason ?? null,
    createdAt: value.createdAt, updatedAt: value.updatedAt, closedAt: value.closedAt ?? null,
    mergedAt: value.mergedAt ?? null, mergeCommit: value.mergeCommit?.oid ?? null, observedAt,
    historyComplete: !value.timelineItems.pageInfo.hasNextPage, closingLinksComplete, edges, events };
}

export function readTimeline(request, repository, number, until) {
  const events = [];
  let complete = false;
  for (let page = 1; page <= 5; page++) {
    const rows = request(`repos/${repository}/issues/${number}/timeline?per_page=100&page=${page}`);
    assert.ok(Array.isArray(rows), "Invalid issue timeline");
    for (const row of rows) {
      if (!["assigned", "unassigned", "connected", "disconnected", "cross-referenced", "closed", "reopened",
        "copilot_work_started", "copilot_work_finished", "merged"].includes(row.event)) continue;
      assert.ok(instant(row.created_at), "Timeline event lacks a timestamp");
      if (Date.parse(row.created_at) >= Date.parse(until)) continue;
      const event = { type: row.event, at: row.created_at };
      const subject = row.subject ?? row.source?.issue;
      if (subject && (subject.repository?.full_name ?? subject.repository_url?.replace("https://api.github.com/repos/", ""))
        ?.toLowerCase() === repository.toLowerCase()) {
        assert.ok(Number.isSafeInteger(subject.number) && subject.number > 0, "Invalid timeline target");
        event.target = { number: subject.number, title: subject.title,
          kind: subject.type === "pull_request" || subject.pull_request ? "pull-request" : "issue" };
      }
      events.push(event);
    }
    if (rows.length < 100) { complete = true; break; }
  }
  return { complete, events };
}
