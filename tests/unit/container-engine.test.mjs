import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { join, resolve } from "node:path";
import test from "node:test";
import { promisify } from "node:util";

const execute = promisify(execFile);
const helper = resolve("scripts/container-engine.sh");

async function fixture(t, tools, selection) {
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
  delete env.CAO_CONTAINER_ENGINE;
  if (selection !== undefined) env.CAO_CONTAINER_ENGINE = selection;
  const run = (body, args = []) => execute("/bin/bash", ["-c", `source "$1"; shift; ${body}`, "test", helper, ...args], { env });
  return { directory, env, run, commands: async () => (await readFile(trace, "utf8")).split("\0").slice(0, -1) };
}

for (const selection of [undefined, "docker", "podman"]) {
  test(`container selection ${selection ?? "default"} forwards exact arguments`, async (t) => {
    const f = await fixture(t, { docker: "exit 0", podman: "exit 0" }, selection);
    await f.run('cao_container_engine_init && cao_container "$@"', ["run", "--name", "a b", ""]);
    assert.deepEqual(await f.commands(), [selection ?? "docker", "run", "--name", "a b", ""]);
  });
}

for (const selection of ["", "auto", "nerdctl", "Docker", "podman --remote"]) {
  test(`invalid engine '${selection}' fails closed`, async (t) => {
    const f = await fixture(t, { docker: "exit 0", podman: "exit 0" }, selection);
    await assert.rejects(f.run("cao_container_engine_init"), /must be docker or podman/);
    await assert.rejects(f.commands(), { code: "ENOENT" });
  });
}

for (const selection of [undefined, "docker", "podman"]) {
  test(`missing selected ${selection ?? "default"} engine never falls back`, async (t) => {
    const f = await fixture(t, { [selection === "podman" ? "docker" : "podman"]: "exit 0" }, selection);
    await assert.rejects(f.run("cao_container_engine_init"), /no alternate engine will be used/);
    await assert.rejects(f.commands(), { code: "ENOENT" });
  });
}

for (const engine of ["docker", "podman"]) {
  test(`${engine} compose forwards exact arguments`, async (t) => {
    const f = await fixture(t, { docker: "exit 0", podman: "exit 0", "docker-compose": "exit 99" }, engine);
    await f.run('cao_container_compose "$@"', ["-f", "file with spaces.yml", "up", "-d"]);
    assert.deepEqual(await f.commands(), [engine, "compose", "version", engine, "compose", "-f", "file with spaces.yml", "up", "-d"]);
  });
  test(`${engine} missing Compose gives actionable error`, async (t) => {
    const f = await fixture(t, { [engine]: "exit 1" }, engine);
    await assert.rejects(f.run("cao_container_compose up"), engine === "docker" ? /install docker compose or docker-compose/ : /install a Compose provider/);
  });
}

test("Docker uses legacy Compose only when its plugin is absent", async (t) => {
  const f = await fixture(t, { docker: "exit 1", "docker-compose": "exit 0", podman: "exit 99" });
  await f.run('cao_container_compose "$@"', ["logs", "--no-color"]);
  assert.deepEqual(await f.commands(), ["docker", "compose", "version", "docker-compose", "logs", "--no-color"]);
});

test("Podman never invokes Docker Compose directly when its provider fails", async (t) => {
  const f = await fixture(t, { podman: "exit 1", docker: "exit 0", "docker-compose": "exit 0" }, "podman");
  await assert.rejects(f.run("cao_container_compose up"), /Podman Compose is unavailable/);
  assert.deepEqual(await f.commands(), ["podman", "compose", "version"]);
});

test("Compose wrapper preserves command failures without trying another provider", async (t) => {
  const f = await fixture(t, {
    docker: 'if [[ "$2" == version ]]; then exit 0; fi\nexit 17',
    "docker-compose": "exit 0",
    dirname: 'echo "$SCRIPT_DIRECTORY"',
  });
  f.env.SCRIPT_DIRECTORY = resolve("scripts");
  await assert.rejects(execute("/bin/bash", [resolve("scripts/container-compose.sh"), "-f", "file with spaces.yml", "up"], { env: f.env }), { code: 17 });
  const commands = await f.commands();
  assert.ok(commands.includes("file with spaces.yml"));
  assert.ok(!commands.includes("docker-compose"));
});

for (const remoteVariable of ["CONTAINER_HOST", "CONTAINER_CONNECTION", "CONTAINER_SSHKEY", "DOCKER_HOST", "DOCKER_CONTEXT"]) {
  test(`Kusto Podman rejects ${remoteVariable}`, async (t) => {
    const f = await fixture(t, { podman: "exit 0", uname: "echo Linux", dirname: 'echo "$SCRIPT_DIRECTORY"' }, "podman");
    f.env[remoteVariable] = "remote";
    f.env.ACCEPT_EULA = "Y";
    f.env.SCRIPT_DIRECTORY = resolve("scripts");
    await assert.rejects(execute("/bin/bash", [resolve("scripts/test-kusto-emulator.sh")], { env: f.env }), /requires Linux Podman --remote=false/);
    assert.doesNotMatch((await f.commands()).join(" "), /podman /);
  });
}

test("Kusto Podman is Linux-only and uses --remote=false for every engine call", async (t) => {
  const f = await fixture(t, {
    podman: 'if [[ "$2 $3" == "image inspect" ]]; then exit 1; fi\nexit 37',
    uname: "echo Linux",
    dirname: 'echo "$SCRIPT_DIRECTORY"',
  }, "podman");
  for (const name of ["CONTAINER_HOST", "CONTAINER_CONNECTION", "CONTAINER_SSHKEY", "DOCKER_HOST", "DOCKER_CONTEXT"]) delete f.env[name];
  f.env.ACCEPT_EULA = "Y";
  f.env.SCRIPT_DIRECTORY = resolve("scripts");
  await assert.rejects(execute("/bin/bash", [resolve("scripts/test-kusto-emulator.sh")], { env: f.env }), { code: 37 });
  const commands = await f.commands();
  for (let i = 0; i < commands.length; i++) {
    if (commands[i] === "podman") assert.equal(commands[i + 1], "--remote=false");
  }
  assert.ok(commands.includes("pull"));
  await writeFile(join(f.directory, "uname"), "#!/bin/bash\necho Darwin\n", { mode: 0o700 });
  await assert.rejects(execute("/bin/bash", [resolve("scripts/test-kusto-emulator.sh")], { env: f.env }), /Podman machines are not supported/);
});

test("Kusto default Docker retains explicit local socket binding and rejects TCP", async (t) => {
  const f = await fixture(t, {
    docker: 'if [[ "$*" == *"image inspect"* ]]; then exit 1; fi\nexit 37',
    dirname: 'echo "$SCRIPT_DIRECTORY"',
  });
  f.env.ACCEPT_EULA = "Y";
  f.env.SCRIPT_DIRECTORY = resolve("scripts");
  delete f.env.DOCKER_CONTEXT;
  f.env.DOCKER_HOST = "unix:///var/run/docker.sock";
  await assert.rejects(execute("/bin/bash", [resolve("scripts/test-kusto-emulator.sh")], { env: f.env }), { code: 37 });
  assert.deepEqual((await f.commands()).filter((value) => value === "--host"), ["--host", "--host"]);
  f.env.DOCKER_HOST = "tcp://127.0.0.1:2375";
  await assert.rejects(execute("/bin/bash", [resolve("scripts/test-kusto-emulator.sh")], { env: f.env }), /requires a local Unix Docker socket/);
});

test("macOS Podman setup installs its provider but not Docker or Colima", async (t) => {
  const f = await fixture(t, {
    dirname: 'echo "$SCRIPT_DIRECTORY"',
    uname: "echo Darwin",
    brew: 'if [[ "$1" == list ]]; then exit 1; fi\nif [[ "$1" == --prefix ]]; then echo "$FIXTURE_DIRECTORY"; fi',
    podman: "echo podman-version",
    go: "echo go-version",
    node: "echo node-version",
    npm: "echo npm-version",
    cat: 'exec /bin/cat "$@"',
  }, "podman");
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
  assert.match(result.stdout, /export CAO_CONTAINER_ENGINE=podman/);
});

for (const selection of [undefined, "docker", "podman"]) {
  test(`MCP subprocess skips unavailable ${selection ?? "default Docker"} without fallback`, async (t) => {
    const f = await fixture(t, { docker: "exit 1", podman: "exit 1" }, selection);
    const result = await execute(process.execPath, [resolve("tests/e2e/cao-mcp-container.mjs")], {
      env: { ...f.env, TMPDIR: f.directory },
      timeout: 10_000,
    });
    const engine = selection ?? "docker";
    assert.equal(result.stdout, `Skipping CAO MCP container test: ${engine} is unavailable.\n`);
    assert.deepEqual(await f.commands(), [engine, "info"]);
  });
}

for (const selection of ["", "auto", "nerdctl"]) {
  test(`MCP subprocess rejects invalid engine '${selection}' before invoking engines`, async (t) => {
    const f = await fixture(t, { docker: "exit 1", podman: "exit 1" }, selection);
    await assert.rejects(execute(process.execPath, [resolve("tests/e2e/cao-mcp-container.mjs")], {
      env: { ...f.env, TMPDIR: f.directory },
      timeout: 10_000,
    }), /CAO_CONTAINER_ENGINE must be docker or podman/);
    await assert.rejects(f.commands(), { code: "ENOENT" });
  });
}

test("MCP subprocess skips missing Podman without trying installed Docker", async (t) => {
  const f = await fixture(t, { docker: "exit 1" }, "podman");
  const result = await execute(process.execPath, [resolve("tests/e2e/cao-mcp-container.mjs")], {
    env: { ...f.env, TMPDIR: f.directory },
    timeout: 10_000,
  });
  assert.equal(result.stdout, "Skipping CAO MCP container test: podman is unavailable.\n");
  await assert.rejects(f.commands(), { code: "ENOENT" });
});

for (const provider of ["podman-compose", "missing-flags", "docker-compose-v2"]) {
  test(`backfill checks Podman provider capabilities before creating resources (${provider})`, async (t) => {
    const f = await fixture(t, {
      podman: `
if [[ "$*" == "compose version --short" ]]; then echo "$PROVIDER_VERSION"; fi
if [[ "$*" == "compose up --help" ]]; then printf '%s\\n' "$PROVIDER_HELP"; fi
`,
      docker: "exit 99",
      dirname: 'echo "$SCRIPT_DIRECTORY"',
      bash: 'exec /bin/bash "$@"',
      grep: 'exec /bin/grep "$@"',
      openssl: "exit 47",
    }, "podman");
    f.env.SCRIPT_DIRECTORY = resolve("scripts");
    f.env.PROVIDER_VERSION = provider === "podman-compose" ? "1.5.0" : "2.38.2";
    f.env.PROVIDER_HELP = provider === "missing-flags" ? " --pull string" : " --wait\n --wait-timeout int\n --pull string";
    await assert.rejects(execute("/bin/bash", [resolve("scripts/test-dashboard-backfill.sh")], { env: f.env }), (error) => {
      if (provider === "docker-compose-v2") {
        assert.equal(error.code, 47, error.stderr);
      } else {
        assert.match(error.stderr, /Backfill requires a Docker Compose v2 provider/);
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
