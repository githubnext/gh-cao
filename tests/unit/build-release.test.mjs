import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { delimiter, dirname, join, resolve } from "node:path";
import test from "node:test";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "../..");
const script = join(root, "scripts/build-release.sh");
const platforms = [
  "gh-cao-darwin-amd64", "gh-cao-darwin-arm64", "gh-cao-linux-amd64", "gh-cao-linux-arm64",
  "gh-cao-windows-amd64.exe", "gh-cao-windows-arm64.exe",
];

function fixture(t) {
  const directory = mkdtempSync(join(tmpdir(), "cao-release-build-"));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const bin = join(directory, "bin");
  mkdirSync(bin);
  mkdirSync(join(directory, "scripts"));
  const fixtureScript = join(directory, "scripts/build-release.sh");
  writeFileSync(fixtureScript, readFileSync(script));
  writeFileSync(join(directory, "scripts/generate-dashboard-ingest.mjs"),
    'if (process.argv[2] !== "--check") throw new Error("release must check the embedded normalizer");\n');
  writeFileSync(join(bin, "npm"), `#!/usr/bin/env node
import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";
const args = process.argv.slice(2);
if (process.env.CAO_DASHBOARD_PROFILE !== "immediate") throw new Error("release must select the immediate dashboard profile");
const output = args[args.indexOf("--") + 1];
mkdirSync(output, { recursive: true });
writeFileSync(join(output, "index.html"), "<html></html>");
`, { mode: 0o755 });
  const log = join(directory, "go.jsonl");
  writeFileSync(join(bin, "go"), `#!/usr/bin/env node
import { appendFileSync, writeFileSync } from "node:fs";
const args = process.argv.slice(2);
const { GOOS, GOARCH, CGO_ENABLED, RELEASE_BUILD_LOG, RELEASE_BUILD_FAIL } = process.env;
appendFileSync(RELEASE_BUILD_LOG, JSON.stringify({ args, GOOS, GOARCH, CGO_ENABLED }) + "\\n");
if (RELEASE_BUILD_FAIL === GOOS + "-" + GOARCH) process.exit(2);
writeFileSync(args[args.indexOf("-o") + 1], GOOS + "-" + GOARCH);
`, { mode: 0o755 });
  const output = join(directory, "release binaries");
  const run = (args, extraEnv = {}) => spawnSync("bash", [fixtureScript, ...args], {
    cwd: root,
    encoding: "utf8",
    env: {
      ...process.env,
      PATH: `${bin}${delimiter}${process.env.PATH}`,
      RELEASE_BUILD_LOG: log,
      ...extraEnv,
    },
  });
  return { directory, log, output, run };
}

test("release builder embeds the exact tag in every platform binary and seals checksums", (t) => {
  const { log, output, run } = fixture(t);
  const result = run(["v1.2.3", output]);
  assert.equal(result.status, 0, result.stderr);
  assert.deepEqual(readdirSync(output).sort(), [...platforms, "checksums.txt"].sort());
  const builds = readFileSync(log, "utf8").trim().split("\n").map((line) => JSON.parse(line));
  assert.equal(builds.length, platforms.length * 2);
  for (const [index, build] of builds.filter((entry) => entry.args[0] === "build").entries()) {
    const server = builds[index * 2];
    assert.deepEqual(server.args, [
      "-C", "server", "build", "-mod=readonly", "-trimpath", "-ldflags=-s -w -X main.version=v1.2.3",
      "-o", join(dirname(log), "dashboard/local-assets/bundle", `cao-dashboard${build.GOOS === "windows" ? ".exe" : ""}`),
      "./cmd/cao-dashboard",
    ]);
    assert.equal(server.GOOS, build.GOOS);
    assert.equal(server.GOARCH, build.GOARCH);
    assert.equal(server.CGO_ENABLED, "0");
    const suffix = `${build.GOOS}-${build.GOARCH}${build.GOOS === "windows" ? ".exe" : ""}`;
    assert.equal(`gh-cao-${suffix}`, platforms[index]);
    assert.deepEqual(
      readdirSync(output).filter((name) => name.endsWith(suffix)),
      [platforms[index]],
      "gh extension install must find exactly one asset for each platform",
    );
    assert.equal(build.CGO_ENABLED, "0");
    assert.deepEqual(build.args, [
      "build", "-mod=readonly", "-trimpath", "-ldflags=-s -w -X main.version=v1.2.3",
      "-o", join(output, platforms[index]), "./cmd/gh-cao",
    ]);
  }
  const checksums = readFileSync(join(output, "checksums.txt"), "utf8").trim().split("\n");
  assert.equal(checksums.length, platforms.length);
  for (const [index, line] of checksums.entries()) {
    const [hash, name] = line.split(/\s+/);
    assert.equal(name, platforms[index]);
    assert.equal(hash, createHash("sha256").update(readFileSync(join(output, name))).digest("hex"));
  }
});

test("release builder rejects missing or invalid versions before building", (t) => {
  const { log, output, run } = fixture(t);
  for (const version of ["", "dev", "1.2.3", "v01.2.3", "v1.2.3-rc.1", "v1.2.3 -X main.version=bad"]) {
    const result = run([version, output]);
    assert.notEqual(result.status, 0, version);
    assert.match(result.stderr, /stable vMAJOR\.MINOR\.PATCH/);
  }
  assert.match(run([]).stderr, /Usage:/);
  assert.notEqual(run(["v1.2.3", output, "extra"]).status, 0);
  assert.equal(existsSync(log), false);
  assert.equal(existsSync(output), false);
});

test("release builder refuses empty output paths and existing output files", (t) => {
  const { log, output, run } = fixture(t);
  assert.notEqual(run(["v1.2.3", ""]).status, 0);
  mkdirSync(output);
  const existing = join(output, "checksums.txt");
  writeFileSync(existing, "previous release");
  const result = run(["v1.2.3", output]);
  assert.notEqual(result.status, 0);
  assert.match(result.stderr, /must be empty/);
  assert.equal(readFileSync(existing, "utf8"), "previous release");
  assert.equal(existsSync(log), false);
});

test("release builder stops on compilation failure without sealing partial artifacts", (t) => {
  const { log, output, run } = fixture(t);
  const result = run(["v1.2.3", output], { RELEASE_BUILD_FAIL: "linux-amd64" });
  assert.notEqual(result.status, 0);
  assert.equal(readFileSync(log, "utf8").trim().split("\n").length, 5);
  assert.equal(existsSync(join(output, "checksums.txt")), false);
  assert.equal(existsSync(join(output, "gh-cao-windows-amd64.exe")), false);
});
