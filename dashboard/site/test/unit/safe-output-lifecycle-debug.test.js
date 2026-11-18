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

afterEach(() => {
  vi.doUnmock('../../src/debug.js');
  vi.resetModules();
});

describe('safe-output-lifecycle debug logging', () => {
  it('is disabled by default when the debug query is absent', async () => {
    const { evaluateSafeOutputDelivery, mergeOutputLifecycle, output } = await loadLifecycleWithDebug('');

    evaluateSafeOutputDelivery('custom_output', '2026-11-15T10:00:00.000Z');
    expect(() => mergeOutputLifecycle({ id: 'a', runId: '1' }, { id: 'a', runId: '2' })).toThrow(TypeError);

    expect(output.debug).not.toHaveBeenCalled();
  });

  it('does not log for other categories', async () => {
    const { evaluateSafeOutputDelivery, output } = await loadLifecycleWithDebug('?debug=auth');

    evaluateSafeOutputDelivery('custom_output', '2026-11-15T10:00:00.000Z');

    expect(output.debug).not.toHaveBeenCalled();
  });

  it('logs unsupported safe-output types under its predictable category without the type name', async () => {
    const { evaluateSafeOutputDelivery, output } = await loadLifecycleWithDebug('?debug=safe-output-lifecycle');

    expect(evaluateSafeOutputDelivery('secret-custom-type', '2026-11-15T10:00:00.000Z'))
      .toMatchObject({ state: 'unknown' });
    evaluateSafeOutputDelivery('add_comment', '2026-11-15T10:00:00.000Z');

    expect(output.debug).toHaveBeenCalledTimes(1);
    expect(output.debug).toHaveBeenCalledWith('[cao:safe-output-lifecycle]', {
      event: 'delivery-fail-closed', state: 'unknown', signal: 'unsupported_safe_output'
    });
    expect(JSON.stringify(output.debug.mock.calls)).not.toContain('secret-custom-type');
  });

  it('logs only the conflicting field name, never identifiers or values, and still throws', async () => {
    const { mergeOutputLifecycle, output } = await loadLifecycleWithDebug('?debug=*');

    expect(() => mergeOutputLifecycle(
      { id: 'output-lifecycle:secret-id', runId: 'run-secret-1' },
      { id: 'output-lifecycle:secret-id', runId: 'run-secret-2' }
    )).toThrow('output lifecycle output-lifecycle:secret-id has conflicting runId');

    expect(output.debug).toHaveBeenCalledTimes(1);
    expect(output.debug).toHaveBeenCalledWith('[cao:safe-output-lifecycle]', {
      event: 'merge-conflict', conflict: 'runId'
    });
    expect(JSON.stringify(output.debug.mock.calls)).not.toMatch(/secret/);
  });

  it('supports excluding the category', async () => {
    const { evaluateSafeOutputDelivery, output } = await loadLifecycleWithDebug('?debug=*,-safe-output-lifecycle');

    evaluateSafeOutputDelivery('custom_output', '2026-11-15T10:00:00.000Z');

    expect(output.debug).not.toHaveBeenCalled();
  });
});
