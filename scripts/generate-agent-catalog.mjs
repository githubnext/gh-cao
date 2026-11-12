import { writeFile } from 'node:fs/promises';

import { loadAgentDashboardDocument, readAgentCatalog } from '../dashboard/report/agent-catalog.mjs';

const output = new URL('../dashboard/site/src/agent/catalog.generated.json', import.meta.url);
const queriesOutput = new URL('../dashboard/site/src/agent/queries.generated.json', import.meta.url);
const [catalog, dashboard] = await Promise.all([
  readAgentCatalog(),
  loadAgentDashboardDocument()
]);
await writeFile(output, `${JSON.stringify(catalog, null, 2)}\n`);
await writeFile(queriesOutput, `${JSON.stringify(dashboard.dashboard.queries)}\n`);
