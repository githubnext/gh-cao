import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { basename, join } from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";

const root = fileURLToPath(new URL("../../", import.meta.url));
const execute = promisify(execFile);

export function localEndpoint(value, protocol) {
  const url = new URL(value);
  assert.equal(url.protocol, protocol);
  assert.equal(url.hostname, "127.0.0.1", "service tests require numeric loopback");
  assert.ok(url.port, "service tests require an explicit port");
  assert.equal(url.username, "");
  assert.equal(url.password, "");
  assert.equal(url.search, "");
  assert.equal(url.hash, "");
  return url;
}

export async function goCase(directory, packages, patterns, services = {}, runtime) {
  const env = Object.fromEntries([
    "PATH", "HOME", "TMPDIR", "GOPATH", "GOMODCACHE", "GOCACHE", "SDKROOT", "DEVELOPER_DIR",
  ].filter((name) => process.env[name] !== undefined).map((name) => [name, process.env[name]]));
  Object.assign(env, {
    GOTOOLCHAIN: "local", GOPROXY: "off", GOSUMDB: "off", GOVCS: "*:off",
    GOENV: "off", GOFLAGS: "-mod=readonly", DO_NOT_TRACK: "1", GH_TELEMETRY: "0",
    OTEL_SDK_DISABLED: "true",
  });
  if (services.postgres) {
    const url = new URL(runtime.postgresUrl);
    assert.equal(url.protocol, "postgres:");
    assert.equal(url.hostname, "127.0.0.1");
    assert.ok(url.port);
    assert.equal(url.search, "?sslmode=disable", "connection overrides are forbidden");
    env.POSTGRES_URL = url.href;
  }
  if (services.blob) {
    localEndpoint(runtime.storage.blobEndpoint, "http:");
    Object.assign(env, {
      CAO_AZURITE_BLOB_URL: runtime.storage.blobEndpoint,
      CAO_AZURITE_ACCOUNT: runtime.storage.account,
      CAO_AZURITE_KEY: runtime.storage.key,
    });
  }
  const binaries = process.env.AZURE_LOCAL_TEST_BINARIES;
  const options = { env, timeout: 240_000, maxBuffer: 8 << 20 };
  const pattern = patterns.join("|");
  let stdout;
  if (binaries) {
    const results = await Promise.all(packages.map((pkg) => execute(join(binaries, "test2json"), [
      "-t", "-p", pkg, join(binaries, directory || "root", `${basename(pkg)}.test`),
      "-test.run", pattern, "-test.count=1", "-test.timeout=3m", "-test.v=test2json",
    ], { ...options, cwd: `${root}${directory}/${pkg}` })));
    stdout = results.map((result) => result.stdout.trim()).join("\n");
  } else {
    ({ stdout } = await execute("go", [
      "test", "-race", ...packages, "-run", pattern, "-count=1", "-timeout=3m", "-json",
    ], { ...options, cwd: `${root}${directory}` }));
  }
  assertGoCoverage(stdout, packages, patterns);
}

export function assertGoCoverage(stdout, packages, patterns) {
  const events = stdout.trim().split("\n").map((line) => JSON.parse(line));
  for (const pattern of patterns) {
    const segments = pattern.split("/").map((part) => new RegExp(part));
    assert.ok(events.some((event) => event.Test && event.Action === "pass"
      && segments.every((segment, index) => {
        const part = event.Test.split("/")[index];
        return part !== undefined && segment.test(part);
      })), `selector ${pattern} must execute tests`);
  }
  for (const pkg of packages) {
    assert.ok(events.some((event) => event.Test && event.Action === "pass"
      && event.Package.endsWith(pkg.slice(1))), `package ${pkg} must execute tests`);
  }
  assert.deepEqual(events.filter((event) => event.Action === "skip"), [], "selected service coverage cannot skip");
}
