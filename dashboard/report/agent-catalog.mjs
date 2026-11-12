import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { agentCatalog } from '../site/src/agent/catalog.js';
import { loadDashboardSource } from './bundle-dashboards.mjs';
const DEFAULT_DASHBOARD_PATH = fileURLToPath(new URL('../site/dashboard.json', import.meta.url));
const documents = new Map();
export function loadAgentDashboardDocument(dashboardPath) {
  const resolved = path.resolve(dashboardPath || DEFAULT_DASHBOARD_PATH);
  if (!documents.has(resolved)) {
    documents.set(resolved, loadDashboardSource(resolved).then(loaded => loaded.document));
  }
  return documents.get(resolved);
}
export async function readAgentCatalog({ dashboardPath } = {}) {
  return agentCatalog(await loadAgentDashboardDocument(dashboardPath));
}
