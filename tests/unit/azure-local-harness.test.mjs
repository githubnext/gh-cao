import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { access, copyFile, cp, mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
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

for (const [stage, label, logName] of failures) {
  test(`Azure local harness reports ${stage} failures before cleaning up`, async () => {
    const directory = await mkdtemp(join(tmpdir(), "azure-local-harness-"));
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
      await mkdir(join(directory, "tests/e2e"), { recursive: true });
      await copyFile(resolve("tests/e2e/dashboard-server-fixture.mjs"), join(directory, "tests/e2e/dashboard-server-fixture.mjs"));
      await mkdir(join(directory, "server/testdata"), { recursive: true });
      await cp(resolve("server/testdata/deployed-subset"), join(directory, "server/testdata/deployed-subset"), { recursive: true });
      await mkdir(tools);
      await mkdir(join(directory, ".github/workflows"), { recursive: true });
      await writeFile(join(directory, ".github/workflows/cao.json"), '{"control-plane":{"web":{}}}\n');
      await copyFile(resolve(".github/workflows/cao.azure.json"), join(directory, ".github/workflows/cao.azure.json"));
      const func = join(directory, ".tmp/azure-local/tools/core-tools-4.15.1/func");
      await mkdir(dirname(func), { recursive: true });
      await executable(func, "exit 99\n");
      await executable(join(tools, "uname"), "echo Linux\n");
      await executable(join(tools, "sha256sum"), "cat >/dev/null\necho 0123456789abcdef\n");
      await executable(join(tools, "python3"), 'if [[ "$1" == -c ]]; then echo bG9jYWw=; else cat >/dev/null; echo 12345; fi\n');
      for (const tool of ["curl", "flock", "setsid", "unzip"]) {
        await executable(join(tools, tool), "exit 99\n");
      }
      await executable(join(tools, "docker"), `
echo "docker $*" >> "$HARNESS_TRACE"
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
      // Readiness must succeed without contacting any real services.
      await executable(join(tools, "curl"), "echo 200\n");
      const result = await execute("bash", [script, "run"], {
        env: {
          ...process.env,
          PATH: `${tools}:${process.env.PATH}`,
          AZURE_LOCAL_STATE_DIR: state,
          AZURE_LOCAL_WAIT_TIMEOUT: "1",
          HARNESS_TRACE: trace,
          HARNESS_FAILURE: stage,
          HARNESS_MESSAGE: message,
          HARNESS_BINARY: binary,
          HARNESS_HOST: join(directory, "host.json"),
        },
        timeout: 10_000,
      }).then(
        (value) => ({ ...value, code: 0 }),
        (error) => ({ stdout: error.stdout, stderr: error.stderr, code: error.code }),
      );
      assert.equal(result.code, 42, result.stderr);
      assert.ok(result.stderr.includes(`azure-local: ${label} failed (exit 42)`), result.stderr);
      assert.ok(result.stderr.includes(message), result.stderr);
      assert.ok(result.stderr.includes("captured stdout"), result.stderr);
      const logPath = join(state, "logs", logName);
      assert.ok(result.stderr.includes(logPath), result.stderr);
      assert.equal(await readFile(logPath, "utf8"), `captured stdout\n${message}\n`);
      const commands = await readFile(trace, "utf8");
      assert.doesNotMatch(commands, /docker .*redis/);
      assert.equal((commands.match(/go .* build /g) || []).length, 1, "the shared dashboard executable is built once");
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
        assert.match(commands, new RegExp(`docker rm -f ${service}-id`));
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
