import assert from "node:assert/strict";
import { execFile } from "node:child_process";
import { chmod, mkdir, mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import path from "node:path";
import { promisify } from "node:util";
import test from "node:test";

const executeFile = promisify(execFile);
const catalog = path.resolve(import.meta.dirname, "../..");

async function run(command, args, options, input) {
  const result = executeFile(command, args, {
    timeout: 300_000,
    maxBuffer: 16 * 1024 * 1024,
    ...options,
  });
  result.child.stdin.end(input);
  return result;
}

async function publicReleaseMetadata() {
  const responses = {};
  for (let page = 1; ; page++) {
    const endpoint = `repos/githubnext/gh-cao/releases?per_page=100&page=${page}`;
    const { stdout } = await run("gh", ["api", endpoint]);
    const releases = JSON.parse(stdout);
    assert.ok(Array.isArray(releases), "GitHub must return a release list");
    responses[`https://api.github.com/${endpoint}`] = releases;
    const release = releases.find((item) => item.draft === false && /^v\d+\.\d+\.\d+$/.test(item.tag_name));
    if (release) {
      const endpoint = `repos/githubnext/gh-cao/releases/tags/${release.tag_name}`;
      const { stdout } = await run("gh", ["api", endpoint]);
      responses[`https://api.github.com/${endpoint}`] = JSON.parse(stdout);
      return responses;
    }
    assert.equal(releases.length, 100, "a published CAO release is required");
  }
}

test("streamed installer installs real public tools with unusable cross-organization credentials", {
  timeout: 360_000,
}, async (t) => {
  const root = await mkdtemp(path.join(tmpdir(), "cao-installer-integration-"));
  t.after(() => rm(root, { recursive: true, force: true }));
  const home = path.join(root, "home");
  const config = path.join(home, ".config", "gh");
  const bin = path.join(root, "bin");
  const consumer = path.join(root, "control");
  const log = path.join(root, "control-requests.log");
  const metadata = path.join(root, "public-release-metadata.json");
  const metadataLog = path.join(root, "metadata-requests.log");
  const catalogSource = path.join(root, "catalog with spaces '\"; $()");
  await Promise.all([config, bin, consumer].map((directory) => mkdir(directory, { recursive: true })));
  await mkdir(catalogSource);
  const archive = path.join(root, "catalog.tar");
  await run("git", ["archive", "--format=tar", `--output=${archive}`, "HEAD"], { cwd: catalog });
  await run("tar", ["-xf", archive, "-C", catalogSource]);
  await run("git", ["init", "--quiet", catalogSource]);
  await run("git", ["add", "."], { cwd: catalogSource });
  const { stdout: executable } = await run("which", ["gh"]);
  assert.ok(path.isAbsolute(executable.trim()), "GitHub CLI must be installed");
  const { stdout: curlExecutable } = await run("which", ["curl"]);
  assert.ok(path.isAbsolute(curlExecutable.trim()), "curl must be installed");

  // Snapshot public metadata with the harness credential, not the installer's
  // unusable credentials or the shared runner IP's exhausted anonymous API quota.
  // All asset downloads and both installed extensions remain real.
  const responses = await publicReleaseMetadata();
  await writeFile(metadata, JSON.stringify(responses));
  const curl = path.join(bin, "curl");
  await writeFile(curl, `#!/usr/bin/env node
const { appendFileSync, readFileSync } = require("node:fs");
const { spawnSync } = require("node:child_process");
const args = process.argv.slice(2);
const url = args.find((arg) => arg.startsWith("https://api.github.com/"));
if (url) {
  if (args[0] !== "--disable" || args.some((arg) => /authorization/i.test(arg))) {
    throw new Error("Public metadata requests must ignore curlrc and omit credentials");
  }
  const responses = JSON.parse(readFileSync(process.env.CAO_INSTALLER_METADATA, "utf8"));
  if (!Object.hasOwn(responses, url)) throw new Error("Unexpected public metadata request: " + url);
  appendFileSync(process.env.CAO_INSTALLER_METADATA_LOG, url + "\\n");
  process.stdout.write(JSON.stringify(responses[url]));
} else {
  const result = spawnSync(process.env.CAO_INSTALLER_CURL, args, { stdio: "inherit" });
  if (result.error) throw result.error;
  process.exit(result.status ?? 1);
}
`);
  await chmod(curl, 0o755);
  const gh = path.join(bin, "gh");
  await writeFile(gh, `#!/usr/bin/env bash
set -euo pipefail
if [[ "$*" == "auth status" || "$*" == "repo view --json nameWithOwner --jq .nameWithOwner" ]]; then
  [[ "\${GH_TOKEN:-}" == synthetic-restricted-operator-token ]] || {
    echo "Control repository authentication was lost." >&2
    exit 1
  }
  printf '%s\\n' "$*" >> "$CAO_INSTALLER_CONTROL_LOG"
  [[ "$*" == "auth status" ]] || printf '%s\\n' installer-test/control
  exit 0
fi
# Keep native init's nested gh calls on this control-identity fixture.
if [[ "$*" == "cao init" ]]; then
  GH_PATH="$0" exec "$XDG_DATA_HOME/gh/extensions/gh-cao/gh-cao" init
fi
exec "$CAO_INSTALLER_GH" "$@"
`);
  await chmod(gh, 0o755);
  const hosts = "github.com:\n    oauth_token: synthetic-stored-token\n    git_protocol: https\n";
  const curlrc = 'header = "Authorization: Bearer synthetic-unusable-token"\n';
  await writeFile(path.join(config, "hosts.yml"), hosts);
  await writeFile(path.join(home, ".curlrc"), curlrc);
  const env = { ...process.env };
  for (const name of [
    "GH_DEBUG", "GH_HOST", "GH_PATH", "GH_EXTENSION", "GH_REPO",
    "GITHUB_REPOSITORY", "GITHUB_OUTPUT", "NODE_OPTIONS", "BASH_ENV", "ENV",
  ]) delete env[name];
  Object.assign(env, {
    HOME: home,
    XDG_CONFIG_HOME: path.join(home, ".config"),
    XDG_DATA_HOME: path.join(home, ".local", "share"),
    XDG_STATE_HOME: path.join(home, ".local", "state"),
    XDG_CACHE_HOME: path.join(home, ".cache"),
    GH_CONFIG_DIR: config,
    PATH: `${bin}${path.delimiter}${process.env.PATH}`,
    GH_HOST: "github.com",
    GH_TOKEN: "synthetic-restricted-operator-token",
    GITHUB_TOKEN: "synthetic-unusable-actions-token",
    GH_ENTERPRISE_TOKEN: "synthetic-unusable-enterprise-token",
    GITHUB_ENTERPRISE_TOKEN: "synthetic-unusable-enterprise-actions-token",
    INPUT_VERSION: "v0.0.1",
    GH_PROMPT_DISABLED: "1",
    GH_NO_UPDATE_NOTIFIER: "1",
    GH_NO_EXTENSION_UPDATE_NOTIFIER: "1",
    CI: "true",
    CAO_INSTALLER_GH: executable.trim(),
    CAO_INSTALLER_CURL: curlExecutable.trim(),
    CAO_INSTALLER_METADATA: metadata,
    CAO_INSTALLER_METADATA_LOG: metadataLog,
    CAO_INSTALLER_CONTROL_LOG: log,
  });
  await run("git", ["init", "--quiet", consumer], { env });
  await run("git", ["remote", "add", "origin", "https://github.com/installer-test/control.git"], { cwd: consumer, env });

  const scenario = process.env.CAO_INSTALLER_SCENARIO ?? "missing";
  assert.ok(["missing", "current", "outdated", "newer"].includes(scenario), "known release scenario");
  const requiredVersion = process.env.CAO_INSTALLER_REQUIRED_VERSION ??
    (await readFile(path.join(catalog, "aw.yml"), "utf8")).match(/^min-version: (v\d+\.\d+\.\d+)$/m)?.[1];
  const preinstalledVersion = process.env.CAO_INSTALLER_PREINSTALLED_VERSION ?? "";
  assert.match(requiredVersion ?? "", /^v\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/);
  const sourceManifest = await readFile(path.join(catalog, "aw.yml"), "utf8");
  await writeFile(path.join(catalogSource, "aw.yml"), sourceManifest.replace(/^min-version: .+$/m, `min-version: ${requiredVersion}`));
  await writeFile(path.join(consumer, "aw.yml"), `min-version: ${requiredVersion}\n`);
  const extension = path.join(env.XDG_DATA_HOME, "gh", "extensions", "gh-aw", "gh-aw");
  let beforeExtension;
  if (scenario !== "missing") {
    assert.match(preinstalledVersion, /^v\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/);
    const upstream = await run("curl", ["--disable", "--fail", "--show-error", "--location",
      "https://raw.githubusercontent.com/github/gh-aw/main/install-gh-aw.sh"], { env });
    const anonymous = { ...env };
    for (const name of ["GH_TOKEN", "GITHUB_TOKEN", "GH_ENTERPRISE_TOKEN", "GITHUB_ENTERPRISE_TOKEN", "INPUT_VERSION"]) {
      delete anonymous[name];
    }
    const installation = await run("bash", ["-c", 'curl() { command curl --disable "$@"; }; export -f curl; bash -s -- "$1"', "--", preinstalledVersion],
      { env: anonymous }, upstream.stdout);
    assert.match(installation.stdout + installation.stderr, /Checksum verification passed/);
    beforeExtension = await readFile(extension);
    const version = await run("gh", ["aw", "version"], { cwd: consumer, env });
    assert.ok((version.stdout + version.stderr).includes(`gh aw version ${preinstalledVersion}`));
  } else {
    await assert.rejects(run("gh", ["aw", "version"], { cwd: consumer, env }),
      "isolated missing extension must not resolve an ambient installation");
  }
  const source = await readFile(path.join(catalog, "install-gh-cao.sh"), "utf8");
  if (scenario === "outdated") {
    await assert.rejects(run("bash", ["-s", "--", catalogSource], { cwd: consumer, env }, source), (error) => {
      assert.match(error.stderr, /gh-aw was not upgraded/);
      assert.ok(error.stderr.includes(`bash -s -- ${requiredVersion}`));
      assert.doesNotMatch(error.stdout + error.stderr, /CAO installed and verified|Verifying exact CAO runtime/);
      return true;
    });
    assert.deepEqual(await readFile(extension), beforeExtension);
    await assert.rejects(readFile(path.join(consumer, ".github/workflows/cao.json")), { code: "ENOENT" });
    await assert.rejects(readFile(path.join(env.XDG_DATA_HOME, "gh/extensions/gh-cao/gh-cao")), { code: "ENOENT" });
    return;
  }
  const { stdout, stderr } = await run("bash", ["-s", "--", catalogSource], { cwd: consumer, env }, source);
  assert.match(stdout, /CAO installed and verified/);
  if (scenario === "missing") assert.match(stdout, /Checksum verification passed/);
  assert.doesNotMatch(stdout + stderr, /Checksum verification (?:skipped|will be skipped)|synthetic-[\w-]*token/);
  const expectedGhAw = preinstalledVersion || requiredVersion;
  assert.ok(expectedGhAw, "the catalog declares a required gh-aw version");
  const aw = await run("gh", ["aw", "version"], { cwd: consumer, env });
  assert.match(aw.stdout + aw.stderr, new RegExp(`gh aw version ${expectedGhAw.replaceAll(".", "\\.")}\\b`));
  if (beforeExtension) assert.deepEqual(await readFile(extension), beforeExtension);
  const cao = await run("gh", ["cao", "--version"], { cwd: consumer, env });
  assert.match(cao.stdout, /^gh cao version v\d+\.\d+\.\d+\s*$/);
  const help = await run("gh", ["cao", "--help"], { cwd: consumer, env });
  assert.match(help.stdout, /Usage:/);
  const policy = JSON.parse(await readFile(path.join(consumer, ".github", "workflows", "cao.json"), "utf8"));
  assert.deepEqual(policy["control-plane"].scope["allowed-repositories"], ["installer-test/control"]);
  assert.deepEqual(policy["control-plane"].campaigns, {});
  assert.equal(await readFile(path.join(config, "hosts.yml"), "utf8"), hosts);
  assert.equal(await readFile(path.join(home, ".curlrc"), "utf8"), curlrc);
  assert.match(await readFile(log, "utf8"), /auth status\n[\s\S]*repo view --json nameWithOwner/);
  assert.deepEqual((await readFile(metadataLog, "utf8")).trim().split("\n"), Object.keys(responses));
  for (const bundle of ["dashboard"]) {
    await run(process.execPath, [".github/workflows/shared/materialize-cao.mjs", "verify", bundle], { cwd: consumer, env });
  }
});
