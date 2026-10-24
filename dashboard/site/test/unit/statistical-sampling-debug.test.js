import { webcrypto } from 'node:crypto';
import { afterEach, expect, it, vi } from 'vitest';

/** @param {string} search */
async function load(search) {
  const output = { debug: vi.fn() };
  vi.doMock('../../src/debug.js', async () => {
    const actual = /** @type {typeof import('../../src/debug.js')} */ (await vi.importActual('../../src/debug.js'));
    return {
      ...actual,
      createDebug: (/** @type {string} */ category) => actual.createDebug(category, { search: () => search, output })
    };
  });
  vi.resetModules();
  vi.stubGlobal('crypto', webcrypto);
  return { output, ...await import('../../src/data/queries/statistical-sample.js') };
}

afterEach(() => {
  vi.doUnmock('../../src/debug.js');
  vi.resetModules();
  vi.unstubAllGlobals();
});

const rows = Array.from({ length: 130 }, (_, index) => ({
  id: `github:run:private/repository:${index}`,
  status: 'completed', conclusion: 'success', startedAt: '2026-10-22T00:00:00Z',
  payload: 'private-payload'
}));
const options = { watermark: '2026-10-23T00:00:00Z', capacity: 7, complete: true, seed: 'private-seed' };

it('keeps sampling diagnostics disabled by default', async () => {
  const { sampleStatisticalRuns, output } = await load('');
  await sampleStatisticalRuns(rows, options);
  await expect(sampleStatisticalRuns(rows, { ...options, watermark: '' })).rejects.toThrow();
  expect(output.debug).not.toHaveBeenCalled();
});

it('logs only safe summary counts and bounded hash concurrency', async () => {
  const { sampleStatisticalRuns, output } = await load('?debug=data:sampling');
  await sampleStatisticalRuns(rows, options);
  expect(output.debug).toHaveBeenCalledWith('[cao:data:sampling]', expect.objectContaining({
    event: 'statistical-sample', status: 'completed', stage: 'complete',
    inputCount: 130, populationCount: 130, selectedCount: 7, stratumCount: 1,
    hashBatches: 3, maxPendingHashes: 64, weightValid: true
  }));
  await expect(sampleStatisticalRuns(rows, { ...options, watermark: 'private-invalid-clock' })).rejects.toThrow();
  expect(output.debug).toHaveBeenLastCalledWith('[cao:data:sampling]', expect.objectContaining({
    status: 'failed', stage: 'validation', selectedCount: 0, weightValid: false
  }));
  await expect(sampleStatisticalRuns(rows, {
    ...options, checkpoint: () => { throw new Error('private-failure-message'); }
  })).rejects.toThrow('private-failure-message');
  expect(JSON.stringify(output.debug.mock.calls)).not.toMatch(/private|github:run|sha256:/);
});
