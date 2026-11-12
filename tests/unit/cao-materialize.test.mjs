import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import {
  existsSync,
  lstatSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  readdirSync,
  rmSync,
  statSync,
  symlinkSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import test from "node:test";
import { gzipSync } from "node:zlib";
import { parse } from "yaml";
import {
  extractCaoArchive,
  materializeCaoFromSource,
  packCaoRuntime,
  planCaoMaterialization,
  verifyCaoRuntime,
} from "../../.github/workflows/shared/materialize-cao.mjs";

const sourceRoot = path.resolve(import.meta.dirname, "..", "..");
const runtimeManifest = JSON.parse(readFileSync(path.join(sourceRoot, "runtime", "package.json"), "utf8"));
const retiredLauncher = `#!/bin/sh
set -eu

root="$(CDPATH='' cd -P "$(dirname "$0")" && pwd)"

cli="$root/activity/cao.mjs"
if [ ! -f "$cli" ]; then
  echo "cao CLI is unavailable; install the Central Agentic Ops root campaign." >&2
  exit 1
fi

exec node "$cli" --pipeline "$@"
`;

for (const [name, contents, removed] of [
  ["pipeline launcher", retiredLauncher, true],
  ["older launcher", retiredLauncher.replace(" --pipeline ", " "), true],
  ["consumer script", "#!/bin/sh\necho consumer-owned\n", false],
  ["directory", null, false],
  ["symlink", null, false],
]) {
  test(`root materialization ${removed ? "retires" : "preserves"} ${name}`, {
    skip: name === "symlink" && process.platform === "win32",
  }, (t) => {
    const destination = mkdtempSync(path.join(tmpdir(), "cao-retired-launcher-"));
    t.after(() => rmSync(destination, { recursive: true, force: true }));
    const filename = path.join(destination, "cao.sh");
    if (name === "directory") mkdirSync(filename);
    else if (name === "symlink") {
      writeFileSync(path.join(destination, "consumer-script"), retiredLauncher);
      symlinkSync("consumer-script", filename);
    } else writeFileSync(filename, contents);
    materializeCaoFromSource("root", sourceRoot, destination);
    assert.equal(existsSync(filename), !removed);
    if (name === "consumer script") assert.equal(readFileSync(filename, "utf8"), contents);
    if (name === "directory") assert.ok(lstatSync(filename).isDirectory());
    if (name === "symlink") {
      assert.ok(lstatSync(filename).isSymbolicLink());
      assert.equal(readFileSync(filename, "utf8"), retiredLauncher);
    }
  });
}

test("focused materialization and failed root preflight preserve a retired launcher", (t) => {
  const destination = mkdtempSync(path.join(tmpdir(), "cao-retired-launcher-scope-"));
  t.after(() => rmSync(destination, { recursive: true, force: true }));
  const filename = path.join(destination, "cao.sh");
  writeFileSync(filename, retiredLauncher);
  materializeCaoFromSource("dependabot", sourceRoot, destination);
  assert.equal(readFileSync(filename, "utf8"), retiredLauncher);
  assert.throws(() => materializeCaoFromSource("root", destination, destination), /missing|incomplete/i);
  assert.equal(readFileSync(filename, "utf8"), retiredLauncher);
});

function filesBelow(root, prefix = "") {
  return readdirSync(path.join(root, prefix)).flatMap((name) => {
    const relative = path.join(prefix, name);
    return statSync(path.join(root, relative)).isDirectory()
      ? filesBelow(root, relative)
      : [relative.split(path.sep).join("/")];
  });
}

function tarArchive(entries) {
  const blocks = [];
  for (const { name, content = "", directory = false, mode = 0o644 } of entries) {
    const data = Buffer.from(content);
    const header = Buffer.alloc(512);
    header.write(name);
    header.write(mode.toString(8).padStart(7, "0"), 100);
    header.write(data.length.toString(8).padStart(11, "0"), 124);
    header[156] = directory ? 53 : 48;
    blocks.push(header, data, Buffer.alloc((512 - data.length % 512) % 512));
  }
  return gzipSync(Buffer.concat([...blocks, Buffer.alloc(1024)]));
}

test("Windows CAO extraction does not invoke tar and rejects unsafe archive paths", () => {
  const destination = mkdtempSync(path.join(tmpdir(), "cao-materialize-windows-"));
  const archive = path.join(destination, "cao.tar.gz");
  try {
    writeFileSync(archive, tarArchive([
      { name: "cao-root/", directory: true },
      { name: "cao-root/file.txt", content: "CAO" },
    ]));
    extractCaoArchive(archive, destination, true);
    assert.equal(readFileSync(path.join(destination, "cao-root", "file.txt"), "utf8"), "CAO");

    writeFileSync(archive, tarArchive([{ name: "../escape.txt", content: "unsafe" }]));
    assert.throws(() => extractCaoArchive(archive, destination, true), /unsafe entry path/);
  } finally {
    rmSync(destination, { force: true, recursive: true });
  }
});

test("native extraction preserves executable bits without restoring special or world-writable permissions", () => {
  const destination = mkdtempSync(path.join(tmpdir(), "cao-materialize-modes-"));
  const archive = path.join(destination, "cao.tar.gz");
  try {
    writeFileSync(archive, tarArchive([
      { name: "package/tool.sh", content: "#!/bin/sh\n", mode: 0o6757 },
      { name: "package/data.json", content: "{}", mode: 0o666 },
    ]));
    extractCaoArchive(archive, destination, true);
    if (process.platform !== "win32") {
      assert.equal(statSync(path.join(destination, "package/tool.sh")).mode & 0o7777, 0o755);
      assert.equal(statSync(path.join(destination, "package/data.json")).mode & 0o7777, 0o644);
    }
  } finally {
    rmSync(destination, { force: true, recursive: true });
  }
});

test("the runtime package uses npm files selection without shipping development or plugin resources", () => {
  const destination = mkdtempSync(path.join(tmpdir(), "cao-runtime-package-"));
  try {
    const packed = packCaoRuntime(sourceRoot, destination, { tracked: true });
    assert.ok(packed.files.includes("dashboard/report/configure-site.mjs"));
    assert.ok(packed.files.includes("dashboard/site/scripts/build.mjs"));
    assert.ok(packed.files.includes("dashboard/site/src/data-worker.js"));
    assert.ok(packed.files.includes("dashboard/site/package-lock.json"));
    assert.ok(!packed.files.includes("activity/quickstart.mjs"));
    assert.ok(!packed.files.includes("activity/commands/quickstart.mjs"));
    assert.deepEqual(JSON.parse(readFileSync(path.join(packed.sourceRoot, "package.json"), "utf8")), runtimeManifest);
    for (const file of packed.files) {
      assert.doesNotMatch(file, /(?:^|\/)(?:test|tests|fixtures|screenshots|skills|com\.github\.copilot)(?:\/|$)/);
      assert.doesNotMatch(file, /(?:^|\/)(?:PLAN\.md|eslint\.config\.mjs|vitest\.config\.mjs|playwright\.config\.mjs|plugin\.json)$/);
    }
  } finally {
    rmSync(destination, { force: true, recursive: true });
  }
});


test("CAO materialization validates the complete source bundle before replacing runtime files", () => {
  const source = mkdtempSync(path.join(tmpdir(), "cao-materialize-incomplete-"));
  const destination = mkdtempSync(path.join(tmpdir(), "cao-materialize-existing-"));
  const existingRuntime = path.join(destination, "activity", "existing-runtime.mjs");
  try {
    mkdirSync(path.dirname(existingRuntime), { recursive: true });
    writeFileSync(existingRuntime, "existing");

    assert.throws(
      () => materializeCaoFromSource("root", source, destination),
      /missing required resource: dashboard/,
    );
    assert.equal(readFileSync(existingRuntime, "utf8"), "existing");
  } finally {
    rmSync(source, { force: true, recursive: true });
    rmSync(destination, { force: true, recursive: true });
  }
});

test("root materialization leaves separately installed plugin resources untouched", () => {
  const destination = mkdtempSync(path.join(tmpdir(), "cao-materialize-separate-plugin-"));
  const pluginFiles = [
    "plugin.json",
    "skills/setup-cao/SKILL.md",
    "com.github.copilot/extensions/cao-dashboard/extension.mjs",
    "specs/dashboard-data.md",
    ".github/skills/generate-dashboard-ir/SKILL.md",
  ];
  try {
    for (const file of pluginFiles) {
      mkdirSync(path.dirname(path.join(destination, file)), { recursive: true });
      writeFileSync(path.join(destination, file), "separately installed");
    }
    materializeCaoFromSource("root", sourceRoot, destination);
    for (const file of pluginFiles) {
      assert.equal(readFileSync(path.join(destination, file), "utf8"), "separately installed");
    }
  } finally {
    rmSync(destination, { force: true, recursive: true });
  }
});

for (const missingResource of [
  ".github/workflows/shared/value-acquisition.mjs",
  "dashboard/site/src",
  "dashboard/site/scripts/build.mjs",
  "dashboard/site/package-lock.json",
]) {
  test(`CAO materialization rejects a bundle missing ${missingResource} before replacement`, () => {
    const source = mkdtempSync(path.join(tmpdir(), "cao-materialize-incomplete-plugin-"));
    const destination = mkdtempSync(path.join(tmpdir(), "cao-materialize-existing-plugin-"));
    try {
      const [{ resources }] = planCaoMaterialization([{
        name: "githubnext/gh-cao",
        record: { resolvedCommit: "1".repeat(40) },
      }], "root");
      for (const resource of [...resources, ...runtimeManifest.files]) {
        if (resource === missingResource) continue;
        const target = path.join(source, resource);
        if (statSync(path.join(sourceRoot, resource)).isDirectory()) {
          mkdirSync(target, { recursive: true });
        } else {
          mkdirSync(path.dirname(target), { recursive: true });
          writeFileSync(target, "source");
        }
      }
      mkdirSync(path.join(source, "runtime"));
      writeFileSync(path.join(source, "runtime", "package.json"), JSON.stringify(runtimeManifest));
      const existingRuntime = path.join(destination, "activity", "existing-runtime.mjs");
      const existingExtension = path.join(destination, "com.github.copilot", "extensions", "cao-dashboard", "extension.mjs");
      for (const file of [existingRuntime, existingExtension]) {
        mkdirSync(path.dirname(file), { recursive: true });
        writeFileSync(file, "existing");
      }
      assert.throws(
        () => materializeCaoFromSource("root", source, destination),
        { message: `CAO source revision is missing required resource: ${missingResource}` },
      );
      for (const file of [existingRuntime, existingExtension]) {
        assert.equal(readFileSync(file, "utf8"), "existing");
      }
    } finally {
      rmSync(source, { force: true, recursive: true });
      rmSync(destination, { force: true, recursive: true });
    }
  });
}

test("CAO materialization rejects paths outside a campaign slug", () => {
  assert.throws(
    () => materializeCaoFromSource("../outside", sourceRoot, sourceRoot),
    /Invalid CAO campaign name/,
  );
});

test("root materialization preserves exact focused package revisions", () => {
  const rootRevision = "1".repeat(40);
  const dashboardRevision = "2".repeat(40);
  const plans = planCaoMaterialization([
    {
      name: "githubnext/gh-cao/control-plane",
      record: { resolvedCommit: rootRevision },
    },
    {
      name: "githubnext/gh-cao/dashboard",
      record: { resolvedCommit: dashboardRevision },
    },
  ], "root");

  assert.deepEqual(
    plans.map(({ campaign, revision, resources }) => ({ campaign, revision, resources })),
    [
      {
        campaign: "root",
        revision: rootRevision,
        resources: [
          ".github/workflows/shared/value-acquisition.mjs",
          ".github/workflows/shared/actions-log.mjs",
          ".github/actions/setup-cao-runtime",
          ".github/actions/setup-gh-aw",
          ".github/cao/instructions.md",
          ".github/workflows/shared/control.md",
          ".github/workflows/shared/operation.md",
          ".github/workflows/shared/review-bundle.md",
        ],
      },
      {
        campaign: "dashboard",
        revision: dashboardRevision,
        resources: ["dashboard"],
      },
    ],
  );
});

test("root installation provides every local action used by its installed workflows", () => {
  const installed = new Set();
  const workflows = [];
  const visit = (manifestPath) => {
    const manifest = parse(readFileSync(path.join(sourceRoot, manifestPath), "utf8"));
    const manifestDirectory = path.posix.dirname(manifestPath);
    for (const { destination } of manifest.resources ?? []) installed.add(destination);
    for (const include of manifest.includes ?? []) {
      const source = typeof include === "string" ? include : include.source;
      const resolved = source.startsWith(".github/")
        ? source
        : path.posix.normalize(path.posix.join(manifestDirectory, source));
      if (path.posix.basename(resolved) === "aw.yml") visit(resolved);
      else workflows.push(resolved);
    }
  };
  visit("aw.yml");
  const [{ resources }] = planCaoMaterialization([{
    name: "githubnext/gh-cao/control-plane",
    record: { resolvedCommit: "1".repeat(40) },
  }], "root");
  const provided = (file) =>
    installed.has(file) || resources.some((resource) => file === resource || file.startsWith(`${resource}/`));

  assert.ok(workflows.length > 0);
  for (const workflow of workflows) {
    const source = readFileSync(path.join(sourceRoot, workflow), "utf8");
    for (const [, action] of source.matchAll(/^\s*(?:-\s+)?uses:\s*\.\/([^\s#]+)/gm)) {
      const directory = action.replace(/\/+$/, "");
      assert.ok(
        provided(`${directory}/action.yml`) || provided(`${directory}/action.yaml`),
        `${workflow} uses ./${directory}, which root installation does not provide`,
      );
    }
  }
});

test("CAO materialization requires immutable resolved commit provenance", () => {
  assert.throws(
    () => planCaoMaterialization([{
      name: "githubnext/gh-cao",
      record: { source: "githubnext/gh-cao@v1.2.3" },
    }], "root"),
    /must contain a full resolvedCommit SHA/,
  );
});
