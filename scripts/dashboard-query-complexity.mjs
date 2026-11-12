import { loadDashboardSource } from "../dashboard/report/bundle-dashboards.mjs";
import {
  analyzeDashboardComplexity,
  formatDashboardComplexityMarkdown,
  readDashboardTableCounts,
} from "../dashboard/report/query-complexity.mjs";

const [dashboard, database] = process.argv.slice(2);
if (!dashboard || process.argv.length > 4) {
  throw new Error("Usage: node scripts/dashboard-query-complexity.mjs DASHBOARD_JSON [CANONICAL_SQLITE]");
}
const { document } = await loadDashboardSource(dashboard);
const analysis = analyzeDashboardComplexity(document, {
  ...(database ? { tableCounts: readDashboardTableCounts(database) } : {}),
});
process.stdout.write(`${formatDashboardComplexityMarkdown(analysis)}\n`);
