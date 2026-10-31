// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';

afterEach(() => {
  vi.doUnmock('../../src/debug.js');
  vi.resetModules();
});

/**
 * @param {ReturnType<typeof vi.fn>} debugFn
 * @param {string} search
 */
function mockDebugModule(debugFn, search) {
  const output = /** @type {Pick<Console, 'debug'>} */ ({ debug: debugFn });
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
}

describe('browser first-load debug logging', () => {
  it('is disabled by default (no debug output) when the debug query is absent', async () => {
    const debugFn = vi.fn();
    mockDebugModule(debugFn, '');
    const { browserFirstLoad, showBrowserFirstLoad } = await import('../../src/browser-first-load.js');

    browserFirstLoad.set({ status: 'failed', dismissed: true });
    showBrowserFirstLoad();

    expect(debugFn).not.toHaveBeenCalled();
  });

  it('logs a reopen-requested event with the previous status under its predictable category', async () => {
    const debugFn = vi.fn();
    mockDebugModule(debugFn, '?debug=browser-first-load');
    const { browserFirstLoad, showBrowserFirstLoad } = await import('../../src/browser-first-load.js');

    browserFirstLoad.set({ status: 'failed', dismissed: true });
    showBrowserFirstLoad();

    expect(debugFn).toHaveBeenCalledWith('[cao:browser-first-load]', { event: 'reopen-requested', previousStatus: 'failed' });
  });

  it('is enabled by the wildcard debug query', async () => {
    const debugFn = vi.fn();
    mockDebugModule(debugFn, '?debug=*');
    const { browserFirstLoad, showBrowserFirstLoad } = await import('../../src/browser-first-load.js');

    browserFirstLoad.set({ status: 'loading', dismissed: true });
    showBrowserFirstLoad();

    expect(debugFn).toHaveBeenCalledWith('[cao:browser-first-load]', { event: 'reopen-requested', previousStatus: 'loading' });
  });

  it('never logs record content, only scalar metadata', async () => {
    const debugFn = vi.fn();
    mockDebugModule(debugFn, '?debug=browser-first-load');
    const { browserFirstLoad, showBrowserFirstLoad } = await import('../../src/browser-first-load.js');

    browserFirstLoad.set({ status: 'loading', dismissed: false, completed: 3, total: 10 });
    showBrowserFirstLoad();

    for (const call of debugFn.mock.calls) {
      const [, payload] = call;
      for (const value of Object.values(payload)) {
        expect(value === null || typeof value === 'string' || typeof value === 'number' || typeof value === 'boolean').toBe(true);
      }
    }
  });

  it('still applies the state update regardless of debug status', async () => {
    const debugFn = vi.fn();
    mockDebugModule(debugFn, '');
    const { browserFirstLoad, showBrowserFirstLoad } = await import('../../src/browser-first-load.js');

    browserFirstLoad.set({ status: 'failed', dismissed: true });
    showBrowserFirstLoad();

    expect(browserFirstLoad.get()).toEqual({ status: 'failed', dismissed: false });
  });
});

describe('browser first-load preparation debug logging', () => {
  it('emits nothing by default', async () => {
    const debugFn = vi.fn();
    mockDebugModule(debugFn, '');
    const mod = await import('../../src/browser-first-load.js');

    mod.resetPreparationDiagnostics();
    mod.recordPreparationStorage({ persisted: true, usage: 1, quota: 2 });
    mod.finishPreparationDiagnostics();

    expect(debugFn).not.toHaveBeenCalled();
  });

  it('logs lifecycle transitions with scalar metadata only', async () => {
    const debugFn = vi.fn();
    mockDebugModule(debugFn, '?debug=browser-first-load');
    const mod = await import('../../src/browser-first-load.js');

    mod.resetPreparationDiagnostics();
    mod.recordPreparationEvent('secret-event', { token: 'sekrit-value' });
    mod.recordPreparationStorage({ persisted: false, usage: 123456, quota: 999999, errorName: 'QuotaExceededError' });
    mod.finishPreparationDiagnostics();

    expect(debugFn.mock.calls).toEqual([
      ['[cao:browser-first-load]', { event: 'preparation-started' }],
      ['[cao:browser-first-load]', { event: 'storage-recorded', persisted: false, errorName: 'QuotaExceededError' }],
      ['[cao:browser-first-load]', { event: 'preparation-finished', recordedEvents: 2 }]
    ]);
    expect(JSON.stringify(debugFn.mock.calls)).not.toMatch(/sekrit|secret-event|123456/);
  });

  it('is excluded by a category exclusion', async () => {
    const debugFn = vi.fn();
    mockDebugModule(debugFn, '?debug=*,-browser-first-load');
    const mod = await import('../../src/browser-first-load.js');

    mod.resetPreparationDiagnostics();
    expect(debugFn).not.toHaveBeenCalled();
  });
});
