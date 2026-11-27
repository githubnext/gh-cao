import { afterEach, describe, expect, it, vi } from 'vitest';

/** @param {string} search */
async function loadSampleWithDebug(search) {
  const output = { debug: vi.fn() };
  vi.doMock('../../src/debug.js', async () => {
    const actual = /** @type {typeof import('../../src/debug.js')} */ (
      await vi.importActual('../../src/debug.js')
    );
    return {
      ...actual,
      createDebug: (/** @type {string} */ category) => actual.createDebug(category, { search: () => search, output })
    };
  });
  vi.resetModules();
  const module = await import('../../src/data/demo/sample.mjs');
  // Other imported modules share the mocked output, so isolate this module's category.
  const calls = () => output.debug.mock.calls.filter(([prefix]) => prefix === '[cao:sample]');
  return { ...module, calls };
}

afterEach(() => {
  vi.doUnmock('../../src/debug.js');
  vi.resetModules();
});

describe('synthetic sample debug logging', () => {
  const now = Date.parse('2040-06-15T12:00:00Z');
  const options = { repositories: 2, count: 2, runs: 40 };

  it('is disabled by default when the debug query is absent', async () => {
    const { enterpriseSample, calls } = await loadSampleWithDebug('');
    enterpriseSample(now, options);
    expect(calls()).toEqual([]);
  });

  it('does not log when another category is selected or the category is excluded', async () => {
    for (const search of ['?debug=synthesis', '?debug=*,-sample']) {
      const { enterpriseSample, calls } = await loadSampleWithDebug(search);
      enterpriseSample(now, options);
      expect(calls()).toEqual([]);
    }
  });

  it('logs activity plan and completion counts under the sample category', async () => {
    const { enterpriseSample, calls } = await loadSampleWithDebug('?debug=sample');
    const sample = enterpriseSample(now, options);
    expect(calls()).toEqual([
      ['[cao:sample]', { event: 'activity-plan', repositories: 2, covered: expect.any(Number), runs: sample.runs }],
      ['[cao:sample]', {
        event: 'sample-generated', repositories: 2, runs: sample.runs,
        records: sample.records.length, includeHistory: true
      }]
    ]);
  });

  it('logs only scalar metadata without repository names or record contents', async () => {
    const { enterpriseSample, calls } = await loadSampleWithDebug('?debug=sample');
    const sample = enterpriseSample(now, options);
    const logged = JSON.stringify(calls());
    const repositories = new Set(sample.records.filter(record => record.kind === 'run').map(record => record.run.repository));
    expect(repositories.size).toBeGreaterThan(0);
    for (const repository of repositories) expect(logged).not.toContain(repository);
    expect(logged).not.toContain('contoso');
    for (const [, metadata] of calls()) {
      for (const value of Object.values(metadata)) expect(['string', 'number', 'boolean']).toContain(typeof value);
    }
  });

  it('skips the activity plan when history is not requested', async () => {
    const { enterpriseSample, calls } = await loadSampleWithDebug('?debug=sample');
    enterpriseSample(now, { ...options, includeHistory: false });
    expect(calls()).toEqual([['[cao:sample]', {
      event: 'sample-generated', repositories: 2, runs: 0, records: 0, includeHistory: false
    }]]);
  });
});
