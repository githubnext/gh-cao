import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import test from "node:test";
import { gunzipSync } from "node:zlib";

import {
  AZURE_ASSET_ALLOWLIST,
  AZURE_ASSET_MANIFEST,
  buildCaoAzureAssets,
  selectAzureAssetFiles,
} from "../../scripts/build-cao-azure-assets.mjs";

const root = path.resolve(import.meta.dirname, "../..");
const git = (...args) => {
  const result = spawnSync("git", ["-C", root, ...args], { encoding: "utf8", maxBuffer: 64 * 1024 * 1024 });
  assert.equal(result.status, 0, result.stderr);
  return result.stdout;
};
const revision = git("rev-parse", "HEAD").trim();

function readTar(archive) {
  const tar = gunzipSync(archive);
  const entries = [];
  for (let offset = 0; offset + 512 <= tar.length;) {
    const header = tar.subarray(offset, offset + 512);
    if (header.every((byte) => byte === 0)) break;
    const field = (start, length) => header.subarray(start, start + length).toString("utf8").replace(/\0.*$/s, "");
    const name = field(0, 100);
    const prefix = field(345, 155);
    const size = Number.parseInt(field(124, 12), 8);
    entries.push({
      path: prefix ? `${prefix}/${name}` : name,
      type: field(156, 1),
      mode: Number.parseInt(field(100, 8), 8),
      uid: Number.parseInt(field(108, 8), 8),
      gid: Number.parseInt(field(116, 8), 8),
      mtime: Number.parseInt(field(136, 12), 8),
      content: tar.subarray(offset + 512, offset + 512 + size),
    });
    offset += 512 + Math.ceil(size / 512) * 512;
  }
  return entries;
}

const workspace = mkdtempSync(path.join(tmpdir(), "cao-azure-assets-test-"));
test.after(() => rmSync(workspace, { recursive: true, force: true }));
const first = buildCaoAzureAssets(root, path.join(workspace, "first"), revision);
const second = buildCaoAzureAssets(root, path.join(workspace, "second"), revision);
const entries = readTar(readFileSync(first.archive));
const files = entries.map((entry) => entry.path);

test("Azure asset bundle is byte-for-byte reproducible", () => {
  assert.equal(path.basename(first.archive), `cao-azure-${revision}.tgz`);
  assert.equal(first.digest, second.digest);
  assert.ok(readFileSync(first.archive).equals(readFileSync(second.archive)));
  assert.equal(readFileSync(`${first.archive}.sha256`, "utf8"), `${first.digest}  cao-azure-${revision}.tgz\n`);
  for (const entry of entries) {
    assert.equal(entry.type, "0", `${entry.path} must be a regular file`);
    assert.equal(entry.uid, 0);
    assert.equal(entry.gid, 0);
    assert.equal(entry.mtime, 499162500);
    assert.ok([0o644, 0o755].includes(entry.mode), `${entry.path} has mode ${entry.mode.toString(8)}`);
  }
  assert.deepEqual(files, files.toSorted());
});

test("Azure asset bundle contains exactly the allowlisted Docker build inputs", () => {
  assert.deepEqual(AZURE_ASSET_ALLOWLIST, [
    ".github/workflows/shared/authority.mjs",
    ".github/workflows/shared/policy.mjs",
    ".github/workflows/shared/actions-log.mjs",
    ".github/workflows/shared/value-acquisition.mjs",
    "dashboard/report/bundle-dashboards.mjs",
    "dashboard/report/compose-dashboard-documents.mjs",
    "dashboard/report/configure-site.mjs",
    "dashboard/site/apple-touch-icon.png",
    "dashboard/site/dashboard-fragments/",
    "dashboard/site/dashboard.json",
    "dashboard/site/favicon.svg",
    "dashboard/site/icon-192.png",
    "dashboard/site/icon-512.png",
    "dashboard/site/icon-maskable-512.png",
    "dashboard/site/index.html",
    "dashboard/site/manifest.webmanifest",
    "dashboard/site/package-lock.json",
    "dashboard/site/package.json",
    "dashboard/site/scripts/build.mjs",
    "dashboard/site/scripts/llms.mjs",
    "dashboard/site/service-worker.js",
    "dashboard/site/src/",
    "dashboard/site/tsconfig.json",
    "internal/dashboardquery/",
    "server/Dockerfile",
    "server/azure/empty-data/",
    "server/azure/main.json",
    "server/azure/provision.sh",
    "server/azure/provision.sql",
    "server/cmd/cao-dashboard/",
    "server/cmd/cao-webhook-gateway/",
    "server/go.mod",
    "server/go.sum",
    "server/internal/",
    "server/postgres/",
  ]);
  const tracked = git("ls-tree", "-r", "--name-only", "--full-tree", revision).split("\n").filter(Boolean);
  const expected = [...selectAzureAssetFiles(tracked), AZURE_ASSET_MANIFEST].toSorted();
  assert.deepEqual(files, expected);
  for (const file of files.filter((candidate) => candidate !== AZURE_ASSET_MANIFEST)) {
    assert.ok(
      AZURE_ASSET_ALLOWLIST.some((entry) => entry.endsWith("/") ? file.startsWith(entry) : file === entry),
      `${file} is outside the allowlist`,
    );
  }
  for (const entry of AZURE_ASSET_ALLOWLIST) {
    assert.ok(files.some((file) => entry.endsWith("/") ? file.startsWith(entry) : file === entry), `${entry} is missing`);
  }
});

test("Azure asset bundle excludes policy, credentials, VCS data, dependencies, tests, and docs", () => {
  const forbidden = [
    [/(^|\/)cao(\.[a-z0-9-]+)?\.json$/, "adopter policy"],
    [/(^|\/)\.git(\/|$)/, "Git metadata"],
    [/(^|\/)\.env/, "environment files"],
    [/\.(pem|key|p12|pfx|crt|cer|der)$/i, "keys and certificates"],
    [/(^|\/)id_(rsa|dsa|ecdsa|ed25519)/, "SSH keys"],
    [/(^|\/)\.npmrc$/, "npm credentials"],
    [/(^|\/)node_modules\//, "dependencies"],
    [/(^|\/)(tests?|testdata|testutil|__tests__)\//, "tests"],
    [/_test\.go$|\.(test|spec)\.[cm]?[jt]sx?$/, "tests"],
    [/(^|\/)docs\/|\.md$/i, "documentation"],
    [/^\.github\/(workflows\/(?!shared\/(?:authority|policy|actions-log|value-acquisition)\.mjs$)|aw\/|cao\/)/, "control-plane records"],
  ];
  for (const [pattern, label] of forbidden) {
    const matches = files.filter((file) => pattern.test(file));
    assert.deepEqual(matches, [], `bundle must not contain ${label}`);
  }
  assert.ok(!files.includes(".github/workflows/cao.json"));
  assert.ok(!files.includes(".github/workflows/cao.azure.json"));
});

test("Azure asset manifest records the source revision and every file digest", () => {
  const manifestEntry = entries.find((entry) => entry.path === AZURE_ASSET_MANIFEST);
  const manifest = JSON.parse(manifestEntry.content.toString("utf8"));
  assert.equal(manifest.schemaVersion, 1);
  assert.equal(manifest.revision, revision);
  assert.equal(manifest.dockerfile, "server/Dockerfile");
  assert.equal(manifest.template, "server/azure/main.json");
  assert.deepEqual(manifest.targets, ["azure-functions-runtime", "azure-ingest-runtime", "azure-empty-ingest-runtime", "azure-db-provision"]);
  const contents = entries.filter((entry) => entry.path !== AZURE_ASSET_MANIFEST);
  assert.deepEqual(Object.keys(manifest.files), contents.map((entry) => entry.path));
  for (const entry of contents) {
    assert.equal(manifest.files[entry.path], createHash("sha256").update(entry.content).digest("hex"), entry.path);
    const blob = spawnSync("git", ["-C", root, "show", `${revision}:${entry.path}`], { maxBuffer: 64 * 1024 * 1024 }).stdout;
    assert.ok(blob.equals(entry.content), `${entry.path} must match the source revision`);
  }
});

test("Azure asset selection preserves runtime model aliases while excluding test fixtures", () => {
  const tracked = git("ls-tree", "-r", "--name-only", "--full-tree", revision).split("\n").filter(Boolean);
  const aliases = "server/internal/collectnative/gh-aw-v1.0.1-model-aliases.json";
  const fixture = "server/internal/collectnative/testdata/fixture.json";
  const selected = selectAzureAssetFiles([...tracked, aliases, fixture]);
  assert.ok(selected.includes(aliases));
  assert.ok(!selected.includes(fixture));
  const source = readFileSync(path.join(root, "server/internal/collectnative/audit_model_identity.go"), "utf8");
  assert.match(source, /^\/\/go:embed gh-aw-v1\.0\.1-model-aliases\.json$/m);
});

test("Azure asset selection fails closed on credentials or policy inside allowlisted directories", () => {
  const tracked = git("ls-tree", "-r", "--name-only", "--full-tree", revision).split("\n").filter(Boolean);
  for (const leaked of [
    "server/internal/server/.env",
    "server/internal/server/.env.production",
    "dashboard/site/src/signing.pem",
    "server/internal/githubapp/app.key",
    "dashboard/site/src/cao.json",
    "server/internal/server/cao.azure.json",
    "internal/dashboardquery/node_modules/x/index.js",
  ]) {
    assert.throws(() => selectAzureAssetFiles([...tracked, leaked]), /forbidden files/, leaked);
  }
  assert.throws(() => buildCaoAzureAssets(root, path.join(workspace, "bad"), "HEAD"), /full lowercase source revision/);
});

test("Azure Dockerfile legacy ACR targets and their preceding stages read only bundled sources", () => {
  const dockerfile = readFileSync(path.join(root, "server/Dockerfile"), "utf8");
  const stages = new Map(dockerfile.split(/(?=^FROM )/m).map((stage) => [/ AS (\S+)/.exec(stage)?.[1], stage]));
  const reachable = new Set();
  const visit = (name) => {
    if (reachable.has(name) || !stages.has(name)) return;
    reachable.add(name);
    const stage = stages.get(name);
    visit(/^FROM (\S+)/.exec(stage)[1]);
    for (const match of stage.matchAll(/--from=(\S+)/g)) visit(match[1]);
  };
  for (const target of ["azure-functions-runtime", "azure-ingest-runtime", "azure-empty-ingest-runtime", "azure-db-provision"]) visit(target);
  const order = [...stages.keys()].filter(Boolean);
  for (const target of ["azure-functions-runtime", "azure-ingest-runtime", "azure-empty-ingest-runtime", "azure-db-provision"]) {
    assert.ok(order.includes(target), `${target} must exist`);
    for (const preceding of order.slice(0, order.indexOf(target) + 1)) reachable.add(preceding);
  }
  assert.ok(!reachable.has("control-plane-inventory"), "Azure targets must not depend on the catalog inventory");
  assert.ok(reachable.has("cao-policy") && reachable.has("cao-ingest-source"));
  for (const name of reachable) {
    if (name === "cao-policy" || name === "cao-ingest-source") continue;
    for (const match of stages.get(name).matchAll(/^COPY (?!--from)(?:--\S+ )*(.+)$/gm)) {
      const sources = match[1].trim().split(/\s+/).slice(0, -1);
      for (const source of sources) {
        if (source === ".") continue;
        const normalized = source.replace(/\/$/, "");
        const glob = normalized.includes("*")
          ? new RegExp(`^${normalized.replace(/[.+?^${}()|[\]\\]/g, "\\$&").replace(/\*/g, "[^/]*")}$`)
          : null;
        assert.ok(
          files.some((file) => (glob ? glob.test(file) : file === normalized || file.startsWith(`${normalized}/`))),
          `${name} copies ${source}, which the bundle does not contain`,
        );
      }
    }
  }
  assert.match(stages.get("cao-policy"), /^COPY \.github\/workflows\/cao\.json \.github\/workflows\/cao\.azure\.json \/$/m);
  assert.match(stages.get("dashboard-build"), /^COPY --from=cao-policy \/ \.github\/workflows\/$/m);
  assert.match(stages.get("azure-ingest-runtime"), /^COPY --from=cao-ingest-source --chown=cao:cao \/ \/data\/$/m);
});

test("development CLI and release bundles consume the same reviewed Azure asset allowlist", () => {
  const shared = readFileSync(path.join(root, "internal/cli/deploy_azure_assets.json"), "utf8");
  assert.deepEqual(AZURE_ASSET_ALLOWLIST, JSON.parse(shared));
  assert.ok(AZURE_ASSET_ALLOWLIST.includes("dashboard/site/manifest.webmanifest"));
  const native = readFileSync(path.join(root, "internal/cli/deploy_azure_images.go"), "utf8");
  assert.match(native, /\/\/go:embed deploy_azure_assets\.json/);
  assert.match(native, /for _, entry := range deployAzureAssetAllowlist/);
});
