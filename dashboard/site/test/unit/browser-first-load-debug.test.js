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

  it('logs preparation start and finish with scalar counts only', async () => {
    const debugFn = vi.fn();
    mockDebugModule(debugFn, '?debug=browser-first-load');
    const { browserFirstLoad, resetPreparationDiagnostics, recordPreparationEvent, finishPreparationDiagnostics } = await import('../../src/browser-first-load.js');

    browserFirstLoad.set({ status: 'loading', dismissed: false, ingestion: { id: 'secret-id', message: 'secret message' } });
    resetPreparationDiagnostics();
    recordPreparationEvent('step', { detail: 'secret-detail' });
    finishPreparationDiagnostics();

    expect(debugFn).toHaveBeenNthCalledWith(1, '[cao:browser-first-load]', { event: 'preparation-started' });
    expect(debugFn).toHaveBeenNthCalledWith(2, '[cao:browser-first-load]', {
      event: 'preparation-finished', wasRecording: true, entryCount: 1, status: 'loading'
    });
    expect(JSON.stringify(debugFn.mock.calls)).not.toContain('secret');
  });

  it('emits no preparation logs when disabled or for other categories', async () => {
    const debugFn = vi.fn();
    mockDebugModule(debugFn, '?debug=render:*');
    const { resetPreparationDiagnostics, finishPreparationDiagnostics } = await import('../../src/browser-first-load.js');

    resetPreparationDiagnostics();
    finishPreparationDiagnostics();

    expect(debugFn).not.toHaveBeenCalled();
  });
});
