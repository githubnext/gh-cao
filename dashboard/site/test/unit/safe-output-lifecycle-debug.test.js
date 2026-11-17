// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';

/** @param {string} search */
async function loadLifecycleWithDebug(search) {
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

const base = {
  id: 'output-lifecycle:secret-id',
  repositoryId: 'octo/secret-repo',
  workflowId: 'octo/secret-repo/.github/workflows/secret.yml',
  runId: '42',
  safeOutputType: 'create_issue',
  deliveryState: 'delivered',
  deliverySignal: 'safe_output.result',
  ruleVersion: 1,
  targetUrl: 'https://github.com/octo/secret-repo/issues/1?token=hunter2'
};

afterEach(() => {
  vi.doUnmock('../../src/debug.js');
  vi.resetModules();
});

describe('safe-output-lifecycle debug logging', () => {
  it('is disabled by default when the debug query is absent', async () => {
    const { evaluateSafeOutputDelivery, mergeOutputLifecycle, output } = await loadLifecycleWithDebug('');

    evaluateSafeOutputDelivery('custom_output', '2026-11-15T10:00:00.000Z');
    expect(() => mergeOutputLifecycle(base, { ...base, runId: '43' })).toThrow(TypeError);

    expect(output.debug).not.toHaveBeenCalled();
  });

  it('does not log under an unrelated category', async () => {
    const { evaluateSafeOutputDelivery, output } = await loadLifecycleWithDebug('?debug=schema');

    evaluateSafeOutputDelivery('custom_output', '2026-11-15T10:00:00.000Z');

    expect(output.debug).not.toHaveBeenCalled();
  });

  it('logs unsupported types and conflicts under its predictable category', async () => {
    const { evaluateSafeOutputDelivery, mergeOutputLifecycle, output } = await loadLifecycleWithDebug(
      '?debug=safe-output-lifecycle'
    );

    evaluateSafeOutputDelivery('custom_output', '2026-11-15T10:00:00.000Z');
    expect(output.debug).toHaveBeenLastCalledWith(
      '[cao:safe-output-lifecycle]',
      { operation: 'evaluate-delivery', status: 'unsupported-type' }
    );

    expect(() => mergeOutputLifecycle(base, { ...base, runId: '43' })).toThrow('conflicting runId');
    expect(output.debug).toHaveBeenLastCalledWith(
      '[cao:safe-output-lifecycle]',
      { operation: 'merge', status: 'conflict', field: 'runId' }
    );

    expect(() => mergeOutputLifecycle(
      { ...base, adoptionObservedAt: '2026-11-15T10:00:00Z', adoptionState: 'pending' },
      { ...base, adoptionObservedAt: '2026-11-15T10:00:00Z', adoptionState: 'accepted' }
    )).toThrow('conflicting adoption evidence');
    expect(output.debug).toHaveBeenLastCalledWith(
      '[cao:safe-output-lifecycle]',
      { operation: 'merge', status: 'conflict', field: 'adoption' }
    );
  });

  it('logs only authority levels when a stronger adoption fact is retained', async () => {
    const { mergeOutputLifecycle, output } = await loadLifecycleWithDebug('?debug=safe-output-*');

    const merged = mergeOutputLifecycle(
      { ...base, adoptionSource: 'github-api', adoptionState: 'accepted', adoptionObservedAt: '2026-11-15T10:00:00Z' },
      { ...base, adoptionSource: 'delivery-rule', adoptionState: 'pending', adoptionObservedAt: '2026-11-15T11:00:00Z' }
    );

    expect(merged.adoptionState).toBe('accepted');
    expect(output.debug).toHaveBeenCalledTimes(1);
    expect(output.debug).toHaveBeenCalledWith(
      '[cao:safe-output-lifecycle]',
      { operation: 'merge', status: 'adoption-retained', previousAuthority: 3, incomingAuthority: 1 }
    );
  });

  it('never logs identifiers, URLs, or error messages', async () => {
    const { evaluateSafeOutputDelivery, mergeOutputLifecycle, output } = await loadLifecycleWithDebug('?debug=1');

    evaluateSafeOutputDelivery('secret_type_hunter2', '2026-11-15T10:00:00.000Z');
    expect(() => mergeOutputLifecycle(base, { ...base, targetUrl: 'https://example.com/other?token=abc' })).toThrow();

    const logged = JSON.stringify(output.debug.mock.calls);
    expect(output.debug).toHaveBeenCalled();
    for (const secret of ['secret', 'hunter2', 'token', 'octo', 'https://', 'conflicting']) {
      expect(logged).not.toContain(secret);
    }
  });
});
