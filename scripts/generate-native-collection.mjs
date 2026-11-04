import { readFile, writeFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import { PRUNED_CANONICAL_FIELDS } from '../dashboard/site/src/data/model/fields.js';
import { ROUTING_AGGREGATE_FIELDS } from '../dashboard/site/src/data/normalize/model-routing.js';
import { dashboardFormDefaultValues, resolveDashboardQueryParameters } from '../dashboard/site/src/data/queries/view-payload-compiler.js';

const read = async (path) => JSON.parse(await readFile(new URL(path, import.meta.url), 'utf8'));
const database = await read('../dashboard/site/src/data/queries/database.json');
const routing = await read('../dashboard/site/dashboard-fragments/model-routing.json');
const ingestion = await read('../dashboard/site/src/data/normalize/routing-aggregate.json');
const transient = ['routingBaselineModel', 'routingBaselineComplete', 'routingBaselineMainRequests', 'eligibleChoices', 'catalogueOverlap'];
const audits = database.find((query) => query.name === 'audits');
if (!audits?.select) throw new Error('Missing canonical Audit projection');
const definitions = resolveDashboardQueryParameters([...routing.queries, ...ingestion],
  dashboardFormDefaultValues(routing.pages[0].form));
const queries = [
  { ...audits, select: [...audits.select, { field: 'runId', as: 'canonical-run-id' },
    ...transient.map((field) => ({ field }))] },
  ...definitions.map((query) => query.name === 'routing-runs' ? {
    name: 'routing-runs', from: 'routing-resolved-runs',
    compute: [{ as: 'full-repository', function: 'concat', args: [
      { field: 'organization' }, { value: '/' }, { field: 'repository' }
    ] }]
  } : query.name === 'routing-cost-runs' ? {
    // The ingestion-only routing-runs override has no retained baseline fields.
    // Do not materialize its null placeholders before the authoritative
    // ingestion baseline joins: the SQL contract forbids shadowed join aliases.
    ...query, joins: query.joins.map((join) => join.source === 'routing-runs'
      ? { ...join, fields: [] } : join)
  } : query.name === 'routing-run-facts' ? {
    ...query, aggregate: { ...routing.queries.find((entry) => entry.name === query.name)?.aggregate,
      values: [...(routing.queries.find((entry) => entry.name === query.name)?.aggregate?.values ?? []),
        { field: 'canonical-run-id', as: 'canonical-run-id', reducer: 'unique' }] }
  } : query)
];
const content = `${JSON.stringify({
  queries, routingFields: ROUTING_AGGREGATE_FIELDS, prunedFields: PRUNED_CANONICAL_FIELDS, transient
}, null, 2)}\n`;
const output = new URL('../server/internal/collectnative/projection_contract.gen.json', import.meta.url);
if (process.argv.includes('--check')) {
  if (await readFile(output, 'utf8') !== content) throw new Error(`Stale ${fileURLToPath(output)}`);
} else {
  await writeFile(output, content);
}
