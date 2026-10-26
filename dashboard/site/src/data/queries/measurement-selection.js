import { dashboardQueryIndex, queryInputNames, resolveDashboardQuerySources } from './declarative.js';
import { queryCollection } from '../storage/indexeddb.js';

const source = 'workflow-value-measurements';
/** @param {unknown[]} definitions @param {Iterable<string>} requested */
export function measurementSelection(definitions, requested) {
  const index = dashboardQueryIndex(definitions);
  if ([...requested].some(name => [source, '$operationalValues', 'operational-values'].includes(name))) return null;
  const projection = index.get(source);
  if (projection && (projection.from !== '$operationalValues' || projection.compute?.length
    || projection.joins?.length || projection.union?.length || projection.aggregate || projection.window?.length
    || projection.facet || projection['temporal-series'] || projection.predict?.length || projection.limit !== undefined)) return null;
  /** @type {Array<{field: string, value: string}>} */
  const selections = [];
  for (const name of resolveDashboardQuerySources(definitions, requested)) {
    const query = index.get(name);
    if (!query) continue;
    if (queryInputNames(query).includes('operational-values')
      || query.name !== source && queryInputNames(query).includes('$operationalValues')) return null;
    if (!queryInputNames(query).includes(source)) continue;
    if (query.from !== source || query.compute?.length || query.joins?.length || query.union?.length) return null;
    const predicate = query.filter?.predicates?.find(item => !item.optional
      && ['id', 'workflow-route'].includes(item.field) && typeof item.equals === 'string');
    if (!predicate) return null;
    if (projection?.select?.length && !projection.select.some(selected =>
      selected.field === predicate.field && (selected.as ?? selected.field) === predicate.field)) return null;
    selections.push({ field: predicate.field, value: /** @type {string} */ (predicate.equals) });
  }
  return selections.length > 0 && selections.length <= 32
    ? [...new Map(selections.map(item => [JSON.stringify(item), item])).values()] : null;
}

/** @param {Record<string, unknown>[]} records */
export function measurementRows(records) {
  return records.map(record => {
    const slug = record['workflow-slug'];
    const valid = typeof record.repository === 'string' && typeof slug === 'string'
      && /^[a-z0-9]+(?:-[a-z0-9]+)*$/.test(slug)
      && typeof record.valueId === 'string' && record.valueId.startsWith(`${slug}.`);
    return { ...record, ...(valid ? { 'workflow-route': `${record.repository}:.github/workflows/${slug}.md` } : {}) };
  });
}

/** @param {IDBFactory} indexedDB @param {ReturnType<typeof measurementSelection>} selections @param {() => void} [checkpoint]
 * @param {(metrics: { keysScanned: number, recordsRead: number, bytes: number }) => void} [onMetrics] */
export async function readMeasurements(indexedDB, selections, checkpoint, onMetrics) {
  const result = new Map();
  let bytes = 0;
  let keysScanned = 0;
  let recordsRead = 0;
  for (const selection of selections ?? [null]) {
    checkpoint?.();
    let field = 'id';
    let value = selection?.value;
    /** @type {((key: IDBValidKey) => boolean) | undefined} */
    let keyFilter;
    if (selection?.field === 'workflow-route') {
      const parts = /^([^:]+):\.github\/workflows\/([a-z0-9-]+)\.md$/.exec(selection.value);
      if (!parts) continue;
      field = 'repositoryId';
      value = `repository:${encodeURIComponent(parts[1].toLowerCase())}`;
      const marker = encodeURIComponent(`:${parts[1].toLowerCase()}:${parts[2]}.`);
      keyFilter = key => typeof key === 'string' && key.includes(marker);
    }
    const rows = await queryCollection(indexedDB, 'operationalValues',
      selection ? [{ op: 'filter', predicates: [{ field, equals: value }] }] : [],
      { maxRows: 20000, maxBytes: 16 * 1024 * 1024,
        keyFilter: key => { keysScanned++; return keyFilter ? keyFilter(key) : true; }, checkpoint });
    recordsRead += rows.length;
    for (const row of rows) {
      if (result.has(row.id)) continue;
      bytes += new TextEncoder().encode(JSON.stringify(row)).length;
      if (result.size >= 20000 || bytes > 16 * 1024 * 1024) throw new Error('Native measurement read budget exceeded');
      result.set(row.id, row);
    }
  }
  onMetrics?.({ keysScanned, recordsRead, bytes });
  return [...result.values()];
}
