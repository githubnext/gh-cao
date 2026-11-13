// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';

const queue = vi.hoisted(() => ({
  available: vi.fn(), get: vi.fn(), attach: vi.fn(), cancel: vi.fn(), retry: vi.fn()
}));

/** @param {string} search */
async function load(search) {
  const output = { debug: vi.fn() };
  vi.doMock('../../src/data-processor.js', () => ({
    cliQueueAvailable: queue.available, requestCliQueue: queue.get,
    attachCliCommand: queue.attach, cancelCliCommand: queue.cancel, retryCliCommand: queue.retry
  }));
  vi.doMock('../../src/components/cli-terminal-runtime.js', () => ({
    mountTerminal: () => ({ write: () => {}, reset: () => {} })
  }));
  vi.doMock('../../src/debug.js', async () => {
    const actual = /** @type {typeof import('../../src/debug.js')} */ (await vi.importActual('../../src/debug.js'));
    return { ...actual, createDebug: (/** @type {string} */ category) => actual.createDebug(category, { search: () => search, output }) };
  });
  vi.resetModules();
  const { renderCommandTerminal } = await import('../../src/components/command-terminal.js');
  return { renderCommandTerminal, output };
}

const context = /** @type {import('../../src/components/ui-elements.js').ElementRenderContext} */ ({
  pageId: 'command', title: 'Output', sourceNames: [], sources: {}, contextDetails: [],
  headingTag: 'h3', routeParameters: { command: 'secret-command-id' }
});

afterEach(() => {
  document.body.replaceChildren();
  vi.doUnmock('../../src/debug.js');
  vi.doUnmock('../../src/data-processor.js');
  vi.doUnmock('../../src/components/cli-terminal-runtime.js');
  vi.resetModules();
  vi.clearAllMocks();
});

/** @param {string} search */
async function runFailedAttach(search) {
  queue.available.mockReturnValue(true);
  queue.get.mockResolvedValue({ id: 'secret-command-id', status: 'failed' });
  queue.attach.mockResolvedValue({ command: { id: 'secret-command-id', status: 'failed' }, result: { ok: false, error: 'SECRET stderr text' } });
  const loaded = await load(search);
  const root = loaded.renderCommandTerminal(context);
  document.body.append(root);
  await vi.waitFor(() => expect(queue.attach).toHaveBeenCalled());
  await vi.waitFor(() => expect(root.querySelector('output')?.textContent).toContain('failed'));
  return { ...loaded, root };
}

describe('command-terminal debug logging', () => {
  it('is disabled by default', async () => {
    const { output } = await runFailedAttach('');
    expect(output.debug).not.toHaveBeenCalled();
  });

  it('emits scalar metadata without ids or output text', async () => {
    const { output } = await runFailedAttach('?debug=command-terminal');
    expect(output.debug).toHaveBeenCalledWith('[cao:command-terminal]',
      { event: 'attach', outcome: 'complete', status: 'failed', failed: true });
    const serialized = JSON.stringify(output.debug.mock.calls);
    expect(serialized).not.toContain('secret-command-id');
    expect(serialized).not.toContain('SECRET');
  });

  it('logs sanitized error names without command output', async () => {
    queue.available.mockReturnValue(true);
    queue.get.mockResolvedValue({ id: 'secret-command-id', status: 'running' });
    queue.attach.mockRejectedValue(new TypeError('SECRET message'));
    const { renderCommandTerminal, output } = await load('?debug=command-*');
    const root = renderCommandTerminal(context);
    document.body.append(root);
    await vi.waitFor(() => expect(output.debug).toHaveBeenCalledWith('[cao:command-terminal]',
      { event: 'attach', outcome: 'error', error: 'TypeError' }));
    expect(root.querySelector('.cli-action-cancel')).toBeNull();
    expect(queue.cancel).not.toHaveBeenCalled();
    expect(JSON.stringify(output.debug.mock.calls)).not.toContain('SECRET');
  });

  it('logs retry failures as sanitized error names only', async () => {
    queue.available.mockReturnValue(true);
    queue.get.mockResolvedValue({ id: 'secret-command-id', status: 'failed' });
    queue.attach.mockResolvedValue({ command: { id: 'secret-command-id', status: 'failed' }, result: { ok: false } });
    queue.retry.mockRejectedValue(new TypeError('SECRET retry message'));
    const { renderCommandTerminal, output } = await load('?debug=command-terminal');
    const root = renderCommandTerminal(context);
    document.body.append(root);
    const retry = /** @type {HTMLButtonElement} */ (root.querySelector('.command-terminal-retry'));
    await vi.waitFor(() => expect(retry.hidden).toBe(false));
    retry.click();
    await vi.waitFor(() => expect(output.debug).toHaveBeenCalledWith('[cao:command-terminal]',
      { event: 'retry', outcome: 'error', error: 'TypeError' }));
    const serialized = JSON.stringify(output.debug.mock.calls);
    expect(serialized).not.toContain('secret-command-id');
    expect(serialized).not.toContain('SECRET');
  });

  it('does not log retry failures by default', async () => {
    queue.available.mockReturnValue(true);
    queue.get.mockResolvedValue({ id: 'secret-command-id', status: 'failed' });
    queue.attach.mockResolvedValue({ command: { id: 'secret-command-id', status: 'failed' }, result: { ok: false } });
    queue.retry.mockRejectedValue(new Error('Retry not authorized.'));
    const { renderCommandTerminal, output } = await load('');
    const root = renderCommandTerminal(context);
    document.body.append(root);
    const retry = /** @type {HTMLButtonElement} */ (root.querySelector('.command-terminal-retry'));
    await vi.waitFor(() => expect(retry.hidden).toBe(false));
    retry.click();
    await vi.waitFor(() => expect(queue.retry).toHaveBeenCalled());
    await vi.waitFor(() => expect(retry.disabled).toBe(false));
    expect(output.debug).not.toHaveBeenCalled();
  });

  it('stays off for excluded categories', async () => {
    const { output } = await runFailedAttach('?debug=1,-command-terminal');
    expect(output.debug.mock.calls.some(([category]) => category === '[cao:command-terminal]')).toBe(false);
  });
});
