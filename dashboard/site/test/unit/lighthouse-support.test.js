// @vitest-environment node
import { createHash } from 'node:crypto';
import { mkdir, mkdtemp, readFile, rm } from 'node:fs/promises';
import { join } from 'node:path';
import { fileURLToPath } from 'node:url';
import { expect, it, vi } from 'vitest';
import { preparePerformanceData } from '../performance/lighthouse-support.mjs';

it('prepares only the generated enterprise demo and an empty version without network access', async () => {
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
    const records = { runs: new Set(), repositories: new Set() };
    for (const root of [siteRoot, emptyRoot]) {
      const dataRoot = join(root, 'demo-data-v1');
      const manifest = JSON.parse(await readFile(join(dataRoot, 'payload-hashes.json'), 'utf8'));
      for (const [name, hash] of Object.entries(manifest)) {
        const content = await readFile(join(dataRoot, name));
        expect(createHash('sha256').update(content).digest('hex')).toBe(hash);
        const envelopes = content.toString('utf8').trim().split('\n').map((line) => JSON.parse(line));
        expect(envelopes[0].kind).toBe('metadata');
        if (root === emptyRoot) expect(envelopes).toHaveLength(1);
        else for (const envelope of envelopes) {
          if (envelope.collection === 'runs') records.runs.add(envelope.record.id);
          if (envelope.collection === 'repositories') records.repositories.add(envelope.record.id);
        }
      }
      const inventory = JSON.parse(await readFile(join(dataRoot, 'inventory-sources.json'), 'utf8'));
      expect(Object.values(inventory).every((source) => source.metadata['source-kind'] === 'synthetic')).toBe(true);
      if (root === emptyRoot) expect(Object.values(inventory).every((source) => source.rows.length === 0)).toBe(true);
      for (const path of ['payload-hashes.json', 'gh-aw-logs.sqlite', 'agent-summary.json']) {
        await expect(readFile(join(root, path))).rejects.toThrow(/ENOENT/);
      }
    }
    expect(records.runs.size).toBeGreaterThan(0);
    expect(records.repositories.size).toBeGreaterThan(0);
    expect(dataset.expected).toEqual({ runs: records.runs.size, repositories: records.repositories.size });
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
