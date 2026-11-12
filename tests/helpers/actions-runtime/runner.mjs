import { spawn } from "node:child_process";
import { cp, mkdir, readFile, readdir, rm, stat, writeFile } from "node:fs/promises";
import path from "node:path";
import { condition, evaluate, interpolate } from "./expressions.mjs";
import { githubAPI } from "./github-api.mjs";
import { sampleWorkflow } from "./workflow.mjs";

// Samples spawn their own stdio safe-output server. These compiled infrastructure
// steps cannot contribute to sample execution and must never start real inference.
const infrastructure = new Map([
  ["Install GitHub Copilot CLI", "inference is replaced by compiled samples"],
  ["Install AWF binary", "no inference process or nested container engine"],
  ["Download container images", "no inference process or nested container engine"],
  ["Start MCP Gateway", "samples use the real stdio safe-output server"],
  ["Mount MCP servers as CLIs", "samples call the stdio server directly"],
  ["Stop MCP Gateway", "no network MCP gateway was started"],
  ["Check compile-agentic version", "offline test uses a pinned compiler and action revision"],
]);
const infrastructureScript = new Map([
  ["Install GitHub Copilot CLI", "install_copilot_cli.sh"],
  ["Install AWF binary", "install_awf_binary.sh"],
  ["Download container images", "download_docker_images.sh"],
  ["Start MCP Gateway", "start_mcp_gateway.cjs"],
  ["Mount MCP servers as CLIs", "mount_mcp_as_cli.cjs"],
  ["Stop MCP Gateway", "stop_mcp_gateway.sh"],
  ["Check compile-agentic version", "check_version_updates.cjs"],
]);
const supportedActions = new Set([
  "github/gh-aw-actions/setup", "actions/github-script", "actions/checkout",
  "actions/upload-artifact", "actions/download-artifact",
  "actions/create-github-app-token",
  "actions/setup-node",
]);
const report = { identity: { uid: process.getuid(), gid: process.getgid() }, jobs: {}, requests: [], unhandled: [], simulated: [], artifacts: [] };

async function command(program, args, env, cwd, log) {
  await new Promise((resolve, reject) => {
    const child = spawn(program, args, { env, cwd, timeout: 90_000 });
    let output = "";
    child.stdout.on("data", data => { output += data; });
    child.stderr.on("data", data => { output += data; });
    child.on("error", reject);
    child.on("close", async (code, signal) => {
      try {
        await writeFile(log, output);
        if (code !== 0) reject(new Error(`${program} failed (${signal || code}); see ${log}`));
        else resolve();
      } catch (error) { reject(error); }
    });
  });
}

async function fileCommands(file) {
  const lines = (await readFile(file, "utf8")).split(/\r?\n/);
  const values = {};
  for (let index = 0; index < lines.length; index++) {
    if (!lines[index]) continue;
    const multi = /^([^=]+)<<(.+)$/.exec(lines[index]);
    if (multi) {
      const end = lines.indexOf(multi[2], index + 1);
      if (end < 0) throw new Error("Unterminated Actions file command");
      values[multi[1]] = lines.slice(index + 1, end).join("\n");
      index = end;
    } else {
      const equals = lines[index].indexOf("=");
      if (equals < 1) throw new Error("Unsupported Actions file command");
      values[lines[index].slice(0, equals)] = lines[index].slice(equals + 1);
    }
  }
  return values;
}

const stringify = values => Object.fromEntries(Object.entries(values).map(([key, value]) => [key, String(value ?? "")]));
const render = (values, context) => Object.fromEntries(Object.entries(values || {}).map(([key, value]) => [key, interpolate(value, context)]));
function beneath(root, name) {
  const file = path.resolve(root, name);
  if (file !== root && !file.startsWith(root + "/")) throw new Error("Simulator path escapes its root");
  return file;
}

async function artifactUpload(input, workspace) {
  const names = String(input.path).split("\n").map(s => s.trim()).filter(Boolean);
  if (names.some(name => name.startsWith("!") || /[*?[\]]/.test(name))) throw new Error("Artifact glob patterns are unsupported");
  const files = [];
  for (const name of names) {
    const file = path.resolve(workspace, name);
    if (!file.startsWith("/tmp/")) throw new Error("Artifact source must be test-owned");
    try { await readFileOrDirectory(file); files.push(file); }
    catch (error) { if (error.code !== "ENOENT") throw error; }
  }
  if (!files.length) {
    if (input["if-no-files-found"] === "error") throw new Error("Artifact files not found");
    return;
  }
  let base = path.dirname(files[0]);
  for (const file of files) while (!file.startsWith(base + "/")) base = path.dirname(base);
  const destination = beneath("/tmp/artifacts", input.name);
  await mkdir(destination, { recursive: true });
  for (const file of files) {
    const target = files.length === 1 && (await stat(file)).isDirectory()
      ? destination : beneath(destination, path.relative(base, file));
    await cp(file, target, { recursive: true });
  }
  if (!report.artifacts.includes(input.name)) report.artifacts.push(input.name);
}
async function readFileOrDirectory(file) {
  try { await readdir(file); } catch (error) { if (error.code !== "ENOTDIR") throw error; await readFile(file); }
}

async function executeStep(step, input, env, context, workspace, log) {
  if (infrastructure.has(step.name)) {
    if (!(step.run || step.with?.script || "").includes(infrastructureScript.get(step.name))) {
      throw new Error("Compiled infrastructure adapter does not match the expected helper");
    }
    report.simulated.push({ job: context.github.job, step: step.name, reason: infrastructure.get(step.name) });
    return;
  }
  if (step.run) {
    if (step.shell && !["bash", "sh"].includes(step.shell)) throw new Error(`Unsupported shell: ${step.shell}`);
    const script = `${log}.sh`;
    await writeFile(script, interpolate(step.run, context));
    await command("bash", ["--noprofile", "--norc", "-eo", "pipefail", script], env,
      beneath(workspace, step["working-directory"] || "."), log);
    return;
  }
  const action = step.uses.split("@")[0];
  switch (action) {
    case "github/gh-aw-actions/setup":
      await command("node", ["/tmp/upstream/setup/index.js"], {
        ...env, ...Object.fromEntries(Object.entries(input).map(([key, value]) => [`INPUT_${key.toUpperCase().replaceAll("-", "_")}`, String(value)])),
      }, workspace, log);
      break;
    case "actions/github-script": {
      const script = `${log}.cjs`;
      await writeFile(script, interpolate(step.with.script, context));
      await command("node", ["/simulator/github-script.mjs", script], env, workspace, log);
      break;
    }
    case "actions/checkout": {
      if (input.repository && input.repository !== context.github.repository) throw new Error("Cross-repository checkout unsupported");
      if (input.ref && ![context.github.sha, context.github.workflow_sha, context.github.ref].includes(input.ref)) {
        throw new Error("Only the fixture revision can be checked out");
      }
      const destination = beneath(workspace, input.path || ".");
      await mkdir(destination, { recursive: true });
      await cp("/fixture", destination, { recursive: true, dereference: false });
      break;
    }
    case "actions/upload-artifact": await artifactUpload(input, workspace); break;
    case "actions/download-artifact": {
      const pattern = input.name || input.pattern;
      const names = /^[\w-]+\*$/.test(pattern)
        ? report.artifacts.filter(name => name.startsWith(pattern.slice(0, -1)))
        : pattern.startsWith("{") && pattern.endsWith("}") ? pattern.slice(1, -1).split(",") : [pattern];
      if (names.some(name => !/^[\w-]+$/.test(name))) throw new Error("Only literal artifact names and brace lists are supported");
      let downloaded = 0;
      for (const name of names) {
        if (!report.artifacts.includes(name)) continue;
        const destination = path.resolve(workspace, input.path || ".");
        await mkdir(destination, { recursive: true });
        await cp(beneath("/tmp/artifacts", name),
          beneath("/tmp", path.relative("/tmp", !input.name && input["merge-multiple"] !== true
            ? path.join(destination, name) : destination)), { recursive: true });
        downloaded++;
      }
      if (!downloaded) throw new Error("Artifact not found");
      break;
    }
    case "actions/setup-node":
      if (String(input["node-version"]) !== process.versions.node.split(".")[0]) throw new Error("Runner Node version mismatch");
      break;
    default: throw new Error(`Unsupported action: ${step.uses}`);
  }
}

async function run() {
  const config = JSON.parse(await readFile("/fixture/simulator.json", "utf8"));
  const lock = await readFile(beneath("/fixture", config.workflow), "utf8");
  const workflow = sampleWorkflow(lock, { allowStaged: config.workflow === ".github/workflows/cao.lock.yml" });
  if (workflow.defaults) throw new Error("Workflow defaults are unsupported");
  for (const job of Object.values(workflow.jobs)) {
    if (job.strategy || job.services || job.container || job.uses || job.environment || job.defaults || !job.steps) {
      throw new Error("Unsupported job topology");
    }
    for (const step of job.steps) {
      if (step.uses && (!supportedActions.has(step.uses.split("@")[0]) ||
        !step.uses.startsWith("./") && !/@[a-f0-9]{40}$/.test(step.uses))) {
        throw new Error(`Unsupported or unpinned action: ${step.uses}`);
      }
      if (!step.run && !step.uses) throw new Error("Step requires run or uses");
    }
  }
  const event = config.event;
  const trigger = workflow.on[config.eventName];
  if (!Object.hasOwn(workflow.on, config.eventName) || trigger?.types && !trigger.types.includes(event.action)) {
    throw new Error("Event does not match the compiled workflow trigger");
  }
  const api = await githubAPI("/fixture", event, report, config);
  try {
    await cp("/upstream/setup", "/tmp/upstream/setup", { recursive: true });
    const pending = new Map(Object.entries(workflow.jobs));
    while (pending.size) {
      let progress = false;
      for (const [id, job] of pending) {
        const dependencies = [job.needs || []].flat();
        if (dependencies.some(name => !Object.hasOwn(workflow.jobs, name))) throw new Error("Unknown job dependency");
        if (dependencies.some(name => !report.jobs[name])) continue;
        progress = true;
        pending.delete(id);
        const workspace = `/tmp/work/${id}`, temp = `/tmp/runner/${id}`;
        const github = {
          repository: event.repository.full_name, repository_owner: event.repository.owner.login,
          workflow: workflow.name, workflow_ref: `${event.repository.full_name}/${config.workflow}@refs/heads/main`,
          workflow_sha: config.sha, sha: config.sha, ref: "refs/heads/main", ref_name: "main",
          actor: event.sender.login, triggering_actor: event.sender.login,
          run_id: "1", run_attempt: "1",
          event_name: config.eventName, event, job: id, token: "simulator-not-a-real-token",
          workspace, server_url: "https://github.com",
          api_url: "https://api.github.com",
        };
        const context = {
          github, runner: { temp, tool_cache: "/tmp/tool-cache", debug: "0", os: "Linux", arch: process.arch === "arm64" ? "ARM64" : "X64" },
          job: { workflow_ref: github.workflow_ref, workflow_sha: github.workflow_sha, check_run_id: "1" },
          inputs: event.inputs || {}, vars: config.vars || {}, secrets: { GITHUB_TOKEN: github.token, COPILOT_GITHUB_TOKEN: "github_pat_simulator_not_a_real_token" },
          needs: Object.fromEntries(dependencies.map(name => [name, report.jobs[name]])), steps: {}, env: {},
          status: dependencies.every(name => report.jobs[name].result === "success") ? "success" : "failure",
        };
        if (!condition(job.if, context)) { report.jobs[id] = { result: "skipped", outputs: {}, steps: {} }; continue; }
        await rm("/tmp/gh-aw", { force: true, recursive: true });
        await mkdir(workspace, { recursive: true });
        await mkdir(temp, { recursive: true });
        await mkdir(`${temp}/home`, { recursive: true });
        await writeFile(`${temp}/event.json`, JSON.stringify(event));
        context.status = "success";
        context.env = stringify(render(workflow.env, context));
        Object.assign(context.env, stringify(render(job.env, context)));
        let environment = {
          PATH: `/tools:${process.env.PATH}`, HOME: `${temp}/home`, CI: "true", GITHUB_ACTIONS: "true", RUNNER_TEMP: temp,
          RUNNER_TOOL_CACHE: "/tmp/tool-cache", RUNNER_OS: "Linux", GITHUB_WORKSPACE: workspace,
          GITHUB_REPOSITORY: github.repository, GITHUB_REPOSITORY_OWNER: github.repository_owner,
          GITHUB_REPOSITORY_ID: String(event.repository.id), GITHUB_REPOSITORY_OWNER_ID: String(event.repository.owner.id),
          GITHUB_ACTOR_ID: String(event.sender.id || 7), GITHUB_JOB: id, GITHUB_RUN_NUMBER: "1",
          GITHUB_WORKFLOW: github.workflow, GITHUB_WORKFLOW_REF: github.workflow_ref, GITHUB_WORKFLOW_SHA: config.sha,
          GITHUB_SHA: config.sha, GITHUB_REF: github.ref, GITHUB_REF_NAME: "main", GITHUB_RUN_ID: github.run_id, GITHUB_RUN_ATTEMPT: "1",
          GITHUB_ACTOR: github.actor, GITHUB_TRIGGERING_ACTOR: github.actor, GITHUB_EVENT_NAME: config.eventName,
          GITHUB_EVENT_PATH: `${temp}/event.json`, GITHUB_API_URL: github.api_url, GITHUB_SERVER_URL: github.server_url,
          SIMULATOR_GITHUB_API_URL: api.url,
          GITHUB_TOKEN: github.token, GH_TOKEN: github.token, ...stringify(context.env),
          GIT_CONFIG_NOSYSTEM: "1", GIT_CONFIG_GLOBAL: `${temp}/home/.gitconfig`, GIT_TERMINAL_PROMPT: "0",
        };
        const steps = {};
        for (let index = 0; index < job.steps.length; index++) {
          const step = job.steps[index], key = step.id || String(index);
          if (!condition(step.if, context)) {
            steps[key] = context.steps[key] = { outcome: "skipped", conclusion: "skipped", outputs: {} };
            continue;
          }
          const base = `/output/${id}-${index}`;
          const files = { GITHUB_OUTPUT: `${base}.outputs`, GITHUB_ENV: `${base}.env`, GITHUB_PATH: `${base}.path`, GITHUB_STEP_SUMMARY: `${base}.summary` };
          for (const file of Object.values(files)) await writeFile(file, "");
          const stepEnv = stringify(render(step.env, context));
          const stepContext = { ...context, env: { ...context.env, ...stepEnv } };
          let outcome = "success";
          const started = Date.now();
          try {
            await executeStep(step, render(step.with, stepContext), { ...environment, ...stepEnv, ...files }, stepContext, workspace, `${base}.log`);
          } catch (error) {
            outcome = "failure";
            await writeFile(`${base}.error`, error.message);
          }
          const conclusion = outcome === "failure" && evaluate(step["continue-on-error"] || false, context) ? "success" : outcome;
          steps[key] = context.steps[key] = { index, name: step.name, outcome, conclusion, durationMs: Date.now() - started,
            outputs: await fileCommands(files.GITHUB_OUTPUT) };
          const updates = await fileCommands(files.GITHUB_ENV);
          Object.assign(environment, updates);
          Object.assign(context.env, updates);
          const paths = (await readFile(files.GITHUB_PATH, "utf8")).trim();
          if (paths) environment.PATH = paths.split("\n").reverse().join(":") + ":" + environment.PATH;
          if (conclusion === "failure") context.status = "failure";
        }
        report.jobs[id] = { result: context.status, outputs: stringify(render(job.outputs, context)), steps };
      }
      if (!progress) throw new Error("Cyclic workflow job dependencies");
    }
    if (report.unhandled.length) throw new Error(`Unsupported GitHub API requests: ${report.unhandled.join(", ")}`);
    if (Object.values(report.jobs).some(job => job.result === "failure")) throw new Error("A simulated job failed; inspect step logs");
  } finally { await api.close(); }
}

try { await run(); }
catch (error) { report.error = error.message; process.exitCode = 1; }
finally {
  await cp("/tmp/artifacts", "/output/artifacts", { recursive: true }).catch(error => {
    if (error.code !== "ENOENT") throw error;
  });
  await writeFile("/output/report.json", JSON.stringify(report, null, 2));
}
