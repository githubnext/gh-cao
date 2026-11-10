import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { existsSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";
import test from "node:test";
import { parse } from "yaml";
import {
  compileAndCompare,
  doctorFindings,
  formatValidationReport,
  validateEnablement,
  validatePolicy,
  validateRepository,
  validateSecurity,
} from "../../activity/validation.mjs";

const version = "v0.89.22";
const metadata = `# gh-aw-metadata: {"compiler_version":"${version}"}\n`;

async function fixture({ lock = metadata, policyCampaign = true, declaration = true, source = true } = {}) {
  const root = await mkdtemp(path.join(os.tmpdir(), "cao-validation-test-"));
  await mkdir(path.join(root, ".github", "workflows"), { recursive: true });
  if (source) await writeFile(path.join(root, ".github", "workflows", "sample.md"), "---\nname: Sample\non: workflow_dispatch\n---\n");
  if (lock !== null) await writeFile(path.join(root, ".github", "workflows", "sample.lock.yml"), lock);
  if (declaration) {
    await mkdir(path.join(root, "sample"), { recursive: true });
    await writeFile(path.join(root, "sample", "cao.json"), JSON.stringify({
      campaign: "sample",
      orchestrator: "sample",
      workers: { worker: "sample-worker" },
    }));
    await writeFile(path.join(root, ".github", "workflows", "sample-worker.md"), "---\nname: Worker\non: workflow_dispatch\n---\n");
    await writeFile(path.join(root, ".github", "workflows", "sample-worker.lock.yml"), metadata);
  }
  const policy = {
    version: 1,
    "gh-aw-version": version,
    "control-plane": {
      campaigns: policyCampaign ? {
        sample: { workers: { worker: { workflow: "sample-worker" } } },
      } : {},
    },
  };
  await writeFile(path.join(root, ".github", "workflows", "cao.json"), JSON.stringify(policy));
  return root;
}

function compiler({ status = 0, output, calls = [] } = {}) {
  return (command, args, options) => {
    calls.push({ command, args });
    if (command === "git") {
      return { status: 0, stdout: "", stderr: "" };
    }
    if (command === "gh" && args[0] === "aw" && args[1] === "compile") {
      if (output) output(args, options);
      return { status, stdout: "", stderr: status ? "invalid workflow trigger" : "" };
    }
    throw new Error(`Unexpected command: ${command} ${args.join(" ")}`);
  };
}

test("Activity Value configuration belongs to the installed root runtime, not an operational campaign", async () => {
  const root = await fixture({ declaration: false, policyCampaign: false });
  try {
    const policy = JSON.parse(await readFile(path.join(root, ".github/workflows/cao.json"), "utf8"));
    policy["control-plane"].campaigns.activity = { "workflow-values": [] };
    let result = await validatePolicy(root, JSON.stringify(policy));
    assert.ok(result.findings.some(finding => finding.id === "policy-campaign-not-installed"));
    await writeFile(path.join(root, ".github/workflows/cao-activity.yml"), "name: CAO Activity\non: workflow_dispatch\n");
    result = await validatePolicy(root, JSON.stringify(policy));
    assert.deepEqual(result.findings, []);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test("strict compiler accepts a clean generated workflow set", async () => {
  const root = await fixture({ declaration: false });
  try {
    const calls = [];
    assert.deepEqual(await compileAndCompare(root, compiler({ calls }), version), []);
    assert.deepEqual(calls.filter(({ command }) => command === "git").map(({ args }) => args), [
      ["init", "--quiet"],
      ["add", "--all"],
      [
        "-c", "user.name=CAO Validator",
        "-c", "user.email=cao-validator@localhost",
        "commit", "--quiet", "--no-gpg-sign", "-m", "CAO validation snapshot",
      ],
    ]);
    assert.deepEqual(calls.find(({ command }) => command === "gh").args, [
      "aw", "compile", "--strict", "--validate", "--show-all", "--no-check-update",
      "--schedule-seed", "githubnext/gh-cao", "--dir", ".github/workflows",
    ]);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test("copies repository agent definitions into the isolated compiler checkout", async () => {
  const root = await fixture({ declaration: false });
  try {
    await mkdir(path.join(root, ".github", "agents"), { recursive: true });
    await writeFile(path.join(root, ".github", "agents", "reviewer.md"), "---\nname: Reviewer\n---\n");
    await mkdir(path.join(root, ".claude", "agents"), { recursive: true });
    await writeFile(path.join(root, ".claude", "agents", "helper.md"), "---\nname: Helper\n---\n");
    const copied = [];
    const findings = await compileAndCompare(root, compiler({
      output(args, options) {
        for (const file of [".github/agents/reviewer.md", ".claude/agents/helper.md", ".codex/agents"]) {
          if (existsSync(path.join(options.cwd, file))) copied.push(file);
        }
      },
    }), version);
    assert.deepEqual(findings, []);
    assert.deepEqual(copied, [".github/agents/reviewer.md", ".claude/agents/helper.md"]);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test("detects orphaned generated workflows with and without workflow sources", async (t) => {
  for (const source of [false, true]) {
    await t.test(`workflow sources present: ${source}`, async () => {
      const root = await fixture({ declaration: false, policyCampaign: false, source, lock: null });
      try {
        await writeFile(path.join(root, ".github", "workflows", "orphan.lock.yml"), metadata);
        const calls = [];
        const findings = await compileAndCompare(root, compiler({
          calls,
          output(args, options) {
            writeFileSync(path.join(options.cwd, ".github", "workflows", "sample.lock.yml"), metadata);
          },
        }), version);
        const orphan = findings.find(({ id }) => id === "orphaned-generated-workflow");
        assert.equal(orphan?.severity, "error");
        assert.deepEqual(orphan.files, [".github/workflows/orphan.lock.yml"]);
        assert.equal(calls.some(({ command }) => command === "gh"), source);
      } finally {
        await rm(root, { recursive: true, force: true });
      }
    });
  }
});

test("still compiles existing Markdown sources without a frontmatter opener", async () => {
  const root = await fixture({ declaration: false, policyCampaign: false, lock: null });
  try {
    await writeFile(path.join(root, ".github", "workflows", "sample.md"), "# Missing frontmatter\n");
    const calls = [];
    const findings = await compileAndCompare(root, (command, args, options) => {
      if (command === "gh") {
        calls.push({ command, args });
        return { status: 1, stdout: "", stderr: "no workflow markdown files found (workflow files must start with a frontmatter opener on the first line)" };
      }
      return compiler({ calls })(command, args, options);
    }, version);
    assert.equal(calls.some(({ command }) => command === "gh"), true);
    assert.equal(findings[0].id, "invalid-frontmatter");
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test("normalizes compiler errors", async () => {
  const root = await fixture({ declaration: false });
  try {
    const findings = await compileAndCompare(root, compiler({ status: 1 }), version);
    assert.equal(findings[0].id, "invalid-trigger");
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test("detects missing generated workflows", async () => {
  const root = await fixture({ declaration: false, lock: null });
  try {
    const findings = await compileAndCompare(root, compiler({
      output(args, options) {
        writeFileSync(path.join(options.cwd, ".github", "workflows", "sample.lock.yml"), metadata);
      },
    }), version);
    assert.equal(findings[0].id, "missing-generated-workflow");
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test("detects stale generated workflows", async () => {
  const root = await fixture({ declaration: false });
  try {
    const findings = await compileAndCompare(root, compiler({
      output(args, options) {
        writeFileSync(path.join(options.cwd, ".github", "workflows", "sample.lock.yml"), `${metadata}changed\n`);
      },
    }), version);
    assert.equal(findings[0].id, "stale-generated-workflow");
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test("detects generated workflows from another compiler version", async () => {
  const root = await fixture({ declaration: false, lock: '# gh-aw-metadata: {"compiler_version":"v0.1.0"}\n' });
  try {
    const findings = await compileAndCompare(root, compiler(), version);
    assert.equal(findings[0].id, "unexpected-compiler-version");
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test("production policy validation rejects invalid policy", async () => {
  const root = await fixture({ declaration: false });
  try {
    const { findings } = await validatePolicy(root, '{"version":2}');
    assert.equal(findings[0].id, "invalid-cao-policy");
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test("detects installed campaigns absent from policy", async () => {
  const root = await fixture({ policyCampaign: false });
  try {
    const source = await readFile(path.join(root, ".github", "workflows", "cao.json"), "utf8");
    const { findings } = await validatePolicy(root, source);
    assert.equal(findings.some(({ id }) => id === "installed-campaign-absent-from-policy"), true);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test("validates standalone policy without campaign declarations and diagnoses missing sources", async () => {
  const root = await fixture({ policyCampaign: false, declaration: false });
  try {
    const policy = JSON.parse(await readFile(path.join(root, ".github", "workflows", "cao.json"), "utf8"));
    delete policy["control-plane"].campaigns;
    policy["control-plane"].workflows = { sample: { mode: "review" } };
    const valid = await validatePolicy(root, JSON.stringify(policy));
    assert.deepEqual(valid.findings, []);
    assert.deepEqual(valid.installed, []);
    policy["control-plane"].workflows.missing = {};
    const missing = await validatePolicy(root, JSON.stringify(policy));
    assert.equal(missing.findings.length, 1);
    assert.equal(missing.findings[0].id, "missing-standalone-workflow");
    assert.deepEqual(missing.findings[0].files, [
      ".github/workflows/cao.json", ".github/workflows/missing.md",
    ]);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test("validates direct operation policy and diagnoses missing operation sources", async () => {
  const root = await fixture({ policyCampaign: false, declaration: false });
  try {
    const policy = JSON.parse(await readFile(path.join(root, ".github", "workflows", "cao.json"), "utf8"));
    delete policy["control-plane"].campaigns;
    policy["control-plane"].operations = { sample: { enabled: false, mode: "review" } };
    const valid = await validatePolicy(root, JSON.stringify(policy));
    assert.deepEqual(valid.findings, []);
    assert.deepEqual(valid.installed, []);
    policy["control-plane"].operations.missing = {};
    const missing = await validatePolicy(root, JSON.stringify(policy));
    assert.equal(missing.findings.length, 1);
    assert.equal(missing.findings[0].id, "missing-operation-workflow");
    assert.match(missing.findings[0].title, /direct operation/);
    assert.deepEqual(missing.findings[0].files, [
      ".github/workflows/cao.json", ".github/workflows/missing.md",
    ]);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test("detects campaign declarations with a missing worker workflow", async () => {
  const root = await fixture();
  try {
    await rm(path.join(root, ".github", "workflows", "sample-worker.md"));
    const source = await readFile(path.join(root, ".github", "workflows", "cao.json"), "utf8");
    const { findings } = await validatePolicy(root, source);
    assert.equal(findings.some(({ id }) => id === "missing-workflow"), true);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test("detects partially disabled campaigns from one workflow registry call", () => {
  let calls = 0;
  const execute = () => {
    calls += 1;
    return {
      status: 0,
      stdout: JSON.stringify([
        { path: ".github/workflows/sample.lock.yml", state: "active" },
        { path: ".github/workflows/sample-worker.lock.yml", state: "disabled_manually" },
      ]),
      stderr: "",
    };
  };
  const findings = validateEnablement(".", execute, [{
    campaign: "sample", orchestrator: "sample", workers: { worker: "sample-worker" },
  }]);
  assert.equal(calls, 1);
  assert.equal(findings[0].id, "campaign-partially-disabled");
});

test("preserves unavailable GitHub enablement state as unknown", () => {
  const findings = validateEnablement(".", () => ({ status: 1, stdout: "", stderr: "no auth" }), []);
  assert.equal(findings[0].id, "github-enablement-state-unavailable");
  assert.equal(findings[0].severity, "warning");
});

test("reports a manually paused orchestrator with active workers as a note", () => {
  const findings = validateEnablement(".", () => ({
    status: 0,
    stdout: JSON.stringify([
      { path: ".github/workflows/sample.lock.yml", state: "disabled_manually" },
      { path: ".github/workflows/sample-worker.lock.yml", state: "active" },
    ]),
    stderr: "",
  }), [{
    campaign: "sample", orchestrator: "sample", workers: { worker: "sample-worker" },
  }]);
  assert.equal(findings.length, 1);
  assert.equal(findings[0].id, "campaign-orchestrator-paused");
  assert.equal(findings[0].severity, "note");
  assert.doesNotMatch(findings[0].remediation, /\.\/cao\.sh enable/);
  assert.match(findings[0].remediation, /resumes its schedule/);
});

test("does not treat unavailable or inactive workers as an intentional pause", () => {
  for (const workerState of ["unknown", "disabled_inactivity", "disabled_manually"]) {
    const findings = validateEnablement(".", () => ({
      status: 0,
      stdout: JSON.stringify([
        { path: ".github/workflows/sample.lock.yml", state: "disabled_manually" },
        { path: ".github/workflows/sample-worker.lock.yml", state: workerState },
      ]),
      stderr: "",
    }), [{
      campaign: "sample", orchestrator: "sample", workers: { worker: "sample-worker" },
    }]);
    assert.ok(!findings.some(({ id }) => id === "campaign-orchestrator-paused"), workerState);
  }
});

test("detects bounded static security findings", async () => {
  const root = await fixture({ declaration: false });
  try {
    await writeFile(path.join(root, ".github", "workflows", "unsafe.yml"), [
      "on:", "  pull_request_target:", "jobs:", "  test:", "    steps:",
      "      - uses: someone/action@v1", "",
    ].join("\n"));
    const findings = await validateSecurity(root, "{}");
    assert.deepEqual(findings.map(({ id }) => id), [
      "unsafe-pull-request-target",
      "unpinned-third-party-action",
    ]);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test("allows only the reviewed protected-main publisher exception", async () => {
  const root = await fixture({ declaration: false });
  try {
    await writeFile(path.join(root, ".github", "workflows", "package.yml"), [
      "jobs:",
      "  publish:",
      "    uses: githubnext/gh-cao/.github/workflows/cao-package-publish.yml@main # zizmor: ignore[unpinned-uses] protected default-branch publisher",
      "  unreviewed:",
      "    uses: githubnext/gh-cao/.github/workflows/cao-package-publish.yml@main",
      "  other:",
      "    uses: githubnext/gh-cao/.github/workflows/other.yml@main",
      "  external:",
      "    steps:",
      "      - uses: someone/action@main",
      "",
    ].join("\n"));
    const findings = await validateSecurity(root, "{}");
    assert.deepEqual(findings.map(({ observed }) => observed), [
      "githubnext/gh-cao/.github/workflows/cao-package-publish.yml@main",
      "githubnext/gh-cao/.github/workflows/other.yml@main",
      "someone/action@main",
    ]);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test("detects worker dispatch and orchestrator mutation capabilities", async () => {
  const root = await fixture();
  try {
    await writeFile(path.join(root, ".github", "workflows", "sample.md"), [
      "---", "safe-outputs:", "  create-issue:", "---", "",
    ].join("\n"));
    await writeFile(path.join(root, ".github", "workflows", "sample-worker.md"), [
      "---", "safe-outputs:", "  dispatch-workflow:", "---", "",
    ].join("\n"));
    const findings = await validateSecurity(root, "{}", [{
      campaign: "sample", orchestrator: "sample", workers: { worker: "sample-worker" },
    }]);
    assert.deepEqual(findings.map(({ id }) => id), [
      "orchestrator-can-mutate-target",
      "worker-can-dispatch-workflow",
    ]);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test("doctor failures are warnings", () => {
  const execute = (command) => command === "git"
    ? { status: 0, stdout: "https://github.com/acme/control.git\n", stderr: "" }
    : { status: 1, stdout: "", stderr: "not authenticated" };
  assert.equal(doctorFindings(".", execute)[0].severity, "warning");
});

test("CI validation uses the installed runtime without consumer npm metadata", async () => {
  const workflow = parse(await readFile(".github/workflows/cao-validate.yml", "utf8"));
  const runtime = JSON.parse(await readFile("runtime/package.json", "utf8"));
  const steps = workflow.jobs.validate.steps;
  const setupNode = steps.find(({ uses }) => uses?.startsWith("actions/setup-node@"));
  const validation = steps.find(({ id }) => id === "validation");

  assert.equal(workflow["run-name"], "CAO validation / ${{ github.run_id }}");
  assert.equal(setupNode.with["node-version"], 24);
  assert.equal(setupNode.with["package-manager-cache"], false);
  assert.equal(setupNode.with.cache, undefined);
  assert.ok(!steps.some(({ run }) => /\bnpm (?:ci|install)\b/.test(run ?? "")));
  assert.ok(!steps.some(({ uses }) => uses?.startsWith("actions/setup-go@")));
  assert.ok(!steps.some(({ run }) => /\bgo build\b|\bgh release download\b/.test(run ?? "")));
  assert.match(validation.run, /node activity\/cao\.mjs validate --json/);
  assert.doesNotMatch(validation.run, /cao\.sh|gh-cao/);
  assert.ok(runtime.files.includes("activity/validation.mjs"));
  assert.ok(runtime.files.includes("activity/commands"));
  for (const event of [workflow.on.pull_request, workflow.on.push]) {
    assert.ok(event.paths.includes("activity/validation.mjs"));
    assert.ok(event.paths.includes("runtime/package.json"));
  }
});

test("the JavaScript runtime exposes structured validation without a consumer package", async (t) => {
  const root = await mkdtemp(path.join(os.tmpdir(), "cao-validation-runtime-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const entrypoint = fileURLToPath(new URL("../../activity/cao.mjs", import.meta.url));
  const result = spawnSync(process.execPath, [entrypoint, "validate", "--json"], {
    cwd: root,
    encoding: "utf8",
  });
  assert.equal(result.status, 1, result.stderr || result.error?.message);
  const report = JSON.parse(result.stdout);
  assert.equal(report.validatorVersion, "1");
  assert.equal(report.exitCode, 1);
  assert.equal(report.findings[0].id, "missing-cao-policy");
  assert.equal(report.findings[0].remediation, "gh cao init");
  assert.equal(report.findings[0].validate, "gh workflow run cao-validate.yml");
});

test("CI validation does not dirty a fresh checkout with its own report", async (t) => {
  const temporaryRoot = await mkdtemp(path.join(os.tmpdir(), "cao-validation-workflow-"));
  t.after(() => rm(temporaryRoot, { recursive: true, force: true }));
  const root = path.join(temporaryRoot, "checkout");
  const runnerTemp = path.join(temporaryRoot, "runner temp");
  await mkdir(root);
  await mkdir(runnerTemp);
  const workflow = parse(await readFile(".github/workflows/cao-validate.yml", "utf8"));
  const validation = workflow.jobs.validate.steps.find(({ id }) => id === "validation");
  const upload = workflow.jobs.validate.steps.find(({ name }) => name === "Upload validation report");
  const moduleUrl = new URL("../../activity/validation.mjs", import.meta.url).href;
  await mkdir(path.join(root, "activity"));
  await writeFile(path.join(root, "activity", "cao.mjs"), `
import { spawnSync } from "node:child_process";
import { doctorFindings } from ${JSON.stringify(moduleUrl)};
const findings = doctorFindings(process.cwd(), (command, args, options) => {
  if (command === "gh" && args[0] === "aw" && args[1] === "doctor") {
    const status = spawnSync("git", ["status", "--porcelain"], options);
    if (status.status !== 0) return status;
    return status.stdout
      ? { status: 1, stdout: "", stderr: "working directory has uncommitted changes, please commit or stash them first" }
      : { status: 0, stdout: "{}", stderr: "" };
  }
  return spawnSync(command, args, options);
});
console.log(JSON.stringify({
  validatorVersion: "1",
  findings,
  summary: { errors: 0, warnings: findings.length, notes: 0 },
}));
`);
  const execute = (command, args, extra = {}) => {
    const result = spawnSync(command, args, { cwd: root, encoding: "utf8", ...extra });
    assert.equal(result.status, 0, result.stderr || result.error?.message);
    return result.stdout;
  };
  execute("git", ["init", "--quiet"]);
  execute("git", ["remote", "add", "origin", "https://github.com/acme/control.git"]);
  execute("git", ["add", "--all"]);
  execute("git", [
    "-c", "user.name=CAO Test", "-c", "user.email=cao-test@localhost",
    "commit", "--quiet", "--no-gpg-sign", "-m", "Clean validation fixture",
  ]);
  assert.equal(execute("git", ["status", "--porcelain"]), "");
  const output = path.join(runnerTemp, "output");
  execute("bash", ["-e", "-c", validation.run], {
    env: { ...process.env, RUNNER_TEMP: runnerTemp, GITHUB_OUTPUT: output, GITHUB_REPOSITORY: "acme/control" },
  });
  const reportPath = path.resolve(root, upload.with.path.replace("${{ runner.temp }}", runnerTemp));
  const report = JSON.parse(await readFile(reportPath, "utf8"));
  assert.deepEqual(report.summary, { errors: 0, warnings: 0, notes: 0 });
  assert.deepEqual(report.findings, []);
  assert.equal(await readFile(output, "utf8"), "exit-code=0\n");
  assert.equal(execute("git", ["status", "--porcelain"]), "");
  assert.equal(reportPath, path.join(runnerTemp, "cao-validation.json"));

  await writeFile(path.join(root, "uncommitted.txt"), "A real checkout change\n");
  execute("bash", ["-e", "-c", validation.run], {
    env: { ...process.env, RUNNER_TEMP: runnerTemp, GITHUB_OUTPUT: output, GITHUB_REPOSITORY: "acme/control" },
  });
  const changedReport = JSON.parse(await readFile(reportPath, "utf8"));
  assert.equal(changedReport.summary.warnings, 1);
  assert.equal(changedReport.findings[0].id, "doctor-check-failed");
  assert.match(changedReport.findings[0].observed, /working directory has uncommitted changes/);
});

function cleanExecutor() {
  return (command, args) => {
    if (command === "git") {
      return { status: 0, stdout: "", stderr: "" };
    }
    if (command === "gh" && args.join(" ") === "aw version") {
      return { status: 0, stdout: "", stderr: `gh aw version ${version}\n` };
    }
    if (command === "gh" && args[0] === "aw" && args[1] === "compile") {
      return { status: 0, stdout: "", stderr: "" };
    }
    if (command === "gh" && args[0] === "aw" && args[1] === "doctor") {
      return { status: 0, stdout: "{}", stderr: "" };
    }
    if (command === "gh" && args[0] === "workflow") {
      return {
        status: 0,
        stdout: JSON.stringify([
          { path: ".github/workflows/sample.lock.yml", state: "active" },
          { path: ".github/workflows/sample-worker.lock.yml", state: "active" },
        ]),
        stderr: "",
      };
    }
    throw new Error(`Unexpected command: ${command} ${args.join(" ")}`);
  };
}

test("monitoring-only repositories skip compilation but retain all other checks", async (t) => {
  const root = await fixture({ declaration: false, policyCampaign: false, source: false, lock: null });
  t.after(() => rm(root, { recursive: true, force: true }));
  const workflowRoot = path.join(root, ".github", "workflows");
  for (const name of ["cao-activity", "cao-dashboard", "cao-validate"]) {
    await writeFile(path.join(workflowRoot, `${name}.yml`), "on: workflow_dispatch\njobs: {}\n");
  }
  await mkdir(path.join(workflowRoot, "shared"));
  await writeFile(path.join(workflowRoot, "shared", "control.md"), "# Shared import, not a workflow\n");
  const calls = [];
  const clean = await validateRepository({
    root,
    execute(command, args, options) {
      calls.push({ command, args });
      if (command === "gh" && args[1] === "compile") {
        return { status: 1, stdout: "", stderr: "no workflow markdown files found (frontmatter required)" };
      }
      return cleanExecutor()(command, args, options);
    },
  });
  assert.equal(clean.exitCode, 0);
  assert.deepEqual(clean.summary, { errors: 0, warnings: 0, notes: 0 });
  assert.equal(calls.some(({ command, args }) => command === "gh" && args[1] === "compile"), false);
  for (const prefix of ["aw version", "aw doctor", "workflow list"]) {
    assert.ok(calls.some(({ command, args }) => command === "gh" && args.join(" ").startsWith(prefix)), prefix);
  }

  for (const [prefix, result, id, severity] of [
    ["aw version", { status: 0, stdout: "v0.1.0" }, "wrong-gh-aw-compiler-version", "error"],
    ["aw version", { status: 1, stderr: "not installed" }, "gh-aw-unavailable", "error"],
    ["aw doctor", { status: 1, stderr: "setup unavailable" }, "doctor-check-failed", "warning"],
    ["workflow list", { status: 1, stderr: "offline" }, "github-enablement-state-unavailable", "warning"],
  ]) {
    const report = await validateRepository({
      root,
      execute(command, args, options) {
        if (command === "gh" && args.join(" ").startsWith(prefix)) return result;
        return cleanExecutor()(command, args, options);
      },
      strictWarnings: true,
    });
    assert.equal(report.exitCode, 1, id);
    assert.ok(report.findings.some((entry) => entry.id === id && entry.severity === severity), id);
  }

  await writeFile(path.join(workflowRoot, "cao-activity.yml"), "on:\n  pull_request_target:\n");
  const unsafe = await validateRepository({ root, execute: cleanExecutor() });
  assert.equal(unsafe.exitCode, 1);
  assert.ok(unsafe.findings.some(({ id }) => id === "unsafe-pull-request-target"));

  await writeFile(path.join(workflowRoot, "cao.json"), '{"version":2}');
  const invalid = await validateRepository({ root, execute: cleanExecutor() });
  assert.equal(invalid.exitCode, 1);
  assert.ok(invalid.findings.some(({ id }) => id === "invalid-cao-policy"));
});

test("clean repositories and warnings-only repositories exit successfully", async () => {
  const root = await fixture();
  try {
    const clean = await validateRepository({ root, execute: cleanExecutor(), now: () => new Date(0) });
    assert.equal(clean.exitCode, 0);
    assert.deepEqual(clean.summary, { errors: 0, warnings: 0, notes: 0 });
    const warning = await validateRepository({
      root,
      execute(command, args, options) {
        if (command === "gh" && args[0] === "workflow") return { status: 1, stdout: "", stderr: "offline" };
        return cleanExecutor()(command, args, options);
      },
      now: () => new Date(0),
    });
    assert.equal(warning.exitCode, 0);
    assert.equal(warning.summary.warnings, 1);
    const strict = await validateRepository({
      root,
      execute(command, args, options) {
        if (command === "gh" && args[0] === "workflow") return { status: 1, stdout: "", stderr: "offline" };
        return cleanExecutor()(command, args, options);
      },
      strictWarnings: true,
    });
    assert.equal(strict.exitCode, 1);
    assert.deepEqual(strict.summary, { errors: 0, warnings: 1, notes: 0 });
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});

test("validation errors exit 1 and internal failures exit 2", async () => {
  const root = await fixture();
  const missing = await mkdtemp(path.join(os.tmpdir(), "cao-validation-missing-"));
  try {
    const wrongVersion = await validateRepository({
      root,
      execute(command, args, options) {
        if (command === "gh" && args.join(" ") === "aw version") {
          return { status: 0, stdout: "v0.1.0", stderr: "" };
        }
        return cleanExecutor()(command, args, options);
      },
    });
    assert.equal(wrongVersion.exitCode, 1);
    assert.equal(wrongVersion.findings.some(({ id }) => id === "wrong-gh-aw-compiler-version"), true);
    const missingPolicy = await validateRepository({ root: missing, execute: cleanExecutor() });
    assert.equal(missingPolicy.exitCode, 1);
    assert.equal(missingPolicy.findings[0].id, "missing-cao-policy");
    const internalFailure = await validateRepository({
      root,
      execute() {
        throw new Error("executor failed");
      },
    });
    assert.equal(internalFailure.exitCode, 2);
    assert.equal(internalFailure.findings.at(-1).id, "validator-failure");
  } finally {
    await rm(root, { recursive: true, force: true });
    await rm(missing, { recursive: true, force: true });
  }
});

test("human and JSON frontends represent the same report", async () => {
  const root = await fixture();
  try {
    const report = await validateRepository({ root, execute: cleanExecutor(), now: () => new Date(0) });
    const human = formatValidationReport(report);
    const json = JSON.parse(JSON.stringify(report));
    assert.match(human, /CAO validation/);
    assert.equal(json.summary.errors, report.summary.errors);
    assert.deepEqual(json.findings, report.findings);
  } finally {
    await rm(root, { recursive: true, force: true });
  }
});
