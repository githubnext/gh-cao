import { readdirSync } from "node:fs";
import { dirname, join, resolve } from "node:path";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";

const root = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const unitDirectory = join(root, "tests", "unit");

export const unitTestAreas = {
  activity: [/^activity-/, /^workflow-values-recurring\./, /^actions-log\./, /^github-telemetry\./],
  dashboard: [/^dashboard-/, /^cli-action-/, /^server-azure-/],
  workflows: [
    /^workflow-contract-/, /^control-/, /^campaign-/, /^contoso-/,
    /^github-apps\./, /^gh-aw-version-/, /^release-workflow\./, /^build-release\./,
    /^add-operational-value-skill\./, /^token-intervention-lifecycle\./,
  ],
  tooling: [
    /^cao-/, /^ops-publish/, /^optimization-/, /^dependabot-/, /^azure-local-/,
    /^eslint-rules-/, /^actions-context\./, /^actions-runtime\./, /^agent-plugin\./,
    /^install-script\./, /^installer-canary\./, /^repository-/, /^no-hardcoded-github-actions-url\./,
    /^token-optimization-data-contract\./, /^unit-area-selection\./,
    /^backfill-stress-report\./, /^source-map-compat\./,
    /^maintainer-simulation\./,
    /^(?:native-value-import|value-session-archive)\./,
  ],
  documentation: [
    /^docs-/, /^landing-page-/, /^catalog-page\./,
    /^agent-docs-validation\./, /^svg-visual-language\./,
  ],
};

export function partitionUnitTests(files) {
  const partition = Object.fromEntries(Object.keys(unitTestAreas).map((area) => [area, []]));
  for (const file of files) {
    const matches = Object.entries(unitTestAreas)
      .filter(([, patterns]) => patterns.some((pattern) => pattern.test(file)))
      .map(([area]) => area);
    if (matches.length !== 1) {
      throw new Error(`Unit test ${file} must belong to exactly one area (found: ${matches.join(", ") || "none"})`);
    }
    partition[matches[0]].push(file);
  }
  for (const [area, selected] of Object.entries(partition)) {
    if (selected.length === 0) throw new Error(`Unit test area ${area} has no tests`);
  }
  return partition;
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const area = process.argv[2];
  if (!Object.hasOwn(unitTestAreas, area) || process.argv.length !== 3) {
    throw new Error(`Expected one unit test area: ${Object.keys(unitTestAreas).join(", ")}`);
  }
  const files = readdirSync(unitDirectory).filter((file) => file.endsWith(".test.mjs")).sort();
  const selected = partitionUnitTests(files)[area].map((file) => join(unitDirectory, file));
  const result = spawnSync(process.execPath, ["--test", ...selected], { stdio: "inherit" });
  if (result.error) throw result.error;
  if (result.signal) process.kill(process.pid, result.signal);
  process.exitCode = result.status ?? 1;
}
