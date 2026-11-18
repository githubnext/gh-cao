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

afterEach(() => {
  vi.doUnmock('../../src/debug.js');
  vi.resetModules();
});

const deliveredAt = '2026-11-15T10:00:00.000Z';

describe('safe-output-lifecycle debug logging', () => {
  it('is disabled by default', async () => {
    const { evaluateSafeOutputDelivery, mergeOutputLifecycle, output } = await loadLifecycleWithDebug('');

    evaluateSafeOutputDelivery('custom_output', deliveredAt);
    expect(() => mergeOutputLifecycle({ id: 'a', runId: '1' }, { id: 'a', runId: '2' })).toThrow();

    expect(output.debug).not.toHaveBeenCalled();
  });

  it('does not log for supported output types or non-conflicting merges', async () => {
    const { evaluateSafeOutputDelivery, mergeOutputLifecycle, output } =
      await loadLifecycleWithDebug('?debug=safe-output-lifecycle');

    evaluateSafeOutputDelivery('add_comment', deliveredAt);
    mergeOutputLifecycle({ id: 'a', runId: '1' }, { id: 'a', runId: '1' });

    expect(output.debug).not.toHaveBeenCalled();
  });

  it('logs unsupported output types under its predictable category', async () => {
    const { evaluateSafeOutputDelivery, SAFE_OUTPUT_ADOPTION_RULE_VERSION, output } =
      await loadLifecycleWithDebug('?debug=safe-output-lifecycle');

    evaluateSafeOutputDelivery('secret-custom-type', deliveredAt);

    expect(output.debug).toHaveBeenCalledTimes(1);
    expect(output.debug).toHaveBeenCalledWith('[cao:safe-output-lifecycle]', {
      operation: 'evaluate-delivery',
      status: 'unsupported-output-type',
      ruleVersion: SAFE_OUTPUT_ADOPTION_RULE_VERSION
    });
    expect(JSON.stringify(output.debug.mock.calls)).not.toContain('secret-custom-type');
  });

  it('logs the conflicting field name without identifiers or values', async () => {
    const { mergeOutputLifecycle, output } = await loadLifecycleWithDebug('?debug=safe-output-*');

    expect(() => mergeOutputLifecycle(
      { id: 'output-lifecycle:private-id', runId: 'run-secret-1', targetUrl: 'https://example.test/a?token=x' },
      { id: 'output-lifecycle:private-id', runId: 'run-secret-2', targetUrl: 'https://example.test/b?token=y' }
    )).toThrow(/conflicting runId/);
    expect(output.debug).toHaveBeenCalledWith('[cao:safe-output-lifecycle]', {
      operation: 'merge-output-lifecycle', status: 'conflict', kind: 'identity', field: 'runId'
    });

    output.debug.mockClear();
    expect(() => mergeOutputLifecycle(
      { id: 'a', adoptionObservedAt: deliveredAt, adoptionSource: 'github-api', adoptionState: 'accepted' },
      { id: 'a', adoptionObservedAt: deliveredAt, adoptionSource: 'github-api', adoptionState: 'rejected' }
    )).toThrow(/conflicting adoption evidence/);
    expect(output.debug).toHaveBeenCalledWith('[cao:safe-output-lifecycle]', {
      operation: 'merge-output-lifecycle', status: 'conflict', kind: 'evidence', field: 'adoption'
    });

    const logged = JSON.stringify(output.debug.mock.calls);
    for (const sensitive of ['private-id', 'run-secret', 'example.test', 'token=']) {
      expect(logged).not.toContain(sensitive);
    }
  });

  it('respects category exclusion', async () => {
    const { evaluateSafeOutputDelivery, output } =
      await loadLifecycleWithDebug('?debug=*,-safe-output-lifecycle');

    evaluateSafeOutputDelivery('custom_output', deliveredAt);

    expect(output.debug).not.toHaveBeenCalled();
  });
});
