import { afterEach, describe, expect, it, vi } from 'vitest';

const deliveredAt = '2026-11-15T10:00:00.000Z';
const secretUrl = 'https://example.invalid/private?token=SECRET';

afterEach(() => {
  vi.doUnmock('../../src/debug.js');
  vi.resetModules();
});

/** @param {string} search */
async function loadLifecycle(search) {
  const output = { debug: vi.fn() };
  /** @type {string[]} */
  const categories = [];
  vi.doMock('../../src/debug.js', async () => {
    const actual = /** @type {typeof import('../../src/debug.js')} */ (
      await vi.importActual('../../src/debug.js')
    );
    return {
      ...actual,
      createDebug: (/** @type {string} */ category) => {
        categories.push(category);
        return actual.createDebug(category, { search: () => search, output });
      }
    };
  });
  vi.resetModules();
  const lifecycle = await import('../../src/data/model/safe-output-lifecycle.js');
  return { lifecycle, output, categories };
}

/** @param {Record<string, unknown>} [overrides] */
const lifecycleRecord = (overrides = {}) => ({
  id: 'output-lifecycle:one',
  repositoryId: 'repo',
  workflowId: 'workflow',
  runId: '1',
  safeOutputType: 'create_issue',
  deliveryState: 'delivered',
  deliverySignal: 'safe_output.result',
  ruleVersion: 'v1',
  targetUrl: secretUrl,
  title: 'private title',
  ...overrides
});

describe('safe-output lifecycle debug logging', () => {
  it('uses the filename-derived category and is silent by default', async () => {
    const { lifecycle, output, categories } = await loadLifecycle('');

    lifecycle.evaluateSafeOutputDelivery('custom_output', deliveredAt);
    expect(() => lifecycle.mergeOutputLifecycle(
      lifecycleRecord(), lifecycleRecord({ runId: '2' })
    )).toThrow('conflicting runId');

    expect(categories).toContain('safe-output-lifecycle');
    expect(output.debug).not.toHaveBeenCalled();
  });

  it('logs unresolved deliveries only when the category is selected', async () => {
    const { lifecycle, output } = await loadLifecycle('?debug=safe-output-lifecycle');

    lifecycle.evaluateSafeOutputDelivery('add_comment', deliveredAt);
    expect(output.debug).not.toHaveBeenCalled();

    expect(lifecycle.evaluateSafeOutputDelivery('custom_output', deliveredAt)).toMatchObject({
      state: 'unknown', signal: 'unsupported_safe_output'
    });
    expect(output.debug).toHaveBeenCalledWith('[cao:safe-output-lifecycle]', {
      event: 'delivery-unresolved', signal: 'unsupported_safe_output'
    });
  });

  it('logs conflicts without sensitive values and preserves the thrown error', async () => {
    const { lifecycle, output } = await loadLifecycle('?debug=safe-output-*');

    expect(() => lifecycle.mergeOutputLifecycle(
      lifecycleRecord({ targetUrl: secretUrl }),
      lifecycleRecord({ targetUrl: 'https://example.invalid/other?token=OTHER' })
    )).toThrow('output lifecycle output-lifecycle:one has conflicting targetUrl');

    expect(output.debug).toHaveBeenCalledTimes(1);
    expect(output.debug).toHaveBeenCalledWith('[cao:safe-output-lifecycle]', {
      event: 'lifecycle-conflict', field: 'targetUrl'
    });
    const logged = JSON.stringify(output.debug.mock.calls);
    expect(logged).not.toMatch(/SECRET|OTHER|private title|example\.invalid/);
  });

  it('honors exclusions', async () => {
    const { lifecycle, output } = await loadLifecycle('?debug=1,-safe-output-lifecycle');

    lifecycle.evaluateSafeOutputDelivery('custom_output', deliveredAt);

    expect(output.debug).not.toHaveBeenCalled();
  });
});
