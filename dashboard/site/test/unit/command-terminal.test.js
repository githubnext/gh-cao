import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
const queue = vi.hoisted(() => ({
  available: vi.fn(), get: vi.fn(), attach: vi.fn(), cancel: vi.fn(), retry: vi.fn()
}));
const terminalWrites = vi.hoisted(() => vi.fn());
vi.mock('../../src/data-processor.js', () => ({
  cliQueueAvailable: queue.available, requestCliQueue: queue.get,
  attachCliCommand: queue.attach, cancelCliCommand: queue.cancel, retryCliCommand: queue.retry
}));
vi.mock('../../src/components/cli-terminal-runtime.js', async () => {
  const { default: stripAnsi } = await import('strip-ansi');
  return {
    mountTerminal: (/** @type {HTMLElement} */ root) => ({
      plainText: stripAnsi,
      write: (/** @type {string} */ text, /** @type {string} */ stream) => {
        terminalWrites(text, stream);
        root.textContent += text;
      },
      reset: () => { root.textContent = ''; }
    })
  };
});
import { renderCommandTerminal } from '../../src/components/command-terminal.js';
import { shellStyles } from '../../src/styles-shell.js';

const context = /** @type {import('../../src/components/ui-elements.js').ElementRenderContext} */ ({
  pageId: 'command', title: 'Output', sourceNames: [], sources: {}, contextDetails: [],
  headingTag: 'h3', routeParameters: { command: 'persistent-1' }
});

beforeEach(() => {
  vi.resetAllMocks();
  Object.defineProperty(navigator, 'clipboard', {
    configurable: true, value: { writeText: vi.fn().mockResolvedValue(undefined) }
  });
  window.location.hash = 'page-command?command=persistent-1';
});

afterEach(async () => {
  document.body.replaceChildren();
  await Promise.resolve();
});

describe('persistent command terminal', () => {
  it('updates queued status from the worker stream before completion', async () => {
    queue.available.mockReturnValue(true);
    queue.get.mockResolvedValue({ id: 'persistent-1', status: 'queued', command: 'gh aw compile' });
    queue.attach.mockImplementation(() => new Promise(() => {}));
    const root = renderCommandTerminal(context);
    document.body.append(root);
    await vi.waitFor(() => expect(queue.attach).toHaveBeenCalledOnce());
    expect(root.querySelector('[role="status"]')?.textContent).toBe('persistent-1 · queued');
    const [, notify] = queue.attach.mock.calls[0];
    notify({ type: 'status', command: { id: 'persistent-1', status: 'running' } });
    await vi.waitFor(() => expect(root.querySelector('[role="status"]')?.textContent).toBe('persistent-1 · running'));
    expect(terminalWrites).not.toHaveBeenCalledWith(undefined, undefined);
  });

  it('renders an accessible full-page console and detaches without cancelling', async () => {
    queue.available.mockReturnValue(true);
    queue.get.mockResolvedValue({ id: 'persistent-1', status: 'running', command: 'gh aw compile --strict' });
    queue.attach.mockImplementation(() => new Promise(() => {}));
    const root = renderCommandTerminal(context);
    const allocation = vi.fn();
    root.addEventListener('dashboard-route-allocation', allocation);
    document.body.append(root);
    expect(root.getAttribute('aria-label')).toBe('Command console log');
    expect(root.querySelector('dialog')).toBeNull();
    const buttonLabels = [...root.querySelectorAll('button')].map((button) => button.textContent);
    expect(buttonLabels).not.toContain('Reconnect output');
    expect(buttonLabels).not.toContain('Back to Commands');
    await vi.waitFor(() => expect(queue.attach).toHaveBeenCalledOnce());
    expect(allocation.mock.calls.at(-1)?.[0].detail).toEqual({
      title: 'gh aw compile --strict', description: 'persistent-1 · running'
    });
    expect(root.querySelector('header')).toBeNull();
    const styles = document.createElement('style');
    styles.textContent = shellStyles;
    document.head.append(styles);
    try {
      const actions = root.querySelector('.command-terminal-actions');
      expect(actions && getComputedStyle(actions).right).toBe('24px');
      expect(actions && getComputedStyle(actions).bottom).toBe('24px');
      expect(root.querySelector('.command-terminal-copy .octicon-copy')).not.toBeNull();
      expect(root.querySelector('.cli-action-cancel')).toBeNull();
    } finally {
      styles.remove();
    }
    const signal = queue.attach.mock.calls[0][2];
    window.location.hash = 'page-commands';
    root.remove();
    await vi.waitFor(() => expect(signal.aborted).toBe(true));
    expect(window.location.hash).toBe('#page-commands');
    expect(signal.aborted).toBe(true);
    expect(queue.cancel).not.toHaveBeenCalled();
    expect(queue.retry).not.toHaveBeenCalled();
  });

  it('renders a missing-selection state without fetching a command', () => {
    queue.available.mockReturnValue(true);
    const root = renderCommandTerminal({ ...context, routeParameters: {} });
    document.body.append(root);
    expect(root.textContent).toContain('Select a command');
    expect(queue.get).not.toHaveBeenCalled();
  });

  it('starts live output with the exact executed command', async () => {
    queue.available.mockReturnValue(true);
    queue.get.mockResolvedValue({
      id: 'persistent-1', status: 'running',
      command: 'gh aw compile --strict --repo "octo/repo"'
    });
    queue.attach.mockImplementation((_id, output) => {
      expect(document.querySelector('.cli-action-terminal')?.textContent)
        .toBe('$ gh aw compile --strict --repo "octo/repo"\r\n');
      output({ data: '\x1b[3' });
      output({ data: '2mCompiling workflows...\x1b[0m\r\n' });
      return new Promise(() => {});
    });
    const root = renderCommandTerminal(context);
    document.body.append(root);
    await vi.waitFor(() => expect(root.querySelector('.cli-action-terminal')?.textContent)
      .toBe('$ gh aw compile --strict --repo "octo/repo"\r\n\x1b[32mCompiling workflows...\x1b[0m\r\n'));
  });

  it('shows the PR result after live completion without navigating away', async () => {
    const pullRequestUrl = 'https://ghe.example.test:8443/github/team/project/pull/42';
    queue.available.mockReturnValue(true);
    queue.get.mockResolvedValue({ id: 'persistent-1', status: 'running', command: 'gh aw compile --strict' });
    queue.attach.mockResolvedValue({
      command: { id: 'persistent-1', status: 'completed' },
      result: { ok: true, pullRequestUrl }
    });
    const root = renderCommandTerminal(context);
    document.body.append(root);
    const link = /** @type {HTMLAnchorElement} */ (root.querySelector('a'));
    expect(link.hidden).toBe(true);
    await vi.waitFor(() => expect(link.hidden).toBe(false));
    expect(link.textContent).toBe('Open pull request');
    expect(link.href).toBe(pullRequestUrl);
    expect(link.target).toBe('_blank');
    expect(link.rel).toBe('noopener noreferrer');
    expect(link.parentElement?.hidden).toBe(false);
    /** @type {HTMLButtonElement} */ (root.querySelector('.command-terminal-copy')).click();
    await vi.waitFor(() => expect(navigator.clipboard.writeText).toHaveBeenCalledExactlyOnceWith(pullRequestUrl));
    expect(window.location.hash).toBe('#page-command?command=persistent-1');
  });

  it('restores a persisted PR result when reopening a completed command', async () => {
    const pullRequestUrl = 'https://github.internal/team/project/pull/7';
    queue.available.mockReturnValue(true);
    const command = { id: 'persistent-1', status: 'completed', result: { ok: true, pullRequestUrl } };
    queue.get.mockResolvedValue(command);
    queue.attach.mockResolvedValue({ command, result: { ok: true } });
    const root = renderCommandTerminal(context);
    document.body.append(root);
    await vi.waitFor(() => expect(root.querySelector('a')?.getAttribute('href')).toBe(pullRequestUrl));
    expect(/** @type {HTMLAnchorElement} */ (root.querySelector('a')).hidden).toBe(false);
  });

  it('reattaches on reopening and ignores output from the closed console', async () => {
    queue.available.mockReturnValue(true);
    queue.get.mockResolvedValue({ id: 'persistent-1', status: 'running' });
    queue.attach.mockImplementation(() => new Promise(() => {}));
    const root = renderCommandTerminal(context);
    document.body.append(root);
    await vi.waitFor(() => expect(queue.attach).toHaveBeenCalledOnce());
    const [, staleOutput, signal] = queue.attach.mock.calls[0];
    root.remove();
    await vi.waitFor(() => expect(signal.aborted).toBe(true));
    const reopened = renderCommandTerminal(context);
    document.body.append(reopened);
    await vi.waitFor(() => expect(queue.attach).toHaveBeenCalledTimes(2));
    expect(signal.aborted).toBe(true);
    staleOutput({ data: 'stale output' });
    queue.attach.mock.calls[1][1]({ data: 'new output' });
    expect(reopened.querySelector('.cli-action-terminal')?.textContent).toBe('new output');
    expect(queue.retry).not.toHaveBeenCalled();
  });

  it('retries explicitly once and navigates to the new durable command', async () => {
    queue.available.mockReturnValue(true);
    queue.get.mockResolvedValue({ id: 'persistent-1', status: 'failed' });
    queue.attach.mockResolvedValue({ command: { id: 'persistent-1', status: 'failed' }, result: {} });
    queue.retry.mockResolvedValue({ id: 'persistent-2', status: 'queued' });
    const root = renderCommandTerminal(context);
    document.body.append(root);
    await vi.waitFor(() => expect(queue.attach).toHaveBeenCalledOnce());
    const retry = /** @type {HTMLButtonElement} */ (root.querySelector('.command-terminal-retry'));
    expect(retry.hidden).toBe(false);
    retry.click();
    retry.click();
    await vi.waitFor(() => expect(window.location.hash).toBe('#page-command?command=persistent-2'));
    expect(queue.retry).toHaveBeenCalledExactlyOnceWith('persistent-1', expect.any(AbortSignal));
    expect(queue.attach).toHaveBeenCalledOnce();
  });

  it('preserves retry errors without navigating or rerunning a command', async () => {
    queue.available.mockReturnValue(true);
    queue.get.mockResolvedValue({ id: 'persistent-1', status: 'timed-out' });
    queue.attach.mockResolvedValue({ command: { id: 'persistent-1', status: 'timed-out' }, result: {} });
    queue.retry.mockRejectedValue(new Error('Retry not authorized.'));
    const root = renderCommandTerminal(context);
    document.body.append(root);
    await vi.waitFor(() => expect(queue.attach).toHaveBeenCalledOnce());
    const retry = /** @type {HTMLButtonElement} */ (root.querySelector('.command-terminal-retry'));
    retry.click();
    await vi.waitFor(() => expect(retry.disabled).toBe(false));
    expect(window.location.hash).toBe('#page-command?command=persistent-1');
    expect(queue.attach).toHaveBeenCalledOnce();
  });
  it('renders an honest unavailable state without worker requests', () => {
    queue.available.mockReturnValue(false);
    const root = renderCommandTerminal(context);
    document.body.append(root);
    expect(root.textContent).toContain('authorized local admin or canvas');
    expect(queue.get).not.toHaveBeenCalled();
    expect(queue.attach).not.toHaveBeenCalled();
  });

  it('replays persistent output and reveals retry only for a failed command', async () => {
    queue.available.mockReturnValue(true);
    queue.get.mockResolvedValue({ id: 'persistent-1', status: 'failed', command: 'gh aw compile --strict' });
    queue.attach.mockImplementation(async (_id, output) => {
      output({ data: 'Retained output' });
      return { command: { id: 'persistent-1', status: 'failed' }, result: { ok: false, error: 'Exit 1' } };
    });
    const root = renderCommandTerminal(context);
    document.body.append(root);
    await vi.waitFor(() => expect(root.querySelector('.cli-action-terminal')?.textContent).toContain('Retained output'));
    expect(root.querySelector('.cli-action-terminal')?.textContent)
      .toBe('$ gh aw compile --strict\r\nRetained output\r\nExit 1\r\n');
    expect(terminalWrites).toHaveBeenLastCalledWith('\r\nExit 1\r\n', 'stderr');
    expect(root.querySelector('output')?.textContent).toBe('persistent-1 · failed');
    expect(/** @type {HTMLButtonElement} */ (root.querySelector('.command-terminal-retry')).hidden).toBe(false);
    expect(root.querySelector('.cli-action-cancel')).toBeNull();
  });

  it.each(['completed', 'failed', 'canceled', 'timed-out'])('keeps the console view after %s until the user navigates away', async (status) => {
    queue.available.mockReturnValue(true);
    queue.get.mockResolvedValue({ id: 'persistent-1', status: 'running' });
    queue.attach.mockResolvedValue({ command: { id: 'persistent-1', status }, result: {} });
    const root = renderCommandTerminal(context);
    document.body.append(root);
    await vi.waitFor(() => expect(root.querySelector('output')?.textContent).toBe(`persistent-1 · ${status}`));
    expect(/** @type {HTMLAnchorElement} */ (root.querySelector('a')).hidden).toBe(true);
    expect(root.isConnected).toBe(true);
    expect(window.location.hash).toBe('#page-command?command=persistent-1');
    expect(queue.cancel).not.toHaveBeenCalled();
    expect(queue.retry).not.toHaveBeenCalled();
  });

  it('aborts the browser attachment on detachment without cancelling server work', async () => {
    queue.available.mockReturnValue(true);
    queue.get.mockResolvedValue({ id: 'persistent-1', status: 'running' });
    queue.attach.mockImplementation((_id, _output, signal) => new Promise((_resolve, reject) => {
      signal.addEventListener('abort', () => reject(new DOMException('Detached', 'AbortError')), { once: true });
    }));
    const root = renderCommandTerminal(context);
    document.body.append(root);
    await vi.waitFor(() => expect(queue.attach).toHaveBeenCalled());
    const signal = queue.attach.mock.calls[0][2];
    root.remove();
    await vi.waitFor(() => expect(signal.aborted).toBe(true));
    expect(queue.cancel).not.toHaveBeenCalled();
  });

  it('copies plain text from split colored output without changing terminal rendering or running work', async () => {
    queue.available.mockReturnValue(true);
    queue.get.mockResolvedValue({ id: 'persistent-1', status: 'running', command: 'gh aw compile --strict' });
    queue.attach.mockImplementation((_id, output) => {
      output({ data: '\x1b[3' });
      output({ data: '2mCompiling workflows...\x1b[0m\r\n' });
      return new Promise(() => {});
    });
    const root = renderCommandTerminal(context);
    document.body.append(root);
    await vi.waitFor(() => expect(queue.attach).toHaveBeenCalled());
    const copy = /** @type {HTMLButtonElement} */ (root.querySelector('.command-terminal-copy'));
    expect(copy.getAttribute('aria-label')).toBe('Copy command output');
    expect(copy.textContent).toBe('Copy');
    expect(copy.querySelector('.octicon-copy')).not.toBeNull();
    expect(root.querySelector('.cli-action-terminal')?.textContent).toContain('\x1b[32mCompiling workflows...\x1b[0m');
    copy.click();
    await vi.waitFor(() => expect(navigator.clipboard.writeText)
      .toHaveBeenCalledExactlyOnceWith('$ gh aw compile --strict\r\nCompiling workflows...\r\n'));
    expect(queue.cancel).not.toHaveBeenCalled();
    expect(root.querySelector('output')?.textContent).toContain('running');
  });

  it.each([
    ['\x1b[1;95mBright\x1b[0m', 'Bright'],
    ['\x1b[38;5;208mIndexed\x1b[39m', 'Indexed'],
    ['\x1b[38;2;10;20;30mTrue color\x1b[0m', 'True color'],
    ['\x1b[38:2::10:20:30mColon color\x1b[m', 'Colon color'],
    ['\u009b32mC1 color\u009b0m', 'C1 color'],
    ['\x1b]8;;https://example.com\x07Link\x1b]8;;\x07', 'Link'],
    ['Plain [31m text\r\nNext line', 'Plain [31m text\r\nNext line']
  ])('copies formatting-free output for %j', async (formatted, plain) => {
    queue.available.mockReturnValue(true);
    queue.get.mockResolvedValue({ id: 'persistent-1', status: 'running' });
    queue.attach.mockImplementation((_id, output) => {
      output({ data: formatted.slice(0, 3), stream: 'stderr' });
      output({ data: formatted.slice(3), stream: 'stderr' });
      return new Promise(() => {});
    });
    const root = renderCommandTerminal(context);
    document.body.append(root);
    await vi.waitFor(() => expect(queue.attach).toHaveBeenCalled());
    /** @type {HTMLButtonElement} */ (root.querySelector('.command-terminal-copy')).click();
    await vi.waitFor(() => expect(navigator.clipboard.writeText).toHaveBeenCalledExactlyOnceWith(plain));
  });

  it('reports clipboard errors without changing command state', async () => {
    vi.mocked(navigator.clipboard.writeText).mockRejectedValue(new Error('Clipboard blocked.'));
    queue.available.mockReturnValue(true);
    queue.get.mockResolvedValue({ id: 'persistent-1', status: 'running', command: 'gh aw compile' });
    queue.attach.mockImplementation(() => new Promise(() => {}));
    const root = renderCommandTerminal(context);
    document.body.append(root);
    await vi.waitFor(() => expect(queue.attach).toHaveBeenCalled());
    const copy = /** @type {HTMLButtonElement} */ (root.querySelector('.command-terminal-copy'));
    copy.click();
    await vi.waitFor(() => expect(copy.disabled).toBe(false));
    expect(navigator.clipboard.writeText).toHaveBeenCalledOnce();
    expect(queue.cancel).not.toHaveBeenCalled();
    expect(root.querySelector('output')?.textContent).toContain('running');
  });
});
