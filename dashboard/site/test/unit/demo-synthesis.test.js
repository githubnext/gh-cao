import { describe, expect, it, vi } from 'vitest';
import 'fake-indexeddb/auto';
import { IDBFactory } from 'fake-indexeddb';
import * as coordinator from '../../src/data/ingest/coordinator.js';
import { readCollection, readTransactions } from '../../src/data/storage/indexeddb.js';
import { synthesizeEnterpriseDemo, syntheticMemoryContent, syntheticMemoryManifest } from '../../src/data/demo/synthesis.js';
import { enterpriseRepositorySources } from '../../src/data/demo/sample.mjs';

vi.mock('../../src/deployment-profile.js', () => ({ DEMO_MODE: true }));

vi.mock('../../src/data/demo/sample.mjs', async (importOriginal) => {
  const actual = /** @type {typeof import('../../src/data/demo/sample.mjs')} */ (await importOriginal());
  return {
    ...actual,
    enterpriseDemo: { ...actual.enterpriseDemo, repositories: 2_101, installedRepositories: 1 },
    enterpriseRepositorySources: vi.fn(actual.enterpriseRepositorySources),
    enterpriseActivityBatches: function* (/** @type {number} */ now, /** @type {Parameters<typeof actual.enterpriseSample>[1]} */ options) {
      const sample = actual.enterpriseSample(now, { ...options, runs: 1_200 });
      // Exercise full production sessions and their children without replaying
      // three weeks of history on every batching/resume/reset assertion.
      const runs = sample.records.filter(record => record.kind === 'run');
      const selected = new Set([
        runs.find(record => record.run.safe_outputs.length > 0),
        runs.find(record => record.run.conclusion === 'failure'),
        runs.find(record => record.run.status !== 'completed')
      ].filter(record => record !== undefined).map(record => record.run.run_id));
      yield {
        runs: selected.size,
        records: sample.records.filter(record => record.kind === 'run'
          ? selected.has(record.run.run_id)
          : record.kind === 'safe_output_item' ? selected.has(record.safe_output.run_id) : true)
      };
    },
  };
});

describe('worker-owned synthetic enterprise population', () => {
  it('resumes committed batches, reuses same-day data and replaces the demo epoch without stale children', async () => {
    const indexedDB = new IDBFactory();
    const now = Date.parse('2040-06-15T12:00:00Z');
    const controller = new AbortController();
    const finalize = vi.spyOn(coordinator, 'finalizeNormalizedJsonlIngestion');
    /** @type {number[]} */
    const progress = [];
    await expect(synthesizeEnterpriseDemo(indexedDB, {
      now, signal: controller.signal,
      onProgress: (completed) => {
        progress.push(completed);
        if (completed === 1_001) controller.abort();
      },
    })).rejects.toThrow(/abort/i);
    expect(progress[0]).toBe(1);
    expect((await readCollection(indexedDB, 'repositories')).length).toBe(1_001);
    expect((await readTransactions(indexedDB)).find(row => row.kind === 'synthetic-demo-progress')?.complete).toBe(false);
    expect(finalize).toHaveBeenCalledTimes(1);

    vi.mocked(enterpriseRepositorySources).mockClear();
    await expect(synthesizeEnterpriseDemo(indexedDB, { now })).resolves.toMatchObject({ changed: true });
    expect(enterpriseRepositorySources).toHaveBeenNthCalledWith(1, 1_001, 1_000, now);
    expect((await readCollection(indexedDB, 'repositories')).length).toBe(2_101);
    expect((await readCollection(indexedDB, 'workflows')).length).toBe(4);
    expect((await readTransactions(indexedDB)).find(row => row.kind === 'synthetic-demo-complete')?.complete).toBe(true);

    vi.mocked(enterpriseRepositorySources).mockClear();
    finalize.mockClear();
    await expect(synthesizeEnterpriseDemo(indexedDB, { now: now + 60_000 })).resolves.toMatchObject({ changed: false });
    expect(finalize).not.toHaveBeenCalled();
    expect(enterpriseRepositorySources).not.toHaveBeenCalled();

    const tomorrow = now + 86_400_000;
    await expect(synthesizeEnterpriseDemo(indexedDB, { now: tomorrow })).resolves.toMatchObject({ changed: true });
    expect(finalize).toHaveBeenCalledTimes(1);
    expect(enterpriseRepositorySources).toHaveBeenNthCalledWith(1, 1, 1_000, tomorrow);
    expect((await readCollection(indexedDB, 'repositories')).length).toBe(2_101);
    const clean = new IDBFactory();
    await synthesizeEnterpriseDemo(clean, { now: tomorrow });
    expect(await readCollection(indexedDB, 'audits')).not.toHaveLength(0);
    expect(await readCollection(indexedDB, 'outputLifecycles')).not.toHaveLength(0);
    for (const collection of /** @type {const} */ (['runs', 'audits', 'tools', 'issues', 'outputLifecycles', 'operationalValues'])) {
      expect(await readCollection(indexedDB, collection)).toEqual(await readCollection(clean, collection));
    }
    const manifest = syntheticMemoryManifest(['dependency-health']);
    const file = manifest.campaigns[0].files[0];
    const content = syntheticMemoryContent('dependency-health', file.path)?.content;
    expect(new TextEncoder().encode(content).byteLength).toBe(file.size);
    expect(content).toContain('Synthetic dependency upgrade accepted');
    finalize.mockRestore();
  }, 60_000);
});
