import { execFileSync } from "node:child_process";
import { existsSync, readFileSync, statSync } from "node:fs";
import path from "node:path";

const maximumSize = 256 * 1024;
// gh-aw lock files are compiler output; GitHub does not start runs for workflow files above 500 KB.
const maximumGeneratedWorkflowSize = 500 * 1024;
const maximumLines = 3000;
const imageExtensions = new Set([".avif", ".gif", ".ico", ".jpeg", ".jpg", ".png", ".svg", ".webp"]);
const sourceExtensions = new Set([".js", ".jsx", ".mjs", ".ts", ".tsx"]);
// Aggregated documents are generated from independently size-checked fragments.
const excludedFiles = new Set([
  "dashboard/site/dashboard.json",
  "dashboard/site/src/agent/catalog.generated.json",
  "dashboard/site/src/agent/queries.generated.json",
]);
const lineCountExclusions = new Set([
  "dashboard/site/test/unit/data-view.test.js",
  "dashboard/site/test/unit/presenter.test.js",
  "dashboard/site/test/unit/validator.test.js",
]);
const root = path.resolve(import.meta.dirname, "..");
let trackedFiles;

try {
  trackedFiles = execFileSync("git", ["ls-files", "--cached", "--others", "--exclude-standard", "-z"], {
    cwd: root,
    encoding: "utf8",
  })
    .split("\0")
    .filter(Boolean)
    .filter((file) => existsSync(path.join(root, file)));
} catch {
  console.error("Unable to list tracked files. Ensure Git is installed and this command runs inside the repository.");
  process.exit(1);
}

const oversizedFiles = trackedFiles.filter((file) => {
  const isLockfile = path.basename(file) === "package-lock.json";
  const isAssetImage = file.split(/[\\/]/).includes("assets") && imageExtensions.has(path.extname(file).toLowerCase());
  return !isLockfile && !isAssetImage && !excludedFiles.has(file);
}).flatMap((file) => {
  const filePath = path.join(root, file);
  const size = statSync(filePath).size;
  const isGeneratedWorkflow = file.startsWith(".github/workflows/") && file.endsWith(".lock.yml");
  const limit = isGeneratedWorkflow ? maximumGeneratedWorkflowSize : maximumSize;
  return size > limit ? [{ file, size, limit }] : [];
});

if (oversizedFiles.length > 0) {
  console.error(`Tracked files must not exceed 256 KiB (${maximumSize} bytes); generated .github/workflows/*.lock.yml files must not exceed 500 KiB (${maximumGeneratedWorkflowSize} bytes):`);
  for (const { file, size, limit } of oversizedFiles) {
    console.error(`- ${file} (${size} bytes, limit ${limit} bytes)`);
  }
  process.exitCode = 1;
}

const overlongFiles = trackedFiles.flatMap((file) => {
  if (lineCountExclusions.has(file) || !sourceExtensions.has(path.extname(file).toLowerCase())) return [];
  const lineCount = readFileSync(path.join(root, file), "utf8").split(/\r?\n/).length;
  return lineCount > maximumLines ? [{ file, lineCount }] : [];
});

if (overlongFiles.length > 0) {
  console.error(`Source files must not exceed ${maximumLines} lines:`);
  for (const { file, lineCount } of overlongFiles) {
    console.error(`- ${file} (${lineCount} lines)`);
  }
  process.exitCode = 1;
}
