import { afterEach, describe, expect, it, vi } from 'vitest';

const definition = /** @type {import('../../src/dialog-selector.js').DialogSelector} */ ({
  label: 'Repository', 'debounce-ms': 300,
  query: { command: 'gh search repos', fields: ['fullName'], limit: 20 },
  mapping: { value: 'fullName', label: 'fullName', target: 'repository' }
});

/**
 * @param {string} debug
 * @param {ReturnType<typeof vi.fn>} fetchResult
 */
async function search(debug, fetchResult) {
  vi.resetModules();
  vi.useFakeTimers();
  history.replaceState(null, '', debug ? `/?debug=${debug}` : '/');
  const output = vi.spyOn(console, 'debug').mockImplementation(() => {});
  vi.stubGlobal('fetch', fetchResult);
  const { createDialogSelector } = await import('../../src/components/dialog-selector.js');
  const lifetime = new AbortController();
  const control = createDialogSelector('add', definition, { signal: lifetime.signal, select: () => {} });
  const input = /** @type {HTMLInputElement} */ (control.root.querySelector('input'));
  input.value = 'octo/secret-term';
  input.dispatchEvent(new Event('input'));
  await vi.advanceTimersByTimeAsync(400);
  lifetime.abort();
  return output;
}

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  vi.useRealTimers();
  history.replaceState(null, '', '/');
});

describe('dialog selector debug logging', () => {
  it('is silent by default and when another category is selected', async () => {
    const ok = vi.fn().mockResolvedValue({ ok: true, json: async () => ({ options: [] }) });
    expect((await search('', ok)).mock.calls).toEqual([]);
    expect((await search('data', ok)).mock.calls).toEqual([]);
  });

  it('logs scalar metadata for the predictable category without search text', async () => {
    const ok = vi.fn().mockResolvedValue({ ok: true, json: async () => ({ options: [{ value: 'o/r', label: 'o/r', description: 'private' }] }) });
    const output = await search('components:dialog-selector', ok);
    expect(output.mock.calls).toEqual([
      ['[cao:components:dialog-selector]', { event: 'search.start', textLength: 16 }],
      ['[cao:components:dialog-selector]', { event: 'search.result', status: 'ok', count: 1, limit: 20 }]
    ]);
    expect(JSON.stringify(output.mock.calls)).not.toMatch(/secret-term|private|o\/r/);
  });

  it('logs only a sanitized error name on failure', async () => {
    const failed = vi.fn().mockResolvedValue({ ok: false, json: async () => ({ error: 'token abc123' }) });
    const output = await search('1', failed);
    const own = output.mock.calls.filter(([prefix]) => prefix === '[cao:components:dialog-selector]');
    expect(own[1]).toEqual(['[cao:components:dialog-selector]', { event: 'search.result', status: 'error', error: 'Error' }]);
    expect(JSON.stringify(output.mock.calls)).not.toContain('abc123');
  });
});
