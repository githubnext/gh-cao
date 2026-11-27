import { afterEach, describe, expect, it, vi } from 'vitest';

/** @param {string} search */
async function loadWithDebug(search) {
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
  const module = await import('../../src/data/model/safe-output-lifecycle.js');
  return { ...module, output };
}

const secretTitle = 'SECRET-TITLE https://example.test/private?token=abc';
const base = {
  id: 'output-lifecycle:one', repositoryId: 'repo', workflowId: 'workflow', runId: '1',
  safeOutputType: 'create_issue', deliveryState: 'delivered', deliverySignal: 'safe_output.result',
  ruleVersion: 'v1', title: secretTitle, targetUrl: 'https://example.test/private?token=abc'
};

afterEach(() => {
  vi.doUnmock('../../src/debug.js');
  vi.resetModules();
});

describe('safe-output-lifecycle debug logging', () => {
  it('is disabled by default and does not change the thrown error', async () => {
    const { mergeOutputLifecycle, output } = await loadWithDebug('');

    expect(() => mergeOutputLifecycle(base, { ...base, runId: '2' }))
      .toThrow('output lifecycle output-lifecycle:one has conflicting runId');
    expect(output.debug).not.toHaveBeenCalled();
  });

  it('logs only the conflict kind and field under its predictable category', async () => {
    const { mergeOutputLifecycle, output } = await loadWithDebug('?debug=safe-output-lifecycle');

    expect(() => mergeOutputLifecycle(base, { ...base, runId: '2' })).toThrow(TypeError);
    expect(() => mergeOutputLifecycle(base, { ...base, targetUrl: 'https://example.test/other' })).toThrow(TypeError);
    expect(() => mergeOutputLifecycle(
      { ...base, survivalState: 'observed', survivalObservedAt: '2026-11-15T12:00:00Z' },
      { ...base, survivalState: 'unknown', survivalObservedAt: '2026-11-15T12:00:00Z' }
    )).toThrow(TypeError);

    expect(output.debug.mock.calls).toEqual([
      ['[cao:safe-output-lifecycle]', { event: 'merge-conflict', kind: 'identity', field: 'runId' }],
      ['[cao:safe-output-lifecycle]', { event: 'merge-conflict', kind: 'target', field: 'targetUrl' }],
      ['[cao:safe-output-lifecycle]', { event: 'merge-conflict', kind: 'survival', field: 'survivalEvidence' }]
    ]);
    expect(JSON.stringify(output.debug.mock.calls)).not.toMatch(/SECRET|token|example\.test|output-lifecycle:one/);
  });

  it('does not log for non-conflicting merges or other categories', async () => {
    const enabled = await loadWithDebug('?debug=safe-output-lifecycle');
    enabled.mergeOutputLifecycle(base, { ...base });
    expect(enabled.output.debug).not.toHaveBeenCalled();

    const other = await loadWithDebug('?debug=auth');
    expect(() => other.mergeOutputLifecycle(base, { ...base, runId: '2' })).toThrow(TypeError);
    expect(other.output.debug).not.toHaveBeenCalled();
  });
});
