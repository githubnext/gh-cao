import fs from "node:fs";
import path from "node:path";

export const BACKFILL_STRESS_REPOSITORIES = [1000, 10000, 50000];

export function readBackfillStressCases(directory) {
  return BACKFILL_STRESS_REPOSITORIES.map((repositories) => {
    const folder = path.join(directory, `postgres-backfill-stress-${repositories}`);
    const read = (name) => {
      const file = path.join(folder, name);
      if (!fs.existsSync(file)) return null;
      return JSON.parse(fs.readFileSync(file, "utf8"));
    };
    return {
      repositories,
      status: read("case-status.json"),
      report: read(`stress-${repositories}.json`),
    };
  });
}

function casePassed(entry) {
  const report = entry.report;
  if (entry.status?.repositories !== entry.repositories || entry.status.status !== "success" || !report) {
    return false;
  }
  const expected = entry.repositories * 7 * report.runsPerDay;
  return report.repositories === entry.repositories &&
    Number.isSafeInteger(report.runsPerDay) && report.runsPerDay >= 1 &&
    report.horizonDays === 7 &&
    report.expectedRuns === expected &&
    report.queuedRuns === expected &&
    report.counts?.$runs === Math.min(expected, 200_000) &&
    report.counts?.$repositories === entry.repositories &&
    report.counts?.$workflows === entry.repositories &&
    report.webhooksDuringBackfill > 0 &&
    report.webhooks?.failed === 0 &&
    report.webhooks.accepted === report.webhooks.attempts &&
    Number.isFinite(report.backfillDurationMs) && report.backfillDurationMs >= 0;
}

export function buildBackfillStressReport({ repository, serverUrl, runId, runAttempt, sha, result, cases }) {
  const entries = BACKFILL_STRESS_REPOSITORIES.map((repositories) =>
    cases.find((entry) => entry.repositories === repositories) ?? { repositories, status: null, report: null });
  const passed = result === "success" && entries.every(casePassed);
  const runUrl = `${serverUrl}/${repository}/actions/runs/${runId}`;
  const lines = [
    `## Daily synthetic backfill: ${passed ? "PASSED" : "FAILED / INCOMPLETE"}`,
    "",
    `[Workflow run and diagnostic artifacts](${runUrl}) (attempt ${runAttempt}, commit \`${sha}\`).`,
    "",
    "| Repositories | Status | Seven-day runs | Backfill seconds | Accepted webhooks | During backfill | Retries |",
    "| ---: | --- | ---: | ---: | ---: | ---: | ---: |",
  ];
  for (const entry of entries) {
    const report = entry.report;
    const status = casePassed(entry) ? "Passed" :
      entry.status?.status === "success" ? "Incomplete / invalid report" : entry.status?.status ?? "Missing artifact";
    lines.push(`| ${entry.repositories.toLocaleString("en-US")} | ${status} | ` +
      `${report?.queuedRuns ?? "N/A"} | ${report ? (report.backfillDurationMs / 1000).toFixed(1) : "N/A"} | ` +
      `${report?.webhooks?.accepted ?? "N/A"} | ${report?.webhooksDuringBackfill ?? "N/A"} | ${report?.webhooks?.retries ?? "N/A"} |`);
  }
  lines.push("", "All traffic uses isolated synthetic numeric-loopback endpoints; no live GitHub APIs are used.",
    "Artifacts include per-case JSON, test output, local OTEL traces/logs/counters, service logs, and container exit/OOM state.",
    "Missing reports are incomplete evidence, not successful tests. Retained Run subtrees are capped at the native 200,000-input-row boundary; durable discovery remains complete.");
  return lines.join("\n");
}
