import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { commandHandlers } from "../../activity/commands/index.mjs";

const docs = readFileSync("docs/cao-cli.md", "utf8");
const help = `${readFileSync("activity/cao.mjs", "utf8")}\n${readFileSync("activity/cli-usage.mjs", "utf8")}`;
const gettingStarted = readFileSync("docs/setup-quickstarts.md", "utf8");
const walkthrough = readFileSync("docs/repo-assist-quickstart.md", "utf8");
const installation = "gh extension install githubnext/gh-cao";
const fallback = "curl -fsSL https://raw.githubusercontent.com/githubnext/gh-cao/main/install-gh-cao.sh | bash";

test("getting started reaches monitoring before optional campaign execution", () => {
  const optional = gettingStarted.indexOf("## Optional: add automation later");
  assert.ok(optional > 0);
  const monitoring = gettingStarted.slice(0, optional);
  const commands = [
    installation,
    "gh cao --version",
    "gh cao setup",
    "gh cao doctor",
    'git commit -m "Set up CAO monitoring"',
    "git push",
    "gh workflow run cao-activity.yml",
  ];
  let previous = -1;
  for (const command of commands) {
    const index = monitoring.indexOf(command);
    assert.ok(index > previous, `${command} should follow the preceding stage`);
    previous = index;
  }
  assert.deepEqual([...monitoring.matchAll(/^## Step \d - (.+)$/gm)].map((match) => match[1]), [
    "Install CAO",
    "Connect one repository",
    "Publish and open your dashboard",
  ]);
  assert.match(monitoring, /\*\*Estimated time: 15 minutes\*\*/);
  assert.doesNotMatch(monitoring, /Video TODO|recording not yet available/);
  assert.match(monitoring, /one monitoring path[\s\S]*setup-compatibility\.md/);
  const install = monitoring.slice(monitoring.indexOf("## Step 1"), monitoring.indexOf("## Step 2"));
  const extension = install.slice(install.indexOf("### Extension-first quickstart"), install.indexOf("<details>"));
  assert.ok(extension.includes("```bash\ngh extension install githubnext/gh-cao\ngh cao --version\n```"));
  assert.match(extension, /Native setup prepares missing runtime, policy, and gh-aw/);
  assert.match(extension, /without a separate\s+installer or local Node\.js/);
  const installer = install.slice(install.indexOf("<summary>Installer alternative"));
  assert.match(install, /`gh cao` GitHub CLI extension/);
  assert.match(install, /Existing policy is preserved/);
  assert.match(install, /gh repo create OWNER\/CONTROL_REPOSITORY --private --clone/);
  assert.match(install, /gh repo clone OWNER\/CONTROL_REPOSITORY/);
  assert.ok(install.includes(`\`\`\`bash\n${installation}\ngh cao --version\n\`\`\``));
  assert.ok(installer.includes(`\`\`\`bash\n${fallback}\n\`\`\``));
  assert.match(monitoring, /Setup prepares missing runtime and policy, reuses existing installations/);
  assert.match(monitoring, /No separate `init` is needed/);
  assert.match(install, /:::tip\[Installation and authentication\]/);
  assert.match(install, /```bash\ngh auth login\ngh auth status\n```/);
  assert.match(install, /:::tip\[SAML-token installation failure\]/);
  assert.ok(install.indexOf(installation) < install.indexOf(fallback), "prefer GitHub CLI before curl fallback");
  assert.match(install, /Public tool downloads are anonymous/);
  assert.doesNotMatch(install, /--scopes repo,workflow/);
  const prerequisites = monitoring.slice(monitoring.indexOf("## Prerequisites"), monitoring.indexOf("## Step 1"));
  assert.match(prerequisites, /Logged in to GitHub CLI[\s\S]*gh auth status[\s\S]*gh auth login/);
  assert.match(monitoring, /## Prerequisites/);
  assert.match(gettingStarted, /## Continue learning/);
  assert.match(gettingStarted, /<span id="4-open-your-dashboard"><\/span>/);
  for (const anchor of [
    "guided-setup-instead",
    "step-3---review-and-publish-your-configuration",
    "step-4---wait-for-collection-and-inspect-your-dashboard",
  ]) {
    assert.ok(gettingStarted.includes(`<span id="${anchor}"></span>`), anchor);
  }
  assert.ok(gettingStarted.trim().split(/\s+/).length < 1000, "first-run guide should stay concise");
  assert.doesNotMatch(monitoring, /--quickstart|gh aw run|cao\.sh (?:add|enable|quickstart)|AI credits/);
  assert.match(monitoring, /No campaigns or agentic work are installed/);
  assert.match(monitoring, /Commit and push these CAO monitoring changes\? \[y\/N\]/);
  assert.match(monitoring, /reviewed CAO files and returns \*\*deployment pending\*\* links immediately/);
  assert.match(monitoring, /synchronized\s+default branch, matching origin, and empty index; no protection bypass/);
  assert.match(monitoring, /Declining, blocked, or unattended setup stays local/);
  assert.match(monitoring, /Check \*\*CAO validation\*\* in Actions/);
  assert.match(monitoring, /control-plane\.campaigns` is `\{\}`/);
  assert.match(monitoring, /control-plane\.workflows` and\s+`control-plane\.operations` absent or `\{\}`/);
  assert.match(monitoring, /empty dashboard is expected/);
  assert.match(monitoring, /not every ordinary Actions job/);
  assert.match(monitoring, /does\s+\*\*not\*\*\s+guarantee a\s+private Pages site/);
  assert.match(monitoring, /including its Pages deployment/);
  assert.match(monitoring, /authentication\.md#public-only-monitoring-quickstart/);
  const nativeSetup = readFileSync("internal/cli/native_setup.go", "utf8");
  for (const prompt of ["Repositories", "Authentication", "Apply this setup?"]) {
    assert.ok(nativeSetup.includes(prompt) && monitoring.includes(`**${prompt}`), prompt);
  }
  assert.match(monitoring, /public-only[\s\S]*GITHUB_TOKEN/);
  assert.match(monitoring, /Choose \*\*Authentication\*\* only if prompted/);
  assert.match(monitoring, /Confirmed public targets[\s\S]*automatically use `public-only`/);
  assert.match(monitoring, /even when the control repository is private/);
  assert.match(monitoring, /Decline for non-public evidence/);
  assert.match(monitoring, /before retrying; do not publish incomplete setup/);
  assert.doesNotMatch(monitoring, /gh cao validate/);
  assert.match(gettingStarted.slice(optional), /setup\/quickstart\.md/);
  assert.match(gettingStarted.slice(optional), /repo-assist-quickstart\.md/);
  assert.match(gettingStarted.slice(optional), /Installation is not execution/);
  assert.match(gettingStarted.slice(optional), /operation-patterns\.md/);
  assert.match(gettingStarted.slice(optional), /Direct operation.*authority-envelopes\.md/);
  assert.match(gettingStarted.slice(optional), /Standalone fleet workflow.*configuration\.md#standalone-agentic-workflows/);
  assert.match(gettingStarted.slice(optional), /None is enrolled, enabled or run by setup/);
  assert.match(gettingStarted.slice(optional), /\*\*review\*\* mode with \*\*one\s+repository\*\*/);
  assert.match(walkthrough, /monitor existing[\s\S]*?\[Set Up CAO\]\(setup-quickstarts\.md\)/);
  assert.match(docs, /\[Repo Assist walkthrough\]\(repo-assist-quickstart\.md\)/);
});

test("extension installation guidance distinguishes discovery, promotion, and runtime installation", () => {
  assert.ok(docs.includes(installation));
  assert.ok(docs.indexOf(installation) < docs.indexOf(fallback), "document GitHub CLI installation first");
  assert.match(docs, /:::tip\[SAML-token installation failure\]/);
  assert.match(docs, /fallback when SAML-restricted tokens prevent/);
  assert.match(docs, /upstream[\s\S]*install-gh-aw\.sh/);
  assert.match(docs, /Public CAO release metadata, binaries, checksums, and the upstream installer\s+are fetched anonymously/);
  assert.match(docs, /No credentials are cleared from the parent environment/);
  assert.doesNotMatch(docs, /Legacy control-plane installer|legacy installer/);
  assert.match(docs, /CAO releases include native binaries for GitHub CLI discovery/);
  assert.match(docs, /`releases\/latest` before selecting a\s+`--pin` tag/);
  assert.match(docs, /merging source does not update an installed binary/);
  assert.doesNotMatch(docs, /verified with `v0\.1\.24`|unified setup requires a newer/);
  assert.match(docs, /gh extension upgrade gh-cao/);
  assert.match(docs, /Extension installation alone does not install the control-repository runtime\s+or `gh-aw`/);
});

test("native setup hands off to outcome and scope rather than pattern selection", () => {
  const source = readFileSync("internal/cli/native_setup.go", "utf8");
  assert.ok(source.includes("Monitoring configured locally. No commit or push was performed."));
  assert.ok(source.includes("setupPublishOutput("));
  assert.ok(source.includes("Optional automation: https://githubnext.github.io/gh-cao/operation-patterns/"));
  assert.ok(readFileSync("internal/cli/setup_publish.go", "utf8")
    .includes("Deployment pending; setup is not waiting for Actions."));
  assert.doesNotMatch(source, /Add your first campaign separately|Choose an operation pattern/);
  const skill = readFileSync("skills/setup-cao/SKILL.md", "utf8");
  assert.match(skill, /operation-patterns\.md/);
});

test("outcome-to-execution guidance derives structures without granting authority", () => {
  const guidance = readFileSync("docs/operation-patterns.md", "utf8");
  const normalized = guidance.replace(/\s+/g, " ");
  const cases = [
    ["shared visibility", "Observe only"],
    ["caller already knows the target", "Direct operation"],
    ["bounded portion of an enrolled fleet", "Standalone fleet workflow"],
    ["independent isolation and history", "Campaign"],
    ["deployment stages, approvals, fan-in or long waits", "Ordinary GitHub Actions"],
  ];
  const rows = guidance.split("\n").filter((line) => line.startsWith("|"));
  for (const [requirement, structure] of cases) {
    assert.ok(rows.some((row) => row.includes(requirement) && row.includes(`**${structure}**`)),
      `${requirement} should derive ${structure}`);
  }
  assert.match(normalized, /campaign even when the caller knows the targets/);
  assert.match(normalized, /Repository count or a schedule alone does not require a campaign/);
  assert.match(normalized, /Infer execution structure, never authority/);
  assert.match(normalized, /reviewed policy decision/);
  assert.match(normalized, /exact workflow SHA/);
  assert.match(normalized, /Ask only for genuinely missing requirements/);
  for (const filename of [
    "skills/setup-cao/SKILL.md",
    "skills/create-cao-campaign/SKILL.md",
    "skills/add-cao-campaign/SKILL.md",
  ]) {
    const skill = readFileSync(filename, "utf8").replace(/\s+/g, " ");
    assert.match(skill, /operation-patterns\.md/, filename);
    assert.match(skill, /do not ask users to choose a pattern/, filename);
    assert.match(skill, /genuinely missing requirements/, filename);
  }
  const authoring = readFileSync(".github/cao/instructions.md", "utf8");
  assert.match(authoring, /Infer execution structure, never authority/);
  const installation = readFileSync("skills/add-cao-campaign/SKILL.md", "utf8");
  assert.match(installation, /Keep exact package selection and installation approval explicit/);
});

test("CAO command guide covers the operator-facing command surface", () => {
  for (const command of [
    "init", "setup", "setup auth github-app", "setup auth enterprise-app",
    "setup auth token", "add OWNER/REPO/CAMPAIGN", "update",
    "upgrade-gh-aw", "campaigns mode preview", "campaigns mode live",
    "campaigns enable", "campaigns disable", "download", "doctor",
    "computation runtime-health", "query", "gh runs", "gh issues", "gh prs",
  ]) {
    assert.match(docs, new RegExp(`gh cao ${command.replaceAll("-", "\\-")}`));

  }
});

test("Node-only command documentation uses explicit runtime entry points without a shell launcher", () => {
  for (const command of [
    "repair-database", "operational-value", "cluster-problems",
    "dashboard-complexity", "prune-dashboard", "validate --json",
  ]) {
    assert.ok(docs.includes(`node activity/cao.mjs ${command}`), command);
  }
  assert.doesNotMatch(docs, /(?<![\w-])cao\.sh/);
  assert.match(docs, /gh cao setup --help/);
  const demo = readFileSync("docs/components/TerminalDemo.astro", "utf8");
  assert.match(demo, /gh cao<\/span> setup/);
  assert.match(demo, /data-terminal-output="setup">Monitoring configured locally\. No commit or push was performed\.[\s\S]*Next: gh cao doctor/);
  assert.doesNotMatch(demo, /(?<![\w-])cao\.sh|<\/span> validate/);
});

test("command inventory covers registered Node APIs and process boundaries", () => {
  const inventory = docs.slice(docs.indexOf("| Surface | Commands | Execution |"), docs.indexOf("The extension is **entirely Go**"));
  for (const command of commandHandlers.keys()) {
    assert.ok(inventory.includes(`\`${command}`) || inventory.includes(`\`node activity/cao.mjs ${command}`), command);
  }
  assert.match(inventory, /Actions-only JavaScript runtime/);
  assert.match(inventory, /Workflow-agent evidence APIs[\s\S]*not deprecated duplicates/);
  assert.match(inventory, /configure admission with `setup admission`/);
  assert.doesNotMatch(inventory, /`admission setup`/);
  assert.match(inventory, /`init` is optional local policy-only/);
  for (const binary of ["cao-dashboard", "cao-webhook-gateway", "cao-cli-quota", "cao-cli-relay"]) {
    assert.ok(inventory.includes(`\`${binary}\``), binary);
  }
  assert.match(inventory, /internal stdin\/stdout scheduling protocol/);
  assert.match(inventory, /npm `cao` bin[\s\S]*same\s+Node entry point/);
});

test("ingestion documentation separates active packaging from supported manual artifact import", () => {
  const rows = docs.split("\n").filter((line) => line.startsWith("|"));
  const pipeline = rows.find((line) => line.startsWith("| Active ingestion and packaging |"));
  assert.ok(pipeline);
  for (const command of ["ingest-jsonl", "compact-jsonl", "hash-payloads"]) {
    assert.ok(pipeline.includes(`\`${command}\``), command);
  }
  assert.ok(!pipeline.includes("`ingest`"));
  const manual = rows.find((line) => line.startsWith("| Manual artifact import |"));
  assert.ok(manual);
  assert.match(manual, /`ingest`/);
  assert.match(manual, /not used by the Activity or Dashboard pipelines and not deprecated/);
  const ingestion = readFileSync("docs/dashboard-data-ingestion.md", "utf8").replace(/\s+/g, " ");
  assert.match(ingestion, /### Manual artifact import .*`node activity\/cao\.mjs ingest` is a supported manual importer/);
  assert.match(ingestion, /### Active JSONL pipeline .*`compact-jsonl`, `hash-payloads`, and `ingest-jsonl`/);
  assert.match(ingestion, /not just a checksum utility/);
  assert.match(ingestion, /gh cao download/);
  assert.match(ingestion, /gh cao query/);
  assert.match(ingestion, /node activity\/cao\.mjs repair-database/);
});

test("hosted-executor App setup belongs only to the native operator CLI", () => {
  assert.match(docs, /gh cao setup auth hosted-cli --repo OWNER\/CONTROL/);
  assert.match(docs, /mode-`0600` temporary bootstrap file/);
  assert.match(docs, /Setup does not change policy, Actions secrets, authentication mode/);
  assert.doesNotMatch(help, /cao setup-auth hosted-cli/);
  assert.doesNotMatch(docs, /node activity\/cao\.mjs setup-auth hosted-cli/);
});

test("active documentation and architecture do not advertise the retired Quickstart capability", () => {
  for (const filename of [
    "docs/cao-cli.md", "docs/repo-assist-quickstart.md", "docs/glossary.md",
    "docs/authentication.md", "docs/setup-quickstarts.md", "README.md",
    "repo-assist/README.md", "ARCHITECTURE.md", "CODEBASE.yml",
    "specs/repository-layout-and-installation.md", "skills/setup-cao/SKILL.md",
    "skills/setup-cao/references/implementation-details.md",
    "skills/setup-cao/references/non-interactive.md",
    "skills/setup-cao/references/public-control-repositories.md",
  ]) {
    assert.doesNotMatch(
      readFileSync(filename, "utf8"),
      /--quickstart|\b(?:gh cao|node activity\/cao\.mjs) quickstart\b|activity\/quickstart\.mjs|Node-only installer quickstart|control_plane_quickstart|first_success_quickstart/,
      filename,
    );
  }
});

test("CAO command guide explains the workflow execution boundary", () => {
  assert.match(docs, /There is intentionally no `cao run` command/);
  assert.match(docs, /gh-aw owns workflow execution/);
  assert.match(docs, /`gh cao campaigns mode preview` writes `mode: review`/);
  assert.match(docs, /different from\s+`gh aw doctor`/);
  assert.match(docs, /Control-plane validation is Actions-only/);
  assert.match(docs, /node activity\/cao\.mjs validate --json/);
  assert.doesNotMatch(docs, /gh cao validate/);
});

test("campaign guides distinguish native campaign installation from standalone workflows", () => {
  for (const campaign of ["bug-bash", "repo-assist"]) {
    const guide = readFileSync(`${campaign}/README.md`, "utf8");
    assert.ok(guide.includes(`gh cao add githubnext/gh-cao/${campaign}`));
  }
  assert.match(docs, /gh cao add CAMPAIGN/);
  assert.match(docs, /gh cao add WORKFLOW/);
  assert.match(docs, /single native command for installing a standalone CAO workflow\s+or a campaign package/);
  assert.doesNotMatch(docs, /gh cao campaigns add/);
  assert.match(docs, /Campaign installation never commits, pushes\s+or dispatches work/);
  assert.match(docs, /Standalone workflows accept only `--force` and `--engine`/);
});

test("manual Repo Assist walkthrough separates installation, execution, and evidence collection", () => {
  const install = walkthrough.indexOf("gh cao add githubnext/gh-cao/repo-assist");
  const review = walkthrough.indexOf("gh cao campaigns mode preview repo-assist");
  const run = walkthrough.indexOf("gh aw run repo-assist");
  const collect = walkthrough.indexOf("gh workflow run cao-activity.yml");
  assert.ok(install > 0 && review > install && run > review && collect > run);
  const push = walkthrough.indexOf("\ngit push\n");
  const initialPause = walkthrough.indexOf("\ngh workflow disable repo-assist.lock.yml\n");
  const checkpoint = walkthrough.indexOf("Check **CAO validation**");
  const executionStep = walkthrough.indexOf("## 2. Request one maintenance review");
  assert.ok(push > review && initialPause > push && checkpoint > initialPause && executionStep > checkpoint);
  assert.match(walkthrough, /Keep it paused until Step 2/);
  const execution = walkthrough.slice(executionStep);
  assert.ok(execution.indexOf("gh workflow enable") < execution.indexOf("gh aw run"));
  assert.ok(execution.indexOf("gh workflow disable") > execution.indexOf("gh aw run"));
  assert.match(walkthrough, /Installation is not execution/);
  assert.match(walkthrough, /nothing commits, pushes, or dispatches on your behalf/);
  assert.match(walkthrough, /Commit the reviewed workflows and\s+policy together/);
  assert.match(walkthrough, /Check \*\*CAO validation\*\*/);
  assert.match(walkthrough, /control-plane\.campaigns\.repo-assist\.targets/);
  assert.match(walkthrough, /control-plane\.activity\.repositories/);
  assert.match(walkthrough, /enable only maintenance/);
  assert.match(walkthrough, /gh workflow disable repo-assist\.lock\.yml/);
  assert.match(walkthrough, /Pause the orchestrator even if dispatch fails/);
  assert.match(walkthrough, /does not cancel an already requested run/);
  assert.match(walkthrough, /repeating `gh aw run` requests new work/);
  assert.match(walkthrough, /successful Pages deployment job/);
  assert.match(walkthrough, /never applies\s+changes to the target repository/);
  assert.match(walkthrough, /A no-op is a valid result/);
  assert.match(walkthrough, /1,250 AI credits/);
  assert.match(walkthrough, /Use a private control repository for non-public targets or evidence/);
  assert.doesNotMatch(walkthrough, /--step (?:install|run|collect|dashboard)|--wait|--raw-field starter=/);
});

test("Repo Assist walkthrough preserves verified historical examples without dispatching work", () => {
  const campaign = readFileSync("repo-assist/README.md", "utf8");
  const readme = readFileSync("README.md", "utf8");
  const showcase = campaign.slice(campaign.indexOf("## See Repo Assist in action"), campaign.indexOf("## Install"));
  assert.match(walkthrough, /No demo repository is created,\s+and useful findings are not guaranteed/);
  assert.match(walkthrough, /## See more without running more/);
  assert.match(walkthrough, /github\.com\/githubnext\/gh-cao\/blob\/main\/repo-assist\/README\.md#see-repo-assist-in-action/);
  assert.match(readme, /repo-assist\/README\.md#see-repo-assist-in-action/);
  for (const [issue, run] of [
    [17740, 38399472881], [17851, 38493105790],
    [17378, 38119485916], [14060, 36227760220],
  ]) {
    assert.ok(showcase.includes(`https://github.com/githubnext/gh-cao/issues/${issue}`));
    // eslint-disable-next-line cao/no-hardcoded-github-actions-url -- These historical reports belong to github.com.
    assert.ok(showcase.includes(`https://github.com/githubnext/gh-cao/actions/runs/${run}`));
  }
  assert.match(showcase, /Reading these reports does not start a workflow or spend AI credits/);
  assert.match(showcase, /No new patch was made[\s\S]*originating run failed[\s\S]*not a successful repair example/);
  assert.match(showcase, /not proof that a patch was applied, accepted, or merged/);
  assert.match(showcase, /workflow artifacts may expire/);
  assert.doesNotMatch(showcase, /gh aw run|gh workflow enable/);
});
