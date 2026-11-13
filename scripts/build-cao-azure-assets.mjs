import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdirSync, writeFileSync } from "node:fs";
import path from "node:path";
import { pathToFileURL } from "node:url";
import { gzipSync } from "node:zlib";
import azureAssetAllowlist from "./cao-azure-assets.json" with { type: "json" };

export const AZURE_ASSET_MANIFEST = "cao-azure.json";

// Exactly the inputs that the azure-container-apps-runtime, azure-ingest-runtime, and
// azure-empty-ingest-runtime and azure-db-provision targets read from the build
// context (esbuild also reads dashboard/site/tsconfig.json), plus the compiled
// ARM template. Entries ending in "/" select tracked files below that directory.
// Adopter policy and ingestion snapshots are supplied at build time through the
// cao-policy and cao-ingest-source build contexts.
export const AZURE_ASSET_ALLOWLIST = Object.freeze(azureAssetAllowlist);

// Tests, fixtures, and documentation below allowlisted directories are not
// build inputs and are omitted.
const omitted = [
  /(^|\/)testdata\//,
  /(^|\/)__tests__\//,
  /^server\/internal\/testutil\//,
  /_test\.go$/,
  /\.(test|spec)\.[cm]?[jt]sx?$/,
  /\.md$/i,
];

// Credentials and adopter policy must never reach the bundle. Any match inside
// the allowlist fails the build instead of being silently dropped.
export const AZURE_ASSET_FORBIDDEN = Object.freeze([
  /(^|\/)\.git(\/|$)/,
  /(^|\/)node_modules\//,
  /(^|\/)\.env(\.|$)/,
  /\.(pem|key|p12|pfx|crt|cer|der)$/i,
  /(^|\/)id_(rsa|dsa|ecdsa|ed25519)/,
  /(^|\/)\.npmrc$/,
  /(^|\/)cao(\.[a-z0-9-]+)?\.json$/,
]);

const fixedMtime = 499162500;

function git(sourceRoot, args, options = {}) {
  const result = spawnSync("git", ["-C", sourceRoot, ...args], { maxBuffer: 256 * 1024 * 1024, ...options });
  if (result.error || result.status !== 0) {
    throw new Error(`git ${args[0]} failed: ${(result.stderr?.toString() || result.error?.message || `exit ${result.status}`).trim()}`);
  }
  return result.stdout;
}

function allowed(file) {
  return AZURE_ASSET_ALLOWLIST.some((entry) => entry.endsWith("/") ? file.startsWith(entry) : file === entry);
}

export function selectAzureAssetFiles(trackedFiles) {
  const selected = trackedFiles.filter((file) => allowed(file) && !omitted.some((pattern) => pattern.test(file)));
  const forbidden = selected.filter((file) => AZURE_ASSET_FORBIDDEN.some((pattern) => pattern.test(file)));
  if (forbidden.length) throw new Error(`Azure asset bundle would include forbidden files: ${forbidden.join(", ")}`);
  for (const entry of AZURE_ASSET_ALLOWLIST) {
    if (!selected.some((file) => entry.endsWith("/") ? file.startsWith(entry) : file === entry)) {
      throw new Error(`Azure asset allowlist entry has no tracked source: ${entry}`);
    }
  }
  return selected.toSorted();
}

function readTree(sourceRoot, revision) {
  const listing = git(sourceRoot, ["ls-tree", "-r", "-z", "--full-tree", revision]).toString("utf8");
  const entries = new Map();
  for (const record of listing.split("\0").filter(Boolean)) {
    const [meta, file] = record.split("\t");
    const [mode, type, object] = meta.split(" ");
    if (type !== "blob") continue;
    entries.set(file, { mode, object });
  }
  return entries;
}

function readBlobs(sourceRoot, objects) {
  const output = git(sourceRoot, ["cat-file", "--batch"], { input: `${objects.join("\n")}\n` });
  const blobs = [];
  let offset = 0;
  for (const object of objects) {
    const newline = output.indexOf(0x0a, offset);
    const [name, type, size] = output.subarray(offset, newline).toString("utf8").split(" ");
    if (name !== object || type !== "blob") throw new Error(`Unexpected git object for ${object}`);
    const start = newline + 1;
    blobs.push(output.subarray(start, start + Number(size)));
    offset = start + Number(size) + 1;
  }
  return blobs;
}

function octal(value, length) {
  return `${value.toString(8).padStart(length - 1, "0")}\0`;
}

function tarHeader(file, size, mode) {
  const header = Buffer.alloc(512, 0);
  let name = file;
  let prefix = "";
  if (Buffer.byteLength(name) > 100) {
    const split = file.lastIndexOf("/", file.length - 1);
    for (let index = split; index > 0; index = file.lastIndexOf("/", index - 1)) {
      if (Buffer.byteLength(file.slice(index + 1)) <= 100 && Buffer.byteLength(file.slice(0, index)) <= 155) {
        prefix = file.slice(0, index);
        name = file.slice(index + 1);
        break;
      }
    }
    if (!prefix) throw new Error(`Azure asset path is too long for ustar: ${file}`);
  }
  header.write(name, 0, 100, "utf8");
  header.write(octal(mode, 8), 100, 8, "ascii");
  header.write(octal(0, 8), 108, 8, "ascii");
  header.write(octal(0, 8), 116, 8, "ascii");
  header.write(octal(size, 12), 124, 12, "ascii");
  header.write(octal(fixedMtime, 12), 136, 12, "ascii");
  header.fill(0x20, 148, 156);
  header.write("0", 156, 1, "ascii");
  header.write("ustar\0", 257, 6, "ascii");
  header.write("00", 263, 2, "ascii");
  header.write(prefix, 345, 155, "utf8");
  let checksum = 0;
  for (const byte of header) checksum += byte;
  header.write(`${checksum.toString(8).padStart(6, "0")}\0 `, 148, 8, "ascii");
  return header;
}

export function packDeterministicTar(entries) {
  const chunks = [];
  for (const { path: file, content, mode } of entries.toSorted((a, b) => (a.path < b.path ? -1 : a.path > b.path ? 1 : 0))) {
    chunks.push(tarHeader(file, content.length, mode), content);
    const padding = (512 - (content.length % 512)) % 512;
    if (padding) chunks.push(Buffer.alloc(padding, 0));
  }
  chunks.push(Buffer.alloc(1024, 0));
  const archive = gzipSync(Buffer.concat(chunks), { level: 9 });
  // The gzip header records the producing OS; pin it to Unix so archives are
  // byte-identical across build hosts.
  archive[9] = 3;
  return archive;
}

export function buildCaoAzureAssets(sourceRoot, destination, revision) {
  if (!/^[0-9a-f]{40}$/.test(revision)) throw new Error("A full lowercase source revision SHA is required");
  const resolved = git(sourceRoot, ["rev-parse", "--verify", `${revision}^{commit}`]).toString("utf8").trim();
  if (resolved !== revision) throw new Error(`Source revision ${revision} is not available`);
  const tree = readTree(sourceRoot, revision);
  const files = selectAzureAssetFiles([...tree.keys()]);
  const symlinks = files.filter((file) => tree.get(file).mode === "120000");
  if (symlinks.length) throw new Error(`Azure asset bundle cannot include symbolic links: ${symlinks.join(", ")}`);
  const blobs = readBlobs(sourceRoot, files.map((file) => tree.get(file).object));
  const entries = files.map((file, index) => ({
    path: file,
    content: blobs[index],
    mode: tree.get(file).mode === "100755" ? 0o755 : 0o644,
  }));
  const manifest = {
    schemaVersion: 1,
    revision,
    dockerfile: "server/Dockerfile",
    template: "internal/topology/assets/container-apps/main.generated.json",
    targets: ["azure-container-apps-runtime", "azure-ingest-runtime", "azure-empty-ingest-runtime", "azure-db-provision"],
    buildContexts: { "cao-policy": "required", "cao-ingest-source": "azure-ingest-runtime" },
    files: Object.fromEntries(entries.map(({ path: file, content }) => [
      file, createHash("sha256").update(content).digest("hex"),
    ])),
  };
  entries.push({ path: AZURE_ASSET_MANIFEST, content: Buffer.from(`${JSON.stringify(manifest, null, 2)}\n`), mode: 0o644 });
  const archive = packDeterministicTar(entries);
  mkdirSync(destination, { recursive: true });
  const name = `cao-azure-${revision}.tgz`;
  const archivePath = path.join(destination, name);
  writeFileSync(archivePath, archive);
  const digest = createHash("sha256").update(archive).digest("hex");
  writeFileSync(`${archivePath}.sha256`, `${digest}  ${name}\n`);
  return { archive: archivePath, digest, files: entries.map(({ path: file }) => file).toSorted() };
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const sourceRoot = path.resolve(import.meta.dirname, "..");
  const [destination = ".tmp/cao-azure-assets", revision = git(sourceRoot, ["rev-parse", "HEAD"]).toString("utf8").trim()] = process.argv.slice(2);
  const result = buildCaoAzureAssets(sourceRoot, path.resolve(destination), revision);
  console.log(`Built ${path.basename(result.archive)} (${result.files.length} files, sha256:${result.digest})`);
}
