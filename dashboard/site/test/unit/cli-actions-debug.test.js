// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';

const queue = vi.hoisted(() => ({ enqueue: vi.fn(), attach: vi.fn(), cancel: vi.fn() }));
vi.mock('../../src/data-processor.js', () => ({
  cliQueueAvailable: () => true,
  enqueueCliCommand: queue.enqueue,
  attachCliCommand: queue.attach,
  cancelCliCommand: queue.cancel,
  subscribeCanonicalDashboardView: vi.fn()
}));
vi.mock('../../src/components/cli-terminal-runtime.js', () => ({
  mountTerminal: (/** @type {HTMLElement} */ root) => ({
    write: (/** @type {string} */ text) => { root.textContent += text; }, reset() {}
  })
}));

const SECRET_PROMPT = 'confidential prompt text';
const SECRET_REPOSITORY = 'octo/confidential-repository';
const SECRET_ERROR = 'confidential error message';
const SECRET_SELECTION = 'confidential/selection;value';

/** @param {string} search */
async function loadCliActions(search) {
  const output = { debug: vi.fn() };
  vi.doMock('../../src/debug.js', async () => {
    const actual = /** @type {typeof import('../../src/debug.js')} */ (await vi.importActual('../../src/debug.js'));
    return {
      ...actual,
      createDebug: (/** @type {string} */ category) => actual.createDebug(category, { search: () => search, output })
    };
  });
  vi.resetModules();
  const module = await import('../../src/components/cli-actions.js');
  module.setDeclaredCliActions([
    {
      id: 'agent-task', label: 'Agent task', icon: 'zap', command: 'gh agent-task create --from-file -', placement: 'row'
    },
    {
      id: 'upgrade', label: 'Upgrade all', icon: 'download', placement: 'view',
      command: 'gh aw upgrade --repo {{repository}}'
    }
  ]);
  return { ...module, output };
}

/** @param {typeof import('../../src/components/cli-actions.js')} module */
function startAgentTask(module) {
  const control = module.createPromptCliActionControl('agent-task', () => SECRET_PROMPT);
  if (!control) throw new Error('Missing agent task control');
  document.body.append(control.button);
  control.button.click();
}

/** @param {typeof import('../../src/components/cli-actions.js')} module */
function startBatch(module) {
  const root = module.renderDeclaredCliAction('upgrade', {}, {
    rows: [{ repository: SECRET_REPOSITORY }, { repository: 'octo/second' }],
    fields: ['repository'],
    available: true
  });
  if (!root) throw new Error('Missing batch action');
  document.body.append(root);
  /** @type {HTMLButtonElement} */ (root.querySelector('.cli-action-trigger')).click();
  /** @type {HTMLButtonElement} */ (root.querySelector('.cli-action-confirm')).click();
}

afterEach(() => {
  vi.doUnmock('../../src/debug.js');
  vi.resetModules();
  document.body.replaceChildren();
  window.location.hash = '';
  queue.enqueue.mockReset();
  queue.attach.mockReset();
});

describe('cli-actions debug logging', () => {
  it('emits nothing unless the cli-actions category is enabled', async () => {
    queue.enqueue.mockResolvedValue({ id: 'command-1' });
    queue.attach.mockResolvedValue({ result: { ok: true }, command: { status: 'completed' } });
    const disabled = await loadCliActions('');
    startAgentTask(disabled);
    await vi.waitFor(() => expect(queue.attach).toHaveBeenCalledTimes(1));
    startBatch(disabled);
    await vi.waitFor(() => expect(window.location.hash).toBe('#page-commands'));
    expect(disabled.output.debug).not.toHaveBeenCalled();

    const otherCategory = await loadCliActions('?debug=cli-action-template,-cli-actions');
    startAgentTask(otherCategory);
    await vi.waitFor(() => expect(queue.attach).toHaveBeenCalledTimes(2));
    expect(otherCategory.output.debug).not.toHaveBeenCalled();
  });

  it('logs agent task outcomes under the filename-derived category', async () => {
    queue.enqueue.mockResolvedValueOnce({ id: 'command-1' }).mockRejectedValueOnce(new TypeError(SECRET_ERROR));
    queue.attach.mockResolvedValueOnce({ result: { ok: true }, command: { status: 'completed' } });
    const enabled = await loadCliActions('?debug=cli-actions');

    startAgentTask(enabled);
    await vi.waitFor(() => expect(enabled.output.debug).toHaveBeenCalledWith('[cao:cli-actions]', {
      event: 'agent-task-finished', actionId: 'agent-task', status: 'started'
    }));

    document.body.replaceChildren();
    startAgentTask(enabled);
    await vi.waitFor(() => expect(enabled.output.debug).toHaveBeenCalledWith('[cao:cli-actions]', {
      event: 'agent-task-finished', actionId: 'agent-task', status: 'failed', errorName: 'TypeError'
    }));
  });

  it('logs rejected agent task results with only the status', async () => {
    queue.enqueue.mockResolvedValue({ id: 'command-1' });
    queue.attach.mockResolvedValue({ result: { ok: false, error: SECRET_ERROR }, command: { status: 'failed' } });
    const enabled = await loadCliActions('?debug=cli-actions:*,cli-actions');

    startAgentTask(enabled);
    await vi.waitFor(() => expect(enabled.output.debug).toHaveBeenCalledWith('[cao:cli-actions]', {
      event: 'agent-task-finished', actionId: 'agent-task', status: 'rejected'
    }));
  });

  it('logs batch queue counts and excludes targets, prompts and error text', async () => {
    queue.enqueue.mockResolvedValueOnce({ id: 'command-1' }).mockRejectedValueOnce(new Error(SECRET_ERROR));
    queue.attach.mockResolvedValue({ result: { ok: true }, command: { status: 'completed' } });
    const enabled = await loadCliActions('?debug=cli-actions');

    startAgentTask(enabled);
    await vi.waitFor(() => expect(queue.attach).toHaveBeenCalledTimes(1));
    startBatch(enabled);
    await vi.waitFor(() => expect(enabled.output.debug).toHaveBeenCalledWith('[cao:cli-actions]', {
      event: 'batch-enqueued', actionId: 'upgrade', targetCount: 2, admittedCount: 1, failedCount: 1
    }));

    const serialized = JSON.stringify(enabled.output.debug.mock.calls);
    for (const secret of [SECRET_PROMPT, SECRET_REPOSITORY, SECRET_ERROR]) {
      expect(serialized).not.toContain(secret);
    }
    for (const [, metadata] of enabled.output.debug.mock.calls) {
      expect(Object.values(metadata).every((value) => typeof value !== 'object')).toBe(true);
    }
  });

  it('logs batch resolution and unavailable actions without row values', async () => {
    const enabled = await loadCliActions('?debug=cli-actions');
    expect(enabled.renderDeclaredCliAction('missing')).toBeNull();
    expect(enabled.output.debug).toHaveBeenCalledWith('[cao:cli-actions]', {
      event: 'action-unavailable', scope: 'declared', reason: 'unknown-action'
    });

    enabled.renderDeclaredCliAction('upgrade', {}, {
      rows: [{ repository: SECRET_REPOSITORY }, { repository: 7 }], fields: ['repository'], available: true
    });
    enabled.renderDeclaredCliAction('upgrade', {}, {
      rows: [{ repository: SECRET_REPOSITORY }, { repository: 'octo/second' }], fields: ['repository'], available: true
    });
    const batchCalls = enabled.output.debug.mock.calls.filter(([, metadata]) => metadata.event === 'batch-resolved');
    expect(batchCalls.map(([, metadata]) => metadata)).toEqual([
      { event: 'batch-resolved', actionId: 'upgrade', status: 'rejected', targetCount: 0, available: true },
      { event: 'batch-resolved', actionId: 'upgrade', status: 'ready', targetCount: 2, available: true }
    ]);
    expect(JSON.stringify(enabled.output.debug.mock.calls)).not.toContain(SECRET_REPOSITORY);

    const disabled = await loadCliActions('?debug=cli-actions,-cli-actions');
    disabled.renderDeclaredCliAction('missing');
    disabled.renderDeclaredCliAction('upgrade', {}, {
      rows: [{ repository: 'octo/second' }], fields: ['repository'], available: true
    });
    expect(disabled.output.debug).not.toHaveBeenCalled();
  });

  it('logs only the action id when a selector choice is rejected', async () => {
    const enabled = await loadCliActions('?debug=cli-actions');
    enabled.setDeclaredCliActions([{
      id: 'add-repository', label: 'Add repository', icon: 'plus', placement: 'view',
      command: 'gh cao repos add {{repository}}',
      selector: {
        label: 'Repository', 'debounce-ms': 300,
        query: { command: 'gh search repos', fields: ['fullName'], limit: 20 },
        mapping: { value: 'fullName', label: 'fullName', target: 'repository' }
      }
    }], { canExecute: true });
    const root = enabled.renderDeclaredCliAction('add-repository');
    if (!root) throw new Error('Missing selector action');
    document.body.append(root);
    /** @type {HTMLButtonElement} */ (root.querySelector('.cli-action-trigger')).click();
    const input = /** @type {HTMLInputElement} */ (root.querySelector('input'));
    input.value = SECRET_SELECTION;
    input.dispatchEvent(new Event('input'));
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue({
      ok: true, json: async () => ({ options: [{ value: SECRET_SELECTION, label: SECRET_SELECTION, description: '' }] })
    }));
    await vi.waitFor(() => expect(root.querySelector('.dialog-selector-option')).not.toBeNull(), { timeout: 2000 });
    /** @type {HTMLButtonElement} */ (root.querySelector('.dialog-selector-option')).click();
    expect(enabled.output.debug).toHaveBeenCalledWith('[cao:cli-actions]', {
      event: 'selection-rejected', actionId: 'add-repository'
    });
    expect(JSON.stringify(enabled.output.debug.mock.calls)).not.toContain(SECRET_SELECTION);
    vi.unstubAllGlobals();
  });
});
