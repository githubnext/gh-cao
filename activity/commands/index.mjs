import { runActivityStats } from "./activity-stats.mjs";
import { runAuditJsonl } from "./audit-jsonl.mjs";
import { runClusterProblems } from "./cluster-problems.mjs";
import { runCompactJsonl } from "./compact-jsonl.mjs";
import { runComputation } from "./computation.mjs";
import { runDashboardComplexity } from "./dashboard-complexity.mjs";
import { runDiscoverWorkflows } from "./discover-workflows.mjs";
import { runDownload } from "./download.mjs";
import { runGh } from "./gh.mjs";
import { runHashPayloads } from "./hash-payloads.mjs";
import { runIngestJsonl } from "./ingest-jsonl.mjs";
import { runIngest } from "./ingest.mjs";
import { runIssueStatus } from "./issue-status.mjs";
import { runMcp } from "./mcp.mjs";
import { runOperationalValueCommand } from "./operational-value.mjs";
import { runPages } from "./pages.mjs";
import { runPruneDashboard } from "./prune-dashboard.mjs";
import { runPrompt } from "./prompt.mjs";
import { runRecycleCache } from "./recycle-cache.mjs";
import { runRepairDatabase } from "./repair-database.mjs";
import { runQueries } from "./queries.mjs";
import { runQueryInfo } from "./query-info.mjs";
import { runQuery } from "./query.mjs";
import { runValidateActivityData } from "./validate-activity-data.mjs";
import { runValidate } from "./validate.mjs";

// Node cao is the Actions runtime, including supported workflow-agent evidence
// APIs. Overlap with operator-facing gh cao is intentional, not parity-only code.
// Keep workflow-agent evidence handlers and dependencies in the installed runtime.
export const commandHandlers = new Map([
  ["activity-stats", runActivityStats],
  ["audit-jsonl", runAuditJsonl],
  ["cluster-problems", runClusterProblems],
  ["compact-jsonl", runCompactJsonl],
  ["computation", runComputation],
  ["dashboard-complexity", runDashboardComplexity],
  ["discover-workflows", runDiscoverWorkflows],
  ["download", runDownload],
  ["gh", runGh],
  ["hash-payloads", runHashPayloads],
  ["ingest", runIngest],
  ["ingest-jsonl", runIngestJsonl],
  ["issue-status", runIssueStatus],
  ["mcp", runMcp],
  ["operational-value", runOperationalValueCommand],
  ["pages", runPages],
  ["prune-dashboard", runPruneDashboard],
  ["prompt", runPrompt],
  ["recycle-cache", runRecycleCache],
  ["repair-database", runRepairDatabase],
  ["queries", runQueries],
  ["query", runQuery],
  ["query-info", runQueryInfo],
  ["validate", runValidate],
  ["validate-activity-data", runValidateActivityData],
]);
