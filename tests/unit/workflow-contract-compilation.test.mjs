import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { cpSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { parse } from "yaml";
import { generatedJobs, ghAwVersion, root, stepBlock, workflow, workflowsDirectory } from "./workflow-contract.helpers.mjs";

// Compiled workflow output contracts.

function workflowConfig(name, directory = workflowsDirectory) {
  const frontmatter = /^---\n([\s\S]*?)\n---/.exec(workflow(name, directory))?.[1];
  assert.ok(frontmatter, `${name} must have frontmatter`);
  return parse(frontmatter);
}

test("compiled workflow expressions do not contain HTML-escaped operators", () => {
  const lockNames = readdirSync(workflowsDirectory).filter((name) => name.endsWith(".lock.yml"));

  for (const lockName of lockNames) {
    const expressions = workflow(lockName).match(/\$\{\{[\s\S]*?\}\}/g) ?? [];
    for (const expression of expressions) {
      assert.doesNotMatch(
        expression,
        /\\+u(?:0026|003c|003e)/i,
        `${lockName} contains an HTML-escaped operator in ${expression}`,
      );
    }
  }
});

test("clean-room compilation emits the expected GitHub Actions settings", { timeout: 120_000 }, () => {
  const temporaryRoot = mkdtempSync(join(tmpdir(), "central-agentic-ops-test-"));

  try {
    cpSync(join(root, ".github"), join(temporaryRoot, ".github"), { recursive: true });
    cpSync(join(root, "AGENTS.md"), join(temporaryRoot, "AGENTS.md"));
    cpSync(join(root, "aw.yml"), join(temporaryRoot, "aw.yml"));
    cpSync(join(root, "README.md"), join(temporaryRoot, "README.md"));
    for (const campaignDirectory of ["activity", "cao-evolution", "dashboard", "dependabot", "optimization", "repo-assist"]) {
      cpSync(join(root, campaignDirectory), join(temporaryRoot, campaignDirectory), { recursive: true });
    }
    for (const manifest of ["aw.yml", "cao-evolution/aw.yml", "dependabot/aw.yml", "optimization/aw.yml", "repo-assist/aw.yml"]) {
      const manifestPath = join(temporaryRoot, manifest);
      writeFileSync(manifestPath, readFileSync(manifestPath, "utf8").replaceAll(ghAwVersion, "v0.89.4"));
    }
    execFileSync("git", ["init", "--quiet"], { cwd: temporaryRoot });

    const generatedDirectory = join(temporaryRoot, ".github", "workflows");
    const sourceNames = readdirSync(generatedDirectory)
      .filter((name) => name.endsWith(".md"))
      .sort();
    const expectedLockNames = sourceNames
      .map((name) => name.replace(/\.md$/, ".lock.yml"))
      .sort();
    for (const lockName of readdirSync(generatedDirectory).filter((name) => name.endsWith(".lock.yml"))) {
      rmSync(join(generatedDirectory, lockName));
    }
    const controlContracts = sourceNames.flatMap((sourceName) => {
      const controlImport = workflowConfig(sourceName, generatedDirectory).imports
        ?.find((entry) => entry.uses === "shared/control.md");
      if (!controlImport) return [];
      return [{
        sourceName,
        lockName: sourceName.replace(/\.md$/, ".lock.yml"),
        campaignName: controlImport.with.campaign,
        role: controlImport.with.role,
        workerName: controlImport.with.worker ?? "__none__",
      }];
    });

    execFileSync("gh", [
      "aw",
      "compile",
      "--no-check-update",
      "--schedule-seed",
      "githubnext/gh-cao",
    ], { cwd: temporaryRoot, stdio: "pipe" });

    const lockNames = readdirSync(generatedDirectory)
      .filter((name) => name.endsWith(".lock.yml"))
      .sort();
    const campaignLockNames = controlContracts.map(({ lockName }) => lockName);

    assert.deepEqual(lockNames, expectedLockNames);
    for (const name of campaignLockNames) {
      const generated = workflow(name, generatedDirectory);
      const jobs = generatedJobs(generated);
      const preActivation = jobs.get("pre_activation").block;
      const agent = jobs.get("agent").block;

      assert.match(preActivation, /actions: read/);
      assert.match(preActivation, /name: Evaluate Central Agentic Ops admission/);
      assert.match(preActivation, /name: Checkout CAO control modules/);
      assert.match(preActivation, /sparse-checkout: \|\n\s+\.github\/\n\s+\/activity\/value-acquisition\.mjs/);
      assert.match(preActivation, /sparse-checkout-cone-mode: false/);
      assert.match(preActivation, /name: Resolve CAO control runtime/);
      assert.match(preActivation, /\.cao\/\.github\/workflows\/shared\/control\.mjs/);
      assert.doesNotMatch(preActivation, /name: Checkout installed CAO control source|\.cao-runtime|# Source: /);
      assert.match(preActivation, /fetch-depth: 1/);
      assert.doesNotMatch(preActivation, /contents\/\.github\/cao\/src\/(?:control|policy)\.mjs/);
      assert.doesNotMatch(preActivation, /github\/gh-aw-actions\/setup-cli@/);
      assert.doesNotMatch(preActivation, /steps\.cao_admission\.outputs\.monthly_credit_budget != '0'/);
      assert.match(preActivation, /name: Run CAO control precompute/);
      assert.match(preActivation, /CAO_DISPATCH_MAX: "\d+"/);
      assert.match(preActivation, /CAO_ORCHESTRATOR_CREDITS: "\d+"/);
      assert.match(preActivation, /CAO_WORKER_CREDITS_PER_TARGET: "\d+"/);
      assert.doesNotMatch(preActivation, /github\.aw\.import-inputs/);
      assert.match(preActivation, /uses: actions\/github-script@[0-9a-f]{40}/);
      assert.match(preActivation, /await control\.main\(\{ core, github, context, exec, io, getOctokit \}, \['precompute'\]\)/);
      assert.match(preActivation, /name: Validate CAO control precompute artifact/);
      assert.match(preActivation, /\.authorized == true/);
      assert.match(preActivation, /\.policy_source == \{repository:\$repository,path:"\.github\/workflows\/cao\.json",sha:\$sha\}/);
      assert.match(preActivation, /name: Upload CAO control precompute artifact/);
      const precomputeUpload = parse(stepBlock(preActivation, "Upload CAO control precompute artifact"))[0];
      assert.equal(
        precomputeUpload.with["retention-days"],
        "${{ vars.GH_AW_DEFAULT_ARTIFACT_RETENTION_DAYS || '1.0' }}",
        `${name} must retain the one-day precompute fallback under the compiler retention policy`,
      );

      assert.match(agent, /name: Download CAO control precompute artifact/);
      assert.doesNotMatch(agent, /name: Validate CAO control precompute artifact/);
      assert.doesNotMatch(agent, /contents\/\.github\/cao\/(?:control|policy)/);
      assert.doesNotMatch(agent, /node .*cao\/control\.mjs.*precompute|target-authority\.json|candidate-pages\.jsonl/);
      assert.doesNotMatch(generated, /vars\.CENTRAL_AGENTIC_OPS_|central-agentic-ops\.yml/);
      assert.doesNotMatch(generated, /github\.aw\.import-inputs/);
      assert.doesNotMatch(
        generated,
        /github\.event\.inputs\.(?:max_repos|rollout_percent|correlation_id|central_repo|control_plane_run_url)/
      );
      assert.doesNotMatch(generated, /PREVIEW_ONLY|preview_only/);
      assert.doesNotMatch(generated, /== 'preview'/);
      assert.doesNotMatch(generated, /safe_output_mode == 'private'/);
    }

    for (const { lockName, campaignName, workerName } of controlContracts.filter(({ role }) => role === "orchestrator")) {
      const generated = workflow(lockName, generatedDirectory);
      assert.match(generated, new RegExp(`CAO_CAMPAIGN: ${campaignName}`));
      assert.match(generated, /CAO_ROLE: orchestrator/);
      assert.equal(workerName, "__none__", `${lockName}: orchestrators must not declare a worker identity`);
      assert.match(generated, new RegExp(`CAO_WORKER: ${workerName}`));
      assert.match(generated, /GH_AW_SAFE_OUTPUT_MODE:.*inputs\.safe_output_mode.*\|\| 'review'/);
      assert.match(generated, /CAO_REQUESTED_ROLLOUT_PERCENT: \$\{\{ inputs\.rollout_percent \|\| '' \}\}/);
      assert.match(generated, /rollout_percent:\n\s+default: 100\n\s+type: number/);
      assert.match(generated, /timeout-minutes: 15/);
      assert.match(generated, /cancel-in-progress: true/);
      const jobs = generatedJobs(generated);
      const agent = jobs.get("agent").block;
      const safeOutputs = jobs.get("safe_outputs").block;
      assert.doesNotMatch(agent, /name: Emit control-plane dispatcher telemetry/);
      const outputSetup = safeOutputs.indexOf("name: Setup agent output environment variable");
      const precomputeDownload = safeOutputs.indexOf("name: Download CAO dispatcher precompute artifact");
      const dispatcherTelemetry = safeOutputs.indexOf("name: Emit control-plane dispatcher telemetry");
      const outputHandlers = safeOutputs.indexOf("id: process_safe_outputs");
      assert.ok(outputSetup >= 0 && outputSetup < precomputeDownload, `${lockName} must restore normalized outputs before dispatcher precompute`);
      assert.ok(precomputeDownload < dispatcherTelemetry, `${lockName} must restore dispatcher precompute before telemetry`);
      assert.ok(dispatcherTelemetry < outputHandlers, `${lockName} must emit dispatch intent before processing safe outputs`);
      assert.match(safeOutputs, /otlp\.logSpan\('central-agentic-ops\.dispatcher'/);
    }

    for (const { lockName, campaignName, workerName } of controlContracts.filter(({ role }) => role === "worker")) {
      const generated = workflow(lockName, generatedDirectory);
      assert.match(generated, new RegExp(`CAO_CAMPAIGN: ${campaignName}`));
      assert.match(generated, /CAO_ROLE: worker/);
      assert.match(generated, new RegExp(`CAO_WORKER: ${workerName}`));
      assert.match(generated, /GH_AW_SAFE_OUTPUT_MODE: \$\{\{ inputs\.safe_output_mode \|\| 'review' \}\}/);
      assert.match(generated, /SAFE_OUTPUT_REPO:.*safe_output_mode.*'review'.*safe_output_repo.*github\.repository.*inputs\.target_repo/);
      assert.match(generated, /CAO_REQUESTED_ROLLOUT_PERCENT: \$\{\{ inputs\.rollout_percent \|\| '' \}\}/);
      assert.match(generated, /GH_AW_SAFE_OUTPUTS_CONFIG:/);
    }

    const generatedDependabotPlan = workflow("dependabot-update-planner.lock.yml", generatedDirectory);
    assert.match(generatedDependabotPlan, /create_issue/);
    assert.match(generatedDependabotPlan, /update_issue/);
    assert.match(generatedDependabotPlan, /add_comment/);
    assert.doesNotMatch(generatedDependabotPlan, /create_pull_request/);

    const advisoryMaintainer = workflow("uk-ai-advisory-campaign-maintainer.lock.yml", generatedDirectory);
    assert.match(advisoryMaintainer, /schedule:/);
    assert.match(advisoryMaintainer, /uk-ai-advisory\/implementation-status\.md/);
    assert.match(advisoryMaintainer, /\\?"objective\\?":\{\\?"goal\\?":\\?"cost\\?",\\?"mode\\?":\\?"auto\\?"\}/);

    const craMaintainer = workflow("eu-cra-compliance-campaign-maintainer.lock.yml", generatedDirectory);
    assert.match(craMaintainer, /schedule:/);
    assert.match(craMaintainer, /eu-cra-compliance\/implementation-status\.md/);
    assert.match(craMaintainer, /\\?"objective\\?":\{\\?"goal\\?":\\?"cost\\?",\\?"mode\\?":\\?"auto\\?"\}/);

    const prReviewer = workflow("pr-reviewer.lock.yml", generatedDirectory);
    assert.match(prReviewer, /create_pull_request_review_comment/);
    assert.match(prReviewer, /name: "Workflow PR Validator"/);
    assert.match(prReviewer, /submit_pull_request_review/);
    assert.match(prReviewer, /REQUEST_CHANGES/);
    assert.match(prReviewer, /agenticworkflows/);
    assert.match(prReviewer, /Mount MCP servers as CLIs/);
    assert.doesNotMatch(prReviewer, /go build .*cmd\/gh-aw/);

    const prReviewerSource = workflow("pr-reviewer.md");
    assert.match(prReviewerSource, /types: \[ready_for_review\]/);
    assert.match(prReviewerSource, /^max-daily-ai-credits: -1$/m);
    assert.match(prReviewerSource, /agentic-workflows: true/);
    assert.match(prReviewerSource, /cli-proxy: true/);
    assert.match(prReviewerSource, /agentic-workflows compile/);

    const svgVisualAudit = workflow("svg-visual-audit.lock.yml", generatedDirectory);
    assert.match(svgVisualAudit, /name: "SVG Visual Audit"/);
    assert.match(svgVisualAudit, /create_check_run/);
    assert.match(svgVisualAudit, /upload_artifact/);
    assert.match(svgVisualAudit, /GH_AW_INFO_ALLOWED_DOMAINS: '\["defaults","playwright"\]'/);
    assert.doesNotMatch(svgVisualAudit, /python3 -m http\.server 4321/);

    const docsDiagramGenerator = workflow("docs-explanatory-diagrams.lock.yml", generatedDirectory);
    assert.match(docsDiagramGenerator, /name: "Docs Diagrams"/);
    assert.match(docsDiagramGenerator, /create_pull_request/);
  } finally {
    rmSync(temporaryRoot, { recursive: true, force: true });
  }
});
