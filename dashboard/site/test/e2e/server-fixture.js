import { createHash } from 'node:crypto';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { normalize } from '../../src/data/normalize/index.js';
import { queryDashboardSourceObservations } from '../../src/data/queries/ingestion.js';
import { mergeActivityStructuralRecord } from '../../src/data/storage/retention.js';
const { writeDashboardSnapshot } = await import(new URL('../../../../tests/helpers/dashboard-snapshot.mjs', import.meta.url).href);

/**
 * Prepare canonical evidence offline for production Go ingestion.
 * @param {string} destination
 * @param {{ sources?: Record<string, unknown>, shards?: string[] }} [options]
 */
export async function writeServerArtifact(destination, { sources = {}, shards = [] } = {}) {
  await mkdir(destination, { recursive: true });
  const collections = new Map(Object.entries(normalize(queryDashboardSourceObservations(sources).observations))
    .map(([name, rows]) => [name, new Map(rows.map(record => [record.id, record]))]));
  for (const content of shards) {
    const [headerLine, ...lines] = content.trim().split('\n');
    const header = JSON.parse(headerLine);
    if (header.kind !== 'metadata' || !['runs', 'records'].includes(header.phase)) {
      throw new Error('Server fixtures require compacted canonical run/record shards.');
    }
    for (const line of lines) {
      const { collection, record } = JSON.parse(line);
      const records = collections.get(collection);
      if (!records) throw new Error(`Unknown canonical fixture collection: ${collection}`);
      const existing = records.get(record.id);
      records.set(record.id, existing && ['repositories', 'workflows'].includes(collection)
        ? mergeActivityStructuralRecord(collection, existing, record) : record);
    }
  }
  await writeDashboardSnapshot(destination, Object.fromEntries(
    [...collections].map(([name, records]) => [name, [...records.values()]])));
  const { readFile } = await import('node:fs/promises');
  const manifest = JSON.parse(await readFile(join(destination, 'payload-hashes.json'), 'utf8'));
  const inventory = JSON.stringify(sources);
  await writeFile(join(destination, 'inventory-sources.json'), inventory);
  manifest['inventory-sources.json'] = createHash('sha256').update(inventory).digest('hex');
  await writeFile(join(destination, 'payload-hashes.json'), JSON.stringify(manifest));
}
