// @vitest-environment jsdom
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
  const module = await import('../../src/browser-storage.js');
  return { ...module, output };
}

afterEach(() => {
  vi.doUnmock('../../src/debug.js');
  vi.resetModules();
});

describe('browser-storage debug logging', () => {
  it('is disabled by default', async () => {
    const { inspectBrowserStorage, output } = await loadWithDebug('');
    await inspectBrowserStorage(undefined);
    expect(output.debug).not.toHaveBeenCalled();
  });

  it('logs scalar inspection metadata under the browser-storage category', async () => {
    const { inspectBrowserStorage, output } = await loadWithDebug('?debug=browser-storage');
    await inspectBrowserStorage(/** @type {StorageManager} */ ({
      persisted: async () => true,
      estimate: async () => ({ quota: 1000, usage: 200 })
    }));
    expect(output.debug).toHaveBeenCalledWith('[cao:browser-storage]', {
      operation: 'inspect', persisted: true, 'has-estimate': true, 'persist-api': false
    });
  });

  it('logs only the sanitized error name and never usage numbers or messages', async () => {
    const { inspectBrowserStorage, output } = await loadWithDebug('?debug=browser-storage');
    await inspectBrowserStorage(/** @type {StorageManager} */ (/** @type {unknown} */ ({
      persisted: async () => true,
      estimate: async () => { throw new Error('sensitive context'); }
    })));
    const [, payload] = output.debug.mock.calls[0];
    expect(payload.errorName).toBe('Error');
    expect(JSON.stringify(payload)).not.toContain('sensitive');
    for (const value of Object.values(payload)) {
      expect(['string', 'boolean']).toContain(typeof value);
    }
  });

  it('logs the persist request outcome only when persistence was requested', async () => {
    const { inspectBrowserStorage, output } = await loadWithDebug('?debug=browser-storage');
    const persist = vi.fn(async () => false);
    await inspectBrowserStorage(/** @type {StorageManager} */ (/** @type {unknown} */ ({
      persisted: async () => false,
      persist,
      estimate: async () => ({ quota: 1000, usage: 200 })
    })));
    expect(persist).toHaveBeenCalledOnce();
    expect(output.debug.mock.calls).toEqual([
      ['[cao:browser-storage]', { operation: 'persist-request', granted: false }],
      ['[cao:browser-storage]', {
        operation: 'inspect', persisted: false, 'has-estimate': true, 'persist-api': true
      }]
    ]);

    output.debug.mockClear();
    await inspectBrowserStorage(/** @type {StorageManager} */ (/** @type {unknown} */ ({
      persisted: async () => true,
      persist,
      estimate: async () => ({ quota: 1000, usage: 200 })
    })));
    expect(output.debug.mock.calls.map(([, payload]) => payload.operation)).toEqual(['inspect']);
  });

  it('does not log the persist request when debugging is disabled and preserves its result', async () => {
    const { inspectBrowserStorage, output } = await loadWithDebug('');
    const inspection = await inspectBrowserStorage(/** @type {StorageManager} */ (/** @type {unknown} */ ({
      persisted: async () => false,
      persist: async () => true,
      estimate: async () => ({ quota: 1000, usage: 200 })
    })));
    expect(inspection.persisted).toBe(true);
    expect(output.debug).not.toHaveBeenCalled();
  });

  it('is excluded by a negative pattern', async () => {
    const { inspectBrowserStorage, output } = await loadWithDebug('?debug=*,-browser-storage');
    await inspectBrowserStorage(undefined);
    expect(output.debug).not.toHaveBeenCalled();
  });
});
