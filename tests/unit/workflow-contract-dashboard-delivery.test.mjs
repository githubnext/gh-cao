import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import { existsSync, mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { parse } from "yaml";
import { generatedJobs, root, script, workflow } from "./workflow-contract.helpers.mjs";

// Dashboard, activity, and documentation delivery workflow contracts.



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

test("Documentation publishes only independently generated demo data", () => {
  const workflow = readFileSync(join(root, ".github/workflows/docs.yml"), "utf8");
  const parsed = parse(workflow);
  assert.deepEqual(Object.keys(parsed.jobs), ["build", "deploy"]);
  assert.equal(parsed.on.workflow_run, undefined);
  assert.equal(parsed.permissions.actions, undefined);
  assert.ok(parsed.on.schedule.length);
  const steps = parsed.jobs.build.steps;
  assert.ok(steps.some(({ run }) => run === "npm run docs:build"));
  const build = steps.find(({ name }) => name === "Build synthetic enterprise demo");
  assert.match(build.run, /npm run dashboard:pages:build/);
  assert.match(build.run, /cp -R .tmp\/dashboard-pages dist\/cao/);
  assert.doesNotMatch(workflow, /central-agentic-ops-dashboard|download-artifact|cache\/restore|cao-dashboard.yml|freshness|force_deploy|GH_TOKEN/);
  assert.match(workflow, /actions\/upload-pages-artifact@/);
  assert.match(workflow, /actions\/deploy-pages@/);
});

test("mobile integration measures the synthetic demo rather than operational evidence", () => {
  const workflow = readFileSync(join(root, ".github/workflows/actions.yml"), "utf8");
  const mobile = readFileSync(join(root, "tests/e2e/dashboard-mobile-live.spec.mjs"), "utf8");
  const document = JSON.parse(readFileSync(join(root, "package.json"), "utf8"));
  assert.doesNotMatch(workflow, /DASHBOARD_DATA_URL|githubnext\.github\.io|deployed-data:/);
  assert.match(mobile, /startDashboardPagesPreview/);
  assert.doesNotMatch(mobile, /downloadData:|DASHBOARD_DATA_URL/);
  assert.match(workflow, /MOBILE_MEMORY_MB: 256/);
  assert.match(workflow, /MOBILE_NETWORK_LATENCY_MS: 150/);
  assert.match(document.scripts["dashboard:demo:mobile"], /MOBILE_DEVICE='Pixel 7'/);
  assert.equal(document.scripts["dashboard:local:mobile"], undefined);
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
