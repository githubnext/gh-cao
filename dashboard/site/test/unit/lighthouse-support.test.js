// @vitest-environment node
import { createHash } from 'node:crypto';
import { mkdir, mkdtemp, readFile, rm } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { expect, it, vi } from 'vitest';
import { IDBFactory } from 'fake-indexeddb';
import { recordTransaction } from '../../src/data/storage/indexeddb.js';
import { canonicalCounts, preparePerformanceData } from '../performance/lighthouse-support.mjs';

it('prepares the browser-generated enterprise demo and a private empty fixture without network access', async () => {
  const temporaryRoot = fileURLToPath(new URL('../../../../.tmp/', import.meta.url));
  await mkdir(temporaryRoot, { recursive: true });
  const directory = await mkdtemp(join(temporaryRoot, 'lighthouse-support-'));
  const fetch = vi.fn(() => { throw new Error('Lighthouse must not download operational data.'); });
  vi.stubGlobal('fetch', fetch);
  vi.stubEnv('DASHBOARD_DATA_URL', 'https://operational.example/payload-hashes.json');
  vi.stubEnv('DASHBOARD_PERFORMANCE_DATA_ROOT', '/must-not-read');
  const siteRoot = join(directory, 'site');
  const emptyRoot = join(directory, 'empty');
  const now = Date.parse('2026-11-11T12:00:00Z');
  try {
    const dataset = await preparePerformanceData({ siteRoot, emptyRoot, now });
    expect(fetch).not.toHaveBeenCalled();
    expect(dataset).toMatchObject({
      evidenceKind: 'synthetic-enterprise-demo', synthetic: true,
      generatedAt: new Date(now).toISOString(), retentionAsOf: now
    });
    expect(dataset.evidenceLimits).toMatch(/not operational freshness, completeness, or production performance/);
    expect(dataset).not.toHaveProperty('sourceUrl');
    const bootstrap = await readFile(join(siteRoot, 'index.html'), 'utf8');
    expect(bootstrap).toContain('name="dashboard-deployment" content="synthetic-demo"');
    expect(dataset.expected.runs).toBeGreaterThan(0);
    expect(dataset.expected.repositories).toBe(1_000);
    expect(dataset.sampleVersion).toBeGreaterThan(0);
    for (const path of ['payload-hashes.json', 'demo-data/payload-hashes.json', 'inventory-sources.json', 'agent-summary.json']) {
      await expect(readFile(join(siteRoot, path))).rejects.toThrow(/ENOENT/);
    }
    const manifest = JSON.parse(await readFile(join(emptyRoot, 'payload-hashes.json'), 'utf8'));
    expect(Object.keys(manifest).some((name) => name.startsWith('gh-aw-logs-runs/'))).toBe(true);
    expect(Object.keys(manifest).some((name) => name.startsWith('gh-aw-logs-records/'))).toBe(true);
    for (const [name, hash] of Object.entries(manifest)) {
      const content = await readFile(join(emptyRoot, name));
      expect(createHash('sha256').update(content).digest('hex')).toBe(hash);
      const envelopes = content.toString('utf8').trim().split('\n').map((line) => JSON.parse(line));
      expect(envelopes[0].kind).toBe('metadata');
      expect(envelopes).toHaveLength(1);
    }
    expect(JSON.parse(await readFile(join(emptyRoot, 'inventory-sources.json'), 'utf8'))).toEqual({});
    // Both output roots remain owned and safely replaceable on repeated runs.
    await expect(preparePerformanceData({ siteRoot, emptyRoot, now })).resolves.toEqual(dataset);
  } finally {
    vi.unstubAllGlobals();
    vi.unstubAllEnvs();
    await rm(directory, { recursive: true, force: true });
  }
}, 60_000);

it('rejects the retired external-source and local-real-data preparation options', async () => {
  for (const options of [{ sourceUrl: 'https://operational.example/payload-hashes.json' }, { local: true }]) {
    await expect(preparePerformanceData(options)).rejects.toThrow(/synthetic demo preparation options only/);
  }
});

it('requires a completed compatible synthesis receipt before accepting full performance evidence', async () => {
  const indexedDB = new IDBFactory();
  vi.stubGlobal('indexedDB', indexedDB);
  const population = { repositories: 10_000, sampleVersion: 3, retentionAsOf: Date.parse('2026-11-11T12:00:00Z') };
  const receipt = {
    id: 'synthetic-demo:population', kind: 'synthetic-demo-complete', complete: true,
    createdAt: new Date(population.retentionAsOf).toISOString(),
    repositories: population.repositories, sampleVersion: population.sampleVersion,
    sampleDay: Math.floor(population.retentionAsOf / 86_400_000)
  };
  // Execute the browser probe against the production IndexedDB schema.
  const page = /** @type {import('@playwright/test').Page} */ (/** @type {unknown} */ ({
    evaluate: (/** @type {Function} */ evaluate, /** @type {unknown} */ argument) => evaluate(argument)
  }));
  try {
    await recordTransaction(indexedDB, { id: 'unrelated', kind: 'test', createdAt: receipt.createdAt });
    await expect(canonicalCounts(page, population)).rejects.toThrow(/incomplete or incompatible/);
    for (const patch of [
      { complete: false }, { repositories: population.repositories - 1 },
      { sampleVersion: population.sampleVersion - 1 }, { sampleDay: receipt.sampleDay - 1 }
    ]) {
      await recordTransaction(indexedDB, { ...receipt, ...patch });
      await expect(canonicalCounts(page, population)).rejects.toThrow(/incomplete or incompatible/);
    }
    await recordTransaction(indexedDB, receipt);
    await expect(canonicalCounts(page, population)).resolves.toMatchObject({ runs: 0, repositories: 0, transactions: 2 });
  } finally {
    vi.unstubAllGlobals();
  }
});
