import { dashboardQueryIndex, queryInputNames, resolveDashboardQuerySources } from './declarative.js';
import { queryCollection } from '../storage/indexeddb.js';

export const EVIDENCE_READ_LIMITS = Object.freeze({ packets: 32, bytes: 16 * 1024 * 1024, facts: 20000 });
const source = 'operational-value-evidence';

/**
 * Union necessary equality predicates across every reachable source consumer.
 * Never narrow a shared root when even one consumer needs it unscoped.
 * @param {unknown[]} queries
 * @param {Iterable<string>} requested
 */
export function evidencePacketSelection(queries, requested) {
  const index = dashboardQueryIndex(queries);
  const reachable = resolveDashboardQuerySources(queries, requested);
  if ([...requested].some(name => name === source || name === '$operationalValueEvidence')) return null;
  const projection = index.get(source);
  if (projection && (projection.from !== '$operationalValueEvidence' || projection.compute?.length
      || projection.union?.length || projection.joins?.length || projection.aggregate || projection.window?.length
      || projection['temporal-series'] || projection.facet || projection.predict?.length || projection.limit !== undefined
      || (projection.select && ['report-id', 'workflow-route'].some(field =>
        !projection.select?.some(selected => selected.field === field && (selected.as ?? selected.field) === field))))) return null;
  /** @type {Array<{ field: 'id' | 'workflowId', value: string }>} */
  const selections = [];
  for (const name of reachable) {
    const query = index.get(name);
    if (query && query.name !== source && queryInputNames(query).includes('$operationalValueEvidence')) return null;
    if (!query || !queryInputNames(query).includes(source)) continue;
    if (query.from !== source || query.union?.length || query.joins?.length || query.compute?.length) return null;
    const predicate = query.filter?.predicates?.find(item => !item.optional
      && ['report-id', 'workflow-route'].includes(item.field) && typeof item.equals === 'string');
    if (!predicate) return null;
    const value = /** @type {string} */ (predicate.equals);
    selections.push(predicate.field === 'report-id'
      ? { field: 'id', value }
      : { field: 'workflowId', value: `workflow:${encodeURIComponent(value.toLowerCase())}` });
  }
  const distinct = [...new Map(selections.map(selection => [JSON.stringify(selection), selection])).values()];
  return distinct.length > 0 && distinct.length <= 32 ? distinct : null;
}

/**
 * Use existing primary/byWorkflow indexes before loading packet bodies.
 * The complete authored query still evaluates every predicate afterward.
 * @param {IDBFactory} indexedDB
 * @param {ReturnType<typeof evidencePacketSelection>} selections
 * @param {() => void} [checkpoint]
 */
export async function readEvidencePackets(indexedDB, selections, checkpoint) {
  /** @type {Map<unknown, Record<string, unknown>>} */
  const packets = new Map();
  let bytes = 0;
  let facts = 0;
  for (const selection of selections ?? [null]) {
    checkpoint?.();
    const rows = await queryCollection(indexedDB, 'operationalValueEvidence', selection
      ? [{ op: 'filter', predicates: [{ field: selection.field, equals: selection.value }] }] : [],
    { maxRows: EVIDENCE_READ_LIMITS.packets, maxBytes: EVIDENCE_READ_LIMITS.bytes, checkpoint });
    for (const packet of rows) {
      checkpoint?.();
      if (packets.has(packet.id)) continue;
      bytes += new TextEncoder().encode(JSON.stringify(packet)).length;
      facts += Array.isArray(packet.facts) ? packet.facts.length : 0;
      if (packets.size >= EVIDENCE_READ_LIMITS.packets || bytes > EVIDENCE_READ_LIMITS.bytes
          || facts > EVIDENCE_READ_LIMITS.facts) {
        throw new Error('Value evidence exceeds the bounded read budget; select an exact workflow or report.');
      }
      packets.set(packet.id, packet);
    }
  }
  return [...packets.values()];
}
