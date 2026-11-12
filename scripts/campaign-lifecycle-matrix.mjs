import { execFileSync } from "node:child_process";
import { readFileSync } from "node:fs";
import { dirname, join, posix, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { parse } from "yaml";

const suites = [
  {
    name: "root",
    manifest: "aw.yml",
    testPattern: "root campaign",
    prefixes: [
      "activity/",
      "dashboard/",
      "install-gh-cao.sh",
      ".github/actions/setup-cao-runtime/",
      ".github/actions/setup-gh-aw/",
      "cao-evolution/.github/graders/",
      ".github/workflows/graders/cao-evolution-",
      ".github/workflows/graders/dependabot-",
      ".github/workflows/shared/",
      ".github/workflows/daily-repo-status.",
    ],
  },
  {
    name: "CAO Evolution",
    manifest: "cao-evolution/aw.yml",
    testPattern: "focused CAO Evolution campaign contract",
    prefixes: [
      "cao-evolution/.github/graders/",
      ".github/workflows/graders/cao-evolution-",
      ".github/workflows/cao-evolution",
      ".github/workflows/shared/",
    ],
  },
  {
    name: "EU CRA",
    manifest: "eu-cra-compliance/aw.yml",
    testPattern: "focused EU CRA campaign contract",
    prefixes: [
      "eu-cra-compliance/.github/graders/",
      ".github/workflows/eu-cra-compliance",
      ".github/workflows/graders/eu-cra-compliance",
      ".github/workflows/shared/",
    ],
  },
  {
    name: "UK AI Advisory",
    manifest: "uk-ai-advisory/aw.yml",
    testPattern: "focused UK AI Advisory campaign contract",
    prefixes: [
      ".github/workflows/shared/",
      ".github/workflows/uk-ai-advisory",
    ],
  },
  {
    name: "SelfCare",
    manifest: "self-care/aw.yml",
    testPattern: "focused SelfCare campaign contract",
    prefixes: [
      "self-care/.github/graders/",
      ".github/workflows/graders/self-care-",
      ".github/workflows/self-care",
      ".github/workflows/shared/",
    ],
  },
  {
    name: "Software Development Practices",
    manifest: "software-development-practices/aw.yml",
    testPattern: "focused Software Development Practices campaign contract",
    prefixes: [
      "software-development-practices/.github/graders/",
      ".github/workflows/graders/software-development-practices-",
      ".github/workflows/shared/",
      ".github/workflows/software-development-practices",
    ],
  },
  {
    name: "Dependabot",
    manifest: "dependabot/aw.yml",
    testPattern: "update replaces",
    prefixes: [
      ".github/workflows/graders/dependabot-",
      ".github/workflows/dependabot",
      ".github/workflows/shared/",
    ],
  },
  {
    name: "Optimization",
    manifest: "optimization/aw.yml",
    testPattern: "focused Optimization campaign contract",
    prefixes: [
      "optimization/.github/graders/",
      ".github/workflows/graders/optimization-",
      ".github/workflows/optimization",
      ".github/workflows/shared/",
    ],
  },
];

function sourcePath(manifest, source) {
  if (source.startsWith(".github/")) return source;
  return posix.normalize(posix.join(posix.dirname(manifest), source));
}

function resourceSourcePath(manifest, source) {
  return posix.normalize(posix.join(posix.dirname(manifest), source));
}

function campaignSources(root, suite) {
  const sources = [];
  const visited = new Set();
  const collect = (manifestPath) => {
    if (visited.has(manifestPath)) return;
    visited.add(manifestPath);
    sources.push(manifestPath);
    const manifest = parse(readFileSync(join(root, manifestPath), "utf8"));
    for (const entry of manifest.includes ?? []) {
      const source = sourcePath(manifestPath, typeof entry === "string" ? entry : entry.source);
      if (typeof entry === "string" && posix.basename(source) === "aw.yml") {
        collect(source);
      } else {
        sources.push(source);
      }
    }
    for (const entry of manifest.resources ?? []) {
      sources.push(resourceSourcePath(manifestPath, entry.source));
    }
  }
  collect(suite.manifest);
  return sources;
}

export function selectCampaignLifecycleSuites(changedFiles, root = process.cwd()) {
  const installableSuites = suites.filter((suite) => {
    const manifest = parse(readFileSync(join(root, suite.manifest), "utf8"));
    return manifest.private !== true;
  });
  if (changedFiles === null) return installableSuites.map(({ name, testPattern }) => ({ name, "test-pattern": testPattern }));

  const normalized = changedFiles.map((file) => file.replaceAll("\\", "/"));
  if (normalized.some((file) => [
    "scripts/campaign-lifecycle-matrix.mjs",
    "tests/integration/campaign-lifecycle.test.mjs",
  ].includes(file))) {
    return selectCampaignLifecycleSuites(null, root);
  }
  return installableSuites
    .filter((suite) => {
      const campaignDirectory = posix.dirname(suite.manifest);
      const prefixes = campaignDirectory === "."
        ? suite.prefixes
        : [`${campaignDirectory}/`, ...suite.prefixes];
      if (normalized.some((file) => file === suite.manifest || prefixes.some((prefix) => file.startsWith(prefix)))) {
        return true;
      }
      const sources = new Set(campaignSources(root, suite));
      return normalized.some((file) => sources.has(file));
    })
    .map(({ name, testPattern }) => ({ name, "test-pattern": testPattern }));
}

function changedFiles(base, head, root) {
  return execFileSync("git", ["diff", "--name-only", "--diff-filter=ACMR", base, head], {
    cwd: root,
    encoding: "utf8",
  }).trim().split("\n").filter(Boolean);
}

const invokedPath = process.argv[1] && pathToFileURL(resolve(process.argv[1])).href;
if (invokedPath === import.meta.url) {
  const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
  const files = process.argv[2] === "--all"
    ? null
    : changedFiles(process.argv[2], process.argv[3], root);
  process.stdout.write(JSON.stringify({ include: selectCampaignLifecycleSuites(files, root) }));
}
