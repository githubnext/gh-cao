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
    enterpriseDemo: { ...actual.enterpriseDemo, repositories: 2_101, installedRepositories: 3 },
    enterpriseRepositorySources: vi.fn(actual.enterpriseRepositorySources),
    enterpriseActivityBatches: function* (/** @type {number} */ now) {
      const sample = actual.enterpriseSample(now, { repositories: 3, count: 3, runs: 1_200 });
      // Exercise full production sessions and their children without replaying
      // three weeks of history on every batching/resume/reset assertion.
      const runs = sample.records.filter(record => record.kind === 'run');
      const recent = runs.filter(record => Date.parse(record.run.started_at) >= now - 7 * 86_400_000);
      const repositories = [...new Set(runs.map(record => record.run.repository))];
      const firstPass = new Set(repositories.map(repository =>
        recent.find(record => record.run.repository === repository)?.run.run_id));
      const selected = new Set([
        ...firstPass,
        recent.find(record => record.run.safe_outputs.length > 0)?.run.run_id,
        recent.find(record => record.run.conclusion === 'failure')?.run.run_id,
        recent.find(record => record.run.status !== 'completed')?.run.run_id
      ].filter(id => id !== undefined));
      for (const ids of [firstPass, new Set([...selected].filter(id => !firstPass.has(id)))]) {
        yield {
          runs: ids.size,
          records: sample.records.filter(record => record.kind === 'run'
            ? ids.has(record.run.run_id)
            : record.kind === 'safe_output_item' && ids.has(record.safe_output.run_id))
        };
      }
      yield { runs: 0, records: sample.records.filter(record => record.kind !== 'run' && record.kind !== 'safe_output_item') };
    },
  };
});

describe('worker-owned synthetic enterprise population', () => {
  it('resumes committed batches, reuses same-day data and replaces the demo epoch without stale children', async () => {
    const indexedDB = new IDBFactory();
    const now = Date.parse('2040-06-15T12:00:00Z');
    const controller = new AbortController();
    const finalize = vi.spyOn(coordinator, 'finalizeNormalizedJsonlIngestion');
    const onCommitted = vi.fn();
    /** @type {number[]} */
    const progress = [];
    await expect(synthesizeEnterpriseDemo(indexedDB, {
      now, signal: controller.signal,
      onProgress: (completed) => {
        progress.push(completed);
        if (completed === 1_000) controller.abort();
      },
    })).rejects.toThrow(/abort/i);
    expect(progress[0]).toBe(1_000);
    expect((await readCollection(indexedDB, 'repositories')).length).toBe(1_000);
    expect((await readTransactions(indexedDB)).find(row => row.kind === 'synthetic-demo-progress')?.complete).toBe(false);
    expect(finalize).not.toHaveBeenCalled();

    vi.mocked(enterpriseRepositorySources).mockClear();
    await expect(synthesizeEnterpriseDemo(indexedDB, { now, onCommitted })).resolves.toMatchObject({ changed: true });
    expect(onCommitted).toHaveBeenCalledTimes(6);
    onCommitted.mockClear();
    expect(enterpriseRepositorySources).toHaveBeenNthCalledWith(1, 1_000, 1_000, now);
    expect((await readCollection(indexedDB, 'repositories')).length).toBe(2_101);
    expect((await readCollection(indexedDB, 'workflows')).length).toBe(12);
    expect((await readTransactions(indexedDB)).find(row => row.kind === 'synthetic-demo-complete')?.complete).toBe(true);

    vi.mocked(enterpriseRepositorySources).mockClear();
    finalize.mockClear();
    await expect(synthesizeEnterpriseDemo(indexedDB, { now: now + 60_000, onCommitted })).resolves.toMatchObject({ changed: false });
    expect(onCommitted).not.toHaveBeenCalled();
    expect(finalize).not.toHaveBeenCalled();
    expect(enterpriseRepositorySources).not.toHaveBeenCalled();

    const tomorrow = now + 86_400_000;
    await expect(synthesizeEnterpriseDemo(indexedDB, { now: tomorrow })).resolves.toMatchObject({ changed: true });
    expect(finalize).toHaveBeenCalledTimes(1);
    expect(enterpriseRepositorySources).toHaveBeenNthCalledWith(1, 0, 1_000, tomorrow);
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

  it('commits fleet-wide early runs and resumes after the last committed activity batch', async () => {
    const indexedDB = new IDBFactory();
    const now = Date.parse('2040-06-15T12:00:00Z');
    const controller = new AbortController();
    const ingest = coordinator.ingestCachedGhAwJsonl;
    const batches = vi.spyOn(coordinator, 'ingestCachedGhAwJsonl').mockImplementationOnce(async (...args) => {
      const result = await ingest(...args);
      controller.abort();
      return result;
    });
    try {
      await expect(synthesizeEnterpriseDemo(indexedDB, { now, signal: controller.signal })).rejects.toThrow(/abort/i);
      const runs = await readCollection(indexedDB, 'runs');
      expect(runs).toHaveLength(3);
      expect(new Set(runs.map(run => run.repository)).size).toBe(3);
      expect((await readCollection(indexedDB, 'repositories')).length).toBe(2_101);
      expect((await readCollection(indexedDB, 'workflows')).length).toBe(12);
      expect((await readTransactions(indexedDB)).find(row => row.id === 'synthetic-demo:population'))
        .toMatchObject({ complete: false, activityBatches: 1, installedRepositories: 3 });
      batches.mockClear();
      await synthesizeEnterpriseDemo(indexedDB, { now });
      expect(batches).toHaveBeenCalledTimes(2);
      expect(batches.mock.calls[0][2]?.payloadScope).toBe('synthetic-demo:population/activity/1');
      const clean = new IDBFactory();
      await synthesizeEnterpriseDemo(clean, { now });
      for (const collection of /** @type {const} */ (['runs', 'audits', 'tools', 'issues', 'operationalValues'])) {
        expect(await readCollection(indexedDB, collection)).toEqual(await readCollection(clean, collection));
      }
    } finally {
      batches.mockRestore();
    }
  }, 60_000);
});
