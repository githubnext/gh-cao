import { spawnSync } from "node:child_process";
import { cp, mkdir, mkdtemp, readFile, readdir, rm } from "node:fs/promises";
import os from "node:os";
import path from "node:path";

export const VALIDATOR_VERSION = "1";
const POLICY_PATH = ".github/workflows/cao.json";
const WORKFLOW_DIRECTORY = ".github/workflows";
// gh-aw records Markdown metadata from these repository agent directories in every generated lock file.
const AGENT_METADATA_DIRECTORIES = [".github/agents", ".agents/agents", ".claude/agents", ".codex/agents", ".gemini/agents", ".pi/agents"];
const VERSION_PATTERN = /\bv\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?\b/;
const FINDING_FIELDS = [
  "id", "severity", "category", "title", "campaign", "files",
  "expected", "observed", "remediation", "validate",
];

function finding(id, severity, category, title, details = {}) {
  return Object.fromEntries(FINDING_FIELDS.map((key) => [
    key,
    key === "files" ? (details[key] ?? []) : (details[key] ?? null),
  ]).concat([["id", id], ["severity", severity], ["category", category], ["title", title],
    ["validate", details.validate ?? "gh workflow run cao-validate.yml"]]));
}

async function filesIn(directory, suffix) {
  try {
    return (await readdir(directory, { withFileTypes: true }))
      .filter((entry) => entry.isFile() && entry.name.endsWith(suffix))
      .map((entry) => entry.name)
      .sort();
  } catch (error) {
    if (error?.code === "ENOENT") return [];
    throw error;
  }
}

function commandOutput(result) {
  return `${result.stdout ?? ""}\n${result.stderr ?? ""}`.trim();
}

function run(execute, command, args, cwd) {
  return execute(command, args, {
    cwd,
    encoding: "utf8",
    maxBuffer: 64 * 1024 * 1024,
  });
}

function installedVersion(execute, root) {
  const result = run(execute, "gh", ["aw", "version"], root);
  return {
    result,
    version: commandOutput(result).match(VERSION_PATTERN)?.[0] ?? null,
  };
}

function compilerFinding(output) {
  const normalized = output.toLowerCase();
  const id = normalized.includes("frontmatter") ? "invalid-frontmatter"
    : normalized.includes("permission") ? "invalid-permissions"
      : normalized.includes("safe-output") || normalized.includes("safe output") ? "invalid-safe-output"
        : normalized.includes("trigger") ? "invalid-trigger"
          : normalized.includes("tool") ? "invalid-tool"
            : normalized.includes("workflow") ? "invalid-workflow"
              : "compile-error";
  return finding(id, "error", "compiler", "gh-aw strict compilation failed", {
    expected: "Every agentic workflow compiles with strict full validation",
    observed: output || "gh aw compile failed without diagnostics",
    remediation: "npm run compile:locks",
  });
}

export async function compileAndCompare(root, execute, expectedVersion) {
  const findings = [];
  const workflowRoot = path.join(root, WORKFLOW_DIRECTORY);
  const sources = await filesIn(workflowRoot, ".md");
  const locks = await filesIn(workflowRoot, ".lock.yml");
  const expectedLocks = new Set(sources.map((name) => name.replace(/\.md$/, ".lock.yml")));
  for (const lock of locks.filter((name) => !expectedLocks.has(name))) {
    findings.push(finding("orphaned-generated-workflow", "error", "generated-workflows", "Generated workflow has no source", {
      files: [path.posix.join(WORKFLOW_DIRECTORY, lock)],
      expected: "Every generated workflow has a matching .md source",
      observed: "No matching workflow source exists",
      remediation: `Remove ${path.posix.join(WORKFLOW_DIRECTORY, lock)} after confirming its source was retired`,
    }));
  }
  if (sources.length === 0) return findings;

  const temporaryRoot = await mkdtemp(path.join(os.tmpdir(), "cao-validate-"));
  const temporaryWorkflows = path.join(temporaryRoot, WORKFLOW_DIRECTORY);
  try {
    await cp(workflowRoot, temporaryWorkflows, { recursive: true });
    try {
      await mkdir(path.join(temporaryRoot, ".github", "aw"), { recursive: true });
      await cp(
        path.join(root, ".github", "aw", "actions-lock.json"),
        path.join(temporaryRoot, ".github", "aw", "actions-lock.json"),
      );
    } catch (error) {
      if (error?.code !== "ENOENT") throw error;
    }
    for (const directory of AGENT_METADATA_DIRECTORIES) {
      try {
        await cp(path.join(root, directory), path.join(temporaryRoot, directory), { recursive: true });
      } catch (error) {
        if (error?.code !== "ENOENT") throw error;
      }
    }
    const initialize = run(execute, "git", ["init", "--quiet"], temporaryRoot);
    if (initialize.error || initialize.status !== 0) {
      throw new Error(`Unable to initialize isolated compiler checkout: ${commandOutput(initialize) || initialize.error?.message}`);
    }
    const stage = run(execute, "git", ["add", "--all"], temporaryRoot);
    if (stage.error || stage.status !== 0) {
      throw new Error(`Unable to stage isolated compiler checkout: ${commandOutput(stage) || stage.error?.message}`);
    }
    const commit = run(execute, "git", [
      "-c", "user.name=CAO Validator",
      "-c", "user.email=cao-validator@localhost",
      "commit", "--quiet", "--no-gpg-sign", "-m", "CAO validation snapshot",
    ], temporaryRoot);
    if (commit.error || commit.status !== 0) {
      throw new Error(`Unable to commit isolated compiler checkout: ${commandOutput(commit) || commit.error?.message}`);
    }
    const result = run(execute, "gh", [
      "aw", "compile", "--strict", "--validate", "--show-all", "--no-check-update",
      "--schedule-seed", "githubnext/gh-cao", "--dir", WORKFLOW_DIRECTORY,
    ], temporaryRoot);
    if (result.error || result.status !== 0) {
      findings.push(compilerFinding(commandOutput(result) || result.error?.message));
      return findings;
    }

    for (const lock of expectedLocks) {
      const checkedInPath = path.join(workflowRoot, lock);
      const generatedPath = path.join(temporaryWorkflows, lock);
      let checkedIn;
      let generated;
      try {
        checkedIn = await readFile(checkedInPath, "utf8");
      } catch (error) {
        if (error?.code !== "ENOENT") throw error;
        findings.push(finding("missing-generated-workflow", "error", "generated-workflows", "Generated workflow is missing", {
          files: [path.posix.join(WORKFLOW_DIRECTORY, lock)],
          expected: "A checked-in .lock.yml artifact for every workflow source",
          observed: "No generated artifact exists",
          remediation: "npm run compile:locks",
        }));
        continue;
      }
      generated = await readFile(generatedPath, "utf8");
      if (checkedIn !== generated) {
        findings.push(finding("stale-generated-workflow", "error", "generated-workflows", "Generated workflow is stale", {
          files: [path.posix.join(WORKFLOW_DIRECTORY, lock)],
          expected: "Checked-in artifact matches strict compiler output",
          observed: "Compiler output differs from the checked-in artifact",
          remediation: "npm run compile:locks",
        }));
      }
      const metadata = checkedIn.match(/^# gh-aw-metadata: (.+)$/m)?.[1];
      let compilerVersion = null;
      try {
        compilerVersion = metadata ? JSON.parse(metadata).compiler_version : null;
      } catch {
        // Report malformed metadata as a version mismatch.
      }
      if (compilerVersion !== expectedVersion) {
        findings.push(finding("unexpected-compiler-version", "error", "compiler", "Generated workflow compiler version is unexpected", {
          files: [path.posix.join(WORKFLOW_DIRECTORY, lock)],
          expected: expectedVersion,
          observed: compilerVersion ?? "missing or invalid metadata",
          remediation: "npm run compile:locks",
        }));
      }
    }
  } finally {
    await rm(temporaryRoot, { recursive: true, force: true });
  }
  return findings;
}

async function declarations(root) {
  const result = [];
  const entries = await readdir(root, { withFileTypes: true });
  for (const entry of entries) {
    if (!entry.isDirectory() || entry.name.startsWith(".")) continue;
    const relativePath = `${entry.name}/cao.json`;
    try {
      const document = JSON.parse(await readFile(path.join(root, relativePath), "utf8"));
      if (document?.campaign && document?.orchestrator && document?.workers) {
        result.push({ ...document, file: relativePath });
      }
    } catch (error) {
      if (error?.code !== "ENOENT") {
        result.push({ file: relativePath, error: error.message, campaign: entry.name });
      }
    }
  }
  return result.sort((left, right) => left.campaign.localeCompare(right.campaign));
}

export async function validatePolicy(root, source) {
  const findings = [];
  let policy;
  try {
    const policyModule = await import(new URL(
      "../.github/workflows/shared/" + "policy.mjs",
      import.meta.url,
    ));
    const { parsePolicy, controlSettings } = policyModule;
    policy = parsePolicy(source);
    const repository = process.env.GITHUB_REPOSITORY || "local/control";
    controlSettings(policy, repository);
  } catch (error) {
    findings.push(finding("invalid-cao-policy", "error", "policy", "CAO policy is invalid", {
      files: [POLICY_PATH],
      expected: "Policy passes the production parser and resolver",
      observed: error.message,
      remediation: `Fix ${POLICY_PATH}, publish the reviewed changes, and run gh workflow run cao-validate.yml`,
    }));
    return { policy: null, findings };
  }

  const { effectivePolicy } = await import(new URL(
    "../.github/workflows/shared/" + "policy.mjs",
    import.meta.url,
  ));
  const installed = await declarations(root);
  const installedNames = new Set(installed.map(({ campaign }) => campaign));
  const policyCampaigns = policy["control-plane"]?.campaigns ?? {};
  const workflowNames = new Set((await filesIn(path.join(root, WORKFLOW_DIRECTORY), ".md"))
    .map((name) => name.replace(/\.md$/, "")));

  for (const declaration of installed) {
    if (declaration.error) {
      findings.push(finding("invalid-campaign-declaration", "error", "policy", "Campaign declaration is invalid", {
        campaign: declaration.campaign,
        files: [declaration.file],
        observed: declaration.error,
        remediation: `Fix ${declaration.file}`,
      }));
      continue;
    }
    if (!Object.hasOwn(policyCampaigns, declaration.campaign)) {
      findings.push(finding("installed-campaign-absent-from-policy", "error", "policy", "Installed campaign is absent from policy", {
        campaign: declaration.campaign,
        files: [declaration.file, POLICY_PATH],
        expected: "Every installed operational campaign is declared in policy",
        observed: `${declaration.campaign} is installed but undeclared`,
        remediation: `Add ${declaration.campaign} to ${POLICY_PATH}`,
      }));
    }
    const identities = [declaration.orchestrator, ...Object.values(declaration.workers)];
    for (const identity of identities.filter((name) => !workflowNames.has(name))) {
      findings.push(finding("missing-workflow", "error", "policy", "Campaign references a missing workflow", {
        campaign: declaration.campaign,
        files: [declaration.file],
        expected: `${identity}.md is installed`,
        observed: "Workflow source is missing",
        remediation: `Reinstall or update campaign ${declaration.campaign}`,
      }));
    }
    if (policyCampaigns[declaration.campaign]) {
      try {
        effectivePolicy(policy, {
          campaignName: declaration.campaign,
          role: "orchestrator",
          controlRepository: process.env.GITHUB_REPOSITORY || "local/control",
        });
        for (const workerName of Object.keys(declaration.workers)) {
          effectivePolicy(policy, {
            campaignName: declaration.campaign,
            role: "worker",
            workerName,
            controlRepository: process.env.GITHUB_REPOSITORY || "local/control",
          });
        }
      } catch (error) {
        findings.push(finding("invalid-policy-resolution", "error", "policy", "Campaign policy cannot be resolved", {
          campaign: declaration.campaign,
          files: [POLICY_PATH, declaration.file],
          observed: error.message,
          remediation: `Reconcile ${declaration.file} with ${POLICY_PATH}`,
        }));
      }
    }
  }
  const internalCampaigns = new Set(["dashboard"]);
  internalCampaigns.add("activity");
  for (const campaign of Object.keys(policyCampaigns).filter((name) => !internalCampaigns.has(name) && !installedNames.has(name))) {
    findings.push(finding("policy-campaign-not-installed", "error", "policy", "Policy references a campaign that is not installed", {
      campaign,
      files: [POLICY_PATH],
      expected: `${campaign}/cao.json is installed`,
      observed: "Campaign declaration is missing",
      remediation: `Install ${campaign} or remove its stale policy entry`,
    }));
  }
  for (const workflowName of [
    ...Object.keys(policy["control-plane"]?.workflows ?? {}),
    ...Object.keys(policy["control-plane"]?.operations ?? {}),
  ]) {
    const workflowPath = path.posix.join(WORKFLOW_DIRECTORY, `${workflowName}.md`);
    const operation = Object.hasOwn(policy["control-plane"]?.operations ?? {}, workflowName);
    if (!workflowNames.has(workflowName)) {
      findings.push(finding(operation ? "missing-operation-workflow" : "missing-standalone-workflow", "error", "policy", `Policy references a missing ${operation ? "direct operation" : "standalone"} workflow`, {
        files: [POLICY_PATH, workflowPath],
        expected: `${workflowName}.md is installed`,
        observed: "Workflow source is missing",
        remediation: `Install ${workflowName} or remove its stale policy entry`,
      }));
      continue;
    }
    try {
      if (operation) continue;
      effectivePolicy(policy, {
        workflowName, role: "standalone",
        controlRepository: process.env.GITHUB_REPOSITORY || "local/control",
      });
    } catch (error) {
      findings.push(finding("invalid-policy-resolution", "error", "policy", "Standalone workflow policy cannot be resolved", {
        files: [POLICY_PATH, workflowPath],
        observed: error.message,
        remediation: `Reconcile ${workflowPath} with ${POLICY_PATH}`,
      }));
    }
  }
  return { policy, installed, findings };
}

function normalizeWorkflowPath(value) {
  return String(value ?? "").replace(/^\/+/, "");
}

export function validateEnablement(root, execute, installed) {
  const result = run(execute, "gh", ["workflow", "list", "--all", "--json", "path,state"], root);
  if (result.error || result.status !== 0) {
    return [finding("github-enablement-state-unavailable", "warning", "configuration", "GitHub workflow enablement state is unavailable", {
      expected: "GitHub Actions workflow states can be enumerated",
      observed: commandOutput(result) || result.error?.message || "GitHub API unavailable",
      remediation: "Verify Actions read access for the workflow token and rerun the CAO validation workflow",
    })];
  }
  let workflows;
  try {
    workflows = JSON.parse(result.stdout);
  } catch (error) {
    return [finding("github-enablement-state-unavailable", "warning", "configuration", "GitHub workflow enablement state is unavailable", {
      expected: "gh workflow list returns JSON",
      observed: error.message,
      remediation: "Verify GitHub CLI in Actions and rerun the CAO validation workflow",
    })];
  }
  const states = new Map(workflows.map((workflow) => [normalizeWorkflowPath(workflow.path), workflow.state ?? "unknown"]));
  const findings = [];
  for (const declaration of installed ?? []) {
    if (declaration.error) continue;
    const names = [declaration.orchestrator, ...Object.values(declaration.workers)];
    const observed = names.map((name) => {
      const workflowPath = `${WORKFLOW_DIRECTORY}/${name}.lock.yml`;
      return { workflow: name, path: workflowPath, state: states.get(workflowPath) ?? "unknown" };
    });
    const knownStates = new Set(observed.map(({ state }) => state).filter((state) => state !== "unknown"));
    if (observed.length > 1
        && observed[0].state === "disabled_manually"
        && observed.slice(1).every(({ state }) => state === "active")) {
      findings.push(finding("campaign-orchestrator-paused", "note", "configuration", "Campaign orchestration is paused", {
        campaign: declaration.campaign,
        files: observed.map(({ path: workflowPath }) => workflowPath),
        expected: "A manually paused orchestrator does not start new work; active workers may finish dispatched work",
        observed: observed.map(({ workflow, state }) => `${workflow}: ${state}`).join(", "),
        remediation: "Leave orchestration paused until another run is intended; enabling the campaign also resumes its schedule",
      }));
      continue;
    }
    if (knownStates.size > 1) {
      const activeCount = observed.filter(({ state }) => state === "active").length;
      findings.push(finding("campaign-partially-disabled", "warning", "configuration", "Campaign workflows have inconsistent enablement", {
        campaign: declaration.campaign,
        files: observed.map(({ path: workflowPath }) => workflowPath),
        expected: "All campaign workflows share one enablement state",
        observed: observed.map(({ workflow, state }) => `${workflow}: ${state}`).join(", "),
        remediation: `gh cao campaigns ${activeCount >= observed.length / 2 ? "enable" : "disable"} ${declaration.campaign}`,
      }));
    }
  }
  return findings;
}

export async function validateSecurity(root, policySource, installed = []) {
  const findings = [];
  const workflowRoot = path.join(root, WORKFLOW_DIRECTORY);
  const yamlFiles = (await filesIn(workflowRoot, ".yml")).filter((name) => !name.endsWith(".lock.yml"));
  for (const name of yamlFiles) {
    const relativePath = path.posix.join(WORKFLOW_DIRECTORY, name);
    const source = await readFile(path.join(workflowRoot, name), "utf8");
    if (/\bpull_request_target\s*:/.test(source)) {
      findings.push(finding("unsafe-pull-request-target", "error", "security", "Workflow uses pull_request_target", {
        files: [relativePath],
        expected: "Untrusted pull request code never runs with base-repository authority",
        observed: "pull_request_target trigger is configured",
        remediation: "Replace pull_request_target with a least-privilege pull_request design",
      }));
    }
    for (const match of source.matchAll(/^\s*(?:-\s*)?uses:\s*([^/\s]+\/[^@\s]+)@([^\s#]+)([^\r\n]*)/gm)) {
      if (match[1] === "githubnext/gh-cao/.github/workflows/cao-package-publish.yml"
        && match[2] === "main"
        && match[3].trim() === "# zizmor: ignore[unpinned-uses] protected default-branch publisher") {
        continue;
      }
      if (!/^[0-9a-f]{40}$/i.test(match[2])) {
        findings.push(finding("unpinned-third-party-action", "error", "security", "Workflow uses an unpinned third-party Action", {
          files: [relativePath],
          expected: "Third-party Actions are pinned to a full commit SHA",
          observed: `${match[1]}@${match[2]}`,
          remediation: `Pin ${match[1]} to a reviewed full commit SHA`,
        }));
      }
    }
  }
  if (/(?:ghp_|github_pat_|-----BEGIN (?:RSA |EC |OPENSSH )?PRIVATE KEY-----)/.test(policySource)) {
    findings.push(finding("credential-in-policy", "error", "security", "CAO policy appears to contain credential material", {
      files: [POLICY_PATH],
      expected: "Policy contains secret names only; values remain in Actions secrets",
      observed: "A credential-like value appears in policy",
      remediation: "Revoke the credential and replace it with an Actions secret reference",
    }));
  }
  for (const declaration of installed) {
    if (declaration.error) continue;
    const roles = [
      { role: "orchestrator", workflow: declaration.orchestrator },
      ...Object.values(declaration.workers).map((workflow) => ({ role: "worker", workflow })),
    ];
    for (const { role, workflow } of roles) {
      const relativePath = path.posix.join(WORKFLOW_DIRECTORY, `${workflow}.md`);
      let source;
      try {
        source = await readFile(path.join(root, relativePath), "utf8");
      } catch (error) {
        if (error?.code === "ENOENT") continue;
        throw error;
      }
      const frontmatter = /^---\r?\n([\s\S]*?)\r?\n---/.exec(source)?.[1] ?? "";
      if (role === "worker" && /^\s*dispatch-workflow\s*:/m.test(frontmatter)) {
        findings.push(finding("worker-can-dispatch-workflow", "error", "security", "Worker workflow can dispatch additional work", {
          campaign: declaration.campaign,
          files: [relativePath],
          expected: "Only orchestrators dispatch campaign workers",
          observed: `${workflow} declares dispatch-workflow`,
          remediation: `Remove dispatch-workflow from ${relativePath}`,
        }));
      }
      if (role === "orchestrator" && /^\s*(?:create-pull-request|push-to-branch|create-issue|add-comment)\s*:/m.test(frontmatter)) {
        findings.push(finding("orchestrator-can-mutate-target", "error", "security", "Orchestrator declares a direct mutation capability", {
          campaign: declaration.campaign,
          files: [relativePath],
          expected: "Orchestrators select and dispatch; workers perform target work",
          observed: `${workflow} declares a direct write safe output`,
          remediation: `Move target mutation from ${workflow} to a bounded worker`,
        }));
      }
      if (role === "orchestrator" && /^\s+(?:token|password|private[_-]key|credential)\s*:/mi.test(frontmatter)) {
        findings.push(finding("credential-in-dispatch-envelope", "error", "security", "Orchestrator dispatch input can carry credentials", {
          campaign: declaration.campaign,
          files: [relativePath],
          expected: "Dispatch envelopes contain no credentials",
          observed: `${workflow} declares a credential-like input`,
          remediation: "Remove the credential input and use repository Actions secrets",
        }));
      }
    }
  }
  return findings;
}

export function doctorFindings(root, execute) {
  const repository = repositoryName(root, execute);
  const args = repository.includes("/")
    ? ["aw", "doctor", "--repo", repository, "--dir", ".", "--json"]
    : ["aw", "doctor", "--json"];
  const result = run(execute, "gh", args, root);
  if (result.error || result.status !== 0) {
    return [finding("doctor-check-failed", "warning", "installation", "gh-aw doctor could not verify repository setup", {
      expected: "gh-aw doctor verifies installation and repository setup",
      observed: commandOutput(result) || result.error?.message || "Doctor failed",
      remediation: "Run gh aw doctor --dir . --json after authenticating GitHub CLI",
    })];
  }
  return [];
}

function summary(findings) {
  return {
    errors: findings.filter(({ severity }) => severity === "error").length,
    warnings: findings.filter(({ severity }) => severity === "warning").length,
    notes: findings.filter(({ severity }) => severity === "note").length,
  };
}

function repositoryName(root, execute) {
  if (process.env.GITHUB_REPOSITORY) return process.env.GITHUB_REPOSITORY;
  try {
    const result = run(execute, "git", ["config", "--get", "remote.origin.url"], root);
    const match = String(result.stdout ?? "").trim().match(/(?:github\.com[:/])([^/]+\/[^/.]+)(?:\.git)?$/);
    return match?.[1] ?? path.basename(root);
  } catch {
    return path.basename(root);
  }
}

export async function validateRepository({
  root = process.cwd(),
  execute = spawnSync,
  now = () => new Date(),
  strictWarnings = false,
} = {}) {
  const findings = [];
  let expectedVersion = null;
  let currentVersion = null;
  try {
    let policySource;
    try {
      policySource = await readFile(path.join(root, POLICY_PATH), "utf8");
    } catch (error) {
      if (error?.code !== "ENOENT") throw error;
      const missingPolicy = finding("missing-cao-policy", "error", "policy", "CAO policy is missing", {
        files: [POLICY_PATH],
        expected: "A checked-in CAO policy",
        observed: `${POLICY_PATH} does not exist`,
        remediation: "gh cao init",
      });
      return {
        validatorVersion: VALIDATOR_VERSION,
        repository: repositoryName(root, execute),
        ghAwVersion: null,
        expectedGhAwVersion: null,
        timestamp: now().toISOString(),
        findings: [missingPolicy],
        summary: summary([missingPolicy]),
        exitCode: 1,
      };
    }
    let rawPolicy;
    try {
      rawPolicy = JSON.parse(policySource);
      expectedVersion = rawPolicy["gh-aw-version"] ?? null;
    } catch {
      // The production policy parser reports the actionable error below.
    }

    const installed = installedVersion(execute, root);
    currentVersion = installed.version;
    if (!currentVersion) {
      findings.push(finding("gh-aw-unavailable", "error", "compiler", "gh-aw compiler is unavailable", {
        expected: expectedVersion ?? "Version declared by CAO policy",
        observed: commandOutput(installed.result) || installed.result.error?.message || "gh aw version failed",
        remediation: "npm run install:gh-aw",
      }));
    } else if (expectedVersion && currentVersion !== expectedVersion) {
      findings.push(finding("wrong-gh-aw-compiler-version", "error", "compiler", "Installed gh-aw compiler version does not match policy", {
        files: [POLICY_PATH],
        expected: expectedVersion,
        observed: currentVersion,
        remediation: "npm run install:gh-aw",
      }));
    }

    if (currentVersion) findings.push(...await compileAndCompare(root, execute, expectedVersion));
    const policyValidation = await validatePolicy(root, policySource);
    findings.push(...policyValidation.findings);
    findings.push(...doctorFindings(root, execute));
    findings.push(...validateEnablement(root, execute, policyValidation.installed));
    findings.push(...await validateSecurity(root, policySource, policyValidation.installed));

    const counts = summary(findings);
    const exitCode = counts.errors > 0 || (strictWarnings && counts.warnings > 0) ? 1 : 0;
    return {
      validatorVersion: VALIDATOR_VERSION,
      repository: repositoryName(root, execute),
      ghAwVersion: currentVersion,
      expectedGhAwVersion: expectedVersion,
      timestamp: now().toISOString(),
      findings,
      summary: counts,
      exitCode,
    };
  } catch (error) {
    const infrastructureFinding = finding("validator-failure", "error", "infrastructure", "CAO validator failed", {
      observed: error.message,
      remediation: "Verify the Actions checkout, runtime, and validator configuration, then rerun the CAO validation workflow",
    });
    return {
      validatorVersion: VALIDATOR_VERSION,
      repository: repositoryName(root, execute),
      ghAwVersion: currentVersion,
      expectedGhAwVersion: expectedVersion,
      timestamp: now().toISOString(),
      findings: [...findings, infrastructureFinding],
      summary: summary([...findings, infrastructureFinding]),
      exitCode: 2,
    };
  }
}

export function formatValidationReport(report) {
  const groups = [
    ["Policy", ["policy"]],
    ["gh-aw compiler", ["compiler", "installation"]],
    ["Generated workflows", ["generated-workflows"]],
    ["Campaign enablement", ["configuration"]],
    ["Security", ["security"]],
    ["Validator infrastructure", ["infrastructure"]],
  ];
  const lines = ["CAO validation"];
  for (const [label, categories] of groups) {
    const relevant = report.findings.filter(({ category }) => categories.includes(category));
    const symbol = relevant.some(({ severity }) => severity === "error") ? "✗"
      : relevant.some(({ severity }) => severity === "warning") ? "⚠" : "✓";
    lines.push(`${symbol} ${label}`);
  }
  for (const item of report.findings) {
    lines.push(`  ${item.severity}: ${item.id}: ${item.title}`);
    if (item.observed) lines.push(`    ${item.observed}`);
    if (item.remediation) lines.push(`    Remediation: ${item.remediation}`);
  }
  lines.push(`${report.summary.errors} error${report.summary.errors === 1 ? "" : "s"}, ${report.summary.warnings} warning${report.summary.warnings === 1 ? "" : "s"}`);
  return lines.join("\n");
}
