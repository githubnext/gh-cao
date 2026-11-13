import { afterEach, describe, expect, it, vi } from 'vitest';
import { createDialogSelector } from '../../src/components/dialog-selector.js';
import { renderDeclaredCliAction, setDeclaredCliActions } from '../../src/components/cli-actions.js';
import { validateDashboardDocument } from '../../src/validator.js';
import { validateViewActions } from '../../src/validator-views.js';
import { state as validationState } from '../../src/validator-state.js';
import { authoritativeDashboard } from '../authoritative-dashboard.js';
import { DataRequestWorker } from '../data-request-worker.js';

vi.mock('../../src/components/cli-terminal-runtime.js', () => ({
  mountTerminal: () => ({ write: vi.fn(), reset: vi.fn() })
}));

const definition = /** @type {import('../../src/dialog-selector.js').DialogSelector} */ ({
  label: 'Repository', 'debounce-ms': 300,
  query: { command: 'gh search repos', fields: ['fullName', 'description'], limit: 20 },
  mapping: { value: 'fullName', label: 'fullName', description: 'description', target: 'repository' }
});
const action = {
  id: 'add-repository', level: /** @type {const} */ ('operate'), label: 'Add repository', icon: 'plus',
  command: 'gh cao repos add {{repository}}', placement: /** @type {const} */ ('view'), selector: definition
};
const option = { value: 'octo/repo', label: 'octo/repo', description: 'Example repository' };
const response = (options = [option]) => ({ ok: true, json: async () => ({ options }) });
/** @type {AbortController[]} */
const lifetimes = [];
function picker(selectorDefinition = definition) {
  vi.useFakeTimers();
  const lifetime = new AbortController();
  lifetimes.push(lifetime);
  const select = vi.fn();
  const control = createDialogSelector(action.id, selectorDefinition, { signal: lifetime.signal, select });
  const input = /** @type {HTMLInputElement} */ (control.root.querySelector('input'));
  return { ...control, select, input, lifetime, type: (/** @type {string} */ text) => {
    input.value = text;
    input.dispatchEvent(new Event('input'));
  } };
}
afterEach(() => {
  for (const lifetime of lifetimes.splice(0)) lifetime.abort();
  document.body.replaceChildren();
  vi.useRealTimers();
  vi.unstubAllGlobals();
  setDeclaredCliActions([]);
});

describe('declarative external dialog selector', () => {
  it('offers exact input and wildcard patterns only when declared and clears selection on edits', async () => {
    const fetch = vi.fn();
    vi.stubGlobal('fetch', fetch);
    const control = picker({ ...definition, 'allow-patterns': true, mapping: { ...definition.mapping, target: 'repository-pattern' } });
    for (const value of ['octo/repo', 'octo/service-*', 'octo/*']) {
      control.type(value);
      expect(control.select).toHaveBeenLastCalledWith(null);
      control.root.querySelector('button')?.click();
      expect(control.select).toHaveBeenLastCalledWith(value);
    }
    control.type('octo/*;echo');
    expect(control.select).toHaveBeenLastCalledWith(null);
    expect(control.root.querySelector('button')).toBeNull();
    expect(control.root.textContent).toContain('Enter an owner/repository pattern');
    await vi.advanceTimersByTimeAsync(500);
    expect(fetch).not.toHaveBeenCalled();
    control.reset();
    expect(control.input.value).toBe('');
    expect(control.root.querySelector('button')).toBeNull();
  });

  it('debounces input and uses only action identity and opaque text in requests', async () => {
    const fetch = vi.fn().mockResolvedValue(response());
    vi.stubGlobal('fetch', fetch);
    const control = picker();
    control.type('o');
    await vi.advanceTimersByTimeAsync(400);
    expect(fetch).not.toHaveBeenCalled();
    control.type('oc');
    await vi.advanceTimersByTimeAsync(200);
    control.type('octo');
    await vi.advanceTimersByTimeAsync(299);
    expect(fetch).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(1);
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(JSON.parse(fetch.mock.calls[0][1].body)).toEqual({ id: action.id, text: 'octo' });
    expect(control.root.querySelector('input')?.getAttribute('aria-label')).toBe('Repository');
    control.root.querySelector('button')?.click();
    expect(control.select).toHaveBeenLastCalledWith('octo/repo');
    expect(control.root.querySelector('button')?.getAttribute('aria-pressed')).toBe('true');
    control.type('different');
    expect(control.select).toHaveBeenLastCalledWith(null);
    expect(control.root.querySelector('button')).toBeNull();
  });

  it('ignores stale responses even when the transport does not honor abort', async () => {
    /** @type {((value: ReturnType<typeof response>) => void) | undefined} */
    let resolveFirst;
    const fetch = vi.fn().mockImplementationOnce(() => new Promise((resolve) => { resolveFirst = resolve; }))
      .mockResolvedValueOnce(response([{ ...option, value: 'octo/new', label: 'octo/new' }]));
    vi.stubGlobal('fetch', fetch);
    const control = picker();
    control.type('first');
    await vi.advanceTimersByTimeAsync(300);
    const signal = fetch.mock.calls[0][1].signal;
    control.type('new');
    expect(signal.aborted).toBe(true);
    await vi.advanceTimersByTimeAsync(300);
    resolveFirst?.(response());
    await vi.advanceTimersByTimeAsync(0);
    expect(control.root.textContent).toContain('octo/new');
    expect(control.root.textContent).not.toContain('octo/repo');
  });

  it('announces the declared result limit and allows selection at the end of a large list', async () => {
    const limit = 50;
    const options = Array.from({ length: limit }, (_, index) => ({
      value: `octo/repo-${index}`, label: `octo/repo-${index}`, description: `Repository ${index}`
    }));
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(response(options)));
    const control = picker({ ...definition, query: { ...definition.query, limit } });
    control.type('repo');
    await vi.advanceTimersByTimeAsync(300);
    expect(control.root.querySelectorAll('.dialog-selector-option')).toHaveLength(limit);
    expect(control.root.textContent).toContain('Showing the first 50 repositories. Refine your search');
    control.root.querySelectorAll('button')[limit - 1]?.click();
    expect(control.select).toHaveBeenLastCalledWith('octo/repo-49');
    expect(control.root.querySelectorAll('button')[limit - 1]?.getAttribute('aria-pressed')).toBe('true');
    expect(control.root.querySelector('.dialog-selector-options')?.getAttribute('aria-busy')).toBe('false');
  });

  it('keeps slow searches editable, announces the delay, and clears timers on replacement and completion', async () => {
    /** @type {((value: ReturnType<typeof response>) => void) | undefined} */
    let resolveFirst;
    const fetch = vi.fn().mockImplementationOnce(() => new Promise((resolve) => { resolveFirst = resolve; }))
      .mockResolvedValueOnce(response([{ ...option, value: 'octo/new', label: 'octo/new' }]));
    vi.stubGlobal('fetch', fetch);
    const control = picker();
    const results = control.root.querySelector('.dialog-selector-options');
    control.type('first');
    await vi.advanceTimersByTimeAsync(300);
    expect(control.root.textContent).toContain('Searching repositories...');
    expect(results?.getAttribute('aria-busy')).toBe('true');
    expect(control.input.disabled).toBe(false);
    await vi.advanceTimersByTimeAsync(1999);
    expect(control.root.textContent).not.toContain('taking longer');
    await vi.advanceTimersByTimeAsync(1);
    expect(control.root.textContent).toContain('taking longer than usual');
    expect(control.root.textContent).toContain('keep typing');
    expect(control.root.querySelector('button')).toBeNull();
    control.type('new');
    expect(fetch.mock.calls[0][1].signal.aborted).toBe(true);
    expect(results?.getAttribute('aria-busy')).toBe('false');
    await vi.advanceTimersByTimeAsync(300);
    resolveFirst?.(response());
    await vi.advanceTimersByTimeAsync(3000);
    expect(control.root.textContent).toContain('octo/new');
    expect(control.root.textContent).not.toContain('octo/repo');
    expect(control.root.textContent).not.toContain('taking longer');
    expect(results?.getAttribute('aria-busy')).toBe('false');
    expect(vi.getTimerCount()).toBe(0);
  });

  it('cleans up delayed search timers on cancellation, disposal, and failure', async () => {
    /** @type {((reason: Error) => void) | undefined} */
    let rejectSearch;
    const fetch = vi.fn().mockImplementation(() => new Promise((_, reject) => { rejectSearch = reject; }));
    vi.stubGlobal('fetch', fetch);
    const control = picker();
    control.type('failed');
    await vi.advanceTimersByTimeAsync(300);
    rejectSearch?.(new Error('GitHub search timed out'));
    await vi.advanceTimersByTimeAsync(0);
    expect(control.root.textContent).toContain('GitHub search timed out');
    expect(vi.getTimerCount()).toBe(0);
    control.type('cancelled');
    await vi.advanceTimersByTimeAsync(300);
    control.cancel();
    expect(fetch.mock.calls[1][1].signal.aborted).toBe(true);
    expect(vi.getTimerCount()).toBe(0);
    control.type('disposed');
    await vi.advanceTimersByTimeAsync(300);
    control.lifetime.abort();
    expect(fetch.mock.calls[2][1].signal.aborted).toBe(true);
    expect(vi.getTimerCount()).toBe(0);
  });

  it('announces empty and unavailable states and cancels work on close or disposal', async () => {
    const fetch = vi.fn().mockResolvedValueOnce(response([]))
      .mockRejectedValueOnce(new Error('Lookup unavailable'));
    vi.stubGlobal('fetch', fetch);
    const control = picker();
    control.type('empty');
    await vi.advanceTimersByTimeAsync(300);
    expect(control.root.textContent).toContain('No repositories found.');
    control.type('failed');
    await vi.advanceTimersByTimeAsync(300);
    expect(control.root.textContent).toContain('Lookup unavailable');
    control.type('cancelled');
    control.cancel();
    await vi.advanceTimersByTimeAsync(300);
    expect(fetch).toHaveBeenCalledTimes(2);
    control.type('disposed');
    control.lifetime.abort();
    await vi.advanceTimersByTimeAsync(300);
    expect(fetch).toHaveBeenCalledTimes(2);
  });

  it('requires selection and explicit confirmation, with no selector outside canvas mode', async () => {
    vi.useFakeTimers();
    vi.stubGlobal('__CLI_QUEUE__', true);
    vi.stubGlobal('Worker', DataRequestWorker);
    setDeclaredCliActions([action], { canExecute: false });
    expect(renderDeclaredCliAction(action.id)).toBeNull();
    setDeclaredCliActions([action], { canExecute: true, templateValues: { repository: 'acme/control', unrelated: 'must-not-be-sent' } });
    const fetch = vi.fn().mockResolvedValueOnce(response())
      .mockResolvedValueOnce(Response.json({ id: 'persistent-1', status: 'queued' }, { status: 202 }))
      .mockResolvedValueOnce({
      ok: true, body: new ReadableStream({ start(controller) {
        controller.enqueue(new TextEncoder().encode('{"type":"complete","result":{"ok":true}}\n'));
        controller.close();
      } })
    });
    vi.stubGlobal('fetch', fetch);
    const root = renderDeclaredCliAction(action.id);
    if (!root) throw new Error('Expected declared selector action.');
    document.body.append(root);
    root.querySelector('.cli-action-trigger')?.dispatchEvent(new MouseEvent('click'));
    expect(root.querySelector('.cli-action-dialog-body > p')).toBeNull();
    const confirm = /** @type {HTMLButtonElement} */ (root.querySelector('.cli-action-confirm'));
    const input = /** @type {HTMLInputElement} */ (root.querySelector('input'));
    expect(confirm.textContent).toBe('Add repository');
    expect(confirm.disabled).toBe(true);
    input.value = 'octo';
    input.dispatchEvent(new Event('input'));
    await vi.advanceTimersByTimeAsync(300);
    root.querySelector('.dialog-selector-option')?.dispatchEvent(new MouseEvent('click'));
    expect(fetch).toHaveBeenCalledTimes(1);
    expect(root.querySelector('.cli-action-command')?.textContent).toBe('gh cao repos add octo/repo');
    expect(confirm.textContent).toBe('Add repository');
    expect(confirm.disabled).toBe(false);
    confirm.click();
    await vi.dynamicImportSettled();
    await vi.advanceTimersByTimeAsync(0);
    expect(JSON.parse(fetch.mock.calls[1][1].body)).toEqual({ id: action.id, arguments: {}, values: { repository: 'octo/repo' }, idempotencyKey: expect.any(String) });
    expect(window.location.hash).toBe('#page-command?command=persistent-1');
    expect(root.querySelector('dialog')?.open).toBe(false);
    expect(fetch).toHaveBeenCalledTimes(2);
    root.remove();
    await vi.advanceTimersByTimeAsync(0);
  });

  it('validates selector declarations and list action references with the production document', () => {
    expect(validateDashboardDocument(JSON.stringify(authoritativeDashboard)).ok).toBe(true);
    const invalid = structuredClone(authoritativeDashboard);
    invalid.dashboard['cli-actions'].find((/** @type {{ id: string }} */ candidate) => candidate.id === action.id).selector.mapping.target = 'undeclared';
    expect(validateDashboardDocument(JSON.stringify(invalid)).ok).toBe(false);
  });

  it('rejects missing, duplicate, and non-view action references', () => {
    validationState.declaredCliActions = new Map(authoritativeDashboard.dashboard['cli-actions'].map(
      (/** @type {{ id: string }} */ action) => [action.id, action]));
    try {
      for (const actions of [[], ['missing'], ['add-repository', 'add-repository'], ['create-agent-task']]) {
        /** @type {import('../../src/validator.js').ValidationError[]} */
        const errors = [];
        validateViewActions({ mark: 'chart', actions }, '$.view', errors);
        expect(errors.length).toBeGreaterThan(0);
      }
    } finally {
      validationState.declaredCliActions = new Map();
    }
  });
});
