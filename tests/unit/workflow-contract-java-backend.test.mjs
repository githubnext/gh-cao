import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import test from "node:test";
import { parse } from "yaml";
import { controlPolicy, root, workflow } from "./workflow-contract.helpers.mjs";

const source = workflow("java-backend-reimplementation.md");
const config = parse(/^---\n([\s\S]*?)\n---/.exec(source)[1]);
const compiled = parse(workflow("java-backend-reimplementation.lock.yml"));

test("Java reimplementation is a daily repository-local experiment, not fleet authority", () => {
  assert.equal(config.private, true);
  assert.equal(config.on.schedule, "daily");
  assert.equal(config.on.workflow_dispatch, null);
  assert.equal(config.imports, undefined);
  assert.equal(config.on["skip-if-match"], "is:pr is:open label:java-backend-experiment");
  assert.equal(config["tracker-id"], "java-backend-reimplementation");
  assert.equal(config.engine, "copilot");
  assert.equal(config.strict, true);
  assert.equal(config["timeout-minutes"], 60);
  assert.equal(config["max-ai-credits"], 600);
  assert.equal(config["max-daily-ai-credits"], 600);
  assert.deepEqual(config.permissions, {
    contents: "read", "pull-requests": "read", "copilot-requests": "write",
  });
  assert.deepEqual(config.tools.github, { mode: "gh-proxy", toolsets: ["repos", "pull_requests"] });
  assert.equal(config.concurrency.group, "${{ github.workflow }}");
  assert.equal(config.concurrency["cancel-in-progress"], false);
  assert.equal(controlPolicy["control-plane"].workflows["java-backend-reimplementation"], undefined);
  assert.equal(controlPolicy["control-plane"].campaigns["java-backend-reimplementation"], undefined);
  assert.doesNotMatch(readFileSync(join(root, "aw.yml"), "utf8"), /java-backend/);
  assert.equal(compiled.on.schedule.length, 1);
  assert.match(compiled.on.schedule[0].cron, /^\d+ \d+ \* \* \*$/);
  assert.equal(compiled.on.workflow_dispatch.inputs.aw_context.type, "string");
});

test("Java proposals are bounded draft PRs restricted to the experiment directory", () => {
  const outputs = config["safe-outputs"];
  const proposal = outputs["create-pull-request"];
  assert.deepEqual(proposal["allowed-files"], ["experiments/java-backend/**"]);
  assert.equal(proposal.draft, true);
  assert.equal(proposal.max, 1);
  assert.equal(proposal.expires, "14d");
  assert.equal(proposal["max-patch-files"], 30);
  assert.equal(proposal["max-patch-size"], 150);
  assert.equal(proposal["protected-files"], "request-review");
  assert.equal(proposal["fallback-as-issue"], false);
  assert.equal(proposal["target-repo"], undefined);
  assert.deepEqual(proposal.labels, ["java-backend-experiment", "ai-generated"]);
  assert.equal(outputs["concurrency-group"], "${{ github.workflow }}-outputs");
  assert.equal(outputs.noop["report-as-issue"], false);
  assert.equal(outputs.mentions, false);
  for (const forbidden of ["create-issue", "push-to-pull-request-branch", "dispatch-workflow", "merge-pull-request"]) {
    assert.equal(outputs[forbidden], undefined);
  }
  const handler = compiled.jobs.safe_outputs.steps.find(step => step.env?.GH_AW_SAFE_OUTPUTS_HANDLER_CONFIG);
  assert.ok(handler, "the publishing restrictions must reach the production handler");
  const handlers = JSON.parse(handler.env.GH_AW_SAFE_OUTPUTS_HANDLER_CONFIG);
  assert.deepEqual(handlers.create_pull_request.allowed_files, proposal["allowed-files"]);
  assert.equal(handlers.create_pull_request.draft, true);
  assert.equal(handlers.create_pull_request.max, 1);
  assert.equal(handlers.create_pull_request.protected_files_policy, "request-review");
  assert.ok(handlers.create_pull_request.protected_files.includes("pom.xml"),
    "Maven bootstrapping must be reviewable, not blocked as a protected manifest");
  assert.equal(handlers.create_pull_request.fallback_as_issue, false);
  assert.equal(compiled.jobs.safe_outputs.concurrency.group, "${{ github.workflow }}-outputs");
});

test("Java toolchain and durable evidence support real implementation attempts", () => {
  const setup = config["pre-agent-steps"][0];
  assert.match(setup.uses, /^actions\/setup-java@[0-9a-f]{40}$/);
  assert.deepEqual(setup.with, { distribution: "temurin", "java-version": "21" });
  assert.match(config["pre-agent-steps"][1].run, /java -version\nmvn --version/);
  assert.ok(config.network.allowed.includes("java"));
  const domains = compiled.jobs.agent.steps.find(step => step.env?.GH_AW_ALLOWED_DOMAINS)?.env.GH_AW_ALLOWED_DOMAINS;
  assert.ok(domains.split(",").includes("repo.maven.apache.org"));
  const artifact = config["safe-outputs"]["upload-artifact"];
  assert.equal(artifact["max-uploads"], 1);
  assert.equal(artifact["retention-days"], 14);
  assert.deepEqual(artifact["allowed-paths"], ["/tmp/gh-aw/agent/java-backend-reimplementation/**"]);
  const steps = compiled.jobs.agent.steps;
  assert.ok(steps.some(step => step.uses === setup.uses));
  assert.ok(steps.some(step => step.name === "Verify the Java build toolchain"));
  assert.match(source, /mvn --batch-mode --no-transfer-progress -f experiments\/java-backend\/pom\.xml verify/);
  assert.match(source, /before editing when it exists/);
  assert.match(source, /positive and negative tests/);
  assert.match(source, /Failed, skipped or unavailable checks are not passing evidence/);
  assert.match(source, /do not create a pull request; preserve the\nfailure evidence and call `report_incomplete`/);
  assert.match(source, /summary\.md`, `validation\.txt` and\n`changes\.patch`/);
  assert.match(source, /git diff --cached --binary/);
  assert.match(source, /Failure evidence belongs in that artifact too/);
});

test("Java mission is spec-driven, incremental, independent and never deploys", () => {
  for (const contract of [
    "specs/control-architecture.md", "specs/dashboard-data.md", "specs/dashboard.md",
    "server/spec/generated/openapi.json", "docs/dashboard-language-specification.md",
  ]) {
    assert.ok(source.includes(contract), contract);
  }
  assert.match(source, /git rev-parse HEAD/);
  assert.match(source, /Only the checked-out default-branch experiment\nis accepted implementation progress/);
  assert.match(source, /extend the accepted implementation\ninstead of restarting from scratch/);
  assert.match(source, /not wrap or shell out to the Go\nserver, Node\.js query engine or browser IndexedDB/);
  assert.match(source, /Never deploy, provision cloud resources, publish images or packages, push branches/);
  assert.match(source, /Do not call live GitHub mutation APIs/);
  assert.match(source, /Use PostgreSQL for hosted canonical storage/);
  assert.match(source, /non-root execution/);
  assert.match(source, /numeric loopback only/);
  assert.match(source, /section-level coverage map/);
  assert.match(source, /implementation-status\.md/);
  assert.match(source, /never auto-merge or replace production code/);
  assert.match(readFileSync(join(root, "docs/operation-patterns.md"), "utf8"),
    /schedule takes effect only\nafter the compiled workflow is merged/);
});
