import assert from "node:assert/strict";
import test from "node:test";
import { workflow } from "./workflow-contract.helpers.mjs";

// SelfCare workers that audit documentation, diagrams, and SVG assets.

test("SVG visual audit covers every tracked SVG in both color schemes", () => {
  const source = workflow("svg-visual-audit.md");
  const compiled = workflow("svg-visual-audit.lock.yml");

  assert.match(source, /git ls-files '\*\.svg'/);
  assert.match(source, /local checkout and manifest as the complete audit scope/);
  assert.match(source, /set-color-scheme light/);
  assert.match(source, /set-color-scheme dark/);
  assert.match(source, /\bid: pi\b/);
  assert.match(source, /\bmodel: copilot\/gpt-5\.6-sol\b/);
  assert.match(source, /cli-proxy: true/);
  assert.match(source, /playwright:/);
  assert.match(source, /version: "0\.1\.\d+"/);
  assert.match(source, /playwright-cli -s=svg-audit set-color-scheme light/);
  assert.match(source, /playwright-cli -s=svg-audit set-color-scheme dark/);
  assert.match(source, /`run-code` with `page\.screenshot\(\{ path: \.\.\., fullPage: true \}\)`/);
  assert.match(source, /Do not use Playwright MCP tool names as CLI commands/);
  assert.match(source, /4\.5:1/);
  assert.match(source, /overlap between a `<text>` element and its own descendant `<tspan>`/);
  assert.match(source, /create-check-run:/);
  assert.match(source, /upload-artifact:/);
  assert.match(source, /safeoutputs upload_artifact/);
  assert.match(source, /safeoutputs create_check_run/);
  assert.doesNotMatch(source, /playwright-cli browser_run_code/);
  assert.match(source, /python3 -m http\.server 4321/);
  assert.match(source, /--bind 127\.0\.0\.1/);
  assert.match(source, /http:\/\/127\.0\.0\.1:4321\//);
  assert.match(source, /--retry-connrefused/);
  assert.doesNotMatch(source, /host\.docker\.internal/);
  assert.doesNotMatch(source, /^\s+- local$/m);
  assert.doesNotMatch(source, /^env:\n\s+NO_PROXY:/m);
  assert.doesNotMatch(compiled, /^  NO_PROXY:/m);
  assert.match(source, /Never claim success if any manifest entry was skipped/);
});

test("multi-device docs tester runs daily and covers browser and appearance compatibility", () => {
  const source = workflow("multi-device-docs-tester.md");
  const compiled = workflow("multi-device-docs-tester.lock.yml");

  assert.match(source, /schedule: daily/);
  assert.doesNotMatch(source, /pull_request:|workflow_dispatch:|inputs\.devices/);
  assert.match(compiled, /cron: "\d+ \d+ \* \* \*"  # Friendly format: daily \(scattered\)/);
  assert.doesNotMatch(compiled, /^  pull_request:/m);
  assert.match(source, /MAX_ATTEMPTS=3/);
  assert.match(source, /shell: bash/);
  assert.match(source, /STATUS=1/);
  assert.match(source, /if timeout 5m npm ci --ignore-scripts/);
  assert.match(source, /npm ci attempt \$ATTEMPT\/\$MAX_ATTEMPTS failed with exit code \$STATUS/);
  assert.match(source, /playwright@1\.63\.0-alpha-2026-08-05 install --with-deps webkit/);
  assert.equal(
    (source.match(/PLAYWRIGHT_BROWSERS_PATH: \$\{\{ runner\.temp \}\}\/gh-aw\/playwright-browsers/g) || []).length,
    2,
  );
  assert.match(compiled, /PLAYWRIGHT_BROWSERS_PATH: \$\{\{ runner\.temp \}\}\/gh-aw\/playwright-browsers/);
  assert.match(source, /^      cat > "\$EXPR_GITHUB_WORKSPACE\/\.playwright\/webkit\.config\.json" <<'EOF'\n      \{\}\n      EOF$/m);
  assert.match(source, /for BROWSER in chrome webkit/);
  assert.match(source, /colorScheme: "light"/);
  assert.match(source, /colorScheme: "dark"/);
  assert.match(source, /currentSrc/);
  assert.doesNotMatch(source, /create-check-run:|create_check_run|action_required/);
  assert.match(source, /create-issue:/);
  assert.match(source, /multi-device-docs\/screenshots/);
});

test("SelfCare data acquisition audit refreshes its specification", () => {
  const source = workflow("self-care-data-acquisition-audit.md");
  const compiled = workflow("self-care-data-acquisition-audit.lock.yml");

  assert.match(source, /on:\n\s+bots: \["github-actions\[bot\]", "cao-githubnext-gh-aw-cao-write\[bot\]"\]/);
  assert.match(source, /campaign: self-care\n\s+role: worker\n\s+worker: data-acquisition-audit/);
  assert.match(source, /safe_output_mode` is `live`/);
  assert.match(source, /draft: true/);
  assert.match(
    source,
    /allowed-files:\n\s+- "specs\/data-acquisition-audit\.md"\n\s+- "specs\/data-acquisition-audit-history\.md"/,
  );
  assert.match(source, /Inspect JavaScript and embedded JavaScript/);
  assert.match(compiled, /specs\/data-acquisition-audit\.md/);
  assert.match(compiled, /specs\/data-acquisition-audit-history\.md/);
});

test("SelfCare runs every 20 minutes", () => {
  const source = workflow("self-care.md");
  const compiled = workflow("self-care.lock.yml");

  assert.match(source, /schedule: every 20 minutes/);
  assert.match(source, /engine: copilot/);
  assert.doesNotMatch(source, /model: copilot\/gpt-5\.4/);
  assert.match(
    source,
    /self-care-dashboard-data-schema`, `self-care-data-acquisition-audit`, `self-care-docs-maintainer`, `self-care-glossary`, and `self-care-specs-maintainer`.*preceding 24 hours/
  );
  assert.match(source, /ten most recent runs of each workflow/);
  assert.match(source, /self-care-documentation-discoverability.*preceding seven days/);
  assert.match(source, /documentation discoverability worker was weekly-eligible and dispatched/);
  assert.match(source, /self-care-pages-health.*no run of that workflow is queued, in progress, or started during the preceding six hours/);
  assert.match(source, /at most the 20 most recent Pages Health workflow runs/);
  assert.match(compiled, /cron: "[0-5]?\d\/20 \* \* \* \*"  # Friendly format: every 20 minutes \(scattered\)/);
  assert.doesNotMatch(compiled, /GH_AW_INFO_MODEL: "copilot\/gpt-5\.4"/);
});

test("SelfCare confirms safe-output requests before claiming dispatches", () => {
  const source = workflow("self-care.md");

  assert.match(source, /safeoutputs <tool_name> '<JSON>'/);
  assert.match(source, /wait for the complete result/);
  assert.match(source, /Never pipe `safeoutputs` output to `head` or another truncating consumer/);
  assert.match(source, /Count a dispatch only after the tool confirms that its safe-output request was recorded/);
  assert.match(source, /this confirms dispatch intent, not downstream worker completion/);
  assert.match(source, /unconfirmed, record that worker as deferred and call the configured `report_incomplete` safe-output tool without retrying/);
  assert.match(source, /If no workers are eligible, call the configured `noop` safe-output tool/);
  assert.match(source, /A prose report alone is not a terminal safe output/);
});

test("SelfCare documentation discoverability audits the public agent entry point", () => {
  const source = workflow("self-care-documentation-discoverability.md");

  assert.match(source, /^name: "SelfCare \/ Documentation Discoverability"$/m);
  assert.match(source, /campaign: self-care\n\s+role: worker\n\s+worker: documentation-discoverability/);
  assert.match(source, /safe_output_mode` is `live`/);
  assert.match(source, /githubnext\.github\.io\/gh-cao\/llms\.txt/);
  assert.match(source, /llms-small\.txt/);
  assert.match(source, /agent\/resources\.json/);
  assert.match(source, /resource\.json/);
  assert.match(source, /catalog-resource\.json/);
  assert.match(source, /cao_catalog/);
  assert.match(source, /set -euo pipefail/);
  assert.match(source, /WebMCP bindings/);
  assert.match(source, /sha256sum/);
  assert.match(source, /Evaluate exactly these ten tasks/);
  assert.match(source, /more than two document hops/);
  assert.match(source, /exactly one targeted repository search/);
  assert.match(source, /at\s+most two matching source files/);
  assert.match(source, /routing-success count and percentage/);
  assert.match(source, /create-issue:/);
  assert.match(source, /update-issue:/);
  assert.match(source, /close-issue:/);
  assert.doesNotMatch(source, /create-pull-request:/);
  assert.doesNotMatch(source, /^\s+(contents|issues|pull-requests): write$/m);
});

test("SelfCare accessibility checker audits the served docs site with axe-core evidence", () => {
  const source = workflow("self-care-accessibility-checker.md");
  const compiled = workflow("self-care-accessibility-checker.lock.yml");
  const liveGuard = "if: ${{ inputs.target_repo == 'githubnext/gh-cao' && (inputs.safe_output_mode || 'review') == 'live' }}";

  assert.match(source, /^name: "SelfCare \/ Accessibility"$/m);
  assert.match(source, /workflow_dispatch:/);
  assert.match(source, /campaign: self-care/);
  assert.match(source, /worker: accessibility-checker/);
  assert.match(source, /safe_output_mode` is `live`/);
  assert.match(source, /read_pull_requests: read/);
  assert.match(source, /pull-requests: read/);
  assert.doesNotMatch(source, /^\s+(contents|issues|pull-requests): write$/m);
  assert.match(source, /engine:\n\s+id: pi\n\s+model: copilot\/gpt-5\.6-sol/);
  assert.match(source, /cli-proxy: true/);
  assert.match(source, /playwright:\n\s+version: "0\.1\.18"/);
  assert.match(source, /npm pack axe-core@4\.13\.0/);
  assert.match(source, /npm run docs:preview -- --host 127\.0\.0\.1 --port <port>/);
  assert.match(source, /Do not use a generic flat static server rooted at `dist\/` as the primary preview mechanism/);
  assert.match(source, /Astro preview performs the base-path routing/);
  assert.match(source, /WCAG 2\.2 Level AA/);
  assert.match(source, /playwright-cli` is a pre-installed CLI binary already on `PATH`/);
  assert.match(source, /never call `report_missing_tool` for it based on assumption alone/);
  assert.match(source, /node_modules\/\.bin\/playwright install chromium/);
  assert.match(source, /Node Playwright package launch preflight/);
  assert.match(source, /preflight-node\.log/);
  assert.match(source, /colorScheme: "light"/);
  assert.match(source, /colorScheme: "dark"/);
  assert.match(source, /prefers-reduced-motion/);
  assert.match(source, /safe-outputs:\n\s+allowed-domains:\n\s+- githubnext\.github\.io\n\s+create-pull-request:/);
  assert.match(source, /create-pull-request:\n\s+target-repo:.*\n\s+title-prefix: "\[self-care:accessibility-checker\] "/);
  assert.match(source, /labels: \[self-care, self-care:accessibility-checker\]/);
  assert.match(source, /draft: true/);
  assert.match(source, /if-no-changes: ignore/);
  assert.match(source, /fallback-as-issue: false/);
  assert.match(source, /protected-files: blocked/);
  assert.match(source, /max-patch-files: 4/);
  assert.match(source, /allowed-files:\n\s+- "docs\/styles\/\*\.css"\n\s+- "docs\/components\/\*\.astro"\n\s+- "docs\/\*\.md"/);
  assert.match(source, /exact `gh-aw-workflow-id: self-care-accessibility-checker` body marker/);
  assert.match(source, /Call `create_pull_request` exactly once only when the fix is supported/);
  assert.match(source, /Call `noop` exactly once instead if the audit is clean/);
  assert.equal(source.split(liveGuard).length - 1, 7);
  assert.doesNotMatch(source, /^\s+(create-issue|add-comment|create-discussion|push-to-pull-request-branch):/m);
  assert.match(compiled, /docs\/styles\/\*\.css/);
  assert.match(compiled, /docs\/components\/\*\.astro/);
  assert.match(compiled, /"tools":\["create_pull_request","noop","report_incomplete","report_missing_data","report_missing_tool"\]/);
});

test("docs diagram generator creates one validated theme-aware SVG pair", () => {
  const source = workflow("docs-explanatory-diagrams.md");

  assert.match(source, /schedule: weekly/);
  assert.match(source, /public\/assets\/\*-light\.svg/);
  assert.match(source, /public\/assets\/\*-dark\.svg/);
  assert.match(source, /data-visual-kind=\"diagram\"/);
  assert.match(source, /check-svg-visual-language\.mjs/);
  assert.match(source, /colorScheme: \"light\"/);
  assert.match(source, /colorScheme: \"dark\"/);
  assert.match(source, /create-pull-request:/);
  assert.match(source, /Call `noop`/);
});

test("SelfCare docs maintainer uses daily merged-change and ADR evidence", () => {
  const source = workflow("self-care-docs-maintainer.md");

  assert.match(source, /^name: "SelfCare \/ Docs Maintainer"$/m);
  assert.match(source, /campaign: self-care\n\s+role: worker\n\s+worker: docs-maintainer/);
  assert.match(source, /safe_output_mode` is `live`/);
  assert.match(source, /cache-memory:\n\s+retention-days: 30/);
  assert.match(source, /evidence-watermark\.json/);
  assert.match(source, /at most 30 pull requests merged into the default branch/);
  assert.match(source, /ordered by `\(merged_at, number\)` oldest first so overflow remains queued/);
  assert.match(source, /query inclusively from the pull request cursor timestamp/);
  assert.match(source, /discard only keys less than or equal to the saved key/);
  assert.match(source, /Ignore copyable markers from every other author/);
  assert.match(source, /skip-if-match: 'is:pr is:open "gh-aw-workflow-id: self-care-docs-maintainer" in:body'/);
  assert.match(source, /inspect its changed-file list and bounded diff/);
  assert.match(source, /never treat a pull request description as proof of behavior/);
  assert.match(source, /first 100 commits from `saved_sha\.\.current_head`/);
  assert.match(source, /This ancestry range, not author or committer timestamps/);
  assert.match(source, /saved SHA is no longer an ancestor/);
  assert.match(source, /ADR files under `adr\/` added or changed by direct pushes/);
  assert.match(source, /Advance each cursor only through fully inspected evidence/);
  assert.match(source, /safe-output failure will therefore be retried on the next run/);
  assert.match(source, /Clear it when a merged, provenance-verified `self-care-docs-maintainer` pull request cites every pending/);
  assert.match(source, /documentation was fixed independently or the correction is no longer necessary/);
  assert.match(source, /clear the pending evidence while retaining its advanced cursors/);
  assert.match(source, /allowed-files:\n\s+- "docs\/\*\.md"\n\s+- "docs\/\*\*\/\*\.md"/);
  assert.match(source, /draft: true/);
  assert.match(source, /npm run docs:build/);
  assert.match(source, /Call `create_pull_request` exactly once/);
  assert.match(source, /Call `noop` exactly once/);
  assert.match(source, /\{\{#runtime-import\? \.github\/cao\/self-care\.md\}\}/);
  assert.doesNotMatch(source, /^\s+(contents|actions|pull-requests): write$/m);
});

test("SelfCare specs maintainer fixes missing specification updates in W3C style", () => {
  const source = workflow("self-care-specs-maintainer.md");
  const compiled = workflow("self-care-specs-maintainer.lock.yml");

  assert.match(source, /^name: "SelfCare \/ Specs Maintainer"$/m);
  assert.match(source, /on:\n\s+bots: \["github-actions\[bot\]", "cao-githubnext-gh-aw-cao-write\[bot\]"\]/);
  assert.match(source, /imports:\n\s+- uses: shared\/control\.md/);
  assert.match(source, /campaign: self-care\n\s+role: worker\n\s+worker: specs-maintainer/);
  assert.match(source, /safe_output_mode` is `live`/);
  assert.match(source, /evidence-watermark\.json/);
  assert.match(source, /Inspect at most 30 merged pull requests/);
  assert.match(source, /discard only keys less than or equal to the saved key/);
  assert.match(source, /Ignore copyable markers from every other author/);
  assert.match(source, /never treat a pull request description as proof of behavior/);
  assert.match(source, /does not modify the governing file under `specs\/`/);
  assert.match(source, /RFC 2119/);
  assert.match(source, /Follow W3C conventions for every specification change/);
  assert.match(source, /allowed-files:\n\s+- "specs\/\*\.md"\n/);
  assert.match(source, /draft: true/);
  assert.match(source, /max-patch-files: 3/);
  assert.match(source, /npm run docs:build/);
  assert.match(source, /Call `create_pull_request` exactly once/);
  assert.match(source, /Call `noop` exactly once/);
  assert.match(source, /\{\{#runtime-import\? \.github\/cao\/self-care\.md\}\}/);
  assert.doesNotMatch(source, /^\s+(contents|actions|pull-requests): write$/m);
  assert.doesNotMatch(compiled, /GH_AW_AGENT_FILE:/);
});

test("SelfCare docs build-time investigator rotates evidenced recommendations", () => {
  const source = workflow("self-care-docs-build-time-investigator.md");
  const compiled = workflow("self-care-docs-build-time-investigator.lock.yml");

  assert.match(source, /^name: "SelfCare \/ Docs Build Time"$/m);
  assert.match(source, /on:\n\s+bots: \["github-actions\[bot\]", "cao-githubnext-gh-aw-cao-write\[bot\]"\]/);
  assert.match(source, /campaign: self-care\n\s+role: worker\n\s+worker: docs-build-time-investigator/);
  assert.match(source, /safe_output_mode` is `live`/);
  assert.match(source, /at most the latest 20 completed `docs\.yml` runs from the last 14 days/);
  assert.match(source, /median and p90 durations/);
  assert.match(source, /repo-memory:\n\s+branch-name: memory\/self-care-docs-build-time/);
  assert.match(source, /githubnext__gh-cao__docs-build-time-suggestions\.json/);
  assert.match(source, /Advance `next_category` after every complete evaluation/);
  assert.match(source, /noop cap is two only to tolerate one accidental placeholder call/);
  assert.match(source, /Call `create_issue` exactly once/);
  assert.match(source, /Otherwise call `noop` exactly once/);
  assert.match(source, /title-prefix: "\[self-care:docs-build-time-investigator\] "/);
  assert.match(source, /noop:\n\s+max: 2\n\s+report-as-issue: false/);
  assert.match(compiled, /\\"noop\\":\{\\"max\\":2,\\"report-as-issue\\":\\"false\\"\}/);
  assert.doesNotMatch(source, /^\s+(create-pull-request|add-comment|create-discussion|push-to-pull-request-branch):/m);
});

test("SelfCare glossary worker maintains Astro documentation from daily change evidence", () => {
  const source = workflow("self-care-glossary.md");

  assert.match(source, /^name: "SelfCare \/ Glossary"$/m);
  assert.match(source, /campaign: self-care\n\s+role: worker\n\s+worker: glossary/);
  assert.match(source, /safe_output_mode` is `live`/);
  assert.match(source, /most recent completed successful run of `self-care-glossary`/);
  assert.match(source, /preceding 24 hours/);
  assert.match(source, /at most 50 pull requests merged into the default branch/);
  assert.match(source, /at most 100 default-branch commits/);
  assert.match(source, /normative repository definition plus one implementation use/);
  assert.match(source, /Preserve the existing Astro-compatible YAML frontmatter exactly/);
  assert.match(source, /allowed-files:\n\s+- "docs\/glossary\.md"/);
  assert.match(source, /labels: \[self-care, self-care:glossary\]/);
  assert.match(source, /title-prefix: "\[self-care:glossary\] "/);
  assert.match(source, /draft: true/);
  assert.match(source, /npm run docs:build/);
  assert.match(source, /Call `create_pull_request` exactly once/);
  assert.match(source, /Call `noop` exactly once/);
  assert.doesNotMatch(source, /^evals:/m);
  assert.doesNotMatch(source, /^\s+(contents|actions|pull-requests): write$/m);
});
