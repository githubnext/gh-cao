import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import test from "node:test";
import { promisify } from "node:util";

const execute = promisify(execFile);
const helper = resolve("scripts/container-engine.sh");

test("local service npm commands use the Podman Compose wrapper", async () => {
  const { scripts } = JSON.parse(await readFile(resolve("package.json"), "utf8"));
  for (const service of ["aspire", "lgtm"]) {
    for (const [action, args] of [["up", "up -d"], ["down", "down"]]) {
      assert.equal(scripts[`dashboard:server:${service}-${action}`],
        `bash scripts/container-compose.sh -f server/${service}-compose.yml ${args}`);
    }
  }
  assert.equal(scripts["dashboard:server:postgres-up"], "bash scripts/container-compose.sh -f server/compose.yml up -d postgres");
});

async function fixture(t, tools) {
  await mkdir(resolve(".tmp"), { recursive: true });
  const directory = await mkdtemp(resolve(".tmp/container-engine-"));
  t.after(() => rm(directory, { recursive: true, force: true }));
  const trace = join(directory, "trace");
  for (const [name, code] of Object.entries(tools)) {
    await writeFile(join(directory, name), `#!/bin/bash
printf '%s\\0' '${name}' "$@" >> "$TRACE"
${code}
`, { mode: 0o700 });
  }
  const env = { ...process.env, PATH: directory, TRACE: trace };
  env.CAO_CONTAINER_ENGINE = "docker";
  const run = (body, args = []) => execute("/bin/bash", ["-c", `source "$1"; shift; ${body}`, "test", helper, ...args], { env });
  return { directory, env, run, commands: async () => (await readFile(trace, "utf8")).split("\0").slice(0, -1) };
}

test("container commands always use Podman with exact arguments", async (t) => {
  const f = await fixture(t, { docker: "exit 0", podman: "exit 0" });
  await f.run('cao_container_engine_init && cao_container "$@"', ["run", "--name", "a b", ""]);
  assert.deepEqual(await f.commands(), ["podman", "run", "--name", "a b", ""]);
});

test("missing Podman never falls back to Docker", async (t) => {
  const f = await fixture(t, { docker: "exit 0", "docker-compose": "exit 0" });
  await assert.rejects(f.run("cao_container_engine_init"), /Podman is required.*no other engine will be used/);
  await assert.rejects(f.run("cao_container_compose up"), /Podman is required/);
  await assert.rejects(f.commands(), { code: "ENOENT" });
});

test("Podman compose forwards exact arguments", async (t) => {
  const f = await fixture(t, { docker: "exit 99", podman: "exit 0", "docker-compose": "exit 99" });
  await f.run('cao_container_compose "$@"', ["-f", "file with spaces.yml", "up", "-d"]);
  assert.deepEqual(await f.commands(), ["podman", "compose", "version", "podman", "compose", "-f", "file with spaces.yml", "up", "-d"]);
});

test("missing Podman Compose provider gives an actionable error without other engines", async (t) => {
  const f = await fixture(t, { podman: "exit 1", docker: "exit 0", "docker-compose": "exit 0" });
  await assert.rejects(f.run("cao_container_compose up"), /Podman Compose is unavailable; install a Compose provider/);
  assert.deepEqual(await f.commands(), ["podman", "compose", "version"]);
});

test("Compose wrapper preserves command failures without trying another provider", async (t) => {
  const f = await fixture(t, {
    podman: 'if [[ "$2" == version ]]; then exit 0; fi\nexit 17',
    docker: "exit 0",
    "docker-compose": "exit 0",
    dirname: 'echo "$SCRIPT_DIRECTORY"',
  });
  f.env.SCRIPT_DIRECTORY = resolve("scripts");
  await assert.rejects(execute("/bin/bash", [resolve("scripts/container-compose.sh"), "-f", "file with spaces.yml", "up"], { env: f.env }), { code: 17 });
  const commands = await f.commands();
  assert.ok(commands.includes("file with spaces.yml"));
  assert.ok(!commands.includes("docker"));
  assert.ok(!commands.includes("docker-compose"));
});

for (const remoteVariable of ["CONTAINER_HOST", "CONTAINER_CONNECTION", "CONTAINER_SSHKEY"]) {
  test(`Kusto Podman rejects ${remoteVariable}`, async (t) => {
    const f = await fixture(t, { podman: "exit 0", docker: "exit 0", uname: "echo Linux", dirname: 'echo "$SCRIPT_DIRECTORY"' });
    f.env[remoteVariable] = "remote";
    f.env.ACCEPT_EULA = "Y";
    f.env.SCRIPT_DIRECTORY = resolve("scripts");
    await assert.rejects(execute("/bin/bash", [resolve("scripts/test-kusto-emulator.sh")], { env: f.env }), /requires Linux Podman --remote=false/);
    assert.doesNotMatch((await f.commands()).join(" "), /podman |docker/);
  });
}

test("Kusto Podman is Linux-only and uses --remote=false for every engine call", async (t) => {
  const f = await fixture(t, {
    podman: 'if [[ "$2 $3" == "image inspect" ]]; then exit 1; fi\nexit 37',
    docker: "exit 99",
    uname: "echo Linux",
    dirname: 'echo "$SCRIPT_DIRECTORY"',
  });
  for (const name of ["CONTAINER_HOST", "CONTAINER_CONNECTION", "CONTAINER_SSHKEY"]) delete f.env[name];
  f.env.DOCKER_HOST = "unix:///var/run/docker.sock";
  f.env.ACCEPT_EULA = "Y";
  f.env.SCRIPT_DIRECTORY = resolve("scripts");
  await assert.rejects(execute("/bin/bash", [resolve("scripts/test-kusto-emulator.sh")], { env: f.env }), { code: 37 });
  const commands = await f.commands();
  for (let i = 0; i < commands.length; i++) {
    if (commands[i] === "podman") assert.equal(commands[i + 1], "--remote=false");
  }
  assert.ok(commands.includes("pull"));
  assert.ok(!commands.includes("docker"));
  await writeFile(join(f.directory, "uname"), "#!/bin/bash\necho Darwin\n", { mode: 0o700 });
  await assert.rejects(execute("/bin/bash", [resolve("scripts/test-kusto-emulator.sh")], { env: f.env }), /Podman machines are not supported/);
});

test("macOS setup installs Podman and its provider but not Docker or Colima", async (t) => {
  const f = await fixture(t, {
    dirname: 'echo "$SCRIPT_DIRECTORY"',
    uname: "echo Darwin",
    brew: 'if [[ "$1" == list ]]; then exit 1; fi\nif [[ "$1" == --prefix ]]; then echo "$FIXTURE_DIRECTORY"; fi',
    podman: "echo podman-version",
    go: "echo go-version",
    node: "echo node-version",
    npm: "echo npm-version",
    cat: 'exec /bin/cat "$@"',
  });
  f.env.SCRIPT_DIRECTORY = resolve("scripts");
  f.env.FIXTURE_DIRECTORY = f.directory;
  const result = await execute("/bin/bash", [resolve("scripts/setup-dashboard-server-macos.sh")], { env: f.env });
  const commands = await f.commands();
  assert.ok(commands.includes("podman-compose"));
  assert.ok(commands.includes("podman"));
  assert.ok(!commands.includes("docker"));
  assert.ok(!commands.includes("docker-compose"));
  assert.ok(!commands.includes("colima"));
  assert.match(result.stdout, /podman machine init/);
  assert.match(result.stdout, /podman machine start/);
  assert.match(result.stdout, /export PODMAN_COMPOSE_PROVIDER=podman-compose/);
  assert.doesNotMatch(result.stdout, /CAO_CONTAINER_ENGINE|colima/);
});

test("Compose subprocess rejects unavailable Podman Compose without fallback", async (t) => {
  const f = await fixture(t, {
    docker: "exit 0", podman: "exit 1", dirname: 'echo "$SCRIPT_DIRECTORY"',
  });
  f.env.SCRIPT_DIRECTORY = resolve("scripts");
  await assert.rejects(execute("/bin/bash", [resolve("scripts/container-compose.sh"), "up"], {
    env: f.env, timeout: 10_000,
  }), /Compose is unavailable/);
  assert.deepEqual(await f.commands(), ["dirname", resolve("scripts/container-compose.sh"), "podman", "compose", "version"]);
});

for (const provider of ["podman-compose", "missing-flags", "docker-compose-v2"]) {
  test(`backfill checks Podman Compose provider capabilities before creating resources (${provider})`, async (t) => {
    const f = await fixture(t, {
      podman: `
if [[ "$*" == "compose version --short" ]]; then echo "$PROVIDER_VERSION"; fi
if [[ "$*" == "compose up --help" ]]; then printf '%s\\n' "$PROVIDER_HELP"; fi
`,
      docker: "exit 99",
      dirname: 'echo "$SCRIPT_DIRECTORY"',
      bash: 'exec /bin/bash "$@"',
      grep: 'exec /usr/bin/grep "$@"',
      openssl: "exit 47",
    });
    f.env.SCRIPT_DIRECTORY = resolve("scripts");
    f.env.PROVIDER_VERSION = provider === "podman-compose" ? "1.5.0" : "2.38.2";
    f.env.PROVIDER_HELP = provider === "missing-flags" ? " --pull string" : " --wait\n --wait-timeout int\n --pull string";
    await assert.rejects(execute("/bin/bash", [resolve("scripts/test-dashboard-backfill.sh")], { env: f.env }), (error) => {
      if (provider === "docker-compose-v2") {
        assert.equal(error.code, 47, error.stderr);
      } else {
        assert.match(error.stderr, /Backfill requires a Compose v2 provider for podman compose/);
        assert.match(error.stderr, /PODMAN_COMPOSE_PROVIDER/);
      }
      return true;
    });
    const commands = await f.commands();
    assert.ok(!commands.includes("docker"));
    assert.ok(!commands.includes("run"));
    assert.ok(!commands.includes("-d"));
    assert.equal(commands.includes("openssl"), provider === "docker-compose-v2");
  });
}
