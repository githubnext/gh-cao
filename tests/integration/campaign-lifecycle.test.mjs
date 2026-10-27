import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import {
  existsSync,
  mkdtempSync,
  readFileSync,
  readdirSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import test from "node:test";
import { retryTransientCampaignInstall } from "../helpers/campaign-install-retry.mjs";

const campaignSource = process.env.CENTRAL_AGENTIC_OPS_CAMPAIGN_SOURCE
  || "githubnext/gh-cao@main";
const campaignUpdateSource = "https://github.com/githubnext/gh-cao";
// Public read-only identity for the disposable consumer's `origin`. It must
// differ from the catalog because gh aw refuses to add workflows into their
// own source repository; the consumer is never pushed.
const consumerRepository = "octocat/Hello-World";
const materializerScript = resolve(".github/workflows/shared/materialize-cao.mjs");
const installerSource = readFileSync(resolve("install-gh-cao.sh"), "utf8");
const controlRuntimeFiles = [
  ".github/workflows/shared/control.mjs",
  ".github/workflows/shared/policy.mjs",
  ".github/workflows/shared/authority.mjs",
  ".github/workflows/shared/operations.mjs",
  ".github/workflows/shared/github-apps.mjs",
];
const controlPlaneSkillFiles = [
  ".github/skills/agentic-workflows/SKILL.md",
  ".github/skills/add-cao-campaign/SKILL.md",
  ".github/skills/analyze-cao/SKILL.md",
  ".github/skills/cao-cli/SKILL.md",
  ".github/skills/create-cao-campaign/SKILL.md",
  ".github/skills/debug-cao/SKILL.md",
  ".github/skills/setup-cao/SKILL.md",
];
function focusedCampaignSource(slug, source = campaignSource) {
  const separator = source.lastIndexOf("@");
  assert.notEqual(separator, -1, "campaign source must include a ref");
  return `${source.slice(0, separator)}/${slug}${source.slice(separator)}`;
}
const ukAiAdvisoryCampaignSource = focusedCampaignSource("uk-ai-advisory");
const activityCampaignSource = focusedCampaignSource("activity");
const caoEvolutionCampaignSource = focusedCampaignSource("cao-evolution");
const craCampaignSource = focusedCampaignSource("eu-cra-compliance");
const dashboardCampaignSource = focusedCampaignSource("dashboard");
const dependabotUpdateSource = focusedCampaignSource("dependabot");
const dependabotCampaignUpdateSource = `${campaignUpdateSource}/dependabot`;
const optimizationCampaignSource = focusedCampaignSource("optimization");
const repoStatusWorkflowSource = `https://github.com/${campaignSource.slice(0, campaignSource.lastIndexOf("@"))}/blob/${campaignSource.slice(campaignSource.lastIndexOf("@") + 1)}/.github/workflows/daily-repo-status.md`;
const selfCareCampaignSource = focusedCampaignSource("self-care");
const softwareDevelopmentPracticesCampaignSource = focusedCampaignSource("software-development-practices");
const activityExpectedFiles = [
  "activity/actions-context.mjs",
  "activity/actions-log.mjs",
  "activity/campaign-intelligence.mjs",
  "activity/cao.mjs",
  "activity/cli-usage.mjs",
  "activity/commands/activity-stats.mjs",
  "activity/commands/audit-jsonl.mjs",
  "activity/commands/cluster-problems.mjs",
  "activity/commands/compact-jsonl.mjs",
  "activity/commands/computation.mjs",
  "activity/commands/dashboard-complexity.mjs",
  "activity/commands/discover-workflows.mjs",
  "activity/commands/repair-database.mjs",
  "activity/commands/download.mjs",
  "activity/commands/gh.mjs",
  "activity/commands/hash-payloads.mjs",
  "activity/commands/index.mjs",
  "activity/commands/ingest-jsonl.mjs",
  "activity/commands/ingest.mjs",
  "activity/commands/issue-status.mjs",
  "activity/commands/operational-value.mjs",
  "activity/commands/prune-dashboard.mjs",
  "activity/commands/query.mjs",
  "activity/collect-logs.sh",
  "activity/collect-workflow-logs.mjs",
  "activity/computations/intelligence-contracts.mjs",
  "activity/computations/intelligence.mjs",
  "activity/computations/index.mjs",
  "activity/computations/runtime-health.mjs",
  "activity/control-settings.mjs",
  "activity/debug.mjs",
  "activity/gh-aw-logs.mjs",
  "activity/inventory.mjs",
  "activity/inventory-sources.mjs",
  "activity/marketplace.mjs",
  "activity/normalized-phase.mjs",
  "activity/repository-visibility.mjs",
  "activity/token-intervention-lifecycle.mjs",
  "activity/version.mjs",
  ".github/workflows/cao-activity.yml",
];
const caoEvolutionExpectedFiles = [
  ".github/workflows/cao-evolution-failures-investigator.md",
  ".github/workflows/cao-evolution-compiler-security.md",
  ".github/workflows/cao-evolution-efficiency.md",
  ".github/workflows/cao-evolution-integrity.md",
  ".github/workflows/cao-evolution-reliability.md",
  ".github/workflows/cao-evolution.md",
  ".github/workflows/shared/activity-cache.md",
  ".github/workflows/shared/control.md",
];
const ukAiAdvisoryExpectedFiles = [
  "uk-ai-advisory/implementation-status.md",
  ".github/workflows/uk-ai-advisory-campaign-maintainer.md",
  ".github/workflows/uk-ai-advisory-operational-resilience.md",
  ".github/workflows/uk-ai-advisory.md",
  ".github/workflows/shared/control.md",
];
const craExpectedFiles = [
  "eu-cra-compliance/implementation-status.md",
  "eu-cra-compliance/eu-cra-report-operational-value-runtime.bash",
  ".github/workflows/eu-cra-compliance-article-14-reporting-readiness.md",
  ".github/workflows/eu-cra-compliance-conformity-release-evidence.md",
  ".github/workflows/eu-cra-compliance-campaign-maintainer.md",
  ".github/workflows/eu-cra-compliance-scope-classifier.md",
  ".github/workflows/eu-cra-compliance-security-requirements-auditor.md",
  ".github/workflows/eu-cra-compliance-supply-chain-sbom-auditor.md",
  ".github/workflows/eu-cra-compliance-vulnerability-handling-auditor.md",
  ".github/workflows/eu-cra-compliance.md",
  ".github/workflows/shared/control.md",
];
const dashboardExpectedFiles = [
  ".github/workflows/cao-dashboard.yml",
  ...[...readFileSync(
    new URL("../../dashboard/aw.yml", import.meta.url),
    "utf8",
  ).matchAll(/^\s+destination: (.+)$/gm)].map((match) => match[1]),
];
const optimizationExpectedFiles = [
  "optimization/operational-value.mjs",
  "optimization/operational-value/optimization-token-optimizer.mjs",
  ".github/workflows/optimization-token-auditor.md",
  ".github/workflows/optimization-token-optimizer.md",
  ".github/workflows/optimization.md",
  ".github/workflows/shared/activity-cache.md",
  ".github/workflows/shared/control.md",
];
const selfCareExpectedFiles = [
  ".github/workflows/self-care-accessibility-checker.md",
  ".github/workflows/self-care-agent-discoverability.md",
  ".github/workflows/self-care-code-improvement.md",
  ".github/workflows/self-care-dashboard-data-schema.md",
  ".github/workflows/self-care-dashboard-debug-logging.md",
  ".github/workflows/self-care-dashboard-performance.md",
  ".github/workflows/self-care-data-acquisition-audit.md",
  ".github/workflows/self-care-dashboard-language-refactor.md",
  ".github/workflows/self-care-dashboard-review.md",
  ".github/workflows/self-care-docs-build-time-investigator.md",
  ".github/workflows/self-care-docs-maintainer.md",
  ".github/workflows/self-care-glossary.md",
  ".github/workflows/self-care-open-source-failures.md",
  ".github/workflows/self-care-pages-health.md",
  ".github/workflows/self-care-primer-brand-checker.md",
  ".github/workflows/self-care-reactive-ui-expert.md",
  ".github/workflows/self-care-server-go-logging.md",
  ".github/workflows/self-care-specs-maintainer.md",
  ".github/workflows/self-care.md",
  ".github/workflows/shared/activity-cache.md",
  ".github/workflows/shared/control.md",
];
const softwareDevelopmentPracticesExpectedFiles = [
  "software-development-practices/software-development-guidance-operational-value-runtime.bash",
  ".github/workflows/shared/control.md",
  ".github/workflows/software-development-practices-github-well-architected.md",
  ".github/workflows/software-development-practices-nist-ssdf.md",
  ".github/workflows/software-development-practices.md",
];
const repositoryOnlyFiles = [
  ".github/aw/e2e/run-canary.sh",
  ".github/aw/e2e/run-stress.sh",
  ".github/workflows/enterprise-canary.yml",
  ".github/workflows/enterprise-stress.yml",
  ".github/workflows/review-smoke.yml",
];

function installedManifestPath(consumer, manifestName) {
  return join(consumer, ".github", "aw", "packages", manifestName);
}

function installedManifests(consumer) {
  return readdirSync(join(consumer, ".github", "aw", "packages"));
}

function run(command, args, cwd, input = undefined, env = process.env) {
  return execFileSync(command, args, {
    cwd,
    encoding: "utf8",
    env,
    input,
    maxBuffer: 16 * 1024 * 1024,
    stdio: [input === undefined ? "ignore" : "pipe", "pipe", "pipe"],
  });
}

// `gh aw update` reports a package whose manifest resolution failed as zero
// successes and zero failures when the package installs no agentic workflows,
// so the only visible symptom is "no workflows were successfully updated".
// Enabling the gh-aw CLI debug namespaces keeps the underlying cause in the
// captured stderr, which node:test reports when the command exits non-zero.
// The update re-resolves the package through the contents API, so it is exposed
// to the same transient GitHub gateway errors as the initial install.
function runUpdate(args, cwd) {
  return retryTransientCampaignInstall(
    () => run("gh", args, cwd, undefined, { ...process.env, DEBUG: "cli:*" }),
  );
}

async function installCampaign(source) {
  const packageName = source.slice(0, source.lastIndexOf("@"));
  const campaign = packageName === "githubnext/gh-cao"
    ? "root"
    : packageName.split("/").at(-1);
  return retryTransientCampaignInstall(() => {
    const consumer = mkdtempSync(join(tmpdir(), "central-agentic-ops-campaign-"));
    try {
      run("git", ["init", "--quiet"], consumer);
      if (campaign === "root") {
        run("git", ["remote", "add", "origin", `https://github.com/${consumerRepository}.git`], consumer);
        run("bash", ["-s", "--", source], consumer, installerSource);
        return consumer;
      }
      run("gh", [
        "aw",
        "add",
        source,
        "--force",
        "--no-security-scanner",
      ], consumer);
      if (campaign !== "activity" && campaign !== "dashboard") {
        run(process.execPath, [materializerScript, "materialize", campaign], consumer);
      }
      return consumer;
    } catch (error) {
      rmSync(consumer, { recursive: true, force: true });
      throw error;
    }
  });
}

function assertInstalledBootstrap(repository, ghAwVersion, controlRepository) {
  const policy = ".github/workflows/cao.json";
  const control = ".github/workflows/shared/control.mjs";
  run(process.execPath, ["activity/cao.mjs", "--help"], repository);
  for (const bundle of ["activity", "dashboard"]) {
    run(process.execPath, [".github/workflows/shared/materialize-cao.mjs", "verify", bundle], repository);
  }
  run(process.execPath, [control, "validate-policy", policy], repository);
  assert.equal(run(process.execPath, [control, "compiler-version", policy], repository).trim(), ghAwVersion);

  const settingsDirectory = mkdtempSync(join(tmpdir(), "central-agentic-ops-settings-"));
  try {
    const settingsPath = join(settingsDirectory, "control-settings.json");
    execFileSync(process.execPath, ["activity/control-settings.mjs", control, policy, settingsPath], {
      cwd: repository,
      encoding: "utf8",
      env: { ...process.env, GITHUB_REPOSITORY: controlRepository },
      stdio: ["ignore", "pipe", "pipe"],
    });
    const settings = JSON.parse(readFileSync(settingsPath, "utf8"));
    assert.equal(settings.policy_resolution.status, "available", settings.policy_resolution.reason);
    assert.deepEqual(settings.allowed_repositories, [controlRepository]);
  } finally {
    rmSync(settingsDirectory, { recursive: true, force: true });
  }
}

test("root campaign bootstraps an empty CAO and preserves resources during workflow update", { timeout: 240_000 }, async () => {
  const consumer = await installCampaign(campaignSource);
  try {
    assert.equal(existsSync(join(consumer, ".github", "aw", "default-AGENTS.md")), false);
    assert.equal(existsSync(join(consumer, ".github", "aw", "cao")), false);
    for (const relativePath of controlRuntimeFiles) {
      assert.ok(existsSync(join(consumer, relativePath)), `root campaign omitted control file ${relativePath}`);
    }
    for (const relativePath of controlPlaneSkillFiles) {
      assert.ok(existsSync(join(consumer, relativePath)), `root campaign omitted declared skill ${relativePath}`);
    }
    const policyPath = join(consumer, ".github", "workflows", "cao.json");
    const ghAwVersion = run("bash", ["-c", "gh aw version 2>&1"], consumer)
      .match(/\bv[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?\b/)?.[0];
    assert.ok(ghAwVersion, "gh aw version did not report a version");
    const initializedPolicy = JSON.parse(readFileSync(policyPath, "utf8"));
    const controlRepository = run("gh", ["repo", "view", "--json", "nameWithOwner", "--jq", ".nameWithOwner"], consumer).trim();
    assert.equal(controlRepository, consumerRepository);
    assert.equal(initializedPolicy.version, 1);
    assert.equal(initializedPolicy["gh-aw-version"], ghAwVersion);
    const officialRepository = initializedPolicy["control-plane"]?.marketplace?.registries?.[0]?.repository;
    assert.match(officialRepository, /^[A-Za-z0-9-]+\/[A-Za-z0-9_.-]+$/);
    // Published extensions can predate a catalog rename; require the same live repository identity.
    assert.equal(run("gh", [
      "api", "--hostname", "github.com", `repos/${officialRepository}`, "--jq", ".full_name",
    ], consumer).trim(), "githubnext/gh-cao");
    assert.deepEqual(initializedPolicy["control-plane"], {
      scope: {
        "allowed-owners": [controlRepository.split("/")[0]],
        "allowed-repositories": [controlRepository],
      },
      marketplace: {
        registries: [{
          id: "official",
          name: "Official CAO catalog",
          repository: officialRepository,
          ref: "main",
          auth: { type: "none" },
        }, {
          id: "agentics",
          name: "Agentics workflows",
          repository: "githubnext/agentics",
          path: "workflows",
          format: "workflows",
          ref: "main",
          auth: { type: "none" },
        }],
      },
      campaigns: {},
    });
    assertInstalledBootstrap(consumer, ghAwVersion, controlRepository);

    run("git", ["add", ".github", "activity", "dashboard"], consumer);
    run("git", [
      "-c", "user.name=CAO installer test",
      "-c", "user.email=cao-installer-test@example.invalid",
      "-c", "commit.gpgsign=false",
      "-c", "core.hooksPath=/dev/null",
      "commit", "--quiet", "-m", "CAO installer smoke",
    ], consumer);
    const checkout = mkdtempSync(join(tmpdir(), "central-agentic-ops-checkout-"));
    try {
      run("git", ["clone", "--quiet", consumer, checkout], tmpdir());
      assertInstalledBootstrap(checkout, ghAwVersion, controlRepository);
    } finally {
      rmSync(checkout, { recursive: true, force: true });
    }

    const policy = `${JSON.stringify({
      version: 1,
      "control-plane": {
        scope: {
          "allowed-owners": ["acme"],
          "allowed-repositories": ["acme/example"],
        },
        campaigns: {},
      },
    }, null, 2)}\n`;
    writeFileSync(policyPath, policy);
    assert.deepEqual(
      JSON.parse(readFileSync(join(consumer, ".github", "workflows", "aw.json"), "utf8")).auto_upgrade.options,
      ["--pre-releases"],
    );
    // The root campaign installs deterministic workflows; compiler-generated
    // helpers are only materialized when an agentic workflow is compiled.
    assert.deepEqual(
      readdirSync(join(consumer, ".github", "workflows")).filter((name) => name.endsWith(".lock.yml")),
      [],
    );
    for (const relativePath of activityExpectedFiles) {
      assert.ok(existsSync(join(consumer, relativePath)), `root campaign omitted activity file ${relativePath}`);
    }
    for (const relativePath of dashboardExpectedFiles) {
      assert.ok(existsSync(join(consumer, relativePath)), `root campaign omitted dashboard file ${relativePath}`);
    }
    const campaignRecords = installedManifests(consumer);
    assert.equal(campaignRecords.length, 1, "expected one installed root campaign manifest");
    const installedCampaign = JSON.parse(readFileSync(
      installedManifestPath(consumer, campaignRecords[0]),
      "utf8",
    ));
    for (const { destination } of installedCampaign.files) {
      const workflowPath = join(consumer, destination);
      if (!destination.endsWith(".md") || !existsSync(workflowPath)) continue;
      const workflow = readFileSync(workflowPath, "utf8");
      writeFileSync(workflowPath, workflow.replace(/^source: .*$/m, `source: ${campaignSource}`));
    }

    const removedRuntime = controlRuntimeFiles[0];
    rmSync(join(consumer, removedRuntime));
    await runUpdate([
      "aw",
      "update",
      campaignUpdateSource,
      "--force",
      "--no-merge",
      "--no-compile",
      "--no-security-scanner",
      "--cool-down",
      "0",
    ], consumer);

    assert.ok(existsSync(join(consumer, removedRuntime)), "gh aw update did not restore the control runtime");
    assert.equal(existsSync(join(consumer, ".github", "aw", "cao")), false);
    assert.equal(readFileSync(policyPath, "utf8"), policy, "gh aw update changed consumer-owned CAO policy");
  } finally {
    rmSync(consumer, { recursive: true, force: true });
  }
});

test("direct focused activity installation remains incomplete without the CAO materializer", { timeout: 180_000 }, async () => {
  const consumer = await installCampaign(activityCampaignSource);
  try {
    assert.ok(existsSync(join(consumer, ".github", "workflows", "cao-activity.yml")));
    assert.equal(existsSync(join(consumer, "activity", "cao.mjs")), false);
    assert.equal(existsSync(join(consumer, ".github", "actions", "setup-cao-runtime", "action.yml")), false);
  } finally {
    rmSync(consumer, { recursive: true, force: true });
  }
});

test("gh aw add installs the focused EU CRA campaign contract", { timeout: 180_000 }, async () => {
  const consumer = await installCampaign(craCampaignSource);

  try {
    for (const relativePath of craExpectedFiles) {
      assert.ok(existsSync(join(consumer, relativePath)), `focused CRA campaign omitted ${relativePath}`);
    }
    assert.ok(
      !existsSync(join(consumer, ".github", "workflows", "dependabot.md")),
      "focused CRA campaign installed an unrelated orchestrator",
    );

    const campaignManifests = installedManifests(consumer);
    assert.equal(campaignManifests.length, 1, "expected one focused CRA campaign manifest");
    const installedManifest = JSON.parse(readFileSync(
      installedManifestPath(consumer, campaignManifests[0]),
      "utf8",
    ));
    assert.deepEqual(
      installedManifest.files.map(({ destination }) => destination).sort(),
      [
        "eu-cra-compliance/implementation-status.md",
        "eu-cra-compliance/eu-cra-report-operational-value-runtime.bash",
        ".github/workflows/eu-cra-compliance-article-14-reporting-readiness.md",
        ".github/workflows/eu-cra-compliance-conformity-release-evidence.md",
        ".github/workflows/eu-cra-compliance-campaign-maintainer.md",
        ".github/workflows/eu-cra-compliance-scope-classifier.md",
        ".github/workflows/eu-cra-compliance-security-requirements-auditor.md",
        ".github/workflows/eu-cra-compliance-supply-chain-sbom-auditor.md",
        ".github/workflows/eu-cra-compliance-vulnerability-handling-auditor.md",
        ".github/workflows/eu-cra-compliance.md",
      ].sort(),
      "focused CRA campaign manifest must own its entry workflows and ledger",
    );
  } finally {
    rmSync(consumer, { recursive: true, force: true });
  }
});

test("gh aw add installs the focused UK AI Advisory campaign contract", { timeout: 180_000 }, async () => {
  const consumer = await installCampaign(ukAiAdvisoryCampaignSource);

  try {
    for (const relativePath of ukAiAdvisoryExpectedFiles) {
      assert.ok(existsSync(join(consumer, relativePath)), `focused UK AI Advisory campaign omitted ${relativePath}`);
    }
    assert.ok(
      !existsSync(join(consumer, ".github", "workflows", "dependabot.md")),
      "focused UK AI Advisory campaign installed an unrelated orchestrator",
    );

    const campaignManifests = installedManifests(consumer);
    assert.equal(campaignManifests.length, 1, "expected one focused UK AI Advisory campaign manifest");
    const installedManifest = JSON.parse(readFileSync(
      installedManifestPath(consumer, campaignManifests[0]),
      "utf8",
    ));
    assert.deepEqual(
      installedManifest.files.map(({ destination }) => destination).sort(),
      [
        "uk-ai-advisory/implementation-status.md",
        ".github/workflows/uk-ai-advisory-campaign-maintainer.md",
        ".github/workflows/uk-ai-advisory.md",
      ].toSorted(),
      "focused UK AI Advisory campaign manifest must own its entry workflows and ledger",
    );
  } finally {
    rmSync(consumer, { recursive: true, force: true });
  }
});

test("gh aw add installs the focused SelfCare campaign contract", { timeout: 180_000 }, async () => {
  const consumer = await installCampaign(selfCareCampaignSource);

  try {
    for (const relativePath of selfCareExpectedFiles) {
      assert.ok(existsSync(join(consumer, relativePath)), `focused SelfCare campaign omitted ${relativePath}`);
    }
  } finally {
    rmSync(consumer, { recursive: true, force: true });
  }
});

test("gh aw add installs the focused CAO Evolution campaign contract", { timeout: 180_000 }, async () => {
  const consumer = await installCampaign(caoEvolutionCampaignSource);
  try {
    for (const relativePath of caoEvolutionExpectedFiles) {
      assert.ok(existsSync(join(consumer, relativePath)), `focused CAO Evolution campaign omitted ${relativePath}`);
    }
  } finally {
    rmSync(consumer, { recursive: true, force: true });
  }
});

test("gh aw add installs the focused Optimization campaign contract", { timeout: 180_000 }, async () => {
  const consumer = await installCampaign(optimizationCampaignSource);
  try {
    for (const relativePath of optimizationExpectedFiles) {
      assert.ok(existsSync(join(consumer, relativePath)), `focused Optimization campaign omitted ${relativePath}`);
    }
    assert.ok(
      !existsSync(join(consumer, ".github", "workflows", "dependabot.md")),
      "focused Optimization campaign installed an unrelated orchestrator",
    );
  } finally {
    rmSync(consumer, { recursive: true, force: true });
  }
});

test("root campaign runtime supports standalone Daily Repo Status installation", { timeout: 240_000 }, async () => {
  const consumer = mkdtempSync(join(tmpdir(), "central-agentic-ops-standalone-"));
  try {
    run("git", ["init", "--quiet"], consumer);
    run("git", ["remote", "add", "origin", `https://github.com/${consumerRepository}.git`], consumer);
    retryTransientCampaignInstall(() => run("gh", ["aw", "add", campaignSource, "--no-security-scanner"], consumer));
    retryTransientCampaignInstall(() => run("gh", ["aw", "add", repoStatusWorkflowSource], consumer));
    for (const relativePath of [
      ".github/workflows/daily-repo-status.md",
      ".github/workflows/daily-repo-status.lock.yml",
      ".github/workflows/shared/control.md",
      ".github/workflows/shared/control.mjs",
    ]) {
      assert.ok(existsSync(join(consumer, relativePath)), `Daily Repo Status omitted ${relativePath}`);
    }
    const policyPath = join(consumer, ".github/workflows/cao.json");
    assert.equal(existsSync(policyPath), false, "installation does not copy catalog rollout authority");
    const policy = {
      version: 1,
      "gh-aw-version": JSON.parse(readFileSync(resolve(".github/workflows/cao.json"), "utf8"))["gh-aw-version"],
      "control-plane": {
        scope: { "allowed-owners": ["octocat"], "allowed-repositories": [consumerRepository] },
      },
    };
    policy["control-plane"].workflows = {
      ...policy["control-plane"].workflows,
      "daily-repo-status": { enabled: false, mode: "review", "max-mode": "review", "max-repositories": 1 },
    };
    writeFileSync(policyPath, `${JSON.stringify(policy, null, 2)}\n`);
    run(process.execPath, [".github/workflows/shared/control.mjs", "validate-policy", policyPath], consumer);
    assert.equal(existsSync(join(consumer, "repo-status/cao.json")), false);
    assert.equal(installedManifests(consumer).some(name => /repo-status/.test(name)), false);
    assert.equal(existsSync(join(consumer, ".github/workflows/repo-status-report.md")), false);
    assert.equal(existsSync(join(consumer, ".github/workflows/repo-assist.md")), false);
    const source = readFileSync(join(consumer, ".github/workflows/daily-repo-status.md"), "utf8");
    assert.match(source, /role: standalone\n\s+workflow: daily-repo-status/);
    assert.match(source, /model: gpt-5\.3-codex/);
    assert.match(source, /required-labels: \[repo-status\]/);
    assert.match(source, /Copyright \(c\) 2025 GitHub/);
  } finally {
    rmSync(consumer, { recursive: true, force: true });
  }
});

test("gh aw add installs the focused Software Development Practices campaign contract", { timeout: 180_000 }, async () => {
  const consumer = await installCampaign(softwareDevelopmentPracticesCampaignSource);

  try {
    for (const relativePath of softwareDevelopmentPracticesExpectedFiles) {
      assert.ok(
        existsSync(join(consumer, relativePath)),
        `focused Software Development Practices campaign omitted ${relativePath}`,
      );
    }
    assert.ok(
      !existsSync(join(consumer, ".github", "workflows", "dependabot.md")),
      "focused Software Development Practices campaign installed an unrelated orchestrator",
    );

    const campaignManifests = installedManifests(consumer);
    assert.equal(campaignManifests.length, 1, "expected one focused Software Development Practices campaign manifest");
    const installedManifest = JSON.parse(readFileSync(
      installedManifestPath(consumer, campaignManifests[0]),
      "utf8",
    ));
    assert.deepEqual(
      installedManifest.files.map(({ destination }) => destination).sort(),
      [
        "software-development-practices/software-development-guidance-operational-value-runtime.bash",
        ".github/workflows/software-development-practices-github-well-architected.md",
        ".github/workflows/software-development-practices-nist-ssdf.md",
        ".github/workflows/software-development-practices.md",
      ],
      "focused Software Development Practices campaign manifest must own its entry workflows and runtime",
    );
  } finally {
    rmSync(consumer, { recursive: true, force: true });
  }
});

test("root CAO installation materializes the dashboard campaign contract", { timeout: 180_000 }, async () => {
  const consumer = await installCampaign(campaignSource);

  try {
    for (const relativePath of dashboardExpectedFiles) {
      assert.ok(existsSync(join(consumer, relativePath)), `dashboard campaign omitted ${relativePath}`);
    }

    assert.equal(existsSync(join(consumer, ".github", "aw", "dashboard")), false);

    const dashboardWorkflow = readFileSync(join(consumer, ".github", "workflows", "cao-dashboard.yml"), "utf8");
    assert.doesNotMatch(dashboardWorkflow, /workflow_call:|cao-dashboard-build|dispatch-workflow/);
    assert.match(dashboardWorkflow, /workflow_dispatch:/);
    assert.match(dashboardWorkflow, /actions\/upload-artifact@[0-9a-f]{40}/);
    assert.match(dashboardWorkflow, /actions\/cache\/save@[0-9a-f]{40}/);
    assert.match(dashboardWorkflow, /actions\/(?:upload-pages-artifact|deploy-pages)@[0-9a-f]{40}/);
    assert.match(dashboardWorkflow, /name: CAO Dashboard/);
    assert.match(dashboardWorkflow, /enablement: false/);
    assert.match(dashboardWorkflow, /deploy: \$\{\{ steps\.deployment-policy\.outputs\.deploy \}\}/);
    assert.match(dashboardWorkflow, /if: needs\.build\.outputs\.deploy == 'true'/);
    assert.doesNotMatch(dashboardWorkflow, /^\s+run:/m);
    assert.doesNotMatch(dashboardWorkflow, /actions\/github-script@(?![0-9a-f]{40}\b)/);
    assert.match(dashboardWorkflow, /Standalone Pages deployment:[\s\S]*?Dashboard artifact assembly completed/);
    assert.doesNotMatch(dashboardWorkflow, /schedule:/);
    assert.match(dashboardWorkflow, /workflow_run:[\s\S]*?workflows:[\s\S]*?- CAO Activity[\s\S]*?types:[\s\S]*?- completed/);
    assert.doesNotMatch(dashboardWorkflow, /^  push:/m);
    assert.doesNotMatch(dashboardWorkflow, /\.github\/aw\/(?:dashboard|dashboards)/);
    assert.match(dashboardWorkflow, /github\.event\.workflow_run\.conclusion == 'success'/);
    assert.match(dashboardWorkflow, /github\.event\.workflow_run\.head_branch == github\.event\.repository\.default_branch/);
    assert.match(dashboardWorkflow, /ref: \$\{\{ github\.workflow_sha \}\}/);

    const dashboardSite = join(consumer, "dashboard", "site");
    const dashboardOutput = join(consumer, "dashboard-output");
    const controlSettings = join(consumer, "control-settings.json");
    run("gh", ["aw", "add", activityCampaignSource, "--force", "--no-security-scanner"], consumer);
    writeFileSync(controlSettings, "{}\n");
    run("npm", ["ci", "--ignore-scripts"], dashboardSite);
    run("npm", ["run", "build", "--", dashboardOutput, controlSettings], dashboardSite);
    for (const asset of ["src/main.js", "src/main.js.map", "src/data-worker.js", "src/data-worker.js.map", "smells.svg"]) {
      assert.ok(existsSync(join(dashboardOutput, asset)), `dashboard build omitted ${asset}`);
    }
  } finally {
    rmSync(consumer, { recursive: true, force: true });
  }
});

test("root CAO rematerialization restores dashboard workflows, producers, and renderer assets", { timeout: 180_000 }, async () => {
  const consumer = await installCampaign(campaignSource);

  try {
    const deployPath = join(consumer, ".github", "workflows", "cao-dashboard.yml");
    const deployWorkflow = readFileSync(deployPath, "utf8");
    writeFileSync(deployPath, `${deployWorkflow}\n# local integration-test change\n`);

    const removedFiles = [
      "dashboard/report/records.mjs",
      "dashboard/site/index.html",
      "dashboard/site/scripts/build.mjs",
    ];
    for (const relativePath of removedFiles) {
      rmSync(join(consumer, relativePath));
    }

    run("gh", [
      "aw",
      "add",
      campaignSource,
      "--force",
      "--no-security-scanner",
    ], consumer);
    run(process.execPath, [materializerScript, "materialize", "root"], consumer);

    assert.ok(
      !readFileSync(deployPath, "utf8").includes("# local integration-test change"),
      "gh aw add --force retained a local dashboard workflow modification",
    );
    for (const relativePath of removedFiles) {
      assert.ok(existsSync(join(consumer, relativePath)), `gh aw add --force did not restore ${relativePath}`);
    }
  } finally {
    rmSync(consumer, { recursive: true, force: true });
  }
});

test("gh aw update replaces workflows and restores campaign-owned assets after canonical materialization", { timeout: 180_000 }, async () => {
  const consumer = await installCampaign(dependabotUpdateSource);

  try {
    const intelligenceSourcePath = join(consumer, "dependabot", "intelligence.json");
    const intelligencePath = join(consumer, ".github", "cao", "intelligence", "dependabot.json");
    assert.equal(
      readFileSync(intelligencePath, "utf8"),
      readFileSync(intelligenceSourcePath, "utf8"),
      "initial exact-revision installation did not materialize Dependabot intelligence",
    );
    const orchestratorPath = join(consumer, ".github", "workflows", "dependabot.md");
    const orchestrator = readFileSync(orchestratorPath, "utf8");
    writeFileSync(orchestratorPath, `${orchestrator.replace(/^source: .*$/m, `source: ${campaignSource}`)}\n# local integration-test change\n`);

    const plannerPath = join(consumer, ".github", "workflows", "dependabot-update-planner.md");
    const planner = readFileSync(plannerPath, "utf8");
    writeFileSync(plannerPath, planner.replace(/^source: .*$/m, `source: ${campaignSource}`));

    const removedFiles = [
      "dependabot/operational-value.mjs",
      ".github/workflows/dependabot-update-planner.md",
      ".github/workflows/shared/control.md",
    ];
    for (const relativePath of removedFiles) {
      rmSync(join(consumer, relativePath));
    }
    rmSync(intelligencePath);

    await runUpdate([
      "aw",
      "update",
      dependabotCampaignUpdateSource,
      "--force",
      "--no-merge",
      "--no-compile",
      "--no-security-scanner",
      "--cool-down",
      "0",
    ], consumer);
    run(process.execPath, [materializerScript, "materialize", "dependabot"], consumer);

    const updatedOrchestrator = readFileSync(orchestratorPath, "utf8");
    assert.ok(
      !updatedOrchestrator.includes("# local integration-test change"),
      "gh aw update retained a local campaign workflow modification",
    );
    for (const relativePath of removedFiles) {
      assert.ok(existsSync(join(consumer, relativePath)), `gh aw update did not restore ${relativePath}`);
    }
    assert.equal(
      existsSync(intelligencePath),
      existsSync(intelligenceSourcePath),
      "materialized intelligence did not match the updated Campaign revision",
    );
    if (existsSync(intelligenceSourcePath)) {
      assert.equal(readFileSync(intelligencePath, "utf8"), readFileSync(intelligenceSourcePath, "utf8"));
    }
  } finally {
    rmSync(consumer, { recursive: true, force: true });
  }
});