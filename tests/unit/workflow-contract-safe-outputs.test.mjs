import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import { mkdtempSync, mkdirSync, readFileSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { parse } from "yaml";
import { escapedTargetReadCredential, portableSkill, root, stepBlock, workflow, workflowsDirectory } from "./workflow-contract.helpers.mjs";

// Safe-output reporting, issue, and pull request contracts.

test("Repo Assist runs every six hours and scopes Copilot assignment to live targets", () => {
  const dispatcher = parse(/^---\n([\s\S]*?)\n---/.exec(workflow("repo-assist.md"))[1]);
  assert.equal(dispatcher.on.schedule, "every 6 hours");
  const compiledDispatcher = parse(workflow("repo-assist.lock.yml"));
  assert.match(compiledDispatcher.on.schedule[0].cron, /^\d+ \*\/6 \* \* \*$/);

  const source = workflow("repo-assist-issue-triage.md");
  const config = parse(/^---\n([\s\S]*?)\n---/.exec(source)[1]);
  const assignment = config["safe-outputs"]["assign-to-agent"];
  assert.deepEqual(assignment, {
    name: "copilot",
    allowed: ["copilot"],
    target: "*",
    "target-repo": "${{ inputs.target_repo }}",
    "pull-request-repo": "${{ inputs.target_repo }}",
    "custom-instructions": "Read the Repo Assist triage comment on this issue for the verified change, relevant paths, and acceptance tests. Implement only that bounded change, validate it, and open a pull request for maintainer review. Do not merge or broaden scope.",
    "github-token": "${{ secrets.GH_AW_AGENT_TOKEN }}",
    max: 1,
    staged: "${{ inputs.safe_output_mode != 'live' }}",
  });
  const compiled = parse(workflow("repo-assist-issue-triage.lock.yml"));
  const handlerStep = compiled.jobs.safe_outputs.steps.find(
    (step) => step.env?.GH_AW_SAFE_OUTPUTS_HANDLER_CONFIG,
  );
  assert.ok(handlerStep, "assignment must reach the production safe-output handler");
  assert.deepEqual(JSON.parse(handlerStep.env.GH_AW_SAFE_OUTPUTS_HANDLER_CONFIG).assign_to_agent, assignment);
  assert.match(source, /first call `add_comment`[\s\S]*Then call `assign_to_agent`/);
  assert.match(source, /Do not pass per-call custom instructions/);
  assert.match(source, /Skip issues already assigned to Copilot or another implementer/);
  assert.match(source, /Search open pull requests and Repo Assist issue-fix review records for an active fix/);
  assert.match(source, /Never call `assign_to_agent` in `review` mode, including for a central review issue/);
  assert.match(source, /Do not delegate ambiguous, broad, breaking, security-sensitive, or new-dependency work/);
});

test("Repo Assist triage grader accepts one target-bound live assignment and rejects unsafe requests", () => {
  const assignment = {
    type: "assign_to_agent",
    issue_number: 42,
    repo: "example/target",
    agent: "copilot",
    pull_request_repo: "example/target",
  };
  function score(outputs, mode = "live") {
    const request = {
      schemaVersion: 1,
      run: { repository: "example/control" },
      event: { inputs: { target_repo: "example/target", safe_output_mode: mode } },
      outputs,
    };
    const result = execFileSync("bash", [
      join(root, "repo-assist", ".github", "graders", "repo-assist-issue-triage-operational-value.sh"),
    ], { input: JSON.stringify(request), encoding: "utf8" });
    return JSON.parse(result)[0].value;
  }
  const comment = {
    type: "add_comment", issue_number: 42, repo: "example/target",
    body: "🤖 *This is an automated response from Repo Assist.* Verified root cause. Fix src/example.js and run npm test.",
  };
  assert.equal(score([assignment]), 0);
  assert.equal(score([comment, assignment]), 1);
  assert.equal(score([assignment], "review"), 0);
  assert.equal(score([assignment, assignment]), 0);
  for (const override of [
    { repo: "example/other" },
    { pull_request_repo: "example/other" },
    { agent: "other" },
    { issue_number: null },
  ]) {
    assert.equal(score([comment, { ...assignment, ...override }]), 0);
  }
  assert.equal(score([comment]), 1);
  assert.equal(score([assignment, comment]), 1);
  assert.equal(score([assignment, { ...comment, issue_number: 43 }]), 0);
  assert.equal(score([{ type: "noop" }]), null);
});

test("operations creation guidance scopes detection and omits worker evals", () => {
  const campaignSkill = portableSkill("create-cao-campaign");

  assert.match(campaignSkill, /safe-outputs\.threat-detection: false/);
  assert.match(campaignSkill, /default new dispatchers to `hourly`/);
  assert.match(campaignSkill, /`safe-outputs\.create-issue` or `safe-outputs\.create-pull-request`[\s\S]*?`labels: \[<campaign-slug>, <campaign-slug>:<worker-slug>\]`[\s\S]*?`title-prefix: "\[<campaign-slug>:<worker-slug>\] "`/);
  assert.match(campaignSkill, /every created issue or pull request identifies both its owning operation and worker/);
  assert.match(campaignSkill, /when `safe-outputs\.create-issue` is enabled, configure `deduplicate-by-title: true`/);
  assert.match(campaignSkill, /canonical unprefixed subject that remains identical for the same unresolved repository work across reruns/);
  assert.match(campaignSkill, /search all open campaign-worker issues in the safe-output repository and reuse or comment on matching work, or call `noop`/);
  assert.match(campaignSkill, /every issue-creating worker configures `deduplicate-by-title: true`, explicit expiry, bounded `max`, stable subject, and existing-item reuse instructions/);
  assert.match(campaignSkill, /Use `3d` for high-frequency telemetry, `7d` for fast-changing operational findings, `14d` for dependency and routine maintenance work, and `30d` only for compliance/);
  assert.match(campaignSkill, /Dependabot worker issues expire after `14d`/);
  assert.match(campaignSkill, /control-plane workflows inherit `noop\.report-as-issue: false` from `shared\/control\.md`/);
  assert.match(campaignSkill, /must not redeclare an empty local `noop:` block because it overrides imported handler settings/);
  assert.match(campaignSkill, /Standalone workflows that do not import shared control must configure `safe-outputs\.noop\.report-as-issue: false` explicitly/);
  assert.match(campaignSkill, /Do not use sub-issue grouping as backlog control/);
  assert.match(campaignSkill, /Expiration is lifecycle cleanup, not duplicate prevention/);
  assert.match(campaignSkill, /A model instruction alone is not sufficient when a handler-level safeguard exists/);
  assert.match(campaignSkill, /Pull requests:[\s\S]*stable branch or machine-readable body marker[\s\S]*search open pull requests/);
  assert.match(campaignSkill, /Comments and reviews:[\s\S]*do not post the same finding or status again/);
  assert.match(campaignSkill, /The orchestrator owns idempotent selection and dispatch\. Workers own idempotent repository outputs/);
  assert.match(campaignSkill, /singleton campaign concurrency with `group: "\$\{\{ github\.workflow \}\}"` and `cancel-in-progress: true`/);
  assert.match(campaignSkill, /unique worker, target repository, and effective mode tuple/);
  assert.match(campaignSkill, /evaluate the potential follow-up actions/);
  assert.match(campaignSkill, /single most important action with the highest expected return on investment/);
  assert.match(campaignSkill, /<details><summary><b>Agent prompt<\/b><\/summary> \.\.\. <\/details>/);
  assert.match(campaignSkill, /human can review the issue before using the prompt for an agentic run/);
  assert.match(campaignSkill, /no `evals` configuration; use deterministic graders for worker measurement/);
  assert.match(campaignSkill, /Confirm the orchestrator disables threat detection and every worker omits `evals`/);
  assert.match(campaignSkill, /CAO does not require organization-billed Copilot inference/);
  assert.match(campaignSkill, /Follow the control\s+repository's selected engine and inference\/billing profile/);
  assert.match(campaignSkill, /A different supported gh-aw engine\/provider is valid when explicitly configured for the control repository/);
  assert.match(campaignSkill, /not as a blocker to authoring campaigns for other providers/);
  assert.match(campaignSkill, /gh api orgs\/<organization>\/copilot\/billing/);
  assert.match(campaignSkill, /is completely optional: the user's token may not have access to billing information/);
  assert.match(campaignSkill, /affirmative `total_seats: 0` with `seat_management_setting: unconfigured` as Copilot runs being unavailable/);
  assert.match(campaignSkill, /Pi or Codex workflow using a `copilot\/\*` model is Copilot-backed/);
  assert.match(campaignSkill, /Do not use `aw\.yml` bootstrap `config`/);
});

test("issue-creating workers use campaign and worker title prefixes and labels", () => {
  for (const name of readdirSync(workflowsDirectory).filter((entry) => entry.endsWith(".md"))) {
    const source = workflow(name);
    if (!/role: worker/.test(source) || !/create-issue:/.test(source)) continue;
    if (name === "dependabot-update-planner.md") continue;

    const frontmatter = /^---\n([\s\S]*?)\n---/.exec(source)?.[1];
    assert.ok(frontmatter, `${name} must have frontmatter`);
    const config = parse(frontmatter);
    const controlImport = config.imports.find((entry) => entry.with?.role === "worker");
    assert.ok(controlImport?.with?.campaign, `${name} must declare its campaign slug`);
    assert.ok(controlImport?.with?.worker, `${name} must declare its worker slug`);
    assert.equal(
      config["safe-outputs"]["create-issue"]["title-prefix"],
      `[${controlImport.with.campaign}:${controlImport.with.worker}] `,
      name,
    );
    assert.deepEqual(
      config["safe-outputs"]["create-issue"].labels,
      [controlImport.with.campaign, `${controlImport.with.campaign}:${controlImport.with.worker}`],
      name,
    );
  }
});

test("shared control puts worker provenance in a linked footer instead of a section", () => {
  const control = workflow("shared/control.md");
  assert.ok(control.includes('When `correlation_id` is present, append one final blockquote line to safe-output issues, pull requests, or comments: `> cao: <central_repo>, correlation: [<correlation_id>](<control_plane_run_url>)`'));
  assert.ok(control.includes("Replace any importing workflow's `### Control Plane` section requirement with this footer; do not include both."));
});

test("workflow issue outputs are bounded, deduplicated, and centrally quiet on no-op", () => {
  const sharedSource = workflow("shared/control.md");
  const sharedFrontmatter = /^---\n([\s\S]*?)\n---/.exec(sharedSource)?.[1];
  assert.ok(sharedFrontmatter, "shared control must have frontmatter");
  assert.equal(parse(sharedFrontmatter)["safe-outputs"].noop["report-as-issue"], false);

  for (const name of readdirSync(workflowsDirectory).filter((entry) => entry.endsWith(".md"))) {
    const source = workflow(name);
    const frontmatter = /^---\n([\s\S]*?)\n---/.exec(source)?.[1];
    assert.ok(frontmatter, `${name} must have frontmatter`);
    const config = parse(frontmatter);
    const safeOutputs = config["safe-outputs"] ?? {};
    const issue = safeOutputs["create-issue"];
    if (issue) {
      if (name === "dependabot-update-planner.md") {
        assert.equal(issue.expires, undefined, `${name} keeps its parent durable across refreshes`);
        assert.equal(issue["deduplicate-by-title"], true, `${name} deduplicates stable parent and child titles`);
      } else {
        assert.equal(issue["deduplicate-by-title"], true, name);
      }
      if (name === "dependabot-update-planner.md") {
        assert.equal(issue["close-older-issues"], undefined, `${name} preserves its durable parent`);
        assert.equal(issue["require-temporary-id"], true, `${name} links same-run children to a new parent`);
      } else {
        assert.match(String(issue.expires), /^[1-9][0-9]*d$/, name);
      }
      assert.ok(Number.isInteger(issue.max) && issue.max > 0, `${name} must bound create-issue max`);
      assert.ok(typeof issue["title-prefix"] === "string" && issue["title-prefix"].length > 0, `${name} must prefix issue titles`);
    }

    const importsControl = config.imports?.some((entry) => entry.uses === "shared/control.md");
    if (importsControl) {
      if (name === "self-care-docs-build-time-investigator.md") {
        assert.deepEqual(safeOutputs.noop, { max: 2, "report-as-issue": false }, name);
      } else {
        assert.equal(safeOutputs.noop, undefined, `${name} must inherit shared noop policy without overriding it`);
      }
    } else if (safeOutputs.noop) {
      assert.equal(safeOutputs.noop["report-as-issue"], false, name);
    }
  }

  const project = JSON.parse(workflow("aw.json"));
  assert.equal(project.maintenance.action_failure_issue_expires, 24);
  assert.equal(parse(/^---\n([\s\S]*?)\n---/.exec(workflow("docs-explanatory-diagrams.md"))[1])["safe-outputs"].noop["report-as-issue"], false);
});

test("self-care pages health worker creates a fix PR instead of a report issue", () => {
  const source = workflow("self-care-pages-health.md");

  assert.match(source, /create-pull-request:/);
  assert.doesNotMatch(source, /create-issue:/);
  assert.match(source, /Call `create_pull_request` exactly once/);
  assert.match(source, /Fix only the selected quick wins/);
});

test("Dependabot worker maintains a durable parent and one-PR child tasks without writing pull requests", () => {
  const source = workflow("dependabot-update-planner.md");
  const frontmatter = /^---\n([\s\S]*?)\n---/.exec(source)?.[1];
  assert.ok(frontmatter, "Dependabot worker must have frontmatter");
  const config = parse(frontmatter);
  const outputs = config["safe-outputs"];

  assert.equal(config.tools.github.mode, "local");
  assert.equal(config.tools.github["min-integrity"], "unapproved");
  assert.equal(config.permissions["vulnerability-alerts"], "read");
  assert.ok(config.tools.github.toolsets.includes("dependabot"));
  const controlImport = config.imports.find((entry) => entry.uses === "shared/control.md");
  assert.equal(controlImport.with.read_actions, "read");
  assert.equal(controlImport.with.read_checks, "read");
  assert.equal(controlImport.with.read_contents, "read");
  assert.equal(controlImport.with.read_issues, "read");
  assert.equal(controlImport.with.read_pull_requests, "read");
  assert.equal(controlImport.with.read_security_events, "read");
  assert.equal(controlImport.with.read_statuses, "read");
  assert.equal(controlImport.with.read_vulnerability_alerts, "read");
  const lock = workflow("dependabot-update-planner.lock.yml");
  assert.match(lock, /permission-vulnerability-alerts: "?read"?/);
  assert.match(
    lock,
    new RegExp(`GITHUB_MCP_SERVER_TOKEN: ${escapedTargetReadCredential}`),
  );
  const credential = lock.indexOf("name: Resolve CAO target read credential");
  const handoff = lock.indexOf("name: Download CAO control precompute artifact");
  const alertFetch = lock.indexOf("name: Fetch target Dependabot alert evidence");
  assert.ok(credential >= 0 && handoff > credential && alertFetch > handoff);
  assert.match(lock.slice(alertFetch), new RegExp(`github-token: ${escapedTargetReadCredential}`));
  assert.match(lock.slice(alertFetch), /GITHUB_ACTION_TOKEN: \$\{\{ github\.token \}\}/);
  assert.match(lock.slice(alertFetch), /getOctokit\(process\.env\.GITHUB_ACTION_TOKEN\)/);
  assert.doesNotMatch(lock.slice(alertFetch), /require\('@actions\/github'\)/);
  assert.match(lock.slice(alertFetch), /precompute\.target_repo !== target/);
  assert.match(lock.slice(alertFetch), /GET \/repos\/\{owner\}\/\{repo\}\/dependabot\/alerts/);
  assert.match(lock.slice(alertFetch), /response\.data\.length < 100/);
  assert.match(lock.slice(alertFetch), /complete: false, alerts: null, unavailable_reason: reason/);
  assert.match(lock.slice(alertFetch), /markUnavailable\(rateLimited \? 'api_rate_limit' : 'api_request_failed'\)/);
  assert.match(lock.slice(alertFetch), /markUnavailable\('pagination_limit_exceeded'\)/);
  assert.match(lock.slice(alertFetch), /complete: true, alerts/);
  assert.match(source, /If `complete` is `false` or `unavailable_reason` is present, treat alert evidence as unavailable/);
  assert.match(source, /Read `\/tmp\/gh-aw\/agent\/dependabot-alerts\.json`/);
  assert.match(source, /Do not use `list_dependabot_alerts` as a fallback/);
  assert.deepEqual(Object.keys(outputs).sort(), ["add-comment", "close-issue", "create-issue", "update-issue"]);
  assert.equal(outputs["create-issue"].max, 13);
  assert.equal(outputs["create-issue"]["deduplicate-by-title"], true);
  assert.equal(outputs["create-issue"]["require-temporary-id"], true);
  assert.deepEqual(outputs["create-issue"].labels, ["dependabot"]);
  assert.equal(outputs["create-issue"].expires, undefined);
  assert.equal(outputs["update-issue"].body, true);
  assert.equal(outputs["update-issue"].max, 13);
  assert.equal(outputs["update-issue"]["required-labels"], undefined);
  assert.equal(outputs["update-issue"]["required-title-prefix"], "[dependabot:update-planner] ");
  assert.equal(outputs["add-comment"]["pull-requests"], false);
  assert.equal(outputs["add-comment"].max, 1);
  assert.equal(outputs["add-comment"]["required-labels"], undefined);
  assert.equal(outputs["add-comment"]["required-title-prefix"], "[dependabot:update-planner] ");
  assert.equal(outputs["close-issue"].max, 12);
  assert.match(outputs["close-issue"]["required-title-prefix"], /Dependency update task for/);
  assert.match(source, /Dependency update plan for <owner>\/<repository>/);
  assert.match(source, /Dependabot update plan refreshed\./);
  assert.match(source, /<summary><b>Agent prompt<\/b><\/summary>/);
  assert.match(source, /Do not assign this parent issue to a coding agent/);
  assert.match(source, /produce exactly one pull request/);
  assert.match(source, /at most twelve open child task issues/);
  assert.match(source, /dependabot-update-task:repository=/);
  assert.match(source, /peer dependency compatibility/);
  assert.match(source, /golden fixtures/);
  assert.match(source, /never close the parent issue from the pull request/);
  assert.doesNotMatch(source, /Assign this issue to Copilot or another coding agent to complete every unchecked item/);
  assert.match(source, /repo-memory:/);
  assert.match(source, /issue-index\/<safe-output-owner>/);
  assert.match(source, /Do not call `search_issues`/);
  assert.match(source, /target\/\.github\/dependabot\.md/);
  assert.match(source, /Repository guidance/);
  assert.match(source, /Dependabot repository-access gap/);
  assert.match(source, /### Apply in this order/);
  assert.match(source, /### Security and access boundaries/);
  assert.match(source, /### Comment response/);
  assert.match(source, /call `issue_read` for its comments/);
  assert.match(source, /List only open issues in `SAFE_OUTPUT_REPO` without requiring labels/);
  assert.match(source, /Do not list, search, match, or reuse closed issues/);
  assert.match(source, /never let a closed parent prevent this creation/i);
  assert.match(source, /not all live targets allow this workflow to create missing labels/);
  assert.match(source, /`npm outdated --json`/);
  assert.match(source, /Routine package-manager results do not replace security evidence/);
  assert.match(source, /checking out `target_repo` proves only repository contents access/);
  assert.match(source, /complete pre-agent alert-list response/);
  assert.match(source, /missing alert access as a blocker/);
  assert.match(source, /require `TARGET_REPO` to equal `\/tmp\/gh-aw\/agent\/control-precompute\.json\.target_repo`/);
  assert.match(source, /derive the call's `owner` and `repo` arguments from that validated `TARGET_REPO`/);
  assert.match(source, /Never default these calls to `github\.repository`, `SAFE_OUTPUT_REPO`, or the current checkout/);
  assert.match(source, /Use `SAFE_OUTPUT_REPO` only for planning issue discovery and reporting/);
  assert.match(source, /Their absence does not make the inventory incomplete/);
  assert.match(source, /do not run install, update, audit-fix, or lifecycle scripts/);
  assert.doesNotMatch(source, /fallback inventory/);
  assert.match(source, /fetched before agent execution with the target-scoped read credential/);
  assert.match(source, /Never create, update, push to, comment on, or otherwise mutate a pull request/);
});

test("workers with title prefixes provide unprefixed safe-output titles", () => {
  for (const name of readdirSync(workflowsDirectory).filter((entry) => entry.endsWith(".md"))) {
    const source = workflow(name);
    if (!/^\s+role: worker$/m.test(source) || !/^\s+title-prefix:/m.test(source)) continue;

    assert.match(source, /unprefixed/, name);
    assert.match(source, /configured `title-prefix`/, name);
    assert.match(source, /added automatically/, name);
    assert.match(source, /semantically equivalent category prefix/, name);
  }
});

test("review bundles persist redacted patch evidence before publishing", () => {
  const reviewBundle = workflow("shared/review-bundle.md");
  const config = parse(/^---\n([\s\S]*?)\n---/.exec(reviewBundle)[1]);
  assert.deepEqual(config["safe-outputs"].jobs["publish-review-bundle"].artifacts, [
    "/tmp/gh-aw/agent/review-bundles/",
  ]);
  for (const name of ["repo-assist-maintenance", "repo-assist-issue-fix", "repo-assist-pr-upkeep", "bug-bash-investigation"]) {
    const agent = parse(workflow(`${name}.lock.yml`)).jobs.agent;
    const redactionIndex = agent.steps.findIndex((step) => step.name === "Redact secrets in logs");
    const uploadIndex = agent.steps.findIndex((step) => step.name === "Upload agent artifacts");
    const upload = agent.steps[uploadIndex];
    assert.ok(redactionIndex >= 0 && uploadIndex > redactionIndex, name);
    assert.equal(upload.if, "always() && steps.redact_secrets.outcome == 'success'", name);
    for (const extension of [".md", ".txt", ".patch"]) {
      assert.ok(upload.with.path.includes(`/tmp/gh-aw/agent/review-bundles/**/*${extension}`), `${name}: ${extension}`);
    }
  }
  const missingBundleBranch = /if \[ ! -d "\$SOURCE_DIR" \]; then[\s\S]*?^\s+fi$/m.exec(reviewBundle)?.[0];
  const uploadStep = stepBlock(reviewBundle, "Upload review bundle artifact");

  assert.ok(missingBundleBranch, "missing review bundle branch must exist");
  assert.match(missingBundleBranch, /::error::Requested review bundle was not persisted in the agent artifact/);
  assert.doesNotMatch(missingBundleBranch, /skip_upload=true/);
  assert.match(missingBundleBranch, /exit 1/);
  assert.match(uploadStep, /if: steps\.prepare\.outputs\.skip_upload != 'true'/);
});

test("review bundle preparation fails on missing requested evidence but permits a genuine no-op", (t) => {
  const directory = mkdtempSync(join(tmpdir(), "cao-review-bundle-"));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const config = parse(/^---\n([\s\S]*?)\n---/.exec(workflow("shared/review-bundle.md"))[1]);
  const prepare = config["safe-outputs"].jobs["publish-review-bundle"].steps[0].run;
  const output = join(directory, "agent_output.json");
  const githubOutput = join(directory, "github-output");
  const request = {
    type: "publish_review_bundle",
    bundle_name: "maintenance",
    source_dir: "/tmp/gh-aw/agent/review-bundles/maintenance",
    target_repo: "github/gh-aw",
    requested_output: "create-pull-request",
    base_sha: "a".repeat(40),
  };
  const runPrepare = (items) => {
    writeFileSync(output, JSON.stringify({ items }));
    writeFileSync(githubOutput, "");
    return spawnSync("bash", ["-c", `
realpath() { "$NODE" -e 'console.log(require("node:path").resolve(process.argv[1]))' "$2"; }
${prepare}`], {
      encoding: "utf8",
      env: {
        ...process.env,
        NODE: process.execPath,
        GH_AW_AGENT_OUTPUT: output,
        GITHUB_OUTPUT: githubOutput,
        RUNNER_TEMP: join(directory, "runner"),
        GITHUB_WORKFLOW: "Maintenance",
        GITHUB_RUN_ID: "123",
        GITHUB_RUN_ATTEMPT: "1",
        GITHUB_SERVER_URL: "https://github.com",
        GITHUB_REPOSITORY: "acme/control",
        GH_AW_SAFE_OUTPUTS_STAGED: "false",
      },
    });
  };
  const missing = runPrepare([request]);
  assert.equal(missing.status, 1, missing.stderr);
  assert.match(missing.stderr, /::error::Requested review bundle was not persisted/);
  assert.doesNotMatch(readFileSync(githubOutput, "utf8"), /skip_upload=true/);

  const noop = runPrepare([]);
  assert.equal(noop.status, 0, noop.stderr);
  assert.match(readFileSync(githubOutput, "utf8"), /skip_upload=true/);

  const restored = join(directory, "agent", "review-bundles", "maintenance");
  mkdirSync(restored, { recursive: true });
  for (const file of ["summary.md", "changed-files.txt", "validation.txt", "changes.patch"]) {
    writeFileSync(join(restored, file), `Evidence in ${file}\n`);
  }
  const published = runPrepare([request]);
  assert.equal(published.status, 0, published.stderr);
  const preparedOutput = readFileSync(githubOutput, "utf8");
  assert.match(preparedOutput, /skip_upload=false/);
  const staging = /^bundle_path=(.+)$/m.exec(preparedOutput)[1];
  for (const file of ["summary.md", "changed-files.txt", "validation.txt", "changes.patch"]) {
    assert.equal(readFileSync(join(staging, file), "utf8"), `Evidence in ${file}\n`);
  }
  assert.equal(JSON.parse(readFileSync(join(staging, "bundle-manifest.json"), "utf8")).base_sha, request.base_sha);
});

test("workers inherit human-first progressive report disclosure", () => {
  const campaignSkill = portableSkill("create-cao-campaign");
  const sharedControl = workflow("shared/control.md");
  const workers = readdirSync(workflowsDirectory)
    .filter((name) => name.endsWith(".md"))
    .map((name) => [name, workflow(name)])
    .filter(([, source]) => /^\s+role: worker$/m.test(source));

  assert.match(campaignSkill, /when `safe-outputs\.create-issue` or `safe-outputs\.create-pull-request` is enabled, require every created issue or pull request body to follow the complete Worker Report Formatting contract/);
  assert.match(campaignSkill, /mandatory for every worker that creates issues, pull requests, comments, or reviews and applies to the complete output body/);
  assert.match(campaignSkill, /Make the report delightful to read, precise, terse, and easy to scan/);
  assert.match(campaignSkill, /Use plain language, short sentences, compact bullets, and descriptive labels/);
  assert.match(campaignSkill, /Keep the entire visible report to a single screen at normal GitHub desktop viewing/);
  assert.match(campaignSkill, /Show only the decision essentials; move everything else into progressive disclosure/);
  assert.match(campaignSkill, /Start directly with a concise executive-summary paragraph/);
  assert.match(campaignSkill, /Do not add a heading before this opening paragraph because the first paragraph is always the executive summary/);
  assert.match(campaignSkill, /After the opening paragraph, use `###` for every heading; never use `#`, `##`, or `####` and deeper headings/);
  assert.doesNotMatch(campaignSkill, /Start with the h3 heading `### Summary`/);
  assert.match(campaignSkill, /states what happened, the decision-relevant result, critical findings, and key metrics/);
  assert.match(campaignSkill, /Immediately follow the summary with one clear `\*\*Action:\*\*` sentence naming who should do what next and the acceptance check/);
  assert.match(campaignSkill, /non-essential background, verbose evidence, logs, secondary metrics, per-item breakdowns, and every Markdown table in clearly named `<details><summary><b>\.\.\.<\/b><\/summary>/);
  assert.match(campaignSkill, /every Markdown table in clearly named `<details>/);
  assert.match(campaignSkill, /`\> \[!NOTE\]` for neutral status/);
  assert.match(campaignSkill, /`\> \[!WARNING\]` for warnings/);
  assert.match(campaignSkill, /`\> \[!CAUTION\]` for high-risk or blocking findings/);
  assert.match(campaignSkill, /Do not use emoji severity markers/);
  assert.match(sharedControl, /Begin directly with a short, plain-language executive summary/);
  assert.match(sharedControl, /do not add a heading for this opening summary/);
  assert.match(sharedControl, /Immediately follow it with one visible `\*\*Action:\*\*` sentence that says who should do what next and the acceptance check/);
  assert.match(sharedControl, /tell the maintainer to assign the issue to Copilot/);
  assert.match(sharedControl, /<details><summary><b>Agent prompt<\/b><\/summary>/);
  assert.match(sharedControl, /when no action is required, say `\*\*Action:\*\* None\.`/);
  assert.doesNotMatch(sharedControl, /### Executive Summary/);
  assert.match(sharedControl, /non-essential background, verbose supporting evidence, logs, secondary metrics, and per-item breakdowns inside clearly named `<details>/);
  assert.match(sharedControl, /In comments and reviews, use `###` for every heading; never use `#`, `##`, or `####` and deeper headings/);
  assert.match(sharedControl, /Put every Markdown table inside a clearly named `<details>` element/);
  assert.ok(workers.length > 0, "expected at least one worker workflow");
  for (const [name] of workers) {
    const generated = workflow(name.replace(/\.md$/, ".lock.yml"));
    assert.match(generated, /Begin directly with a short, plain-language executive summary/, name);
    assert.match(generated, /do not add a heading for this opening summary/, name);
    assert.match(generated, /Immediately follow it with one visible `\*\*Action:\*\*` sentence/, name);
    assert.match(generated, /tell the maintainer to assign the issue to Copilot/, name);
    assert.match(generated, /<details><summary><b>Agent prompt<\/b><\/summary>/, name);
    assert.doesNotMatch(generated, /### Executive Summary/, name);
    assert.match(generated, /non-essential background, verbose supporting evidence, logs, secondary metrics, and per-item breakdowns inside clearly named `<details>/, name);
  }
});

test("comment-producing safe outputs expose one formatting contract", () => {
  const commentOutputs = new Set([
    "add-comment",
    "create-pull-request-review-comment",
    "submit-pull-request-review",
  ]);
  const producers = [];

  for (const name of readdirSync(workflowsDirectory).filter((entry) => entry.endsWith(".md"))) {
    const source = workflow(name);
    const frontmatter = /^---\n([\s\S]*?)\n---/.exec(source)?.[1];
    if (!frontmatter) continue;
    const config = parse(frontmatter);
    const outputs = Object.keys(config["safe-outputs"] ?? {}).filter((output) => commentOutputs.has(output));
    if (outputs.length === 0) continue;
    producers.push([name, outputs.toSorted()]);

    const importsSharedControl = config.imports?.some((entry) => entry.uses === "shared/control.md");
    const formattingContract = importsSharedControl ? workflow("shared/control.md") : source;
    assert.match(formattingContract, /use `###` for every heading/);
    assert.match(formattingContract, /never use `#`, `##`, or `####` and deeper headings/);
    assert.match(formattingContract, /every Markdown table inside a clearly named `<details>` element/);
  }

  assert.deepEqual(producers.toSorted(([left], [right]) => left.localeCompare(right)), [
    ["cao-evolution-catalog-advisor.md", ["add-comment"]],
    ["cao-evolution-efficiency.md", ["add-comment"]],
    ["cao-evolution-integrity.md", ["add-comment"]],
    ["cao-evolution-reliability.md", ["add-comment"]],
    ["dependabot-update-planner.md", ["add-comment"]],
    ["design-decision-gate.md", ["add-comment"]],
    ["mattpocock-skills-reviewer.md", [
      "create-pull-request-review-comment",
      "submit-pull-request-review",
    ]],
    ["pr-reviewer.md", [
      "create-pull-request-review-comment",
      "submit-pull-request-review",
    ]],
    ["pr-sous-chef.md", ["add-comment"]],
    ["repo-assist-issue-triage.md", ["add-comment"]],
  ]);
});

test("repository PR automation remains bounded and adapted to CAO", () => {
  const finisher = readFileSync(join(root, ".github", "skills", "pr-finisher", "SKILL.md"), "utf8");
  const sousChef = workflow("pr-sous-chef.md");
  const mattReviewer = workflow("mattpocock-skills-reviewer.md");
  const decisionGate = workflow("design-decision-gate.md");

  assert.match(finisher, /npm run check/);
  assert.match(finisher, /npm run compile:locks/);
  assert.doesNotMatch(finisher, /\bmake (?:fmt|lint|test|recompile)\b/);

  assert.match(sousChef, /push-to-pull-request-branch:/);
  assert.match(sousChef, /required-labels: \[sous-chef\]/);
  assert.match(sousChef, /is:pr is:open -is:draft label:sous-chef/);
  assert.match(sousChef, /bash:\n\s+- "\*"/);
  assert.match(sousChef, /npm ci/);
  assert.match(sousChef, /browsers: \[chrome, chromium\]/);
  assert.match(sousChef, /Chrome for Testing/);
  assert.match(sousChef, /if and only if the pushed commit modifies one or more `\.lock\.yml` files/);
  assert.doesNotMatch(sousChef, /mention `@copilot`/);
  assert.match(sousChef, /fromJSON\(github\.event\.inputs\.aw_context \|\| '\{\}'\)\.item_number/);

  assert.equal([...mattReviewer.matchAll(/mattpocock\/skills\/[\w-]+@[0-9a-f]{40}/g)].length, 5);
  assert.match(mattReviewer, /emitted < 3000/);
  assert.doesNotMatch(mattReviewer, /\|\s*head -n 3000/);
  assert.match(mattReviewer, /fromJSON\(github\.event\.inputs\.aw_context \|\| '\{\}'\)\.item_number/);
  assert.match(mattReviewer, /reaction: none/);
  assert.match(mattReviewer, /slash_command:[\s\S]*?\n\s+name: matt\n\s+strategy: centralized/);
  assert.doesNotMatch(mattReviewer, /\n\s+pull_request:/);

  assert.match(decisionGate, /slash_command:\n\s+strategy: centralized\n\s+name: design-gate/);
  assert.doesNotMatch(decisionGate, /\n\s+(?:pull_request|workflow_dispatch):/);
  assert.match(decisionGate, /allowed-files:\n\s+- "adr\/\*\*"/);
  for (const section of ["Context", "Decision", "Alternatives Considered", "Consequences"]) {
    assert.match(decisionGate, new RegExp(`\`${section}\``));
  }
  assert.match(decisionGate, /Not inferable from current pull request evidence/);
});
