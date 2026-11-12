import { loadDashboardSource } from "../dashboard/report/bundle-dashboards.mjs";
import {
  analyzeDashboardComplexity,
  formatDashboardComplexityMarkdown,
  readDashboardTableCounts,
} from "../dashboard/report/query-complexity.mjs";

const [dashboard, canonical] = process.argv.slice(2);
if (!dashboard || process.argv.length > 4 || (canonical !== undefined && canonical !== "--canonical")) {
  throw new Error("Usage: node scripts/dashboard-query-complexity.mjs DASHBOARD_JSON [--canonical]");
}
const { document } = await loadDashboardSource(dashboard);
const analysis = analyzeDashboardComplexity(document, {
  ...(canonical ? { tableCounts: await readDashboardTableCounts() } : {}),
});
process.stdout.write(`${formatDashboardComplexityMarkdown(analysis)}\n`);
