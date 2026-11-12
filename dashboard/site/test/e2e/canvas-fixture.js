import { createHash } from 'node:crypto';
import { mkdir, writeFile } from 'node:fs/promises';
import { join } from 'node:path';
import { normalize } from '../../src/data/normalize/index.js';
import { queryDashboardSourceObservations } from '../../src/data/queries/ingestion.js';
const { writeCanvasSnapshot } = await import(new URL('../../../../tests/helpers/canvas-snapshot.mjs', import.meta.url).href);

/**
 * Prepare canonical evidence offline; the canvas itself never ingests browser sources.
 * @param {string} destination
 * @param {{ sources?: Record<string, unknown>, shards?: string[] }} [options]
 */
export async function writeCanvasArtifact(destination, { sources = {}, shards = [] } = {}) {
  await mkdir(destination, { recursive: true });
  await writeCanvasSnapshot(destination, normalize(queryDashboardSourceObservations(sources).observations));
  /** @type {Record<string, string>} */
  const hashes = {};
  for (const [index, content] of shards.entries()) {
    const name = `fixture-${index}.jsonl`;
    await writeFile(join(destination, 'gh-aw-logs-shards', name), content);
    hashes[`gh-aw-logs-shards/${name}`] = createHash('sha256').update(content).digest('hex');
  }
  const { readFile } = await import('node:fs/promises');
  const manifest = JSON.parse(await readFile(join(destination, 'payload-hashes.json'), 'utf8'));
  const inventory = JSON.stringify(sources);
  await writeFile(join(destination, 'inventory-sources.json'), inventory);
  hashes['inventory-sources.json'] = createHash('sha256').update(inventory).digest('hex');
  await writeFile(join(destination, 'payload-hashes.json'), JSON.stringify({ ...manifest, ...hashes }));
}
