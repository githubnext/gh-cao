import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import test from "node:test";
import { promisify } from "node:util";

const execute = promisify(execFile);

for (const outcome of ["available", "rate-limit", "other-error"]) {
  test(`backfill harness handles OpenObserve ${outcome}`, async () => {
    const directory = await mkdtemp(join(tmpdir(), "backfill-harness-"));
    const trace = join(directory, "commands.log");
    try {
      await writeFile(join(directory, "docker"), `#!/usr/bin/env bash
set -eu
echo "$*" >> "$HARNESS_TRACE"
if [[ "$*" == *"pull openobserve"* ]]; then
  case "$HARNESS_PULL" in
    rate-limit) echo "toomanyrequests: Data limit exceeded" >&2; exit 1 ;;
    other-error) echo "manifest unknown" >&2; exit 1 ;;
  esac
elif [[ "$*" == *"port redis"* ]]; then
  echo 127.0.0.1:16379
elif [[ "$*" == *"port postgres"* ]]; then
  echo 127.0.0.1:15432
elif [[ "$*" == *"port openobserve"* ]]; then
  echo 127.0.0.1:15080
fi
`, { mode: 0o700 });
      await writeFile(join(directory, "curl"), "#!/usr/bin/env bash\nexit 0\n", { mode: 0o700 });
      await writeFile(join(directory, "go"), `#!/usr/bin/env bash
set -eu
echo "go $*" >> "$HARNESS_TRACE"
echo "go skip=\${CAO_BACKFILL_SKIP_OPENOBSERVE:-0} endpoint=\${CAO_BACKFILL_OPENOBSERVE_URL:+configured} traces=\${OTEL_EXPORTER_OTLP_TRACES_ENDPOINT:+configured} headers=\${OTEL_EXPORTER_OTLP_HEADERS:+configured}" >> "$HARNESS_TRACE"
test -n "$CAO_POSTGRES_URL"
test -n "$REDIS_URL"
`, { mode: 0o700 });
      const result = await execute("bash", [resolve("scripts/test-dashboard-backfill.sh")], {
        env: {
          ...process.env,
          PATH: `${directory}:${process.env.PATH}`,
          HARNESS_TRACE: trace,
          HARNESS_PULL: outcome,
          CAO_BACKFILL_REPORT_DIR: directory,
          CAO_BACKFILL_STRESS: "0",
          CAO_BACKFILL_OPENOBSERVE_URL: "http://127.0.0.1:1",
          OTEL_EXPORTER_OTLP_TRACES_ENDPOINT: "http://127.0.0.1:1/v1/traces",
          OTEL_EXPORTER_OTLP_HEADERS: "test-header=test-value",
        },
      }).then(
        (value) => ({ ...value, code: 0 }),
        (error) => ({ stderr: error.stderr, code: error.code }),
      );
      const commands = await readFile(trace, "utf8");
      assert.match(commands, /up -d --wait --wait-timeout 120 redis postgres/);
      assert.match(commands, /down --volumes --remove-orphans/);
      if (outcome === "other-error") {
        assert.notEqual(result.code, 0);
        assert.doesNotMatch(commands, /^go /m);
      } else {
        assert.equal(result.code, 0, result.stderr);
        assert.equal(commands.split("\n").filter((line) => line.startsWith("go skip=")).length, 2);
        assert.match(commands, /go -C server test \.\/internal\/server \.\/internal\/simulator .* -timeout=10m -v/);
        assert.match(commands, /go -C server test \.\/internal\/collect -count=1 -timeout=5m -v/);
        if (outcome === "rate-limit") {
          assert.match(result.stderr, /rate-limited; running backfill tests without telemetry verification/);
          assert.doesNotMatch(commands, /up .*openobserve/);
          assert.match(commands, /go skip=1 endpoint= traces= headers=/);
        } else {
          assert.match(commands, /up .*--pull never openobserve/);
          assert.match(commands, /go skip=0 endpoint=configured traces=configured headers=configured/);
        }
      }
    } finally {
      await rm(directory, { recursive: true, force: true });
    }
  });
}
