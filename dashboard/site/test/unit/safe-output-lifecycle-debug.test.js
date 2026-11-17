// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';

/** @param {string} search */
async function loadLifecycle(search) {
  vi.resetModules();
  window.history.replaceState(null, '', `/${search}`);
  const debug = vi.spyOn(console, 'debug').mockImplementation(() => {});
  const lifecycle = await import('../../src/data/model/safe-output-lifecycle.js');
  return { debug, ...lifecycle };
}

const base = {
  id: 'output-lifecycle:one',
  runId: 'run-private-1',
  safeOutputType: 'add_comment',
  targetUrl: 'https://github.com/private-org/private-repo/issues/1',
  adoptionSource: 'delivery-rule',
  adoptionObservedAt: '2026-11-15T10:00:00.000Z'
};

afterEach(() => {
  window.history.replaceState(null, '', '/');
  vi.restoreAllMocks();
});

describe('safe-output lifecycle debug logging', () => {
  it('is silent by default and still throws the same error', async () => {
    const { debug, mergeOutputLifecycle } = await loadLifecycle('');
    expect(() => mergeOutputLifecycle(base, { ...base, runId: 'other' }))
      .toThrow('output lifecycle output-lifecycle:one has conflicting runId');
    expect(debug).not.toHaveBeenCalled();
  });

  it('logs only the conflicting subject under safe-output-lifecycle', async () => {
    const { debug, mergeOutputLifecycle } = await loadLifecycle('?debug=safe-output-lifecycle');
    expect(() => mergeOutputLifecycle(base, { ...base, targetUrl: 'https://github.com/other/other/issues/2' }))
      .toThrow('has conflicting targetUrl');
    expect(() => mergeOutputLifecycle(
      { ...base, adoptionState: 'accepted' },
      { ...base, adoptionState: 'pending' }
    )).toThrow('has conflicting adoption evidence');
    expect(debug.mock.calls).toEqual([
      ['[cao:safe-output-lifecycle]', { event: 'merge-conflict', subject: 'targetUrl' }],
      ['[cao:safe-output-lifecycle]', { event: 'merge-conflict', subject: 'adoption evidence' }]
    ]);
    const logged = JSON.stringify(debug.mock.calls);
    expect(logged).not.toContain('private');
    expect(logged).not.toContain('github.com');
  });

  it('does not log successful merges and honors negated filters', async () => {
    const enabled = await loadLifecycle('?debug=safe-output-lifecycle');
    expect(enabled.mergeOutputLifecycle(base, { ...base })).toMatchObject({ id: base.id });
    expect(enabled.debug).not.toHaveBeenCalled();
    const excluded = await loadLifecycle('?debug=*,-safe-output-lifecycle');
    expect(() => excluded.mergeOutputLifecycle(base, { ...base, runId: 'other' })).toThrow();
    expect(excluded.debug).not.toHaveBeenCalled();
  });
});
