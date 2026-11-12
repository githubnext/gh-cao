import { buildSync } from "esbuild";
import { spawnSync } from "node:child_process";
import { cpSync, mkdirSync, mkdtempSync, readdirSync, readFileSync, rmSync, statSync, writeFileSync } from "node:fs";
import { createHash } from "node:crypto";
import { tmpdir } from "node:os";
import path from "node:path";
import { pathToFileURL } from "node:url";
import { createRequire } from "node:module";

const repository = "githubnext/gh-cao";
// Keep aligned with nativeRuntimeResources in internal/cli/native_management_runtime.go.
export const runtimeResources = [
  "dashboard",
  ".github/workflows/shared/value-acquisition.mjs",
  ".github/workflows/shared/actions-log.mjs",
  ".github/actions/setup-gh-aw",
  ".github/cao/instructions.md",
  ".github/workflows/shared/control.md",
  ".github/workflows/shared/operation.md",
  ".github/workflows/shared/review-bundle.md",
];
const requiredRuntimeFiles = ["dashboard/site/package.json", "dashboard/report/configure-site.mjs"];

function run(command, arguments_, options = {}) {
  const result = spawnSync(command, arguments_, { encoding: "utf8", maxBuffer: 16 * 1024 * 1024, ...options });
  if (result.error || result.status !== 0) {
    throw new Error(`${command} ${arguments_[0]} failed: ${(result.stderr || result.error?.message || `exit ${result.status}`).trim()}`);
  }
  return result.stdout;
}

function requireResources(root, resources) {
  for (const resource of resources) {
    try {
      statSync(path.join(root, ...resource.split("/")));
    } catch (error) {
      if (error?.code === "ENOENT") throw new Error(`CAO runtime is missing required resource: ${resource}`);
      throw error;
    }
  }
}

function owned(file) {
  return runtimeResources.some((resource) => file === resource || file.startsWith(`${resource}/`));
}

function copyTrackedResource(sourceRoot, destinationRoot, resource) {
  const files = run("git", ["-C", sourceRoot, "ls-files", "-z", "--", resource]).split("\0").filter(Boolean);
  if (files.length === 0) throw new Error(`CAO source resource has no tracked files: ${resource}`);
  for (const file of files) {
    const destination = path.join(destinationRoot, ...file.split("/"));
    mkdirSync(path.dirname(destination), { recursive: true });
    cpSync(path.join(sourceRoot, ...file.split("/")), destination);
  }
}

function npmPack(cwd, destination) {
  const arguments_ = ["pack", "--ignore-scripts", "--json", "--pack-destination", destination, "--cache", path.join(destination, "cache")];
  const [command, prefix] = process.platform === "win32"
    ? [process.execPath, [process.env.npm_execpath ?? path.join(path.dirname(process.execPath), "node_modules", "npm", "bin", "npm-cli.js")]]
    : ["npm", []];
  const [packed] = JSON.parse(run(command, [...prefix, ...arguments_], { cwd }));
  if (!packed?.filename || path.basename(packed.filename) !== packed.filename) {
    throw new Error("npm pack did not produce a valid CAO runtime archive");
  }
  return packed;
}

function runtimeFiles(directory, prefix = "") {
  return readdirSync(path.join(directory, prefix), { withFileTypes: true }).flatMap((entry) => {
    const file = prefix ? `${prefix}/${entry.name}` : entry.name;
    if (entry.isDirectory()) return runtimeFiles(directory, file);
    if (!entry.isFile()) throw new Error(`CAO runtime contains a non-regular file: ${file}`);
    return [file];
  }).sort();
}

function inventoryFiles(root) {
  return runtimeFiles(root).filter((file) => file !== "package.json" && file !== "cao-runtime.json");
}

function sha256(filename) {
  return createHash("sha256").update(readFileSync(filename)).digest("hex");
}

function sealCaoRuntime(root, revision) {
  const metadata = {
    schemaVersion: 1, repository, revision,
    files: Object.fromEntries(inventoryFiles(root).map((file) => [file, sha256(path.join(root, file))])),
  };
  writeFileSync(path.join(root, "cao-runtime.json"), `${JSON.stringify(metadata, null, 2)}\n`);
  const manifestPath = path.join(root, "package.json");
  const manifest = JSON.parse(readFileSync(manifestPath, "utf8"));
  manifest.files = [...manifest.files, "cao-runtime.json"];
  writeFileSync(manifestPath, `${JSON.stringify(manifest, null, 2)}\n`);
}

export function validateCaoRuntimeArtifact(root, revision) {
  requireResources(root, ["cao-runtime.json", ...runtimeResources]);
  const metadata = JSON.parse(readFileSync(path.join(root, "cao-runtime.json"), "utf8"));
  if (metadata.schemaVersion !== 1 || metadata.repository !== repository || metadata.revision !== revision) {
    throw new Error(`CAO runtime artifact provenance does not match ${repository}@${revision}`);
  }
  if (!metadata.files || typeof metadata.files !== "object" || Array.isArray(metadata.files)) {
    throw new Error("CAO runtime artifact has no valid file inventory");
  }
  const files = inventoryFiles(root);
  if (JSON.stringify(files) !== JSON.stringify(Object.keys(metadata.files).sort())) {
    throw new Error("CAO runtime artifact file inventory does not match its contents");
  }
  for (const file of files) {
    if (!owned(file)) throw new Error(`CAO runtime artifact contains an unowned path: ${file}`);
    if (metadata.files[file] !== sha256(path.join(root, file))) throw new Error(`CAO runtime artifact checksum mismatch: ${file}`);
  }
  requireResources(root, requiredRuntimeFiles);
}

function vendorYaml(sourceRoot, root) {
  const vendor = path.join(root, "dashboard/site/src/vendor");
  mkdirSync(vendor, { recursive: true });
  buildSync({
    absWorkingDir: sourceRoot,
    stdin: { contents: 'export { parseAllDocuments } from "yaml";', resolveDir: sourceRoot },
    outfile: path.join(vendor, "yaml.js"),
    bundle: true, format: "esm", platform: "neutral", target: "es2022", legalComments: "eof",
  });
  const require = createRequire(path.join(sourceRoot, "package.json"));
  cpSync(path.join(path.dirname(require.resolve("yaml/package.json")), "LICENSE"), path.join(vendor, "yaml.LICENSE"));
  const consumer = path.join(root, "dashboard/site/src/validator-dashboard.js");
  const source = readFileSync(consumer, "utf8");
  const compiled = source.replace("from 'yaml';", "from './vendor/yaml.js';");
  if (compiled === source) throw new Error("Dashboard YAML dependency changed; runtime bundling must be updated");
  writeFileSync(consumer, compiled);
}

export function buildCaoRuntime(sourceRoot, destination, revision) {
  if (!/^[0-9a-f]{40}$/.test(revision)) throw new Error("A full lowercase source revision SHA is required");
  const manifestPath = path.join(sourceRoot, "runtime", "package.json");
  const manifest = JSON.parse(readFileSync(manifestPath, "utf8"));
  if (!Array.isArray(manifest.files) || manifest.files.some((file) => (
    typeof file !== "string" || file.split("/").some((part) => part === "." || part === "..") || !owned(file)
  ))) {
    throw new Error("CAO runtime package must declare files within its owned runtime destinations");
  }
  requireResources(sourceRoot, [...runtimeResources, ...manifest.files]);
  const temporary = mkdtempSync(path.join(tmpdir(), "cao-runtime-build-"));
  try {
    const staging = path.join(temporary, "source");
    const unpacked = path.join(temporary, "unpacked");
    mkdirSync(staging, { recursive: true });
    mkdirSync(unpacked, { recursive: true });
    for (const resource of runtimeResources) copyTrackedResource(sourceRoot, staging, resource);
    cpSync(manifestPath, path.join(staging, "package.json"));
    const selected = npmPack(staging, temporary);
    run("tar", ["-xzf", path.join(temporary, selected.filename), "-C", unpacked]);
    const root = path.join(unpacked, "package");
    requireResources(root, [...runtimeResources, ...manifest.files]);
    vendorYaml(sourceRoot, root);
    sealCaoRuntime(root, revision);
    const packed = npmPack(root, temporary);
    validateCaoRuntimeArtifact(root, revision);
    mkdirSync(destination, { recursive: true });
    const name = `cao-runtime-${revision}.tgz`;
    const archive = path.join(destination, name);
    cpSync(path.join(temporary, packed.filename), archive);
    const digest = sha256(archive);
    writeFileSync(`${archive}.sha256`, `${digest}  ${name}\n`);
    return { archive, digest, files: packed.files.map(({ path: file }) => file) };
  } finally {
    rmSync(temporary, { recursive: true, force: true });
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  const [destination, revision] = process.argv.slice(2);
  if (!destination || !revision) throw new Error("Usage: build-cao-runtime.mjs DESTINATION REVISION");
  const result = buildCaoRuntime(path.resolve(import.meta.dirname, ".."), path.resolve(destination), revision);
  console.log(`Built ${path.basename(result.archive)} (${result.files.length} files, sha256:${result.digest})`);
}
