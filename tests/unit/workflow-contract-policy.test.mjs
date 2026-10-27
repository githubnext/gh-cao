import assert from "node:assert/strict";
import { existsSync, readFileSync, readdirSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import { parse } from "yaml";
import { policyCases, userFacingScenarios } from "./workflow-contract.matrix.mjs";
import { controlPolicy, controlPrecompute, generatedJobs, modes, portableSkill, resolvePolicy, root, stepBlock, transitivelyNeeds, workflow, workflowsDirectory } from "./workflow-contract.helpers.mjs";

// Central policy resolution, rollout limits, and activation contracts.

function workflowConfig(name) {
  const frontmatter = /^---\n([\s\S]*?)\n---/.exec(workflow(name))?.[1];
  assert.ok(frontmatter, `${name} must have frontmatter`);
  return parse(frontmatter);
}

function operationCampaigns() {
  const policyCampaigns = JSON.parse(
    readFileSync(join(root, ".github", "workflows", "cao.json"), "utf8"),
  )["control-plane"].campaigns;
  return Object.entries(policyCampaigns).flatMap(([campaignName, policy]) => {
    const descriptorPath = join(root, campaignName, "cao.json");
    if (!existsSync(descriptorPath)) {
      assert.equal(policy.workers, undefined, `${campaignName} workers require a campaign descriptor`);
      return [];
    }
    return [{
      campaignName,
      policy,
      descriptor: JSON.parse(readFileSync(descriptorPath, "utf8")),
    }];
  });
}

function operationWorkflowRegistrations() {
  const campaigns = operationCampaigns().flatMap(({ campaignName, descriptor }) => [
    {
      name: `${descriptor.orchestrator}.md`,
      campaignName,
      role: "orchestrator",
      workerName: undefined,
    },
    ...Object.entries(descriptor.workers).map(([workerName, workflowId]) => ({
      name: `${workflowId}.md`,
      campaignName,
      role: "worker",
      workerName,
    })),
  ]);
  const standalone = Object.keys(controlPolicy["control-plane"].workflows ?? {}).map((workflowName) => ({
    name: `${workflowName}.md`,
    workflowName,
    role: "standalone",
  }));
  return [...campaigns, ...standalone];
}

test("Pi workflows no longer pin the unavailable GPT-5.4 model", () => {
  const names = readdirSync(workflowsDirectory).filter((name) => name.endsWith(".md"));
  const piWorkflows = names.filter((name) => workflowConfig(name).engine?.id === "pi");
  assert.ok(piWorkflows.length > 0);
  for (const name of piWorkflows) {
    assert.notEqual(workflowConfig(name).engine.model, "copilot/gpt-5.4", name);
  }
});

test("all scheduled configurations and manual selections route safely", () => {
  const cases = policyCases();
  const uniqueInputs = new Set(cases.map(({ id, ...values }) => JSON.stringify(values)));

  assert.equal(cases.length, 120);
  assert.equal(cases.filter(({ eventName }) => eventName === "schedule").length, 24);
  assert.equal(cases.filter(({ eventName }) => eventName === "workflow_dispatch").length, 96);
  assert.equal(uniqueInputs.size, cases.length, "matrix contains duplicate policy inputs");

  for (const scenario of cases) {
    const policy = resolvePolicy(scenario);
    const expectedMode = scenario.eventName === "workflow_dispatch"
      ? scenario.manualMode
      : scenario.configuredMode;
    const expectedReviewRepo = scenario.manualReviewRepo || "acme/control-plane";
    const percentageCap = scenario.rolloutPercent === 10 ? 3 : 25;

    assert.equal(policy.enabled, scenario.campaignEnabled, scenario.id);
    assert.equal(policy.safeOutputMode, scenario.campaignEnabled ? expectedMode : null, scenario.id);
    assert.equal(
      policy.safeOutputRepo,
      scenario.campaignEnabled && expectedMode === "review" ? expectedReviewRepo : "",
      scenario.id,
    );
    assert.equal(
      policy.dispatchAllowed,
      scenario.campaignEnabled,
      scenario.id,
    );
    assert.equal(
      policy.effectiveMaxRepos,
      scenario.campaignEnabled
        ? (scenario.maxRepos ? Math.min(scenario.maxRepos, percentageCap) : percentageCap)
        : 0,
      scenario.id,
    );
  }
});

test("every checked user-facing scenario is backed by the exhaustive matrix", () => {
  const cases = policyCases();
  const groupCounts = Object.groupBy(userFacingScenarios, ({ group }) => group);

  assert.equal(userFacingScenarios.length, 22);
  assert.equal(new Set(userFacingScenarios.map(({ name }) => name)).size, 22);
  assert.equal(groupCounts["Scheduled modes"].length, 4);
  assert.equal(groupCounts["Manual runs"].length, 3);
  assert.equal(groupCounts["Review routing"].length, 4);
  assert.equal(groupCounts["Rollout limits"].length, 7);
  assert.equal(groupCounts["Kill switch"].length, 4);

  for (const scenario of userFacingScenarios) {
    const matrixCase = cases.find(({ id, totalRepositories, ...inputs }) =>
      Object.entries(scenario.inputs).every(([name, value]) => inputs[name] === value)
      && inputs.campaignEnabled === (scenario.inputs.campaignEnabled ?? true));

    assert.ok(matrixCase, `${scenario.name} is missing from the exhaustive matrix`);
    const policy = resolvePolicy(matrixCase);
    assert.equal(policy.enabled, scenario.inputs.campaignEnabled ?? true, scenario.name);
    const { enabled, ...actual } = policy;
    assert.deepEqual(actual, scenario.expected, scenario.name);
  }
});

test("percentage rollout rejects invalid settings and handles an empty organization", () => {
  for (const maxRepos of [0, -1, 1.5, 1001, Number.NaN]) {
    assert.throws(
      () => resolvePolicy({ maxRepos, rolloutPercent: 100, totalRepositories: 10 }),
      RangeError,
    );
  }

  for (const rolloutPercent of [0, 101, 10.5, Number.NaN]) {
    assert.throws(
      () => resolvePolicy({ rolloutPercent, totalRepositories: 10 }),
      RangeError,
    );
  }

  assert.equal(resolvePolicy({ maxRepos: 1, rolloutPercent: 10, totalRepositories: 0 }).effectiveMaxRepos, 0);
  for (const configuredMode of ["unknown", "preview", "preview_only", "staged", "Review", "LIVE", "review "]) {
    assert.throws(() => resolvePolicy({
      eventName: "schedule",
      configuredMode,
      maxRepos: 1,
      rolloutPercent: 100,
      totalRepositories: 25,
    }), RangeError);
  }
  assert.deepEqual(resolvePolicy({
    eventName: "schedule",
    configuredMode: "invalid-but-disabled",
    campaignEnabled: false,
    maxRepos: 0,
    rolloutPercent: 0,
    totalRepositories: 25,
  }), {
    enabled: false,
    safeOutputMode: null,
    safeOutputRepo: "",
    effectiveMaxRepos: 0,
    dispatchAllowed: false,
  });
  assert.equal(resolvePolicy({
    eventName: "schedule",
    configuredMode: "",
    maxRepos: 1,
    rolloutPercent: 100,
    totalRepositories: 25,
  }).safeOutputMode, "review");
  assert.equal(resolvePolicy({
    eventName: "workflow_dispatch",
    manualMode: "",
    maxRepos: 1,
    rolloutPercent: 100,
    totalRepositories: 25,
  }).safeOutputMode, "review");
});

test("manual requests run independently of scheduled configuration", () => {
  for (const manualMode of modes) {
    const policy = resolvePolicy({
      eventName: "workflow_dispatch",
      configuredMode: manualMode === "review" ? "live" : "review",
      manualMode,
      manualReviewRepo: manualMode === "review" ? "acme/manual-review" : "",
      maxRepos: 1,
      rolloutPercent: 100,
      totalRepositories: 25,
    });

    assert.equal(policy.enabled, true, manualMode);
    assert.equal(policy.safeOutputMode, manualMode, manualMode);
    assert.equal(policy.dispatchAllowed, true, manualMode);
  }
});

test("enterprise-scale limits remain bounded across inventory sizes", () => {
  const inventorySizes = [0, 1, 2, 10, 99, 100, 999, 1000, 10_000, 1_000_000];
  const rolloutPercents = [1, 2, 10, 33, 50, 99, 100];
  const absoluteCaps = [1, 10, 50, 1000];

  for (const totalRepositories of inventorySizes) {
    for (const rolloutPercent of rolloutPercents) {
      for (const maxRepos of absoluteCaps) {
        const policy = resolvePolicy({
          eventName: "schedule",
          configuredMode: "live",
          maxRepos,
          rolloutPercent,
          totalRepositories,
          dispatchMax: 50,
          eligibleWorkers: 1,
        });
        const percentageCap = totalRepositories === 0
          ? 0
          : Math.max(1, Math.ceil(totalRepositories * rolloutPercent / 100));

        assert.equal(policy.effectiveMaxRepos, Math.min(maxRepos, percentageCap, 50));
        assert.ok(policy.effectiveMaxRepos <= 50);
      }
    }
  }

  assert.equal(resolvePolicy({
    eventName: "schedule",
    configuredMode: "live",
    maxRepos: 1000,
    rolloutPercent: 100,
    totalRepositories: 1_000_000,
    dispatchMax: 20,
    eligibleWorkers: 4,
  }).effectiveMaxRepos, 5, "four workers share the optimization dispatch budget");
  assert.equal(resolvePolicy({
    eventName: "schedule",
    configuredMode: "live",
    maxRepos: 1000,
    rolloutPercent: 100,
    totalRepositories: 1_000_000,
    dispatchMax: 20,
    eligibleWorkers: 0,
  }).effectiveMaxRepos, 0, "disabled workers form a worker-level kill switch");
});

test("enterprise defaults, budgets, timeouts, and concurrency are finite", () => {
  const expected = {
    "uk-ai-advisory.md": { credits: 250, timeout: 15, dispatchMax: 50, workers: 1 },
    "uk-ai-advisory-campaign-maintainer.md": { credits: 200, timeout: 20 },
    "uk-ai-advisory-operational-resilience.md": { credits: 600, timeout: 30 },
    "bug-bash.md": { credits: 200, timeout: 15, dispatchMax: 1, workers: 1 },
    "bug-bash-investigation.md": { credits: 500, timeout: 45 },
    "repo-assist.md": { credits: 250, timeout: 15, dispatchMax: 3, workers: 4 },
    "repo-assist-maintenance.md": { credits: 600, timeout: 45 },
    "cao-evolution.md": { credits: 250, timeout: 15, dispatchMax: 6, workers: 6 },
    "cao-evolution-catalog-advisor.md": { credits: 400, timeout: 40 },
    "cao-evolution-efficiency.md": { credits: 450, timeout: 40 },
    "cao-evolution-integrity.md": { credits: 400, timeout: 35 },
    "cao-evolution-reliability.md": { credits: 450, timeout: 40 },
    "dependabot.md": { credits: 250, timeout: 15, dispatchMax: 50, workers: 1 },
    "dreaming.md": { credits: 250, timeout: 15, dispatchMax: 12, workers: 1 },
    "eslint-rules.md": { credits: 250, timeout: 15, dispatchMax: 5, workers: 5 },
    "eslint-rules-inventory.md": { credits: 200, timeout: 20 },
    "eslint-rules-miner.md": { credits: 450, timeout: 30 },
    "eslint-rules-refiner.md": { credits: 450, timeout: 30 },
    "eslint-rules-applier.md": { credits: 350, timeout: 25 },
    "eslint-rules-librarian.md": { credits: 300, timeout: 25 },
    "eu-cra-compliance.md": { credits: 200, timeout: 15, dispatchMax: 48, workers: 6 },
    "eu-cra-compliance-campaign-maintainer.md": { credits: 200, timeout: 20 },
    "optimization.md": { credits: 250, timeout: 15, dispatchMax: 12, workers: 2 },
    "self-care.md": { credits: 200, timeout: 15, dispatchMax: 21, workers: 21 },
    "cao-evolution-failures-investigator.md": { credits: 500, timeout: 30 },
    "cao-evolution-compiler-security.md": { credits: 500, timeout: 45 },
    "dependabot-update-planner.md": { credits: 600, timeout: 60 },
    "dreaming-agents-md-curator.md": { credits: 400, timeout: 35 },
    "eu-cra-compliance-article-14-reporting-readiness.md": { credits: 100, timeout: 30 },
    "eu-cra-compliance-conformity-release-evidence.md": { credits: 100, timeout: 30 },
    "eu-cra-compliance-scope-classifier.md": { credits: 100, timeout: 25 },
    "eu-cra-compliance-security-requirements-auditor.md": { credits: 100, timeout: 30 },
    "eu-cra-compliance-supply-chain-sbom-auditor.md": { credits: 100, timeout: 30 },
    "eu-cra-compliance-vulnerability-handling-auditor.md": { credits: 100, timeout: 30 },
    "optimization-token-auditor.md": { credits: 400, timeout: 30 },
    "optimization-token-optimizer.md": { credits: 500, timeout: 40 },
    "software-development-practices.md": { credits: 250, timeout: 15, dispatchMax: 20, workers: 2 },
    "software-development-practices-github-well-architected.md": { credits: 400, timeout: 30 },
    "software-development-practices-nist-ssdf.md": { credits: 400, timeout: 30 },
    "self-care-accessibility-checker.md": { credits: 400, timeout: 30 },
    "self-care-agent-discoverability.md": { credits: 250, timeout: 30 },
    "self-care-cli-consistency.md": { credits: 500, timeout: 60 },
    "self-care-code-improvement.md": { credits: 400, timeout: 30 },
    "self-care-dashboard-data-schema.md": { credits: 100, timeout: 15 },
    "self-care-dashboard-debug-logging.md": { credits: 350, timeout: 40 },
    "self-care-dashboard-performance.md": { credits: 400, timeout: 30 },
    "self-care-data-acquisition-audit.md": { credits: 300, timeout: 20 },
    "self-care-dashboard-language-refactor.md": { credits: 400, timeout: 30 },
    "self-care-dashboard-review.md": { credits: 400, timeout: 30 },
    "self-care-docs-build-time-investigator.md": { credits: 400, timeout: 30 },
    "self-care-docs-maintainer.md": { credits: 500, timeout: 30 },
    "self-care-glossary.md": { credits: 400, timeout: 30 },
    "self-care-open-source-failures.md": { credits: 500, timeout: 30 },
    "self-care-pages-health.md": { credits: 400, timeout: 120 },
    "self-care-primer-brand-checker.md": { credits: 400, timeout: 25 },
    "self-care-reactive-ui-expert.md": { credits: 500, timeout: 45 },
    "self-care-server-go-logging.md": { credits: 400, timeout: 40 },
    "self-care-server-soc2.md": { credits: 400, timeout: 45 },
    "self-care-specs-maintainer.md": { credits: 500, timeout: 30 },
  };

  for (const [name, limits] of Object.entries(expected)) {
    const source = workflow(name);
    assert.match(source, new RegExp(`max-ai-credits: ${limits.credits}`), name);
    assert.match(source, new RegExp(`timeout-minutes: ${limits.timeout}`), name);
    assert.match(source, /concurrency:\n\s+group:.*\n\s+job-discriminator: \$\{\{ github\.run_id \}\}\n\s+cancel-in-progress: true/, name);
    assert.doesNotMatch(source, /^\s+(contents|actions|issues|pull-requests): write$/m, name);
    if (limits.dispatchMax) {
      assert.match(source, new RegExp(`dispatch_max: ${limits.dispatchMax}`), name);
      assert.match(source, new RegExp(`dispatch-workflow:[\\s\\S]*?max: ${limits.dispatchMax}`), name);
      assert.match(source, new RegExp(`orchestrator_credits: ${limits.credits}`), name);
    }
  }

  const control = workflow("shared/control.md");
  const precompute = controlPrecompute();
  assert.match(control, /campaign:\n\s+type: string\n\s+default: "__none__"/);
  assert.match(control, /role:\n\s+type: choice\n\s+options: \[orchestrator, worker, standalone, direct\]/);
  assert.match(control, /worker:\n\s+type: string\n\s+default: "__none__"/);
  assert.match(precompute, /join\(admissionDirectory\(\), "effective-policy\.json"\)/);
  assert.doesNotMatch(control, /^steps:/m);
  assert.match(precompute, /max_repos must be an integer from 1 through 1000/);
  assert.match(precompute, /max_scan_repos must be an integer from 1 through 100000/);
  assert.match(precompute, /assertUniqueStrings\(scope\["allowed-repositories"\], "control-plane\.scope\.allowed-repositories"/);
  assert.match(precompute, /source: "allowed_repos"/);
  assert.match(precompute, /inventory_version/);
  assert.match(precompute, /batch_id/);
  assert.match(precompute, /id % cellCount/);
  assert.match(precompute, /dispatch_max must be an integer from 1 through 1000/);
  assert.match(precompute, /Math\.min\(eligibleWorkers, context\.dispatchMaximum\)/);
  assert.match(precompute, /Math\.floor\(context\.dispatchMaximum \/ workersPerTarget\)/);
  assert.doesNotMatch(precompute, /monthly_credit_budget must be a non-negative integer/);
  assert.doesNotMatch(precompute, /gh", \["aw", "logs"/);
  assert.doesNotMatch(precompute, /--paginate/);
  assert.doesNotMatch(`${control}\n${precompute}`, /vars\.CENTRAL_AGENTIC_OPS_|repositories: \["\*"\]/);
});

test("control workflows deny before activation through one shared admission contract", () => {
  const sharedControl = workflow("shared/control.md");
  const registrations = operationWorkflowRegistrations();
  const controlled = readdirSync(workflowsDirectory)
    .filter((name) => name.endsWith(".md") && !name.endsWith(".lock.md"))
    .map((name) => [name, workflow(name)])
    .filter(([name]) =>
      workflowConfig(name).imports?.some((entry) => entry.uses === "shared/control.md"));

  assert.deepEqual(
    controlled.map(([name]) => name).sort(),
    registrations.map(({ name }) => name).sort(),
    "shared control imports must match the declarative operation inventory",
  );
  for (const { campaignName, policy, descriptor } of operationCampaigns()) {
    assert.equal(descriptor.campaign, campaignName);
    assert.deepEqual(
      Object.fromEntries(Object.entries(policy.workers).map(([workerName, config]) => [
        workerName,
        config.workflow,
      ])),
      descriptor.workers,
      `${campaignName} policy workers must match its campaign descriptor`,
    );
  }
  assert.equal(
    [...sharedControl.matchAll(/^\s+- name: Evaluate Central Agentic Ops admission$/gm)].length,
    1,
  );
  assert.match(sharedControl, /^\s+id: cao_admission$/m);
  assert.match(sharedControl, /Generate CAO pre-activation GitHub App token/);
  assert.match(sharedControl, /actions\/create-github-app-token@[0-9a-f]{40} # v3\.2\.0/);
  assert.match(sharedControl, /permission-actions: read[\s\S]*?permission-contents: read/);
  assert.match(stepBlock(sharedControl, "Generate CAO pre-activation GitHub App token"), /repositories: \$\{\{ github\.event\.repository\.name \}\}/);
  assert.match(sharedControl, /CAO_API_TOKEN: \$\{\{ github\.token \}\}/);
  assert.match(sharedControl, /name: Checkout CAO control modules/);
  assert.match(sharedControl, /actions\/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7\.0\.1/);
  assert.match(sharedControl, /ref: \$\{\{ github\.workflow_sha \}\}/);
  assert.match(sharedControl, /path: \.cao\n/);
  assert.match(sharedControl, /sparse-checkout: \.github/);
  assert.match(sharedControl, /sparse-checkout-cone-mode: true/);
  assert.match(sharedControl, /fetch-depth: 1/);
  assert.doesNotMatch(sharedControl, /gh api --method GET "repos\/\$\{GITHUB_REPOSITORY\}\/contents\/\.github\/cao\/src/);
  assert.doesNotMatch(sharedControl, /base64\s+(?:-d|--decode)/);
  assert.match(stepBlock(sharedControl, "Evaluate Central Agentic Ops admission"), /uses: actions\/github-script@[0-9a-f]{40} # v9\.0\.0/);
  assert.match(stepBlock(sharedControl, "Evaluate Central Agentic Ops admission"), /await control\.main\(\{ core, github, context, exec, io, getOctokit \}, \['admit'\]\)/);
  assert.match(stepBlock(sharedControl, "Run CAO control precompute"), /uses: actions\/github-script@[0-9a-f]{40} # v9\.0\.0/);
  assert.match(stepBlock(sharedControl, "Run CAO control precompute"), /await control\.main\(\{ core, github, context, exec, io, getOctokit \}, \['precompute'\]\)/);
  assert.ok(
    sharedControl.indexOf("- name: Run CAO control precompute")
      < sharedControl.indexOf("- name: Ensure CAO admission record"),
    "precompute must run immediately after admission, before mutable handoff consumers",
  );
  const admissionEnd = sharedControl.indexOf("- name: Run CAO control precompute");
  const admissionStart = sharedControl.indexOf("- name: Evaluate Central Agentic Ops admission");
  assert.deepEqual(
    [...sharedControl.slice(admissionStart, admissionEnd).matchAll(/\n\s+- name: (.+)/g)].map(match => match[1]),
    ["Generate CAO pre-activation GitHub App token"],
  );
  for (const action of sharedControl.matchAll(/^\s*uses:\s+([^./\s][^@\s]+)@([^\s#]+)/gm)) {
    assert.match(action[2], /^[0-9a-f]{40}$/, `shared/control.md: ${action[1]} is mutable`);
  }
  assert.doesNotMatch(sharedControl, /permission-actions: write/);
  assert.doesNotMatch(sharedControl, /CAO_GITHUB_API_GATE|persist-api-gate|gate_writer_token/);
  assert.match(sharedControl, /CAO admission blocked: GitHub API limited until \$\{\{ steps\.cao_admission\.outputs\.github_api_reset_at \}\}/);
  assert.match(sharedControl, /reason == 'github-api-capacity-insufficient'/);
  assert.match(sharedControl, /^\s+id: cao_precompute$/m);
  assert.match(sharedControl, /CAO precompute blocked: GitHub API limited until \$\{\{ steps\.cao_precompute\.outputs\.github_api_reset_at \}\}/);
  assert.match(stepBlock(sharedControl, "\"CAO precompute blocked: GitHub API limited until ${{ steps.cao_precompute.outputs.github_api_reset_at }}\""), /::warning title=CAO precompute blocked by GitHub API capacity/);
  assert.doesNotMatch(stepBlock(sharedControl, "\"CAO precompute blocked: GitHub API limited until ${{ steps.cao_precompute.outputs.github_api_reset_at }}\""), /^\s+exit 1$/m);
  assert.match(stepBlock(sharedControl, "\"CAO precompute blocked: GitHub API capacity unavailable\""), /::warning title=CAO precompute could not verify GitHub API capacity/);
  assert.doesNotMatch(stepBlock(sharedControl, "\"CAO precompute blocked: GitHub API capacity unavailable\""), /^\s+exit 1$/m);
  assert.match(sharedControl, /name: Validate CAO control precompute artifact\n\s+id: cao_precompute_validation\n\s+if: \$\{\{ steps\.cao_admission\.outputs\.authorized == 'true' && steps\.cao_precompute\.outputs\.authorized != 'false' \}\}/);
  assert.match(sharedControl, /name: Upload CAO control precompute artifact\n\s+id: cao_precompute_upload\n\s+if: \$\{\{ always\(\) && steps\.cao_admission\.outputs\.authorized == 'true' && steps\.cao_precompute\.outcome != 'skipped' \}\}/);
  for (const id of [
    "cao_pre_activation_app_token",
    "cao_control_checkout",
    "cao_control_source",
    "cao_admission",
    "cao_precompute",
    "cao_admission_record",
    "cao_admission_upload",
    "cao_admission_capacity_limited",
    "cao_admission_capacity_unavailable",
    "cao_precompute_capacity_limited",
    "cao_precompute_capacity_unavailable",
    "cao_precompute_validation",
    "cao_precompute_upload",
  ]) {
    assert.match(sharedControl, new RegExp(`id: ${id}`), `missing activation step id ${id}`);
    assert.match(
      stepBlock(sharedControl, "Summarize CAO pre-activation steps"),
      new RegExp(`steps\\.${id}\\.outcome`),
      `activation summary omits ${id}`,
    );
  }
  assert.match(stepBlock(sharedControl, "Summarize CAO pre-activation steps"), /decision: "read-credential"/);
  assert.match(stepBlock(sharedControl, "Summarize CAO pre-activation steps"), /decision: "control-handoff"/);
  assert.match(stepBlock(sharedControl, "Summarize CAO pre-activation steps"), /CAO_PRECOMPUTE_AUTHORIZED === "true"/);
  assert.match(stepBlock(sharedControl, "Summarize CAO pre-activation steps"), /steps\.precompute_validation === "success"/);
  assert.match(stepBlock(sharedControl, "Summarize CAO pre-activation steps"), /steps\.precompute_artifact_upload === "success"/);
  assert.doesNotMatch(stepBlock(sharedControl, "Summarize CAO pre-activation steps"), /secrets\.|outputs\.token/);
  assert.match(stepBlock(sharedControl, "Log CAO activation handoff"), /uses: actions\/github-script@[0-9a-f]{40} # v9\.0\.0/);
  assert.match(stepBlock(sharedControl, "Log CAO activation handoff"), /core\.info\(`\[cao] /);
  assert.match(stepBlock(sharedControl, "Log CAO activation handoff"), /decision: "agent-handoff"/);
  assert.match(sharedControl, /const reason = 'cannot read or execute the CAO control modules at github\.workflow_sha'/);
  for (const { name, campaignName, workflowName, role, workerName } of registrations) {
    const source = workflow(name);
    const controlImport = workflowConfig(name).imports.find((entry) => entry.uses === "shared/control.md");

    assert.equal(controlImport.with.campaign, campaignName, name);
    assert.equal(controlImport.with.workflow, workflowName, name);
    assert.equal(controlImport.with.role, role, name);
    assert.equal(controlImport.with.worker, workerName, name);
    assert.equal(
      [...source.matchAll(/^\s+- name: Evaluate Central Agentic Ops admission$/gm)].length,
      0,
      name,
    );
    assert.match(
      source,
      /on:[\s\S]*?permissions:\n\s+(?:actions: read\n\s+contents: read|contents: read\n\s+actions: read)/,
      name,
    );
    assert.match(source, /jobs:\n  pre-activation:\n    outputs:\n      cao_authorized: \$\{\{ steps\.cao_admission\.outputs\.authorized == 'true' && steps\.cao_precompute\.outputs\.authorized != 'false' \}\}/, name);
    const expectedGuard = name === "self-care-server-soc2.md"
      ? "needs.pre_activation.outputs.cao_authorized == 'true' && inputs.target_repo == 'githubnext/gh-cao' && inputs.safe_output_mode == 'live'"
      : "needs.pre_activation.outputs.cao_authorized == 'true'";
    assert.equal(workflowConfig(name).if, expectedGuard, name);

    const generatedName = name.replace(/\.md$/, ".lock.yml");
    const generated = workflow(generatedName);
    const jobs = generatedJobs(generated);
    const preActivation = jobs.get("pre_activation")?.block ?? "";
    const activation = jobs.get("activation")?.block ?? "";

    assert.match(preActivation, /cao_authorized: \$\{\{ steps\.cao_admission\.outputs\.authorized == 'true' && steps\.cao_precompute\.outputs\.authorized != 'false' \}\}/, generatedName);
    assert.match(preActivation, /Evaluate Central Agentic Ops admission/, generatedName);
    assert.match(preActivation, /Generate CAO pre-activation GitHub App token/, generatedName);
    assert.doesNotMatch(preActivation, /github\/gh-aw-actions\/setup-cli@/, generatedName);
    assert.match(preActivation, /CAO admission blocked: GitHub API limited until/, generatedName);
    assert.match(preActivation, /CAO precompute blocked: GitHub API limited until/, generatedName);
    assert.match(stepBlock(preActivation, "\"CAO precompute blocked: GitHub API capacity unavailable\""), /::warning title=CAO precompute could not verify GitHub API capacity/);
    assert.doesNotMatch(stepBlock(preActivation, "\"CAO precompute blocked: GitHub API capacity unavailable\""), /^\s+exit 1$/m);
    assert.match(preActivation, /Validate CAO control precompute artifact[\s\S]*?if: \$\{\{ steps\.cao_admission\.outputs\.authorized == 'true' && steps\.cao_precompute\.outputs\.authorized != 'false' \}\}/, generatedName);
    assert.match(preActivation, /Upload CAO control precompute artifact[\s\S]*?if: \$\{\{ always\(\) && steps\.cao_admission\.outputs\.authorized == 'true' && steps\.cao_precompute\.outcome != 'skipped' \}\}/, generatedName);
    assert.match(activation, /needs\.pre_activation\.outputs\.cao_authorized == 'true'/, generatedName);
    assert.ok(transitivelyNeeds(jobs, "agent", "activation"), `${generatedName}: agent must depend on activation`);
  }
});

test("live workers use central policy as the activation authority", () => {
  const control = workflow("shared/control.md");
  const precompute = controlPrecompute();

  assert.match(control, /campaign:\n\s+type: string\n\s+default: "__none__"/);
  assert.doesNotMatch(precompute, /validateLiveAuthority|target_authority_source|target default branch/);
  assert.match(precompute, /validateWorkerDispatch\(context\)[\s\S]*writeWorkerPrecompute\(context\)/);

  for (const { name, campaignName } of operationWorkflowRegistrations().filter(({ role }) => role !== "standalone")) {
    assert.match(workflow(name), new RegExp(`campaign: ${campaignName}`));
  }
});

test("orchestrators use checked-in policy with independent manual narrowing", () => {
  for (const { name, campaignName } of operationWorkflowRegistrations().filter(({ role }) => role === "orchestrator")) {
    const source = workflow(name);

    assert.match(source, /rollout_percent:\n\s+default: 100\n\s+type: number/);
    assert.match(source, /max_repos:\n\s+default: 1\n\s+type: number/);
    assert.match(source, /safe_output_mode:\n\s+default: "review"\n\s+type: choice/);
    assert.match(source, new RegExp(`campaign: ${campaignName}`));
    assert.match(source, /role: orchestrator/);
    assert.doesNotMatch(source, /vars\.CENTRAL_AGENTIC_OPS_|cell_count:|cell_index:|batch_size:|batch_index:/);
  }
});

test("operation workflows optionally load per-operation markdown steering", () => {
  const campaignSkill = portableSkill("create-cao-campaign");

  assert.match(campaignSkill, /Every orchestrator and worker prompt must include[\s\S]*at the bottom of the Markdown body/);
  assert.match(campaignSkill, /Never place the runtime import at the top of the Markdown body/);
  assert.match(campaignSkill, /\{\{#runtime-import\? \.github\/cao\/<campaign-slug>\.md\}\}/);

  for (const { name, campaignName, workflowName } of operationWorkflowRegistrations()) {
    const operation = workflowName ?? campaignName;
    assert.match(
      workflow(name),
      new RegExp(`\\{\\{#runtime-import\\? \\.github/cao/${operation}\\.md\\}\\}\\s*$`),
    );
  }
});

test("review destinations allow control self-review and isolate other targets", () => {
  const precompute = controlPrecompute();

  assert.match(precompute, /validateOutputDestination/);
  assert.match(precompute, /repositoryEqual\(safeOutputRepository, targetRepository\)[\s\S]*!repositoryEqual\(safeOutputRepository, controlRepository\)/);
  assert.match(precompute, /review safe_output_repo must differ from target_repo/);
  assert.match(precompute, /const targetBound = role === "worker" \|\| role === "standalone"/);
  assert.match(precompute, /live \$\{role\} safe_output_repo must equal target_repo/);
  assert.match(precompute, /repositoryEqual\(safeOutputRepository, controlRepository\)\) \{[\s\S]*central-review-shortcut[\s\S]*return/);
  assert.match(precompute, /ghApi\(`repos\/\$\{safeOutputRepository\}`\)/);
  assert.match(precompute, /review safe_output_repo must be accessible/);
  assert.match(precompute, /non-central review safe_output_repo must be private/);
});

test("persistent modes remain review and live; manual debug forces compiled staging", () => {
  const control = workflow("shared/control.md");
  const precompute = controlPrecompute();

  assert.match(precompute, /typeof policy\.authorized !== "boolean"/);
  assert.match(precompute, /if \(!policy\.authorized\)/);
  assert.match(precompute, /type: "noop"/);
  assert.doesNotMatch(`${control}\n${precompute}`, /preview_only/);
  const config = workflowConfig("shared/control.md");
  assert.equal(config["safe-outputs"].staged, "${{ inputs.safe_output_mode == 'debug' }}");
  assert.equal(config["safe-outputs"]["activation-comments"], "${{ inputs.safe_output_mode != 'debug' }}");
  assert.equal(config["safe-outputs"]["report-failure-as-issue"], "${{ inputs.safe_output_mode != 'debug' }}");
  for (const { name, role } of operationWorkflowRegistrations()) {
    const generated = workflow(name.replace(/\.md$/, ".lock.yml"));
    const jobs = parse(generated).jobs;
    assert.equal(jobs.safe_outputs.env.GH_AW_SAFE_OUTPUTS_STAGED, "${{ inputs.safe_output_mode == 'debug' }}", name);
    const failureStep = jobs.conclusion.steps.find((step) => step.env?.GH_AW_FAILURE_REPORT_AS_ISSUE);
    assert.equal(failureStep?.env.GH_AW_FAILURE_REPORT_AS_ISSUE, "${{ inputs.safe_output_mode != 'debug' }}", name);
    if (role === "worker") {
      assert.equal(workflowConfig(name).on.workflow_dispatch.inputs.safe_output_mode.type, "string", name);
      assert.match(generated, /debug requires a manual, human-authorized worker|safe_output_mode` is `debug`/, name);
    } else {
      assert.ok(!workflowConfig(name).on.workflow_dispatch.inputs.safe_output_mode.options.includes("debug"), name);
    }
  }
});

test("exact campaign target modes flow through candidate dispatch and reporting", () => {
  const control = workflow("shared/control.md");
  const precompute = controlPrecompute();

  assert.match(precompute, /targetRepository: environment\("CAO_TARGET_REPOSITORY"\)/);
  assert.match(precompute, /target_policies\?\.\[repository\.full_name\.toLowerCase\(\)\]\?\.mode \?\? context\.mode/);
  assert.match(precompute, /worker_policies\?\.\[configured\]/);
  assert.match(precompute, /worker disabled by control-plane policy/);
  assert.match(precompute, /resolvedCandidates = candidates\.map/);
  assert.match(control, /treat each candidate's `safe_output_mode` as authoritative for that target/);
  assert.match(control, /start `effective_safe_output_mode` at the selected candidate's `safe_output_mode`/);
  assert.match(control, /when the worker's `max_mode` is `review`, set `effective_safe_output_mode` to `review`/);
  assert.match(control, /never use a worker ceiling to widen a review candidate/);
  assert.match(control, /when `effective_safe_output_mode` is `live`, set `effective_safe_output_repo` to the selected target repository/);
  assert.match(control, /`safe_output_mode`: `effective_safe_output_mode`/);
  assert.match(control, /Selected target modes: <target-to-mode list or none>/);
  assert.match(control, /const effectiveMode = dispatchModes\.size === 0[\s\S]*?: 'mixed';/);
});

test("shared control keeps manual and scheduled routing event-scoped", () => {
  const control = workflow("shared/control.md");
  const precompute = controlPrecompute();

  for (const { name } of operationWorkflowRegistrations().filter(({ role }) => role === "orchestrator")) {
    const orchestrator = workflow(name);
    assert.match(orchestrator, /GH_AW_SAFE_OUTPUT_MODE:.*inputs\.safe_output_mode.*\|\| 'review'/);
    assert.match(orchestrator, /REVIEW_OUTPUT_REPO:.*inputs\.safe_output_repo \|\| github\.repository/);
    assert.match(orchestrator, /SAFE_OUTPUT_REPO:.*== 'review'/);
    assert.doesNotMatch(orchestrator, /vars\.CENTRAL_AGENTIC_OPS_/);
  }
  assert.match(control, /CAO_REQUESTED_MODE: \$\{\{ inputs\.safe_output_mode \|\| '' \}\}/);
  assert.match(control, /CAO_SAFE_OUTPUT_REPOSITORY: \$\{\{ env\.CAO_ROLE == 'standalone'[\s\S]*?\|\| \(inputs\.safe_output_mode/);
  assert.doesNotMatch(control, /review_repo/);
  assert.match(control, /CAO_REQUESTED_ROLLOUT_PERCENT: \$\{\{ inputs\.rollout_percent \|\| '' \}\}/);
  assert.match(control, /select no more than `effective_max_repos` repositories/);

  assert.match(precompute, /rollout_percent must be an integer from 1 through 100/);
  assert.match(precompute, /effective_max_repos:/);
  assert.match(precompute, /Math\.ceil\(resolvedCandidates\.length \* context\.policy\.rollout_percent \/ 100\)/);
  assert.doesNotMatch(precompute, /ROLLOUT_PERCENT.*(?:eval|curl|gh api)/);
});

test("blank manual runs preserve an empty target for allowlisted discovery", () => {
  const control = workflow("shared/control.md");

  assert.match(
    control,
    /CAO_TARGET_REPOSITORY: \$\{\{ inputs\.target_repo \|\| '' \}\}/,
  );
  assert.doesNotMatch(control, /target_repo:.*github\.repository/);
});

test("workers reject disabled, malformed, or over-ceiling dispatches before execution", () => {
  const control = workflow("shared/control.md");
  const precompute = controlPrecompute();

  for (const input of ["worker", "correlation_id", "central_repo", "control_plane_run_url"]) {
    assert.match(control, new RegExp(input));
    assert.match(precompute, new RegExp(`${input}:`));
  }
  assert.match(precompute, /join\(admissionDirectory\(\), "effective-policy\.json"\)/);
  assert.match(control, /Evaluate Central Agentic Ops admission/);
  assert.match(precompute, /validateWorkerDispatch\(context\)[\s\S]*writeWorkerPrecompute\(context\)/);
  assert.match(precompute, /must be review or live/);
  assert.match(precompute, /central_repo must identify the current control repository/);
  assert.match(precompute, /control_plane_run_url must match correlation_id and central_repo/);
  assert.doesNotMatch(`${control}\n${precompute}`, /vars\.CENTRAL_AGENTIC_OPS_/);
});
