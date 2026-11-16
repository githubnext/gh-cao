import assert from "node:assert/strict";
import { existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { parse } from "yaml";

import {
  DEFAULT_HYPOTHESIS,
  HARNESS_VERSION,
  HYPOTHESIS_ID,
  HYPOTHESIS_VERSION,
  INVESTIGATION_OUTCOMES,
  RESOURCE_ENVELOPE,
  REVIEW_BUNDLE_FILES,
  STARTER_TARGET,
  VERIFICATION_GATES,
  classifyOutcome,
  findingIdentity,
  investigationBudget,
  retryPlan,
  reviewBundleManifest,
  runCommand,
  skipDecision,
  validateReviewBundle,
  workIdentity,
} from "../../bug-bash/investigation-contract.mjs";
import { controlPolicy, root, workflow } from "./workflow-contract.helpers.mjs";

// Bug Bash campaign contracts.
//
// The campaign's promise is "find a bug you can reproduce, review a fix you can
// understand". These tests hold the two halves of that promise apart: a failing
// generated test must never be reported as a defect, and passing tests must
// never be reported as a correct repair.

const campaignDirectory = join(root, "bug-bash");
const orchestrator = workflow("bug-bash.md");
const investigation = workflow("bug-bash-investigation.md");
const investigationLock = workflow("bug-bash-investigation.lock.yml");
const orchestratorLock = workflow("bug-bash.lock.yml");

function frontmatter(source) {
  const block = /^---\r?\n([\s\S]*?)\r?\n---/.exec(source)?.[1];
  assert.ok(block, "workflow must have frontmatter");
  return parse(block);
}

function reproducedSignals(overrides = {}) {
  return {
    modelAccess: true,
    setupSucceeded: true,
    contractSupportsDefect: true,
    candidateFound: true,
    replayedInFreshEnvironment: true,
    reproducedOnSourceRevision: true,
    failureClass: "behavior",
    ...overrides,
  };
}

function reviewReadySignals(overrides = {}) {
  return reproducedSignals({
    testPatch: true,
    productionPatch: true,
    patchResolvesReproduction: true,
    applicableTestsPass: true,
    boundaryChallenged: true,
    testsWeakened: false,
    ...overrides,
  });
}

function completeBundle(overrides = {}) {
  return {
    "summary.md": `Bug Bash found one reproducible defect in ${STARTER_TARGET.repository}.`,
    "contract.md": "The documented contract requires sorted output.",
    "reproduction.md": `Replay at ${STARTER_TARGET.revision} with the recorded command.`,
    "tests.patch": "--- a/test.mjs\n+++ b/test.mjs\n+assert.deepEqual(sorted, expected);\n",
    "production.patch": "--- a/source.mjs\n+++ b/source.mjs\n-return values;\n+return values.toSorted();\n",
    "validation.txt": "node --test tests/unit/example.test.mjs -> pass",
    "verification.json": JSON.stringify({ gates: VERIFICATION_GATES, failureClass: "behavior" }),
    ...overrides,
  };
}

// 1. Installation and package completeness.

test("bug bash installs as a complete, self-describing campaign package", () => {
  const manifest = parse(readFileSync(join(campaignDirectory, "aw.yml"), "utf8"));
  const declaration = JSON.parse(readFileSync(join(campaignDirectory, "cao.json"), "utf8"));

  assert.equal(manifest.name, "Bug Bash");
  assert.equal(manifest.experimental, true);
  assert.equal(manifest.private ?? false, false, "the getting-started campaign must stay publicly installable");
  assert.equal(manifest["min-version"], controlPolicy["gh-aw-version"]);
  assert.match(
    manifest.description,
    /Find a bug you can reproduce\. Review a fix you can understand\./,
    "the manifest must carry the campaign promise",
  );

  assert.equal(declaration.campaign, "bug-bash");
  assert.equal(declaration.orchestrator, "bug-bash");
  assert.deepEqual(declaration.workers, { investigation: "bug-bash-investigation" });

  assert.deepEqual(manifest.includes.toSorted(), [
    ".github/workflows/bug-bash.md",
    ".github/workflows/bug-bash-investigation.md",
  ].sort());
  assert.ok(!manifest.includes.includes("../aw.yml"), "cao add must not reinstall the control plane");
  for (const include of manifest.includes) {
    const source = join(root, include);
    assert.ok(existsSync(source), `manifest include must exist: ${include}`);
  }
  for (const resource of [
    "README.md",
    "cao.json",
    "intelligence.json",
    "investigation-contract.mjs",
    "fixtures/historical-pilot.json",
  ]) {
    assert.ok(existsSync(join(campaignDirectory, resource)), `materialized campaign resource must exist: ${resource}`);
  }

  const readme = readFileSync(join(campaignDirectory, "README.md"), "utf8");
  assert.match(readme, /Find a bug you can reproduce\. Review a fix you can understand\./);
  assert.match(readme, /gh cao add githubnext\/gh-cao\/bug-bash/);
  assert.doesNotMatch(
    readme,
    /^gh aw add githubnext\/gh-cao\/bug-bash/m,
    "the README must not prescribe a direct gh-aw install as a complete CAO installation",
  );
  assert.match(readme, /no additional GitHub credentials/i);
  assert.match(readme, /is not the same as "free"|not the same as .free./i);
  assert.match(readme, /canonical starter is intended to produce a reproducible explicit-zero finding/i);
  assert.match(readme, /`no-finding` remains a valid and expected outcome/);
  assert.equal(STARTER_TARGET.repository, "githubnext/gh-aw-cao-bug-bash");
  assert.equal(STARTER_TARGET.revision, "49cfd2afb66bd6a07b8e6d18a21ccacc4871a4e9");
  assert.ok(STARTER_TARGET.focusPaths.includes("examples/paused-rollout.json"));
  assert.ok(STARTER_TARGET.focusPaths.includes("src/demo.mjs"));
  assert.deepEqual(STARTER_TARGET.hypothesis, DEFAULT_HYPOTHESIS);
});

test("bug bash remains available while campaign onboarding is optional after monitoring", () => {
  const quickstarts = readFileSync(join(root, "docs", "setup-quickstarts.md"), "utf8");
  const catalogIndex = readFileSync(join(root, "docs", "pages", "catalog", "index.astro"), "utf8");
  const authentication = readFileSync(join(root, "docs", "authentication.md"), "utf8");

  assert.match(quickstarts, /## Optional: add automation later/);
  assert.match(quickstarts, /\[Repo Assist walkthrough\]\(repo-assist-quickstart\.md\)/);
  assert.doesNotMatch(quickstarts, /recommended getting-started campaign[\s\S]*Bug Bash/);
  assert.match(catalogIndex, /catalog\/repo-assist/);
  assert.match(catalogIndex, /New to CAO\? Try/);
  assert.match(catalogIndex, /none of its proposals is a requirement for using CAO/);
  assert.match(
    authentication,
    /It does not remove \*model\* cost or entitlement/,
    "authentication docs must separate GitHub credentials from model entitlement",
  );
  assert.match(authentication, /Treat missing model access as a blocked run with its own reason/);
});

// 2. Admission denial.

test("neither bug bash workflow runs without CAO admission", () => {
  for (const [name, source] of [["bug-bash.md", orchestrator], ["bug-bash-investigation.md", investigation]]) {
    assert.match(source, /^if: needs\.pre_activation\.outputs\.cao_authorized == 'true'$/m, name);
    assert.match(source, /cao_authorized: \$\{\{ steps\.cao_admission\.outputs\.authorized == 'true'/, name);
  }

  const policy = controlPolicy["control-plane"].campaigns["bug-bash"];
  assert.equal(policy.mode, "review", "bug bash must be registered review-only");
  assert.equal(policy["max-repositories"], 1);
  assert.equal(policy.workers.investigation["max-mode"], "review", "the worker must be review-capped");
  assert.equal(policy.workers.investigation.workflow, "bug-bash-investigation");
  assert.deepEqual(policy.targets, {
    "githubnext/gh-aw-wizard": { mode: "review" },
    "githubnext/gh-aw-workshop": { mode: "review" },
  }, "bug bash enrollment must not grant live mode");
  assert.equal(
    controlPolicy["target-authority"]?.campaigns?.["bug-bash"],
    undefined,
    "a review-only campaign must not claim live target authority",
  );

  assert.equal(
    classifyOutcome({ modelAccess: true, setupSucceeded: false }).outcome,
    "blocked",
    "a denied or unusable run must never be reported as an absence of bugs",
  );
  assert.equal(retryPlan(1, { reason: "admission-denied" }).retry, false);
});

test("registering bug bash does not broaden unrelated campaigns", () => {
  const campaigns = controlPolicy["control-plane"].campaigns;

  assert.equal(campaigns.dreaming.mode, "review");
  assert.equal(campaigns.dreaming["max-repositories"], 1);
  assert.equal(campaigns.dependabot.mode, "review");
  assert.deepEqual(Object.keys(campaigns["bug-bash"]).toSorted(), ["icon", "max-repositories", "mode", "targets", "workers"]);
});

// 3. Bounded dispatch.

test("the orchestrator dispatches at most one bounded investigation", () => {
  const config = frontmatter(orchestrator);

  assert.equal(config["max-ai-credits"], 200);
  assert.equal(config["timeout-minutes"], 15);
  assert.equal(config["safe-outputs"]["dispatch-workflow"].max, 1);
  assert.deepEqual(config["safe-outputs"]["dispatch-workflow"].workflows, ["bug-bash-investigation"]);
  assert.equal(config.on.workflow_dispatch.inputs.max_repos.default, 1);
  assert.deepEqual(config.on.workflow_dispatch.inputs.safe_output_mode.options, ["review"]);
  assert.equal(config.on.schedule, undefined, "the getting-started campaign must stay manual by default");
  assert.doesNotMatch(orchestratorLock, /^\s+schedule:$/m, "no compiled schedule may be emitted");

  const control = config.imports.find((entry) => entry.uses === "shared/control.md");
  assert.equal(control.with.dispatch_max, 1);
  assert.equal(control.with.role, "orchestrator");
  assert.equal(control.with.worker_credits_per_target, RESOURCE_ENVELOPE.workerCredits);

  assert.equal(RESOURCE_ENVELOPE.maxInvestigationsPerRun, 1);
  assert.equal(RESOURCE_ENVELOPE.maxConcurrentInvestigations, 1);
  assert.equal(RESOURCE_ENVELOPE.maxSurfacedFindings, 1);
  assert.deepEqual(
    skipDecision({ workId: "work:second", runningWorkIds: ["work:first"] }),
    { skip: true, reason: "concurrency-limit" },
  );
});

test("the worker cannot write to the investigated repository", () => {
  const config = frontmatter(investigation);

  assert.deepEqual(Object.keys(config["safe-outputs"]).toSorted(), ["create-issue", "mentions"]);
  assert.equal(config["safe-outputs"]["create-issue"].max, 1);
  assert.equal(
    config["safe-outputs"]["create-issue"]["target-repo"],
    "${{ inputs.safe_output_repo || github.repository }}",
    "findings must land in the control repository regardless of resolved mode",
  );
  assert.equal(config["safe-outputs"]["create-pull-request"], undefined);
  assert.equal(config["safe-outputs"]["push-to-pull-request-branch"], undefined);
  assert.equal(config["safe-outputs"]["add-comment"], undefined);
  assert.doesNotMatch(investigation, /^\s+(contents|actions|issues|pull-requests): write$/m);
  assert.doesNotMatch(investigationLock, /create_pull_request/);
  assert.match(investigation, /Never write to `TARGET_REPO`/);
  assert.match(investigation, /no merge/);
});

// 4. Missing model access.

test("missing model access is blocked, terminal, and never a no-finding", () => {
  const decision = classifyOutcome(reproducedSignals({ modelAccess: false }));

  assert.deepEqual(
    { outcome: decision.outcome, reason: decision.reason, surfaced: decision.surfaced },
    { outcome: "blocked", reason: "model-access-unavailable", surfaced: false },
  );
  assert.equal(retryPlan(1, { reason: "model-access-unavailable" }).retry, false);
  assert.match(retryPlan(1, { reason: "model-access-unavailable" }).reason, /^terminal:/);
  assert.match(investigation, /model-access-unavailable/);
  assert.match(investigation, /copilot-requests: write/);
});

// 5. Setup failure.

test("a setup failure is reported as blocked rather than as an absence of bugs", () => {
  const decision = classifyOutcome(reproducedSignals({ setupSucceeded: false }));

  assert.deepEqual(
    { outcome: decision.outcome, reason: decision.reason },
    { outcome: "blocked", reason: "environment-setup-failed" },
  );
  assert.equal(
    classifyOutcome(reproducedSignals({ failureClass: "setup" })).outcome,
    "inconclusive",
    "a failure caused by the harness is never a defect",
  );
  assert.equal(
    classifyOutcome(reproducedSignals({ failureClass: "setup" })).failedGate,
    "behaviorClassifiedFailure",
  );
  assert.match(investigation, /A baseline that cannot be made green is a setup failure, not a defect/);
});

// 6. Valid no-finding.

test("a completed investigation with no contract violation reports no-finding", () => {
  const decision = classifyOutcome(reproducedSignals({ contractSupportsDefect: false }));

  assert.deepEqual(
    { outcome: decision.outcome, reason: decision.reason, publishReviewBundle: decision.publishReviewBundle },
    { outcome: "no-finding", reason: "behavior-matches-stated-contract", publishReviewBundle: false },
  );
  assert.equal(classifyOutcome(reproducedSignals({ candidateFound: false })).outcome, "no-finding");
  assert.equal(classifyOutcome(reproducedSignals({ candidateFound: false })).reason, "no-candidate-within-budget");
  assert.deepEqual(INVESTIGATION_OUTCOMES, [
    "no-finding",
    "blocked",
    "inconclusive",
    "reproduced-candidate",
    "review-ready",
  ]);
  assert.match(investigation, /An honest `no-finding` keeps that promise/);
});

// 7. Reproducible defect.

test("a reproducible defect without a verified repair stops at reproduced-candidate", () => {
  const decision = classifyOutcome(reproducedSignals({ testPatch: true, productionPatch: false }));

  assert.deepEqual(
    { outcome: decision.outcome, reason: decision.reason, failedGate: decision.failedGate },
    { outcome: "reproduced-candidate", reason: "missing-production-patch", failedGate: "productionPatch" },
  );
  assert.equal(decision.surfaced, false, "an unverified repair must not be surfaced as review-ready");

  const verified = classifyOutcome(reviewReadySignals());
  assert.equal(verified.outcome, "review-ready");
  assert.equal(verified.publishReviewBundle, true);

  assert.equal(
    classifyOutcome(reproducedSignals({ replayedInFreshEnvironment: false })).outcome,
    "inconclusive",
    "a failing generated test alone is not a reproduced defect",
  );
  assert.equal(
    classifyOutcome(reproducedSignals({ reproducedOnSourceRevision: false })).failedGate,
    "reproducedOnSourceRevision",
  );
  assert.match(investigation, /A failing generated test is not a bug/);
});

// 8. Rejected behavioral assumption.

test("a rejected behavioral assumption never becomes a defect", () => {
  const decision = classifyOutcome(reviewReadySignals({ contractSupportsDefect: false }));

  assert.equal(decision.outcome, "no-finding");
  assert.equal(decision.failedGate, "contractSupportsDefect");
  assert.equal(decision.surfaced, false);
  assert.ok(
    VERIFICATION_GATES.includes("contractSupportsDefect") && VERIFICATION_GATES.includes("boundaryChallenged"),
    "verification must challenge both the contract and the boundary",
  );
  assert.equal(
    classifyOutcome(reviewReadySignals({ boundaryChallenged: false })).outcome,
    "reproduced-candidate",
  );
  assert.match(investigation, /Undocumented, ambiguous, or intentionally-permissive behavior fails this gate/);
  assert.match(investigation, /at least one alternative explanation was considered and rejected with evidence/);
});

// 9. Incorrect repair.

test("passing tests alone never certify a repair", () => {
  assert.equal(
    classifyOutcome(reviewReadySignals({ patchResolvesReproduction: false })).outcome,
    "reproduced-candidate",
  );
  assert.equal(
    classifyOutcome(reviewReadySignals({ patchResolvesReproduction: false })).reason,
    "patch-does-not-resolve-reproduction",
  );
  assert.equal(classifyOutcome(reviewReadySignals({ applicableTestsPass: false })).outcome, "reproduced-candidate");

  const weakened = classifyOutcome(reviewReadySignals({ testsWeakened: true }));
  assert.equal(weakened.outcome, "reproduced-candidate");
  assert.equal(weakened.reason, "weakened-or-suppressed-tests");
  assert.equal(weakened.failedGate, "noWeakenedTests");

  const bundle = completeBundle({ "production.patch": completeBundle()["tests.patch"] });
  const manifest = reviewBundleManifest(bundle, {
    workId: "work:example",
    findingId: "finding:example",
    decision: { outcome: "review-ready", reason: "verified-reproduction-and-repair" },
    uncertainty: "The fix is narrow.",
  });
  assert.deepEqual(
    validateReviewBundle(bundle, manifest).problems,
    ["test-and-production-patches-not-separated"],
  );
  assert.match(investigation, /Passing tests do not prove a repair is correct/);
});

// 10. Duplicate execution.

test("repeat investigations are suppressed by stable work and finding identity", () => {
  const first = workIdentity({ repository: STARTER_TARGET.repository, revision: STARTER_TARGET.revision });
  const repeat = workIdentity({
    repository: STARTER_TARGET.repository,
    revision: STARTER_TARGET.revision,
    hypothesisId: HYPOTHESIS_ID,
    hypothesisVersion: HYPOTHESIS_VERSION,
    harnessVersion: HARNESS_VERSION,
  });
  const newHarness = workIdentity({
    repository: STARTER_TARGET.repository,
    revision: STARTER_TARGET.revision,
    harnessVersion: "2.0.0",
  });

  assert.equal(first.id, repeat.id, "the same question on the same revision is the same work");
  assert.equal(first.hypothesisId, HYPOTHESIS_ID);
  assert.equal(first.hypothesisVersion, HYPOTHESIS_VERSION);
  assert.notEqual(first.id, newHarness.id, "a new harness is new work");
  assert.throws(() => workIdentity({ repository: "acme/widgets", revision: "main" }), /40-character commit SHA/);

  const defect = {
    repository: STARTER_TARGET.repository,
    contract: "an explicit zero rollout percentage pauses deployments",
    symptom: "zero rollout percentage selects every service",
    entryPoint: "buildRolloutPlan",
  };
  assert.equal(
    findingIdentity(defect).id,
    findingIdentity({ ...defect }).id,
    "finding identity must be stable across repeat investigations",
  );
  assert.equal(
    findingIdentity({ ...defect, repository: "acme/widgets" }).id !== findingIdentity(defect).id,
    true,
  );

  assert.deepEqual(
    skipDecision({ workId: first.id, completedWorkIds: [first.id] }),
    { skip: true, reason: "already-investigated" },
  );
  assert.deepEqual(
    skipDecision({ workId: first.id, findingId: findingIdentity(defect).id, openFindingIds: [findingIdentity(defect).id] }),
    { skip: true, reason: "duplicate-finding" },
  );
  assert.deepEqual(skipDecision({ workId: first.id }), { skip: false, reason: "eligible" });
  assert.equal(classifyOutcome({ duplicate: true }).outcome, "no-finding");
  assert.equal(classifyOutcome({ duplicate: true }).reason, "duplicate-finding-suppressed");
  assert.equal(frontmatter(investigation)["safe-outputs"]["create-issue"]["deduplicate-by-title"], true);
});

test("retry and budget keep a repeat run bounded and reserve verification", () => {
  assert.deepEqual(retryPlan(1, {}), { retry: true, attempt: 1, delaySeconds: 15, reason: "transient-fault" });
  assert.deepEqual(retryPlan(2, {}), { retry: true, attempt: 2, delaySeconds: 60, reason: "transient-fault" });
  assert.equal(retryPlan(RESOURCE_ENVELOPE.maxAttempts, {}).retry, false);
  assert.equal(retryPlan(1, { reason: "credits-exhausted" }).retry, false);
  assert.equal(retryPlan(1, { reason: "rate-limited" }).retry, false);
  assert.throws(() => retryPlan(0, {}), /positive integer/);

  const budget = investigationBudget();
  assert.equal(budget.investigationCredits + budget.verificationCredits, RESOURCE_ENVELOPE.workerCredits);
  assert.equal(budget.investigationMinutes + budget.verificationMinutes, RESOURCE_ENVELOPE.timeoutMinutes);
  assert.ok(budget.verificationCredits > 0 && budget.verificationCredits < budget.investigationCredits);
  assert.throws(
    () => investigationBudget({ workerCredits: 100, verificationReserveCredits: 100 }),
    /positive minority/,
  );

  const config = frontmatter(investigation);
  assert.equal(config["max-ai-credits"], RESOURCE_ENVELOPE.workerCredits);
  assert.equal(config["timeout-minutes"], RESOURCE_ENVELOPE.timeoutMinutes);
  assert.equal(config.concurrency["cancel-in-progress"], true);
});

// 11. Review-bundle publication.

test("only a review-ready outcome publishes a complete, sealed review bundle", () => {
  const bundle = completeBundle();
  const record = {
    workId: workIdentity({ repository: STARTER_TARGET.repository, revision: STARTER_TARGET.revision }).id,
    findingId: "finding:example",
    decision: { outcome: "review-ready", reason: "verified-reproduction-and-repair" },
    uncertainty: "Only the documented boundary was exercised.",
  };
  const manifest = reviewBundleManifest(bundle, record);

  assert.deepEqual(REVIEW_BUNDLE_FILES, [
    "summary.md",
    "contract.md",
    "reproduction.md",
    "tests.patch",
    "production.patch",
    "validation.txt",
    "verification.json",
  ]);
  assert.deepEqual(Object.keys(manifest.digests).toSorted(), [...REVIEW_BUNDLE_FILES].toSorted());
  for (const value of Object.values(manifest.digests)) assert.match(value, /^[0-9a-f]{64}$/);
  assert.equal(validateReviewBundle(bundle, manifest).valid, true);

  const { "reproduction.md": _omitted, ...incomplete } = bundle;
  assert.deepEqual(validateReviewBundle(incomplete, manifest).missing, ["reproduction.md"]);
  assert.deepEqual(
    validateReviewBundle({ ...bundle, "summary.md": "tampered" }, manifest).corrupted,
    ["summary.md"],
  );
  assert.deepEqual(
    validateReviewBundle(bundle, reviewBundleManifest(bundle, { ...record, uncertainty: "" })).problems,
    ["missing-uncertainty"],
  );
  assert.deepEqual(
    validateReviewBundle(
      { ...bundle, "reproduction.md": "replay on the default branch" },
      reviewBundleManifest({ ...bundle, "reproduction.md": "replay on the default branch" }, record),
    ).problems,
    ["missing-source-revision"],
  );

  assert.ok(frontmatter(investigation).imports.some((entry) => entry.uses === "shared/review-bundle.md"));
  assert.match(investigationLock, /publish_review_bundle/);
  assert.match(investigation, /Publish a review bundle only when the classifier returns `review-ready`/);
  assert.match(investigation, /Stands alone\./);
});

test("investigation issues present the decision before machine metadata", () => {
  const finding = investigation.indexOf("`## Finding`");
  const action = investigation.indexOf("`## Maintainer action`");
  const fix = investigation.indexOf("`## Proposed fix`");
  const verification = investigation.indexOf("`## Verification`");
  const metadata = investigation.indexOf("<details><summary>Investigation metadata</summary>");

  assert.ok(finding > 0);
  assert.ok(action > finding);
  assert.ok(fix > action);
  assert.ok(verification > fix);
  assert.ok(metadata > verification);
  assert.match(investigation, /operational consequence on its own `\*\*Impact:\*\*` line/);
  assert.match(investigation, /checklist with no more than three concrete steps/);
  assert.match(investigation, /`\*\*Acceptance check:\*\*` line/);
  assert.match(investigation, /Markdown table with `Check` and `Result` columns/);
  assert.match(investigation, /GitHub note containing `\*\*Uncertainty:\*\*`/);
  assert.match(investigation, /machine-oriented identifiers here instead of in the opening summary/);
});

test("the sealing command writes digests and rejects an incomplete bundle", () => {
  const directory = mkdtempSync(join(tmpdir(), "bug-bash-bundle-"));
  try {
    const bundle = completeBundle();
    for (const [name, contents] of Object.entries(bundle)) writeFileSync(join(directory, name), contents);
    const recordPath = join(directory, "record.json");
    writeFileSync(recordPath, JSON.stringify({
      workId: "work:example",
      findingId: "finding:example",
      decision: { outcome: "review-ready", reason: "verified-reproduction-and-repair" },
      uncertainty: "Only the documented boundary was exercised.",
    }));

    const io = {
      readFile(path, options = {}) {
        try {
          return readFileSync(path, "utf8");
        } catch (error) {
          if (options.optional) return undefined;
          throw error;
        }
      },
      writeFile(path, data) {
        writeFileSync(path, data);
      },
    };

    const sealed = runCommand(["seal", directory, recordPath], io);
    assert.equal(sealed.status, 0);
    const contract = JSON.parse(readFileSync(join(directory, "bundle-contract.json"), "utf8"));
    assert.equal(contract.campaign, "bug-bash");
      assert.equal(contract.hypothesisId, HYPOTHESIS_ID);
    assert.equal(contract.outcome, "review-ready");
    assert.equal(Object.keys(contract.digests).length, REVIEW_BUNDLE_FILES.length);

    rmSync(join(directory, "production.patch"));
    assert.equal(runCommand(["seal", directory, recordPath], io).status, 1, "an incomplete bundle must fail sealing");
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});

// Pilot evidence stays labeled and never seeds a run.

test("historical pilot evidence is labeled, non-authoritative, and seeds nothing", () => {
  const fixture = JSON.parse(readFileSync(join(campaignDirectory, "fixtures", "historical-pilot.json"), "utf8"));

  assert.equal(fixture.label, "historical-pilot-evidence");
  assert.equal(fixture.authoritative, false);
  assert.equal(fixture.producedByThisHarness, false);
  assert.equal(fixture.reviewState, "maintainer-unreviewed");
  assert.equal(fixture.scope.repositoriesPinned, 3);
  assert.equal(fixture.scope.existingTestsSelected, 998);
  assert.equal(fixture.scope.candidatesSurfaced, 2);
  assert.equal(fixture.scope.candidatesConfirmedByMaintainers, 0);
  assert.equal(fixture.candidates.length, 2);
  assert.ok(fixture.candidates.every((candidate) => candidate.reviewState === "maintainer-unreviewed"));
  assert.ok(fixture.candidates.every((candidate) => candidate.confirmed === false));
  assert.deepEqual(
    fixture.candidates.map((candidate) => candidate.project).toSorted(),
    ["Boltons", "Central Agentic Ops"],
  );
  assert.ok(fixture.disclaimer.some((line) => /expected outcome of a first Bug Bash run is no-finding/.test(line)));
  assert.ok(fixture.disclaimer.some((line) => /not an expected yield, a success rate, or a commitment/.test(line)));

  const serialized = JSON.stringify(fixture);
  assert.doesNotMatch(serialized, /diff --git|\+\+\+ b\//, "pilot evidence must not carry patches");
  for (const absent of ["source files", "test patches", "production patches", "minimal reproductions"]) {
    assert.ok(fixture.excluded.fields.includes(absent), `the fixture must state that ${absent} are excluded`);
  }
  assert.match(fixture.excluded.reason, /never seed live source, reproductions, or repairs/);
});

test("campaign intelligence declares an honest, unmeasured operational value", () => {
  const intelligence = JSON.parse(readFileSync(join(campaignDirectory, "intelligence.json"), "utf8"));

  assert.equal(intelligence.campaign, "bug-bash");
  assert.equal(intelligence.fields.triggerAndSchedule.schedule, "none");
  assert.equal(intelligence.fields.triggerAndSchedule.scheduleOptIn, true);
  assert.equal(intelligence.fields.outcomeAttainmentEvidence.evaluationMode, "not-yet-measurable");
  assert.equal(intelligence.fields.outcomeAttainmentEvidence.definitionPath, null);
  assert.equal(intelligence.fields.operationalValueDefinition.state, "declared-not-measured");
  assert.ok(intelligence.fields.operationalValueDefinition.antiMetrics.length > 0);
  assert.equal(intelligence.fields.resourceEnvelope.maxSurfacedFindings, RESOURCE_ENVELOPE.maxSurfacedFindings);
  assert.ok(existsSync(join(campaignDirectory, "fixtures", "historical-pilot.json")));
});
