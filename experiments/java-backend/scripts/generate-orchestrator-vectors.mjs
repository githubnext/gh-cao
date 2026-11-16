#!/usr/bin/env node
// Regenerates src/test/resources/org/cao/backend/policy/orchestrator-vectors.json by running the
// production `.github/workflows/shared/control.mjs` precompute (read-only comparison evidence) against a
// synthetic `gh` stub. No network, credentials or real repositories are used. The Java backend never runs it.
// Usage (repository root): node experiments/java-backend/scripts/generate-orchestrator-vectors.mjs
import { spawnSync } from "node:child_process";
import { chmodSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const root = resolve(here, "../../..");
const program = join(root, ".github/workflows/shared/control.mjs");
const output = join(here, "../src/test/resources/org/cao/backend/policy/orchestrator-vectors.json");
const SHA = "1111111111111111111111111111111111111111";

const repo = (id, name, extra = {}) => ({
  id, full_name: name, archived: false, disabled: false, private: true,
  pushed_at: "2026-09-03T00:00:00Z", default_branch: "main", ...extra,
});
const workflow = (id, slug, state = "active") => ({
  id, name: slug, path: `.github/workflows/${slug}.lock.yml`, state,
});
const policy = (control) => JSON.stringify({ version: 1, "gh-aw-version": "v0.89.20", "control-plane": control });

const campaignWorkers = {
  "update-planner": { workflow: "dependabot-update-planner" },
  "update-applier": { workflow: "dependabot-update-applier", "max-mode": "review" },
  "update-paused": { workflow: "dependabot-update-paused", enabled: false },
  "update-missing": { workflow: "dependabot-update-missing" },
};

const cases = [
  {
    name: "cell-and-batch-partition",
    policy: policy({
      scope: { "allowed-owners": ["acme"], "allowed-repositories": ["acme/a", "acme/b", "acme/c", "acme/d", "acme/e", "acme/f", "acme/g"] },
      inventory: { "cell-count": 2, "cell-index": 1, "batch-size": 2, "batch-index": 1 },
      campaigns: { dependabot: { mode: "review", "max-repositories": 5, workers: campaignWorkers } },
    }),
    repositories: [repo(17, "acme/a"), repo(4, "acme/b"), repo(9, "acme/c"), repo(2, "acme/d"),
      repo(33, "acme/e"), repo(11, "acme/f"), repo(5, "acme/g")],
    configuredWorkers: ["dependabot-update-planner"],
    actionsWorkflows: [workflow(1, "dependabot-update-planner")],
    dispatchMax: 3,
  },
  {
    name: "rollout-and-dispatch-caps",
    policy: policy({
      scope: { "allowed-owners": ["acme"], "allowed-repositories": ["acme/a", "acme/b", "acme/c", "acme/d", "acme/e"] },
      campaigns: { dependabot: { mode: "review", "max-repositories": 10, "rollout-percent": 50, workers: campaignWorkers } },
    }),
    request: { maxRepositories: "9" },
    repositories: [repo(1, "acme/a"), repo(2, "acme/b"), repo(3, "acme/c"), repo(4, "acme/d"), repo(5, "acme/e")],
    configuredWorkers: ["dependabot-update-planner", "dependabot-update-applier"],
    actionsWorkflows: [workflow(1, "dependabot-update-planner"), workflow(2, "dependabot-update-applier")],
    dispatchMax: 3,
  },
  {
    name: "worker-skip-reasons",
    policy: policy({
      scope: { "allowed-owners": ["acme"], "allowed-repositories": ["acme/a"] },
      campaigns: { dependabot: { mode: "review", workers: campaignWorkers } },
    }),
    repositories: [repo(1, "acme/a")],
    configuredWorkers: ["dependabot-update-planner", "dependabot-update-applier", "dependabot-update-paused",
      "dependabot-update-missing", "dependabot-stranger"],
    actionsWorkflows: [workflow(1, "dependabot-update-planner", "disabled_manually"),
      workflow(2, "dependabot-update-applier"), workflow(3, "dependabot-update-paused"),
      workflow(4, "dependabot-stranger")],
    dispatchMax: 1,
  },
  {
    name: "target-modes-live-and-narrowed",
    policy: policy({
      scope: { "allowed-owners": ["acme"], "allowed-repositories": ["acme/a", "acme/b", "acme/c"] },
      campaigns: { dependabot: { mode: "live", "max-repositories": 5, targets: { "acme/b": { mode: "review" }, "ACME/C": { mode: "live" } }, workers: campaignWorkers } },
    }),
    repositories: [repo(1, "acme/a"), repo(2, "acme/b"), repo(3, "acme/C")],
    configuredWorkers: ["dependabot-update-planner"],
    actionsWorkflows: [workflow(1, "dependabot-update-planner")],
    dispatchMax: 2,
  },
  {
    name: "requested-review-narrows-live-targets",
    policy: policy({
      scope: { "allowed-owners": ["acme"], "allowed-repositories": ["acme/a", "acme/b"] },
      campaigns: { dependabot: { mode: "live", "max-repositories": 5, targets: { "acme/b": { mode: "live" } }, workers: campaignWorkers } },
    }),
    request: { mode: "review" },
    repositories: [repo(1, "acme/a"), repo(2, "acme/b")],
    configuredWorkers: ["dependabot-update-planner"],
    actionsWorkflows: [workflow(1, "dependabot-update-planner")],
    dispatchMax: 2,
  },
  {
    name: "archived-disabled-and-null-metadata",
    policy: policy({
      scope: { "allowed-owners": ["acme"], "allowed-repositories": ["acme/a", "acme/b", "acme/c"] },
      campaigns: { dependabot: { mode: "review", "max-repositories": 3, workers: campaignWorkers } },
    }),
    repositories: [repo(1, "acme/a", { archived: true }), repo(2, "acme/b", { disabled: true, private: false }),
      repo(3, "acme/c", { pushed_at: null, default_branch: "ünï/\"main\"\u2028<&>" })],
    configuredWorkers: ["dependabot-update-planner"],
    actionsWorkflows: [workflow(1, "dependabot-update-planner")],
    dispatchMax: 1,
  },
  {
    name: "empty-cell",
    policy: policy({
      scope: { "allowed-owners": ["acme"], "allowed-repositories": ["acme/a"] },
      inventory: { "cell-count": 2, "cell-index": 0 },
      campaigns: { dependabot: { mode: "review", workers: campaignWorkers } },
    }),
    repositories: [repo(1, "acme/a")],
    configuredWorkers: ["dependabot-update-planner"],
    actionsWorkflows: [workflow(1, "dependabot-update-planner")],
    dispatchMax: 1,
  },
];

function stubSource(table) {
  return `#!/usr/bin/env node
const table = ${JSON.stringify(table)};
const endpoint = process.argv.slice(2).find((value) => value.startsWith("repos/"));
if (endpoint.includes("/contents/")) {
  process.stdout.write(table.source);
} else if (endpoint.includes("/actions/workflows")) {
  for (const entry of table.workflows) process.stdout.write(JSON.stringify(entry) + "\\n");
} else {
  const base = endpoint.split("?")[0].toLowerCase();
  const found = table.repositories.find((entry) => "repos/" + entry.full_name.toLowerCase() === base);
  if (!found) { console.error("not found: " + endpoint); process.exit(1); }
  process.stdout.write(JSON.stringify(found) + "\\n");
}
`;
}

function run(testCase) {
  const directory = mkdtempSync(join(tmpdir(), "cao-java-vector-"));
  try {
    const stub = join(directory, "gh");
    const source = `---\nsafe-outputs:\n  dispatch-workflow:\n    workflows: [${testCase.configuredWorkers.join(", ")}]\n---\n`;
    writeFileSync(stub, stubSource({
      repositories: testCase.repositories,
      workflows: testCase.actionsWorkflows,
      source: Buffer.from(source).toString("base64"),
    }));
    chmodSync(stub, 0o755);
    const runnerTemp = join(directory, "runner");
    mkdirSync(join(runnerTemp, "cao"), { recursive: true });
    const env = {
      PATH: `${directory}:${process.env.PATH}`,
      HOME: directory,
      RUNNER_TEMP: runnerTemp,
      GITHUB_ENV: join(directory, "env"),
      GH_AW_SAFE_OUTPUTS: join(directory, "safe-outputs"),
      GITHUB_REPOSITORY: "acme/control",
      GITHUB_SERVER_URL: "https://github.com",
      GITHUB_WORKFLOW_REF: "acme/control/.github/workflows/dependabot.lock.yml@main",
      GITHUB_WORKFLOW_SHA: SHA,
      CAO_CAMPAIGN: "dependabot",
      CAO_ROLE: "orchestrator",
      CAO_WORKER: "",
      CAO_TARGET_REPOSITORY: "",
      CAO_REQUESTED_MODE: testCase.request?.mode ?? "",
      CAO_REQUESTED_MAX_REPOSITORIES: testCase.request?.maxRepositories ?? "",
      CAO_REQUESTED_ROLLOUT_PERCENT: testCase.request?.rolloutPercent ?? "",
      CAO_DISPATCH_MAX: String(testCase.dispatchMax),
      CAO_SAFE_OUTPUT_REPOSITORY: "acme/control",
    };
    const resolved = spawnSync("node", [program, "resolve-policy", "-"], {
      cwd: directory, encoding: "utf8", input: testCase.policy, env,
    });
    if (resolved.status !== 0) throw new Error(`${testCase.name}: resolve-policy failed: ${resolved.stderr}`);
    writeFileSync(join(runnerTemp, "cao/effective-policy.json"), resolved.stdout);
    const precompute = spawnSync("node", [program, "precompute"], { cwd: directory, encoding: "utf8", env });
    if (precompute.status !== 0) throw new Error(`${testCase.name}: precompute failed: ${precompute.stderr}`);
    return JSON.parse(readFileSync("/tmp/gh-aw/agent/control-precompute.json", "utf8"));
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
}

mkdirSync("/tmp/gh-aw/agent", { recursive: true });
const vectors = cases.map((testCase) => ({
  name: testCase.name,
  policy: testCase.policy,
  request: testCase.request ?? {},
  workflowSha: SHA,
  controlRepository: "acme/control",
  dispatchMax: testCase.dispatchMax,
  safeOutputRepository: "acme/control",
  configuredWorkers: testCase.configuredWorkers,
  actionsWorkflows: testCase.actionsWorkflows,
  repositories: testCase.repositories,
  expected: run(testCase),
}));
writeFileSync(output, `${JSON.stringify({ generatedFrom: ".github/workflows/shared/control.mjs", vectors }, null, 2)}\n`);
console.log(`wrote ${vectors.length} vectors to ${output}`);
