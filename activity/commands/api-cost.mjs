import path from "node:path";
import {
  dailyFileDietApiCostScenario,
  formatApiCostMarkdown,
  readApiCostScenario,
  simulateApiCost,
} from "../api-cost-model.mjs";

const numberOption = (options, name) => options[name] === undefined ? undefined : Number(options[name]);

export async function runApiCost({ options, option, rejectUnknownOptions, UsageError }) {
  const allowed = [
    "profile", "scenario", "input", "format", "evidence", "repositories",
    "horizon-hours", "collection-window-hours", "wall-minutes", "parallelism",
    "core-limit", "core-reserve", "search-limit", "search-reserve",
    "search-window-minutes", "graphql-limit", "graphql-reserve",
    "collector-grant", "tracked-issues", "new-issues", "mutable-pulls",
    "new-receipts", "search-pages-per-term", "timeline-pages-per-issue",
    "pull-file-pages", "snapshot-reads", "graphql-points-per-request",
  ];
  rejectUnknownOptions(options, allowed);
  const input = option(options, "input", false);
  const profile = option(options, "profile", false);
  if (input && profile) throw new UsageError("--input cannot be combined with --profile");
  if (!input && profile && profile !== "daily-file-diet") {
    throw new UsageError("--profile must be daily-file-diet");
  }
  let scenario;
  if (input) {
    scenario = await readApiCostScenario(path.resolve(input));
    if (options.repositories !== undefined) scenario.repositories = numberOption(options, "repositories");
    if (options["collection-window-hours"] !== undefined) {
      scenario.collectionWindowHours = numberOption(options, "collection-window-hours");
    }
    if (options["horizon-hours"] !== undefined) scenario.horizonHours = numberOption(options, "horizon-hours");
  } else {
    scenario = await dailyFileDietApiCostScenario({
      scenario: option(options, "scenario", false) ?? "retained",
      evidencePath: option(options, "evidence", false),
      repositories: numberOption(options, "repositories"),
      horizonHours: numberOption(options, "horizon-hours"),
      collectionWindowHours: numberOption(options, "collection-window-hours"),
      wallMinutes: numberOption(options, "wall-minutes"),
      parallelism: numberOption(options, "parallelism"),
      coreLimit: numberOption(options, "core-limit"),
      coreReserve: numberOption(options, "core-reserve"),
      searchLimit: numberOption(options, "search-limit"),
      searchReserve: numberOption(options, "search-reserve"),
      searchWindowMinutes: numberOption(options, "search-window-minutes"),
      graphqlLimit: numberOption(options, "graphql-limit"),
      graphqlReserve: numberOption(options, "graphql-reserve"),
      collectorGrant: numberOption(options, "collector-grant"),
      trackedIssues: numberOption(options, "tracked-issues"),
      newIssues: numberOption(options, "new-issues"),
      mutablePulls: numberOption(options, "mutable-pulls"),
      newReceipts: numberOption(options, "new-receipts"),
      searchPagesPerTerm: numberOption(options, "search-pages-per-term"),
      timelinePagesPerIssue: numberOption(options, "timeline-pages-per-issue"),
      pullFilePages: numberOption(options, "pull-file-pages"),
      snapshotReads: numberOption(options, "snapshot-reads"),
      graphqlPointsPerRequest: numberOption(options, "graphql-points-per-request"),
    });
  }
  const report = simulateApiCost(scenario);
  const format = option(options, "format", false) ?? "json";
  if (!["json", "markdown"].includes(format)) throw new UsageError("--format must be json or markdown");
  return format === "markdown" ? formatApiCostMarkdown(report) : report;
}
