import { execFileSync, spawnSync } from "node:child_process";
import { chmodSync, cpSync, existsSync, mkdirSync, mkdtempSync, readFileSync, renameSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { createHash, randomUUID } from "node:crypto";
import { homedir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { parse } from "yaml";

export const root = resolve(dirname(fileURLToPath(import.meta.url)), "../../..");
const harness = join(root, "tests/helpers/actions-runtime");
export const image = "node:24-bookworm";

export function run(command, args, options = {}) {
  const result = spawnSync(command, args, { encoding: "utf8", timeout: 180_000, maxBuffer: 32 << 20, ...options });
  if (result.error || result.status !== 0) throw new Error(`${command} failed: ${result.error?.message || result.stderr || result.stdout}`);
  return result.stdout;
}

export function publishFile(target, prepare) {
  const temporary = `${target}.${randomUUID()}.partial`;
  try {
    prepare(temporary);
    renameSync(temporary, target);
  } finally { rmSync(temporary, { force: true }); }
}

// Download only the immutable setup revision declared by this compiled workflow.
export function prepareActions(workspace, workflow, cache) {
  const compiled = parse(readFileSync(join(workspace, workflow), "utf8"));
  const refs = new Set(Object.values(compiled.jobs).flatMap(job =>
    job.steps?.filter(step => step.uses?.startsWith("github/gh-aw-actions/setup@")).map(step => step.uses.split("@")[1]) || []));
  if (refs.size !== 1 || !/^[a-f0-9]{40}$/.test([...refs][0])) throw new Error("Exactly one SHA-pinned gh-aw setup action is required");
  const sha = [...refs][0], destination = join(cache, sha);
  if (!existsSync(join(destination, "setup/index.js"))) {
    mkdirSync(destination, { recursive: true });
    const archive = join(cache, `${sha}.tar.gz`);
    run("curl", ["--fail", "--silent", "--show-error", "--location", "--proto", "=https", "--output", archive,
      `https://codeload.github.com/github/gh-aw-actions/tar.gz/${sha}`]);
    run("tar", ["-xzf", archive, "-C", destination, "--strip-components=1"]);
  }
  return destination;
}

export function prepareTools(cache) {
  const arch = run("docker", ["image", "inspect", image, "--format", "{{.Architecture}}"]).trim();
  if (!["arm64", "amd64"].includes(arch)) throw new Error("Unsupported Docker image architecture");
  const destination = join(cache, `tools-${arch}`), binary = join(destination, "jq");
  mkdirSync(destination, { recursive: true });
  if (!existsSync(binary)) {
    const asset = `jq-linux-${arch}`, url = "https://github.com/jqlang/jq/releases/download/jq-1.8.1";
    const sums = run("curl", ["-fsSL", "--proto", "=https", `${url}/sha256sum.txt`]);
    const checksum = sums.split("\n").find(line => line.endsWith(`  ${asset}`))?.split(/\s/)[0];
    if (!/^[a-f0-9]{64}$/.test(checksum)) throw new Error("jq checksum missing");
    publishFile(binary, temporary => {
      run("curl", ["-fsSL", "--proto", "=https", "-o", temporary, `${url}/${asset}`]);
      if (createHash("sha256").update(readFileSync(temporary)).digest("hex") !== checksum) throw new Error("jq checksum mismatch");
      chmodSync(temporary, 0o755);
    });
  }
  return destination;
}

export function compile(workspace, workflows) {
  const realGh = execFileSync("which", ["gh"], { encoding: "utf8" }).trim();
  const bin = join(workspace, ".simulator-bin");
  mkdirSync(bin, { recursive: true });
  symlinkSync(join(harness, "compile-gh.mjs"), join(bin, "gh"));
  run("git", ["init", "--quiet", "--initial-branch=main"], { cwd: workspace });
  run("git", ["remote", "add", "origin", "https://github.com/simulator/repo-00001.git"], { cwd: workspace });
  run("git", ["add", ".github", ".github/workflows/shared/value-acquisition.mjs"], { cwd: workspace });
  run("git", ["-c", "user.name=Actions simulator", "-c", "user.email=simulator@example.invalid",
    "commit", "--quiet", "--no-gpg-sign", "-m",
    "Synthetic workflow revision\n\nCo-authored-by: Copilot App <223556219+Copilot@users.noreply.github.com>"], { cwd: workspace });
  const env = {
    PATH: `${bin}:${process.env.PATH}`, HOME: join(workspace, ".simulator-home"),
    GH_CONFIG_DIR: join(workspace, ".simulator-home/gh"),
    XDG_DATA_HOME: process.env.XDG_DATA_HOME || join(homedir(), ".local/share"),
    GH_NO_UPDATE_NOTIFIER: "1", GH_PROMPT_DISABLED: "1",
    GIT_CONFIG_NOSYSTEM: "1", GIT_CONFIG_GLOBAL: "/dev/null", GIT_TERMINAL_PROMPT: "0",
  };
  const setup = /uses: github\/gh-aw-actions\/setup@([a-f0-9]{40})/.exec(
    readFileSync(join(root, ".github/workflows/dependabot.lock.yml"), "utf8"),
  )?.[1];
  if (!setup) throw new Error("Catalog lock does not declare an immutable setup action");
  const args = ["aw", "compile", "--use-samples", "--no-check-update",
    "--schedule-seed", "githubnext/gh-cao", "--action-mode", "action", "--action-tag", setup];
  // CAO's reviewed host steps are incompatible with compile-only dry runs.
  // Replay them only as staged samples inside the credential-free, offline container.
  const result = workflows.map(workflow => run(realGh, [...args, workflow,
    ...(workflow === "cao" ? ["--staged", "--shellcheck"] : ["--dry-run"])], { cwd: workspace, env })).join("\n");
  run("git", ["add", ".github"], { cwd: workspace });
  run("git", ["-c", "user.name=Actions simulator", "-c", "user.email=simulator@example.invalid",
    "commit", "--quiet", "--no-gpg-sign", "-m",
    "Compile synthetic sample workflows\n\nCo-authored-by: Copilot App <223556219+Copilot@users.noreply.github.com>"], { cwd: workspace });
  return result;
}

export function simulate({ workspace, workflow, event, eventName = "repository_dispatch", output, upstream, tools, vars = {} }) {
  mkdirSync(output, { recursive: true });
  const sha = run("git", ["rev-parse", "HEAD"], { cwd: workspace }).trim();
  writeFileSync(join(workspace, "simulator.json"), JSON.stringify({ workflow, event, eventName, sha, vars }));
  const name = `cao-actions-test-${randomUUID()}`;
  const result = spawnSync("docker", [
    "run", "--name", name, "--rm", "--pull=never", "--network=none", "--read-only", "--cap-drop=ALL",
    "--user", `${process.getuid()}:${process.getgid()}`,
    "--security-opt=no-new-privileges", "--pids-limit=128", "--memory=1g", "--cpus=2",
    "--tmpfs", "/tmp:rw,exec,nosuid,size=768m",
    "--mount", `type=bind,src=${workspace},dst=/fixture,readonly`,
    "--mount", `type=bind,src=${harness},dst=/simulator,readonly`,
    "--mount", `type=bind,src=${join(root, "node_modules")},dst=/node_modules,readonly`,
    ...(upstream ? ["--mount", `type=bind,src=${upstream},dst=/upstream,readonly`] : []),
    ...(tools ? ["--mount", `type=bind,src=${tools},dst=/tools,readonly`] : []),
    "--mount", `type=bind,src=${output},dst=/output`,
    image, "node", "/simulator/runner.mjs",
  ], { encoding: "utf8", timeout: 240_000, maxBuffer: 8 << 20 });
  const cleanup = spawnSync("docker", ["rm", "--force", name], { encoding: "utf8", timeout: 15_000 });
  if (cleanup.error || cleanup.status !== 0 && !cleanup.stderr.includes("No such container")) {
    throw new Error("Could not remove the test-owned Actions container");
  }
  const reportFile = join(output, "report.json");
  if (!existsSync(reportFile)) throw new Error(`Docker runtime did not produce a report: ${result.error?.message || result.stderr}`);
  const report = JSON.parse(readFileSync(reportFile, "utf8"));
  if (result.error || result.status !== 0 || report.error) {
    throw new Error(`Actions simulation failed: ${report.error || result.error?.message || result.stderr}\nReport: ${reportFile}`);
  }
  return report;
}

export function fixture(workspace) {
  mkdirSync(join(workspace, ".github/workflows"), { recursive: true });
  mkdirSync(join(workspace, "activity"), { recursive: true });
  cpSync(join(root, "tests/fixtures/actions-runtime/sample.md"), join(workspace, ".github/workflows/sample.md"));
  cpSync(join(root, "tests/fixtures/actions-runtime/cao.md"), join(workspace, ".github/workflows/cao.md"));
  cpSync(join(root, "tests/fixtures/actions-runtime/cao.json"), join(workspace, ".github/workflows/cao.json"));
  cpSync(join(root, ".github/workflows/shared/value-acquisition.mjs"), join(workspace, ".github/workflows/shared/value-acquisition.mjs"));
  cpSync(join(root, ".github/workflows/shared"), join(workspace, ".github/workflows/shared"), { recursive: true });
}
