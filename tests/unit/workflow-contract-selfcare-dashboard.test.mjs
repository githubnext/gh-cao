import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { execFileSync } from "node:child_process";
import test from "node:test";
import { root, stepBlock, workflow } from "./workflow-contract.helpers.mjs";

// SelfCare workers that audit and improve the dashboard.

test("SelfCare agent discoverability audits bounded access-path routing", () => {
  const source = workflow("self-care-agent-discoverability.md");

  assert.match(source, /worker: agent-discoverability/);
  assert.match(source, /https:\/\/githubnext\.github\.io\/gh-cao\/cao\//);
  assert.match(source, /Start only at/);
  assert.match(source, /at most 25 total HTTP fetches, 8 MCP calls, and 5 local CLI commands/);
  assert.match(source, /`cao_catalog`, then one bounded `cao_query`/);
  assert.match(source, /UI scraping attempted/);
  assert.match(source, /Repository search required/);
  assert.match(source, /create-issue:/);
  assert.match(source, /create-pull-request:/);
  assert.match(source, /Never create both an issue and a pull request/);
  assert.match(source, /draft: true/);
});

test("dashboard view assessment comments only on pull requests", () => {
  const source = workflow("dashboard-views.yml");
  const report = source.slice(source.indexOf("\n  report:"));

  assert.match(report, /always\(\) &&\s+github\.event_name == 'pull_request'/);
  assert.match(report, /github\.rest\.issues\.updateComment/);
  assert.match(report, /github\.rest\.issues\.createComment/);
  assert.doesNotMatch(report, /github\.rest\.issues\.create\(/);
  assert.doesNotMatch(report, /closeIssue/);
  assert.match(source, /typeof summary\.queryUsageGraph === 'string'/);
  assert.match(source, /replaceAll\('```', '` ` `'\)/);
  assert.match(source, /slice\(0, 40_000\)/);
  assert.match(source, /const queryUsageGraphSection = queryUsageGraph/);
  assert.match(source, /<details><summary><b>View-query graph<\/b><\/summary>/);
  assert.match(source, /'```mermaid'/);
  assert.match(source, /queryUsageGraphSection/);
});

test("SelfCare dashboard data schema worker tracks every deployed source with Data Health inference", () => {
  const source = workflow("self-care-dashboard-data-schema.md");

  assert.match(source, /campaign: self-care\n\s+role: worker\n\s+worker: dashboard-data-schema/);
  assert.match(source, /safe_output_mode` is `live`/);
  assert.match(source, /https:\/\/githubnext\.github\.io\/gh-cao\/cao\/sources/);
  assert.match(source, /deriveDataHealthSources/);
  assert.match(source, /manifest\.sources\.sort\(\)/);
  assert.match(source, /allowed-files:\n\s+- "specs\/dashboard-data\.md"/);
  assert.match(source, /if-no-changes: ignore/);
  assert.match(source, /labels: \[self-care, self-care:dashboard-data-schema\]/);
  assert.doesNotMatch(source, /npm (?:install|ci)|npx /);
});

test("SelfCare open source failures uses complete dashboard activity evidence", () => {
  const source = workflow("self-care-open-source-failures.md");

  assert.match(source, /^name: "SelfCare \/ Open Source Failures"$/m);
  assert.match(source, /tracker-id: self-care-open-source-failures/);
  assert.match(source, /uses: shared\/activity-cache\.md/);
  assert.match(source, /deployed-workflows\.json/);
  assert.match(source, /snapshot\.schemaVersion !== 1/);
  assert.match(source, /snapshot\.runHealth\?\.available !== true/);
  assert.match(source, /snapshot\.runHealth\?\.complete !== true/);
  assert.match(source, /snapshot\.runHealth\.windowHours < 168/);
  assert.match(source, /workflow\.visibility === "public"/);
  assert.match(source, /failures\.slice\(0, 100\)/);
  assert.match(source, /exactly `githubnext\/gh-cao`/);
  assert.match(source, /safe_output_mode` is `live`/);
  assert.match(source, /Do not discover repositories/);
  assert.match(source, /conclusion-only runs[\s\S]*?corroborating recurrence/);
  assert.match(source, /labels: \[self-care, self-care:open-source-failures\]/);
  assert.match(source, /title-prefix: "\[self-care:open-source-failures\] "/);
  assert.match(source, /max: 3/);
  assert.match(source, /select the single most important action with the highest expected return on investment/i);
  assert.match(source, /<details><summary><b>Agent prompt<\/b><\/summary>/);
  assert.doesNotMatch(source, /^evals:/m);
  assert.doesNotMatch(source, /^graders:/m);
});

test("SelfCare Primer brand checker audits the dashboard against retrieved guidance", () => {
  const source = workflow("self-care-primer-brand-checker.md");
  const compiled = workflow("self-care-primer-brand-checker.lock.yml");
  const liveGuard = "if: ${{ inputs.target_repo == 'githubnext/gh-cao' && (inputs.safe_output_mode || 'review') == 'live' }}";

  assert.match(source, /^name: "SelfCare \/ Primer"$/m);
  assert.match(source, /campaign: self-care/);
  assert.match(source, /worker: primer-brand-checker/);
  assert.match(source, /safe_output_mode` is `live`/);
  assert.match(source, /skip-if-match: 'is:pr is:open in:title "Primer branding"'/);
  assert.match(source, /@primer\/brand-mcp@0\.74\.0/);
  assert.match(source, /cli-proxy: true/);
  assert.match(source, /dashboard\/site\/src\/styles\.js/);
  assert.match(source, /dashboard\/site\/src\/\*\*\/\*\.js/);
  assert.match(source, /uses: actions\/cache@/);
  assert.match(source, /path: ~\/\.cache\/ms-playwright/);
  assert.match(source, /npm exec --prefix dashboard\/site -- playwright install --with-deps chromium/);
  assert.match(source, /npm --prefix dashboard\/site run test:e2e/);
  assert.match(source, /create-pull-request:\n\s+target-repo:.*\n\s+title-prefix: "Primer branding: "\n\s+draft: true/);
  assert.match(source, /Always finish by calling exactly one safe-output tool/);
  assert.match(source, /no improvement is needed for any other reason, call `noop` once with a concise plain-text reason/);
  assert.match(source, /Never finish with only a textual response/);
  assert.equal(source.split(liveGuard).length - 1, 3);
  assert.match(compiled, /\\"noop\\":\{\\"max\\":2,\\"report-as-issue\\":\\"false\\"\}/);
});

test("SelfCare reactive UI expert applies the local reactive framework skill", () => {
  const source = workflow("self-care-reactive-ui-expert.md");
  const compiled = workflow("self-care-reactive-ui-expert.lock.yml");

  assert.match(source, /^name: "SelfCare \/ Reactive UI Expert"$/m);
  assert.match(source, /campaign: self-care\n\s+role: worker\n\s+worker: reactive-ui-expert/);
  assert.match(source, /skills:\n\s+- \.github\/skills\/reactive-ui/);
  assert.match(source, /\.github\/skills\/migrate-dashboard-view/);
  assert.match(source, /safe_output_mode` is `live`/);
  assert.match(source, /\.github\/skills\/reactive-ui\/SKILL\.md/);
  assert.match(source, /"\.github\/skills\/migrate-dashboard-view\/SKILL\.md"/);
  assert.match(source, /dashboard\/site\/src\/reactive\.js/);
  assert.match(source, /stable keyed rendering/);
  assert.match(source, /state`, `derived`, `effect`, `batch`, `onCleanup`/);
  assert.match(source, /migrate one JavaScript-produced view source to a pure declarative JSON Dashboard Language query and view/);
  assert.match(source, /JavaScript is a last resort/);
  assert.match(source, /pure declarative JSON query-and-view change/);
  assert.match(source, /without adding or changing production JavaScript/);
  assert.match(source, /all selection, filtering, searching, joins, grouping, aggregation, computation, ordering, pagination, and source derivation must be declared in JSON under `dashboard\.queries`/);
  assert.match(source, /Do not implement or preserve JavaScript query callbacks, derived source modules, view-specific projections, main-thread row processing, or test-only JavaScript source synthesis as compatibility paths/);
  assert.match(source, /Define the complete transformation in `dashboard\.queries`/);
  assert.match(source, /do not add a compatibility fallback/);
  assert.match(source, /first record the specific Dashboard Language or shared-presenter limitation/);
  assert.match(source, /Run the focused impacted JavaScript tests/);
  assert.match(source, /npm --prefix dashboard\/site run lint/);
  assert.match(source, /npm run docs:build/);
  assert.match(source, /Call `noop` exactly once when no actionable non-duplicate candidate exists/);
  assert.match(source, /draft: true/);
  assert.match(compiled, /self-care-reactive-ui-expert/);
  assert.match(compiled, /\.github\/skills\/reactive-ui/);
  assert.match(compiled, /\.github\/skills\/migrate-dashboard-view\/SKILL\.md/);
});

test("SelfCare dashboard debug logging worker preserves the logging privacy boundary", () => {
  const source = workflow("self-care-dashboard-debug-logging.md");
  const compiled = workflow("self-care-dashboard-debug-logging.lock.yml");

  assert.match(source, /^name: "SelfCare \/ Dashboard Debug Logging"$/m);
  assert.match(source, /campaign: self-care\n\s+role: worker\n\s+worker: dashboard-debug-logging/);
  assert.match(source, /safe_output_mode` is `live`/);
  assert.match(source, /all-you-can-eat feature grower/);
  assert.match(source, /skip-if-match: 'is:pr is:open "gh-aw-workflow-id: self-care-dashboard-debug-logging" in:body'/);
  assert.match(source, /cache-memory:\n\s+retention-days: 90/);
  assert.match(source, /dashboard-debug-logging-rotation\.json/);
  assert.match(source, /sort their repository-relative paths ascending/);
  assert.match(source, /Begin immediately after `lastPath`, wrapping to the first path/);
  assert.match(source, /Use `createDebug` from `dashboard\/site\/src\/debug\.js`/);
  assert.match(source, /production `\.js` and `\.mjs` files/);
  assert.match(source, /remove the final `\.js` or `\.mjs`, lowercase it/);
  assert.match(source, /source-store\.js` and `source-store\.mjs` use `source-store`/);
  assert.match(source, /Never log secrets, tokens, credentials, prompts, raw records, payload bodies/);
  assert.match(source, /disabled unless the `debug` query argument selects the category/);
  assert.match(source, /must only observe existing values/);
  assert.match(source, /Do not add work solely to feed a disabled logger/);
  assert.match(source, /dashboard\/site\/src\/\*\*\/\*\.js/);
  assert.match(source, /dashboard\/site\/src\/\*\*\/\*\.mjs/);
  assert.match(source, /dashboard\/site\/test\/\*\*\/\*\.mjs/);
  assert.match(source, /npm --prefix dashboard\/site run typecheck/);
  assert.match(source, /Call `noop` exactly once/);
  assert.match(source, /draft: true/);
  assert.match(compiled, /self-care-dashboard-debug-logging/);
  assert.match(compiled, /cache-memory/);
});

test("SelfCare server Go logging worker adds real tests without mocks", () => {
  const source = workflow("self-care-server-go-logging.md");
  const compiled = workflow("self-care-server-go-logging.lock.yml");

  assert.match(source, /^name: "SelfCare \/ Server Go Logging"$/m);
  assert.match(source, /campaign: self-care\n\s+role: worker\n\s+worker: server-go-logging/);
  assert.match(source, /safe_output_mode` is `live`/);
  assert.match(source, /all-you-can-eat feature grower/);
  assert.match(source, /skip-if-match: 'is:pr is:open "gh-aw-workflow-id: self-care-server-go-logging" in:body'/);
  assert.match(source, /server-go-logging-rotation\.json/);
  assert.match(source, /github\.com\/githubnext\/gh-cao\/server\/internal\/logger/);
  assert.match(source, /Do not use mock frameworks, generated mocks, hand-written mocks, fakes, stubs/);
  assert.match(source, /allowed-files:\n\s+- "server\/\*\*\/\*\.go"/);
  assert.match(source, /golangci-lint\/v2\/cmd\/golangci-lint@v2\.14\.0/);
  assert.match(source, /go env GOPATH\)\/bin" >> "\$GITHUB_PATH"/);
  assert.match(source, /working-directory: server/);
  assert.match(source, /test ! -s \/tmp\/golangci-lint-fmt\.diff/);
  assert.match(source, /golangci-lint run \.\/\.\.\./);
  assert.match(source, /go -C server vet \.\/\.\.\./);
  assert.match(source, /go -C server test \.\/\.\.\./);
  assert.match(source, /draft: true/);
  assert.match(compiled, /self-care-server-go-logging/);
  assert.match(compiled, /actions\/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e/);
});

test("SelfCare dashboard reviewer checks deployments through stakeholder personas", () => {
  const source = workflow("self-care-dashboard-review.md");
  const compiled = workflow("self-care-dashboard-review.lock.yml");

  assert.match(source, /name: "SelfCare \/ Dashboard"/);
  assert.match(source, /campaign: self-care\n\s+role: worker\n\s+worker: dashboard-review/);
  assert.match(source, /safe_output_mode` is `live`/);
  assert.match(source, /REPORT_INVENTORY=\/tmp\/gh-aw\/agent\/self-care-dashboard-review\/expected-inventory\.json/);
  assert.match(source, /githubnext\.github\.io\/gh-cao\/cao\//);
  assert.match(source, /^  playwright:\s*$/m);
  assert.match(source, /version: "0\.1\.18"/);
  assert.match(source, /browsers: \[chromium\]/);
  assert.match(compiled, /npm install -g @playwright\/cli@0\.1\.18/);
  assert.match(compiled, /install_playwright_browsers\.sh" chromium/);
  assert.match(compiled, /PLAYWRIGHT_BROWSERS_PATH: \$\{\{ runner\.temp \}\}\/gh-aw\/playwright-browsers/);
  assert.doesNotMatch(source.match(/^env:\n(?:  .*\n)+/m)?.[0] ?? "", /PLAYWRIGHT_BROWSERS_PATH/);
  assert.match(source, /name: Playwright browser launch preflight[\s\S]*?PLAYWRIGHT_BROWSERS_PATH: \$\{\{ runner\.temp \}\}\/gh-aw\/playwright-browsers/);
  assert.doesNotMatch(compiled, /npx --yes playwright@.* install --with-deps chromium/);
  assert.match(source, /playwright-cli -s=preflight-chrome open about:blank[\s\S]*--browser=chromium/);
  assert.match(source, /toolsets: \[repos, issues, actions\]/);
  assert.match(source, /githubnext\.github\.io/);
  assert.match(source, /at most the latest 100 runs from the last 24 hours/);
  assert.match(source, /overview, dispatches, campaigns, repositories, workflows, runs, and coverage routes/);
  assert.match(source, /title-prefix: "\[self-care:dashboard-review\] "/);
  assert.match(source, /close-older-key: self-care-dashboard-review/);
  assert.match(source, /labels: \[self-care, self-care:dashboard-review\]/);
  const expectedInventory = stepBlock(source, "Build expected control-plane inventory");
  const dashboardArtifact = stepBlock(source, "Download and grade the live dashboard artifact");
  const targetGuard = /^\s+if: \$\{\{ inputs\.target_repo == 'githubnext\/gh-cao' \}\}$/m;
  assert.match(expectedInventory, targetGuard);
  assert.match(dashboardArtifact, targetGuard);
  assert.match(source, /central-agentic-ops-dashboard/);
  assert.match(source, /view-grader\.mjs/);
  assert.match(source, /dashboard-artifact/);
  assert.doesNotMatch(dashboardArtifact, /sources\.json/);
  assert.match(dashboardArtifact, /must contain exactly one dashboard\.json/);
  assert.match(source, /successful trusted default-branch build/);
  assert.match(source, /normalized Shannon entropy/);
  assert.match(source, /Reject major page, navigation, information-architecture, or view redesigns/);
  assert.match(source, /<details><summary><b>Agent prompt<\/b><\/summary>/);
  assert.match(source, /Use `\$\{\{ github\.run_id \}\}` as the reproducible random seed/);
  assert.match(source, /Launch the `cfo-dashboard-reviewer`, `cso-dashboard-reviewer`, and `cto-dashboard-reviewer` agents in parallel/);
  assert.match(source, /unique Playwright session name/);
  assert.match(source, /3–5 non-repeating routes and visible interactions per persona/);
  assert.match(source, /representative question/);
  assert.match(source, /grade task efficiency as `efficient`, `workable`, `inefficient`, or `blocked`/);
  assert.match(source, /evidence-backed suggestions for dashboard structure or usability/);
  for (const persona of ["cfo", "cso", "cto"]) {
    assert.match(source, new RegExp(`## agent: \\\`${persona}-dashboard-reviewer\\\``));
  }
  assert.equal(source.match(/^model: small$/gm)?.length, 3);
  assert.doesNotMatch(source, /^\s+(create-pull-request|add-comment|create-discussion|push-to-pull-request-branch):/m);
});

test("SelfCare dashboard reviewer avoids the slow Azure Ubuntu mirror before installing browsers", () => {
  const source = workflow("self-care-dashboard-review.md");
  const compiled = workflow("self-care-dashboard-review.lock.yml");
  const setup = stepBlock(source, "Configure Ubuntu package mirrors");
  const compiledSetup = stepBlock(compiled, "Configure Ubuntu package mirrors");

  assert.match(source, /^steps:\n  - name: Configure Ubuntu package mirrors/m);
  for (const path of ["/etc/apt/apt-mirrors.txt", "/etc/apt/sources.list", "/etc/apt/sources.list.d/ubuntu.sources"]) {
    assert.ok(setup.includes(path));
  }
  assert.match(setup, /if \[ -f "\$source" \]; then/);
  assert.match(compiledSetup, /sudo sed -i/);
  assert.ok(compiled.indexOf("name: Configure Ubuntu package mirrors") < compiled.indexOf("name: Install Playwright browsers"));
  const expression = /sudo sed -i '([^']+)'/.exec(setup)?.[1];
  assert.ok(expression);
  for (const [input, expected] of [
    ["http://azure.archive.ubuntu.com/ubuntu/\tpriority:1\n", "https://archive.ubuntu.com/ubuntu/\tpriority:1\n"],
    ["deb http://azure.archive.ubuntu.com/ubuntu noble main\n", "deb https://archive.ubuntu.com/ubuntu noble main\n"],
    ["URIs: http://azure.archive.ubuntu.com/ubuntu/\nSuites: noble-updates\n", "URIs: https://archive.ubuntu.com/ubuntu/\nSuites: noble-updates\n"],
    ["https://security.ubuntu.com/ubuntu/\tpriority:3\n", "https://security.ubuntu.com/ubuntu/\tpriority:3\n"],
  ]) {
    assert.equal(execFileSync("sed", ["-e", expression], { input, encoding: "utf8" }), expected);
    assert.equal(execFileSync("sed", ["-e", expression], { input: expected, encoding: "utf8" }), expected);
  }
});

test("SelfCare dashboard performance worker selects one highest-ROI small win", () => {
  const source = workflow("self-care-dashboard-performance.md");
  const liveGuard = "if: ${{ inputs.target_repo == 'githubnext/gh-cao' && (inputs.safe_output_mode || 'review') == 'live' }}";
  assert.match(source, /^name: "SelfCare \/ Dashboard Performance"$/m);
  assert.match(source, /campaign: self-care\n\s+role: worker\n\s+worker: dashboard-performance/);
  assert.match(source, /engine:\n\s+id: pi\n\s+model: copilot\/gpt-5\.6-sol/);
  assert.match(source, /safe_output_mode` is `live`/);
  const noopGate = stepBlock(source, "Emit noop for unsupported dashboard performance dispatch");
  assert.match(
    noopGate,
    /if: \$\{\{ inputs\.target_repo != 'githubnext\/gh-cao' \|\| \(inputs\.safe_output_mode \|\| 'review'\) != 'live' \}\}[\s\S]*\{"type":"noop","message":"Dashboard performance runs only in live mode for githubnext\/gh-cao\."\}[\s\S]*>> "\$GH_AW_SAFE_OUTPUTS"/,
  );
  for (const name of [
    "Install dashboard dependencies",
    "Cache Chromium",
    "Install Chromium",
    "Collect baseline performance evidence",
  ]) {
    assert.ok(stepBlock(source, name).includes(liveGuard), `${name} must remain live-only`);
  }
  assert.match(source, /skip-if-match: 'is:pr is:open "gh-aw-workflow-id: self-care-dashboard-performance" in:body'/);
  assert.match(source, /cache-memory:\n\s+retention-days: 30\n\s+allowed-extensions: \["\.json"\]/);
  assert.match(source, /dashboard-performance-rotation\.json/);
  assert.match(source, /initialize .* without reporting missing data/);
  assert.match(source, /Rank candidates by highest evidence-backed impact per unit of effort/);
  assert.match(source, /Select exactly one highest-ranked actionable candidate/);
  assert.match(source, /at most three production files plus focused tests/);
  assert.match(source, /DASHBOARD_PERFORMANCE_OUTPUT_DIR="\$evidence_root\/before"/);
  assert.match(source, /upload-artifact:[\s\S]*?self-care-dashboard-performance-evidence\/\*\*/);
  assert.match(source, /labels: \[self-care, self-care:dashboard-performance\]/);
  assert.match(source, /title-prefix: "\[self-care:dashboard-performance\] "/);
  assert.match(source, /draft: true/);
  assert.match(source, /dashboard\/site\/index\.html/);
  assert.doesNotMatch(source, /allowed-files:[\s\S]*dashboard\/site\/test\/performance/);
  for (const persona of ["CFO", "CTO", "CSO"]) {
    assert.match(source, new RegExp(persona));
  }
  assert.doesNotMatch(source, /^evals:/m);
  assert.doesNotMatch(source, /^graders:/m);
});

test("SelfCare dashboard performance submits safe outputs through executable CLI examples", () => {
  const source = workflow("self-care-dashboard-performance.md");
  const delivery = source.split("### Safe-output delivery\n")[1]?.split("{{#runtime-import?")[0];
  assert.ok(delivery);
  assert.match(delivery, /Writing `Result: noop` in the final response does not submit a safe output/);
  assert.match(delivery, /safeoutputs create_pull_request \./);
  assert.match(delivery, /Submit exactly one terminal decision: `create_pull_request` or `noop`/);
  assert.match(delivery, /Confirm that the CLI accepted the submission/);
  assert.match(delivery, /an evidence upload alone is not a terminal decision/);
  assert.match(source, /Do not pass `name`; the tool schema does not accept it/);

  const examples = [...delivery.matchAll(/```bash\n([\s\S]*?)\n```/g)].map((match) => match[1]);
  assert.equal(examples.length, 2);
  const calls = examples.map((example) => {
    const output = execFileSync("bash", ["-c", `
safeoutputs() { printf '%s\\n' "$1" "$2"; }
${example}
`], { encoding: "utf8" }).trim().split("\n");
    assert.equal(output.length, 2);
    return { tool: output[0], payload: JSON.parse(output[1]) };
  });
  assert.deepEqual(calls, [
    {
      tool: "upload_artifact",
      payload: {
        path: "${{ github.workspace }}/self-care-dashboard-performance-evidence",
      },
    },
    {
      tool: "noop",
      payload: { message: "No measurable improvement was demonstrated; no pull request was created." },
    },
  ]);
});

test("SelfCare no longer dispatches the obsolete experimental views worker", () => {
  const orchestrator = workflow("self-care.md");

  assert.doesNotMatch(orchestrator, /self-care-experimental-views/);
  assert.doesNotMatch(orchestrator, /experimental-views/);
});

test("SelfCare Pages health worker audits every deployed view on three profiles", () => {
  const source = workflow("self-care-pages-health.md");
  assert.match(source, /^name: "SelfCare \/ Pages Health"$/m);
  assert.match(source, /^\s+workflow_dispatch:$/m);
  assert.doesNotMatch(source, /^\s+schedule:/m);
  assert.match(source, /campaign: self-care\n\s+role: worker\n\s+worker: pages-health/);
  assert.match(source, /safe_output_mode` is `live`/);
  assert.match(source, /https:\/\/githubnext\.github\.io\/gh-cao\/cao\//);
  assert.doesNotMatch(source, /close-older-issues: true/);
  assert.doesNotMatch(source, /close-older-key: self-care-pages-health/);
  assert.match(source, /create-pull-request:/);
  assert.match(source, /labels: \[self-care, self-care:pages-health\]/);
  assert.match(source, /"dashboard\/site\/test\/performance\/\*\*\/\*\.mjs"/);
  assert.match(source, /self-care-pages-health-evidence\/\*\*/);
  assert.match(source, /each of the `desktop`, `mobile`, and `low-bandwidth` profiles/);
  assert.match(source, /scrolls every deployed dashboard view/i);
  assert.match(source, /executive summary/i);
  assert.match(source, /containing exactly three numbered, evidence-backed, small JavaScript improvements/);
  assert.match(source, /Call `create_pull_request` exactly once/);
  assert.match(source, /Fix only the selected quick wins/);
  assert.doesNotMatch(source, /^evals:/m);
  assert.doesNotMatch(source, /^graders:/m);
});

test("SelfCare code improvement preserves its focused dashboard component mission", () => {
  const source = workflow("self-care-code-improvement.md");
  const liveGuard = "if: ${{ inputs.target_repo == 'githubnext/gh-cao' && (inputs.safe_output_mode || 'review') == 'live' }}";

  assert.match(source, /^name: "SelfCare \/ Code Quality"$/m);
  assert.match(source, /campaign: self-care/);
  assert.match(source, /worker: code-improvement/);
  assert.match(source, /safe_output_mode` is `live`/);
  assert.match(source, /allowed-files:\n\s+- "dashboard\/site\/src\/\*\.js"\n\s+- "dashboard\/site\/src\/\*\*\/\*\.js"\n\s+- "dashboard\/site\/test\/\*\*\/\*\.js"/);
  assert.match(source, /uses: actions\/cache@/);
  assert.match(source, /path: ~\/\.cache\/ms-playwright/);
  assert.match(source, /npm exec --prefix dashboard\/site -- playwright install --with-deps chromium/);
  assert.equal(source.split(liveGuard).length - 1, 4);
});

test("SelfCare view reuse worker generalizes one Dashboard Language view", () => {
  const source = workflow("self-care-dashboard-language-refactor.md");
  const liveGuard = "if: ${{ inputs.target_repo == 'githubnext/gh-cao' && (inputs.safe_output_mode || 'review') == 'live' }}";

  assert.match(source, /^name: "SelfCare \/ View Reuse"$/m);
  assert.match(source, /campaign: self-care\n\s+role: worker\n\s+worker: dashboard-language-refactor/);
  assert.match(source, /safe_output_mode` is `live`/);
  assert.match(source, /branches on a built-in page identity, route, view ID, or one-off element name/);
  assert.match(source, /dashboard\/site\/dashboard\.json/);
  assert.match(source, /docs\/dashboard-language-specification\.md/);
  assert.match(source, /dashboard\/site\/src\/specification\.js/);
  assert.match(source, /at least one additional existing or test-fixture composition/);
  assert.match(source, /Update both `dashboard\/aw\.yml` and root `aw\.yml` only when a new runtime file must be bundled/);
  assert.match(source, /npm --prefix dashboard\/site run validate:corpus/);
  assert.match(source, /uses: actions\/cache@/);
  assert.match(source, /path: ~\/\.cache\/ms-playwright/);
  assert.match(source, /npm exec --prefix dashboard\/site -- playwright install --with-deps chromium webkit/);
  assert.match(source, /labels: \[self-care, self-care:dashboard-language-refactor\]/);
  assert.match(source, /title-prefix: "\[self-care:dashboard-language-refactor\] "/);
  assert.equal(source.split(liveGuard).length - 1, 4);
});
