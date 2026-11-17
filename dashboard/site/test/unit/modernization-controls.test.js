// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

const native = vi.hoisted(() => ({ available: true, execute: vi.fn(), notify: vi.fn() }));
vi.mock('../../src/data-processor.js', () => ({ cliQueueAvailable: () => native.available }));
vi.mock('../../src/components/cli-actions.js', () => ({ executeDeclaredCliAction: native.execute }));
vi.mock('../../src/notification-service.js', () => ({ publishNotification: native.notify }));
import { renderModernizationControls } from '../../src/components/modernization-controls.js';
import { MODERNIZATION_RESULT_PREFIX, parseModernizationResult } from '../../src/modernization-protocol.js';

const repository = 'octodemo/control';
const common = { schema: 'cao-modernization-operator/v1', status: 'available', controlRepository: repository, controlSha: 'a'.repeat(40), repository };
const task = { id: 't1', title: 'Synthetic characterization', repository: 'octodemo/api', phase: 'issue', state: 'ready', issueUrl: null, pullRequestUrl: null };
const status = { ...common, kind: 'status', capabilities: Object.fromEntries(
  ['assess', 'approve-plan', 'run', 'verify'].map(action => [action, { authorized: true, reason: 'Reviewed scope available' }])
), plan: { id: 'synthetic-plan' }, planDigest: 'b'.repeat(64), repositories: [repository, task.repository], tasks: [task],
links: { issuesUrl: 'https://github.com/octodemo/control/issues', pullRequestsUrl: 'javascript:alert(1)' } };

/** @param {unknown} value */
function frame(value) { return `${MODERNIZATION_RESULT_PREFIX}${JSON.stringify(value)}\n`; }
/** @param {HTMLElement} root @param {string} label */
function button(root, label) {
  const found = [...root.querySelectorAll('button')].find(element => element.textContent === label);
  if (!found) throw new Error(`Missing button ${label}`);
  return found;
}
/** @param {Partial<import('../../src/components/ui-elements.js').ElementRenderContext>} [overrides] */
function mount(overrides = {}) {
  const root = renderModernizationControls({
    pageId: 'ops', title: 'Modernization', contextDetails: [],
    headingTag: 'h3', sourceNames: [], sources: {}, routeParameters: {}, ...overrides,
    elementConfig: { actions: { status: 'modernization-status', preview: 'modernization-preview', execute: 'modernization-execute' }, ...overrides.elementConfig }
  });
  document.body.append(root);
  const input = root.querySelector('input');
  if (input) {
    input.value = repository;
    input.dispatchEvent(new Event('input'));
  }
  return root;
}
/** @param {HTMLElement} root */
async function check(root) {
  button(root, 'Check readiness').click();
  await vi.waitFor(() => expect(root.textContent).toContain('Current check: available'));
}

beforeEach(() => {
  native.available = true;
  native.notify.mockReset();
  native.execute.mockReset().mockImplementation(async (id, values, options) => {
    const result = id === 'modernization-status' ? status : id === 'modernization-preview' ? {
      ...common, kind: 'preview', previewId: 'synthetic-preview', action: values.operation, mode: values.mode,
      canExecute: true, repositories: [repository, task.repository], tasks: [task], effects: ['Create a synthetic issue'],
      warnings: [], expiresAt: new Date(Date.now() + 10_000).toISOString()
    } : { ...common, kind: 'execution', accepted: true, action: 'run', workflowUrl: 'https://github.com/octodemo/control/actions' };
    options.onOutput({ stream: 'stdout', data: `Native operator\n${frame(result)}` });
    return { ok: true };
  });
});
afterEach(() => {
  document.body.replaceChildren();
  vi.useRealTimers();
});

describe('Modernization operator interaction', () => {
  it('keeps safeguards discoverable and distinguishes readiness, supporting actions and confirmation', async () => {
    const root = mount();
    expect(root.textContent).toContain('Check readiness to enable authorized actions.');
    const help = /** @type {HTMLDetailsElement} */ (root.querySelector('.modernization-help'));
    expect(help.open).toBe(false);
    expect(help.querySelector('summary')?.textContent).toBe('Scope and safeguards');
    expect(help.textContent).toContain('Every write needs a fresh native policy check and your confirmation.');
    expect(button(root, 'Assess system').classList.contains('cli-action-cancel')).toBe(true);
    expect(native.execute).not.toHaveBeenCalled();
    await check(root);
    expect(button(root, 'Check readiness').classList.contains('cli-action-cancel')).toBe(true);
    expect(button(root, 'Review next run').classList.contains('cli-action-confirm')).toBe(true);
    expect(root.textContent).not.toContain('Reviewed scope available');
    const plan = button(root, 'Review / approve plan');
    expect(plan.getAttribute('aria-expanded')).toBe('false');
    plan.click();
    expect(button(root, 'Review / approve plan').getAttribute('aria-expanded')).toBe('true');
    expect(root.querySelector(`#${plan.getAttribute('aria-controls')}`)?.textContent).toContain('Concrete plan approval');
  });

  it('does nothing on mount, checks current state, previews review mode and requires separate one-use confirmation', async () => {
    const root = mount();
    expect(native.execute).not.toHaveBeenCalled();
    expect(button(root, 'Review next run').disabled).toBe(true);
    await check(root);
    expect(root.textContent).toContain(task.title);
    expect(root.querySelector('a[href^="javascript:"]')).toBeNull();
    button(root, 'Review next run').click();
    await vi.waitFor(() => expect(root.querySelector('[data-preview-title]')).not.toBeNull());
    expect(native.execute.mock.calls.at(-1)?.[1]).toEqual({ repository, operation: 'run', mode: 'review' });
    expect(native.execute.mock.calls.some(([id]) => id === 'modernization-execute')).toBe(false);
    const submit = button(root, 'Run next batch');
    submit.click();
    submit.click();
    await vi.waitFor(() => expect(root.textContent).toContain('Dispatch accepted—not proof'));
    expect(native.execute.mock.calls.filter(([id]) => id === 'modernization-execute')).toHaveLength(1);
    expect(root.querySelector('[data-preview-title]')).toBeNull();
    expect(button(root, 'Review next run').disabled).toBe(true);
  });

  it('invalidates reviewed effects when mode or repository changes and forces verification to review mode', async () => {
    const root = mount();
    await check(root);
    button(root, 'Review next run').click();
    await vi.waitFor(() => expect(root.querySelector('[data-preview-title]')).not.toBeNull());
    const mode = /** @type {HTMLSelectElement} */ (root.querySelector('select'));
    mode.value = 'live';
    mode.dispatchEvent(new Event('change'));
    expect(root.querySelector('[data-preview-title]')).toBeNull();
    button(root, 'Verify results').click();
    await vi.waitFor(() => expect(native.execute.mock.calls.at(-1)?.[1].operation).toBe('verify'));
    expect(native.execute.mock.calls.at(-1)?.[1].mode).toBe('review');
    const input = /** @type {HTMLInputElement} */ (root.querySelector('input'));
    input.value = 'octodemo/other';
    input.dispatchEvent(new Event('input'));
    expect(root.querySelector('[data-preview-title]')).toBeNull();
    expect(button(root, 'Review next run').disabled).toBe(true);
  });

  it('previews imported concrete plan input without publishing it', async () => {
    const root = mount();
    await check(root);
    button(root, 'Review / approve plan').click();
    const input = /** @type {HTMLInputElement} */ (root.querySelector('input[type="file"]'));
    const plan = '{"id":"reviewed-synthetic-plan"}';
    Object.defineProperty(input, 'files', { value: [{ size: plan.length, text: async () => plan }] });
    input.dispatchEvent(new Event('change'));
    await vi.waitFor(() => expect(root.querySelector('textarea')?.value).toBe(plan));
    button(root, 'Review approval').click();
    await vi.waitFor(() => expect(native.execute.mock.calls.at(-1)?.[1].operation).toBe('approve-plan'));
    expect(native.execute.mock.calls.at(-1)?.[2].input).toBe(plan);
    expect(native.execute.mock.calls.some(([id]) => id === 'modernization-execute')).toBe(false);
  });

  it('expires confirmation without dispatching', async () => {
    const root = mount();
    await check(root);
    vi.useFakeTimers();
    button(root, 'Review next run').click();
    await Promise.resolve();
    await Promise.resolve();
    await vi.advanceTimersByTimeAsync(10_001);
    expect(button(root, 'Run next batch').disabled).toBe(true);
    expect(root.textContent).toContain('Preview expired');
    expect(native.execute.mock.calls.some(([id]) => id === 'modernization-execute')).toBe(false);
  });

  it('makes unsupported transport and failed or malformed responses explicit', async () => {
    native.available = false;
    const unavailable = mount();
    expect(unavailable.textContent).toContain('Read-only deployment');
    expect(button(unavailable, 'Check readiness').disabled).toBe(true);
    unavailable.remove();
    native.available = true;
    native.execute.mockResolvedValue({ ok: true });
    const root = mount();
    button(root, 'Check readiness').click();
    await vi.waitFor(() => expect(root.querySelector('[role="alert"]')?.textContent).toContain('one confirmed Modernization result'));
    expect(native.notify).toHaveBeenCalledWith(expect.objectContaining({ tone: 'error' }));
  });

  it('aborts attachment on navigation without automatically retrying or canceling accepted remote work', async () => {
    /** @type {() => void} */
    let finish = () => { throw new Error('Pending native request was not attached.'); };
    native.execute.mockImplementation((_id, _values, options) => new Promise(resolve => {
      finish = () => { options.onOutput({ stream: 'stdout', data: frame(status) }); resolve({ ok: true }); };
    }));
    const root = mount();
    button(root, 'Check readiness').click();
    const signal = native.execute.mock.calls[0][2].signal;
    root.remove();
    await Promise.resolve();
    expect(signal.aborted).toBe(true);
    finish();
    await Promise.resolve();
    expect(native.execute).toHaveBeenCalledOnce();
    expect(native.notify).not.toHaveBeenCalled();
  });

  it('does not render Modernization controls for another campaign', () => {
    const root = mount({ routeParameter: 'file-diet', elementConfig: { body: 'campaign' } });
    expect(root.hidden).toBe(true);
    expect(native.execute).not.toHaveBeenCalled();
  });
});

describe('Modernization protocol', () => {
  it('preserves explicit native unavailability without inventing an executable preview', () => {
    const unavailable = { ...common, kind: 'preview', action: 'run', mode: 'review',
      canExecute: false, reason: 'Installed workflow lacks the reviewed revision fence.' };
    expect(parseModernizationResult(frame(unavailable), 'preview', repository)).toMatchObject(unavailable);
    expect(() => parseModernizationResult(frame({
      ...common, controlSha: undefined, kind: 'execution', status: 'unconfirmed',
      reason: 'Outcome unconfirmed; preview consumed. Inspect the repository before retrying.'
    }), 'execution', repository)).toThrow('Outcome unconfirmed; preview consumed');
  });

  it.each([
    ['', 'one confirmed'],
    [frame(status) + frame(status), 'one confirmed'],
    [frame({ ...status, controlRepository: 'octodemo/other' }), 'requested control'],
    [frame({ ...status, tasks: [{ id: 't1' }] }), 'task preview'],
    [frame({ ...status, capabilities: { run: { authorized: 'yes' } } }), 'authorization'],
    [frame({ ...status, effects: 'write anything' }), 'effects or warnings']
  ])('rejects invalid native output %#', (output, error) => {
    expect(() => parseModernizationResult(output, 'status', repository)).toThrow(error);
  });
});
