import assert from "node:assert/strict";
import { execFile, spawn } from "node:child_process";
import { access, copyFile, mkdir, mkdtemp, readFile, rm, stat, symlink, writeFile } from "node:fs/promises";
import { createServer } from "node:net";
import { dirname, join, resolve } from "node:path";
import test from "node:test";
import { promisify } from "node:util";
import { githubProfile, validateEnvironment, writeSettings } from "../../scripts/azure-local/profile.mjs";

const execute = promisify(execFile);
const root = resolve(".");
const scratch = join(root, ".tmp/azure-local-unit");
const credentials = {
  CAO_GITHUB_CLIENT_ID: "regression-client",
  CAO_GITHUB_CLIENT_SECRET: 'regression-secret-"\\\n-not-a-real-credential',
  CAO_GITHUB_ALLOWED_ORGS: "githubnext, example",
};

async function directory() {
  await mkdir(scratch, { recursive: true });
  return mkdtemp(join(scratch, "dev-"));
}

test("GitHub Azure profile requires explicit credentials, orgs and exact loopback callback", () => {
  assert.equal(githubProfile(credentials).redirect, "http://localhost:7071/auth/callback");
  assert.equal(githubProfile({ ...credentials, AZURE_LOCAL_FUNCTIONS_PORT: "17071" }).port, "17071");
  for (const invalid of [
    { CAO_GITHUB_CLIENT_ID: "" },
    { CAO_GITHUB_CLIENT_SECRET: "local-client-secret" },
    { CAO_GITHUB_ALLOWED_ORGS: "" },
    { CAO_GITHUB_ALLOWED_ORGS: "*" },
    { CAO_GITHUB_ALLOWED_ORGS: "example," },
    { CAO_GITHUB_ALLOWED_ORGS: "owner/repo" },
    { AZURE_LOCAL_FUNCTIONS_PORT: "0" },
    { AZURE_LOCAL_FUNCTIONS_PORT: "65536" },
    { AZURE_LOCAL_FUNCTIONS_PORT: "7071/evil" },
    { CAO_GITHUB_REDIRECT_URL: "http://127.0.0.1:7071/auth/callback" },
    { CAO_GITHUB_REDIRECT_URL: "http://127.0.0.1:7071/other" },
    { CAO_GITHUB_REDIRECT_URL: "https://example.com/auth/callback" },
    { CAO_SESSION_SECRET: "short" },
    { CAO_SESSION_SECRET: " ".repeat(32) },
    { AZURE_LOCAL_SOURCE_DIRECTORY: "relative" },
    { AZURE_LOCAL_SOURCE_DIRECTORY: join(root, "nonexistent-snapshot") },
  ]) {
    assert.throws(() => githubProfile({ ...credentials, ...invalid }));
  }
  for (const name of ["CAO_GITHUB_LOCAL_USER", "CAO_COLLECT_ENABLED", "CAO_POLICY_PATH", "CAO_GITHUB_ALLOWED_USERS"]) {
    assert.throws(() => validateEnvironment({ ...credentials, [name]: "unexpected" }), /unset other CAO/);
  }
  assert.throws(() => validateEnvironment({ ...credentials, GITHUB_SERVER_URL: "https://github.example.com" }), /GitHub.com only/);
});

test("GitHub settings safely serialize credentials and use built server queries", async () => {
  const dir = await directory();
  try {
    const path = join(dir, "local.settings.json");
    await writeFile(path, JSON.stringify({ Values: { FUNCTIONS_WORKER_RUNTIME: "custom" } }));
    writeSettings(path, "github", credentials);
    const first = JSON.parse(await readFile(path, "utf8"));
    assert.equal(first.Values.CAO_GITHUB_CLIENT_SECRET, credentials.CAO_GITHUB_CLIENT_SECRET);
    assert.equal(first.Values.CAO_GITHUB_ALLOWED_ORGS, "githubnext,example");
    assert.equal(first.Values.CAO_AZURE_DASHBOARD_QUERIES, join(dir, "site/dashboard.json"));
    assert.equal(first.Values.FUNCTIONS_WORKER_RUNTIME, "custom");
    assert.match(first.Values.CAO_SESSION_SECRET, /^[a-f\d]{64}$/);
    assert.equal((await stat(path)).mode & 0o777, 0o600);
    writeSettings(path, "github", credentials);
    assert.notEqual(JSON.parse(await readFile(path, "utf8")).Values.CAO_SESSION_SECRET, first.Values.CAO_SESSION_SECRET);
  } finally {
    await rm(dir, { recursive: true, force: true });
  }
});

test("GitHub stack uses built assets, explicit or empty evidence, isolated environment and exact teardown", async () => {
  const dir = await directory();
  const script = join(dir, "scripts/azure-local/azure-local.sh");
  const state = join(dir, "state");
  const tools = join(dir, "bin");
  const trace = join(dir, "commands.log");
  const executable = (path, content) => writeFile(path, `#!/usr/bin/env bash\nset -eu\n${content}`, { mode: 0o700 });
  const port = await new Promise((resolvePort) => {
    const listener = createServer();
    listener.listen(0, "127.0.0.1", () => {
      const { port } = listener.address();
      listener.close(() => resolvePort(String(port)));
    });
  });
  const env = {
    PATH: `${tools}:${process.env.PATH}`,
    HOME: process.env.HOME,
    ...credentials,
    AZURE_LOCAL_STATE_DIR: state,
    AZURE_LOCAL_PROFILE: "github",
    AZURE_LOCAL_FUNCTIONS_PORT: port,
    AZURE_LOCAL_WAIT_TIMEOUT: "2",
    UNRELATED_SECRET: "must-not-reach-functions",
  };
  const run = (command, environment = env) => execute("bash", [script, command], { env: environment, timeout: 15_000 });
  try {
    await mkdir(dirname(script), { recursive: true });
    await copyFile(join(root, "scripts/azure-local/azure-local.sh"), script);
    await copyFile(join(root, "scripts/azure-local/profile.mjs"), join(dirname(script), "profile.mjs"));
    await copyFile(join(root, "scripts/azure-local/platform.py"), join(dirname(script), "platform.py"));
    await mkdir(join(dir, ".github/workflows"), { recursive: true });
    for (const file of ["cao.json", "cao.azure.json"]) {
      await copyFile(join(root, ".github/workflows", file), join(dir, ".github/workflows", file));
    }
    await mkdir(tools);
    const platform = `${process.platform === "darwin" ? "osx" : "linux"}-${process.arch === "arm64" ? "arm64" : "x64"}`;
    const func = join(dir, `.tmp/azure-local/tools/core-tools-4.15.1-${platform}/func`);
    await mkdir(dirname(func), { recursive: true });
    await symlink(process.execPath, func);
    await executable(join(tools, "docker"), `
echo "docker $*" >> "${trace}"
case "$1" in
  run) case "$*" in *-postgres*) echo postgres-id ;; *) echo azurite-id ;; esac ;;
  port) echo 127.0.0.1:12345 ;;
esac
`);
    await executable(join(tools, "curl"), "echo 200\n");
    await executable(join(tools, "npm"), `
echo "npm $*" >> "${trace}"
if [[ "\${HARNESS_FAIL_BUILD:-}" == 1 ]]; then
  echo "$CAO_GITHUB_CLIENT_SECRET"
  exit 42
fi
if [[ "\${HARNESS_HOLD_BUILD:-}" == 1 ]]; then
  echo waiting > "${dir}/build-blocked"
  sleep 30
fi
while [[ "$1" != -- ]]; do shift; done
mkdir -p "$2"
echo '<title>built dashboard</title>' > "$2/index.html"
echo '{"dashboard":{"queries":[]}}' > "$2/dashboard.json"
`);
    await executable(join(tools, "go"), `
while [[ "$1" != -o ]]; do shift; done
cp "${dir}/dashboard" "$2"
cp "${dir}/host.mjs" "$(dirname "$2")/start"
`);
    await executable(join(dir, "dashboard"), `echo "dashboard $*" >> "${trace}"\n`);
    await writeFile(join(dir, "host.mjs"), `
import { writeFileSync } from "node:fs";
writeFileSync("${dir}/host-observed.json", JSON.stringify({ args: process.argv.slice(2), env: process.env }));
setInterval(() => {}, 1000);
`);
    for (const source of [undefined, join(dir, "snapshot")]) {
      if (source) await mkdir(source);
      const selectedEnv = { ...env, ...(source ? { AZURE_LOCAL_SOURCE_DIRECTORY: source } : {}) };
      await run("start", selectedEnv);
      await run("wait", selectedEnv);
      // The stub host runs a real process but does not emulate Functions or Docker.
      for (let attempt = 0; attempt < 50; attempt++) {
        try { await access(join(dir, "host-observed.json")); break; }
        catch { await new Promise((done) => setTimeout(done, 20)); }
      }
      const settings = JSON.parse(await readFile(join(state, "function-app/local.settings.json"), "utf8")).Values;
      assert.equal(settings.CAO_GITHUB_REDIRECT_URL, `http://localhost:${port}/auth/callback`);
      assert.equal(settings.CAO_GITHUB_CLIENT_SECRET, credentials.CAO_GITHUB_CLIENT_SECRET);
      assert.equal(settings.CAO_AZURE_LOCAL_SIMULATION, "1");
      assert.equal(settings.CAO_AZURE_ALLOWED_HOSTS, "127.0.0.1,localhost");
      assert.match(await readFile(join(state, "function-app/site/index.html"), "utf8"), /built dashboard/);
      assert.equal(JSON.parse(await readFile(join(state, "runtime.json"), "utf8")).profile, "github");
      assert.equal(JSON.parse(await readFile(join(state, "runtime.json"), "utf8")).baseUrl, `http://localhost:${port}`);
      const observed = JSON.parse(await readFile(join(dir, "host-observed.json"), "utf8"));
      assert.deepEqual(observed.args, ["--address", "127.0.0.1", "--port", port]);
      assert.equal(observed.env.UNRELATED_SECRET, undefined);
      assert.equal(observed.env.CAO_GITHUB_CLIENT_SECRET, undefined, "Core Tools loads private settings itself");
      assert.equal(observed.env.HOME, join(state, "function-app/.home"));
      assert.equal(observed.env.TMPDIR, join(state, "function-app/.scratch"));
      assert.equal((await stat(state)).mode & 0o777, 0o700);
      assert.equal((await stat(join(state, "function-app/local.settings.json"))).mode & 0o777, 0o600);
      assert.equal((await stat(join(state, "runtime.env"))).mode & 0o777, 0o600);
      const output = await run("logs", selectedEnv);
      assert.doesNotMatch(output.stdout + output.stderr, /regression-secret/);
      await assert.rejects(run("test", selectedEnv), /synthetic profile/);
      await assert.rejects(run("start", selectedEnv), /stop it explicitly/);
      const synthetic = { PATH: env.PATH, AZURE_LOCAL_STATE_DIR: state };
      await assert.rejects(run("stop", synthetic), /different or invalid profile/);
      await assert.rejects(run("start", synthetic), /different or invalid profile/);
      await assert.rejects(run("logs", synthetic), /different or invalid profile/);
      await assert.rejects(run("run", selectedEnv), /synthetic profile/);
      await access(join(state, "runtime.env"));
      const commands = await readFile(trace, "utf8");
      assert.ok(commands.includes(`dashboard ingest --source ${source ?? join(dir, "server/azure/empty-data")}`));
      assert.doesNotMatch(commands, /fixture/);
      for (const name of ["home", "scratch"]) {
        await mkdir(join(state, name), { recursive: true });
        await writeFile(join(state, name, "operator-owned"), "must survive cleanup\n");
      }
      await run("stop", selectedEnv);
      await run("stop", selectedEnv);
      for (const name of ["home", "scratch"]) {
        assert.equal(await readFile(join(state, name, "operator-owned"), "utf8"), "must survive cleanup\n");
      }
      assert.equal(await readFile(join(state, "profile"), "utf8"), "github\n");
      await assert.rejects(run("logs", synthetic), /different or invalid profile/);
      await assert.rejects(run("start", synthetic), /different or invalid profile/);
      await assert.rejects(access(join(state, "runtime.env")), { code: "ENOENT" });
      await assert.rejects(access(join(state, "function-app")), { code: "ENOENT" });
      await rm(join(dir, "host-observed.json"));
    }
    const commands = await readFile(trace, "utf8");
    assert.equal((commands.match(/docker rm -f postgres-id/g) ?? []).length, 2);
    assert.equal((commands.match(/docker rm -f azurite-id/g) ?? []).length, 2);
    const { CAO_GITHUB_CLIENT_SECRET: omitted, ...missingSecret } = env;
    assert.ok(omitted);
    await assert.rejects(run("dev", missingSecret), /requires real GitHub.com OAuth credentials/);
    assert.equal(await readFile(trace, "utf8"), commands, "missing credentials must not touch services");
    const occupied = createServer();
    await new Promise((done) => occupied.listen(Number(port), "127.0.0.1", done));
    try {
      await assert.rejects(run("start"), /requested Functions port is unavailable/);
      assert.equal(await readFile(trace, "utf8"), commands, "a busy port must not touch services");
    } finally {
      await new Promise((done) => occupied.close(done));
    }
    const failure = await run("start", { ...env, HARNESS_FAIL_BUILD: "1" }).then(
      () => assert.fail("expected build failure"),
      (error) => error,
    );
    assert.equal(failure.code, 42);
    assert.match(failure.stderr, /dashboard site build failed/);
    assert.doesNotMatch(failure.stdout + failure.stderr, /regression-secret/);
    assert.match(await readFile(join(state, "logs/site-build.log"), "utf8"), /regression-secret/);
    assert.equal((await stat(join(state, "logs/site-build.log"))).mode & 0o777, 0o600);
    await assert.rejects(access(join(state, "runtime.env")), { code: "ENOENT" });
    const syntheticAfterFailure = { PATH: env.PATH, AZURE_LOCAL_STATE_DIR: state };
    for (const command of ["logs", "start", "stop"]) {
      const rejected = await run(command, syntheticAfterFailure).then(
        () => assert.fail("stopped GitHub logs must retain their profile boundary"),
        (error) => error,
      );
      assert.match(rejected.stderr, /different or invalid profile/);
      assert.doesNotMatch(rejected.stdout + rejected.stderr, /regression-secret/);
    }
    await writeFile(join(state, "runtime.env"), "STATE_PROFILE=synthetic\nPOSTGRES_CONTAINER_ID=do-not-remove\n");
    const before = await readFile(trace, "utf8");
    await assert.rejects(run("dev"), /different profile/);
    assert.doesNotMatch((await readFile(trace, "utf8")).slice(before.length), /docker rm/);
    await access(join(state, "runtime.env"));
    await rm(join(state, "runtime.env"));
    const startup = spawn("bash", [script, "start"], {
      env: { ...env, HARNESS_HOLD_BUILD: "1" },
      detached: true,
      stdio: "ignore",
    });
    const completion = new Promise((done, reject) => {
      startup.once("error", reject);
      startup.once("exit", (code, signal) => done({ code, signal }));
    });
    try {
      for (let attempt = 0; attempt < 100; attempt++) {
        try { await access(join(dir, "build-blocked")); break; }
        catch { await new Promise((done) => setTimeout(done, 20)); }
      }
      await access(join(dir, "build-blocked"));
      process.kill(-startup.pid, "SIGTERM");
      assert.deepEqual(await completion, { code: 143, signal: null });
      await assert.rejects(access(join(state, "runtime.env")), { code: "ENOENT" });
      await assert.rejects(access(join(state, "function-app")), { code: "ENOENT" });
      const afterCancellation = (await readFile(trace, "utf8")).slice(before.length);
      assert.match(afterCancellation, /docker rm -f postgres-id --volumes/);
      assert.match(afterCancellation, /docker rm -f azurite-id --volumes/);
      await rm(join(state, "profile"));
      await assert.rejects(run("logs", syntheticAfterFailure), /existing unmarked state/);
      await assert.rejects(run("start"), /existing unmarked state/);
      await writeFile(join(state, "profile"), "github\n");
    } finally {
      if (startup.exitCode === null && startup.signalCode === null) {
        process.kill(-startup.pid, "SIGKILL");
        await completion;
      }
    }
  } finally {
    await run("stop").catch(() => {});
    await rm(dir, { recursive: true, force: true });
  }
});
