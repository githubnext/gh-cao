import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { existsSync, mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { parse } from "yaml";
import { generatedJobs, root, script, workflow } from "./workflow-contract.helpers.mjs";

// Dashboard, activity, and documentation delivery workflow contracts.

test("shared activity cache restores into activation and agent jobs", () => {
  const source = workflow("shared/activity-cache.md");

  assert.match(source, /jobs:\n\s+activation:\n\s+pre-steps:/);
  assert.match(source, /\n\s+agent:\n\s+pre-steps:/);
  assert.equal((source.match(/actions\/cache\/restore@55cc8345863c7cc4c66a329aec7e433d2d1c52a9/g) || []).length, 2);
  assert.equal((source.match(/path: \|/g) || []).length, 2);
  assert.equal((source.match(/key: cao-activity-v5-lookup-/g) || []).length, 2);
  assert.equal((source.match(/restore-keys: \|[\s\S]*?cao-activity-v5-/g) || []).length, 2);
  assert.doesNotMatch(source, /cao-activity-(?!v5-)/);
  assert.doesNotMatch(source, /actions\/cache\/save@/);
  assert.doesNotMatch(source, /Install SQLite|apt-get install.*sqlite3/);

  for (const name of [
    "cao-evolution-failures-investigator.md",
    "optimization-token-auditor.md",
    "optimization-token-optimizer.md",
    "self-care-open-source-failures.md",
  ]) {
    assert.match(workflow(name), /uses: shared\/activity-cache\.md/, name);
  }
});

test("shared activity cache is mounted read-only into the agent sandbox", () => {
  const source = workflow("shared/activity-cache.md");
  const lock = workflow("optimization-token-optimizer.lock.yml");

  assert.match(source, /Prepare CAO activity cache mount\n\s+run: mkdir -p "\$\{\{ runner\.temp \}\}\/cao-activity"/);
  assert.match(source, /sandbox:\n\s+agent:\n\s+mounts:\n\s+- "\$\{\{ runner\.temp \}\}\/cao-activity:\$\{\{ runner\.temp \}\}\/cao-activity:ro"\n\s+- "\$\{\{ runner\.temp \}\}\/cao-activity:\/host\$\{\{ runner\.temp \}\}\/cao-activity:ro"/);
  assert.doesNotMatch(source, /cao-activity:[^"\n]*:rw/);
  assert.match(lock, /--mount "\$\{\{ runner\.temp \}\}\/cao-activity:\$\{\{ runner\.temp \}\}\/cao-activity:ro"/);
  assert.match(lock, /--mount "\$\{\{ runner\.temp \}\}\/cao-activity:\/host\$\{\{ runner\.temp \}\}\/cao-activity:ro"/);
});

test("dashboard authoring corpus workflow generates only validated training examples", () => {
  const source = workflow("dashboard-authoring-corpus.md");
  const dashboardIrSkill = readFileSync(
    join(root, ".github", "skills", "generate-dashboard-ir", "SKILL.md"),
    "utf8",
  ).replaceAll("\r\n", "\n");
  const dashboardAuthoringSkill = readFileSync(
    join(root, ".github", "skills", "dashboard-authoring", "SKILL.md"),
    "utf8",
  ).replaceAll("\r\n", "\n");

  assert.match(source, /^intent: Improve model reliability/m);
  assert.match(
    source,
    /^skills:\n\s+- \.github\/skills\/dashboard-authoring\n\s+- \.github\/skills\/generate-dashboard-ir$/m,
  );
  assert.match(source, /Use the installed `generate-dashboard-ir` skill/);
  assert.match(source, /npm ci --prefix dashboard\/site --ignore-scripts/);
  assert.match(source, /npm --prefix dashboard\/site run validate:corpus/);
  assert.match(source, /Scope every view to the synthetic workflow with a `workflow` filter/);
  assert.match(source, /Use an attainment-only baseline with null value and cutoff/);
  assert.match(source, /create-pull-request:[\s\S]*?allowed-files:\n\s+- "\.github\/skills\/generate-dashboard-ir\/corpus\/index\.json"\n\s+- "\.github\/skills\/generate-dashboard-ir\/corpus\/examples\/\*\.json"\n\s+- "\.github\/skills\/generate-dashboard-ir\/corpus\/examples\/\*\.dashboard\.yml"/);
  assert.doesNotMatch(source, /allowed-files:\n(?:\s+- .*\n)*\s+- "(?!\.github\/skills\/generate-dashboard-ir\/corpus\/)/);
  assert.match(dashboardIrSkill, /^---\nname: generate-dashboard-ir\n/);
  assert.match(dashboardIrSkill, /specification as the semantic authority/);
  assert.match(dashboardIrSkill, /validator entry point as the syntax and structural validation authority/);
  assert.match(dashboardIrSkill, /Do not introduce a new intermediate language/);
  assert.match(dashboardIrSkill, /Read the specification(?: \([^)\n]+\))? and `dashboard\/site\/dashboard\.json`/);
  assert.match(dashboardIrSkill, /Reuse an established built-in view pattern/);
  assert.match(dashboardIrSkill, /Return only the validated complete Dashboard Language YAML document/);
  assert.match(dashboardAuthoringSkill, /Pass the intent to `generate-dashboard-ir`/);
  assert.match(dashboardAuthoringSkill, /Store an operation campaign's production Dashboard Language document at `<campaign>\/dashboard\.json`/);
  assert.match(dashboardAuthoringSkill, /materializer preserves the campaign directory at that same path/);
  assert.match(dashboardAuthoringSkill, /bundles installed `<campaign>\/dashboard\.json` documents into the single deployed `dashboard\.json`/);
  assert.match(dashboardAuthoringSkill, /Do not add campaign pages directly to `dashboard\/site\/dashboard\.json`/);
  assert.doesNotMatch(dashboardAuthoringSkill, /Select only the Dashboard Language sources and fields/);
  assert.doesNotMatch(dashboardAuthoringSkill, /corpus\/index\.json/);
  assert.match(dashboardIrSkill, /## Corpus procedure/);
});

test("dashboard CI runs the campaign quality gates", () => {
  const source = workflow("cid.yml");
  const jobs = generatedJobs(source);
  const pages = jobs.get("pages");
  const lintUnit = jobs.get("lint-unit");
  const playwrightIntegration = jobs.get("playwright-integration");
  const ingestionScale = jobs.get("ingestion-scale");
  const deadViews = jobs.get("dead-views");
  const deadViewsComment = jobs.get("dead-views-comment");
  const queryComplexity = jobs.get("query-complexity");
  const queryComplexityComment = jobs.get("query-complexity-comment");
  const lighthousePerformance = jobs.get("lighthouse-performance");
  const lighthouseComment = jobs.get("lighthouse-comment");

  assert.match(source, /dashboard\/site\/\*\*/);
  // Changes to the shared favicon or docs logos must run the branding scaffold gate.
  const triggers = parse(source).on;
  for (const event of ["pull_request", "push"]) {
    for (const path of ["docs/assets/logo-day.svg", "docs/assets/logo-night.svg", "public/favicon.svg"]) {
      assert.ok(
        triggers[event].paths.includes(path),
        `${event}.paths must include ${path} so the branding scaffold gate runs on logo changes`
      );
    }
  }
  assert.match(source, /working-directory: dashboard\/site/);
  assert.match(source, /cache-dependency-path: dashboard\/site\/package-lock\.json/);
  assert.deepEqual(
    [...jobs.keys()],
    [
      "pages",
      "lint-unit",
      "playwright-integration",
      "ingestion-scale",
      "dead-views",
      "dead-views-comment",
      "query-complexity",
      "query-complexity-comment",
      "lighthouse-performance",
      "lighthouse-comment"
    ]
  );
  assert.deepEqual(pages.needs, []);
  assert.match(pages.block, /go-version-file: server\/go\.mod/);
  assert.match(pages.block, /run: npm run test:e2e:dashboard-pages/);
  assert.match(pages.block, /path: test-results\/dashboard-pages\//);
  assert.deepEqual(lintUnit.needs, []);
  assert.deepEqual(playwrightIntegration.needs, []);
  assert.match(playwrightIntegration.block, /if: github\.event_name != 'pull_request'/);
  assert.deepEqual(ingestionScale.needs, []);
  // The synthetic ingestion payload is slow, so the gate stays on main.
  assert.match(ingestionScale.block, /if: github\.ref == 'refs\/heads\/main'/);
  assert.match(ingestionScale.block, /run: npm run test:e2e:dashboard-ingestion/);
  assert.deepEqual(deadViews.needs, []);
  assert.match(deadViews.block, /npm run --silent analyze:dead-views > dead-views\.md/);
  assert.match(deadViews.block, /cat dead-views\.md >> "\$GITHUB_STEP_SUMMARY"/);
  assert.match(deadViews.block, /name: dashboard-dead-views/);
  assert.deepEqual(deadViewsComment.needs, ["dead-views"]);
  assert.match(
    deadViewsComment.block,
    /if: >-\s+always\(\).*github\.event_name == 'pull_request'.*github\.event\.pull_request\.head\.repo\.full_name == github\.repository/s
  );
  assert.match(deadViewsComment.block, /pull-requests: write/);
  assert.doesNotMatch(deadViewsComment.block, /issues: write/);
  assert.match(deadViewsComment.block, /<!-- dashboard-dead-views -->/);
  assert.match(deadViewsComment.block, /<details><summary><b>Dashboard dead views<\/b><\/summary>/);
  assert.match(deadViewsComment.block, /issues\.updateComment/);
  assert.match(deadViewsComment.block, /issues\.createComment/);
  assert.deepEqual(queryComplexity.needs, []);
  assert.match(queryComplexity.block, /if: github\.event_name != 'pull_request'/);
  assert.match(queryComplexity.block, /node activity\/cao\.mjs dashboard-complexity/);
  assert.match(queryComplexity.block, /npm run dashboard:data:download/);
  assert.match(queryComplexity.block, /--database \.cao\/gh-aw-logs\.sqlite/);
  assert.match(queryComplexity.block, /--input dashboard\/site\/dashboard\.json/);
  assert.match(queryComplexity.block, /--format markdown > query-complexity\.md/);
  assert.match(queryComplexity.block, /cat query-complexity\.md >> "\$GITHUB_STEP_SUMMARY"/);
  assert.match(queryComplexity.block, /uses: actions\/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a/);
  assert.match(queryComplexity.block, /name: dashboard-query-complexity/);
  assert.deepEqual(queryComplexityComment.needs, ["query-complexity"]);
  assert.match(
    queryComplexityComment.block,
    /if: >-\s+always\(\).*github\.event_name == 'pull_request'.*github\.event\.pull_request\.head\.repo\.full_name == github\.repository/s
  );
  assert.match(queryComplexityComment.block, /pull-requests: write/);
  assert.doesNotMatch(queryComplexityComment.block, /issues: write/);
  assert.match(queryComplexityComment.block, /name: dashboard-query-complexity/);
  assert.match(queryComplexityComment.block, /<!-- dashboard-query-complexity -->/);
  assert.match(queryComplexityComment.block, /maximumReportLength = 60000/);
  assert.match(queryComplexityComment.block, /<details><summary><b>Query complexity report<\/b><\/summary>/);
  assert.match(queryComplexityComment.block, /issues\.updateComment/);
  assert.match(queryComplexityComment.block, /issues\.createComment/);
  assert.deepEqual(lighthousePerformance.needs, []);
  assert.match(lighthousePerformance.block, /if: github\.event_name != 'pull_request'/);
  assert.deepEqual(lighthouseComment.needs, ["lighthouse-performance"]);
  for (const command of ["npm run typecheck", "npm run lint", "npm test"]) {
    assert.match(lintUnit.block, new RegExp(`run: ${command.replaceAll(".", "\\.")}`));
  }
  assert.doesNotMatch(lintUnit.block, /playwright|test:e2e/i);
  assert.match(playwrightIntegration.block, /uses: actions\/cache@/);
  assert.match(playwrightIntegration.block, /path: ~\/\.cache\/ms-playwright/);
  assert.match(playwrightIntegration.block, /hashFiles\('dashboard\/site\/package-lock\.json'\)/);
  assert.match(playwrightIntegration.block, /npx playwright install --with-deps chromium/);
  assert.match(playwrightIntegration.block, /run: npm run test:e2e/);
  assert.match(playwrightIntegration.block, /go-version-file: server\/go\.mod/);
  assert.match(playwrightIntegration.block, /go -C \.\.\/\.\.\/server build -o "\$RUNNER_TEMP\/cao-dashboard" \.\/cmd\/cao-dashboard/);
  assert.match(playwrightIntegration.block, /echo "CAO_DASHBOARD_EXECUTABLE=\$RUNNER_TEMP\/cao-dashboard" >> "\$GITHUB_ENV"/);
  assert.ok(playwrightIntegration.block.indexOf("go -C ../../server build")
    < playwrightIntegration.block.indexOf("run: npm run test:e2e"));
  assert.doesNotMatch(playwrightIntegration.block, /run: npm (?:run (?:typecheck|lint)|test)$/m);
  assert.match(lighthousePerformance.block, /npm run test:performance/);
  assert.doesNotMatch(lighthousePerformance.block, /continue-on-error:\s*true/);
  assert.doesNotMatch(lighthousePerformance.block, /pull-requests: write/);
  assert.match(lighthousePerformance.block, /uses: actions\/upload-artifact@043fb46d1a93c77aae656e7c1c64a875d1fc6a0a/);
  assert.match(lighthousePerformance.block, /name: dashboard-lighthouse-performance/);
  assert.match(lighthousePerformance.block, /path: dashboard\/site\/test-results\/lighthouse\//);
  assert.match(lighthousePerformance.block, /if: always\(\)/);
  assert.match(
    lighthouseComment.block,
    /if: >-\s+always\(\).*github\.event_name == 'pull_request'.*github\.event\.pull_request\.head\.repo\.full_name == github\.repository/s
  );
  assert.doesNotMatch(source, /^\s+pull_request_target:/m);
  assert.match(lighthouseComment.block, /issues: write/);
  assert.match(lighthouseComment.block, /pull-requests: read/);
  assert.doesNotMatch(lighthouseComment.block, /pull-requests: write/);
  assert.match(lighthouseComment.block, /uses: actions\/download-artifact@3e5f45b2cfb9172054b4087a40e8e0b5a5461e7c/);
  assert.match(lighthouseComment.block, /continue-on-error: true/);
  assert.match(lighthouseComment.block, /if: steps\.download\.outcome == 'success'/);
  assert.match(lighthouseComment.block, /Array\.isArray\(summary\.results\)/);
  assert.match(lighthouseComment.block, /Lighthouse summary has an unexpected shape; skipping PR feedback/);
  assert.match(lighthouseComment.block, /Array\.isArray\(result\.failures\)/);
  assert.match(lighthouseComment.block, /Lighthouse summary contains unexpected results; skipping malformed entries/);
  assert.match(lighthouseComment.block, /Lighthouse summary contains no usable results; skipping PR feedback/);
  assert.match(lighthouseComment.block, /error instanceof Error \? error\.message : String\(error\)/);
  assert.match(lighthouseComment.block, /head: `\$\{headOwner\}:\$\{headBranch\}`/);
  assert.match(lighthouseComment.block, /using event pull request/);
  assert.match(lighthouseComment.block, /### 📉🚦 Dashboard Lighthouse performance degraded/);
  assert.match(lighthouseComment.block, /### 📈🚦 Dashboard Lighthouse performance results/);
  assert.match(lighthouseComment.block, /const sections = validResults\.map/);
  assert.match(lighthouseComment.block, /All \$\{validResults\.length\} scenarios meet their configured thresholds/);
  assert.match(lighthouseComment.block, /github\.paginate\(github\.rest\.issues\.listComments/);
  assert.match(lighthouseComment.block, /comments\.filter\(\(comment\)/);
  assert.match(lighthouseComment.block, /existing\.slice\(1\)/);
  assert.match(lighthouseComment.block, /issues\.deleteComment/);
  assert.match(lighthouseComment.block, /issues\.updateComment/);
  assert.match(lighthouseComment.block, /issues\.createComment/);
});

test("Dashboard campaign builds artifacts and deploys Pages in one workflow", () => {
  const rootManifest = readFileSync(join(root, "aw.yml"), "utf8");
  const controlPlaneManifest = rootManifest;
  const activityManifest = readFileSync(join(root, "activity", "aw.yml"), "utf8");
  const dashboardManifest = readFileSync(join(root, "dashboard", "aw.yml"), "utf8");
  const materializer = readFileSync(join(root, ".github", "workflows", "shared", "materialize-cao.mjs"), "utf8");
  const runtimeAction = readFileSync(join(root, ".github", "actions", "setup-cao-runtime", "action.yml"), "utf8");
  const controlPlaneCampaign = parse(controlPlaneManifest);
  const dashboardCampaign = parse(dashboardManifest);
  const canonicalPolicyResolver = readFileSync(join(root, ".github", "workflows", "shared", "policy.mjs"), "utf8");
  const activityWorkflow = readFileSync(join(root, ".github", "workflows", "cao-activity.yml"), "utf8");
  assert.match(
    activityWorkflow,
    /Configure GitHub CLI host[\s\S]*?GH_HOST=\$\{GITHUB_SERVER_URL#\*:\/\/\}[\s\S]*?\$GITHUB_ENV/,
  );
  const activityPlanJob = activityWorkflow.match(/\n  plan:\n([\s\S]*?)\n  collect:\n/)?.[1];
  const activityCollectJob = activityWorkflow.match(/\n  collect:\n([\s\S]*?)\n  index:\n/)?.[1];
  const activityIndexJob = activityWorkflow.match(/\n  index:\n([\s\S]*?)\n  cache:\n/)?.[1];
  const activityCacheJob = activityWorkflow.match(/\n  cache:\n([\s\S]*?)\n  notify-failure:\n/)?.[1];
  const activityNotifyFailureJob = activityWorkflow.match(/\n  notify-failure:\n([\s\S]*)/)?.[1];
  const siteBuildScript = readFileSync(join(root, "dashboard", "site", "scripts", "build.mjs"), "utf8");
  const dashboardWorkflow = readFileSync(join(root, ".github", "workflows", "cao-dashboard.yml"), "utf8");
  const dashboardBuildJob = dashboardWorkflow.match(/\n  build:\n([\s\S]*?)\n  cache:\n/)?.[1];
  const dashboardCacheJob = dashboardWorkflow.match(/\n  cache:\n([\s\S]*?)\n  pages:\n/)?.[1];
  const dashboardPagesJob = dashboardWorkflow.match(/\n  pages:\n([\s\S]*?)\n  deploy:\n/)?.[1];
  const dashboardDeployJob = dashboardWorkflow.match(/\n  deploy:\n([\s\S]*?)\n  notify-failure:\n/)?.[1];
  const dashboardNotifyFailureJob = dashboardWorkflow.match(/\n  notify-failure:\n([\s\S]*)/)?.[1];
  const aicUsage = readFileSync(join(root, "dashboard", "report", "aic-usage.mjs"), "utf8");
  const deployedWorkflows = readFileSync(join(root, "activity", "index.mjs"), "utf8");
  const activityCollector = readFileSync(join(root, "activity", "collect-logs.sh"), "utf8");
  const activityLogs = readFileSync(join(root, "activity", "logs.mjs"), "utf8");
  const activityRunner = readFileSync(join(root, "activity", "run-activity.mjs"), "utf8");
  const repositoryMemory = readFileSync(join(root, "activity", "repository-memory.mjs"), "utf8");
  const reportAssets = ["aic-usage.mjs", "activity-collectors.mjs", "bundle-dashboards.mjs", "compose-dashboard-documents.mjs", "configure-site.mjs", "records.mjs", "text-utils.mjs"];
  const activityEntrypoints = new Set(["activity-collectors.mjs"]);
  const buildEntrypoints = new Set(["bundle-dashboards.mjs", "configure-site.mjs"]);
  assert.ok(controlPlaneCampaign.includes.includes("dashboard/aw.yml"));
  assert.match(dashboardManifest, /name: CAO Dashboard/);
  assert.match(controlPlaneManifest, /^\s+- dashboard\/aw\.yml$/m);
  assert.match(dashboardManifest, /^\s+- \.github\/workflows\/cao-dashboard\.yml$/m);
  assert.doesNotMatch(dashboardManifest, /cao-dashboard-build|dispatch-workflow/);
  assert.doesNotMatch(dashboardManifest, /destination: \.github\/cao\//);
  assert.doesNotMatch(dashboardManifest, /^resources:/m);
  assert.match(materializer, /rootResources = \[[\s\S]*?'activity'[\s\S]*?'dashboard'/);
  assert.match(dashboardWorkflow, /Install dashboard build dependencies[\s\S]*?'--omit=dev'/);
  assert.match(runtimeAction, /materialize-cao\.mjs" verify "\$CAO_RUNTIME_BUNDLE"/);
  assert.match(canonicalPolicyResolver, /export function parsePolicy/);
  assert.match(deployedWorkflows, /REPORT_RUN_WINDOW_DAYS/);
  assert.match(activityWorkflow, /REPORT_RUN_WINDOW_DAYS: "30"/);
  assert.doesNotMatch(dashboardWorkflow, /workflow_call:|cao-dashboard-build|dispatch-workflow/);
  assert.match(dashboardWorkflow, /workflow_dispatch:/);
  assert.match(dashboardWorkflow, /workflow_run:[\s\S]*?workflows:[\s\S]*?- CAO Activity[\s\S]*?types:[\s\S]*?- completed/);
  const dashboardConcurrency = parse(dashboardWorkflow).concurrency;
  assert.equal(dashboardConcurrency["cancel-in-progress"], false,
    "Activity completions must not cancel a dashboard build before it publishes");
  assert.match(dashboardConcurrency.group, /github\.event_name == 'workflow_run'[\s\S]*?github\.event\.workflow_run\.conclusion != 'success'[\s\S]*?github\.event\.workflow_run\.head_branch != github\.event\.repository\.default_branch/);
  assert.match(dashboardConcurrency.group, /format\('central-agentic-ops-dashboard-ineligible-\{0\}', github\.run_id\)[\s\S]*?\|\| 'central-agentic-ops-dashboard'/);
  assert.doesNotMatch(dashboardWorkflow, /DISPATCH_WORKFLOW: activity\.yml|Dispatch activity refresh|inputs\.mode/);
  assert.match(dashboardBuildJob, /actions: read[\s\S]*?contents: read/);
  assert.match(dashboardWorkflow, /Restore collected activity data[\s\S]*?id: activity-cache[\s\S]*?actions\/cache\/restore@[0-9a-f]{40}[\s\S]*?restore-keys: \$\{\{ github\.event_name == 'workflow_dispatch' && 'cao-activity-v5-' \|\| '' \}\}/);
  assert.doesNotMatch(dashboardWorkflow, /fail-on-cache-miss: true/);
  assert.match(dashboardWorkflow, /Restore collected activity data[\s\S]*?path: \|[\s\S]*?gh-aw-logs\.sqlite[\s\S]*?gh-aw-logs-shards[\s\S]*?gh-aw-logs-runs[\s\S]*?gh-aw-logs-records[\s\S]*?payload-hashes\.json[\s\S]*?control-settings\.json[\s\S]*?inventory-sources\.json/);
  assert.match(dashboardWorkflow, /Resolve fallback activity run[\s\S]*?if: github\.event_name == 'workflow_run' \|\| steps\.activity-cache\.outputs\.cache-matched-key == ''[\s\S]*?listWorkflowRuns\(\{[\s\S]*?workflow_id: 'cao-activity\.yml'[\s\S]*?branch: context\.payload\.repository\.default_branch[\s\S]*?status: 'success'[\s\S]*?per_page: 100[\s\S]*?core\.setOutput\('run-id', String\(run\.id\)\)/);
  assert.match(dashboardWorkflow, /Download fallback activity data[\s\S]*?if: steps\.activity-cache\.outputs\.cache-matched-key == ''[\s\S]*?actions\/download-artifact@[0-9a-f]{40}[\s\S]*?name: cao-activity-index[\s\S]*?path: \$\{\{ runner\.temp \}\}\/cao-activity[\s\S]*?repository: \$\{\{ github\.repository \}\}[\s\S]*?github-token: \$\{\{ github\.token \}\}[\s\S]*?run-id: \$\{\{ steps\.activity-artifact-run\.outputs\.run-id \}\}/);
  assert.match(dashboardWorkflow, /Upgrade fallback activity data[\s\S]*?actions\/github-script@[0-9a-f]{40}[\s\S]*?'hash-payloads'[\s\S]*?'--runs-dir'[\s\S]*?'--records-dir'[\s\S]*?'ingest-jsonl'[\s\S]*?'--runs-dir'[\s\S]*?'--records-dir'[\s\S]*?'hash-payloads'[\s\S]*?'--output', process\.env\.REPORT_PAYLOAD_HASHES/);
  assert.doesNotMatch(dashboardWorkflow, /'--normalized-dir'/);
  assert.match(dashboardWorkflow, /Upgrade fallback activity data[\s\S]*?const path = require\('path'\)[\s\S]*?path\.join\(process\.env\.RUNNER_TEMP, 'cao-activity', 'agent-summary\.json'\)/);
  assert.match(dashboardWorkflow, /Upgrade fallback activity data[\s\S]*?NODE_DEBUG: cao:ingest[\s\S]*?'--retention-days', '7'[\s\S]*?'--run-retention-days', '30'/);
  assert.doesNotMatch(dashboardWorkflow, /Upgrade fallback activity data\n\s+if:/);
  assert.doesNotMatch(activityWorkflow, /workflow_call:/);
  assert.match(activityWorkflow, /workflow_dispatch:[\s\S]*?request-id:/);
  assert.match(activityWorkflow, /push:\n\s+branches: \[main\]\n\s+paths:\n\s+- \.github\/workflows\/cao\.json\n\s+- \.github\/workflows\/cao-activity\.yml/);
  assert.match(activityWorkflow, /\.github\/workflows\/cao-dashboard\.yml[\s\S]*?"dashboard\/\*\*"[\s\S]*?"\*\/dashboard\.json"/);
  assert.match(activityWorkflow, /schedule:\n\s+- cron: "7 \* \* \* \*"/);
  assert.match(activityWorkflow, /run-name: CAO Activity \/ \$\{\{ inputs\.request-id \|\| github\.run_id \}\}/);
  assert.match(dashboardWorkflow, /run-name: CAO Dashboard \/ \$\{\{ github\.run_id \}\}/);
  assert.match(activityWorkflow, /Resolve activity cache key[\s\S]*?cao-activity-v5-\$\{\{ github\.run_id \}\}-\$\{\{ github\.run_attempt \}\}/);
  assert.match(activityPlanJob, /GH_AW_GITHUB_AUTH_MODE[\s\S]*?GH_AW_GITHUB_READ_PAT_REPOSITORIES[\s\S]*?activityCollectionPlan/);
  const collectionStrategy = parse(activityWorkflow).jobs.collect.strategy;
  assert.equal(collectionStrategy["fail-fast"], false);
  assert.equal(collectionStrategy["max-parallel"], "${{ vars.GH_AW_GITHUB_AUTH_MODE == 'pat' && 1 || 256 }}");
  assert.equal(collectionStrategy.matrix, "${{ fromJSON(needs.plan.outputs.matrix) }}");
  assert.match(activityCollectJob, /permissions:\n\s+actions: read\n\s+contents: read/);
  assert.match(activityCollectJob, /pull-requests: read\n\s+security-events: read/);
  assert.match(activityCollectJob, /permission-security-events: read/);
  assert.match(activityCollectJob, /permission-vulnerability-alerts: read/);
  assert.match(activityCollectJob, /owner: \$\{\{ matrix\.owner \}\}/);
  assert.match(activityCollectJob, /secrets\[fromJSON\(vars\.GH_AW_GITHUB_READ_PAT_REPOSITORIES \|\| '\{\}'\)\[matrix\.credentialRepository\]\]/);
  assert.doesNotMatch(activityCollectJob, /GH_AW_GITHUB_READ_PAT \|\||GH_AW_GITHUB_TOKEN \|\||\|\| github\.token/);
  assert.match(activityIndexJob, /Fetch repository memory refs into a separate checkout[\s\S]*?path: cao-memory-source[\s\S]*?fetch-depth: 0[\s\S]*?persist-credentials: false/);
  assert.match(activityIndexJob, /import \{ mergeActivityFragments \} from "\.\/activity\/cli\/cache\.mjs"[\s\S]*?await mergeActivityFragments/);
  assert.doesNotMatch(activityIndexJob, /-exec cp/);
  assert.match(activityIndexJob, /Recycle expired and oversized activity cache[\s\S]*?recycle-cache[\s\S]*?--retention-days 30[\s\S]*?Generate phased activity shards/);
  assert.match(dashboardWorkflow, /'recycle-cache'[\s\S]*?'hash-payloads'/);
  assert.match(activityIndexJob, /--max-cache-bytes "\$REPORT_MAX_NORMALIZED_CACHE_BYTES"/);
  assert.match(activityIndexJob, /Collect campaign repository memory[\s\S]*?activity\/repository-memory\.mjs[\s\S]*?cao-memory-source[\s\S]*?cao-activity\/memory/);
  assert.match(repositoryMemory, /for-each-ref[\s\S]*?MEMORY_REF_PREFIX/);
  assert.match(repositoryMemory, /ls-tree[\s\S]*?cat-file/);
  assert.doesNotMatch(activityIndexJob, /actions\/cache\/save@/);
  assert.match(activityIndexJob, /Upload activity snapshot[\s\S]*?retention-days: 1[\s\S]*?include-hidden-files: true/);
  assert.match(activityCacheJob, /needs: index[\s\S]*?actions: write[\s\S]*?contents: none/);
  assert.match(activityCacheJob, /Download activity snapshot[\s\S]*?path: \$\{\{ runner\.temp \}\}\/cao-activity[\s\S]*?Save activity cache/);
  assert.doesNotMatch(activityCacheJob, /GH_AW_GITHUB_READ_APP_PRIVATE_KEY|gh aw logs/);
  assert.match(activityNotifyFailureJob, /needs: \[plan, collect, index, cache\]/);
  assert.match(activityNotifyFailureJob, /permissions:\n\s+issues: write/);
  assert.doesNotMatch(activityIndexJob, /issues: write/);
  assert.match(activityNotifyFailureJob, /CAO_ACTIVITY_PLAN_FAILED[\s\S]*?CAO_ACTIVITY_COLLECTION_FAILED[\s\S]*?CAO_ACTIVITY_INDEX_FAILED[\s\S]*?CAO_ACTIVITY_CACHE_FAILED/);
  assert.match(activityNotifyFailureJob, /Assign this issue to an agent/);
  assert.match(activityNotifyFailureJob, /GITHUB_WORKFLOW_SHA[\s\S]*?githubnext\/gh-cao\/blob\/main\/skills\/debug-cao\/SKILL\.md/);
  assert.doesNotMatch(activityNotifyFailureJob, /cancelled/);
  assert.match(dashboardWorkflow, /format\('cao-activity-v5-\{0\}-\{1\}', github\.event\.workflow_run\.id, github\.event\.workflow_run\.run_attempt\)/);
  assert.match(dashboardWorkflow, /format\('cao-activity-v5-lookup-\{0\}-\{1\}', github\.run_id, github\.run_attempt\)/);
  assert.match(dashboardWorkflow, /Restore collected activity data[\s\S]*?cao-activity\/memory/);
  assert.match(activityWorkflow, /Restore legacy activity cache layout[\s\S]*?restore-keys: \|[\s\S]*?cao-activity-v3-/);
  assert.equal((activityWorkflow.match(/cao-activity-v3-/g) || []).length, 1);
  assert.doesNotMatch(dashboardWorkflow, /(?:key|restore-keys): cao-activity-(?!v5-)/);
  assert.match(dashboardWorkflow, /Restore collected activity data[\s\S]*?Resolve fallback activity run[\s\S]*?Download fallback activity data[\s\S]*?Upgrade fallback activity data[\s\S]*?Validate restored activity data[\s\S]*?Assemble Dashboard Language site/);
  assert.match(dashboardWorkflow, /Validate restored activity data[\s\S]*?ACTIVITY_DATABASE: \$\{\{ runner\.temp \}\}\/cao-activity\/gh-aw-logs\.sqlite[\s\S]*?REPORT_GH_AW_LOGS_SHARDS: \$\{\{ runner\.temp \}\}\/cao-activity\/gh-aw-logs-shards[\s\S]*?REPORT_PAYLOAD_HASHES: \$\{\{ runner\.temp \}\}\/cao-activity\/payload-hashes\.json[\s\S]*?REPORT_CONTROL_SETTINGS: \$\{\{ runner\.temp \}\}\/cao-activity\/control-settings\.json[\s\S]*?REPORT_INVENTORY_SOURCES: \$\{\{ runner\.temp \}\}\/cao-activity\/inventory-sources\.json[\s\S]*?'validate-activity-data'[\s\S]*?'--database'[\s\S]*?'--shard-dir'[\s\S]*?'--payload-hashes'[\s\S]*?'--control-settings'[\s\S]*?'--inventory'[\s\S]*?'--memory-manifest'/);
  assert.doesNotMatch(dashboardWorkflow, /Validate restored activity data[\s\S]*?(?:Required activity data file is missing|Restored activity cache directory contents)[\s\S]*?Repair activity database and apply retention/);
  assert.match(dashboardWorkflow, /Copied \$\{fileName\} \(\$\{formatFileSize\(fs\.statSync\(destination\)\.size\)\}\)/);
  assert.match(dashboardWorkflow, /const payloadHashes = Object\.fromEntries[\s\S]*?\^gh-aw-logs-\(\?:runs\|records\)[\s\S]*?Dashboard payload contains no compacted run-information shards/);
  assert.match(dashboardWorkflow, /for \(const directory of \['gh-aw-logs-runs', 'gh-aw-logs-records'\]\)/);
  assert.doesNotMatch(dashboardWorkflow, /for \(const directory of \[[^\]]*'gh-aw-logs-shards'/);
  assert.doesNotMatch(dashboardWorkflow, /core\.info\(`[^`]*\$\{[^}]*size[^}]*\} bytes/);
  assert.match(activityWorkflow, /name: Save activity cache[\s\S]*?path: \|[\s\S]*?gh-aw-logs\.sqlite[\s\S]*?gh-aw-logs-shards[\s\S]*?gh-aw-logs-runs[\s\S]*?gh-aw-logs-records[\s\S]*?control-settings\.json[\s\S]*?inventory-sources\.json/);
  assert.match(dashboardWorkflow, /name: Repair activity database and apply retention[\s\S]*?await exec\.exec\(process\.execPath,[\s\S]*?'repair-database'[\s\S]*?'--database'[\s\S]*?process\.env\.ACTIVITY_DATABASE[\s\S]*?'--run-ttl-days'[\s\S]*?'all'/);
  assert.doesNotMatch(dashboardWorkflow, /control-settings\.mjs|ACTIVITY_ROOT/);
  assert.doesNotMatch(dashboardWorkflow, /Discover deployed agentic workflows|Collect AI Credit usage|Collect operational-value observations|Collect durable dashboard records/);
  assert.match(activityRunner, /control-settings\.mjs[\s\S]*?\.github\/workflows\/shared\/control\.mjs[\s\S]*?\.github\/workflows\/cao\.json[\s\S]*?controlSettingsPath/);
  assert.match(activityRunner, /REPORT_CONTROL_SETTINGS[\s\S]*?path\.join\(runnerTemp, "cao-activity", "control-settings\.json"\)/);
  assert.doesNotMatch(dashboardWorkflow, /^\s+run:/m);
  assert.equal((dashboardWorkflow.match(/actions\/github-script@3a2844b7e9c422d3c10d287c895573f7108da1b3/g) || []).length, 10);
  assert.match(dashboardWorkflow, /Install dashboard build dependencies[\s\S]*?await exec\.exec\('npm', \[[\s\S]*?'ci'[\s\S]*?'--prefix'[\s\S]*?'dashboard\/site'[\s\S]*?'--ignore-scripts'/);
  assert.match(dashboardWorkflow, /Assemble Dashboard Language site[\s\S]*?await exec\.exec\('npm', \[[\s\S]*?'--prefix'[\s\S]*?'dashboard\/site'[\s\S]*?'run'[\s\S]*?'build'[\s\S]*?process\.env\.REPORT_OUTPUT[\s\S]*?controlSettings/);
  assert.match(dashboardWorkflow, /DASHBOARD_COMMIT_SHA: \$\{\{ github\.workflow_sha \}\}[\s\S]*?process\.env\.DASHBOARD_COMMIT_SHA/);
  assert.doesNotMatch(dashboardWorkflow, /DASHBOARD_LAYOUT|DASHBOARD_SITE_ROOT|Resolve dashboard source layout/);
  assert.match(dashboardWorkflow, /core\.info\(`Standalone Pages deployment: \$\{deploy \? 'enabled' : 'disabled'\}`\)/);
  assert.match(dashboardWorkflow, /core\.info\('Dashboard build dependencies installed'\)[\s\S]*?'validate-activity-data'[\s\S]*?core\.info\('Activity database repair and retention completed'\)[\s\S]*?core\.info\('Dashboard site build completed'\)[\s\S]*?core\.info\(`Dashboard artifact assembly completed \(\$\{collectedFiles\.length \+ 1\} collected data files\)`\)/);
  assert.doesNotMatch(dashboardWorkflow, /core\.(?:info|error)\(`[^`]*\$\{activityFile\}/);
  assert.match(siteBuildScript, /from "esbuild"/);
  assert.match(dashboardWorkflow, /const crypto = require\('crypto'\)[\s\S]*?const collectedFiles = \[[\s\S]*?'inventory-sources\.json'[\s\S]*?'gh-aw-logs\.sqlite'[\s\S]*?for \(const fileName of collectedFiles\)[\s\S]*?fs\.copyFileSync[\s\S]*?payloadHashes\['gh-aw-logs\.sqlite'\] = crypto[\s\S]*?createHash\('sha256'\)[\s\S]*?REPORT_OUTPUT[\s\S]*?gh-aw-logs\.sqlite[\s\S]*?const payloadManifest = path\.join\(process\.env\.REPORT_OUTPUT, 'payload-hashes\.json'\)[\s\S]*?Wrote payload-hashes\.json[\s\S]*?\['gh-aw-logs-runs', 'gh-aw-logs-records'\][\s\S]*?recursive: true/);
  assert.doesNotMatch(dashboardWorkflow, /REPORT_DASHBOARD_SOURCES|\/sources\.json/);
  assert.match(dashboardWorkflow, /fs\.cpSync\(memorySource, memoryDestination, \{ recursive: true \}\)/);
  assert.doesNotMatch(dashboardManifest, /redirects\.mjs/);
  assert.doesNotMatch(dashboardWorkflow, /legacy dashboard redirects|redirects\.mjs/);
  assert.match(dashboardWorkflow, /actions\/upload-artifact@[0-9a-f]{40}/);
  assert.match(dashboardWorkflow, /name: central-agentic-ops-dashboard[\s\S]*?include-hidden-files: true/);
  assert.match(dashboardCacheJob, /needs: build[\s\S]*?permissions:[\s\S]*?actions: write/);
  assert.match(dashboardCacheJob, /Download dashboard artifact[\s\S]*?name: central-agentic-ops-dashboard[\s\S]*?path: dist\/cao[\s\S]*?Delete previous dashboard cache/);
  assert.match(dashboardCacheJob, /Delete previous dashboard cache[\s\S]*?cache\.key === process\.env\.DASHBOARD_CACHE_KEY[\s\S]*?cache\.ref === process\.env\.GITHUB_REF[\s\S]*?deleteActionsCacheById[\s\S]*?cache_id: cache\.id/);
  assert.match(dashboardCacheJob, /Save dashboard cache[\s\S]*?actions\/cache\/save@[0-9a-f]{40}[\s\S]*?path: dist\/cao[\s\S]*?key: central-agentic-ops-dashboard/);
  assert.match(dashboardWorkflow, /Resolve dashboard deployment policy[\s\S]*?policy\['control-plane'\]\?\.campaigns\?\.dashboard\?\.deploy[\s\S]*?typeof configuredDeploy !== 'boolean'[\s\S]*?const deploy = configuredDeploy \?\? true[\s\S]*?core\.setOutput\('deploy', String\(deploy\)\)/);
  assert.ok(dashboardBuildJob);
  assert.ok(dashboardCacheJob);
  assert.ok(dashboardPagesJob);
  assert.ok(dashboardDeployJob);
  assert.ok(dashboardNotifyFailureJob);
  assert.doesNotMatch(dashboardBuildJob, /actions: write|pages: write|id-token: write|configure-pages|upload-pages-artifact|deploy-pages/);
  assert.doesNotMatch(dashboardBuildJob, /issues: write/);
  assert.match(dashboardPagesJob, /needs: build[\s\S]*?Download dashboard artifact[\s\S]*?name: central-agentic-ops-dashboard[\s\S]*?Configure Pages[\s\S]*?Upload Pages artifact/);
  assert.doesNotMatch(dashboardPagesJob, /deploy-pages|id-token: write/);
  assert.match(dashboardDeployJob, /needs: \[build, pages\][\s\S]*?actions: read[\s\S]*?id-token: write[\s\S]*?pages: write/);
  assert.match(dashboardDeployJob, /Deploy Pages[\s\S]*?actions\/deploy-pages@[0-9a-f]{40}/);
  assert.doesNotMatch(dashboardDeployJob, /Download dashboard artifact|Configure Pages|Upload Pages artifact|upload-pages-artifact/);
  assert.doesNotMatch(dashboardPagesJob, /Verify dashboard Pages access|getPages|pages-access|continue-on-error/);
  const dashboardPagesSteps = parse(dashboardWorkflow).jobs.pages.steps;
  const dashboardDeploySteps = parse(dashboardWorkflow).jobs.deploy.steps;
  for (const step of dashboardPagesSteps) {
    assert.equal(step.if, undefined, step.name);
  }
  for (const step of dashboardDeploySteps) {
    assert.equal(step.if, undefined, step.name);
  }
  assert.equal(
    dashboardPagesSteps.find(({ name }) => name === "Configure Pages").with["ignore-if-missing"],
    true,
  );
  assert.match(dashboardWorkflow, /pages:\n\s+needs: build\n\s+if: needs\.build\.outputs\.deploy == 'true'/);
  assert.match(dashboardWorkflow, /deploy:\n\s+needs: \[build, pages\]\n\s+if: needs\.build\.outputs\.deploy == 'true'/);
  assert.match(dashboardNotifyFailureJob, /needs: \[build, cache, pages, deploy\]/);
  assert.match(dashboardNotifyFailureJob, /permissions:\n\s+issues: write/);
  assert.match(dashboardNotifyFailureJob, /CAO_DASHBOARD_BUILD_FAILED[\s\S]*?CAO_DASHBOARD_CACHE_FAILED[\s\S]*?CAO_DASHBOARD_PAGES_ARTIFACT_FAILED[\s\S]*?CAO_DASHBOARD_DEPLOY_FAILED/);
  assert.match(dashboardNotifyFailureJob, /Assign this issue to an agent/);
  assert.match(dashboardNotifyFailureJob, /GITHUB_WORKFLOW_SHA[\s\S]*?githubnext\/gh-cao\/blob\/main\/skills\/debug-cao\/SKILL\.md/);
  assert.doesNotMatch(dashboardNotifyFailureJob, /cancelled/);
  assert.match(dashboardWorkflow, /name: CAO Dashboard/);
  assert.doesNotMatch(dashboardWorkflow, /\n  push:/);
  assert.match(dashboardWorkflow, /github\.event_name == 'workflow_run'[\s\S]*?github\.event\.workflow_run\.conclusion == 'success'[\s\S]*?github\.event\.workflow_run\.head_branch == github\.event\.repository\.default_branch/);
  assert.doesNotMatch(dashboardWorkflow, /\.github\/aw\/dashboards|\.github\/aw\/dashboard/);
  assert.doesNotMatch(dashboardWorkflow, /github\.event_name == 'push'/);
  assert.match(dashboardWorkflow, /enablement: false/);
  assert.match(dashboardWorkflow, /pages: write/);
  assert.match(dashboardWorkflow, /id-token: write/);
  assert.doesNotMatch(dashboardWorkflow, /schedule:/);
  assert.equal((dashboardWorkflow.match(/actions\/upload-pages-artifact@/g) || []).length, 1);
  assert.equal((dashboardWorkflow.match(/actions\/deploy-pages@/g) || []).length, 1);
  assert.doesNotMatch(activityWorkflow, /actions\/setup-go|go build|go clean|gh-aw-operational-value/);
  assert.doesNotMatch(dashboardWorkflow, /pages-aic|REPORT_AIC_CACHE/);
  assert.match(activityCollector, /--artifacts usage,experiment,graders \\\s+--start-date/);
  assert.match(aicUsage, /const FIREWALL_HORIZON_DAYS = 30/);
  assert.match(activityCollector, /--start-date "-\$\{window_days\}d"/);
  assert.match(activityCollector, /--cache-before "-\$\{window_days\}d"/);
  assert.match(activityCollector, /--count "\$run_limit"/);
  assert.match(activityCollector, /--max-github-api-rate-limit "\$rate_limit"/);
  assert.match(activityCollector, /--max-storage "\$max_storage"/);
  assert.match(activityCollector, /--repo "\$target_repository"/);
  assert.match(activityCollector, /--prune-older-runs/);
  assert.equal((activityCollector.match(/node activity\/collect-workflow-logs\.mjs gh/g) || []).length, 1);
  assert.doesNotMatch(activityCollector, /gh api|token-efficiency/);
  assert.doesNotMatch(activityWorkflow, /(?:\.github\/aw\/)?optimization\/collect-token-efficiency\.sh/);
  assert.doesNotMatch(activityLogs, /gh aw logs --audit|runGhAw/);
  assert.doesNotMatch(aicUsage, /spawn|runGhAw|"aw", "logs"|--stdin|mapWithConcurrency|REPORT_AIC_CONCURRENCY/);
  assert.doesNotMatch(activityWorkflow, /REPORT_AIC_CONCURRENCY/);
  assert.match(activityWorkflow, /REPORT_AIC_CACHE: \$\{\{ runner\.temp \}\}\/cao-gh-aw-logs/);
  assert.doesNotMatch(activityWorkflow, /REPORT_AIC_CACHE: \$\{\{ runner\.temp \}\}\/cao-activity\//);
  assert.match(activityWorkflow, /Collect owner-scoped dashboard inventory[\s\S]*?discover-workflows[\s\S]*?Download owner-scoped agentic workflow logs/);
  assert.match(dashboardWorkflow, /const collectedFiles = \[[\s\S]*?'inventory-sources\.json'[\s\S]*?for \(const fileName of collectedFiles\)[\s\S]*?fs\.copyFileSync/);
  assert.match(activityWorkflow, /REPORT_GH_AW_LOGS_SHARDS: \$\{\{ runner\.temp \}\}\/cao-activity\/gh-aw-logs-shards/);
  assert.match(activityWorkflow, /Set up Node\.js[\s\S]*?node-version: 24/);
  assert.doesNotMatch(activityWorkflow, /Install SQLite|apt-get install.*sqlite3/);
  assert.match(activityWorkflow, /Ingest activity database[\s\S]*?gh-aw-logs\.sqlite[\s\S]*?ingest-jsonl[\s\S]*?--runs-dir "\$REPORT_GH_AW_LOGS_RUNS"[\s\S]*?--records-dir "\$REPORT_GH_AW_LOGS_RECORDS"/);
  assert.match(activityWorkflow, /Hash activity payloads[\s\S]*?hash-payloads[\s\S]*?--database "\$ACTIVITY_DATABASE"[\s\S]*?--shard-dir "\$REPORT_GH_AW_LOGS_SHARDS"[\s\S]*?--runs-dir "\$REPORT_GH_AW_LOGS_RUNS"[\s\S]*?--records-dir "\$REPORT_GH_AW_LOGS_RECORDS"[\s\S]*?--inventory "\$RUNNER_TEMP\/cao-activity\/inventory-sources\.json"[\s\S]*?--output "\$RUNNER_TEMP\/cao-activity\/payload-hashes\.json"/);
  assert.match(activityWorkflow, /Generate phased activity shards[\s\S]*?--max-bytes 1048576/);
  assert.match(activityWorkflow, /Hash activity payloads[\s\S]*?--max-bytes 1048576/);
  assert.equal((activityWorkflow.match(/path: \|[\s\S]*?\$\{\{ runner\.temp \}\}\/cao-activity\/gh-aw-logs\.sqlite[\s\S]*?\$\{\{ runner\.temp \}\}\/cao-activity\/gh-aw-logs-shards/g) || []).length, 5);
  assert.doesNotMatch(activityCacheJob, /Save activity cache[\s\S]*?path: \$\{\{ runner\.temp \}\}\/cao-activity\s*$/m);
  assert.doesNotMatch(activityManifest, /^resources:/m);
  assert.match(activityWorkflow, /uses: \.\/\.github\/actions\/setup-cao-runtime[\s\S]*?bundle: activity/);
  assert.match(dashboardWorkflow, /uses: \.\/\.github\/actions\/setup-cao-runtime[\s\S]*?bundle: dashboard/);
  assert.match(aicUsage, /Processing \$\{logs\.length\} cached gh-aw log records/);
  assert.doesNotMatch(activityWorkflow, /REPORT_VALUE_CACHE/);
  assert.doesNotMatch(activityWorkflow, /REPORT_VALUE_REPLAY_CACHE/);
  assert.match(dashboardWorkflow, /actions\/cache\/restore@[0-9a-f]{40}/);
  assert.equal((activityWorkflow.match(/actions\/cache\/restore@/g) || []).length, 4);
  assert.equal((activityWorkflow.match(/actions\/cache\/save@/g) || []).length, 1);
  assert.doesNotMatch(activityWorkflow, /dashboard-operational-values/);
  assert.match(activityWorkflow, /Resolve gh-aw compiler version[\s\S]*control\.mjs compiler-version \.github\/workflows\/cao\.json/);
  assert.match(activityWorkflow, /uses: github\/gh-aw-actions\/setup-cli@[0-9a-f]{40}/);
  assert.match(activityWorkflow, /version: \$\{\{ steps\.gh-aw-compiler\.outputs\.version \}\}/);
  assert.match(activityWorkflow, /node activity\/control-settings\.mjs \\\n\s+\.github\/workflows\/shared\/control\.mjs/);
  assert.doesNotMatch(deployedWorkflows, /fetch\(|api\.github\.com|gh api|spawn\(/);
  assert.match(deployedWorkflows, /Build activity index from local workflow inventory/);
  assert.match(deployedWorkflows, /usageArtifactGaps/);
  assert.match(deployedWorkflows, /run\.conclusion === "action_required"\) result\.actionRequired \+= 1/);
  assert.match(deployedWorkflows, /event: firstValue\(run\.event, run\.trigger, null\)/);
  assert.doesNotMatch(deployedWorkflows, /\["failure", "timed_out", "startup_failure", "action_required"\]/);
  for (const assetName of ["data-operations.js", "data-processor.js", "data-worker.js"]) {
    assert.ok(existsSync(join(root, "dashboard", "site", "src", assetName)));
  }
  for (const assetName of reportAssets) {
    const assetPath = join(root, "dashboard", "report", assetName);
    assert.ok(existsSync(assetPath), `missing report script ${assetName}`);
    if (activityEntrypoints.has(assetName)) {
      assert.match(activityRunner, new RegExp(`dashboardReportRoot[\\s\\S]*?${assetName.replace(".", "\\.")}`));
    }
    if (buildEntrypoints.has(assetName)) {
      assert.match(siteBuildScript, new RegExp(`\\.\\./\\.\\./report/${assetName.replace(".", "\\.")}`));
    }
    execFileSync(process.execPath, ["--check", assetPath]);
  }
});

test("Activity campaign owns the shared collected-data cache contract", () => {
  const rootManifest = parse(readFileSync(join(root, "aw.yml"), "utf8"));
  const activityManifest = parse(readFileSync(join(root, "activity", "aw.yml"), "utf8"));
  const workflow = readFileSync(join(root, ".github", "workflows", "cao-activity.yml"), "utf8");
  const activityCollector = readFileSync(join(root, "activity", "collect-logs.sh"), "utf8");
  const readme = readFileSync(join(root, "activity", "README.md"), "utf8");
  const campaignDocument = JSON.parse(readFileSync(join(root, "package.json"), "utf8"));

  assert.equal(activityManifest.name, "CAO Activity");
  assert.deepEqual(activityManifest.includes, [
    ".github/workflows/cao-activity.yml",
    ".github/workflows/cao-value-local.yml",
  ]);
  assert.equal(activityManifest.resources, undefined);
  assert.ok(rootManifest.includes.includes("activity/aw.yml"));
  assert.match(workflow, /schedule:[\s\S]*?cron:/);
  assert.doesNotMatch(workflow, /workflow_call:/);
  assert.match(workflow, /Resolve dashboard control settings[\s\S]*?\.github\/workflows\/shared\/control\.mjs/);
  assert.doesNotMatch(workflow, /Resolve CAO control source|\.github\/aw\/campaigns|\.cao-runtime/);
  assert.match(workflow, /uses: \.\/\.github\/actions\/setup-cao-runtime[\s\S]*?bundle: activity/);
  assert.match(workflow, /uses: github\/gh-aw-actions\/setup-cli@[0-9a-f]{40}/);
  assert.match(workflow, /node activity\/control-settings\.mjs \\\n\s+\.github\/workflows\/shared\/control\.mjs/);
  assert.match(workflow, /workflow_dispatch:[\s\S]*?request-id:/);
  assert.match(workflow, /concurrency:[\s\S]*?cancel-in-progress: false/);
  assert.match(workflow, /actions\/cache\/restore@[0-9a-f]{40}/);
  assert.match(workflow, /actions\/cache\/save@[0-9a-f]{40}/);
  assert.equal((workflow.match(/path: \|[\s\S]*?\$\{\{ runner\.temp \}\}\/cao-activity\/gh-aw-logs\.sqlite[\s\S]*?\$\{\{ runner\.temp \}\}\/cao-activity\/gh-aw-logs-shards/g) || []).length, 5);
  assert.equal((workflow.match(/\$\{\{ runner\.temp \}\}\/cao-activity\/drain3_weights\.json/g) || []).length, 6);
  assert.match(activityCollector, /--drain3-weights "\$drain3_weights_path"/);
  assert.match(activityCollector, /mv "\$generated_weights" "\$drain3_weights_path"/);
  assert.match(workflow, /REPORT_GH_AW_LOGS_SHARDS: \$\{\{ runner\.temp \}\}\/cao-activity\/gh-aw-logs-shards/);
  assert.match(workflow, /REPORT_AIC_CACHE: \$\{\{ runner\.temp \}\}\/cao-gh-aw-logs/);
  assert.match(workflow, /bash activity\/collect-logs\.sh/);
  assert.equal((workflow.match(/issues: read/g) || []).length, 3);
  assert.equal((workflow.match(/pull-requests: read/g) || []).length, 3);
  assert.match(workflow, /Generate owner-scoped GitHub App token[\s\S]*?GH_AW_GITHUB_READ_APP_ID[\s\S]*?GH_AW_GITHUB_READ_APP_PRIVATE_KEY/);
  assert.match(workflow, /actions\/create-github-app-token@[0-9a-f]{40}/);
  assert.doesNotMatch(workflow, /secrets\.GH_AW_GITHUB_READ_PAT\b|secrets\.GH_AW_GITHUB_TOKEN/);
  assert.match(workflow, /GH_AW_GITHUB_AUTH_MODE == 'pat' && secrets\[fromJSON\(vars\.GH_AW_GITHUB_READ_PAT_REPOSITORIES \|\| '\{\}'\)\[matrix\.credentialRepository\]\]/);
  assert.doesNotMatch(workflow, /ACTIVITY_INDEXER|ACTIVITY_LOGS|ACTIVITY_RUNNER|GITHUB_TELEMETRY|cao-gh\.jsonl/);
  assert.match(workflow, /Collect owner-scoped dashboard inventory[\s\S]*?Validate repository visibility boundary[\s\S]*?activity\/repository-visibility\.mjs/);
  assert.match(workflow, /Collect owner-scoped dashboard inventory[\s\S]*?discover-workflows/);
  assert.match(workflow, /Restore activity cache[\s\S]*?Download owner-scoped agentic workflow logs[\s\S]*?Upload owner-scoped activity fragment[\s\S]*?index:[\s\S]*?Merge owner-scoped activity fragments[\s\S]*?Upload activity snapshot[\s\S]*?cache:[\s\S]*?needs: index[\s\S]*?Download activity snapshot[\s\S]*?Save activity cache/);
  assert.match(workflow, /bash activity\/collect-logs\.sh/);
  assert.match(activityCollector, /node activity\/collect-workflow-logs\.mjs gh --audit/);
  assert.match(activityCollector, /REPORT_DEFER_ISSUE_STATUS/);
  assert.match(workflow, /Download owner-scoped agentic workflow logs[\s\S]*?REPORT_DEFER_ISSUE_STATUS: "1"[\s\S]*?Enrich owner-scoped issue statuses[\s\S]*?cao\.mjs issue-status[\s\S]*?Generate phased activity shards/);
  assert.match(workflow, /Ingest activity database[\s\S]*?gh-aw-logs\.sqlite[\s\S]*?ingest-jsonl/);
  assert.doesNotMatch(workflow, /cao\.mjs operational-value/);
  assert.match(workflow, /Retain Value evidence and publish collection health[\s\S]*?defer_args\+=\(--defer "\$reason"\)/);
  assert.doesNotMatch(workflow, /--history-campaign/);
  assert.match(workflow, /Retain Value evidence and publish collection health[\s\S]*?REPORT_VALUE_SEED_SHARDS:[\s\S]*?cp "\$retained_values" "\$value_output"/);
  assert.match(workflow, /paths:[\s\S]*?- "activity\/\*\*"/);
  assert.match(workflow, /paths:[\s\S]*?- "\*\/operational-value\/\*\*"/);
  assert.match(workflow, /Select independent activity credential[\s\S]*?id: activity-credential[\s\S]*?echo "token=\$token" >> "\$GITHUB_OUTPUT"/);
  assert.match(workflow, /Retain Value evidence and publish collection health[\s\S]*?CAO_GITHUB_CREDENTIAL_ID:/);
  assert.match(workflow, /Retain Value evidence and publish collection health[\s\S]*?--apiBudget/);
  assert.match(workflow, /Report collection-health publication failure/);
  const optionalImport = parse(workflow).jobs.index.steps.find(step => step.id === "local-value-import");
  assert.equal(optionalImport["continue-on-error"], true);
  assert.equal(optionalImport["timeout-minutes"], 1);
  assert.match(optionalImport.if, /inputs.recycle-only != true/);
  assert.match(workflow, /Report optional local Value import failure/);
  assert.equal((workflow.match(/path: \$\{\{ runner\.temp \}\}\/cao-activity\s*$/gm) || []).length, 1);
  assert.match(workflow, /cao-activity-v5-\$\{\{ github\.run_id \}\}-/);
  assert.equal(campaignDocument.scripts["activity:local"], undefined);
  assert.equal(campaignDocument.scripts["activity:local:node"], undefined);
  assert.equal(campaignDocument.scripts["activity:run-workflow:local"], undefined);
  assert.match(readme, /gh-aw-logs-shards/);
});

test("Documentation Pages deploys docs with the latest dashboard artifact", () => {
  const workflow = readFileSync(join(root, ".github", "workflows", "docs.yml"), "utf8").replaceAll("\r\n", "\n");
  const dashboardWorkflow = readFileSync(join(root, ".github", "workflows", "cao-dashboard.yml"), "utf8").replaceAll("\r\n", "\n");
  const astroConfig = readFileSync(join(root, "astro.config.mjs"), "utf8");

  assert.equal(existsSync(join(root, ".github", "workflows", "dashboard-build.yml")), false);
  assert.equal(existsSync(join(root, ".github", "workflows", "documentation-pages.yml")), false);
  assert.equal(existsSync(join(root, ".github", "workflows", "documentation-build.yml")), false);

  assert.doesNotMatch(workflow, /dashboard-build|needs: dashboard/);
  const steps = parse(workflow).jobs.build.steps;
  const setupNode = steps.find(({ uses }) => uses?.startsWith("actions/setup-node@"));
  assert.equal(setupNode.with.cache, "npm");
  const installs = steps.filter(({ run }) => run === "npm ci");
  assert.equal(installs.length, 1);
  assert.equal(installs[0].if, undefined, "npm ci must run even on a cache hit");
  assert.notEqual(installs[0]["continue-on-error"], true);
  assert.ok(steps.findIndex(({ run }) => run === "npm run docs:build") > steps.indexOf(installs[0]));
  assert.doesNotMatch(workflow, /\bnode_modules\b/);
  assert.match(workflow, /run: npm run docs:build/);
  assert.ok(
    workflow.indexOf("run: npm run docs:build") < workflow.indexOf("uses: actions/upload-pages-artifact@"),
    "agent documentation validation must complete before the Pages artifact is uploaded",
  );
  assert.match(workflow, /schedule:\n\s+- cron: "\*\/15 \* \* \* \*"/);
  assert.match(workflow, /workflow_dispatch:\n\s+inputs:\n\s+force_deploy:[\s\S]*?type: boolean\n\s+default: false/);
  assert.doesNotMatch(workflow, /inputs\.mode|"mode":/);
  assert.match(workflow, /schedule-freshness:[\s\S]*?github\.event_name == 'schedule'[\s\S]*?github\.event_name == 'workflow_run'[\s\S]*?outputs:[\s\S]*?should-build: \$\{\{ steps\.freshness\.outputs\.should-build \}\}/);
  assert.match(workflow, /Check documentation and dashboard freshness[\s\S]*?actions\/github-script@[0-9a-f]{40}[\s\S]*?repos\.getBranch[\s\S]*?workflow_id: 'docs\.yml'[\s\S]*?workflow_id: 'cao-dashboard\.yml'/);
  assert.match(workflow, /listJobsForWorkflowRun[\s\S]*?job\.name === 'deploy' && job\.conclusion === 'success'/);
  assert.match(workflow, /maxDocsRunsToInspect = 100[\s\S]*?per_page: maxDocsRunsToInspect/);
  assert.match(workflow, /const docsCurrent = deployedDocsRun\?\.head_sha === currentBranch\.data\.commit\.sha/);
  assert.match(workflow, /const dashboardCurrent = latestDashboardRun[\s\S]*?latestDashboardRun\.updated_at\) <= new Date\(deployedDocsRun\.updated_at\)/);
  assert.match(workflow, /Documentation build skipped because the deployed docs and dashboard are current/);
  assert.match(workflow, /build:\n\s+needs: schedule-freshness[\s\S]*?!failure\(\) && !cancelled\(\)[\s\S]*?github\.event_name == 'push'[\s\S]*?github\.event_name == 'workflow_dispatch' && inputs\.force_deploy[\s\S]*?needs\.schedule-freshness\.outputs\.should-build == 'true'/);
  assert.match(workflow, /deploy:\n\s+needs: build\n\s+if: \$\{\{ !failure\(\) && !cancelled\(\) && needs\.build\.result == 'success' \}\}/);
  assert.match(workflow, /workflow_run:[\s\S]*?workflows:[\s\S]*?- CAO Dashboard[\s\S]*?types:[\s\S]*?- completed/);
  assert.match(workflow, /actions: read/);
  assert.match(workflow, /Restore cached CAO Dashboard[\s\S]*?id: dashboard-cache[\s\S]*?actions\/cache\/restore@[0-9a-f]{40}[\s\S]*?path: dist\/cao[\s\S]*?key: central-agentic-ops-dashboard/);
  assert.match(workflow, /Find latest CAO Dashboard run[\s\S]*?if: steps\.dashboard-cache\.outputs\.cache-hit != 'true'[\s\S]*?actions\/workflows\/cao-dashboard\.yml\/runs\?branch=\$DEFAULT_BRANCH&status=success&per_page=20[\s\S]*?\.workflow_runs\[0\]\.id/);
  assert.match(workflow, /Mount latest CAO Dashboard artifact[\s\S]*?if: steps\.dashboard-cache\.outputs\.cache-hit != 'true'[\s\S]*?name: central-agentic-ops-dashboard[\s\S]*?path: dist\/cao[\s\S]*?run-id: \$\{\{ steps\.dashboard-run\.outputs\.run-id \}\}/);
  assert.doesNotMatch(workflow, /gh aw add|DASHBOARD_CAMPAIGN/);
  assert.equal((workflow.match(/actions\/upload-pages-artifact@/g) || []).length, 1);
  assert.equal((workflow.match(/actions\/deploy-pages@/g) || []).length, 1);
  assert.doesNotMatch(dashboardWorkflow, /workflow_call:/);
  assert.match(dashboardWorkflow, /workflow_dispatch:/);
  assert.match(dashboardWorkflow, /gh-aw-logs-shards[\s\S]*?recursive: true/);
  assert.match(dashboardWorkflow, /name: central-agentic-ops-dashboard/);
  assert.doesNotMatch(dashboardWorkflow, /DASHBOARD_LAYOUT|DASHBOARD_SITE_ROOT/);
  assert.match(dashboardWorkflow, /push:|deploy-pages|upload-pages-artifact/);
  assert.match(astroConfig, /base: "\/gh-cao"/);
  assert.match(astroConfig, /rewriteDocsLinks, \{ base: "\/gh-cao" \}/);
  assert.match(astroConfig, /githubnext\/gh-cao\/edit\/main/);
  assert.match(astroConfig, /href: "https:\/\/github\.com\/githubnext\/gh-cao"/);
  assert.match(astroConfig, /label: "Sample Dashboard", link: "\/cao\/"/);
});

test("mobile dashboard integration downloads deployed dashboard data", () => {
  const workflow = readFileSync(join(root, ".github", "workflows", "actions.yml"), "utf8").replaceAll("\r\n", "\n");
  const campaignDocument = JSON.parse(readFileSync(join(root, "package.json"), "utf8"));

  assert.match(workflow, /push:\n\s+branches: \[main\][\s\S]*?dashboard\/site\/\*\*/);
  assert.equal(parse(workflow).on.pull_request, undefined);
  assert.match(workflow, /concurrency:\n\s+group: mobile-dashboard-integration-\$\{\{ github\.ref \}\}\n\s+cancel-in-progress: true/);
  assert.match(workflow, /deployed-data:[\s\S]*?if: github\.event_name == 'schedule' \|\| github\.event_name == 'workflow_dispatch'/);
  assert.equal(parse(workflow).jobs.mobile.if, undefined);
  assert.match(workflow, /include: \$\{\{ fromJSON\(github\.ref == 'refs\/heads\/main'/);
  assert.match(workflow, /refs\/heads\/main' && '[^']*Pixel 7[^']*' \|\| '\[\{"browser":"webkit","device":"iPhone 15"\}\]'/);
  assert.match(workflow, /name: Test deployed dashboard data ingestion\n\s+run: node --test tests\/integration\/dashboard-deployed-data\.test\.mjs/);
  assert.match(workflow, /DASHBOARD_DATA_URL: https:\/\/githubnext\.github\.io\/gh-cao\/cao\/payload-hashes\.json/);
  assert.match(workflow, /MOBILE_DEBUG_SHARD_LIMIT: 10/);
  assert.match(workflow, /Test mobile dashboard with restricted memory[\s\S]*?MOBILE_MEMORY_MB: 256/);
  assert.match(workflow, /Test mobile dashboard with throttled network[\s\S]*?MOBILE_NETWORK_DOWNLOAD_KBPS: 1600/);
  assert.match(workflow, /Test mobile dashboard with restricted memory and network[\s\S]*?MOBILE_MEMORY_MB: 256[\s\S]*?MOBILE_NETWORK_LATENCY_MS: 150/);
  assert.match(workflow, /Upload mobile analysis evidence[\s\S]*?if: always\(\)[\s\S]*?path: test-results\/[\s\S]*?retention-days: 1/);
  assert.match(workflow, /mobile-analysis-comment:[\s\S]*?permissions:[\s\S]*?pull-requests: write/);
  assert.match(workflow, /github\.event_name == 'pull_request'[\s\S]*?Comment with mobile analysis[\s\S]*?mobile-dashboard-analysis/);
  assert.match(workflow, /\| Visible target size \| Visible reflow \| Zoom \| Visible accessible names \|/);
  assert.match(workflow, /### Mobile dashboard analysis/);
  assert.match(workflow, /<details><summary><b>Mobile analysis measurements<\/b><\/summary>[\s\S]*?\| Visible target size \|[\s\S]*?<\/details>/);
  assert.match(workflow, /no visible targets/);
  assert.match(workflow, /existsSync\('mobile-analysis'\)/);
  assert.match(workflow, /width min.*height min/);
  const playwrightConfig = readFileSync(join(root, "tests", "playwright", "configs", "mobile.config.mjs"), "utf8");
  assert.match(playwrightConfig, /preserveOutput: "always"/);
  assert.match(playwrightConfig, /--max-old-space-size=\$\{memoryMb\}/);
  assert.match(campaignDocument.scripts["dashboard:local:mobile"], /DASHBOARD_DATA_URL=https:\/\/githubnext\.github\.io\/gh-cao\/cao\/payload-hashes\.json/);
  assert.match(campaignDocument.scripts["dashboard:local:mobile"], /MOBILE_DEVICE='Pixel 7'/);
  assert.match(campaignDocument.scripts["dashboard:local:mobile"], /MOBILE_MEMORY_MB=256/);
  assert.match(campaignDocument.scripts["dashboard:local:mobile"], /tests\/playwright\/configs\/mobile\.config\.mjs/);
  const mobileTest = readFileSync(join(root, "tests", "e2e", "dashboard-mobile-live.spec.mjs"), "utf8");
  assert.match(mobileTest, /await page\.addInitScript\([\s\S]*?document\.addEventListener\("dashboard-data"[\s\S]*?event\.detail\?\.kind === "refresh"[\s\S]*?await page\.goto/);
  assert.match(mobileTest, /await page\.waitForFunction\([\s\S]*?\["completed", "failed"\]\.includes\(window\.__dashboardRefreshStatus\)[\s\S]*?toBe\("completed"\)[\s\S]*?\[\.\.\.shardResponses\.keys\(\)\]/);
  assert.match(mobileTest, /Network\.emulateNetworkConditions/);
  assert.match(mobileTest, /Performance\.getMetrics/);
  assert.match(mobileTest, /HeapProfiler\.collectGarbage/);
  assert.match(mobileTest, /Memory\.getDOMCounters/);
  assert.match(mobileTest, /VmRSS/);
  assert.match(mobileTest, /payload-hashes\.json/);
  assert.match(mobileTest, /deployedActivityShardEntries\(payloadHashes, mobileDebugShardLimit\(\)\)/);
  assert.match(mobileTest, /gh-aw-logs-\(\?:runs\|records\)/);
  assert.match(mobileTest, /inventory-sources\.json/);
  assert.match(mobileTest, /process\.env\.MOBILE_BROWSER === "webkit"[\s\S]*?Math\.min\(requested \?\? maximumWebKitShardCount, maximumWebKitShardCount\)/);
  assert.match(mobileTest, /for \(const \{ name, sourceName \} of selectedShards\)[\s\S]*?legacyPhaseJsonToJsonl[\s\S]*?pipeline\(response\.body, createWriteStream\(shardPath\)\)/);
  assert.match(mobileTest, /parameters\.set\("debug-shard-limit", String\(shardLimit\)\)/);
  assert.doesNotMatch(mobileTest, /fetch\(dataUrl\)/);
  assert.doesNotMatch(mobileTest, /logsResponse\.text\(\)/);
  assert.match(mobileTest, /mobile-dashboard\.png/);
  assert.match(mobileTest, /minimumTargetSize/);
  assert.match(mobileTest, /horizontal page scrolling/);
  assert.match(workflow, /Peak process PSS \| Peak JS heap \| Retained JS heap \| Source payload/);
  const deployedDataTest = readFileSync(join(root, "tests", "integration", "dashboard-deployed-data.test.mjs"), "utf8");
  assert.match(deployedDataTest, /deployedActivityShardEntries\(manifest\)[\s\S]*?ingestNormalizedJsonl\(indexedDB, chunks/);
  assert.match(deployedDataTest, /expectedPhase: name\.startsWith\("gh-aw-logs-runs\/"\) \? "runs" : "records"/);
  assert.doesNotMatch(deployedDataTest, /gh-aw-logs-shards/);
  assert.doesNotMatch(deployedDataTest, /response\.text\(\)/);
  assert.match(deployedDataTest, /event\.source === "firewall"/);
  for (const source of ["workflows", "runs", "domains", "tools", "audits", "issues"]) {
    assert.match(deployedDataTest, new RegExp(`readCollection\\(indexedDB, "${source}"\\)`));
  }
  assert.doesNotMatch(workflow, /actions\/cache|cao-dashboard-/);
  assert.doesNotMatch(workflow, /GH_TOKEN:/);
});

test("deployed integration checks Pages without the retired hosted dashboard", () => {
  const workflow = readFileSync(join(root, ".github", "workflows", "dashboard-deployed-integration.yml"), "utf8");

  assert.match(workflow, /run: npm run test:e2e:dashboard-deployed/);
  assert.match(workflow, /if: steps\.test\.outcome == 'failure'/);
  assert.doesNotMatch(workflow, /dashboard-hosted-backfill|steps\.backfill|cao\.githubnext\.com/);
});

test("Documentation site uses stock Starlight without external themes", () => {
  const campaignJson = JSON.parse(readFileSync(join(root, "package.json"), "utf8"));
  const astroConfig = readFileSync(join(root, "astro.config.mjs"), "utf8");
  const dependencies = { ...campaignJson.dependencies, ...campaignJson.devDependencies };

  assert.deepEqual(Object.keys(dependencies).filter((name) => name.startsWith("starlight-theme-")), []);
  assert.doesNotMatch(astroConfig, /starlight-theme-/);
});

test("Dashboard inventory links multiline orchestrator worker lists", () => {
  const temporaryRoot = mkdtempSync(join(tmpdir(), "central-agentic-ops-inventory-"));
  const outputPath = join(temporaryRoot, "control-plane.json");
  try {
    execFileSync(process.execPath, [join(root, "activity", "inventory.mjs")], {
      env: { ...process.env, REPORT_ROOT: root, REPORT_INVENTORY: outputPath },
    });
    const inventory = JSON.parse(readFileSync(outputPath, "utf8"));
    const dependabotBundle = inventory.bundles.find((bundle) => bundle.id === "dependabot");
    const policyCampaigns = JSON.parse(
      readFileSync(join(root, ".github", "workflows", "cao.json"), "utf8"),
    )["control-plane"].campaigns;
    const registeredCampaignIds = Object.keys(policyCampaigns).sort();
    const expectedBundles = registeredCampaignIds.flatMap((campaignId) => {
      const descriptorPath = join(root, campaignId, "cao.json");
      if (!existsSync(descriptorPath)) {
        assert.equal(policyCampaigns[campaignId].workers, undefined, `${campaignId} workers require a campaign descriptor`);
        return [];
      }
      const descriptor = JSON.parse(readFileSync(descriptorPath, "utf8"));
      assert.equal(descriptor.campaign, campaignId, descriptorPath);
      assert.deepEqual(
        Object.fromEntries(Object.entries(policyCampaigns[campaignId].workers).map(([worker, config]) => [
          worker,
          config.workflow,
        ])),
        descriptor.workers,
        `${campaignId} policy workers must match its campaign descriptor`,
      );
      const orchestratorSource = workflow(`${descriptor.orchestrator}.md`);
      const frontmatter = /^---\n([\s\S]*?)\n---/.exec(orchestratorSource)?.[1];
      assert.ok(frontmatter, `${descriptor.orchestrator}.md must have frontmatter`);
      const dispatchWorkflows = parse(frontmatter)["safe-outputs"]["dispatch-workflow"].workflows;
      assert.deepEqual(
        [...dispatchWorkflows].sort(),
        Object.values(descriptor.workers).sort(),
        `${campaignId} dispatch allowlist must match its campaign descriptor`,
      );
      return [{
        id: campaignId,
        workers: dispatchWorkflows,
      }];
    }).sort((left, right) => left.id.localeCompare(right.id));
    assert.deepEqual(inventory.campaigns.map((entry) => entry.id), registeredCampaignIds);
    assert.equal(inventory.campaigns.find((entry) => entry.id === "repo-assist")?.name, "Repo Assist");
    assert.equal(dependabotBundle.readmePath, "dependabot/README.md");
    assert.match(dependabotBundle.readme, /^# Dependabot Campaign\r?\n/);
    assert.match(dependabotBundle.readme, /## Safety Boundaries/);
    assert.deepEqual(inventory.bundles.map((bundle) => ({
      id: bundle.id,
      workers: bundle.workers.map((worker) => worker.id),
    })), expectedBundles);
  } finally {
    rmSync(temporaryRoot, { recursive: true, force: true });
  }
});
