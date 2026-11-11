import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { parse } from "yaml";
import { buildInventoryDashboardSources, markInventoryCollectionPartial } from "../../activity/inventory-sources.mjs";
import { root, workflow } from "./workflow-contract.helpers.mjs";

const HOUR = 60 * 60 * 1000;
const AUDIT_EXCLUSION_REASON = "Downloaded shard included out-of-scope or unscoped audit records; collection is partial.";
const RATE_LIMIT_REASON = "GitHub API capacity exhausted; activity collection is partial and retained runs may be stale.";

function frontmatter(source) {
  const match = source.match(/^---\n([\s\S]*?)\n---\n/);
  assert.ok(match, "workflow frontmatter is present");
  return parse(match[1]);
}

const prefetchStep = frontmatter(workflow("self-care-open-source-failures.md")).steps
  .find((step) => step.name === "Prepare bounded public failure evidence");

function hoursAgo(hours) {
  return new Date(Date.now() - hours * HOUR).toISOString().replace(/\.\d{3}Z$/, "Z");
}

function workflowRunsEnvelope(repository, runs) {
  return {
    schema_version: 2,
    kind: "workflow_runs",
    request: { host: "github.com", repository, args: ["run", "list", "--repo", repository] },
    payload: runs.map((run) => ({
      databaseId: run.id,
      attempt: 1,
      number: run.id,
      workflowName: run.workflowName,
      displayTitle: run.workflowName,
      event: "schedule",
      status: "completed",
      conclusion: run.conclusion,
      headBranch: "main",
      headSha: "0123456789abcdef0123456789abcdef01234567",
      createdAt: hoursAgo(run.hours),
      startedAt: hoursAgo(run.hours),
      updatedAt: hoursAgo(run.hours),
      url: `https://github.com/${repository}/actions/runs/${run.id}`,
    })),
  };
}

function runEnvelope(repository, run) {
  const [organization] = repository.split("/");
  const failed = run.conclusion !== "success";
  return {
    schema_version: 2,
    kind: "run",
    run: {
      run_id: run.id,
      run_attempt: "1",
      organization,
      repository,
      workflow_name: run.workflowName,
      workflow_path: run.workflowPath,
      display_title: run.workflowName,
      event: "schedule",
      status: "completed",
      conclusion: run.conclusion,
      created_at: hoursAgo(run.hours),
      started_at: hoursAgo(run.hours),
      updated_at: hoursAgo(run.hours),
      job_details: failed
        ? [{ name: "agent", conclusion: "failure", steps: [{ name: "Run agent", conclusion: "failure" }] }]
        : [{ name: "agent", conclusion: "success", steps: [{ name: "Run agent", conclusion: "success" }] }],
      ...(run.failureMessage ? { failure_message: run.failureMessage } : {}),
    },
  };
}

function daily(id, hours, conclusion, extra = {}) {
  return { id, hours, conclusion, workflowName: "Daily", workflowPath: ".github/workflows/daily.md", ...extra };
}

const PUBLIC_RUNS = [
  daily(11, 2, "failure", { failureMessage: "agent exited with code 1" }),
  daily(12, 5, "success"),
  daily(13, 30, "timed_out"),
  daily(14, 200, "failure"),
];
const PRIVATE_RUNS = [daily(21, 3, "failure"), daily(22, 200, "success")];

function buildSnapshot({
  asOf = new Date().toISOString(),
  publicRuns = PUBLIC_RUNS,
  privateRuns = PRIVATE_RUNS,
  repositoryFailures = [[["octo/public", "octo/private"], AUDIT_EXCLUSION_REASON]],
} = {}) {
  const directory = mkdtempSync(join(tmpdir(), "cao-open-source-failures-"));
  const activityRoot = join(directory, "cao-activity");
  const shards = join(directory, "shards");
  mkdirSync(activityRoot, { recursive: true });
  mkdirSync(shards, { recursive: true });

  const records = [
    workflowRunsEnvelope("octo/public", publicRuns),
    ...publicRuns.map((run) => runEnvelope("octo/public", run)),
    workflowRunsEnvelope("octo/private", privateRuns),
    ...privateRuns.map((run) => runEnvelope("octo/private", run)),
  ];
  writeFileSync(join(shards, "gh-aw-logs-000.jsonl"), `${records.map((record) => JSON.stringify(record)).join("\n")}\n`);
  execFileSync(process.execPath, [
    "activity/cao.mjs", "ingest-jsonl",
    "--database", join(activityRoot, "gh-aw-logs.sqlite"),
    "--input-dir", shards,
    "--retention-days", "all",
    "--run-retention-days", "all",
  ], { cwd: root, stdio: ["ignore", "ignore", "inherit"] });

  const inventory = buildInventoryDashboardSources({
    inventory: { workflows: [], bundles: [], campaigns: [] },
    controlSettings: {},
    discoveredRepositories: [
      { full_name: "octo/public", visibility: "public", private: false },
      { full_name: "octo/private", visibility: "private", private: true },
    ],
    repository: "octo/public",
    generatedAt: asOf,
  });
  for (const [repositories, reason] of repositoryFailures) markInventoryCollectionPartial(inventory, repositories, reason);
  writeFileSync(join(activityRoot, "inventory-sources.json"), `${JSON.stringify(inventory, null, 2)}\n`);
  return { directory, activityRoot };
}

function runPrefetch(activityRoot) {
  const directory = mkdtempSync(join(tmpdir(), "cao-open-source-evidence-"));
  const evidencePath = join(directory, "evidence.json");
  try {
    execFileSync("bash", ["-e", "-c", prefetchStep.run], {
      cwd: root,
      env: {
        PATH: process.env.PATH,
        ACTIVITY_ROOT: activityRoot,
        EVIDENCE_PATH: evidencePath,
        GITHUB_SERVER_URL: "https://github.com",
      },
      stdio: ["ignore", "ignore", "inherit"],
    });
    return JSON.parse(readFileSync(evidencePath, "utf8"));
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
}

function withSnapshot(options, assertion) {
  const { directory, activityRoot } = buildSnapshot(options);
  try {
    assertion(runPrefetch(activityRoot));
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
}

test("SelfCare open source failures derives complete public failure evidence from a fresh Activity snapshot", () => {
  assert.ok(prefetchStep, "prefetch step is declared");
  withSnapshot({}, (evidence) => {
    assert.equal(evidence.status, "complete", evidence.reason);
    assert.equal(evidence.evidenceWindowHours, 168);
    assert.ok(Date.now() - Date.parse(evidence.snapshotGeneratedAt) < 2 * HOUR);
    assert.equal(evidence.repositoryScope, "public");
    assert.deepEqual(evidence.projects, ["octo/public"]);
    assert.deepEqual(evidence.allowedRepositories, ["octo/public"]);
    assert.equal(evidence.projectCount, 1);
    assert.equal(evidence.failureCount, 2);
    assert.equal(evidence.truncated, false);
    assert.deepEqual(evidence.failures.map(({ runId, conclusion }) => [runId, conclusion]), [
      [11, "failure"],
      [13, "timed_out"],
    ]);
    assert.deepEqual(evidence.failures[0], {
      repository: "octo/public",
      workflow: ".github/workflows/daily.md",
      workflowName: "Daily",
      runId: 11,
      runAttempt: 1,
      createdAt: evidence.failures[0].createdAt,
      conclusion: "failure",
      failureJob: "agent",
      failureStep: "Run agent",
      failureMessage: "agent exited with code 1",
      url: "https://github.com/octo/public/actions/runs/11",
    });
  });
});

test("SelfCare open source failures reports a cache miss as an unavailable snapshot", () => {
  const directory = mkdtempSync(join(tmpdir(), "cao-open-source-empty-"));
  try {
    const evidence = runPrefetch(directory);
    assert.equal(evidence.status, "incomplete");
    assert.equal(evidence.reason, "CAO activity snapshot is unavailable");
    assert.deepEqual(evidence.failures, []);
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});

test("SelfCare open source failures rejects a stale Activity snapshot", () => {
  withSnapshot({ asOf: new Date(Date.now() - 3 * HOUR).toISOString() }, (evidence) => {
    assert.equal(evidence.status, "incomplete");
    assert.equal(evidence.reason, "CAO activity snapshot is stale or has an invalid generatedAt");
  });
});

test("SelfCare open source failures rejects a snapshot shorter than seven days", () => {
  withSnapshot({ publicRuns: PUBLIC_RUNS.filter((run) => run.hours < 168) }, (evidence) => {
    assert.equal(evidence.status, "incomplete");
    assert.equal(evidence.reason, "CAO activity run-health window is shorter than seven days");
  });
});

test("SelfCare open source failures rejects public run-health collection gaps", () => {
  withSnapshot({ repositoryFailures: [[["octo/public"], RATE_LIMIT_REASON]] }, (evidence) => {
    assert.equal(evidence.status, "incomplete");
    assert.equal(evidence.reason, "CAO activity run-health coverage is unavailable or incomplete");
  });
});

test("SelfCare open source failures reads only Activity files that are saved and restored", () => {
  const read = [...prefetchStep.run.matchAll(/path\.join\(process\.env\.ACTIVITY_ROOT, "([^"]+)"\)/g)].map((match) => match[1]);
  assert.deepEqual(read.sort(), ["gh-aw-logs.sqlite", "inventory-sources.json"]);

  const activitySteps = Object.values(parse(readFileSync(join(root, ".github/workflows/cao-activity.yml"), "utf8")).jobs)
    .flatMap((job) => job.steps ?? []);
  const verify = activitySteps.find((step) => step.name === "Verify activity snapshot");
  const save = activitySteps.find((step) => step.name === "Save activity cache");
  assert.ok(verify && save, "Activity verifies and saves its snapshot");

  const cache = frontmatter(readFileSync(join(root, ".github/workflows/shared/activity-cache.md"), "utf8"));
  const restorePaths = ["activation", "agent"].map((job) => {
    const restore = (cache.jobs?.[job]?.["pre-steps"] ?? [])
      .find((step) => String(step.uses ?? "").startsWith("actions/cache/restore@"));
    assert.ok(restore, `${job} restores the Activity cache`);
    return String(restore.with.path).split("\n").map((line) => line.trim());
  });

  for (const file of read) {
    assert.match(verify.run, new RegExp(`(^|\\s)${file.replaceAll(".", "\\.")}(\\s|\\\\|$)`, "m"), `${file} is verified`);
    assert.ok(String(save.with.path).split("\n").map((line) => line.trim()).includes(`\${{ runner.temp }}/cao-activity/${file}`), `${file} is saved`);
    for (const paths of restorePaths) assert.ok(paths.includes(`\${{ runner.temp }}/cao-activity/${file}`), `${file} is restored`);
  }
});
