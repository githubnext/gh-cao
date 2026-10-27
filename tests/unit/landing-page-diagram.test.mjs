import assert from "node:assert/strict";
import { existsSync, readFileSync } from "node:fs";
import test from "node:test";
import { parse } from "yaml";
import { issueOpenedPaths } from "../../docs/components/workflow-illustration-icons.mjs";

const landingPage = readFileSync("docs/README.md", "utf8");
const hero = readFileSync("docs/components/HierarchyHero.astro", "utf8");
const heroIntro = readFileSync("docs/components/HeroIntro.astro", "utf8");
const illustration = readFileSync("docs/components/DispatchIllustration.astro", "utf8");
const ecosystem = readFileSync("docs/components/WorkflowEcosystem.astro", "utf8");
const ecosystemModel = readFileSync("docs/lib/workflow-ecosystem.mjs", "utf8");
const workflowRun = readFileSync("docs/components/WorkflowRun.astro", "utf8");
const illustrationIcons = readFileSync("docs/components/workflow-illustration-icons.mjs", "utf8");
const campaignPage = readFileSync("docs/pages/catalog/index.astro", "utf8");
const terminal = readFileSync("docs/components/TerminalDemo.astro", "utf8");
let terminalText = terminal;
let previousTerminalText;
do {
  previousTerminalText = terminalText;
  terminalText = terminalText.replaceAll(/<[^>]+>/g, "");
} while (terminalText !== previousTerminalText);
terminalText = terminalText.replaceAll(/[<>]/g, "");
const headerLinks = readFileSync("docs/components/HeaderLinks.astro", "utf8");
const catalog = readFileSync("docs/lib/catalog.ts", "utf8");
const astroConfig = readFileSync("astro.config.mjs", "utf8");

test("landing page presents the product through real operational surfaces", () => {
  assert.match(landingPage, /text: Explore CAO[\s\S]*?link: \/gh-cao\/architecture-at-a-glance\//);
  assert.match(landingPage, /text: Get started[\s\S]*?link: \/gh-cao\/setup-quickstarts\//);
  assert.match(landingPage, /text: Sample Dashboard[\s\S]*?link: https:\/\/githubnext\.github\.io\/gh-cao\/cao/);
  assert.doesNotMatch(headerLinks, /label: "Dashboard"/);
  assert.match(landingPage, /title: Central Agentic Ops \(CAO\)/);
  assert.match(landingPage, /Monitor and govern GitHub Agentic Workflows at scale\./);
  assert.match(landingPage, /Keep track of all runs, costs, and outcomes of your GitHub Agentic Workflows across repositories\. Set execution scope and rollout policy, refine workflows in review mode, and roll out with confidence\./);
  assert.match(hero, /cao-dashboard-mobile-overview\.png/);
  assert.match(hero, /cao-dashboard-mobile-overview-light\.png/);
  assert.doesNotMatch(hero, /cao-dashboard-(?:run-history|campaign-value)|runs-story/);
  assert.match(hero, /ThemeImage/);
  assert.doesNotMatch(hero, /hero-callout|phone-float/);
  assert.match(hero, /Setup, authentication, and rollout/);
  assert.match(hero, /\.terminal-story \{[^}]*grid-template-columns: repeat\(2, minmax\(0, 1fr\)\)/);
  assert.match(hero, /Roll out with confidence\./);
  assert.match(hero, /Start with monitoring\./);
  assert.match(hero, /derive the simplest structure/);
  assert.match(hero, /outcome, target scope, and execution requirements/);
  assert.match(hero, /Dependabot campaign, pairing a central orchestrator with one-target workers/);
  assert.match(hero, /repository scope, read\/write credentials and review\/live policy explicit/);
  assert.match(hero, /href="\/gh-cao\/operation-patterns\/"/);
  assert.match(terminalText, /gh extension install githubnext\/gh-cao/);
  assert.equal([...terminal.matchAll(/<code>/g)].length, 3);
  assert.match(terminalText, /gh cao setup/);
  assert.match(terminalText, /Add the Dependabot campaign to CAO/);
  assert.match(terminalText, /Installed extension githubnext\/gh-cao/);
  assert.match(terminalText, /Next: gh cao doctor/);
  assert.match(terminalText, /then review, commit and push the installed files/);
  assert.match(terminalText, /Monitoring configured locally\. No commit or push was performed/);
  assert.match(terminalText, /Example agent request — not a CLI command/);
  assert.match(terminal, /data-terminal-request="integration"/);
  assert.match(terminalText, /Keep policy disabled, review-only, and scoped to one repo/);
  assert.match(terminalText, /Install githubnext\/gh-cao\/dependabot/);
  assert.match(terminalText, /Use its central orchestrator and one-target workers/);
  assert.match(terminalText, /Review the campaign files and policy\. Do not enable or run/);
  assert.doesNotMatch(terminal, /data-terminal-output="rollout"|\bjq\b/);
  assert.doesNotMatch(terminalText, /Daily Repo Status|as standalone/);
  assert.doesNotMatch(terminal, /<a\b|href=/);
  assert.doesNotMatch(terminal, /result-mark|Ready to review and publish/);
  assert.doesNotMatch(terminalText, /(?<![\w-])cao\.sh|gh cao validate|gh aw run|--raw-field|integrate-cao-workflow/);
  assert.deepEqual(
    [...hero.matchAll(/<section class="product-story ([^"]+)"/g)].map(([, classes]) => classes),
    ["dispatch-story", "maintenance-story", "terminal-story", "decision-story", "snippet-story webhook-story", "snippet-story agentic-workflows-story"],
  );
  assert.match(hero, /aria-labelledby="maintenance-title" data-reveal data-slideshow-slide/);
  assert.match(hero, /EASY UPDATES, ALL IN ONE PLACE\./);
  assert.match(hero, /Keep workflows across all enrolled repositories up to date and secure\./);
  assert.match(hero, /data-slideshow-snippet="Dashboard maintenance"/);
  for (const label of ["gh-aw upgrades", "Workflow source updates"]) assert.ok(hero.includes(label));
  const maintenance = hero.match(/<section class="product-story maintenance-story"[\s\S]*?<\/section>/)?.[0];
  assert.ok(maintenance);
  assert.match(maintenance, /gh aw upgrade/);
  assert.match(maintenance, /gh aw update/);
  assert.match(maintenance, /Version pins keep installs reproducible/);
  assert.match(maintenance, /3-way merges preserve local customizations/);
  assert.doesNotMatch(maintenance, /campaign|gh cao update/i);
  assert.match(maintenance, /https:\/\/github\.com\/user-attachments\/assets\//);
  assert.match(maintenance, /individual Upgrade buttons and Upgrade all/);
  assert.match(maintenance, /Unknown version evidence stays explicit/);
  assert.doesNotMatch(hero, /https:\/\/github\.github\.com\/gh-aw\/practices\//);
  assert.doesNotMatch(hero, /One operating picture|phone-caption/);
  assert.doesNotMatch(
    hero,
    /wizard-launch|Launch setup wizard|OpsWizard|Plan a control plane and first campaign in four steps|Stand up a control plane in three steps/,
  );
  assert.doesNotMatch(hero, /trust-section|Coordination without concentrated risk|section-actions/);
  assert.doesNotMatch(`${landingPage}\n${hero}`, /\bfactor(?:y|ies)\b/i);
});

test("landing page keeps the hero and supporting copy concise", () => {
  const wordCount = (text) => text.replaceAll(/<[^>]+>/g, " ").trim().split(/\s+/).length;
  const title = landingPage.match(/^  title: (.+)$/m)?.[1];
  const tagline = landingPage.match(/^  tagline: (.+)$/m)?.[1];
  assert.ok(title && wordCount(title) <= 8, "hero headline should stay within eight words");
  assert.ok(tagline && wordCount(tagline) <= 40, "hero tagline should stay within forty words");
  const paragraphs = [...hero.matchAll(/<p>\s*([\s\S]*?)<\/p>/g)];
  assert.equal(paragraphs.length, 7);
  const terminalParagraph = hero.match(/id="terminal-title"[\s\S]*?<p>\s*([\s\S]*?)<\/p>/)?.[1];
  for (const [, paragraph] of paragraphs) {
    const budget = paragraph === terminalParagraph ? 60 : 30;
    assert.ok(wordCount(paragraph) <= budget, `supporting paragraph should stay within ${budget} words`);
  }
  assert.doesNotMatch(hero, /<ul class="story-points">/);
});

test("README routes detailed tasks without rebuilding their chapter structure", () => {
  const readme = readFileSync("README.md", "utf8");
  assert.ok([...readme.matchAll(/^## /gm)].length <= 4);
  assert.doesNotMatch(readme, /^### /m);
  for (const anchor of [
    "project-status-and-scope", "github-cli-extension", "agent-plugin-and-requirements",
    "github-pages-setup", "agent-analysis", "contributing", "maintainers", "support",
  ]) {
    assert.ok(readme.includes(`id="${anchor}"`), `retain #${anchor} without a duplicate chapter`);
  }
  for (const guide of ["cao-cli", "agent-plugin", "agent-analysis", "deployment-actions"]) {
    assert.ok(readme.includes(`docs/${guide}.md`), `${guide} must remain discoverable`);
  }
});

test("landing keeps real snapshots without a duplicate fictional evidence board", () => {
  assert.doesNotMatch(hero, /HeroEvidenceStory|evidence-storyboard/);
  assert.match(hero, /Example Overview snapshot/);
  assert.match(hero, /Runtime signals and grader observations are not verified improvements/);
  assert.match(hero, /Example snapshot\. Dashboard maintenance/);
  assert.match(hero, /id="evidence-title"/);
  assert.match(hero, /id="decision-title"/);
  assert.match(hero, /review\/live policy explicit/);
  assert.match(hero, /Reviewed policy controls scope, enablement, and review\/live mode/);
});

test("the original operational-value storyboard retains evidence, human judgment, and reviewed decisions", () => {
  const story = hero.match(/<section class="product-story decision-story"[\s\S]*?<\/section>/)?.[0];
  const decision = readFileSync("docs/components/HeroDecisionLoop.astro", "utf8");
  assert.ok(story);
  assert.match(story, /<HeroDecisionLoop \/>/);
  for (const stage of ["Run", "Observe", "Review", "Refine", "Expand", "Pause"]) {
    assert.ok(decision.includes(`<strong>${stage}</strong>`));
  }
  assert.match(decision, /Human judgment\.<br \/>Reviewed policy\./);
  assert.match(decision, /Accepted outcomes\? Reliable execution\?/);
  assert.match(decision, /Cost justified by the evidence\?/);
  assert.match(decision, /Next run follows the reviewed decision/);
  assert.match(decision, /Decision framework, not automatic optimization/);
  assert.match(decision, /Successful runs alone do not demonstrate value/);
  assert.match(story, /href="\/gh-cao\/operational-value-how-to\/"/);
  assert.doesNotMatch(decision, /<img|<svg|<script/);
});

test("landing campaign example uses the real Dependabot manifest without granting rollout authority", () => {
  const excerpt = hero.match(/filename="dependabot\/aw\.yml"\s+code=\{`([\s\S]*?)`\}/)?.[1];
  assert.ok(excerpt, "campaign snippet should show its manifest");
  const manifest = parse(readFileSync("dependabot/aw.yml", "utf8"));
  assert.deepEqual(parse(excerpt), { name: manifest.name, includes: manifest.includes });
  assert.match(hero, /Keep it disabled and review-only/);
  assert.match(hero, /Each worker handles one target/);
  assert.match(hero, /Reviewed policy controls scope, enablement, and review\/live mode/);
});

test("gateway hero diagram shows cross-repo dispatch without an admission section", () => {
  const diagrams = readFileSync("docs/components/HeroControlDiagram.astro", "utf8");
  assert.match(hero, /<HeroControlDiagram \/>/);
  assert.doesNotMatch(hero, /Admission Gate|admission-story|admission-gate/);
  assert.doesNotMatch(diagrams, /Admission|admission|blocked-symbol|branched/);
  assert.match(hero, /Dispatch events across repositories/);
  assert.match(diagrams, /acme\/service-a/);
  assert.match(diagrams, /acme\/service-b/);
  assert.match(diagrams, /acme\/control/);
  assert.match(diagrams, /Runs in the destination repository, not the event source/);
  assert.doesNotMatch(diagrams, /<script|data-slideshow-snippet/);
});

test("terminal prefers GitHub CLI installation and matches the production CLI", () => {
  const setup = readFileSync("internal/cli/native_setup.go", "utf8");
  const installation = "gh extension install githubnext/gh-cao";
  assert.ok(terminalText.includes(installation));
  assert.ok(readFileSync("README.md", "utf8").includes(installation));
  assert.doesNotMatch(terminal, /install-gh-cao\.sh|\bgh cao init\b/);
  const readme = readFileSync("README.md", "utf8");
  assert.ok(readme.indexOf(installation) < readme.indexOf("curl -fsSL"));
  assert.match(readme, /> \*\*SAML-token installation failure:\*\*/);
  assert.match(terminal, /existing control repository checkout/);
  assert.match(terminal, /separately offer reviewed publication; declining leaves files local/);
  const setupExcerpt = terminal.match(/data-terminal-output="setup">([^<]+)<\/pre>/)?.[1];
  assert.ok(setupExcerpt, "terminal demo must include setup completion output");
  assert.ok(setup.includes(`${setupExcerpt.replaceAll("\n", "\\n")}\\n`),
    "setup excerpt must match the production CLI completion output");
  const integration = readFileSync("docs/configuration.md", "utf8");
  for (const requirement of [
    "Daily Repo Status from Agentics", "role: standalone", "enabled: false", "max-mode: review", "max-repositories: 1",
  ]) assert.ok(integration.includes(requirement));
  assert.match(integration, /does not automatically add CAO\nadmission or redirect its writes/);
  assert.match(integration, /including closing older reports/);
  assert.match(integration, /additional explicitly enrolled targets through reviewed policy changes/);
  assert.match(hero, /From outcome to execution\./);
  assert.match(hero, /Describe the outcome, target scope, and execution requirements/);
  assert.doesNotMatch(hero, /Choose direct operations|Choose an operation pattern/);
});

test("landing page shows shared and local workflows with explicit ownership", () => {
  assert.match(hero, /One workflow ecosystem/);
  assert.match(hero, /Shared workflows\. Clear ownership\./);
  assert.match(hero, /\.product-story \{[^}]*width: min\(calc\(100% - 2rem\), 76rem\)/);
  assert.match(hero, /\.dispatch-story \{[^}]*grid-template-columns: minmax\(0, 1fr\);[^}]*gap: 0\.75rem/);
  assert.match(hero, /\.dispatch-story > \.section-eyebrow \{[\s\S]*?justify-self: center/);
  assert.match(hero, /\.dispatch-story > p:not\(\.section-eyebrow\) \{[^}]*max-width: 80ch/);
  assert.match(hero, /\.dispatch-illustration \{[^}]*width: min\(100%, 72rem\);[^}]*margin: 1rem auto 0/);
  assert.match(hero, /@media \(width > 72rem\) \{[\s\S]*?\.dispatch-illustration \{[^}]*width: min\(80%, 56rem\)/);
  assert.match(hero, /\.dispatch-illustration :global\(\.workflow-ecosystem\) \{[^}]*width: auto;[^}]*zoom: 0\.8/);
  assert.match(hero, /@media \(min-width: 64rem\) \{[\s\S]*?\.dispatch-story h2 \{[\s\S]*?white-space: nowrap/);
  assert.match(hero, /Enrolled repositories benefit from enterprise-, organization-, and repository-owned workflows working side by side/);
  assert.match(hero, /<WorkflowEcosystem \/>/);
  assert.doesNotMatch(hero, /DispatchIllustration/);
  assert.match(hero, /aria-describedby="dispatch-description"/);
  assert.match(hero, /send declared safe outputs\s+directly to enrolled repositories/);
  assert.match(hero, /Labels and layout identify ownership; green marks delivery and completed outputs/);
  assert.match(hero, /Reviewed policy governs shared work, not the visual hierarchy/);
  for (const label of [
    "Optimization", "Issue triage", "Release readiness", "Daily team status", "Test coverage",
    "Org 1", "Org 2", "Org N", "Repo 1", "Repo 2", "Repo 3", "Repo N", "More organizations", "More repositories",
    "Organization standards", "Release checks",
  ]) {
    assert.ok((ecosystem + ecosystemModel).includes(label), `ecosystem should show ${label}`);
  }
  assert.match(ecosystem, /class="safe-output-icons workflow-outputs"/);
  assert.doesNotMatch(ecosystem, /ecosystem-legend/);
  assert.doesNotMatch(ecosystem + ecosystemModel, /node-owner|Enterprise automation team|Platform team|Engineering team|Organization team|Maintainers/);
  assert.doesNotMatch(ecosystem, /Dependabot|dependabot/);
  assert.doesNotMatch(ecosystem + ecosystemModel + workflowRun, /Supply chain|supply-chain/);
  assert.match(ecosystem, /index === 1 &&/);
  assert.doesNotMatch(ecosystem, /runtime-label|ecosystem-scale|Runs in |Shared work arriving|Local workflow running/);
  const outputMarkup = ecosystem.match(/<div class="safe-output-icons workflow-outputs"[^>]*>([\s\S]*?)<\/div>/)?.[1];
  assert.ok(outputMarkup);
  assert.match(outputMarkup, /class={`safe-output-icon \$\{output\.owner\}`}/);
  assert.match(outputMarkup, /<small title=\{output\.source \?\? organization\.workflow\}>\{output\.caption \?\? organization\.caption\}<\/small>/);
  assert.doesNotMatch(outputMarkup, /WorkflowRun|workflow-chip|"Pull request"|"Issue"/);
  assert.doesNotMatch(outputMarkup, /management-label/);
  assert.match(ecosystem, /<div class="managed-workflows" role="group" aria-label="Centrally managed workflows">/);
  assert.match(ecosystem, /<p class="management-label">Centrally managed<\/p>/);
  assert.match(ecosystem, /<div class="local-workflows"[^>]*>\s*<p class="management-label">Repo-managed &middot; monitored<\/p>/);
  assert.match(ecosystem, /Enterprise open issue safe output/);
  assert.match(ecosystem, /Organization open issue safe output/);
  assert.match(outputMarkup, /output\.paths\.map/);
  assert.match(outputMarkup, /owner: "repository", source: repository\.local, caption: repository\.caption, phase: "local"/);
  assert.match(outputMarkup, /safeOutputPaths\[repository\.output\]/);
  assert.deepEqual(issueOpenedPaths, [
    "M8 9.5a1.5 1.5 0 1 0 0-3 1.5 1.5 0 0 0 0 3Z",
    "M8 0a8 8 0 1 1 0 16A8 8 0 0 1 8 0ZM1.5 8a6.5 6.5 0 1 0 13 0 6.5 6.5 0 0 0-13 0Z",
  ]);
  assert.match(ecosystem, /class="local-workflows" aria-label="Repository-owned workflow"/);
  assert.match(ecosystem, /<div class="local-workflows"[\s\S]*?<div class="safe-output-icons workflow-outputs"/);
  assert.match(ecosystem, /connectorLayers\.map/);
  assert.match(ecosystem, /class={`\$\{layer\.owner\}-route`}/);
  assert.match(ecosystem, /<animateMotion dur="14s"/);
  assert.match(ecosystem, /class="continuation-point organization-continuation"/);
  assert.match(ecosystem, /class="continuation-point repository-continuation"/);
  assert.match(ecosystem, /@media \(prefers-reduced-motion: reduce\)/);
  assert.match(ecosystem, /\.safe-output-icon \{ color: var\(--success\); opacity: 1; \}/);
  assert.match(ecosystem, /\.safe-output\.local::before/);
  assert.match(ecosystem, /0%, 49% \{ color: var\(--node-muted\); opacity: 0\.35; \} 50%, 100% \{ color: var\(--success\); opacity: 1; \}/);
  assert.match(ecosystem, /\.organization-route \{ stroke: var\(--node-muted\); \}/);
  assert.doesNotMatch(ecosystem, /--enterprise|--organization|--repository/);
  assert.match(ecosystem, /\.route-pulse \{ fill: var\(--success\);/);
  assert.match(ecosystem, /<button class="animation-toggle" type="button" hidden>Pause animation<\/button>/);
  assert.match(ecosystem, /routes\.pauseAnimations\(\)/);
  assert.match(ecosystem, /routes\.unpauseAnimations\(\)/);
  assert.match(ecosystem, /animation-play-state: paused !important/);
  assert.doesNotMatch(workflowRun, /class="branch"/);
  assert.match(ecosystem, /<WorkflowRun name=\{repository\.local\} phase="local"/);
  assert.match(ecosystem, /from "\.\/workflow-illustration-icons\.mjs"/);
  assert.match(illustration, /from "\.\/workflow-illustration-icons\.mjs"/);
  assert.match(workflowRun, /from "\.\/workflow-illustration-icons\.mjs"/);
  assert.match(workflowRun, /stroke-dasharray: 22 18/);
  assert.match(workflowRun, /animation: spin 1\.05s linear infinite/);
  assert.match(workflowRun, /class="status queued"/);
  assert.match(workflowRun, /class="status running action-progress"/);
  assert.match(workflowRun, /class="status complete"/);
});

test("campaign documentation preserves the animated bounded dispatch illustration", () => {
  assert.match(campaignPage, /import DispatchIllustration/);
  assert.match(campaignPage, /How campaigns run/);
  assert.match(campaignPage, /<DispatchIllustration \/>/);
  assert.match(campaignPage, /container-type: inline-size/);
  assert.match(campaignPage, /Their workflow definitions stay in the control repository, not the targets/);
  assert.doesNotMatch(illustration, /cao-dashboard-campaign/);
  assert.match(illustration, /Central Agentic Ops \(CAO\)/);
  assert.match(illustration, /<small>Control plane<\/small>/);
  assert.match(illustration, /class="campaign-cue"/);
  assert.match(illustration, /Configured Ops/);
  assert.match(illustration, /<strong>Dependabot<\/strong>/);
  assert.match(illustration, /<strong>Repo Assist<\/strong>/);
  assert.match(illustration, /const goalPaths = \[/);
  assert.match(illustration, /M13\.637 2\.363h-\.001l1\.676\.335/);
  assert.match(illustration, /\{goalPaths\.map\(\(path\) => <path d=\{path\}><\/path>\)\}/);
  assert.match(illustration, /GitHub Actions/);
  assert.ok(
    illustration.indexOf("<h3>GitHub Actions</h3>") < illustration.indexOf("<h3>Organizations</h3>") &&
      illustration.indexOf("<h3>Organizations</h3>") <
        illustration.indexOf("<h3>Target Repositories</h3>"),
  );
  assert.match(illustration, /const organizationNodes = \[/);
  assert.match(illustration, /const successfulJobs = jobs\.filter\(\(job\) => !job\.failed\)/);
  assert.match(illustration, /const controlRepository = successfulJobs\.find/);
  assert.match(illustration, /const targetJobs = \[\.\.\.successfulJobs\]\.sort\(\(left, right\) => left\.targetRow - right\.targetRow\)/);
  assert.doesNotMatch(illustration, /organizationJobs|node\.campaigns|node\.repositories/);
  assert.match(illustration, /class="organization-node" style={`top: \$\{node\.top\}px`}/);
  assert.match(illustration, /<svg class="graph-routes" viewBox="0 0 82 488"/);
  assert.match(illustration, /<animateMotion/);
  assert.match(illustration, /keyPoints="0;0;0;1;1;1"/);
  assert.match(illustration, /class="node-continuation"/);
  assert.match(illustration, /class="node-continuation node-continuation-no-icon">\s*<b>•••<\/b>\s*<small>more organizations<\/small>/);
  assert.match(illustration, /<small>more organizations<\/small>/);
  assert.match(illustration, /<small>more repositories<\/small>/);
  assert.match(illustration, /class="node-continuation">[\s\S]*?<svg class="repo-icon repository-icon" viewBox="0 0 16 16">/);
  assert.match(illustration, /path=\{job\.organizationPath\}/);
  assert.match(illustration, /path=\{job\.repositoryPath\}/);
  assert.match(illustration, /\{ organization: "githubnext", top: 146 \}/);
  assert.match(illustration, /\{ organization: "github", top: 318 \}/);
  assert.match(illustration, /organizationPath: "M0 100 H41 V344 H82"/);
  assert.match(illustration, /organizationPath: "M0 308 H41 V344 H82"/);
  assert.match(illustration, /organizationPath: "M0 172 H82"/);
  assert.match(illustration, /repositoryPath: "M0 172 H41 V100 H82"/);
  assert.match(illustration, /repositoryPath: "M0 172 H82"/);
  assert.match(illustration, /repositoryPath: "M0 344 H41 V308 H82"/);
  assert.match(illustration, /repositoryPath: "M0 344 H41 V380 H82"/);
  assert.match(illustration, /class="graph-junction" cx="41" cy="172"/);
  assert.match(illustration, /class="graph-junction" cx="41" cy="344"/);
  assert.doesNotMatch(illustration, /CAO review queue/);
  assert.match(illustration, /graph-route-dot review/);
  assert.doesNotMatch(illustration, /class="control-repository-card"/);
  assert.match(illustration, /<small>CAO control plane<\/small>/);
  assert.doesNotMatch(illustration, /review output · CAO control repository|<small>live rollout/);
  assert.match(illustration, /targetRow: 2,[\s\S]*?repository: "githubnext\/gh-cao"|repository: "githubnext\/gh-cao"[\s\S]*?targetRow: 2/);
  assert.match(illustration, /\{jobs\.map\(\(job\) => \(/);
  assert.match(illustration, /\{targetJobs\.map\(\(job\) => \(/);
  assert.match(illustration, /class={`repository-card \$\{job\.timing\} \$\{job\.mode\}`}/);
  assert.match(illustration, /style={`grid-row: \$\{job\.targetRow\}`}/);
  assert.match(illustration, /--review: #0969da/);
  assert.match(illustration, /--review: #58a6ff/);
  assert.match(illustration, /class={`mobile-pair-route \$\{job\.timing\} \$\{job\.mode\}`}/);
  assert.match(illustration, /class={`mobile-repository-card \$\{job\.mode\}`}/);
  assert.match(illustration, /\{!job\.failed && \(\s*<>\s*<span class={`mobile-pair-route/);
  assert.match(illustration, /<i><\/i>/);
  assert.doesNotMatch(illustration, /No output dispatched|mobile-no-output/);
  assert.match(illustration, /\.organization-icon,[\s\S]*?\.repository-icon \{[\s\S]*?width: 16px;[\s\S]*?height: 16px/);
  assert.match(illustration, /class="organization-icon"/);
  assert.match(illustration, /\.organization-icon \{[\s\S]*?fill: var\(--muted\)/);
  assert.match(illustration, /class="repo-icon repository-icon"/);
  assert.match(illustration, /\.repository-card \{[\s\S]*?border-top: 1px solid var\(--line\)/);
  assert.match(illustration, /\.repository-card\.review \{[\s\S]*?border-color: color-mix[\s\S]*?background: color-mix/);
  assert.match(illustration, /\.repository-card\.review \.repository-icon \{[\s\S]*?fill: var\(--review\)/);
  assert.match(illustration, /class="mobile-scale-cue"/);
  assert.match(illustration, /More organizations and repositories/);
  assert.match(illustration, /The bounded dispatch pattern continues\./);
  assert.match(
    illustration,
    /grid-template-columns: minmax\(520px, 740px\) 72px 150px 72px 220px/,
  );
  assert.match(illustration, /width: min\(100%, 1270px\)/);
  assert.match(illustration, /text-align: left/);
  assert.match(illustration, /class="actions-column cao-surface"/);
  assert.match(illustration, /class="organizations-column"/);
  assert.match(illustration, /class="repositories-column"/);
  assert.doesNotMatch(illustration, /class="(?:organizations|repositories)-column cao-surface"/);
  assert.doesNotMatch(illustration, /dashboard-column|dashboard-route|route-dashboard/);
  assert.match(illustration, /\.cao-surface \{[\s\S]*?border: 1px solid var\(--line\)/);
  assert.match(illustration, /\.control-plane \{\s*overflow: visible;/);
  assert.match(illustration, /const campaigns = \[/);
  assert.match(illustration, /organizations: \["githubnext", "github"\]/);
  assert.match(illustration, /jobs: \[jobs\[1\], jobs\[0\]\]/);
  assert.match(illustration, /jobs: \[jobs\[3\], jobs\[4\], jobs\[2\]\]/);
  assert.match(illustration, /class="mobile-flow"/);
  assert.match(illustration, /class="mobile-campaign"/);
  assert.match(illustration, /class="mobile-workers"/);
  assert.match(illustration, /Parallel workers/);
  assert.match(illustration, /class="mobile-dispatch-pair"/);
  assert.match(illustration, /@keyframes mobile-route-job-1/);
  assert.match(illustration, /@keyframes mobile-route-job-5/);
  assert.match(
    illustration,
    /@container \(max-width: 1180px\) \{[\s\S]*?\.flow-layout \{[\s\S]*?display: none[\s\S]*?\.mobile-flow \{[\s\S]*?display: block/,
  );
  assert.match(illustration, /operation: "Dependabot update planner"/);
  assert.match(illustration, /workflow: "Dependabot \/ Update Planner"/);
  assert.match(illustration, /repository: "github\/gh-aw"/);
  assert.match(illustration, /operation: "Repo Assist issue triage"/);
  assert.match(illustration, /workflow: "Repo Assist \/ Issue Triage"/);
  assert.match(illustration, /operation: "Repo Assist issue fix"/);
  assert.match(illustration, /workflow: "Repo Assist \/ Issue Fix"/);
  assert.match(illustration, /operation: "Repo Assist maintenance"/);
  assert.match(illustration, /workflow: "Repo Assist \/ Maintenance"/);
  assert.match(illustration, /\{job\.operation\} · \{job\.repository\} · \{job\.mode\}/);
  assert.match(illustration, /class="run-card dispatcher dispatcher-dependabot"/);
  assert.match(illustration, /class="run-card dispatcher dispatcher-repo-assist"/);
  assert.match(illustration, /Ops dispatcher/);
  assert.match(illustration, /class={`run-card worker \$\{job\.timing\}`}/);
  assert.match(illustration, /animation: spin/);
  assert.match(illustration, /class="status running action-progress"/);
  assert.match(illustration, /class="progress-track"/);
  assert.match(illustration, /class="progress-ring"/);
  assert.match(illustration, /class="progress-dot"/);
  assert.match(illustration, /stroke-dasharray: 22 18/);
  assert.match(illustrationIcons, /M8 0a8 8 0 1 1 0 16/);
  assert.match(illustration, /repository: "github\/gh-aw-firewall"/);
  assert.match(illustration, /repository: "githubnext\/gh-cao"/);
  assert.match(illustration, /repository: "githubnext\/agentics"/);
  assert.match(illustration, /failed: true/);
  assert.match(illustration, /repository: "githubnext\/gh-aw"/);
  assert.match(illustration, /organizations: \["githubnext", "github"\]/);
  assert.match(illustration, /class="mobile-campaign-organizations"/);
  assert.match(illustration, /<em>\+ others<\/em>/);
  assert.match(illustration, /class="branch">main<\/span>/);
  assert.match(illustration, /class="run-age"/);
  assert.match(illustration, /class="run-meta"/);
  assert.doesNotMatch(illustration, /class="run-menu"/);
  assert.match(illustration, /In progress/);
  assert.match(illustration, /\{job\.failed \? "Failed" : "Done"\} · \{job\.duration\}/);
  assert.match(illustration, /M2\.343 13\.657A8 8 0 1 1/);
  assert.match(illustration, /@keyframes dep-dispatch-running/);
  assert.match(illustration, /@keyframes repo-dispatch-running/);
  assert.match(illustration, /@keyframes job-1-running/);
  assert.match(illustration, /@keyframes job-5-running/);
  assert.match(illustration, /grid-template-rows: 64px 72px 72px 0 64px 72px 72px 72px/);
  assert.match(illustration, /\.execution-grid \.dispatcher-repo-assist \{\s*transform: translateY\(-4px\)/);
  assert.doesNotMatch(illustration, /\.execution-grid \.dispatcher-repo-assist \{[\s\S]*?border-bottom-color:/);
  assert.match(illustration, /\.run-card\.job-5 \{[\s\S]*?translateY\(4px\)/);
  assert.doesNotMatch(illustration, /\.repository-card\.job-5 \{[\s\S]*?translateY\(4px\)/);
  assert.match(illustration, /\.actions-column \{[\s\S]*?align-self: start/);
  assert.match(illustration, /@media \(prefers-reduced-motion: reduce\)/);
  assert.match(
    illustration,
    /\.dispatch-illustration \*,[\s\S]*?\.dispatch-illustration \*::before,[\s\S]*?\.dispatch-illustration \*::after \{[\s\S]*?animation: none/,
  );
  assert.match(illustration, /\.status\.complete \{[\s\S]*?opacity: 1 !important/);
});

test("the home navigation no longer links to the setup wizard", () => {
  assert.doesNotMatch(headerLinks, /\{ label: "Setup", href: "\/gh-cao\/setup\/" \}/);
});

test("the removed setup route redirects to the command quickstart", () => {
  assert.match(astroConfig, /"\/setup": "\/gh-cao\/setup-quickstarts\/"/);
});

test("wizard page, UI, styles, and policy helpers are removed", () => {
  assert.equal(existsSync("docs/pages/setup.astro"), false);
  assert.equal(existsSync("docs/components/OpsWizard.astro"), false);
  assert.equal(existsSync("docs/styles/wizard-page.css"), false);
  assert.equal(existsSync("docs/lib/configured-operations.mjs"), false);
});

test("the campaign catalog remains independent of setup wizard configuration", () => {
  assert.match(catalog, /import rootManifestSource from "\.\.\/\.\.\/aw\.yml\?raw"/);
  assert.match(catalog, /\.experimental\/\*\/aw\.\{yml,yaml\}/);
  assert.match(catalog, /export const catalogEntries = campaignEntries\s+\.filter\(\(entry\) => !entry\.private\)/);
  assert.doesNotMatch(catalog, /controlPolicy|configuredOperationEntries|selectConfiguredOperations/);
});

test("landing page keeps the dark canvas black with green story highlights and the light blueprint intact", () => {
  assert.match(hero, /--cao-page-grid-minor: rgba\(26, 127, 55,/);
  assert.match(hero, /--cao-page-grid-major: rgba\(26, 127, 55,/);
  assert.match(hero, /:global\(:root\[data-theme="dark"\] \.content-panel:has\(\.hierarchy-hero\)\)/);
  assert.match(hero, /--cao-story-text: #4d5560/);
  assert.match(hero, /--cao-story-text: #b1bac4/);
  assert.match(hero, /--cao-section-accent-start: 12%/);
  assert.match(hero, /--cao-section-accent-end: 5%/);
  assert.match(hero, /radial-gradient\(\s*ellipse 80% 120% at 0% 0%,\s*color-mix\(in srgb, var\(--section-accent\) var\(--cao-section-accent-start\), transparent\)/);
  assert.match(hero, /radial-gradient\(\s*ellipse 80% 120% at 100% 100%,\s*color-mix\(in srgb, var\(--section-accent\) var\(--cao-section-accent-end\), transparent\)/);
  const darkCanvas = hero.match(/:global\(:root\[data-theme="dark"\] \.content-panel:has\(\.hierarchy-hero\)\) \{([^}]+)\}/)?.[1];
  assert.ok(darkCanvas);
  assert.match(darkCanvas, /--cao-page-bg: #000000;/);
  assert.match(darkCanvas, /background-image: none;/);
  assert.doesNotMatch(darkCanvas, /gradient\(/);
  assert.match(hero, /:global\(:root\[data-theme="dark"\]\) \.hierarchy-hero \{[^}]*background-image: none;/);
  const darkStory = hero.match(/:global\(:root\[data-theme="dark"\]\) \.product-story \{([^}]+)\}/)?.[1];
  assert.ok(darkStory);
  assert.match(darkStory, /radial-gradient\(ellipse 80% 120% at 0% 0%, rgba\(63, 185, 80, 0\.16\), transparent 70%\)/);
  assert.match(darkStory, /radial-gradient\(ellipse 80% 120% at 100% 100%, rgba\(63, 185, 80, 0\.1\), transparent 72%\)/);
  assert.match(darkStory, /rgba\(13, 17, 23, 0\.88\)/);
  assert.match(hero, /:global\(:root\[data-theme="dark"\]\) \.product-story::before \{[\s\S]*?display: none/);
  assert.match(hero, /\.section-eyebrow::before \{[\s\S]*?border-radius: 1px/);
  assert.doesNotMatch(hero, /--section-accent: #(?:58a6ff|a371f7)/);
  assert.doesNotMatch(hero, /--cao-page-blue-glow/);
  assert.match(hero, /background-size: auto, auto, 160px 160px, 160px 160px, 32px 32px, 32px 32px/);
  assert.match(hero, /linear-gradient\(var\(--cao-page-grid-major\) 1px, transparent 1px\)/);
  assert.match(hero, /linear-gradient\(var\(--cao-page-grid-minor\) 1px, transparent 1px\)/);
});

test("landing product name uses an accessible GitHub-style cursor typing animation", () => {
  assert.match(heroIntro, /class="product-name">/);
  assert.doesNotMatch(heroIntro, /class="product-name" aria-label=/);
  assert.match(heroIntro, /font-family: "Mona Sans Mono", monospace/);
  assert.match(heroIntro, /font-weight: 600/);
  assert.match(heroIntro, /color: var\(--cao-product-label\)/);
  assert.match(hero, /--cao-product-label: #59636e/);
  assert.match(hero, /--cao-product-label: #a4aea6/);
  assert.match(heroIntro, /class="product-name-cursor"/);
  assert.match(heroIntro, /width: 9px/);
  assert.match(heroIntro, /height: 14px/);
  assert.match(heroIntro, /background: #5fed83/);
  assert.match(heroIntro, /const TYPE_DELAY_MS = 900/);
  assert.match(heroIntro, /const CHARACTER_INTERVAL_MS = 32/);
  assert.match(heroIntro, /const TRANSIENT_GLYPHS = \["\*", "_", ""\]/);
  assert.match(heroIntro, /cursor\?\.classList\.add\("is-animated"\)/);
  assert.match(heroIntro, /@keyframes product-name-cursor-blink/);
  assert.match(heroIntro, /@media \(prefers-reduced-motion: reduce\)/);
});

test("landing terminal uses accessible CSS motion without a JavaScript player", () => {
  assert.doesNotMatch(terminal, /<script>|lottie/i);
  assert.match(terminal, /animation: terminal-line-enter/);
  assert.match(terminal, /@keyframes terminal-line-enter/);
  assert.match(terminal, /@media \(prefers-reduced-motion: reduce\)/);
  assert.match(terminal, /animation: none/);
  assert.match(terminal, /--terminal-bg: #ffffff/);
  assert.match(terminal, /:global\(:root\[data-theme="dark"\]\) \.terminal/);
  assert.match(terminal, /--terminal-bg: #0d1117/);
  assert.match(terminal, /background: var\(--terminal-bg\)/);
  assert.match(terminal, /class="syntax-command"/);
  assert.match(terminal, /class="syntax-command">gh<\/span> extension install githubnext\/gh-cao/);
});
