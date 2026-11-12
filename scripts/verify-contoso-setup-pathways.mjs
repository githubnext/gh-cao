import { spawnSync } from "node:child_process";

const host = "contoso-aw.ghe.com";
const maximumAgeMs = 36 * 60 * 60 * 1000;
const timeoutMs = 20 * 60 * 1000;
const pollIntervalMs = 10_000;
const requiredCampaignFiles = [
  "repo-assist.md",
  "repo-assist.lock.yml",
  "repo-assist-issue-triage.md",
  "repo-assist-issue-triage.lock.yml",
  "repo-assist-issue-fix.md",
  "repo-assist-issue-fix.lock.yml",
  "repo-assist-maintenance.md",
  "repo-assist-maintenance.lock.yml",
  "repo-assist-pr-upkeep.md",
  "repo-assist-pr-upkeep.lock.yml",
];
const pathways = [
  {
    name: "organization App",
    repository: "platform/cao-auth-e2e-org-app",
    targetRepository: "platform/cao-auth-e2e-org-app",
  },
  {
    name: "enterprise App",
    repository: "platform/cao-auth-e2e-enterprise-app",
    targetRepository: "platform/cao-auth-e2e-enterprise-app",
  },
  {
    name: "fine-grained PAT",
    repository: "platform/cao-auth-e2e-token",
    targetRepository: "platform/aw-playground",
  },
];

function gh(arguments_, { allowFailure = false } = {}) {
  const result = spawnSync("gh", arguments_, {
    encoding: "utf8",
    env: { ...process.env, GH_HOST: host },
    maxBuffer: 20 * 1024 * 1024,
  });
  if (!allowFailure && (result.error || result.status !== 0)) {
    const message = (result.stderr || result.error?.message || `exit ${result.status}`).trim();
    throw new Error(`Contoso AW command failed: gh ${arguments_.join(" ")}: ${message}`);
  }
  return {
    status: result.status,
    stdout: result.stdout.trim(),
    stderr: result.stderr.trim(),
  };
}

function ghApi(endpoint) {
  return JSON.parse(gh(["api", endpoint]).stdout);
}

function rawFile(repository, path) {
  return gh([
    "api",
    `repos/${repository}/contents/${path}`,
    "-H",
    "Accept: application/vnd.github.raw+json",
  ]).stdout;
}

function defaultHead(repository) {
  const metadata = ghApi(`repos/${repository}`);
  return ghApi(`repos/${repository}/git/ref/heads/${metadata.default_branch}`).object.sha;
}

function workflowRuns(repository, workflow, perPage = 50) {
  return ghApi(`repos/${repository}/actions/workflows/${workflow}/runs?per_page=${perPage}`)
    .workflow_runs ?? [];
}

function workflow(repository, workflowPath) {
  return ghApi(`repos/${repository}/actions/workflows/${workflowPath}`);
}

function assertRecentSuccessfulRun(pathway, workflowFile, label, predicate = () => true) {
  const head = defaultHead(pathway.repository);
  const run = workflowRuns(pathway.repository, workflowFile)
    .find((candidate) => candidate.head_sha === head
      && candidate.status === "completed"
      && candidate.conclusion === "success"
      && predicate(candidate));
  if (!run) {
    const latest = workflowRuns(pathway.repository, workflowFile, 1)[0];
    const suffix = latest
      ? `; latest is ${latest.status}/${latest.conclusion ?? "unknown"} at ${latest.html_url}`
      : "; no run exists";
    throw new Error(`${pathway.repository} has no successful ${label} run for ${head}${suffix}`);
  }
  const ageMs = Date.now() - Date.parse(run.created_at);
  if (!Number.isFinite(ageMs) || ageMs < 0 || ageMs > maximumAgeMs) {
    throw new Error(`${pathway.repository} successful ${label} run is stale: ${run.created_at}`);
  }
  return run;
}

function verifyPages(pathway) {
  const pages = ghApi(`repos/${pathway.repository}/pages`);
  if (pages.build_type !== "workflow") {
    throw new Error(`${pathway.repository} Pages build type is ${pages.build_type}, expected workflow`);
  }
  if (pages.public !== false) {
    throw new Error(`${pathway.repository} Pages must remain access-controlled`);
  }
  if (!pages.html_url) {
    throw new Error(`${pathway.repository} has no dedicated Pages URL`);
  }
  return pages.html_url;
}

function verifyCampaign(pathway) {
  const policy = JSON.parse(rawFile(pathway.repository, ".github/workflows/cao.json"));
  const campaign = policy["control-plane"]?.campaigns?.["repo-assist"];
  if (!campaign) throw new Error(`${pathway.repository} does not declare the Repo Assist campaign`);
  if (campaign.mode !== "live") {
    throw new Error(`${pathway.repository} Repo Assist policy mode is ${campaign.mode}; live authority is required for the E2E`);
  }
  const files = new Set(ghApi(`repos/${pathway.repository}/contents/.github/workflows?per_page=100`)
    .map(({ name }) => name));
  const missing = requiredCampaignFiles.filter((name) => !files.has(name));
  if (missing.length > 0) {
    throw new Error(`${pathway.repository} is missing compiled Repo Assist files: ${missing.join(", ")}`);
  }
  for (const workflowPath of [
    "repo-assist.lock.yml",
    "repo-assist-issue-triage.lock.yml",
    "repo-assist-issue-fix.lock.yml",
    "repo-assist-maintenance.lock.yml",
    "repo-assist-pr-upkeep.lock.yml",
  ]) {
    const installed = workflow(pathway.repository, workflowPath);
    if (installed.state !== "active") {
      throw new Error(`${pathway.repository} workflow ${workflowPath} is ${installed.state}`);
    }
  }
}

function reviewIssue(pathway, targetIssue) {
  const expected = `${pathway.targetRepository} issue ${targetIssue} triage guidance`;
  return ghApi(`repos/${pathway.repository}/issues?state=all&per_page=100`)
    .find((issue) => issue.title.includes("[repo-assist:issue-triage]")
      && issue.title.includes(expected));
}

function targetIssueState(repository, issueNumber) {
  const issue = ghApi(`repos/${repository}/issues/${issueNumber}`);
  const comments = ghApi(`repos/${repository}/issues/${issueNumber}/comments?per_page=100`);
  return {
    state: issue.state,
    title: issue.title,
    body: issue.body ?? "",
    labels: issue.labels.map(({ name }) => name).sort(),
    comments: comments.map(({ id, body }) => ({ id, body })),
  };
}

function diagnoseFailure(repository, run) {
  const logs = gh([
    "run",
    "view",
    String(run.id),
    "--repo",
    repository,
    "--log-failed",
  ], { allowFailure: true });
  const text = `${logs.stdout}\n${logs.stderr}`;
  if (/model_not_supported|requested model is not supported|Model is not supported/i.test(text)) {
    return "the configured agent engine/model is unsupported; use the Copilot engine or a model available on this host";
  }
  if (/\/installation\b.*(?:404|Not Found)|A JSON web token could not be decoded/i.test(text)) {
    return "the write GitHub App is not installed on the repository receiving review output";
  }
  if (/Bad credentials|Resource not accessible by integration|HTTP 403/i.test(text)) {
    return "the configured credential cannot perform the required repository operation";
  }
  if (/report_incomplete|infrastructure_error/i.test(text)) {
    return "the agent reported an incomplete infrastructure result; inspect the failed run logs";
  }
  return "inspect the failed run logs";
}

async function waitForRun(repository, workflowFile, createdAfter, displayTitle) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    const run = workflowRuns(repository, workflowFile)
      .find((candidate) => Date.parse(candidate.created_at) >= createdAfter
        && (!displayTitle || candidate.display_title === displayTitle));
    if (!run || run.status !== "completed") {
      await new Promise((resolve) => setTimeout(resolve, pollIntervalMs));
      continue;
    }
    if (run.conclusion !== "success") {
      throw new Error(
        `${repository} ${displayTitle || workflowFile} failed: ${run.html_url}; ${diagnoseFailure(repository, run)}`,
      );
    }
    return run;
  }
  throw new Error(`Timed out waiting for ${repository} ${displayTitle || workflowFile}`);
}

async function dispatchWorkflow(pathway, workflowFile, fields = {}) {
  const createdAfter = Date.now() - 5_000;
  const arguments_ = ["workflow", "run", workflowFile, "--repo", pathway.repository];
  for (const [name, value] of Object.entries(fields)) {
    arguments_.push("-f", `${name}=${value}`);
  }
  gh(arguments_);
  return waitForRun(pathway.repository, workflowFile, createdAfter);
}

async function dispatchCampaign(pathway, targetIssue, mode) {
  const createdAfter = Date.now() - 5_000;
  gh([
    "workflow",
    "run",
    "repo-assist.lock.yml",
    "--repo",
    pathway.repository,
    "-f",
    `target_repo=${pathway.targetRepository}`,
    "-f",
    `safe_output_repo=${pathway.repository}`,
    "-f",
    `safe_output_mode=${mode}`,
    "-F",
    "max_repos=1",
    "-F",
    "rollout_percent=100",
  ]);
  const orchestratorTitle = `Repo Assist · ${pathway.targetRepository} · ${mode}`;
  const orchestrator = await waitForRun(
    pathway.repository,
    "repo-assist.lock.yml",
    createdAfter,
    orchestratorTitle,
  );
  const workerTitle = `Repo Assist issue triage · ${pathway.targetRepository} · ${mode}`;
  const worker = await waitForRun(
    pathway.repository,
    "repo-assist-issue-triage.lock.yml",
    Date.parse(orchestrator.created_at),
    workerTitle,
  );
  if (worker.head_sha !== orchestrator.head_sha) {
    throw new Error(`${pathway.repository} ${mode} worker ran at ${worker.head_sha}, expected ${orchestrator.head_sha}`);
  }
  if (mode === "review" && !reviewIssue(pathway, targetIssue)) {
    throw new Error(`${pathway.repository} review run did not create routed guidance for ${pathway.targetRepository}#${targetIssue}`);
  }
  return { orchestrator, worker };
}

function closeOpenIssues(repository) {
  const issues = ghApi(`repos/${repository}/issues?state=open&per_page=100`)
    .filter((issue) => !issue.pull_request);
  for (const issue of issues) {
    gh(["issue", "close", String(issue.number), "--repo", repository, "--reason", "not planned"]);
  }
}

function createSyntheticIssue(pathway) {
  closeOpenIssues(pathway.targetRepository);
  if (pathway.targetRepository !== pathway.repository) closeOpenIssues(pathway.repository);
  const title = `E2E triage: classify unsupported sample behavior ${Date.now()}`;
  const result = gh([
    "issue",
    "create",
    "--repo",
    pathway.targetRepository,
    "--title",
    title,
    "--body",
    "The sample command documents behavior that the implementation does not support. Classify and explain the smallest maintainer action.",
  ]);
  const match = /\/issues\/([1-9][0-9]*)$/.exec(result.stdout);
  if (!match) throw new Error(`Unable to parse synthetic issue URL: ${result.stdout}`);
  return Number(match[1]);
}

function verifyPathway(pathway) {
  verifyCampaign(pathway);
  const pagesUrl = verifyPages(pathway);
  const auth = assertRecentSuccessfulRun(pathway, "auth-e2e.yml", "authentication canary");
  const review = assertRecentSuccessfulRun(
    pathway,
    "repo-assist.lock.yml",
    "Repo Assist review orchestrator",
    (run) => run.display_title === `Repo Assist · ${pathway.targetRepository} · review`,
  );
  const live = assertRecentSuccessfulRun(
    pathway,
    "repo-assist.lock.yml",
    "Repo Assist live orchestrator",
    (run) => run.display_title === `Repo Assist · ${pathway.targetRepository} · live`,
  );
  console.log(`${pathway.repository} (${pathway.name})`);
  console.log(`  auth: ${auth.html_url}`);
  console.log(`  private Pages: ${pagesUrl}`);
  console.log(`  review: ${review.html_url}`);
  console.log(`  live: ${live.html_url}`);
}

async function dispatchAndVerifyPathway(pathway) {
  await dispatchWorkflow(pathway, "auth-e2e.yml");

  const issueNumber = createSyntheticIssue(pathway);
  const beforeReview = targetIssueState(pathway.targetRepository, issueNumber);
  await dispatchCampaign(pathway, issueNumber, "review");
  const afterReview = targetIssueState(pathway.targetRepository, issueNumber);
  if (JSON.stringify(afterReview) !== JSON.stringify(beforeReview)) {
    throw new Error(`${pathway.repository} review mode mutated ${pathway.targetRepository}#${issueNumber}`);
  }

  await dispatchCampaign(pathway, issueNumber, "live");
  const afterLive = targetIssueState(pathway.targetRepository, issueNumber);
  if (JSON.stringify(afterLive) === JSON.stringify(afterReview)) {
    throw new Error(`${pathway.repository} live mode did not mutate ${pathway.targetRepository}#${issueNumber}`);
  }
  verifyPathway(pathway);
}

const mode = process.argv[2] || "--verify-only";
if (mode === "--dispatch") {
  for (const pathway of pathways) await dispatchAndVerifyPathway(pathway);
} else if (mode === "--verify-only") {
  for (const pathway of pathways) verifyPathway(pathway);
} else {
  throw new Error("Usage: verify-contoso-setup-pathways.mjs [--dispatch|--verify-only]");
}
