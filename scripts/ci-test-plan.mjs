import { execFileSync } from "node:child_process";
import { appendFileSync } from "node:fs";
import { resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { unitTestAreas } from "./run-unit-area.mjs";

const areas = Object.keys(unitTestAreas);

export const goTestAreas = ["http", "hosted-cli", "storage", "core"];

export function goTestArea(file) {
  if (/^server\/internal\/server\/[^/]+$/.test(file)) return "http";
  if (/^server\/internal\/hostedcli\/[^/]+$/.test(file)) return "hosted-cli";
  if (/^server\/internal\/postgresx\/[^/]+$/.test(file)) return "storage";
  return "core";
}

export function goTestPlan(files, full = false) {
  let lint = full || files.length === 0;
  let query = lint;
  let integration = lint;
  const selected = new Set(lint ? goTestAreas : []);
  for (const file of files) {
    if (/^server\/.*_test\.go$/.test(file)) {
      lint = true;
      selected.add(goTestArea(file));
      integration ||= /^server\/(internal\/(simulator|admission|webhookgateway)|cmd\/cao-webhook-gateway)\//.test(file);
    } else if (/^(server\/|internal\/cli\/|go\.(mod|sum)$)/.test(file)) {
      goTestAreas.forEach((area) => selected.add(area));
      lint = true;
      integration = true;
    } else if (file.startsWith("dashboard/site/")) {
      // Native tests read dashboard documents, query fixtures, and JS parity oracles.
      goTestAreas.forEach((area) => selected.add(area));
      query = true;
      integration = true;
    } else {
      // Shared query code, CI configuration, and unclassified inputs widen coverage.
      goTestAreas.forEach((area) => selected.add(area));
      lint = true;
      query = true;
      integration = true;
    }
  }
  return {
    matrix: {
      include: goTestAreas.filter((area) => selected.has(area)).flatMap((area) =>
        Array.from({ length: area === "storage" ? 4 : 1 }, (_, shard) => ({
          area, shard, shards: area === "storage" ? 4 : 1,
        }))),
    },
    tests: selected.size > 0,
    lint,
    query,
    integration,
  };
}

export function testPlan(files, full = false) {
  const selected = new Set(full || files.length === 0 ? areas : []);
  for (const file of files) {
    if (/^(package(-lock)?\.json|scripts\/|eslint|\.github\/(workflows\/|actions\/)|tests\/helpers\/)/.test(file)) {
      areas.forEach((area) => selected.add(area));
    } else if (file.startsWith("tests/unit/")) {
      const name = file.slice("tests/unit/".length);
      const matches = areas.filter((area) => unitTestAreas[area].some((pattern) => pattern.test(name)));
      (matches.length ? matches : areas).forEach((area) => selected.add(area));
    } else if (/^(docs\/|public\/|astro\.config|README|CONTRIBUTING)/.test(file)) {
      selected.add("documentation");
    } else if (/^(dashboard\/|self-care\/dashboard)/.test(file)) {
      selected.add("dashboard");
      selected.add("tooling");
    } else if (/^(server\/|cmd\/|internal\/|go\.(mod|sum)$)/.test(file)) {
      // Native behavior is checked by Go CI; root contracts cover its CLI and adapters.
      selected.add("tooling");
      selected.add("dashboard");
    } else {
      areas.forEach((area) => selected.add(area));
    }
  }
  const unitAreas = areas.filter((area) => selected.has(area));
  return {
    matrix: { area: unitAreas },
    integration: full || unitAreas.some((area) => area !== "documentation"),
    documentation: selected.has("documentation"),
  };
}

export function changedFiles(base, head) {
  if (!base || /^0+$/.test(base)) return null;
  // A missing comparison (for example after a force push) must widen coverage.
  try {
    execFileSync("git", ["cat-file", "-e", `${base}^{commit}`], { stdio: "pipe" });
  } catch {
    console.warn("::warning::CI comparison base unavailable; selecting all contract areas.");
    return null;
  }
  return execFileSync("git", ["diff", "--name-only", "-z", "--no-renames", base, head], {
    encoding: "utf8",
  }).split("\0").filter(Boolean);
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const full = ["schedule", "workflow_dispatch"].includes(process.env.GITHUB_EVENT_NAME);
  const files = full ? null : changedFiles(process.env.BASE_SHA, process.env.HEAD_SHA);
  const planner = process.argv.includes("--go") ? goTestPlan : testPlan;
  const plan = planner(files ?? [], full || files === null);
  const outputs = Object.entries(plan).map(([key, value]) =>
    `${key}=${typeof value === "object" ? JSON.stringify(value) : value}`).join("\n") + "\n";
  if (process.env.GITHUB_OUTPUT) appendFileSync(process.env.GITHUB_OUTPUT, outputs);
  console.log(outputs);
}
