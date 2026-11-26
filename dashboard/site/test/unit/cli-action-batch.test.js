// @vitest-environment jsdom
import { afterEach, beforeEach, expect, it, vi } from 'vitest';

const queue = vi.hoisted(() => ({ enqueue: vi.fn(), attach: vi.fn(), cancel: vi.fn() }));
vi.mock('../../src/data-processor.js', () => ({
  cliQueueAvailable: () => true,
  enqueueCliCommand: queue.enqueue, attachCliCommand: queue.attach, cancelCliCommand: queue.cancel
}));
vi.mock('../../src/components/cli-terminal-runtime.js', () => ({
  mountTerminal: (/** @type {HTMLElement} */ root) => ({
    write: (/** @type {string} */ text) => { root.textContent += text; }, reset() {}
  })
}));
import { renderDeclaredCliAction, setDeclaredCliActions } from '../../src/components/cli-actions.js';

const rows = [{ repository: 'octo/first' }, { repository: 'other/second' }];

beforeEach(() => {
  queue.enqueue.mockReset().mockImplementation(async ({ values }) => ({ id: values.repository }));
  queue.attach.mockReset();
  queue.cancel.mockReset().mockResolvedValue({});
  setDeclaredCliActions([{
    id: 'upgrade', label: 'Upgrade all', icon: 'download', placement: 'view',
    command: 'gh aw upgrade --repo {{repository}}',
    'preview-command': 'gh aw upgrade --repo {{repository}}',
    arguments: [
      { id: 'pr', label: 'Create pull request', flag: '--create-pull-request', type: 'boolean', default: true },
      { id: 'auto-merge', label: 'Auto merge', flag: '--auto-merge', type: 'boolean', default: false }
    ]
  }], { templateValues: { repository: 'acme/control' } });
});
afterEach(() => { document.body.replaceChildren(); setDeclaredCliActions([]); window.location.hash = ''; });

/** @param {typeof rows} targets @param {boolean} [available] */
function control(targets, available = true) {
  const root = renderDeclaredCliAction('upgrade', {}, { rows: targets, fields: ['repository'], available });
  if (!root) throw new Error('Missing batch action');
  document.body.append(root);
  const trigger = /** @type {HTMLButtonElement} */ (root.querySelector('.cli-action-trigger'));
  const confirm = /** @type {HTMLButtonElement} */ (root.querySelector('.cli-action-confirm'));
  return { root, trigger, confirm };
}

it('reviews batch options before queuing every explicit target without attaching', async () => {
  const { root, trigger, confirm } = control(rows);
  expect(queue.enqueue).not.toHaveBeenCalled();
  trigger.click();
  expect(queue.enqueue).not.toHaveBeenCalled();
  expect(root.querySelector('dialog')?.open).toBe(true);
  expect(root.querySelector('.cli-action-command')?.textContent).toBe(rows.map(({ repository }) =>
    `gh aw upgrade --repo ${repository} --create-pull-request`).join('\n'));
  confirm.click();
  await vi.waitFor(() => expect(root.querySelector('dialog')?.open).toBe(false));
  expect(window.location.hash).toBe('');
  expect(queue.enqueue.mock.calls.map(([payload]) => payload.values)).toEqual(rows);
  expect(queue.enqueue.mock.calls.map(([payload]) => payload.arguments))
    .toEqual(rows.map(() => ({ pr: true, 'auto-merge': false })));
  expect(new Set(queue.enqueue.mock.calls.map(([payload]) => payload.idempotencyKey)).size).toBe(2);
  expect(root.querySelector('dialog')?.open).toBe(false);
  expect(queue.attach).not.toHaveBeenCalled();
});

it('applies the opted-in auto merge flag to every preview and queued target', async () => {
  const { root, trigger, confirm } = control(rows);
  trigger.click();
  /** @type {HTMLInputElement} */ (root.querySelectorAll('.cli-action-argument input')[1]).click();
  expect(root.querySelector('.cli-action-command')?.textContent).toBe(rows.map(({ repository }) =>
    `gh aw upgrade --repo ${repository} --create-pull-request --auto-merge`).join('\n'));
  expect(queue.enqueue).not.toHaveBeenCalled();
  confirm.click();
  await vi.waitFor(() => expect(queue.enqueue).toHaveBeenCalledTimes(2));
  expect(queue.enqueue.mock.calls.map(([payload]) => payload.arguments))
    .toEqual(rows.map(() => ({ pr: true, 'auto-merge': true })));
});

it('shows one basic command and target count while retaining complete commands for review', () => {
  setDeclaredCliActions([{
    id: 'upgrade', label: 'Upgrade all', icon: 'download', placement: 'view',
    command: "gh aw upgrade --repo {{repository}} --pr-message 'Full attribution for {{repository}}'",
    'preview-command': 'gh aw upgrade --repo {{repository}}',
    arguments: [
      { id: 'pr', label: 'Create pull request', flag: '--create-pull-request', type: 'boolean', default: true },
      { id: 'auto-merge', label: 'Auto merge', flag: '--auto-merge', type: 'boolean', default: false }
    ]
  }]);
  const { root, trigger } = control(rows);
  trigger.click();
  const preview = root.querySelector('.cli-action-preview-command');
  const details = /** @type {HTMLDetailsElement} */ (root.querySelector('.cli-action-command-details'));
  expect(preview?.textContent).toBe('gh aw upgrade --repo octo/first --create-pull-request');
  expect(root.textContent).toContain('Preview for the first of 2 targets:');
  expect(details.open).toBe(false);
  expect(details.querySelector('summary')?.textContent).toBe('Show all 2 full commands');
  expect(details.querySelector('.cli-action-command')?.textContent).toBe(rows.map(({ repository }) =>
    `gh aw upgrade --repo ${repository} --pr-message 'Full attribution for ${repository}' --create-pull-request`).join('\n'));
  /** @type {HTMLInputElement} */ (root.querySelectorAll('.cli-action-argument input')[1]).click();
  expect(preview?.textContent).toBe('gh aw upgrade --repo octo/first --create-pull-request --auto-merge');
  expect(details.querySelector('.cli-action-command')?.textContent?.match(/--auto-merge/g)).toHaveLength(2);
  expect(queue.enqueue).not.toHaveBeenCalled();
});

it('dismisses batch options without queuing any commands', () => {
  const { root, trigger } = control(rows);
  trigger.click();
  /** @type {HTMLButtonElement} */ (root.querySelector('.cli-action-cancel')).click();
  expect(root.querySelector('dialog')?.open).toBe(false);
  expect(queue.enqueue).not.toHaveBeenCalled();
});

it.each([
  { targets: [], available: true },
  { targets: rows, available: false },
  { targets: [{ repository: 'octo/first' }, { repository: 'unsafe/repo;echo' }], available: true }
])('rejects empty, incomplete, or unsafe batches without a control-repository fallback', async ({ targets, available }) => {
  const { root, trigger } = control(targets, available);
  expect(trigger.disabled).toBe(true);
  expect(root.querySelector('.cli-action-command')?.textContent).not.toContain('acme/control');
  trigger.click();
  await Promise.resolve();
  expect(queue.enqueue).not.toHaveBeenCalled();
});

it('waits for every admission and prevents double activation while enqueueing', async () => {
  /** @type {((value: { id: string }) => void) | undefined} */
  let admit;
  queue.enqueue.mockImplementation(({ values }) => values.repository === 'octo/first'
    ? Promise.resolve({ id: 'octo/first' })
    : new Promise((resolve) => { admit = resolve; }));
  const { trigger, confirm } = control(rows);
  trigger.click();
  confirm.click();
  confirm.click();
  await vi.waitFor(() => expect(queue.enqueue).toHaveBeenCalledTimes(2));
  expect(trigger.disabled).toBe(true);
  expect(window.location.hash).toBe('');
  admit?.({ id: 'other/second' });
  await vi.waitFor(() => expect(document.body.textContent).toContain('2 commands queued.'));
  expect(window.location.hash).toBe('');
  expect(queue.cancel).not.toHaveBeenCalled();
  expect(queue.attach).not.toHaveBeenCalled();
});

it('shows partial admission failures and closes without navigating', async () => {
  queue.enqueue.mockRejectedValueOnce(new Error('Admission denied'));
  const { root, trigger, confirm } = control(rows);
  trigger.click();
  confirm.click();
  await vi.waitFor(() => expect(document.body.textContent).toContain('1 commands queued; 1 could not be queued.'));
  expect(root.querySelector('dialog')?.open).toBe(false);
  expect(window.location.hash).toBe('');
});

it('keeps failed admissions on the current page and allows retry', async () => {
  queue.enqueue.mockRejectedValue(new Error('Admission denied'));
  const { trigger, confirm } = control(rows);
  trigger.click();
  confirm.click();
  await vi.waitFor(() => expect(document.body.textContent).toContain('0 commands queued; 2 could not be queued.'));
  expect(window.location.hash).toBe('');
  expect(trigger.disabled).toBe(false);
});

it('binds campaign updates to separate scoped CLI commands', async () => {
  setDeclaredCliActions([{
    id: 'update', label: 'Update all', icon: 'sync', placement: 'view',
    command: 'gh cao update --campaign {{campaign}}'
  }]);
  const targets = [{ campaign: 'activity' }, { campaign: 'dependabot' }];
  const root = renderDeclaredCliAction('update', {}, { rows: targets, fields: ['campaign'], available: true });
  if (!root) throw new Error('Missing campaign action');
  document.body.append(root);
  /** @type {HTMLButtonElement} */ (root.querySelector('.cli-action-trigger')).click();
  expect(queue.enqueue).not.toHaveBeenCalled();
  /** @type {HTMLButtonElement} */ (root.querySelector('.cli-action-confirm')).click();
  await vi.waitFor(() => expect(queue.enqueue).toHaveBeenCalledTimes(2));
  expect(window.location.hash).toBe('');
  expect(queue.enqueue.mock.calls.map(([payload]) => payload.values)).toEqual(targets);
});
