import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { access, copyFile, cp, mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { dirname, join, resolve } from "node:path";
import test from "node:test";
import { promisify } from "node:util";

const execute = promisify(execFile);
const failures = [
  ["server-build", "dashboard server build", "server-build.log"],
  ["canonical-database-bootstrap", "canonical database bootstrap", "database-bootstrap.log"],
  ["operational-database-bootstrap", "operational database bootstrap", "operational-database-bootstrap.log"],
  ["ingest", "fixture ingestion", "ingest.log"],
];

const failureCases = failures.flatMap((entry) => ["docker", "podman"].map((engine) => [...entry, engine]));
failureCases.push(...["docker", "podman"].map((engine) => [...failures[3], engine, true]));
failureCases.push([...failures[0], "podman", "missing"]);
for (const [stage, label, logName, engine, prebuilt = false] of failureCases) {
  test(`Azure local harness reports ${stage} failures before cleaning up (${engine}${prebuilt ? ", prebuilt" : ""})`, async () => {
    await mkdir(resolve(".tmp"), { recursive: true });
    const directory = await mkdtemp(resolve(".tmp/azure-local-harness-"));
    const script = join(directory, "scripts/azure-local/azure-local.sh");
    const tools = join(directory, "bin");
    const state = join(directory, "state");
    const trace = join(directory, "commands.log");
    const message = stage === "ingest"
      ? 'error: payload hash mismatch for "gh-aw-logs-records/subset.jsonl"'
      : `${stage} failed for regression test`;
    const executable = (path, content) => writeFile(path, `#!/usr/bin/env bash\nset -eu\n${content}`, { mode: 0o700 });
    try {
      await mkdir(dirname(script), { recursive: true });
      await copyFile(resolve("scripts/azure-local/azure-local.sh"), script);
      await copyFile(resolve("scripts/container-engine.sh"), join(directory, "scripts/container-engine.sh"));
      await copyFile(resolve("scripts/azure-local/platform.py"), join(dirname(script), "platform.py"));
      await mkdir(join(directory, "tests/e2e"), { recursive: true });
      await mkdir(join(directory, "tests/integration"), { recursive: true });
      await writeFile(join(directory, "tests/integration/azure-local-protocols.test.mjs"), "");
      await copyFile(resolve("tests/e2e/dashboard-server-fixture.mjs"), join(directory, "tests/e2e/dashboard-server-fixture.mjs"));
      await mkdir(join(directory, "server/testdata"), { recursive: true });
      await cp(resolve("server/testdata/deployed-subset"), join(directory, "server/testdata/deployed-subset"), { recursive: true });
      await mkdir(tools);
      await mkdir(join(directory, ".github/workflows"), { recursive: true });
      await writeFile(join(directory, ".github/workflows/cao.json"), '{"control-plane":{"web":{}}}\n');
      await copyFile(resolve(".github/workflows/cao.azure.json"), join(directory, ".github/workflows/cao.azure.json"));
      const func = join(directory, ".tmp/azure-local/tools/core-tools-4.15.1-linux-x64/func");
      await mkdir(dirname(func), { recursive: true });
      await executable(func, "exit 99\n");
      await executable(join(tools, "uname"), 'if [[ "$1" == -s ]]; then echo Linux; else echo x86_64; fi\n');
      for (const tool of ["curl", "unzip"]) {
        await executable(join(tools, tool), "exit 99\n");
      }
      await executable(join(tools, engine), `
echo "${engine} $*" >> "$HARNESS_TRACE"
if [[ "$1" == run ]]; then cp "$AZURE_LOCAL_STATE_DIR/runtime.env" "$HARNESS_SAVED_ENV"; fi
case "$1" in
  run)
    for argument in "$@"; do
      case "$argument" in
        *-postgres) echo postgres-id; exit 0 ;;
        *-azurite) echo azurite-id; exit 0 ;;
      esac
    done
    exit 99 ;;
  port) echo 127.0.0.1:12345 ;;
  exec)
    case "$3" in
      pg_isready) echo accepting ;;
    esac ;;
  logs) echo "$2 service output" ;;
esac
`);
      await executable(join(tools, "go"), `
echo "go $*" >> "$HARNESS_TRACE"
output=""
stage=""
while [[ "$#" -gt 0 ]]; do
  case "$1" in
    -o) output="$2"; shift ;;
    ./cmd/cao-dashboard) stage=server-build ;;
  esac
  shift
done
if [[ "$stage" == "$HARNESS_FAILURE" ]]; then
  echo "captured stdout"
  echo "$HARNESS_MESSAGE" >&2
  exit 42
fi
cp "$HARNESS_BINARY" "$output"
chmod +x "$output"
`);
      const binary = join(directory, "fake-dashboard");
      await executable(binary, `
echo "dashboard $*" >> "$HARNESS_TRACE"
cp "$(dirname "$0")/host.json" "$HARNESS_HOST"
case "$1" in
  database)
    test -n "$CAO_DATABASE_ADMIN_URL"
    while [[ "$#" -gt 0 ]]; do
      if [[ "$1" == "--store" ]]; then
        stage="$2-database-bootstrap"
        break
      fi
      shift
    done
    ;;
  ingest) stage=ingest ;;
  *) exit 99 ;;
esac
if [[ "$stage" == "$HARNESS_FAILURE" ]]; then
  echo "captured stdout"
  echo "$HARNESS_MESSAGE" >&2
  exit 42
fi
`);
      const binaryDirectory = join(directory, "test-binaries");
      if (prebuilt) {
        await mkdir(binaryDirectory);
        if (prebuilt !== "missing") await copyFile(binary, join(binaryDirectory, "cao-dashboard"));
      }
      // Readiness must succeed without contacting any real services.
      await executable(join(tools, "curl"), "echo 200\n");
      const result = await execute("bash", [script, prebuilt ? "run-services" : "run"], {
        env: {
          ...process.env,
          CAO_CONTAINER_ENGINE: engine,
          PATH: `${tools}:${process.env.PATH}`,
          AZURE_LOCAL_STATE_DIR: state,
          AZURE_LOCAL_WAIT_TIMEOUT: "1",
          AZURE_LOCAL_TEST_BINARIES: prebuilt ? binaryDirectory : "",
          HARNESS_TRACE: trace,
          HARNESS_FAILURE: stage,
          HARNESS_MESSAGE: message,
          HARNESS_BINARY: binary,
          HARNESS_HOST: join(directory, "host.json"),
          HARNESS_SAVED_ENV: join(directory, "saved-runtime.env"),
        },
        timeout: 10_000,
      }).then(
        (value) => ({ ...value, code: 0 }),
        (error) => ({ stdout: error.stdout, stderr: error.stderr, code: error.code }),
      );
      if (prebuilt === "missing") {
        assert.equal(result.code, 1, result.stderr);
        assert.match(result.stderr, /explicit test binary is missing or not executable/);
        const commands = await readFile(trace, "utf8");
        assert.doesNotMatch(commands, /go .* build /);
        for (const service of ["postgres", "azurite"]) {
          assert.match(commands, new RegExp(`${engine} rm -f ${service}-id`));
        }
        await assert.rejects(access(join(state, "runtime.env")), { code: "ENOENT" });
        return;
      }
      assert.equal(result.code, 42, result.stderr);
      assert.ok(result.stderr.includes(`azure-local: ${label} failed (exit 42)`), result.stderr);
      assert.ok(result.stderr.includes(message), result.stderr);
      assert.ok(result.stderr.includes("captured stdout"), result.stderr);
      const logPath = join(state, "logs", logName);
      assert.ok(result.stderr.includes(logPath), result.stderr);
      assert.equal(await readFile(logPath, "utf8"), `captured stdout\n${message}\n`);
      const commands = await readFile(trace, "utf8");
      assert.match(await readFile(join(directory, "saved-runtime.env"), "utf8"), new RegExp(`CAO_CONTAINER_ENGINE=${engine}`));
      assert.doesNotMatch(commands, /(?:docker|podman) .*redis/);
      assert.match(commands, /docker.io\/library\/postgres:16-alpine/);
      assert.equal((commands.match(/go .* build /g) || []).length, prebuilt ? 0 : 1,
        "explicit prebuilt executables must run without rebuilding");
      if (stage !== "server-build") {
        const host = JSON.parse(await readFile(join(directory, "host.json"), "utf8"));
        assert.deepEqual(host.customHandler.description, {
          defaultExecutablePath: "cao-dashboard",
          arguments: ["azure-functions"],
        });
        assert.equal(host.customHandler.enableForwardingHttpRequest, true);
        assert.equal(host.extensions.http.routePrefix, "");
      }

      if (stage === "canonical-database-bootstrap" || stage === "operational-database-bootstrap") {
        assert.match(commands, /dashboard database bootstrap --store canonical --namespace azure-local-[a-f0-9]{12}/);
      }
      if (stage === "operational-database-bootstrap") {
        assert.match(commands, /dashboard database bootstrap --store operational --namespace azure-local-[a-f0-9]{12}-operational/);
      }
      for (const service of ["postgres", "azurite"]) {
        assert.match(commands, new RegExp(`${engine} rm -f ${service}-id`));
        assert.equal(await readFile(join(state, "logs", `${service}.log`), "utf8"), `${service}-id service output\n`);
      }
      await assert.rejects(access(join(state, "runtime.env")), { code: "ENOENT" });
      await assert.rejects(access(join(state, "runtime.json")), { code: "ENOENT" });
      await assert.rejects(access(join(state, "function-app")), { code: "ENOENT" });
      assert.doesNotMatch(commands, /func start/);
      if (stage !== "ingest") assert.doesNotMatch(commands, /dashboard ingest/);
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  });
}

for (const original of ["docker", "podman", "legacy"]) {
  for (const action of ["stop", "logs", "wait"]) {
    test(`Azure ${action} restores ${original} engine instead of caller selection`, async () => {
      await mkdir(resolve(".tmp"), { recursive: true });
      const directory = await mkdtemp(resolve(".tmp/azure-engine-"));
      const state = join(directory, "state");
      const trace = join(directory, "commands.log");
      const engine = original === "legacy" ? "docker" : original;
      try {
        await mkdir(join(state, "logs"), { recursive: true });
        await writeFile(join(state, "profile"), "synthetic\n");
        for (const name of ["docker", "podman"]) {
          await writeFile(join(directory, name), `#!/bin/bash
printf '${name} %s\\n' "$*" >> "$HARNESS_TRACE"
if [[ "$1" == logs ]]; then echo service-output; fi
`, { mode: 0o700 });
        }
        await writeFile(join(directory, "curl"), "#!/bin/bash\nexit 0\n", { mode: 0o700 });
        await writeFile(join(state, "runtime.env"), [
          ...(original === "legacy" ? [] : [`CAO_CONTAINER_ENGINE=${original}`]),
          "POSTGRES_CONTAINER_ID=postgres-original",
          "AZURITE_CONTAINER_ID=azurite-original",
          "AZURITE_BLOB_PORT=12345",
          "FUNCTIONS_PORT=12346",
          "",
        ].join("\n"));
        await execute("bash", [resolve("scripts/azure-local/azure-local.sh"), action], {
          env: {
            ...process.env,
            PATH: `${directory}:${process.env.PATH}`,
            CAO_CONTAINER_ENGINE: engine === "docker" ? "podman" : "docker",
            AZURE_LOCAL_STATE_DIR: state,
            HARNESS_TRACE: trace,
          },
        });
        const commands = await readFile(trace, "utf8");
        assert.doesNotMatch(commands, new RegExp(`^${engine === "docker" ? "podman" : "docker"} `, "m"));
        if (action === "wait") assert.match(commands, new RegExp(`${engine} exec postgres-original pg_isready`));
        else assert.match(commands, new RegExp(`${engine} logs postgres-original`));
        if (action === "stop") assert.match(commands, new RegExp(`${engine} rm -f postgres-original`));
        else assert.doesNotMatch(commands, / rm /);
      } finally {
        await rm(directory, { recursive: true, force: true });
      }
    });
  }
}

test("Azure restart never removes original container IDs from the new engine", async () => {
  await mkdir(resolve(".tmp"), { recursive: true });
  const directory = await mkdtemp(resolve(".tmp/azure-restart-"));
  const state = join(directory, "state");
  const trace = join(directory, "commands.log");
  try {
    await mkdir(state);
    await writeFile(join(state, "profile"), "synthetic\n");
    for (const engine of ["docker", "podman"]) {
      await writeFile(join(directory, engine), `#!/bin/bash
echo '${engine}' "$@" >> "$HARNESS_TRACE"
if [[ '${engine}' == podman && "$1" == run ]]; then exit 42; fi
`, { mode: 0o700 });
    }
    await writeFile(join(state, "runtime.env"), "CAO_CONTAINER_ENGINE=docker\nPOSTGRES_CONTAINER_ID=old-postgres\nAZURITE_CONTAINER_ID=old-azurite\n");
    await assert.rejects(execute("bash", [resolve("scripts/azure-local/azure-local.sh"), "start"], {
      env: {
        ...process.env,
        PATH: `${directory}:${process.env.PATH}`,
        CAO_CONTAINER_ENGINE: "podman",
        AZURE_LOCAL_STATE_DIR: state,
        HARNESS_TRACE: trace,
      },
    }), { code: 42 });
    const commands = await readFile(trace, "utf8");
    assert.match(commands, /docker rm -f old-postgres/);
    assert.match(commands, /docker rm -f old-azurite/);
    assert.match(commands, /podman run /);
    assert.doesNotMatch(commands, /podman (?:rm|logs|inspect) .*old-/);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

test("Azure cleanup fails closed on invalid saved engine and retains recovery state", async () => {
  await mkdir(resolve(".tmp"), { recursive: true });
  const directory = await mkdtemp(resolve(".tmp/azure-invalid-engine-"));
  try {
    const runtime = join(directory, "runtime.env");
    await writeFile(join(directory, "profile"), "synthetic\n");
    const contents = "CAO_CONTAINER_ENGINE=invalid\nPOSTGRES_CONTAINER_ID=original\n";
    await writeFile(runtime, contents);
    await assert.rejects(execute("bash", [resolve("scripts/azure-local/azure-local.sh"), "stop"], {
      env: { ...process.env, CAO_CONTAINER_ENGINE: "docker", AZURE_LOCAL_STATE_DIR: directory },
    }), /must be docker or podman/);
    assert.equal(await readFile(runtime, "utf8"), contents);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});

for (const action of ["start", "stop"]) {
  for (const failure of ["offline", "removal-failed", "inventory-failed"]) {
    test(`Azure ${action} retains original recovery state on ${failure}`, async () => {
      await mkdir(resolve(".tmp"), { recursive: true });
      const directory = await mkdtemp(resolve(".tmp/azure-recovery-"));
      const state = join(directory, "state");
      const trace = join(directory, "commands.log");
      try {
        await mkdir(join(state, "function-app"), { recursive: true });
        await writeFile(join(state, "profile"), "synthetic\n");
        const runtime = "CAO_CONTAINER_ENGINE=docker\nPOSTGRES_CONTAINER_ID=old-postgres\nAZURITE_CONTAINER_ID=old-azurite\n";
        await writeFile(join(state, "runtime.env"), runtime);
        await writeFile(join(state, "runtime.json"), "{}\n");
        await writeFile(join(state, "function-app/owned"), "retain\n");
        await writeFile(join(directory, "docker"), `#!/bin/bash
echo docker "$@" >> "$HARNESS_TRACE"
case "$1" in
  inspect|rm) exit 41 ;;
  info) [[ "$HARNESS_FAILURE" != offline ]] ;;
  ps)
    if [[ "$HARNESS_FAILURE" == removal-failed ]]; then echo old-postgres; else exit 42; fi
    ;;
esac
`, { mode: 0o700 });
        await writeFile(join(directory, "podman"), '#!/bin/bash\necho podman "$@" >> "$HARNESS_TRACE"\nexit 0\n', { mode: 0o700 });
        await assert.rejects(execute("bash", [resolve("scripts/azure-local/azure-local.sh"), action], {
          env: {
            ...process.env,
            PATH: `${directory}:${process.env.PATH}`,
            CAO_CONTAINER_ENGINE: "podman",
            AZURE_LOCAL_STATE_DIR: state,
            HARNESS_TRACE: trace,
            HARNESS_FAILURE: failure,
          },
        }), /cannot confirm removal.*recovery state retained/);
        assert.equal(await readFile(join(state, "runtime.env"), "utf8"), runtime);
        assert.equal(await readFile(join(state, "runtime.json"), "utf8"), "{}\n");
        assert.equal(await readFile(join(state, "function-app/owned"), "utf8"), "retain\n");
        const commands = await readFile(trace, "utf8");
        assert.match(commands, /docker ps --all --quiet --no-trunc --filter id=old-postgres/);
        assert.match(commands, /docker ps --all --quiet --no-trunc --filter id=old-azurite/);
        assert.doesNotMatch(commands, /podman (?:run|rm|inspect|ps|logs)/);
      } finally {
        await rm(directory, { recursive: true, force: true });
      }
    });
  }
}

test("Azure stop accepts already absent containers only after successful inventory", async () => {
  await mkdir(resolve(".tmp"), { recursive: true });
  const directory = await mkdtemp(resolve(".tmp/azure-absent-"));
  const trace = join(directory, "commands.log");
  try {
    await mkdir(join(directory, "function-app"));
    await writeFile(join(directory, "profile"), "synthetic\n");
    await writeFile(join(directory, "runtime.env"), "CAO_CONTAINER_ENGINE=docker\nPOSTGRES_CONTAINER_ID=old-postgres\nAZURITE_CONTAINER_ID=old-azurite\n");
    await writeFile(join(directory, "runtime.json"), "{}\n");
    await writeFile(join(directory, "docker"), `#!/bin/bash
echo docker "$@" >> "$HARNESS_TRACE"
case "$1" in inspect|rm) exit 41 ;; ps) exit 0 ;; esac
`, { mode: 0o700 });
    await execute("bash", [resolve("scripts/azure-local/azure-local.sh"), "stop"], {
      env: {
        ...process.env,
        PATH: `${directory}:${process.env.PATH}`,
        CAO_CONTAINER_ENGINE: "podman",
        AZURE_LOCAL_STATE_DIR: directory,
        HARNESS_TRACE: trace,
      },
    });
    const commands = await readFile(trace, "utf8");
    assert.match(commands, /docker ps --all --quiet --no-trunc --filter id=old-postgres/);
    assert.match(commands, /docker ps --all --quiet --no-trunc --filter id=old-azurite/);
    for (const file of ["runtime.env", "runtime.json", "function-app"]) {
      await assert.rejects(access(join(directory, file)), { code: "ENOENT" });
    }
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});
