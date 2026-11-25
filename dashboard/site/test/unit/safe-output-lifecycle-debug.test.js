import { afterEach, describe, expect, it, vi } from 'vitest';

/** @param {string} search */
async function load(search) {
  const output = { debug: vi.fn() };
  vi.doMock('../../src/debug.js', async () => {
    const actual = /** @type {typeof import('../../src/debug.js')} */ (await vi.importActual('../../src/debug.js'));
    return { ...actual, createDebug: (/** @type {string} */ c) => actual.createDebug(c, { search: () => search, output }) };
  });
  vi.resetModules();
  const module = await import('../../src/data/model/safe-output-lifecycle.js');
  return { ...module, output };
}

const deliveredAt = '2026-11-15T10:00:00.000Z';
const base = {
  id: 'output-lifecycle:secret-id', repositoryId: 'r', workflowId: 'w', runId: '1', safeOutputType: 'add_comment',
  deliveryState: 'delivered', deliverySignal: 'safe_output.result', ruleVersion: 1,
  targetUrl: 'https://github.com/octo/api/issues/1?token=secret-token'
};

afterEach(() => {
  vi.doUnmock('../../src/debug.js');
  vi.resetModules();
});

describe('safe-output-lifecycle debug logging', () => {
  it('is silent by default', async () => {
    const { evaluateSafeOutputDelivery, mergeOutputLifecycle, output } = await load('');
    evaluateSafeOutputDelivery('custom_output', deliveredAt);
    expect(() => mergeOutputLifecycle(base, { ...base, runId: '2' })).toThrow(TypeError);
    expect(output.debug).not.toHaveBeenCalled();
  });

  it('logs unsupported safe-output types under its predictable category without the type name', async () => {
    const { evaluateSafeOutputDelivery, output } = await load('?debug=safe-output-lifecycle');
    expect(evaluateSafeOutputDelivery('custom_secret_output', deliveredAt)).toMatchObject({ state: 'unknown' });
    expect(output.debug).toHaveBeenCalledTimes(1);
    expect(output.debug).toHaveBeenCalledWith('[cao:safe-output-lifecycle]',
      { event: 'delivery-unknown', signal: 'unsupported_safe_output' });
    expect(JSON.stringify(output.debug.mock.calls)).not.toContain('custom_secret_output');
  });

  it('does not log supported delivery evaluations', async () => {
    const { evaluateSafeOutputDelivery, output } = await load('?debug=safe-output-lifecycle');
    evaluateSafeOutputDelivery('add_comment', deliveredAt);
    expect(output.debug).not.toHaveBeenCalled();
  });

  it('logs merge conflicts with the conflicting field only', async () => {
    const { mergeOutputLifecycle, output } = await load('?debug=safe-output-lifecycle');
    expect(() => mergeOutputLifecycle(base, { ...base, runId: '2' })).toThrow('conflicting runId');
    expect(() => mergeOutputLifecycle(
      { ...base, adoptionSource: 'github-api', adoptionObservedAt: deliveredAt, adoptionState: 'accepted' },
      { ...base, adoptionSource: 'github-api', adoptionObservedAt: deliveredAt, adoptionState: 'rejected' }
    )).toThrow('conflicting adoption evidence');
    expect(output.debug.mock.calls).toEqual([
      ['[cao:safe-output-lifecycle]', { event: 'merge-conflict', evidence: 'runId' }],
      ['[cao:safe-output-lifecycle]', { event: 'merge-conflict', evidence: 'adoption evidence' }]
    ]);
    const logged = JSON.stringify(output.debug.mock.calls);
    expect(logged).not.toContain('secret');
    expect(logged).not.toContain('token');
  });

  it('is excluded by a negated category pattern', async () => {
    const { evaluateSafeOutputDelivery, output } = await load('?debug=*,-safe-output-lifecycle');
    evaluateSafeOutputDelivery('custom_output', deliveredAt);
    expect(output.debug).not.toHaveBeenCalled();
  });
});
