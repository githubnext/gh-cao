import { enterpriseActivityBatches, enterpriseDemo, enterpriseMemory, enterpriseRepositorySources, enterpriseSample } from './sample.mjs';
import { finalizeNormalizedJsonlIngestion, ingestCachedGhAwJsonl, ingestDashboardSourceBatches, ingestDashboardSources } from '../ingest/coordinator.js';
import { canonicalDatabaseName, DATABASE_VERSION, deleteCanonicalDatabase, readTransaction, readTransactions, recordTransaction } from '../storage/indexeddb.js';
import { createDebug } from '../../debug.js';

export { enterpriseDemo };

const debugSynthesis = createDebug('synthesis');

const receiptId = 'synthetic-demo:population';
const batchSize = 1_000;
const installedBatchSize = 50;
const memoryTime = Date.now();

/**
 * Generate raw samples only inside the data worker; all records pass through
 * the same adapters and canonical normalizer as collected evidence.
 * @param {IDBFactory} indexedDB
 * @param {{ signal?: AbortSignal, storage?: StorageManager, now?: number, onProgress?: (completed: number, total: number) => void, onCommitted?: (sources: Record<string, import('../../presenter.js').LogicalSourceInput>) => void }} [options]
 */
export async function synthesizeEnterpriseDemo(indexedDB, options = {}) {
  options.signal?.throwIfAborted();
  if (!/^gh-aw-cao-dashboard-data:synthetic-demo(?::|$)/.test(canonicalDatabaseName())) {
    throw new Error('Synthetic generation requires an isolated synthetic-demo database.');
  }
  const task = () => populateEnterpriseDemo(indexedDB, options);
  return globalThis.navigator?.locks
    ? navigator.locks.request(`synthetic-demo-population:${canonicalDatabaseName()}`, { signal: options.signal }, task)
    : task();
}

/**
 * @param {IDBFactory} indexedDB
 * @param {NonNullable<Parameters<typeof synthesizeEnterpriseDemo>[1]>} options
 */
async function populateEnterpriseDemo(indexedDB, options) {
  let receipt = await readTransaction(indexedDB, receiptId);
  const clock = options.now ?? Date.now();
  const sampleDay = Math.floor(clock / 86_400_000);
  const incompatible = receipt && (receipt.sampleDay !== sampleDay
    || receipt.sampleVersion !== enterpriseDemo.version || receipt.schemaVersion !== DATABASE_VERSION
    || !Number.isSafeInteger(receipt.repositories) || Number(receipt.repositories) < 0
    || Number(receipt.repositories) > enterpriseDemo.repositories
    || !Number.isSafeInteger(receipt.installedRepositories) || Number(receipt.installedRepositories) < 0
    || Number(receipt.installedRepositories) > enterpriseDemo.installedRepositories
    || !Number.isSafeInteger(receipt.activityBatches) || Number(receipt.activityBatches) < 0
    || receipt.complete === true && receipt.repositories !== enterpriseDemo.repositories);
  const legacy = !receipt && (await readTransactions(indexedDB))
    .some(entry => String(entry.kind).startsWith('synthetic-demo-') || entry.kind === 'dashboard-snapshot');
  if (incompatible || legacy) {
    options.signal?.throwIfAborted();
    debugSynthesis({ event: 'reset', reason: incompatible ? 'incompatible-receipt' : 'legacy-data' });
    await deleteCanonicalDatabase(indexedDB);
    receipt = null;
  }
  const now = receipt?.sampleDay === sampleDay && typeof receipt?.sampleTime === 'number' ? receipt.sampleTime : clock;
  /** @type {Record<string, any>} */
  const sources = {};
  const complete = receipt?.complete === true;
  let completed = Number(receipt?.repositories ?? 0);
  let installed = Number(receipt?.installedRepositories ?? 0);
  let activityBatches = Number(receipt?.activityBatches ?? 0);
  debugSynthesis({
    event: 'start', resumed: receipt !== null, complete,
    repositories: completed, installedRepositories: installed, activityBatches
  });
  const save = () => recordTransaction(indexedDB, {
    id: receiptId, kind: 'synthetic-demo-progress', createdAt: new Date().toISOString(),
    sampleTime: now, sampleDay, sampleVersion: enterpriseDemo.version, schemaVersion: DATABASE_VERSION,
    repositories: completed, installedRepositories: installed, activityBatches, complete: false,
  });
  if (!receipt) await save();
  async function* repositories() {
    for (let offset = completed; offset < enterpriseDemo.repositories; offset += batchSize) {
      options.signal?.throwIfAborted();
      yield enterpriseRepositorySources(offset, Math.min(batchSize, enterpriseDemo.repositories - offset), now);
    }
  }
  if (!complete) await ingestDashboardSourceBatches(indexedDB, repositories(), {
    ...options,
    onBatchCommitted: async (count) => {
      completed += count;
      await save();
      options.onProgress?.(completed, enterpriseDemo.repositories);
      options.onCommitted?.(sources);
    },
  });
  for (let index = 0; index < enterpriseDemo.installedRepositories; index += installedBatchSize) {
    options.signal?.throwIfAborted();
    const count = Math.min(installedBatchSize, enterpriseDemo.installedRepositories - index);
    const sample = enterpriseSample(now, { start: index, count, includeHistory: false });
    for (const [name, source] of Object.entries(sample.sources)) {
      sources[name] ??= { ...source, rows: [] };
      if (index && ['campaigns', 'marketplace-packages', 'marketplace-registries', 'configuration-policy'].includes(name)) continue;
      sources[name].rows.push(...source.rows);
    }
    if (!complete && index + count > installed) {
      await ingestDashboardSources(indexedDB, sample.sources, {
        ...options, now, deferMaintenance: true,
        payloadScope: `${receiptId}/inventory/${index}`,
      });
      installed = index + count;
      await save();
      options.onCommitted?.(sources);
    }
    await new Promise(resolve => setTimeout(resolve, 0));
  }
  if (complete) {
    return { sources, changed: false };
  }
  let batchIndex = 0;
  for (const batch of enterpriseActivityBatches(now)) {
    options.signal?.throwIfAborted();
    if (batchIndex++ < activityBatches) continue;
    await ingestCachedGhAwJsonl(indexedDB, batch.records.map(record => JSON.stringify(record)).join('\n'), {
      signal: options.signal, storage: options.storage, now, deferMaintenance: true,
      payloadScope: `${receiptId}/activity/${batchIndex - 1}`,
    });
    activityBatches = batchIndex;
    await save();
    options.onCommitted?.(sources);
    await new Promise(resolve => setTimeout(resolve, 0));
  }
  if (batchIndex !== activityBatches) throw new Error('Synthetic activity checkpoint exceeds the generated sample.');
  await finalizeNormalizedJsonlIngestion(indexedDB, {
    now, storage: options.storage, signal: options.signal,
  });
  options.signal?.throwIfAborted();
  await recordTransaction(indexedDB, {
    id: receiptId, kind: 'synthetic-demo-complete', createdAt: new Date().toISOString(),
    sampleTime: now, sampleDay, sampleVersion: enterpriseDemo.version, schemaVersion: DATABASE_VERSION,
    repositories: completed, installedRepositories: installed, activityBatches, complete: true,
  });
  debugSynthesis({
    event: 'complete', repositories: completed, installedRepositories: installed, activityBatches
  });
  return { sources, changed: true };
}

/** @param {string[]} campaigns @param {number} [now] */
export function syntheticMemoryManifest(campaigns, now = memoryTime) {
  return {
    version: 1,
    campaigns: enterpriseMemory(now).filter(entry => campaigns.includes(entry.campaign)).map(entry => ({
      campaign: entry.campaign, branch: `memory/${entry.campaign}`, commit: entry.oid,
      files: [{ path: entry.path, oid: entry.oid, sha256: entry.sha256, size: new TextEncoder().encode(entry.content).byteLength }],
      omitted: { fileLimit: 0, fileSize: 0, totalSize: 0, extension: 0, nesting: 0, unsafePath: 0, invalidContent: 0, unsupportedType: 0 },
    })),
  };
}

/** @param {string} campaign @param {string} path */
export function syntheticMemoryContent(campaign, path) {
  const entry = enterpriseMemory(memoryTime).find(entry => entry.campaign === campaign && entry.path === path);
  return entry ? { content: entry.content } : null;
}
