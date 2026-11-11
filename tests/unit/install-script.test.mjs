import assert from "node:assert/strict";
import { execFile, spawn } from "node:child_process";
import fs from "node:fs";
import { chmod, copyFile, mkdir, mkdtemp, readFile, rm, stat, symlink, writeFile } from "node:fs/promises";
import os from "node:os";
import path from "node:path";
import { pathToFileURL } from "node:url";
import { promisify } from "node:util";
import test from "node:test";
import vm from "node:vm";
import { parse } from "yaml";
import { materializeCaoFromSource } from "../../.github/workflows/shared/materialize-cao.mjs";
import { buildCaoRuntime } from "../../scripts/build-cao-runtime.mjs";
import { runTerminal } from "../helpers/terminal.mjs";

const executeFile = promisify(execFile);
const catalog = path.resolve(".");
const installScript = path.join(catalog, "install-gh-cao.sh");
const installerSource = await readFile(installScript, "utf8");
const manifest = parse(await readFile(path.join(catalog, "aw.yml"), "utf8"));
const supportedGh = "v2.68.0";
const olderGh = "v2.67.0";
const supportedGhAw = manifest["min-version"];
const [major, minor, patch] = supportedGhAw.slice(1).split(".").map(Number);
const olderGhAw = patch > 0 ? `v${major}.${minor}.${patch - 1}` : `${supportedGhAw}-rc.1`;
const newerGhAw = `v${major}.${minor + 1}.0`;
const manifestGhAw = `v${major}.${minor}.${patch + 1}`;
const revision = "1".repeat(40);
// Windows runtime extraction/copying can exceed a minute on hosted runners.
// Keep a finite subprocess deadline without imposing that budget on other hosts.
const timeout = process.platform === "win32" ? 120_000 : 30_000;
const policyPath = path.join(".github", "workflows", "cao.json");
const materializer = path.join(".github", "workflows", "shared", "materialize-cao.mjs");
const curlCommand = "curl --fail --show-error --location";
const manualUpgrade = (version) => new RegExp(`Run \`${curlCommand} \\S+/install-gh-aw\\.sh \\| bash -s -- ${version.replaceAll(".", "\\.")}\`, then rerun the CAO installer`);

test("installer help and implementation no longer expose Quickstart", async () => {
  const { stdout } = await executeFile("bash", [installScript, "--help"]);
  assert.match(stdout, /^Usage: install-gh-cao\.sh /);
  assert.match(stdout, /--setup/);
  assert.match(stdout, /--create-repo/);
  assert.doesNotMatch(stdout, /quickstart|--target/i);
  assert.doesNotMatch(installerSource, /quickstart|target_repository/i);
});

test("installer release ordering handles same-version prerelease progression", async () => {
  const comparator = installerSource.slice(installerSource.indexOf("version_at_least() {"),
    installerSource.indexOf("\nget_gh_version()"));
  assert.ok(comparator.startsWith("version_at_least() {"));
  for (const [current, required, accepted] of [
    ["v1.0.0-rc.10", "v1.0.0-rc.2", true],
    ["v1.0.0-rc.2", "v1.0.0-rc.10", false],
    ["v1.0.0", "v1.0.0-rc.10", true],
    ["v1.0.0-rc.10", "v1.0.0", false],
    ["v1.0.0-beta", "v1.0.0-alpha", true],
    ["v1.0.0-2", "v1.0.0-beta", false],
    ["v1.0.0-beta.1", "v1.0.0-beta", true],
    ["v1.0.0-beta", "v1.0.0-beta.1", false],
    ["v1.0.0-rc.1", "v1.0.0-rc.1", true],
  ]) {
    const result = executeFile("bash", ["-c", `${comparator}\nversion_at_least "$1" "$2"`, "--", current, required]);
    if (accepted) await result;
    else await assert.rejects(result, { code: 1 });
  }
});

const upgradePrompt = new RegExp(`Upgrade it now with ${curlCommand} \\S+/install-gh-aw\\.sh \\| bash -s -- v\\S+\\? \\[y/N\\]`);
const addedFiles = [
  ...manifest.resources.map(({ source, destination }) => ({
    source: path.normalize(source),
    destination,
  })),
  {
    source: ".github/workflows/aw.json",
    destination: ".github/workflows/aw.json",
  },
  ...[".github/workflows/cao-activity.yml", ".github/workflows/cao-dashboard.yml", ".github/workflows/cao-validate.yml"]
    .map((file) => ({ source: file, destination: file })),
];

const fixtureRoot = await mkdtemp(path.join(os.tmpdir(), "cao-install-fixture-"));
const nativeCao = path.join(fixtureRoot, process.platform === "win32" ? "gh-cao-native.exe" : "gh-cao-native");
await executeFile("go", ["build", "-o", nativeCao, "./cmd/gh-cao"], { cwd: catalog, timeout: 120_000 });
const archive = path.join(fixtureRoot, "gh-cao.tar.gz");
const archiveName = path.basename(archive);
const runtimeArchive = buildCaoRuntime(catalog, fixtureRoot, revision).archive;
const mockFetch = path.join(fixtureRoot, "mock-fetch.mjs");
const archiveResources = [
  "activity",
  "dashboard",
  "runtime/package.json",
  "skills",
  "plugin.json",
  "com.github.copilot/extensions/cao-dashboard",
  "specs/dashboard-data.md",
  "docs/dashboard-language-specification.md",
  ".github/skills/generate-dashboard-ir",
  ".github/skills/dashboard-authoring/SKILL.md",
  ".github/skills/author-dashboard-intent/SKILL.md",
  ".github/actions/setup-cao-runtime",
  ".github/actions/setup-gh-aw",
  ".github/cao/instructions.md",
  ".github/workflows/shared/activity-cache.md",
  ".github/workflows/shared/control.md",
  ".github/workflows/shared/operation.md",
  ".github/workflows/shared/review-bundle.md",
];
const { stdout: trackedArchiveFiles } = await executeFile("git", ["ls-files", "-z", "--", ...archiveResources], { cwd: catalog });
const archiveMembers = [...new Set([
  ...trackedArchiveFiles.split("\0").filter(Boolean),
  "runtime/package.json",
])].map((member) => `${path.basename(catalog)}/${member}`);
// Keep the tracked inventory out of Windows' bounded process command line.
const archiveMembersName = "archive-members.txt";
await writeFile(path.join(fixtureRoot, archiveMembersName), `${archiveMembers.join("\0")}\0`);
await executeFile("tar", [
  "-czf", archiveName,
  "-C", path.dirname(catalog),
  "--null", "-T", archiveMembersName,
], { cwd: fixtureRoot });
await writeFile(mockFetch, `
import { appendFileSync, readFileSync } from "node:fs";
import { spawnSync } from "node:child_process";
import { basename } from "node:path";

if (process.env.FAKE_GH_SCRIPT && [process.execPath, process.argv0].some((executable) => basename(executable).toLowerCase() === "gh.exe")) {
 const result = spawnSync("bash", [process.env.FAKE_GH_SCRIPT, basename(process.argv[1]), ...process.argv.slice(2)], { stdio: "inherit" });
 process.exit(result.status ?? 1);
}

const expected = "https://codeload.github.com/githubnext/gh-cao/tar.gz/${revision}";
const responses = process.env.FAKE_GITHUB_RESPONSES ? JSON.parse(process.env.FAKE_GITHUB_RESPONSES) : {};
globalThis.fetch = async (input) => {
  const url = typeof input === "string" ? input : input.url ?? String(input);
  if (process.env.FAKE_GITHUB_REQUESTS) appendFileSync(process.env.FAKE_GITHUB_REQUESTS, url + "\\n");
  if (Object.hasOwn(responses, url)) return Response.json(responses[url]);
  if (url !== expected) throw new Error("unexpected fetch: " + url);
  const status = Number(process.env.FAKE_CAO_ARCHIVE_STATUS || 200);
  if (status !== 200) return new Response("unavailable", { status });
  return new Response(readFileSync(process.env.FAKE_CAO_ARCHIVE));
};
`);
test.after(() => rm(fixtureRoot, { recursive: true, force: true }));

test("installer fixture archive contains every tracked runtime source and no generated files", async () => {
  const { stdout } = await executeFile("tar", ["-tzf", archiveName], { cwd: fixtureRoot });
  assert.deepEqual(stdout.trimEnd().split(/\r?\n/).sort(), [...archiveMembers].sort());
});

// FAKE_CONTROL_REPOSITORY is gh's answer for the consumer checkout; when it is
// unset the lookup fails as it does outside a GitHub repository checkout.
const fakeGh = `#!/usr/bin/env bash
set -euo pipefail
if [[ "$*" == "--version" ]]; then
  echo "gh version \${FAKE_GH_VERSION:-${supportedGh}} (fake)"
  exit 0
fi
if [[ "\${1:-}" == cao ]]; then
  shift
  executable="$XDG_DATA_HOME/gh/extensions/gh-cao/gh-cao"
  [[ ! -f "$executable.exe" ]] || executable="$executable.exe"
  if [[ "$*" == init && -n "\${FAKE_CAO_POLICY_SCHEMA:-}" ]]; then
    "$executable" "$@" || exit "$?"
    node --input-type=module -e 'import {readFileSync,writeFileSync} from "node:fs"; const file=".github/workflows/cao.json"; const policy=JSON.parse(readFileSync(file,"utf8")); policy.$schema=process.env.FAKE_CAO_POLICY_SCHEMA; writeFileSync(file,JSON.stringify(policy));'
    exit 0
  fi
  exec "$executable" "$@"
fi
if [[ "$*" == "auth status" ]]; then
  [[ -z "\${FAKE_AUTH_DELAY:-}" ]] || sleep "$FAKE_AUTH_DELAY"
  [[ -z "\${FAKE_AUTH_FAILURE:-}" ]] || { echo "not signed in" >&2; exit 1; }
  exit 0
fi
if [[ "$*" == *repos/githubnext/gh-cao/releases* || "$*" == "extension install "* ]]; then
  echo "Resource protected by organization SAML enforcement" >&2
  exit 1
fi
if [[ -n "\${FAKE_REQUIRE_CONTROL_AUTH:-}" && "\${GH_TOKEN:-}" != synthetic-control-token ]]; then
  echo "control repository authentication was lost" >&2
  exit 1
fi
if [[ "\${1:-} \${2:-}" == "api graphql" ]]; then
  [[ "$*" == *"viewerCanCreateRepositories"* ]] || exit 2
  [[ -z "\${FAKE_OWNER_DISCOVERY_FAILURE:-}" ]] || { echo "owner lookup failed" >&2; exit 1; }
  echo "owners-query" >> "$FAKE_COMMAND_LOG"
  printf '%b\\n' "\${FAKE_REPOSITORY_OWNERS:-alpha-user\\\\nalpha-org}"
  exit 0
fi
if [[ "\${1:-}" == api && "\${2:-}" == repos/* && "$#" -eq 2 ]]; then
  if [[ "\${2#repos/}" == "\${FAKE_REPOSITORY_CHECK_FAILURE:-}" ]]; then
    echo "gh: service unavailable (HTTP 503)" >&2
    exit 1
  fi
  if [[ ",\${FAKE_EXISTING_REPOSITORIES:-}," == *",\${2#repos/},"* ]]; then
    exit 0
  fi
  echo "gh: Not Found (HTTP 404)" >&2
  exit 1
fi
if [[ "\${1:-} \${2:-}" == "repo create" ]]; then
  [[ -z "\${FAKE_CREATE_REPOSITORY_FAILURE:-}" ]] || { echo "Repository already exists" >&2; exit 1; }
  if [[ "$*" == "repo create $FAKE_CONTROL_REPOSITORY --private --clone" ]]; then
    echo "create-private" >> "$FAKE_COMMAND_LOG"
    mkdir "\${3#*/}"
    git -C "\${3#*/}" init --quiet
    git -C "\${3#*/}" remote add origin "https://github.com/\${3}.git"
    printf 'Created repository %s on github.com\\n  https://github.com/%s\\n' "\${3}" "\${3}"
  elif [[ "$*" == "repo create $FAKE_CONTROL_REPOSITORY --private --source=. --remote=origin" ]]; then
    echo "create-private-current" >> "$FAKE_COMMAND_LOG"
    git remote add origin "https://github.com/$FAKE_CONTROL_REPOSITORY.git"
    printf 'Created repository %s on github.com\\n  https://github.com/%s\\n' "$FAKE_CONTROL_REPOSITORY" "$FAKE_CONTROL_REPOSITORY"
  else
    exit 2
  fi
  exit 0
fi
if [[ "\${1:-}" == api && "$*" == *"/actions/workflows/cao-runtime.yml/runs?"* ]]; then
  printf '%s\\n' '[{"workflow_runs":[{"id":42,"head_sha":"${revision}","conclusion":"success","event":"push"}]}]'
  exit 0
fi
if [[ "\${1:-}" == api && "$*" == *"/actions/runs/42/artifacts?"* ]]; then
  printf '%s\\n' '[{"artifacts":[{"name":"cao-runtime-${revision}","expired":false}]}]'
  exit 0
fi
if [[ "\${1:-} \${2:-}" == "run download" ]]; then
  [[ "\${FAKE_CAO_ARCHIVE_STATUS:-200}" == 200 ]] || { echo "HTTP 503" >&2; exit 1; }
  while [[ $# -gt 0 ]]; do
    if [[ "$1" == --dir ]]; then destination="$2"; break; fi
    shift
  done
  cp "$FAKE_CAO_RUNTIME_ARCHIVE" "$destination/cao-runtime-${revision}.tgz"
  exit 0
fi
if [[ "\${1:-} \${2:-}" == "attestation verify" ]]; then
  [[ -z "\${FAKE_CAO_ATTESTATION_FAILURE:-}" ]] || { echo "attestation rejected" >&2; exit 1; }
  exit 0
fi
if [[ "$*" == "repo view --json nameWithOwner --jq .nameWithOwner" ]]; then
  if { [[ -n "\${FAKE_REPOSITORY_LOOKUP_FAILURE:-}" ]] && ! git remote get-url origin >/dev/null 2>&1; } ||
    [[ -z "\${FAKE_CONTROL_REPOSITORY+set}" ]]; then
    echo "none of the git remotes configured for this repository point to a known GitHub host" >&2
    exit 1
  fi
  printf '%s\\n' "$FAKE_CONTROL_REPOSITORY"
  exit 0
fi
if [[ "\${1:-} \${2:-}" == "repo view" && "\${3:-}" == "$FAKE_CONTROL_REPOSITORY" ]]; then
  printf '{"nameWithOwner":"%s","visibility":"%s"}\\n' "$3" "\${FAKE_CONTROL_VISIBILITY:-PRIVATE}"
  exit 0
fi
if [[ "$*" == "variable set GH_AW_GITHUB_AUTH_MODE --repo \${FAKE_CONTROL_REPOSITORY:-} --body workflow-token" ]]; then
  echo "setup-workflow-token" >> "$FAKE_COMMAND_LOG"
  exit 0
fi
if [[ "\${1:-}" == api && ( "\${2:-}" == repos/"$FAKE_CONTROL_REPOSITORY"/actions/variables\\?* ||
  "\${2:-}" == repos/"$FAKE_CONTROL_REPOSITORY"/actions/organization-variables\\?* ) ]]; then
  exit 0
fi
if [[ "\${1:-} \${2:-}" == "aw version" ]]; then
  [[ -f "$FAKE_GH_AW_INSTALLED" ]] || exit 1
  echo "gh aw version $(cat "$FAKE_GH_AW_INSTALLED")" >&2
  exit 0
fi
if [[ "\${1:-} \${2:-}" == "aw add" && "\${3:-}" == githubnext/gh-cao* ]]; then
  [[ "\${3:-}" == githubnext/gh-cao || "\${3:-}" == githubnext/gh-cao@* ]] || { echo "unexpected CAO package: \${3:-}" >&2; exit 2; }
  [[ -z "\${FAKE_EXPECT_CAO_SOURCE:-}" || "\${3:-}" == "$FAKE_EXPECT_CAO_SOURCE" ]] ||
    { echo "expected CAO package $FAKE_EXPECT_CAO_SOURCE, got \${3:-}" >&2; exit 2; }
  stdin_bytes="$(wc -c | tr -d ' ')"
  entry=add
  [[ "\${4:-}" != "--force" ]] || entry=add-force
  [[ "$stdin_bytes" == 0 ]] || entry="$entry stdin=$stdin_bytes"
  echo "$entry" >> "$FAKE_COMMAND_LOG"
  if [[ -n "\${FAKE_ADD_FAILURE:-}" ]]; then
    echo "campaign add failed" >&2
    exit 1
  fi
  current="$(cat "$FAKE_GH_AW_INSTALLED")"
  if [[ -n "\${FAKE_MANIFEST_MIN_VERSION:-}" && "$current" != "$FAKE_MANIFEST_MIN_VERSION" ]]; then
    echo "✗ invalid Agentic Workflow manifest \\"aw.yml\\": min-version \\"$FAKE_MANIFEST_MIN_VERSION\\" requires gh-aw $FAKE_MANIFEST_MIN_VERSION or newer (current: $current)." >&2
    exit 1
  fi
${addedFiles.map(({ source, destination }) => `  mkdir -p "$(dirname '${destination}')"
  cp "$FAKE_CATALOG/${source}" '${destination}'`).join("\n")}
  mkdir -p .github/aw/packages
  cat > .github/aw/packages/githubnext-gh-cao-c6b3479204cc.json <<'EOF'
{"package":"githubnext/gh-cao","source":"githubnext/gh-cao@${revision}","resolvedCommit":"${revision}"}
EOF
  exit 0
fi
exit 2
`;

// Serves gh-aw's install script, which records the requested version as installed.
const fakeCurl = `#!/usr/bin/env bash
set -euo pipefail
if [[ -n "\${FAKE_CURLRC_AUTH:-}" && "\${1:-}" != --disable ]]; then
  echo "curlrc authentication triggered SAML enforcement" >&2
  exit 22
fi
url=""
output=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    -o) output="$2"; shift 2 ;;
    https://*) url="$1"; shift ;;
    *) shift ;;
  esac
done
[[ -z "\${FAKE_PUBLIC_REQUESTS:-}" ]] || printf '%s\\n' "$url" >> "$FAKE_PUBLIC_REQUESTS"
if [[ "$url" == https://api.github.com/repos/githubnext/gh-cao/releases\\?* ]]; then
  [[ -z "\${FAKE_LATEST_CAO_RELEASE_FAILURE:-}" ]] || { echo "release lookup failed" >&2; exit 22; }
  node --input-type=module -e '
    const page = Number(new URL(process.argv[1]).searchParams.get("page"));
    if (process.env.FAKE_CAO_RELEASE_RESPONSE) {
      console.log(process.env.FAKE_CAO_RELEASE_RESPONSE);
    } else if (process.env.FAKE_CAO_RELEASE_PAGES) {
      console.log(JSON.stringify(JSON.parse(process.env.FAKE_CAO_RELEASE_PAGES)[page - 1] ?? []));
    } else {
      const list = process.env.FAKE_CAO_RELEASE_LIST ?? "false\\tfalse\\t" + (process.env.FAKE_LATEST_CAO_RELEASE ?? "v1.2.3");
      console.log(JSON.stringify(list.split("\\n").filter(Boolean).map((line) => {
        const [draft, prerelease, tag_name] = line.split("\\t");
        return { draft: draft === "true", prerelease: prerelease === "true", tag_name };
      })));
    }
  ' "$url"
  exit 0
fi
if [[ "$url" == https://api.github.com/repos/githubnext/gh-cao/releases/tags/* ]]; then
  echo "\${url#https://api.github.com/}" >> "$FAKE_CAO_RELEASE_REQUESTS"
  [[ -z "\${FAKE_CAO_ASSET_LOOKUP_FAILURE:-}" ]] || { echo "release asset lookup failed" >&2; exit 22; }
  node --input-type=module -e '
    const names = process.env.FAKE_CAO_RELEASE_ASSETS ?? "gh-cao-darwin-amd64\\ngh-cao-darwin-arm64\\ngh-cao-linux-amd64\\ngh-cao-linux-arm64\\ngh-cao-windows-amd64.exe\\ngh-cao-windows-arm64.exe\\nchecksums.txt";
    console.log(process.env.FAKE_CAO_RELEASE_METADATA ?? JSON.stringify({
      draft: false, assets: names.split("\\n").filter(Boolean).map((name) => ({ name, state: "uploaded", size: 100 })),
    }));
  '
  exit 0
fi
if [[ "$url" == https://github.com/github/gh-aw/releases/download/* ]]; then
  printf 'upstream-public-download\\n' >> "$FAKE_COMMAND_LOG"
  printf 'verified upstream fixture' > "$output"
  exit 0
fi
if [[ "$url" == https://github.com/githubnext/gh-cao/releases/download/* ]]; then
  echo "$url" >> "$FAKE_CAO_DOWNLOADS"
  [[ -z "\${FAKE_CAO_BINARY_FAILURE:-}" ]] || { echo "binary download failed" >&2; exit 22; }
  asset="\${url##*/}"
  release="\${url%/*}"
  release="\${release##*/}"
  if [[ "$asset" == checksums.txt ]]; then
    downloaded_suffix=""
    case "$(uname -s)" in MINGW*|MSYS*|CYGWIN*) downloaded_suffix=.exe ;; esac
    downloaded_binary="$(dirname "$output")/gh-cao$downloaded_suffix"
    node --input-type=module -e '
      import { readFileSync } from "node:fs";
      import { createHash } from "node:crypto";
      const mode = process.env.FAKE_CAO_CHECKSUM_MODE;
      const digest = mode === "corrupt" ? "0".repeat(64) : createHash("sha256").update(readFileSync(process.argv[1])).digest("hex");
      if (mode !== "missing") for (const os of ["darwin", "linux", "windows"]) for (const arch of ["amd64", "arm64"]) {
        const prefix = process.env.FAKE_CAO_ASSET_PREFIX ?? "gh-cao-";
        const line = digest + "  " + prefix + os + "-" + arch + (os === "windows" ? ".exe" : "");
        console.log(line);
        if (mode === "duplicate") console.log(line);
      }
    ' "$downloaded_binary" > "$output"
  else
    cat > "$output" <<EOF
#!/usr/bin/env bash
if [[ "\\$*" == --version ]]; then
  echo "gh cao version \${FAKE_CAO_BINARY_VERSION:-$release}"
else
  exec "$FAKE_NATIVE_CAO" "\\$@"
fi
EOF
  fi
  exit 0
fi
echo "curl" >> "$FAKE_COMMAND_LOG"
if [[ -n "\${FAKE_INSTALL_FAILURE:-}" ]]; then
  echo "curl: (22) The requested URL returned error: 503" >&2
  exit 22
fi
[[ -z "\${FAKE_INSTALL_NOOP:-}" ]] || exit 0
cat <<'EOF'
if [[ -n "\${GH_TOKEN:-}\${GITHUB_TOKEN:-}\${GH_ENTERPRISE_TOKEN:-}\${GITHUB_ENTERPRISE_TOKEN:-}\${INPUT_VERSION:-}" ]]; then
  echo "upstream installer inherited operator authentication or Actions version override" >&2
  exit 1
fi
if [[ -n "\${FAKE_PUBLIC_REQUESTS:-}" ]]; then
  curl --fail --location "https://github.com/github/gh-aw/releases/download/$1/linux-amd64" -o "$FAKE_GH_AW_INSTALLED.download"
fi
printf '%s\\n' "$1" > "$FAKE_GH_AW_INSTALLED"
EOF
`;

async function createConsumer(t, ghAwVersion, { repository = "alpha-org/control" } = {}) {
  const root = await mkdtemp(path.join(os.tmpdir(), "cao-install-"));
  t.after(() => rm(root, { recursive: true, force: true, maxRetries: 3, retryDelay: 50 }));
  const bin = path.join(root, "bin");
  const consumer = path.join(root, "consumer");
  const log = path.join(root, "commands.log");
  const ghAwInstalled = path.join(root, "gh-aw-installed");
  await mkdir(bin);
  await mkdir(consumer);
  await executeFile("git", ["init", "--quiet", consumer]);
  await executeFile("git", ["-C", consumer, "remote", "add", "origin", `https://github.com/${repository}.git`]);
  await writeFile(log, "");
  for (const [name, source] of [
    ["gh", fakeGh],
    ["gh.cmd", "@echo off\r\nbash \"%~dp0gh\" %*\r\n"],
    ["curl", fakeCurl],
    ["curl.exe", fakeCurl],
    ["npm", "#!/usr/bin/env bash\necho 'npm must not run during installation' >&2\nexit 1\n"],
  ]) {
    await writeFile(path.join(bin, name), source);
    await chmod(path.join(bin, name), 0o755);
  }
  if (process.platform === "win32") await copyFile(process.execPath, path.join(bin, "gh.exe"));
  if (ghAwVersion) await writeFile(ghAwInstalled, `${ghAwVersion}\n`);
  const inheritedEnv = { ...process.env };
  const inheritedPath = inheritedEnv.PATH ?? inheritedEnv.Path ?? "";
  if (process.platform === "win32") delete inheritedEnv.Path;
  const systemPath = process.platform === "win32"
    ? inheritedPath.split(path.delimiter).filter((directory) => path.basename(directory).toLowerCase() !== "github cli").join(path.delimiter)
    : inheritedPath;
  const env = {
    ...inheritedEnv,
    PATH: `${bin}${path.delimiter}${systemPath}`,
    CI: "",
    ...(process.platform === "win32" ? { PATHEXT: ".CMD;.COM;.EXE;.BAT" } : {}),
    NODE_OPTIONS: [process.env.NODE_OPTIONS, `--import=${pathToFileURL(mockFetch).href}`].filter(Boolean).join(" "),
    FAKE_CATALOG: catalog,
    FAKE_GH_SCRIPT: path.join(bin, "gh"),
    FAKE_CAO_ARCHIVE: archive,
    FAKE_CAO_RUNTIME_ARCHIVE: runtimeArchive,
    FAKE_NATIVE_CAO: nativeCao,
    FAKE_COMMAND_LOG: log,
    FAKE_GH_AW_INSTALLED: ghAwInstalled,
    FAKE_CONTROL_REPOSITORY: repository,
    XDG_DATA_HOME: path.join(root, "data"),
    FAKE_CAO_DOWNLOADS: path.join(root, "cao-downloads.log"),
    FAKE_CAO_RELEASE_REQUESTS: path.join(root, "cao-release-requests.log"),
  };
  return {
    root,
    consumer,
    repository,
    env,
    log: () => readFile(log, "utf8"),
    installedGhAw: () => readFile(ghAwInstalled, "utf8"),
  };
}

function collect(child, resolve, reject) {
  let stdout = "";
  let stderr = "";
  child.stdout.setEncoding("utf8").on("data", (chunk) => { stdout += chunk; });
  child.stderr.setEncoding("utf8").on("data", (chunk) => { stderr += chunk; });
  child.on("error", reject);
  child.stdin.on("error", reject);
  child.on("close", (code, signal) => resolve({ code, signal, stdout, stderr }));
}

// Detached children start a new session without a controlling terminal, so
// /dev/tty is unavailable regardless of how the test runner was launched.
function runStreamed(cwd, env, installerArguments = []) {
  return new Promise((resolve, reject) => {
    const child = spawn("bash", ["-s", "--", ...installerArguments], { cwd, env, timeout, detached: true });
    collect(child, resolve, reject);
    child.stdin.end(installerSource);
  });
}

async function streamInstaller(cwd, env, installerArguments) {
  const result = await runStreamed(cwd, env, installerArguments);
  assert.equal(result.code, 0, `installer failed (${result.signal ?? "no signal"}):\n${result.stdout}\n${result.stderr}`);
  return result;
}

// script(1) gives the streamed installer a controlling terminal that receives the answer.
function runWithTerminal(cwd, env, answer) {
  const quoted = `'${installScript.replaceAll("'", "'\\''")}'`;
  return runTerminal("bash", ["-c", `cat ${quoted} | bash`], { cwd, env, timeout, input: `${answer}\nn\n` });
}

function runFile(cwd, env, installerArguments = []) {
  return executeFile("bash", [installScript, ...installerArguments], { cwd, env, timeout, detached: true });
}

async function exists(file) {
  try {
    await stat(file);
    return true;
  } catch (error) {
    if (error?.code === "ENOENT") return false;
    throw error;
  }
}

async function assertCompleteInstall(consumer, env, ghAwVersion, repository, { activity = false } = {}) {
  const policy = JSON.parse(await readFile(path.join(consumer, policyPath), "utf8"));
  assert.equal(policy.version, 1);
  assert.equal(policy["gh-aw-version"], ghAwVersion);
  assert.deepEqual(policy["control-plane"], {
    scope: { "allowed-owners": [repository.split("/")[0]], "allowed-repositories": [repository] },
    ...(activity ? { activity: { repositories: [repository] } } : {}),
    marketplace: {
      registries: [{
        id: "official",
        name: "Official CAO catalog",
        repository: "githubnext/gh-cao",
        ref: "main",
        auth: { type: "none" },
      }, {
        id: "agentics",
        name: "Agentics workflows",
        repository: "githubnext/agentics",
        path: "workflows",
        format: "workflows",
        ref: "main",
        auth: { type: "none" },
      }],
    },
    campaigns: {},
  });
  assert.equal(await exists(path.join(consumer, "cao.sh")), false);
  await executeFile(process.execPath, ["activity/cao.mjs", "--help"], { cwd: consumer, env, timeout });
  for (const bundle of ["activity", "dashboard"]) {
    await executeFile(process.execPath, [materializer, "verify", bundle], { cwd: consumer, env, timeout });
  }
  for (const file of [
    "plugin.json",
    "com.github.copilot/extensions/cao-dashboard/extension.mjs",
    "specs/dashboard-data.md",
    "skills",
    "dashboard/site/test",
    "dashboard/site/screenshots",
  ]) {
    assert.equal(await exists(path.join(consumer, file)), false, `${file} is not runtime`);
  }
}

async function assertNothingInstalled(consumer) {
  assert.equal(await exists(path.join(consumer, policyPath)), false);
  assert.equal(await exists(path.join(consumer, "cao.sh")), false);
}

const githubApi = "https://api.example.invalid";
const deterministicWorkflows = [".github/workflows/cao-activity.yml", ".github/workflows/cao-dashboard.yml"];

function githubFixture(repository) {
  const [owner, name] = repository.split("/");
  return {
    [`${githubApi}/repos/${repository}`]: {
      id: 101,
      name,
      full_name: repository,
      owner: { login: owner },
      private: true,
      visibility: "private",
      archived: false,
      default_branch: "main",
      html_url: `https://github.com/${repository}`,
    },
    [`${githubApi}/repos/${repository}/actions/workflows?per_page=100&page=1`]: {
      total_count: deterministicWorkflows.length,
      workflows: deterministicWorkflows.map((file, index) => ({
        id: 201 + index,
        name: path.basename(file, ".yml"),
        path: file,
        state: "active",
        html_url: `https://github.com/${repository}/blob/main/${file}`,
        created_at: "2025-01-01T00:00:00Z",
        updated_at: "2025-01-02T00:00:00Z",
      })),
    },
    [`${githubApi}/repos/github/gh-aw/releases?per_page=100&page=1`]: [
      { tag_name: supportedGhAw, draft: false, prerelease: false },
    ],
  };
}

// Mirrors the Activity job environment when no read-only GitHub App is configured.
function activityEnvironment(root, consumer, env, repository) {
  const work = path.join(consumer, ".cao-activity");
  return {
    ...env,
    GITHUB_REPOSITORY: repository,
    ACTIVITY_APP_TOKEN: "",
    GH_TOKEN: "synthetic-workflow-token",
    GITHUB_API_URL: githubApi,
    REPORT_CONTROL_SETTINGS: path.join(work, "control-settings.json"),
    REPORT_INVENTORY: path.join(work, "control-plane-inventory.json"),
    REPORT_INVENTORY_SOURCES: path.join(work, "inventory-sources.json"),
    FAKE_GITHUB_RESPONSES: JSON.stringify(githubFixture(repository)),
    FAKE_GITHUB_REQUESTS: path.join(root, "github-requests.log"),
  };
}

async function resolveActivitySettings(consumer, activityEnv) {
  await executeFile(process.execPath, [
    path.join("activity", "control-settings.mjs"),
    path.join(".github", "workflows", "shared", "control.mjs"),
    policyPath,
    activityEnv.REPORT_CONTROL_SETTINGS,
  ], { cwd: consumer, env: activityEnv, timeout });
  return JSON.parse(await readFile(activityEnv.REPORT_CONTROL_SETTINGS, "utf8"));
}

// Executes the installed workflow's complete inventory script as github-script
// would, with exec.getExecOutput spawning the installed CLI in the consumer.
async function runInventoryStep(consumer, activityEnv, spawned = []) {
  const workflow = parse(await readFile(path.join(consumer, ".github", "workflows", "cao-activity.yml"), "utf8"));
  const step = Object.values(workflow.jobs)
    .flatMap((job) => job.steps ?? [])
    .find(({ name }) => name === "Collect dashboard inventory");
  assert.ok(step?.with?.script, "installed Activity workflow collects dashboard inventory");
  const logs = [];
  const context = {
    require(specifier) {
      const modules = { fs, path };
      if (!Object.hasOwn(modules, specifier)) throw new Error(`unexpected require: ${specifier}`);
      return modules[specifier];
    },
    process: { env: activityEnv, execPath: process.execPath },
    core: { info: (message) => { logs.push(message); } },
    exec: {
      getExecOutput(command, args, { ignoreReturnCode = false } = {}) {
        spawned.push(args);
        return new Promise((resolve, reject) => {
          const child = spawn(command, args, { cwd: consumer, env: activityEnv, timeout, stdio: ["ignore", "pipe", "pipe"] });
          let stdout = "";
          let stderr = "";
          child.stdout.setEncoding("utf8").on("data", (chunk) => { stdout += chunk; });
          child.stderr.setEncoding("utf8").on("data", (chunk) => { stderr += chunk; });
          child.on("error", reject);
          child.on("close", (exitCode) => {
            if (exitCode !== 0 && !ignoreReturnCode) reject(new Error(`${command} exited with ${exitCode}: ${stderr}`));
            else resolve({ exitCode, stdout, stderr });
          });
        });
      },
    },
  };
  try {
    await vm.runInNewContext(`(async () => {\n${step.with.script}\n})()`, context);
  } catch (error) {
    throw new Error(`${error.message}\n${logs.join("\n")}`, { cause: error });
  }
  return { spawned, logs };
}

async function githubRequests(activityEnv) {
  if (!await exists(activityEnv.FAKE_GITHUB_REQUESTS)) return [];
  return (await readFile(activityEnv.FAKE_GITHUB_REQUESTS, "utf8")).trim().split("\n").filter(Boolean);
}

for (const [state, initialVersion, expectedVersion, expectedLog] of [
  ["missing gh-aw", undefined, supportedGhAw, "curl\nadd\n"],
  [`gh-aw ${supportedGhAw}`, supportedGhAw, supportedGhAw, "add\n"],
  [`gh-aw ${newerGhAw}`, newerGhAw, newerGhAw, "add\n"],
]) {
  test(`streamed install-gh-cao.sh with ${state} materializes the runtime and initializes policy`, async (t) => {
    const { consumer, repository, env, log } = await createConsumer(t, initialVersion);
    await streamInstaller(consumer, env);
    assert.equal(await log(), expectedLog);
    await assertCompleteInstall(consumer, env, expectedVersion, repository);
  });
}

test("public tool installation bypasses SAML-restricted tokens without changing control repository credentials", async (t) => {
  const { root, consumer, env, log } = await createConsumer(t);
  const config = path.join(root, "gh-config");
  const home = path.join(root, "home");
  await mkdir(config);
  await mkdir(home);
  const hosts = "github.com:\n    oauth_token: synthetic-stored-token\n";
  const curlrc = 'header = "Authorization: Bearer synthetic-curlrc-token"\n';
  await writeFile(path.join(config, "hosts.yml"), hosts);
  await writeFile(path.join(home, ".curlrc"), curlrc);
  const requests = path.join(root, "public-requests.log");
  const installEnv = {
    ...env,
    HOME: home,
    GH_CONFIG_DIR: config,
    GH_TOKEN: "synthetic-control-token",
    GITHUB_TOKEN: "synthetic-actions-token",
    GH_ENTERPRISE_TOKEN: "synthetic-enterprise-token",
    GITHUB_ENTERPRISE_TOKEN: "synthetic-enterprise-actions-token",
    INPUT_VERSION: "v0.0.1",
    FAKE_REQUIRE_CONTROL_AUTH: "1",
    FAKE_CURLRC_AUTH: "1",
    FAKE_PUBLIC_REQUESTS: requests,
  };
  const result = await streamInstaller(consumer, installEnv);
  await assertCompleteInstall(consumer, installEnv, supportedGhAw, "alpha-org/control");
  assert.equal(await readFile(path.join(config, "hosts.yml"), "utf8"), hosts);
  assert.equal(await readFile(path.join(home, ".curlrc"), "utf8"), curlrc);
  assert.equal(await log(), "curl\nupstream-public-download\nadd\n");
  const urls = (await readFile(requests, "utf8")).trim().split("\n");
  assert.ok(urls.includes("https://api.github.com/repos/githubnext/gh-cao/releases?per_page=100&page=1"));
  assert.ok(urls.includes("https://api.github.com/repos/githubnext/gh-cao/releases/tags/v1.2.3"));
  assert.ok(urls.includes("https://raw.githubusercontent.com/github/gh-aw/main/install-gh-aw.sh"));
  assert.ok(urls.includes(`https://github.com/github/gh-aw/releases/download/${supportedGhAw}/linux-amd64`));
  assert.ok(urls.some((url) => /githubnext\/gh-cao\/releases\/download\/v1\.2\.3\/gh-cao-/.test(url)));
  assert.doesNotMatch(result.stdout + result.stderr, /synthetic-.*token/);
});

test("anonymous release discovery paginates past a full page of drafts and unsupported tags", async (t) => {
  const { consumer, env } = await createConsumer(t, supportedGhAw);
  await streamInstaller(consumer, {
    ...env,
    FAKE_CAO_RELEASE_PAGES: JSON.stringify([
      Array.from({ length: 100 }, (_, index) => ({ draft: index !== 0, tag_name: index === 0 ? "nightly" : `v9.0.${index}` })),
      [{ draft: false, prerelease: true, tag_name: "v1.2.5" }],
    ]),
  });
  assert.equal(await readFile(env.FAKE_CAO_RELEASE_REQUESTS, "utf8"), "repos/githubnext/gh-cao/releases/tags/v1.2.5\n");
});

for (const response of ['{"message":"API rate limit exceeded"}', "not JSON"]) {
  test(`anonymous release discovery fails closed on ${response.startsWith("{") ? "an API error object" : "invalid JSON"}`, async (t) => {
    const { consumer, env, log } = await createConsumer(t);
    const result = await runStreamed(consumer, { ...env, FAKE_CAO_RELEASE_RESPONSE: response });
    assert.notEqual(result.code, 0);
    assert.match(result.stderr, /Public release discovery is anonymous/);
    assert.equal(await log(), "");
    assert.equal(await exists(env.XDG_DATA_HOME), false);
    await assertNothingInstalled(consumer);
  });
}

test("documented staging preserves the complete curated runtime in a fresh checkout", async (t) => {
  const { root, consumer, repository, env } = await createConsumer(t, supportedGhAw);
  await streamInstaller(consumer, env);
  await executeFile("git", ["init", "--quiet"], { cwd: consumer });
  await executeFile("git", ["add", ".github", "activity", "dashboard"], { cwd: consumer });
  const { stdout: untracked } = await executeFile("git", ["ls-files", "--others", "--exclude-standard"], { cwd: consumer });
  assert.equal(untracked, "", "the documented commit must include every installed resource");
  await executeFile("git", [
    "-c", "user.name=CAO installation test",
    "-c", "user.email=cao-test@example.invalid",
    "-c", "commit.gpgsign=false",
    "-c", "core.hooksPath=/dev/null",
    "commit", "--quiet", "-m", "Install curated CAO runtime",
  ], { cwd: consumer });
  const checkout = path.join(root, "fresh-checkout");
  await executeFile("git", ["clone", "--quiet", "--no-local", consumer, checkout]);
  await assertCompleteInstall(checkout, env, supportedGhAw, repository);
  await executeFile(process.execPath, [
    ".github/workflows/shared/control.mjs", "validate-policy", policyPath,
  ], { cwd: checkout, env, timeout });
  const { stdout: status } = await executeFile("git", ["status", "--porcelain"], { cwd: checkout });
  assert.equal(status, "", "using the installed CLI must not modify the checkout");
});

test("install-gh-cao.sh reports useful progress and does not silence curl", async (t) => {
  const { consumer, env } = await createConsumer(t);
  const result = await streamInstaller(consumer, env);
  assert.equal(result.code, 0, result.stderr);
  assert.match(result.stdout, new RegExp(`Installing gh-aw ${supportedGhAw.replaceAll(".", "\\.")}`));
  assert.match(result.stdout, /Installs 3 workflows, 5 shared control plane resources, and project settings\./);
  assert.match(result.stdout, /Installing control plane components/);
  assert.match(result.stdout, /A newly published catalog revision may wait for its exact verified runtime build/);
  assert.match(result.stdout, /Verifying exact CAO runtime/);
  assert.doesNotMatch(result.stdout, /Making the CAO launcher executable/);
  assert.match(result.stdout, /Initializing the CAO policy/);
  assert.match(result.stdout, /CAO installed and verified/);
  assert.match(result.stdout, /Next: gh cao setup/);
  assert.doesNotMatch(result.stdout, /Continue to guided setup/);
  assert.doesNotMatch(installerSource, /--silent|curl -s/);
});

test("streamed install-gh-cao.sh scopes policy to gh's current repository, not the ambient GITHUB_REPOSITORY", async (t) => {
  const { consumer, repository, env, log } = await createConsumer(t, supportedGhAw, { repository: "beta-org/ops.tools" });
  await streamInstaller(consumer, { ...env, GITHUB_REPOSITORY: "catalog-org/ambient" });
  assert.equal(await log(), "add\n");
  await assertCompleteInstall(consumer, env, supportedGhAw, repository);
});

test(`streamed install-gh-cao.sh with gh-aw ${olderGhAw} and no terminal fails with upgrade guidance`, async (t) => {
  const { consumer, env, log, installedGhAw } = await createConsumer(t, olderGhAw);
  const result = await runStreamed(consumer, env);
  assert.notEqual(result.code, 0);
  assert.match(result.stderr, manualUpgrade(supportedGhAw));
  assert.doesNotMatch(result.stdout, /CAO installed and verified/);
  assert.equal(await log(), "");
  assert.equal(await installedGhAw(), `${olderGhAw}\n`);
  await assertNothingInstalled(consumer);
});

test(`streamed install-gh-cao.sh requires the local aw.yml min-version ${manifestGhAw} before adding`, async (t) => {
  const { consumer, env, log, installedGhAw } = await createConsumer(t, supportedGhAw);
  await writeFile(path.join(consumer, "aw.yml"), `min-version: ${manifestGhAw}\n`);
  const result = await runStreamed(consumer, env);
  assert.notEqual(result.code, 0);
  assert.match(result.stderr, manualUpgrade(manifestGhAw));
  assert.equal(await log(), "");
  assert.equal(await installedGhAw(), `${supportedGhAw}\n`);
  await assertNothingInstalled(consumer);
});

test("explicit local catalog minimum is checked before installing CAO components", async (t) => {
  const { root, consumer, env, log, installedGhAw } = await createConsumer(t, supportedGhAw);
  const source = path.join(root, "local catalog");
  await mkdir(source);
  await writeFile(path.join(source, "aw.yml"), `min-version: ${manifestGhAw}\n`);
  const result = await runStreamed(consumer, env, [source]);
  assert.notEqual(result.code, 0);
  assert.match(result.stderr, manualUpgrade(manifestGhAw));
  assert.equal(await log(), "");
  assert.equal(await installedGhAw(), `${supportedGhAw}\n`);
  await assertNothingInstalled(consumer);
});

test("install-gh-cao.sh exits nonzero when a fresh gh-aw install does not provide the required version", async (t) => {
  const { consumer, env, log } = await createConsumer(t);
  const result = await runStreamed(consumer, { ...env, FAKE_INSTALL_NOOP: "1" });
  assert.notEqual(result.code, 0);
  assert.match(result.stderr, new RegExp(`Failed to verify gh-aw installation: expected at least ${supportedGhAw.replaceAll(".", "\\.")}, got no version`));
  assert.equal(await log(), "curl\n");
  await assertNothingInstalled(consumer);
});

test("install-gh-cao.sh does not retry when the campaign manifest rejects the installed gh-aw", async (t) => {
  const { consumer, env, log, installedGhAw } = await createConsumer(t, supportedGhAw);
  const result = await runStreamed(consumer, { ...env, FAKE_MANIFEST_MIN_VERSION: manifestGhAw });
  assert.notEqual(result.code, 0);
  assert.match(result.stderr, new RegExp(`min-version "${manifestGhAw}" requires gh-aw`));
  assert.equal(await log(), "add\n");
  assert.equal(await installedGhAw(), `${supportedGhAw}\n`);
  await assertNothingInstalled(consumer);
});

const terminalCases = [
  {
    name: `approved upgrade of gh-aw ${olderGhAw} completes installation`,
    initialVersion: olderGhAw,
    answer: "y",
    expectedLog: "curl\nadd\n",
    expectedVersion: supportedGhAw,
  },
  {
    name: `declined upgrade of gh-aw ${olderGhAw} stops with upgrade guidance`,
    initialVersion: olderGhAw,
    answer: "n",
    expectedLog: "",
  },
  {
    name: `failed approved upgrade of gh-aw ${olderGhAw} exits nonzero`,
    initialVersion: olderGhAw,
    answer: "y",
    environment: { FAKE_INSTALL_FAILURE: "1" },
    expectedLog: "curl\n",
    failure: /Failed to install gh-aw/,
  },
  {
    name: `approved upgrade that does not provide gh-aw ${supportedGhAw} exits nonzero`,
    initialVersion: olderGhAw,
    answer: "y",
    environment: { FAKE_INSTALL_NOOP: "1" },
    expectedLog: "curl\n",
    failure: /Failed to verify gh-aw installation/,
  },
  {
    name: `approved upgrade to the local aw.yml min-version ${manifestGhAw} completes installation`,
    initialVersion: supportedGhAw,
    answer: "y",
    manifest: manifestGhAw,
    expectedLog: "curl\nadd\n",
    expectedVersion: manifestGhAw,
  },
];

for (const { name, initialVersion, answer, environment = {}, manifest, expectedLog, expectedVersion, failure } of terminalCases) {
  test(`install-gh-cao.sh on a terminal: ${name}`, { skip: process.platform === "win32" }, async (t) => {
    const { consumer, repository, env, log, installedGhAw } = await createConsumer(t, initialVersion);
    if (manifest) await writeFile(path.join(consumer, "aw.yml"), `min-version: ${manifest}\n`);
    const terminalEnv = { ...env, ...environment };
    const result = await runWithTerminal(consumer, terminalEnv, answer);
    assert.match(result.stdout, upgradePrompt);
    assert.equal(await log(), expectedLog);
    if (failure) {
      assert.notEqual(result.code, 0, result.stdout);
      assert.match(result.stdout, failure);
      assert.equal(await installedGhAw(), `${initialVersion}\n`);
      await assertNothingInstalled(consumer);
    } else if (expectedVersion) {
      assert.equal(result.code, 0, result.stdout);
      assert.equal(await installedGhAw(), `${expectedVersion}\n`);
      await assertCompleteInstall(consumer, terminalEnv, expectedVersion, repository);
    } else {
      assert.equal(result.code, 0, result.stdout);
      assert.match(result.stdout, manualUpgrade(supportedGhAw));
      assert.equal(await installedGhAw(), `${initialVersion}\n`);
      await assertNothingInstalled(consumer);
    }
  });
}

test("install-gh-cao.sh reruns restore missing policy without replacing consumer policy or requiring a launcher", async (t) => {
  const { root, consumer, repository, env, log } = await createConsumer(t, supportedGhAw);
  await runFile(consumer, env);
  assert.equal(await log(), "add\n");
  await assertCompleteInstall(consumer, env, supportedGhAw, repository);

  await rm(path.join(consumer, policyPath));
  await runFile(consumer, env);
  assert.equal(await log(), "add\n");
  await assertCompleteInstall(consumer, env, supportedGhAw, repository);

  // Existing policies are consumer-owned: reruns neither look up nor narrow their scope.
  const { FAKE_CONTROL_REPOSITORY: _, ...withoutLookup } = env;
  const customPolicy = '{"version":1,"gh-aw-version":"v9.9.9","control-plane":{"campaigns":{"custom":{}}}}\n';
  await writeFile(path.join(consumer, policyPath), customPolicy);
  await streamInstaller(consumer, withoutLookup);
  assert.equal(await log(), "add\n");
  assert.equal(await readFile(path.join(consumer, policyPath), "utf8"), customPolicy);
  assert.equal(await exists(path.join(consumer, "cao.sh")), false);

  await runFile(consumer, withoutLookup, ["githubnext/gh-cao@v1.2.3"]);
  assert.equal(await log(), "add\nadd-force\n");
  assert.equal(await readFile(path.join(consumer, policyPath), "utf8"), customPolicy);

  const ownerPolicy = `${JSON.stringify({
    version: 1,
    "gh-aw-version": supportedGhAw,
    "control-plane": { scope: { "allowed-owners": ["alpha-org"] }, campaigns: {} },
  })}\n`;
  await writeFile(path.join(consumer, policyPath), ownerPolicy);
  await streamInstaller(consumer, withoutLookup, ["githubnext/gh-cao@v1.2.3"]);
  assert.equal(await log(), "add\nadd-force\nadd-force\n");
  assert.equal(await readFile(path.join(consumer, policyPath), "utf8"), ownerPolicy);
  await executeFile(process.execPath, [path.join(".github", "workflows", "shared", "control.mjs"), "validate-policy", policyPath], {
    cwd: consumer, env: withoutLookup, timeout,
  });
});

for (const [failure, environment, expectedLog] of [
  ["campaign add fails", { FAKE_ADD_FAILURE: "1" }, "add\n"],
  ["the immutable archive download fails", { FAKE_CAO_ARCHIVE_STATUS: "503" }, "add\n"],
  ["runtime provenance verification fails", { FAKE_CAO_ATTESTATION_FAILURE: "1" }, "add\n"],
]) {
  test(`install-gh-cao.sh exits nonzero without policy when ${failure}`, async (t) => {
    const { consumer, env, log } = await createConsumer(t, supportedGhAw);
    const result = await runStreamed(consumer, { ...env, ...environment });
    assert.notEqual(result.code, 0);
    assert.equal(await log(), expectedLog);
    await assertNothingInstalled(consumer);
  });
}

for (const [description, repository] of [
  ["fails", undefined],
  ["returns no repository", ""],
  ["returns a malformed repository", "not a repository"],
  ["returns multiple repositories", "alpha-org/control\nbeta-org/ops.tools"],
]) {
  test(`install-gh-cao.sh exits nonzero without policy when the repository lookup ${description}`, async (t) => {
    const { consumer, env, log } = await createConsumer(t, supportedGhAw);
    const { FAKE_CONTROL_REPOSITORY: _, ...withoutLookup } = env;
    const lookupEnv = repository === undefined ? withoutLookup : { ...env, FAKE_CONTROL_REPOSITORY: repository };
    const result = await runStreamed(consumer, lookupEnv);
    assert.notEqual(result.code, 0);
    assert.match(result.stderr, /Open an existing GitHub repository checkout, or rerun with --create-repo OWNER\/NAME/);
    assert.equal(await log(), "");
    await assertNothingInstalled(consumer);
  });
}

test("install-gh-cao.sh creates only an explicitly requested private repository and installs inside its clone", async (t) => {
  const { consumer, env, repository, log } = await createConsumer(t, supportedGhAw);
  const result = await streamInstaller(consumer, env, ["--create-repo", repository]);
  assert.equal(await log(), "create-private\nadd\n");
  assert.match(result.stdout, new RegExp(`Created private control repository ${repository.replace("/", "\\/")}\\.`));
  assert.doesNotMatch(result.stdout, new RegExp(`https://github\\.com/${repository.replace("/", "\\/")}`));
  assert.match(result.stdout, /Open the new checkout: cd control/);
  await assertNothingInstalled(consumer);
  await assertCompleteInstall(path.join(consumer, "control"), env, supportedGhAw, repository);
});

test("install-gh-cao.sh interactively selects an eligible owner and repository name", { skip: process.platform === "win32" }, async (t) => {
  const { consumer, env, repository, log } = await createConsumer(t, supportedGhAw);
  const quoted = `'${installScript.replaceAll("'", "'\\''")}'`;
  const result = await runTerminal("bash", ["-c", `cat ${quoted} | bash -s -- --create-repo`], {
    cwd: consumer,
    env: { ...env, TERM: "xterm-256color", ACCESSIBLE: "", NO_COLOR: "" },
    timeout,
    input: "alpha-org\ncontrol\nn\n",
  });
  assert.equal(result.code, 0, result.stdout + result.stderr);
  assert.match(result.stdout, /Prerequisites and GitHub sign-in verified/);
  assert.match(result.stdout, /Create a private CAO control repository/);
  assert.match(result.stdout, /Repository owner \(OWNER\):/);
  assert.doesNotMatch(result.stdout, /enter select|Owner \[1-/);
  assert.match(result.stdout, /Repository name\s+control/);
  assert.equal(await log(), "owners-query\ncreate-private\nadd\n");
  await assertNothingInstalled(consumer);
  await assertCompleteInstall(path.join(consumer, "control"), env, supportedGhAw, repository);
});

test("install-gh-cao.sh requires typing both owner and repository name without defaults", { skip: process.platform === "win32" }, async (t) => {
  const repository = "alpha-org/control";
  const { consumer, env, log } = await createConsumer(t, supportedGhAw, { repository });
  const quoted = `'${installScript.replaceAll("'", "'\\''")}'`;
  const result = await runTerminal("bash", ["-c", `cat ${quoted} | bash -s -- --create-repo`], {
    cwd: consumer,
    env: { ...env, TERM: "xterm-256color", ACCESSIBLE: "", NO_COLOR: "" },
    timeout,
    input: "\nalpha-org\n\ncontrol\nn\n",
  });
  assert.equal(result.code, 0, result.stdout + result.stderr);
  assert.match(result.stdout, /blank input does not choose an owner/);
  assert.doesNotMatch(result.stdout, /Repository name\s+cao|Repository name \[/);
  assert.match(result.stdout, /Repository name\s+control/);
  assert.equal(await log(), "owners-query\ncreate-private\nadd\n");
  await assertCompleteInstall(path.join(consumer, "control"), env, supportedGhAw, repository);
});

test("install-gh-cao.sh rejects an ineligible typed owner before accepting an eligible owner", { skip: process.platform === "win32" }, async (t) => {
  const { consumer, env, repository, log } = await createConsumer(t, supportedGhAw);
  const quoted = `'${installScript.replaceAll("'", "'\\''")}'`;
  const result = await runTerminal("bash", ["-c", `cat ${quoted} | bash -s -- --create-repo`], {
    cwd: consumer,
    env: {
      ...env,
      TERM: "xterm-256color",
      ACCESSIBLE: "",
      NO_COLOR: "",
    },
    timeout,
    input: "unknown-owner\nalpha-org\ncontrol\nn\n",
  });
  assert.equal(result.code, 0, result.stdout + result.stderr);
  assert.doesNotMatch(result.stdout, /Repository name\s+cao/);
  assert.match(result.stdout, /Repository name\s+control/);
  assert.match(result.stdout, /Type an account where you can create repositories/);
  assert.equal(await log(), "owners-query\ncreate-private\nadd\n");
  await assertCompleteInstall(path.join(consumer, "control"), env, supportedGhAw, repository);
});

test("install-gh-cao.sh reports explicit-name availability failures before creating files", { skip: process.platform === "win32" }, async (t) => {
  const { consumer, env, log } = await createConsumer(t, supportedGhAw);
  const quoted = `'${installScript.replaceAll("'", "'\\''")}'`;
  const result = await runTerminal("bash", ["-c", `cat ${quoted} | bash -s -- --create-repo`], {
    cwd: consumer,
    env: {
      ...env,
      TERM: "xterm-256color",
      ACCESSIBLE: "",
      NO_COLOR: "",
      FAKE_REPOSITORY_CHECK_FAILURE: "alpha-org/control",
    },
    timeout,
    input: "alpha-org\ncontrol\n",
  });
  assert.notEqual(result.code, 0);
  assert.match(result.stdout + result.stderr, /Unable to check whether alpha-org\/control is available/);
  assert.equal(await log(), "owners-query\n");
  await assertNothingInstalled(consumer);
});

test("install-gh-cao.sh offers to create a private GitHub repository for a checkout without remotes", { skip: process.platform === "win32" }, async (t) => {
  const { consumer, env, repository, log } = await createConsumer(t, supportedGhAw);
  await executeFile("git", ["remote", "remove", "origin"], { cwd: consumer });
  const quoted = `'${installScript.replaceAll("'", "'\\''")}'`;
  const result = await runTerminal("bash", ["-c", `cat ${quoted} | bash`], {
    cwd: consumer,
    env: {
      ...env,
      TERM: "xterm-256color",
      ACCESSIBLE: "",
      NO_COLOR: "",
      FAKE_REPOSITORY_LOOKUP_FAILURE: "1",
    },
    timeout,
    input: "y\nalpha-org\ncontrol\nn\n",
  });
  assert.equal(result.code, 0, result.stdout + result.stderr);
  assert.match(result.stdout, /No GitHub control repository found/);
  assert.match(result.stdout, /Create a new private GitHub repository for this checkout/);
  assert.match(result.stdout, /Create a private GitHub control repository/);
  assert.equal(await log(), "owners-query\ncreate-private-current\nadd\n");
  await assertCompleteInstall(consumer, env, supportedGhAw, repository);
});

test("install-gh-cao.sh defaults missing-remote repository creation to no", { skip: process.platform === "win32" }, async (t) => {
  const { consumer, env, log } = await createConsumer(t, supportedGhAw);
  await executeFile("git", ["remote", "remove", "origin"], { cwd: consumer });
  const quoted = `'${installScript.replaceAll("'", "'\\''")}'`;
  const result = await runTerminal("bash", ["-c", `cat ${quoted} | bash`], {
    cwd: consumer,
    env: { ...env, FAKE_REPOSITORY_LOOKUP_FAILURE: "1" },
    timeout,
    input: "\n",
  });
  assert.notEqual(result.code, 0);
  assert.match(result.stdout, /Repository creation cancelled/);
  assert.equal(await log(), "");
  await assertNothingInstalled(consumer);
});

for (const [environment, message] of [
  [{ FAKE_EXISTING_REPOSITORIES: "alpha-org/control" }, /Repository alpha-org\/control already exists/],
  [{ FAKE_REPOSITORY_CHECK_FAILURE: "alpha-org/control" }, /Unable to check whether alpha-org\/control is available/],
]) {
  test(`install-gh-cao.sh validates the typed remote name before in-place creation: ${message}`, { skip: process.platform === "win32" }, async (t) => {
    const { consumer, env, log } = await createConsumer(t);
    await executeFile("git", ["remote", "remove", "origin"], { cwd: consumer });
    const result = await runTerminal("bash", [installScript], {
      cwd: consumer,
      env: {
        ...env, ...environment, TERM: "", ACCESSIBLE: "1", NO_COLOR: "1",
        FAKE_REPOSITORY_LOOKUP_FAILURE: "1",
      },
      timeout,
      input: "y\nalpha-org\ncontrol\n",
    });
    assert.notEqual(result.code, 0);
    assert.match(result.stdout + result.stderr, message);
    assert.equal(await log(), "owners-query\n");
    assert.equal((await executeFile("git", ["remote"], { cwd: consumer })).stdout, "");
    await assertNothingInstalled(consumer);
  });
}

test("install-gh-cao.sh offers to create and clone from outside a Git checkout", { skip: process.platform === "win32" }, async (t) => {
  const { root, env, repository, log } = await createConsumer(t, supportedGhAw);
  const start = path.join(root, "start");
  await mkdir(start);
  const quoted = `'${installScript.replaceAll("'", "'\\''")}'`;
  const result = await runTerminal("bash", ["-c", `cat ${quoted} | bash`], {
    cwd: start,
    env: { ...env, TERM: "xterm-256color", ACCESSIBLE: "", NO_COLOR: "" },
    timeout,
    input: "y\nalpha-org\ncontrol\nn\n",
  });
  assert.equal(result.code, 0, result.stdout + result.stderr);
  assert.match(result.stdout, /Create and clone a new private control repository/);
  assert.equal(await log(), "owners-query\ncreate-private\nadd\n");
  await assertCompleteInstall(path.join(start, "control"), env, supportedGhAw, repository);
});

test("install-gh-cao.sh requires typed owner and name in accessible mode", { skip: process.platform === "win32" }, async (t) => {
  const { consumer, env, repository, log } = await createConsumer(t, supportedGhAw);
  const quoted = `'${installScript.replaceAll("'", "'\\''")}'`;
  const result = await runTerminal("bash", ["-c", `cat ${quoted} | bash -s -- --create-repo`], {
    cwd: consumer,
    env: { ...env, TERM: "", ACCESSIBLE: "1", NO_COLOR: "1" },
    timeout,
    input: "2\nalpha-org\n\ncontrol\nn\n",
  });
  assert.equal(result.code, 0, result.stdout + result.stderr);
  assert.match(result.stdout, /Repository owner \(OWNER\):/);
  assert.match(result.stdout, /Repository name \(NAME\):/);
  assert.match(result.stdout, /Type an account where you can create repositories/);
  assert.match(result.stdout, /Use a repository name containing/);
  assert.doesNotMatch(result.stdout, /1\) alpha-user|2\) alpha-org|Repository name \[/);
  assert.doesNotMatch(result.stdout, /enter select/);
  assert.equal(await log(), "owners-query\ncreate-private\nadd\n");
  await assertNothingInstalled(consumer);
  await assertCompleteInstall(path.join(consumer, "control"), env, supportedGhAw, repository);
});

test("install-gh-cao.sh checks sign-in before upgrading gh-aw, creating repositories, or writing files", async (t) => {
  const { consumer, env, log } = await createConsumer(t);
  const result = await runStreamed(consumer, { ...env, FAKE_AUTH_FAILURE: "1" }, ["--create-repo", "alpha-org/control"]);
  assert.notEqual(result.code, 0);
  assert.match(result.stderr, /gh auth login/);
  assert.equal(await log(), "");
  assert.equal(await exists(path.join(consumer, "control")), false);
  await assertNothingInstalled(consumer);
});

test("install-gh-cao.sh uses one restrained spinner for a slow sign-in check", { skip: process.platform === "win32" }, async (t) => {
  const { consumer, env, log } = await createConsumer(t);
  const quoted = `'${installScript.replaceAll("'", "'\\''")}'`;
  const result = await runTerminal("bash", ["-c", `cat ${quoted} | bash -s -- --create-repo alpha-org/control`], {
    cwd: consumer,
    env: {
      ...env,
      TERM: "xterm-256color",
      ACCESSIBLE: "",
      NO_COLOR: "",
      FAKE_AUTH_DELAY: "0.35",
      FAKE_AUTH_FAILURE: "1",
    },
    timeout,
  });
  assert.notEqual(result.code, 0);
  const frames = [...result.stdout.matchAll(/[⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏][^\r\n]*?· (\d+)s elapsed/gu)];
  assert.ok(frames.length >= 1);
  assert.equal(frames.length, new Set(frames.map((match) => match[1])).size);
  assert.match(result.stdout, /\d+s elapsed/);
  assert.doesNotMatch(result.stdout, /Prerequisites and GitHub sign-in verified/);
  assert.equal(await log(), "");
  await assertNothingInstalled(consumer);
});

test("install-gh-cao.sh disables spinner animation in CI", { skip: process.platform === "win32" }, async (t) => {
  const { consumer, env, log } = await createConsumer(t);
  const quoted = `'${installScript.replaceAll("'", "'\\''")}'`;
  const result = await runTerminal("bash", ["-c", `cat ${quoted} | bash -s -- --create-repo alpha-org/control`], {
    cwd: consumer,
    env: {
      ...env,
      TERM: "xterm-256color",
      ACCESSIBLE: "",
      NO_COLOR: "",
      CI: "1",
      FAKE_AUTH_DELAY: "0.2",
      FAKE_AUTH_FAILURE: "1",
    },
    timeout,
  });
  assert.notEqual(result.code, 0);
  assert.doesNotMatch(result.stdout, /[⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏]|\u001b\[/);
  assert.match(result.stdout, /==> Checking tools and GitHub sign-in/);
  assert.equal(await log(), "");
  await assertNothingInstalled(consumer);
});

test("interactive bare installation stops cleanly with an explicit setup next step", { skip: process.platform === "win32" }, async (t) => {
  const { consumer, env, repository, log } = await createConsumer(t, supportedGhAw);
  const quoted = `'${installScript.replaceAll("'", "'\\''")}'`;
  const result = await runTerminal("bash", ["-c", `cat ${quoted} | bash`], {
    cwd: consumer,
    env: { ...env, TERM: "xterm-256color", FORCE_COLOR: "0", CI: "" },
    timeout,
  });
  assert.equal(result.code, 0, result.stdout + result.stderr);
  assert.doesNotMatch(result.stdout, /Continue to guided setup|Repositories CAO should read/);
  assert.match(result.stdout, /Next: gh cao setup/);
  assert.equal(await log(), "add\n");
  await assertCompleteInstall(consumer, env, supportedGhAw, repository);
});

test(`install-gh-cao.sh rejects GitHub CLI older than ${supportedGh} before sign-in or writes`, async (t) => {
  const { consumer, env, log } = await createConsumer(t);
  const result = await runStreamed(consumer, {
    ...env,
    FAKE_GH_VERSION: olderGh,
    FAKE_AUTH_FAILURE: "1",
  }, ["--create-repo", "alpha-org/control"]);
  assert.notEqual(result.code, 0);
  assert.match(result.stderr, new RegExp(`requires GitHub CLI ${supportedGh.replaceAll(".", "\\.")} or newer`));
  assert.match(result.stderr, new RegExp(`current: ${olderGh.replaceAll(".", "\\.")}`));
  assert.doesNotMatch(result.stderr, /gh auth login/);
  assert.equal(await log(), "");
  assert.equal(await exists(path.join(consumer, "control")), false);
  await assertNothingInstalled(consumer);
});

test("install-gh-cao.sh rejects an unparseable GitHub CLI version before sign-in or writes", async (t) => {
  const { consumer, env, log } = await createConsumer(t);
  const result = await runStreamed(consumer, {
    ...env,
    FAKE_GH_VERSION: "unknown",
    FAKE_AUTH_FAILURE: "1",
  }, ["--create-repo", "alpha-org/control"]);
  assert.notEqual(result.code, 0);
  assert.match(result.stderr, new RegExp(`requires GitHub CLI ${supportedGh.replaceAll(".", "\\.")} or newer`));
  assert.match(result.stderr, /current: unknown/);
  assert.doesNotMatch(result.stderr, /gh auth login/);
  assert.equal(await log(), "");
  assert.equal(await exists(path.join(consumer, "control")), false);
  await assertNothingInstalled(consumer);
});

test("streamed new-repository installation enters bundled setup without consuming script stdin", { skip: process.platform === "win32" }, async (t) => {
  const { consumer, env, repository, log } = await createConsumer(t, supportedGhAw);
  let step = 0;
  const steps = ["Repositories (comma-separated", "Apply this setup?"];
  const quoted = `'${installScript.replaceAll("'", "'\\''")}'`;
  const result = await runTerminal("bash", ["-c", `cat ${quoted} | bash -s -- --create-repo ${repository} --setup`], {
    cwd: consumer, env: { ...env, TERM: "xterm-256color", FORCE_COLOR: "0", CI: "" }, timeout,
    onOutput(output, write) {
      if (steps[step] && output.includes(steps[step])) {
        step += 1;
        write("\r");
      }
    },
  });
  assert.equal(result.code, 0, result.stdout + result.stderr);
  assert.equal(step, 2);
  assert.match(result.stdout, /Apply this setup\? \(y\/n\) \[n\]/);
  assert.doesNotMatch(result.stdout, /Monitoring configured locally|Actions pending/);
  assert.match(result.stdout, /Open the new checkout: cd control/);
  assert.doesNotMatch(result.stdout, /"command": "setup"/);
  assert.equal(await log(), "create-private\nadd\n");
  await assertCompleteInstall(path.join(consumer, "control"), env, supportedGhAw, repository);
});

test("install-gh-cao.sh refuses non-root checkout directories before installing or upgrading anything", async (t) => {
  const { consumer, env, log } = await createConsumer(t);
  const nested = path.join(consumer, "nested");
  await mkdir(nested);
  const result = await runStreamed(nested, env);
  assert.notEqual(result.code, 0);
  assert.match(result.stderr, /root of a GitHub repository checkout/);
  assert.equal(await log(), "");
  await assertNothingInstalled(nested);
});

test("install-gh-cao.sh refuses to create a repository when its clone directory already exists", async (t) => {
  const { consumer, env, log } = await createConsumer(t, supportedGhAw);
  await mkdir(path.join(consumer, "control"));
  const result = await runStreamed(consumer, env, ["--create-repo", "alpha-org/control"]);
  assert.notEqual(result.code, 0);
  assert.match(result.stderr, /Cannot clone into existing path/);
  assert.equal(await log(), "");
});

test("install-gh-cao.sh refuses an explicit clone path occupied by a dangling symlink", {
  skip: process.platform === "win32",
}, async (t) => {
  const { consumer, env, log } = await createConsumer(t, supportedGhAw);
  const occupied = path.join(consumer, "control");
  await fs.promises.symlink("missing-clone", occupied);
  const result = await runStreamed(consumer, env, ["--create-repo", "alpha-org/control"]);
  assert.notEqual(result.code, 0);
  assert.match(result.stderr, /Cannot clone into existing path/);
  assert.equal(await log(), "");
  assert.equal(await fs.promises.readlink(occupied), "missing-clone");
});

for (const [name, environment, arguments_] of [
  ["bare installation in CI", { CI: "1" }, []],
  ["guided installation without a terminal", { FAKE_CONTROL_VISIBILITY: "PUBLIC" }, ["--setup"]],
  ["guided installation in CI", { CI: "1", FAKE_CONTROL_VISIBILITY: "PUBLIC" }, ["--setup"]],
]) {
  test(`headless install-gh-cao.sh completes safe ${name}`, async (t) => {
    const { consumer, env, log } = await createConsumer(t, supportedGhAw);
    const creating = arguments_.includes("--create-repo");
    const repository = creating ? "alpha-org/new-control" : env.FAKE_CONTROL_REPOSITORY;
    const installEnv = { ...env, ...environment, FAKE_CONTROL_REPOSITORY: repository };
    const result = await streamInstaller(consumer, installEnv, arguments_);
    const setup = arguments_.includes("--setup");
    assert.equal(await log(), `${creating ? "create-private\n" : ""}add\n`);
    assert.match(result.stdout, /CAO installed and verified/);
    if (setup) {
      assert.match(result.stderr, /Non-interactive setup applied/);
      assert.match(result.stderr, /repository GITHUB_TOKEN \(workflow-token\)/);
      assert.match(result.stdout + result.stderr, /Actions operational artifacts for the local or cloud dashboard/);
      assert.doesNotMatch(result.stdout + result.stderr, /Pages|public dashboard|dashboardPages|pagesSettingsURL|dashboardURL/);
      assert.doesNotMatch(result.stdout + result.stderr, /Repositories CAO should read|Apply this setup\?/);
    }
    await assertCompleteInstall(
      creating ? path.join(consumer, "new-control") : consumer,
      installEnv,
      supportedGhAw,
      repository,
      { activity: setup },
    );
  });
}

for (const environment of [{}, { CI: "1" }]) {
  test(`headless setup refuses an explicitly created private repository${environment.CI ? " in CI" : ""}`, async (t) => {
    const { consumer, env, log } = await createConsumer(t, supportedGhAw);
    const result = await runStreamed(consumer, {
      ...env,
      ...environment,
      FAKE_CONTROL_REPOSITORY: "alpha-org/new-control",
    }, [
      "--create-repo", "alpha-org/new-control", "--setup",
    ]);
    assert.notEqual(result.code, 0);
    assert.match(result.stderr, /requires a public control repository/);
    assert.equal(await log(), "create-private\nadd\n");
  });
}

test("headless guided reinstallation preserves incompatible operator-owned policy", async (t) => {
  const { consumer, env, log } = await createConsumer(t, supportedGhAw);
  await streamInstaller(consumer, env);
  const customPolicy = '{"version":1,"gh-aw-version":"v9.9.9","control-plane":{"campaigns":{"custom":{}}}}\n';
  await writeFile(path.join(consumer, policyPath), customPolicy);
  const result = await runStreamed(consumer, {
    ...env,
    CI: "1",
    FAKE_CONTROL_VISIBILITY: "PUBLIC",
  }, ["--setup"]);
  assert.notEqual(result.code, 0);
  assert.match(result.stderr, /setup only configures a bare control plane with no declared campaigns/);
  assert.equal(await readFile(path.join(consumer, policyPath), "utf8"), customPolicy);
  assert.equal(await log(), "add\n");
});

test("headless setup does not reconfigure an existing standalone control plane", async (t) => {
  const { consumer, env, log } = await createConsumer(t, supportedGhAw);
  await streamInstaller(consumer, env);
  const customPolicy = JSON.stringify({
    version: 1, "gh-aw-version": supportedGhAw,
    "control-plane": { campaigns: {}, workflows: { audit: { enabled: true } } },
  });
  await writeFile(path.join(consumer, policyPath), customPolicy);
  const result = await runStreamed(consumer, {
    ...env, CI: "1", FAKE_CONTROL_VISIBILITY: "PUBLIC",
  }, ["--setup"]);
  assert.notEqual(result.code, 0);
  assert.match(result.stderr, /setup only configures a bare control plane with no declared standalone workflows/);
  assert.equal(await readFile(path.join(consumer, policyPath), "utf8"), customPolicy);
  assert.equal(await log(), "add\n");
});

test("an explicit release downloads the matching Go binary without a latest-release lookup", async (t) => {
  const { consumer, env } = await createConsumer(t, supportedGhAw);
  await streamInstaller(consumer, { ...env, FAKE_LATEST_CAO_RELEASE_FAILURE: "1" }, ["githubnext/gh-cao@v2.3.4"]);
  const downloads = await readFile(env.FAKE_CAO_DOWNLOADS, "utf8");
  assert.match(downloads, /\/v2\.3\.4\/gh-cao-(?:darwin|linux|windows)-(?:amd64|arm64)(?:\.exe)?\n/);
  assert.match(downloads, /\/v2\.3\.4\/checksums\.txt\n/);
  const version = await executeFile("gh", ["cao", "--version"], { cwd: consumer, env });
  assert.equal(version.stdout.trim(), "gh cao version v2.3.4");
  assert.equal(await readFile(env.FAKE_CAO_RELEASE_REQUESTS, "utf8"), "repos/githubnext/gh-cao/releases/tags/v2.3.4\n");
});

const publishedExtensionAssets = [
  "gh-cao-darwin-amd64", "gh-cao-darwin-arm64", "gh-cao-linux-amd64", "gh-cao-linux-arm64",
  "gh-cao-windows-amd64.exe", "gh-cao-windows-arm64.exe", "checksums.txt",
];
for (const [name, assets, missing] of [
  ["runtime-only stable release", ["cao-runtime-sha.tgz", "cao-runtime-sha.tgz.sha256"], "checksums.txt"],
  ["mixed binary names", publishedExtensionAssets.map((asset) => asset === "gh-cao-linux-amd64" ? "linux-amd64" : asset), "gh-cao-linux-amd64"],
  ["missing Windows binary", publishedExtensionAssets.filter((asset) => asset !== "gh-cao-windows-arm64.exe"), "gh-cao-windows-arm64.exe"],
  ["missing checksums", publishedExtensionAssets.filter((asset) => asset !== "checksums.txt"), "checksums.txt"],
  ["duplicate binary", [...publishedExtensionAssets, "gh-cao-linux-amd64"], "gh-cao-linux-amd64"],
]) {
  test(`installer rejects a ${name} before installing or replacing tools`, async (t) => {
    const { consumer, env, log } = await createConsumer(t);
    const directory = path.join(env.XDG_DATA_HOME, "gh", "extensions", "gh-cao");
    const binary = path.join(directory, process.platform === "win32" ? "gh-cao.exe" : "gh-cao");
    await mkdir(directory, { recursive: true });
    await writeFile(binary, "previous verified executable");
    const result = await runStreamed(consumer, {
      ...env,
      FAKE_CAO_RELEASE_ASSETS: assets.join("\n"),
    });
    assert.notEqual(result.code, 0);
    assert.match(result.stderr, /CAO release v1\.2\.3 is not installable/);
    assert.ok(result.stderr.includes(`expected exactly one uploaded, nonempty ${missing} asset`), result.stderr);
    assert.match(result.stderr, /publish a corrected release/);
    assert.match(result.stderr, /No CAO files were written/);
    assert.equal(await log(), "", "gh-aw must not be installed before release preflight");
    assert.equal(await exists(env.FAKE_CAO_DOWNLOADS), false);
    assert.equal(await readFile(binary, "utf8"), "previous verified executable");
    assert.deepEqual(await fs.promises.readdir(directory), [path.basename(binary)]);
    assert.equal(await readFile(env.FAKE_CAO_RELEASE_REQUESTS, "utf8"), "repos/githubnext/gh-cao/releases/tags/v1.2.3\n");
    await assertNothingInstalled(consumer);
  });
}

test("installer prefers the modern naming set when both complete sets are published", async (t) => {
  const { consumer, env } = await createConsumer(t, supportedGhAw);
  await streamInstaller(consumer, {
    ...env,
    FAKE_CAO_RELEASE_ASSETS: [
      ...publishedExtensionAssets,
      ...publishedExtensionAssets.slice(0, -1).map((asset) => asset.replace(/^gh-cao-/, "")),
    ].join("\n"),
  });
  const downloads = await readFile(env.FAKE_CAO_DOWNLOADS, "utf8");
  assert.match(downloads, /\/gh-cao-(?:darwin|linux|windows)-(?:amd64|arm64)(?:\.exe)?\n/);
});

for (const corrupt of [false, true]) {
  test(`published prerelease with legacy binaries ${corrupt ? "rejects a corrupt checksum" : "installs with verified checksums"}`, async (t) => {
    const { consumer, env, log } = await createConsumer(t, supportedGhAw);
    const result = await runStreamed(consumer, {
      ...env,
      FAKE_CAO_RELEASE_LIST: "false\ttrue\tv1.2.5\nfalse\tfalse\tv1.2.3",
      FAKE_CAO_RELEASE_ASSETS: publishedExtensionAssets.map((asset) => asset.replace(/^gh-cao-/, "")).join("\n"),
      FAKE_CAO_ASSET_PREFIX: "",
      ...(corrupt ? { FAKE_CAO_CHECKSUM_MODE: "corrupt" } : {}),
    }, [`githubnext/gh-cao@${revision}`]);
    assert.equal(result.code, corrupt ? 1 : 0, result.stderr);
    const downloads = await readFile(env.FAKE_CAO_DOWNLOADS, "utf8");
    assert.match(downloads, /\/v1\.2\.5\/(?:darwin|linux|windows)-(?:amd64|arm64)(?:\.exe)?\n/);
    assert.doesNotMatch(downloads, /\/gh-cao-(?:darwin|linux|windows)/);
    assert.equal(await readFile(env.FAKE_CAO_RELEASE_REQUESTS, "utf8"), "repos/githubnext/gh-cao/releases/tags/v1.2.5\n");
    if (corrupt) {
      assert.match(result.stderr, /checksum verification failed/);
      assert.equal(await log(), "");
      await assertNothingInstalled(consumer);
    } else {
      const version = await executeFile("gh", ["cao", "--version"], { cwd: consumer, env });
      assert.equal(version.stdout.trim(), "gh cao version v1.2.5");
      assert.equal(await log(), "add\n");
    }
  });
}

test("an older extension's initial schema URI is aligned with the verified catalog without changing existing policy", async (t) => {
  const { consumer, repository, env } = await createConsumer(t, supportedGhAw);
  const legacyEnv = {
    ...env,
    FAKE_CAO_POLICY_SCHEMA: "https://raw.githubusercontent.com/githubnext/gh-aw-cao/main/.github/workflows/shared/cao.schema.json",
  };
  await streamInstaller(consumer, legacyEnv);
  await assertCompleteInstall(consumer, env, supportedGhAw, repository);
  const policy = JSON.parse(await readFile(path.join(consumer, policyPath), "utf8"));
  assert.equal(policy.$schema, "https://raw.githubusercontent.com/githubnext/gh-cao/main/.github/workflows/shared/cao.schema.json");
  const existing = `${JSON.stringify({ ...policy, $schema: legacyEnv.FAKE_CAO_POLICY_SCHEMA })}\n`;
  await writeFile(path.join(consumer, policyPath), existing);
  await streamInstaller(consumer, legacyEnv);
  assert.equal(await readFile(path.join(consumer, policyPath), "utf8"), existing);
});

test("initial schema alignment rejects an unknown schema rather than repairing it", async (t) => {
  const { consumer, env } = await createConsumer(t, supportedGhAw);
  const result = await runStreamed(consumer, {
    ...env,
    FAKE_CAO_POLICY_SCHEMA: "https://example.invalid/untrusted.schema.json",
  });
  assert.notEqual(result.code, 0);
  assert.match(result.stderr, /initialized policy schema does not match the installed catalog/);
  assert.doesNotMatch(result.stdout, /CAO installed and verified/);
  assert.equal(JSON.parse(await readFile(path.join(consumer, policyPath), "utf8")).$schema, "https://example.invalid/untrusted.schema.json");
});

test("explicit release asset lookup failures stop before installing tools or CAO files", async (t) => {
  const { consumer, env, log } = await createConsumer(t);
  const result = await runStreamed(consumer, {
    ...env,
    FAKE_LATEST_CAO_RELEASE_FAILURE: "1",
    FAKE_CAO_ASSET_LOOKUP_FAILURE: "1",
  }, ["githubnext/gh-cao@v2.3.4"]);
  assert.notEqual(result.code, 0);
  assert.match(result.stderr, /release asset lookup failed/);
  assert.match(result.stderr, /Unable to inspect CAO extension release v2\.3\.4/);
  assert.equal(await log(), "");
  assert.equal(await exists(env.FAKE_CAO_DOWNLOADS), false);
  assert.equal(await exists(env.XDG_DATA_HOME), false);
  await assertNothingInstalled(consumer);
});

for (const [name, override, message] of [
  ["download failure", { FAKE_CAO_BINARY_FAILURE: "1" }, /binary download failed/],
  ["checksum mismatch", { FAKE_CAO_CHECKSUM_MODE: "corrupt" }, /checksum verification failed/],
  ["missing checksum", { FAKE_CAO_CHECKSUM_MODE: "missing" }, /exactly one SHA-256 digest/],
  ["duplicate checksum", { FAKE_CAO_CHECKSUM_MODE: "duplicate" }, /exactly one SHA-256 digest/],
  ["embedded version mismatch", { FAKE_CAO_BINARY_VERSION: "v0.0.1" }, /version verification failed/],
]) {
  test(`Go extension installation preserves an existing binary on ${name}`, async (t) => {
    const { consumer, env, log } = await createConsumer(t, supportedGhAw);
    const directory = path.join(env.XDG_DATA_HOME, "gh", "extensions", "gh-cao");
    const binary = path.join(directory, process.platform === "win32" ? "gh-cao.exe" : "gh-cao");
    await mkdir(directory, { recursive: true });
    await writeFile(binary, "previous verified executable");
    const result = await runStreamed(consumer, { ...env, ...override });
    assert.notEqual(result.code, 0);
    assert.match(result.stderr, message);
    assert.equal(await readFile(binary, "utf8"), "previous verified executable");
    assert.equal(await log(), "");
    assert.deepEqual((await fs.promises.readdir(directory)).sort(), [path.basename(binary)]);
    await assertNothingInstalled(consumer);
  });
}

async function runExtensionInstaller(consumer, env, prefix = "gh-cao-") {
  const extensionInstaller = installerSource.match(/^install_cao_extension\(\) \([\s\S]*?^\)/m)?.[0];
  const publicCurl = installerSource.match(/^public_curl\(\) \{[\s\S]*?^\}/m)?.[0];
  assert.ok(extensionInstaller, "the production extension installer is available");
  assert.ok(publicCurl, "the production anonymous download helper is available");
  return executeFile("bash", [
    "-c",
    `set -euo pipefail\ninfo() { :; }\nsuccess() { :; }\n${publicCurl}\n${extensionInstaller}\ninstall_cao_extension v1.2.3 "$1"`,
    "extension-installer",
    prefix,
  ], { cwd: consumer, env, timeout });
}

test("Go extension verifies checksums without filename-escape markers in native Windows paths", async (t) => {
  const { root, consumer, env } = await createConsumer(t, supportedGhAw);
  await writeFile(path.join(root, "bin", "sha256sum"), `#!/usr/bin/env bash
node --input-type=module -e '
  import { readFileSync } from "node:fs";
  import { createHash } from "node:crypto";
  const file = process.argv.slice(1).filter((argument) => argument !== "-b")[0];
  const digest = createHash("sha256").update(readFileSync(file ?? 0)).digest("hex");
  console.log((file ? "\\\\" : "") + digest + "  " + (file ?? "-"));
' -- "$@"
`, { mode: 0o755 });
  await runExtensionInstaller(consumer, {
    ...env,
    XDG_DATA_HOME: path.join(root, "data\\native-windows-path"),
  });
  const binary = path.join(root, "data\\native-windows-path", "gh", "extensions", "gh-cao", `gh-cao${process.platform === "win32" ? ".exe" : ""}`);
  const { stdout } = await executeFile("bash", [binary, "--version"], { env });
  assert.equal(stdout.trim(), "gh cao version v1.2.3");
});

for (const [system, machine, asset, prefix] of [
  ["Linux", "x86_64", "linux-amd64"],
  ["Linux", "aarch64", "linux-arm64"],
  ["Darwin", "x86_64", "darwin-amd64"],
  ["Darwin", "arm64", "darwin-arm64"],
  ["MINGW64_NT-10.0", "x86_64", "windows-amd64.exe"],
  ["MSYS_NT-10.0", "aarch64", "windows-arm64.exe"],
].flatMap(([system, machine, asset]) => ["gh-cao-", ""].map((prefix) => [system, machine, asset, prefix]))) {
  test(`Go extension selects and installs the published ${prefix}${asset} asset`, async (t) => {
    const { root, consumer, env } = await createConsumer(t, supportedGhAw);
    await writeFile(path.join(root, "bin", "uname"), `#!/usr/bin/env bash\nif [[ "$1" == -s ]]; then echo "${system}"; else echo "${machine}"; fi\n`, { mode: 0o755 });
    const useLocalAppData = system.startsWith("MSYS");
    const useHome = system === "Darwin" && machine === "x86_64";
    const installEnv = {
      ...env,
      HOME: path.join(root, "home"),
      LOCALAPPDATA: path.join(root, "local-app-data"),
      FAKE_CAO_ASSET_PREFIX: prefix,
      ...((useLocalAppData || useHome) ? { XDG_DATA_HOME: "" } : {}),
    };
    await runExtensionInstaller(consumer, installEnv, prefix);
    const downloads = (await readFile(env.FAKE_CAO_DOWNLOADS, "utf8")).trim().split("\n");
    assert.deepEqual(downloads, [
      `https://github.com/githubnext/gh-cao/releases/download/v1.2.3/${prefix}${asset}`,
      "https://github.com/githubnext/gh-cao/releases/download/v1.2.3/checksums.txt",
    ]);
    const extensionRoot = useLocalAppData
      ? path.join(installEnv.LOCALAPPDATA, "GitHub CLI")
      : useHome ? path.join(installEnv.HOME, ".local", "share", "gh") : path.join(env.XDG_DATA_HOME, "gh");
    const binary = path.join(extensionRoot, "extensions", "gh-cao", `gh-cao${asset.endsWith(".exe") ? ".exe" : ""}`);
    const { stdout } = await executeFile("bash", [binary, "--version"], { env: installEnv });
    assert.equal(stdout.trim(), "gh cao version v1.2.3");
  });
}

for (const [system, machine, message] of [
  ["FreeBSD", "x86_64", /Unsupported CAO extension operating system/],
  ["Linux", "riscv64", /Unsupported CAO extension architecture/],
]) {
  test(`Go extension rejects unsupported ${system}/${machine} before downloading`, async (t) => {
    const { root, consumer, env } = await createConsumer(t, supportedGhAw);
    await writeFile(path.join(root, "bin", "uname"), `#!/usr/bin/env bash\nif [[ "$1" == -s ]]; then echo "${system}"; else echo "${machine}"; fi\n`, { mode: 0o755 });
    await assert.rejects(runExtensionInstaller(consumer, env), (error) => {
      assert.match(error.stderr, message);
      return true;
    });
    assert.equal(fs.existsSync(env.FAKE_CAO_DOWNLOADS), false);
    assert.equal(fs.existsSync(env.XDG_DATA_HOME), false);
  });
}

for (const kind of ["source", "linked", "non-file"]) {
  test(`Go extension refuses to overwrite a ${kind} installation`, async (t) => {
    const { root, consumer, env } = await createConsumer(t, supportedGhAw);
    const directory = path.join(env.XDG_DATA_HOME, "gh", "extensions", "gh-cao");
    await mkdir(path.dirname(directory), { recursive: true });
    if (kind === "linked") {
      const source = path.join(root, "linked-source");
      await mkdir(source);
      await symlink(source, directory, "junction");
    } else {
      await mkdir(directory);
      if (kind === "source") await writeFile(path.join(directory, ".git"), "gitdir: source\n");
      else await mkdir(path.join(directory, `gh-cao${process.platform === "win32" ? ".exe" : ""}`));
    }
    await assert.rejects(runExtensionInstaller(consumer, env), (error) => {
      assert.match(error.stderr, /Refusing to replace/);
      return true;
    });
    assert.equal(fs.existsSync(env.FAKE_CAO_DOWNLOADS), false);
  });
}

for (const [name, environment, message] of [
  ["the selected name is occupied", { FAKE_EXISTING_REPOSITORIES: "alpha-org/control" }, /Repository alpha-org\/control already exists/],
  ["the availability check fails", { FAKE_REPOSITORY_CHECK_FAILURE: "alpha-org/control" }, /Unable to check whether alpha-org\/control is available/],
]) {
  test(`explicit creation fails before mutation when ${name}`, async (t) => {
    const { consumer, env, log } = await createConsumer(t);
    const result = await runStreamed(consumer, {
      ...env, CI: "1", ...environment,
    }, ["--create-repo", "alpha-org/control"]);
    assert.notEqual(result.code, 0);
    assert.match(result.stderr, message);
    assert.equal(await log(), "");
    assert.equal(await exists(path.join(consumer, "control")), false);
    await assertNothingInstalled(consumer);
  });
}

test("install-gh-cao.sh never changes an explicitly requested repository name on collision", async (t) => {
  const { root, env, log } = await createConsumer(t, supportedGhAw);
  const outsideCheckout = path.join(root, "outside-checkout");
  await mkdir(outsideCheckout);
  const result = await runStreamed(outsideCheckout, {
    ...env,
    CI: "1",
    FAKE_CONTROL_REPOSITORY: "alpha-org/control",
    FAKE_CREATE_REPOSITORY_FAILURE: "1",
  }, ["--create-repo", "alpha-org/control"]);
  assert.notEqual(result.code, 0);
  assert.match(result.stderr, /Repository already exists/);
  assert.doesNotMatch(result.stderr, /UTC timestamp|creating a fresh private control repository/);
  assert.equal(await log(), "");
  await assertNothingInstalled(outsideCheckout);
});

test("install-gh-cao.sh in CI applies public defaults without prompting on an available controlling terminal", { skip: process.platform === "win32" }, async (t) => {
  const { consumer, env, log } = await createConsumer(t, supportedGhAw);
  const result = await runTerminal("bash", [installScript, "--setup"], {
    cwd: consumer,
    env: { ...env, CI: "1", FAKE_CONTROL_VISIBILITY: "PUBLIC" },
    timeout,
    input: "",
  });
  assert.equal(result.code, 0, result.stdout + result.stderr);
  assert.match(result.stdout, /Non-interactive setup applied/);
  assert.equal(await log(), "add\n");
});

test("install-gh-cao.sh in CI fails an old gh-aw without reading the controlling terminal", { skip: process.platform === "win32" }, async (t) => {
  const { consumer, env, log } = await createConsumer(t, olderGhAw);
  const result = await runTerminal("bash", [installScript], {
    cwd: consumer, env: { ...env, CI: "1" }, timeout, input: "",
  });
  assert.notEqual(result.code, 0);
  assert.match(result.stdout, manualUpgrade(supportedGhAw));
  assert.doesNotMatch(result.stdout, /Upgrade it now/);
  assert.equal(await log(), "");
  await assertNothingInstalled(consumer);
});

for (const arguments_ of [
  ["--create-repo"],
  ["--create-repo", "not-a-coordinate"],
  ["--create-repo", "owner/../escape"],
  ["--unknown"],
]) {
  test(`install-gh-cao.sh rejects unusable onboarding arguments before mutations: ${arguments_.join(" ")}`, async (t) => {
    const { consumer, env, log } = await createConsumer(t);
    const result = await runStreamed(consumer, env, arguments_);
    assert.notEqual(result.code, 0);
    assert.equal(await log(), "");
    await assertNothingInstalled(consumer);
  });
}

for (const arguments_ of [
  ["--quickstart"],
  ["--target"],
  ["--target", "alpha-org/project"],
  ["--setup", "--quickstart"],
  ["--quickstart", "--target", "alpha-org/project", "--create-repo", "alpha-org/control"],
  ["--create-repo", "alpha-org/control", "--quickstart"],
  ["--create-repo", "alpha-org/control", "--target", "alpha-org/project"],
]) {
  test(`install-gh-cao.sh rejects removed flags before sign-in or mutations: ${arguments_.join(" ")}`, async (t) => {
    const { consumer, env, log } = await createConsumer(t);
    const result = await runStreamed(consumer, { ...env, FAKE_AUTH_FAILURE: "1" }, arguments_);
    const removedFlag = arguments_.find((argument) => argument === "--quickstart" || argument === "--target");
    assert.equal(result.code, 1);
    assert.ok(result.stderr.includes(`Unknown option: ${removedFlag}`), result.stderr);
    assert.doesNotMatch(result.stdout + result.stderr, /Checking tools|gh auth login|Interactive repository creation/);
    assert.equal(await log(), "");
    assert.equal(await exists(env.XDG_DATA_HOME), false);
    assert.equal(await exists(env.FAKE_CAO_DOWNLOADS), false);
    assert.equal(await exists(env.FAKE_CAO_RELEASE_REQUESTS), false);
    assert.equal(await exists(path.join(consumer, "control")), false);
    await assertNothingInstalled(consumer);
  });
}

test("streamed install-gh-cao.sh defaults Activity authentication to the control repository workflow token", async (t) => {
  const { root, consumer, repository, env, log } = await createConsumer(t, supportedGhAw);
  await streamInstaller(consumer, env);
  assert.equal(await log(), "add\n");

  const activityEnv = activityEnvironment(root, consumer, env, repository);
  const settings = await resolveActivitySettings(consumer, activityEnv);
  assert.equal(settings.policy_resolution.status, "available", settings.policy_resolution.reason);
  assert.deepEqual(settings.allowed_owners, [repository.split("/")[0]]);
  assert.deepEqual(settings.allowed_repositories, [repository]);
  assert.deepEqual(settings.campaigns, {});
  const authentication = await import(pathToFileURL(path.join(consumer, "activity", "authentication.mjs")).href);
  assert.deepEqual(
    authentication.activityCollectionPlan(settings, { controlRepository: repository }),
    [{
      owner: repository.split("/")[0],
      repositories: [repository],
      credentialRepository: repository,
      artifact: repository.split("/")[0].toLowerCase(),
    }],
  );
  for (const broaderScope of [
    { ...settings, allowed_repositories: [repository, "alpha-org/service"] },
    { ...settings, allowed_repositories: [] },
  ]) {
    assert.throws(
      () => authentication.activityCollectionPlan(broaderScope, { controlRepository: repository }),
      /GITHUB_TOKEN can collect CAO Activity only for the control repository/,
    );
  }
});

test("installed Activity reserves owner-wide discovery for App mode", async (t) => {
  const { root, consumer, repository, env } = await createConsumer(t, supportedGhAw);
  materializeCaoFromSource("root", catalog, consumer);
  for (const { source, destination } of addedFiles) {
    const target = path.join(consumer, destination);
    await mkdir(path.dirname(target), { recursive: true });
    fs.cpSync(path.join(catalog, source), target, { recursive: true });
  }
  await writeFile(path.join(consumer, policyPath), `${JSON.stringify({
    version: 1,
    "gh-aw-version": supportedGhAw,
    "control-plane": { scope: { "allowed-owners": [repository.split("/")[0]] }, campaigns: {} },
  })}\n`);

  const activityEnv = activityEnvironment(root, consumer, env, repository);
  const settings = await resolveActivitySettings(consumer, activityEnv);
  assert.equal(settings.policy_resolution.status, "available", settings.policy_resolution.reason);
  assert.deepEqual(settings.allowed_repositories, []);
  const authentication = await import(pathToFileURL(path.join(consumer, "activity", "authentication.mjs")).href);
  assert.throws(
    () => authentication.activityCollectionPlan(settings, {
      authMode: "pat",
      controlRepository: repository,
      patRepositoryMap: "{}",
    }),
    /PAT-mode CAO Activity requires an exact allowed-repositories scope/,
  );
  assert.deepEqual(
    authentication.activityCollectionPlan(settings, {
      authMode: "app",
      controlRepository: repository,
    }).map(({ owner, repositories }) => ({ owner, repositories })),
    [{ owner: repository.split("/")[0], repositories: [] }],
  );
});
