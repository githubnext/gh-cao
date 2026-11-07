#!/usr/bin/env node

import { execFileSync } from "node:child_process";

const REPOSITORY = /^[A-Za-z0-9][A-Za-z0-9-]*\/[A-Za-z0-9._-]+$/;

export const definition = {
  schemaVersion: 3,
  slug: "dependabot-update-planner",
  repository: "githubnext/gh-cao",
  workflowName: "Dependabot / Update Planner",
  sourcePath: ".github/workflows/dependabot-update-planner.md",
  adoption: {
    commit: "7f31a746d534f128d92221edcad975972c361c0f",
    adoptedAt: "2026-09-17T08:48:00Z",
    baselineCommit: "68bfddaf467b0b6ee608a7fe0827662ba3673f78",
    baselineAt: "2026-09-17T06:14:04Z",
  },
  evaluation: {
    mode: "baseline-comparable",
  },
  evidence: {
    key: "dependabot-security-risk",
    measurementKind: "snapshot",
    repositories: ["github/gh-aw"],
    opportunity: "A Dependabot security alert in an authorized target repository that was open at the immutable observation cutoff.",
    filters: [
      "Count alerts created no later than the cutoff and not fixed, dismissed, or auto-dismissed by that cutoff.",
      "Count critical and high alerts separately without severity weighting.",
      "Count Dependabot-authored pull requests created no later than the cutoff and not closed by that cutoff as a separate queue diagnostic.",
      "Fail the observation closed when complete alert or pull-request lifecycle evidence is unavailable.",
    ],
    collection: "Fetch Dependabot alert lifecycle records and Dependabot-authored pull-request lifecycle records once per supported repository, then reconstruct every requested cutoff locally from their authoritative timestamps.",
    window: {
      durationDays: 7,
      cadenceDays: 7,
      maturationDays: 0,
    },
    zeroRule: "Complete lifecycle evidence with no alert or pull request open at the cutoff records zero.",
    missingRule: "Inaccessible, truncated, malformed, or incomplete lifecycle evidence records missing rather than zero.",
  },
  acquisition: {
    requirements: [
      {
        source: "github",
        pool: "core",
        primitive: "github.dependabot-alert-lifecycle.v1",
        scope: "github/gh-aw",
        cache: "invocation",
      },
      {
        source: "github",
        pool: "search",
        primitive: "github.dependabot-pull-request-lifecycle.v1",
        scope: "github/gh-aw",
        cache: "invocation",
      },
    ],
  },
  model: {
    architecture: "Problem-first repository-risk measurement independent of gh-aw outputs, recommendations, runs, and engagement.",
    recommendation: "Use total open Dependabot security alerts as the primary outcome and retain critical/high alerts and the Dependabot pull-request queue as separate diagnostics. Count improvement regardless of actor and describe adoption-timed movement as association.",
    presentation: {
      label: "Dependabot security risk",
      betterLabel: "Fewer open security alerts and Dependabot pull requests",
    },
  },
  summary: {
    nativeLabel: "Open Dependabot security-alert backlog",
  },
  metricDisposition: {
    retained: [
      "Total open Dependabot security alerts are the primary repository outcome.",
      "Open critical/high Dependabot security alerts are a severity diagnostic.",
      "Open Dependabot-authored pull requests are a dependency-maintenance queue diagnostic.",
    ],
    rejected: [
      "Plan creation, assignment, comments, checklist progress, linked pull requests, issue closure, workflow runs, and agent assessments are gh-aw mechanism signals.",
    ],
  },
  metrics: [
    {
      id: "open-security-alert-count",
      name: "Open security alerts",
      role: "primary",
      formula: "Dependabot security alerts open at the cutoff",
      direction: "decrease",
      unit: "alerts",
      presentation: {
        name: "Open security alerts",
        legendLabel: "Security alerts",
        transform: "identity",
      },
    },
    {
      id: "open-high-critical-alert-count",
      name: "Open critical/high security alerts",
      role: "diagnostic",
      formula: "Dependabot security alerts with critical or high severity open at the cutoff",
      direction: "decrease",
      unit: "alerts",
      presentation: {
        name: "Open critical/high alerts",
        legendLabel: "Critical/high alerts",
        transform: "identity",
      },
    },
    {
      id: "open-dependabot-pr-count",
      name: "Open Dependabot pull requests",
      role: "diagnostic",
      formula: "Dependabot-authored dependency pull requests open at the cutoff",
      direction: "decrease",
      unit: "pull-requests",
      presentation: {
        name: "Open Dependabot pull requests",
        legendLabel: "Dependabot PRs",
        transform: "identity",
      },
    },
  ],
  validationExamples: {
    targetAttained: {
      valid: true,
      openSecurityAlertCount: 0,
      openHighCriticalAlertCount: 0,
      openDependabotPullRequestCount: 0,
    },
    targetMissed: {
      valid: true,
      openSecurityAlertCount: 8,
      openHighCriticalAlertCount: 3,
      openDependabotPullRequestCount: 6,
    },
    missing: { valid: false },
    malformed: {
      valid: true,
      openSecurityAlertCount: "eight",
      openHighCriticalAlertCount: 3,
      openDependabotPullRequestCount: 6,
    },
  },
};

const metricFields = {
  "open-security-alert-count": "openSecurityAlertCount",
  "open-high-critical-alert-count": "openHighCriticalAlertCount",
  "open-dependabot-pr-count": "openDependabotPullRequestCount",
};

function finiteNonNegativeInteger(value) {
  return Number.isInteger(value) && value >= 0;
}

export function scoreMetric(id, evidence) {
  const valid = evidence?.valid === true
    && finiteNonNegativeInteger(evidence.openSecurityAlertCount)
    && finiteNonNegativeInteger(evidence.openHighCriticalAlertCount)
    && evidence.openHighCriticalAlertCount <= evidence.openSecurityAlertCount
    && finiteNonNegativeInteger(evidence.openDependabotPullRequestCount);
  if (!valid) return null;
  const field = metricFields[id];
  return field ? evidence[field] : null;
}

function parseTime(value) {
  const timestamp = Date.parse(value);
  return Number.isFinite(timestamp) ? timestamp : null;
}

function terminalAt(record) {
  return [
    record.fixed_at,
    record.dismissed_at,
    record.auto_dismissed_at,
    record.closed_at,
  ].map(parseTime).filter((value) => value !== null).sort((left, right) => left - right)[0] ?? null;
}

export function isOpenAt(record, cutoff) {
  const createdAt = parseTime(record?.created_at);
  const cutoffAt = parseTime(cutoff);
  if (createdAt === null || cutoffAt === null || createdAt > cutoffAt) return false;
  const endedAt = terminalAt(record);
  return endedAt === null || endedAt > cutoffAt;
}

export function buildEvidence({
  alerts,
  pullRequests,
  repository,
  request,
}) {
  if (!Array.isArray(alerts) || !Array.isArray(pullRequests)) return { valid: false };
  const openAlerts = alerts.filter((alert) => isOpenAt(alert, request.windowEnd));
  const openPullRequests = pullRequests.filter((pullRequest) => isOpenAt(
    pullRequest,
    request.windowEnd,
  ));
  return {
    valid: true,
    openSecurityAlertCount: openAlerts.length,
    openHighCriticalAlertCount: openAlerts.filter(({ severity }) => (
      severity === "critical" || severity === "high"
    )).length,
    openDependabotPullRequestCount: openPullRequests.length,
    maturityStatus: "matured",
    dubious: false,
    key: definition.evidence.key,
    repositories: [repository],
    opportunity: definition.evidence.opportunity,
    filters: definition.evidence.filters,
    collection: definition.evidence.collection,
    window: definition.evidence.window,
  };
}

function run(command, args) {
  return execFileSync(command, args, {
    encoding: "utf8",
    env: process.env,
    maxBuffer: 128 * 1024 * 1024,
    stdio: ["ignore", "pipe", "pipe"],
  });
}

function api(endpoint, fields = []) {
  const output = run("gh", [
    "api", "--method", "GET", "--paginate", "--slurp", endpoint,
    ...fields.flatMap(([name, value]) => ["-f", `${name}=${value}`]),
  ]);
  const pages = JSON.parse(output);
  if (!Array.isArray(pages)) throw new Error(`GitHub API returned malformed evidence for ${endpoint}`);
  return pages;
}

function alertEvidence(repository) {
  return api(`repos/${repository}/dependabot/alerts`, [
    ["state", "auto_dismissed,dismissed,fixed,open"],
    ["per_page", "100"],
  ]).flat().map((alert) => ({
    created_at: alert.created_at,
    fixed_at: alert.fixed_at,
    dismissed_at: alert.dismissed_at,
    auto_dismissed_at: alert.auto_dismissed_at,
    severity: alert.security_advisory?.severity,
  }));
}

function pullRequestEvidence(repository, latestCutoff) {
  const query = `query($searchQuery:String!,$endCursor:String) {
    search(query:$searchQuery,type:ISSUE,first:100,after:$endCursor) {
      issueCount
      nodes {
        ... on PullRequest {
          createdAt
          closedAt
        }
      }
      pageInfo {
        hasNextPage
        endCursor
      }
    }
  }`;
  const output = run("gh", [
    "api", "graphql", "--paginate", "--slurp",
    "-f", `query=${query}`,
    "-f", `searchQuery=repo:${repository} is:pr author:app/dependabot created:<=${latestCutoff}`,
  ]);
  const pages = JSON.parse(output);
  if (!Array.isArray(pages)
      || pages.some((page) => !Array.isArray(page.data?.search?.nodes))
      || pages.some((page) => page.data.search.issueCount > 1_000)) {
    throw new Error(`GitHub GraphQL returned incomplete Dependabot pull request evidence for ${repository}`);
  }
  return pages.flatMap((page) => page.data.search.nodes).map((pullRequest) => ({
    created_at: pullRequest.createdAt,
    closed_at: pullRequest.closedAt,
  }));
}

function validRequest(request) {
  return request
    && typeof request.windowStart === "string"
    && typeof request.windowEnd === "string"
    && typeof request.observedAt === "string"
    && parseTime(request.windowStart) !== null
    && parseTime(request.windowEnd) !== null
    && parseTime(request.observedAt) !== null
    && parseTime(request.windowStart) < parseTime(request.windowEnd);
}

export async function collectBatch(requests) {
  if (!Array.isArray(requests) || requests.length === 0 || requests.some((request) => !validRequest(request))) {
    throw new Error("collectBatch requires valid observation windows");
  }
  const supported = new Set(definition.evidence.repositories.map((repository) => repository.toLowerCase()));
  const grouped = Map.groupBy(requests, (request) => (
    request.repository ?? definition.evidence.repositories[0]
  ));
  const results = new Map();
  for (const [repository, repositoryRequests] of grouped) {
    if (!REPOSITORY.test(repository) || !supported.has(repository.toLowerCase())) {
      throw new Error(`Unsupported evidence repository: ${repository}`);
    }
    const alerts = alertEvidence(repository);
    const latestCutoff = repositoryRequests.map(({ windowEnd }) => windowEnd).toSorted().at(-1);
    const pullRequests = pullRequestEvidence(repository, latestCutoff);
    for (const request of repositoryRequests) {
      results.set(request, {
        evidence: buildEvidence({
          alerts,
          pullRequests,
          repository,
          request,
        }),
        provenance: [
          { repository, kind: "dependabot-alert-lifecycle", ref: request.windowEnd },
          { repository, kind: "dependabot-pull-request-lifecycle", ref: request.windowEnd },
        ],
      });
    }
  }
  return requests.map((request) => results.get(request));
}
