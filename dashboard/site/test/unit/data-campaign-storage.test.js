import { IDBFactory, IDBKeyRange } from 'fake-indexeddb';
import { afterEach, describe, expect, it } from 'vitest';
import { normalize } from '../../src/data/normalize/index.js';
import { queryDatabaseSources } from '../../src/data/queries/database.js';
import {
  deleteCanonicalDatabase, maintainCanonicalDatabase, openCanonicalDatabase,
  readCollection, readIndex, replaceCanonicalBatch, upsertCanonicalBatch
} from '../../src/data/storage/indexeddb.js';

const originalKeyRange = globalThis.IDBKeyRange;
afterEach(() => { globalThis.IDBKeyRange = originalKeyRange; });

function campaignBatch(version = 'v1') {
  return normalize([{
    kind: 'campaign', source: 'dashboard-sources', sourceId: 'dependabot',
    observedAt: '2026-10-02T00:00:00Z',
    data: {
      slug: 'dependabot', name: 'Dependabot', description: 'Dependency maintenance',
      mode: 'review', enabled: false, version, currentVersion: 'v2',
      updateState: version === 'v2' ? 'current' : 'update-available',
      workerCount: 0, rolloutPercent: 12.5, monthlyAiCreditBudget: 10.5,
      campaignLink: { relation: 'internal', href: '#campaign/dependabot', label: 'Dependabot' },
      intelligenceDeclaration: { campaign: 'dependabot' }
    }
  }]);
}

describe('Campaign browser storage', () => {
  it('persists idle campaigns, refreshes stable identity, queries by slug, and deletes them', async () => {
    globalThis.IDBKeyRange = IDBKeyRange;
    const factory = new IDBFactory();
    const database = await openCanonicalDatabase(factory);
    const store = database.transaction('campaigns').objectStore('campaigns');
    expect(store.keyPath).toBe('id');
    expect(store.index('bySlug').keyPath).toBe('slug');
    database.close();
    await upsertCanonicalBatch(factory, campaignBatch());
    await upsertCanonicalBatch(factory, campaignBatch('v2'));
    const records = await readCollection(factory, 'campaigns');
    expect(records).toEqual(campaignBatch('v2').campaigns);
    expect(await readIndex(factory, 'campaigns', 'bySlug', ['dependabot'])).toEqual(records);
    const sources = await queryDatabaseSources(factory, {}, ['campaigns']);
    expect(sources.campaigns.rows).toEqual([expect.objectContaining({
      id: records[0].id, campaign: 'dependabot', 'campaign-version': 'v2',
      'campaign-enabled': false, 'campaign-worker-count': 0
    })]);
    await maintainCanonicalDatabase(factory, {
      now: Date.parse('2027-01-01T00:00:00Z'), retentionWindowMs: 1, maxDatabaseBytes: 0
    });
    expect(await readCollection(factory, 'campaigns')).toEqual(records);
    await replaceCanonicalBatch(factory, normalize([]));
    expect(await readCollection(factory, 'campaigns')).toEqual([]);
    await upsertCanonicalBatch(factory, campaignBatch());
    await deleteCanonicalDatabase(factory);
    expect(await readCollection(factory, 'campaigns')).toEqual([]);
  });
});
