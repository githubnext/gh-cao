// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
const queue = vi.hoisted(() => ({
  enqueue: vi.fn(), attach: vi.fn(), cancel: vi.fn()
}));
vi.mock('../../src/data-processor.js', () => ({
  cliQueueAvailable: () => Reflect.get(globalThis, '__CLI_QUEUE__') === true,
  enqueueCliCommand: queue.enqueue,
  attachCliCommand: queue.attach,
  cancelCliCommand: queue.cancel,
  subscribeCanonicalDashboardView: vi.fn()
}));
vi.mock('../../src/components/cli-terminal-runtime.js', () => ({
  mountTerminal: (/** @type {HTMLElement} */ root) => ({
    write: (/** @type {string} */ text) => { root.textContent += text; },
    reset: () => { root.textContent = ''; }
  })
}));
import {
  attachCliActions,
  createPromptCliActionControl,
  renderCliActions,
  renderRowCliAction,
  setDeclaredCliActions
} from '../../src/components/cli-actions.js';
import { subscribeCanonicalDashboardView } from '../../src/data-processor.js';
import { shellStyles } from '../../src/styles-shell.js';
import dashboardDocument from '../../dashboard.json' with { type: 'json' };

beforeEach(() => {
  vi.stubGlobal('__CLI_QUEUE__', true);
  queue.enqueue.mockReset().mockResolvedValue({ id: 'command-1', status: 'queued' });
  queue.attach.mockReset().mockResolvedValue({ result: { ok: true, exitCode: 0 }, command: { id: 'command-1', status: 'completed' } });
  queue.cancel.mockReset().mockResolvedValue({ id: 'command-1', status: 'canceled' });
  vi.mocked(subscribeCanonicalDashboardView).mockClear();
});

afterEach(() => {
  vi.unstubAllGlobals();
  setDeclaredCliActions([]);
});

describe('CLI actions', () => {
  it.each(['update-repository', 'update-campaign', 'update-workflow-source', 'upgrade-repository', 'upgrade-target-repository'])(
    'keeps auto merge opt-in and previews and submits its supported flag for %s',
    async (id) => {
      const action = dashboardDocument.dashboard['cli-actions'].find((candidate) => candidate.id === id);
      if (!action) throw new Error(`Missing action ${id}`);
      expect(action.arguments?.find((argument) => argument.id === 'auto-merge')).toEqual({
        id: 'auto-merge', label: 'Auto merge',
        description: 'Enable auto-merge on the created pull request. Requires Create pull request.',
        type: 'boolean', flag: '--auto-merge', default: false
      });
      const root = renderCliActions([{
        id: action.id, label: action.label, icon: action.icon, command: action.command,
        arguments: action.arguments?.map((argument) => ({ ...argument, type: /** @type {const} */ ('boolean') }))
      }], {
        templateValues: {
          repository: 'octo/example',
          'campaign-source': 'acme/catalog/activity',
          'campaign-name': 'Activity',
          'workflow-source': 'acme/workflows/review',
          'workflow-name': 'Review'
        },
        context: {
          'cao-url': 'https://ops.example.com/central/', 'cao-repository': 'acme/control',
          'cao-repository-url': 'https://github.com/acme/control'
        }
      });
      if (!root) throw new Error(`Missing action control ${id}`);
      document.body.append(root);
      const trigger = /** @type {HTMLButtonElement} */ (root.querySelector('.cli-action-trigger'));
      const autoMerge = /** @type {HTMLInputElement} */ (
        [...root.querySelectorAll('.cli-action-argument')].find((label) =>
          label.textContent?.startsWith('Auto merge'))?.querySelector('input')
      );
      trigger.click();
      expect(autoMerge.checked).toBe(false);
      expect(root.querySelector('.cli-action-command')?.textContent).not.toContain('--auto-merge');
      const expectedPrefix = id === 'update-workflow-source'
        ? 'gh aw update https://github.com/acme/workflows/review --repo acme/control'
        : id.startsWith('update')
          ? 'gh aw update https://github.com/acme/catalog/activity --repo acme/control'
          : 'gh aw upgrade --repo octo/example';
      expect(root.querySelector('.cli-action-command')?.textContent?.startsWith(`${expectedPrefix} --pr-message `)).toBe(true);
      expect(root.querySelector('.cli-action-command')?.textContent).toContain(
        "--pr-message 'This pull request was requested through [Central Agentic Ops (CAO)]"
      );
      expect(root.querySelector('.cli-action-command')?.textContent).toContain(
        'https://github.com/acme/control'
      );
      expect(root.querySelector('.cli-action-command')?.textContent).toContain(
        `https://ops.example.com/central/#page-maintenance${id.startsWith('update') ? '-campaigns' : ''}`
      );

      autoMerge.click();
      expect(root.querySelector('.cli-action-command')?.textContent).toContain('--auto-merge');
      /** @type {HTMLButtonElement} */ (root.querySelector('.cli-action-cancel')).click();
      trigger.click();
      expect(autoMerge.checked).toBe(false);
      const createPr = /** @type {HTMLInputElement} */ (root.querySelector('.cli-action-argument input'));
      if (!createPr.checked) createPr.click();
      autoMerge.click();
      expect(root.querySelector('.cli-action-command')?.textContent).toContain('--create-pull-request');
      /** @type {HTMLButtonElement} */ (root.querySelector('.cli-action-confirm')).click();
      await vi.waitFor(() => expect(queue.enqueue).toHaveBeenCalledTimes(1));
      expect(queue.enqueue.mock.calls[0][0].arguments).toMatchObject({
        'create-pull-request': true, 'auto-merge': true
      });
      root.remove();
    }
  );

  it('shows missing host context and disables execution without using the local capability URL', () => {
    const root = renderCliActions([{
      id: 'update', label: 'Update', icon: 'sync',
      command: "gh aw update --pr-message 'See {{cao-url}}'"
    }]);
    expect(root?.querySelector('.cli-action-command')?.textContent).toContain('"cao-url" is unavailable');
    expect(root?.querySelector('.cli-action-confirm')).toHaveProperty('disabled', true);
    expect(queue.enqueue).not.toHaveBeenCalled();
  });

  it.each(['update-repository', 'update-campaign', 'update-workflow-source'])(
    'fails closed for %s without control-repository context instead of targeting the row repository',
    (id) => {
      const action = dashboardDocument.dashboard['cli-actions'].find((candidate) => candidate.id === id);
      if (!action) throw new Error(`Missing action ${id}`);
      const root = renderCliActions([{
        id: action.id, label: action.label, icon: action.icon, command: action.command
      }], {
        templateValues: {
          repository: 'octo/example',
          'campaign-source': 'acme/catalog/activity',
          'campaign-name': 'Activity',
          'workflow-source': 'acme/workflows/review',
          'workflow-name': 'Review'
        },
        context: { 'cao-url': 'https://ops.example.com/central/', 'cao-repository-url': 'https://github.com/acme/control' }
      });
      expect(root?.querySelector('.cli-action-command')?.textContent).toContain('"cao-repository" is unavailable');
      expect(root?.querySelector('.cli-action-confirm')).toHaveProperty('disabled', true);
      expect(queue.enqueue).not.toHaveBeenCalled();
    }
  );

  it('shares the command summary context across shell replacements with independent lifetimes', async () => {
    const styles = document.createElement('style');
    styles.textContent = shellStyles;
    document.head.append(styles);
    const dashboards = Array.from({ length: 2 }, () => {
      const dashboard = document.createElement('div');
      const actions = document.createElement('div');
      actions.className = 'report-actions';
      dashboard.append(actions);
      document.body.append(dashboard);
      attachCliActions(dashboard, []);
      return dashboard;
    });
    const [first, second] = vi.mocked(subscribeCanonicalDashboardView).mock.calls;
    const navigations = dashboards.map((dashboard) => dashboard.querySelector('.cli-commands-navigation'));
    for (const navigation of navigations) {
      expect(navigation).toHaveProperty('hidden', true);
      expect(navigation && getComputedStyle(navigation).display).toBe('none');
    }
    expect(first[0]).toBe('cli-commands-navigation');
    expect(second[2]).toBe(first[2]);
    expect(first[5]?.signal).not.toBe(second[5]?.signal);
    /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */
    const sources = { 'cli-command-summary': {
      source: 'cli-command-summary', rows: [{ running: 2 }],
      metadata: {
        'source-id': 'cli-command-summary', 'source-kind': 'runtime-command-queue',
        'as-of': '2026-10-16T00:00:00Z', 'retrieved-at': '2026-10-16T00:00:00Z',
        completeness: 'complete', freshness: 'fresh'
      }
    } };
    first[3](sources);
    second[3](sources);
    for (const navigation of navigations) {
      expect(navigation).toHaveProperty('hidden', false);
      expect(navigation && getComputedStyle(navigation).display).toBe('inline-flex');
      expect(navigation?.textContent).toBe('');
      expect(navigation?.getAttribute('aria-label')).toBe('Commands running');
      expect(navigation?.getAttribute('title')).toBe('Commands running');
      expect(navigation?.querySelector('.octicon-terminal')?.getAttribute('aria-hidden')).toBe('true');
    }
    sources['cli-command-summary'].rows[0].running = 0;
    first[3](sources);
    second[3](sources);
    for (const navigation of navigations) expect(navigation).toHaveProperty('hidden', true);
    sources['cli-command-summary'].rows[0].running = 1;
    first[3](sources);
    second[3](sources);
    for (const navigation of navigations) {
      expect(navigation).toHaveProperty('hidden', false);
      expect(navigation?.textContent).toBe('');
      expect(navigation?.querySelector('.octicon-terminal')).not.toBeNull();
    }
    first[3]({});
    second[3]({});
    for (const navigation of navigations) expect(navigation).toHaveProperty('hidden', true);
    dashboards[0].remove();
    await vi.waitFor(() => expect(first[5]?.signal?.aborted).toBe(true));
    expect(second[5]?.signal?.aborted).toBe(false);
    dashboards[1].remove();
    await vi.waitFor(() => expect(second[5]?.signal?.aborted).toBe(true));
    styles.remove();
  });

  it('rejects UI-only declarations and prompt execution without sending a request', () => {
    const fetch = vi.fn();
    vi.stubGlobal('fetch', fetch);
    const action = { id: 'invalid-ui', level: /** @type {const} */ ('ui'), label: 'Log out',
      icon: 'sign-out', command: 'gh aw status', placement: /** @type {const} */ ('row') };
    expect(() => setDeclaredCliActions([action])).toThrow('reserved for native dashboard');
    expect(() => renderCliActions([action])).toThrow('reserved for native dashboard');
    setDeclaredCliActions([{ ...action, level: 'explore' }]);
    expect(() => renderRowCliAction(action.id, {}, { level: 'ui' })).toThrow('reserved for native dashboard');
    expect(() => createPromptCliActionControl(action.id, () => 'Log out', { level: 'ui' }))
      .toThrow('reserved for native dashboard');
    expect(fetch).not.toHaveBeenCalled();
  });

  it.each([
    { id: 'refresh', label: 'Refresh data', command: 'gh aw compile' },
    { id: 'upgrade-target-repository', label: 'Upgrade', command: 'gh aw upgrade --repo octo/example' }
  ])('requires explicit confirmation for $label and retains readable row labels and decorative icons', async ({ id, label, command }) => {
    const fetch = vi.fn().mockResolvedValue({
      ok: true,
      body: new ReadableStream({ start(controller) {
        controller.enqueue(new TextEncoder().encode('{"type":"complete","result":{"ok":true}}\n'));
        controller.close();
      } })
    });
    vi.stubGlobal('fetch', fetch);
    setDeclaredCliActions([{
      id,
      level: 'operate',
      label,
      icon: 'zap',
      command,
      placement: 'row'
    }]);
    const rendered = renderRowCliAction(id, {});
    expect(rendered?.querySelector('.octicon-zap')?.getAttribute('aria-hidden')).toBe('true');
    expect(rendered?.querySelector('.cli-action-trigger')?.textContent).toContain(label);
    rendered?.querySelector('.cli-action-trigger')?.dispatchEvent(new MouseEvent('click'));
    expect(fetch).not.toHaveBeenCalled();
    expect(rendered?.querySelector('.cli-action-command')?.textContent).toBe(command);
    expect(rendered?.querySelector('.cli-action-confirm')?.textContent).toBe(label);
    rendered?.querySelector('.cli-action-confirm')?.dispatchEvent(new MouseEvent('click'));
    await vi.waitFor(() => expect(queue.enqueue).toHaveBeenCalledTimes(1));
    expect(fetch).not.toHaveBeenCalled();
  });

  it('uses the action label without a confirmation prefix for operational prompts', () => {
    setDeclaredCliActions([{
      id: 'apply-fix', label: 'Apply fix', icon: 'zap',
      command: 'gh agent-task create --from-file -', placement: 'row'
    }]);
    const control = createPromptCliActionControl('apply-fix', () => 'Fix the issue', {
      level: 'operate', label: 'Apply fix'
    });
    expect(control?.button.textContent).toBe('Apply fix');
  });

  it('renders update actions with repository and pull-request creation', () => {
    const rendered = renderCliActions([{
      id: 'update-repository',
      label: 'Update repository',
      icon: 'sync',
      command: 'gh aw update --repo {{repository}}',
      arguments: [{
        id: 'create-pull-request',
        label: 'Create pull request',
        type: 'boolean',
        flag: '--create-pull-request',
        default: true
      }]
    }], {
      presentation: 'settings',
      templateValues: { repository: 'octo/example' }
    });
    rendered?.querySelector('.cli-action-trigger')?.dispatchEvent(new MouseEvent('click'));
    expect(rendered?.querySelector('.cli-action-command')?.textContent)
      .toBe('gh aw update --repo octo/example --create-pull-request');
  });

  it('renders settings actions as a settings section', () => {
    const rendered = renderCliActions([{
      id: 'upgrade-repository',
      label: 'Upgrade repository',
      icon: 'download',
      command: 'gh aw upgrade --repo {{repository}}',
      arguments: [{
        id: 'create-pull-request',
        label: 'Create pull request',
        type: 'boolean',
        flag: '--create-pull-request',
        default: true
      }]
    }], {
      presentation: 'settings',
      templateValues: { repository: 'octo/example' }
    });

    expect(rendered?.classList.contains('cli-actions-settings')).toBe(true);
    expect(rendered?.querySelector('#configuration-cli-actions-heading')?.textContent).toBe('Agentic Workflows');
    expect(rendered?.querySelector('.cli-action-trigger')?.classList.contains('configuration-cli-action')).toBe(true);
    expect(rendered?.querySelector('.cli-action-trigger')?.getAttribute('aria-label')).toBe('Upgrade repository');
    expect(rendered?.querySelector('.cli-action-trigger')?.getAttribute('title')).toBe('Upgrade repository');
    rendered?.querySelector('button')?.click();
    const checkbox = /** @type {HTMLInputElement} */ (
      rendered?.querySelector('.cli-action-argument input')
    );
    expect(checkbox.checked).toBe(true);
    expect(rendered?.querySelector('.cli-action-command')?.textContent)
      .toBe('gh aw upgrade --repo octo/example --create-pull-request');
  });

  it('keeps copy-only actions non-executable in every placement', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText }
    });
    const fetch = vi.fn();
    vi.stubGlobal('fetch', fetch);
    const rendered = renderCliActions([{
      id: 'copy-package-command',
      label: 'Copy add command',
      icon: 'copy',
      command: 'gh cao add octo/packages/demo@abc123',
      'copy-only': true
    }], { canExecute: true });

    rendered?.querySelector('.cli-action-trigger')?.dispatchEvent(new MouseEvent('click'));
    rendered?.querySelector('.cli-action-confirm')?.dispatchEvent(new MouseEvent('click'));

    await vi.waitFor(() => expect(writeText).toHaveBeenCalledWith(
      'gh cao add octo/packages/demo@abc123'
    ));
    expect(fetch).not.toHaveBeenCalled();
  });

  it('does not create an executable prompt control for copy-only actions', () => {
    setDeclaredCliActions([{
      id: 'copy-agent-prompt',
      label: 'Copy prompt',
      icon: 'copy',
      command: 'gh agent-task create --from-file -',
      placement: 'row',
      'copy-only': true
    }], { canExecute: true });

    expect(createPromptCliActionControl('copy-agent-prompt', () => 'prompt')).toBeNull();
  });

  it('attaches toolbar and settings actions to the dashboard shell', () => {
    const dashboard = document.createElement('div');
    const reportActions = document.createElement('div');
    reportActions.className = 'report-actions';
    const configurationView = document.createElement('section');
    configurationView.className = 'configuration-view';
    const configurationSetting = document.createElement('section');
    configurationSetting.className = 'configuration-browser-settings';
    configurationView.append(configurationSetting);
    dashboard.append(reportActions, configurationView);
    attachCliActions(dashboard, [
      {
        id: 'update-repository',
        label: 'Update repository',
        icon: 'sync',
        command: 'gh aw update --repo {{repository}}'
      },
      {
        id: 'upgrade-repository',
        label: 'Upgrade repository',
        icon: 'download',
        command: 'gh aw upgrade --repo {{repository}}',
        placement: 'settings'
      },
      {
        id: 'maintenance-update',
        label: 'Maintenance update',
        icon: 'sync',
        command: 'gh aw update --repo {{repository}}',
        placement: 'view'
      }
    ], { repository: 'octo/example', canExecute: false });

    expect(dashboard.querySelector('.report-actions > .cli-actions-menu')).not.toBeNull();
    expect(dashboard.querySelector('.configuration-view > .cli-actions-settings')).not.toBeNull();
    expect(dashboard.querySelectorAll('.cli-action-dialog')).toHaveLength(2);
    expect(dashboard.querySelectorAll(':scope > .cli-action-dialog')).toHaveLength(1);
    expect(dashboard.textContent).not.toContain('Maintenance update');
    dashboard.querySelector('.cli-action-trigger')?.dispatchEvent(new MouseEvent('click'));
    expect(dashboard.querySelector('.cli-action-command')?.textContent)
      .toBe('gh aw update --repo octo/example');
    expect(dashboard.querySelector('.cli-action-confirm')?.textContent).toContain('Copy command');
  });

  it('replaces settings actions with the latest dashboard declaration', () => {
    const dashboard = document.createElement('div');
    const reportActions = document.createElement('div');
    reportActions.className = 'report-actions';
    const configurationView = document.createElement('section');
    configurationView.className = 'configuration-view';
    const staleActions = document.createElement('section');
    staleActions.className = 'cli-actions-settings';
    configurationView.append(staleActions);
    dashboard.append(reportActions, configurationView);

    attachCliActions(dashboard, [{
      id: 'update-repository',
      label: 'Current update',
      icon: 'sync',
      command: 'gh aw update --repo {{repository}}',
      placement: 'settings'
    }], { repository: 'octo/current' });

    expect(dashboard.querySelectorAll('.configuration-view > .cli-actions-settings')).toHaveLength(1);
    expect(dashboard.querySelector('.configuration-view > .cli-actions-settings')?.textContent)
      .toContain('Current update');
  });

  it('copies commands instead of executing them outside canvas', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText }
    });
    const fetch = vi.fn();
    vi.stubGlobal('fetch', fetch);
    const rendered = renderCliActions([{
      id: 'compile-workflows',
      label: 'Compile workflows',
      icon: 'play',
      command: 'gh aw compile --strict',
      arguments: [{
        id: 'pre-releases',
        label: 'Include pre-releases',
        type: 'boolean',
        flag: '--pre-releases',
        default: true
      }]
    }], { canExecute: false });
    document.body.append(/** @type {HTMLElement} */ (rendered));

    rendered?.querySelector('.cli-action-trigger')?.dispatchEvent(new MouseEvent('click'));
    expect(rendered?.querySelector('.cli-action-dialog-body p')?.textContent)
      .toBe('Copy this command and run it in your terminal.');
    expect(rendered?.querySelector('.cli-action-output')).toBeNull();
    rendered?.querySelector('.cli-action-confirm')?.dispatchEvent(new MouseEvent('click'));

    await vi.waitFor(() => expect(rendered?.querySelector('.cli-action-status')?.textContent).toBe('Command copied.'));
    expect(writeText).toHaveBeenCalledWith('gh aw compile --strict --pre-releases');
    expect(fetch).not.toHaveBeenCalled();
  });

  it('uses Bash to make shell-script commands copyable from Windows terminals', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    vi.stubGlobal('navigator', { platform: 'Win32', clipboard: { writeText } });
    const rendered = renderCliActions([{
      id: 'update-repository',
      label: 'Update all',
      icon: 'sync',
      command: './scripts/update.sh update --repo {{repository}}',
      arguments: [{
        id: 'create-pull-request',
        label: 'Create pull request',
        type: 'boolean',
        flag: '--create-pull-request',
        default: true
      }]
    }], {
      canExecute: false,
      templateValues: { repository: 'octo/example' }
    });
    document.body.append(/** @type {HTMLElement} */ (rendered));

    rendered?.querySelector('.cli-action-trigger')?.dispatchEvent(new MouseEvent('click'));
    expect(rendered?.querySelector('.cli-action-command')?.textContent)
      .toBe('bash ./scripts/update.sh update --repo octo/example --create-pull-request');
    rendered?.querySelector('.cli-action-confirm')?.dispatchEvent(new MouseEvent('click'));

    await vi.waitFor(() => expect(writeText).toHaveBeenCalledWith(
      'bash ./scripts/update.sh update --repo octo/example --create-pull-request'
    ));
  });

  it('opens the durable command view after approval without attaching or cancelling in the dialog', async () => {
    const root = renderCliActions([{
      id: 'compile-workflows', label: 'Compile workflows', icon: 'play', command: 'gh aw compile'
    }]);
    if (!root) throw new Error('Expected a CLI action control.');
    document.body.append(root);
    const trigger = /** @type {HTMLButtonElement} */ (root.querySelector('.cli-action-trigger'));
    const confirm = /** @type {HTMLButtonElement} */ (root.querySelector('.cli-action-confirm'));
    expect(root.querySelector('input[type="checkbox"]')).toBeNull();
    expect(root.querySelector('.cli-action-terminal-dialog')).toBeNull();
    expect(root.textContent).not.toContain('Open terminal window');
    expect(root.textContent).not.toContain('Output appears here by default.');
    expect(root.textContent).not.toContain('Close terminal');
    expect(root.querySelector('.cli-action-dialog-body p code')?.textContent).toBe('gh aw');
    trigger.click();
    expect(queue.enqueue).not.toHaveBeenCalled();
    confirm.click();
    await vi.waitFor(() => expect(window.location.hash).toBe('#page-command?command=command-1'));
    expect(confirm.disabled).toBe(true);
    confirm.click();
    expect(queue.enqueue).toHaveBeenCalledTimes(1);
    expect(queue.enqueue.mock.calls[0][0].arguments).toEqual({});
    expect(root.querySelector('.cli-action-terminal')).toBeNull();
    expect(queue.attach).not.toHaveBeenCalled();
    expect(queue.cancel).not.toHaveBeenCalled();
    expect(root.querySelector('.cli-action-dialog')?.classList.contains('cli-action-running')).toBe(false);
    expect(root.querySelector('.cli-action-dialog')?.hasAttribute('open')).toBe(false);
    confirm.click();
    expect(queue.enqueue).toHaveBeenCalledTimes(1);
    trigger.click();
    expect(confirm.disabled).toBe(false);
    confirm.click();
    await vi.waitFor(() => expect(queue.enqueue).toHaveBeenCalledTimes(2));
    root.remove();
  });

  it('preserves approval and reports admission errors without navigating', async () => {
    /** @type {(value: unknown) => void} */
    let finish = () => {};
    queue.enqueue.mockImplementation(() => new Promise((resolve) => { finish = resolve; }));
    window.location.hash = 'page-maintenance';
    const root = renderCliActions([{
      id: 'compile-workflows', label: 'Compile workflows', icon: 'play', command: 'gh aw compile'
    }]);
    if (!root) throw new Error('Expected a CLI action control.');
    document.body.append(root);
    const trigger = /** @type {HTMLButtonElement} */ (root.querySelector('.cli-action-trigger'));
    const approval = /** @type {HTMLDialogElement} */ (root.querySelector('.cli-action-dialog'));
    trigger.click();
    /** @type {HTMLButtonElement} */ (root.querySelector('.cli-action-confirm')).click();
    await vi.waitFor(() => expect(queue.enqueue).toHaveBeenCalledTimes(1));
    expect(approval.open).toBe(true);
    expect(approval.querySelector('header button')?.getAttribute('aria-label')).toBe('Close action approval');
    finish({ id: 'command-1', status: 'queued' });
    await vi.waitFor(() => expect(window.location.hash).toBe('#page-command?command=command-1'));
    expect(approval.open).toBe(false);
    expect(document.activeElement).toBe(trigger);
    expect(queue.cancel).not.toHaveBeenCalled();
    root.remove();

    queue.enqueue.mockRejectedValueOnce(new Error('Queue admission unavailable.'));
    window.location.hash = 'page-maintenance';
    const failedRoot = renderCliActions([{
      id: 'compile-workflows', label: 'Compile workflows', icon: 'play', command: 'gh aw compile'
    }]);
    document.body.append(/** @type {HTMLElement} */ (failedRoot));
    failedRoot?.querySelector('.cli-action-trigger')?.dispatchEvent(new MouseEvent('click'));
    failedRoot?.querySelector('.cli-action-confirm')?.dispatchEvent(new MouseEvent('click'));
    await vi.waitFor(() => expect(failedRoot?.querySelector('.cli-action-status')?.textContent).toBe('Queue admission unavailable.'));
    expect(failedRoot?.querySelector('dialog')?.open).toBe(true);
    expect(window.location.hash).toBe('#page-maintenance');
    failedRoot?.remove();
  });

  it('requires a fresh confirmation before every execution', async () => {
    queue.attach.mockImplementation(async (_id, onOutput) => {
      onOutput({ stream: 'stdout', data: 'compiled\n' });
      return { result: { ok: true, exitCode: 0 }, command: { id: 'command-1', status: 'completed' } };
    });
    const streamingBody = () => {
      const encoder = new TextEncoder();
      return new ReadableStream({
        start(controller) {
          controller.enqueue(encoder.encode('{"type":"output","stream":"stdout","data":"comp"}\n'));
          controller.enqueue(encoder.encode('{"type":"output","stream":"stdout","data":"iled\\n"}\n'));
          controller.enqueue(encoder.encode('{"type":"complete","result":{"ok":true,"exitCode":0,"stdout":"","stderr":""}}\n'));
          controller.close();
        }
      });
    };
    const fetch = vi.fn().mockResolvedValue({
      ok: true,
      body: streamingBody()
    });
    vi.stubGlobal('fetch', fetch);
    const rendered = renderCliActions([{
      id: 'compile-workflows',
      label: 'Compile workflows',
      description: 'Validate workflows.',
      icon: 'play',
      command: 'gh aw compile --strict',
      arguments: [{
        id: 'pre-releases',
        label: 'Include pre-releases',
        type: 'boolean',
        flag: '--pre-releases',
        default: false
      }]
    }]);
    expect(rendered).not.toBeNull();
    const root = /** @type {HTMLElement} */ (rendered);
    document.body.append(root);
    const trigger = /** @type {HTMLButtonElement} */ (root.querySelector('.cli-action-trigger'));
    const confirm = /** @type {HTMLButtonElement} */ (root.querySelector('.cli-action-confirm'));
    const cancel = /** @type {HTMLButtonElement} */ (root.querySelector('.cli-action-detach'));

    trigger.click();
    expect(fetch).not.toHaveBeenCalled();
    expect(root.querySelector('.cli-action-dialog')?.hasAttribute('open')).toBe(true);
    expect(root.querySelector('.cli-action-command')?.textContent).toBe('gh aw compile --strict');
    const checkbox = /** @type {HTMLInputElement} */ (root.querySelector('.cli-action-argument input'));
    checkbox.click();
    expect(root.querySelector('.cli-action-command')?.textContent)
      .toBe('gh aw compile --strict --pre-releases');

    confirm.click();
    await vi.waitFor(() => expect(queue.enqueue).toHaveBeenCalledTimes(1));
    expect(queue.enqueue.mock.calls[0][0]).toEqual({
      id: 'compile-workflows',
      arguments: { 'pre-releases': true },
      idempotencyKey: expect.any(String)
    });
    await vi.waitFor(() => expect(window.location.hash).toBe('#page-command?command=command-1'));
    expect(root.querySelector('.cli-action-terminal')).toBeNull();
    expect(confirm.disabled).toBe(true);
    confirm.click();
    expect(queue.enqueue).toHaveBeenCalledTimes(1);

    cancel.click();
    trigger.click();
    expect(queue.enqueue).toHaveBeenCalledTimes(1);
    expect(checkbox.checked).toBe(false);
    expect(root.querySelector('.cli-action-command')?.textContent).toBe('gh aw compile --strict');
    fetch.mockResolvedValueOnce({ ok: true, body: streamingBody() });
    confirm.click();
    await vi.waitFor(() => expect(queue.enqueue).toHaveBeenCalledTimes(2));
  });

  it('renders a row action command with repository context', () => {
    setDeclaredCliActions([{
      id: 'update-target-repository',
      label: 'Update repository',
      icon: 'sync',
      command: 'gh aw update --repo {{repository}}'
    }]);
    const rendered = renderRowCliAction('update-target-repository', { repository: 'octo/example' });
    expect(rendered).not.toBeNull();
    if (!rendered) return;
    document.body.append(rendered);

    rendered.querySelector('button')?.click();

    expect(rendered.querySelector('.cli-action-command')?.textContent)
      .toBe('gh aw update --repo octo/example');
    expect(rendered.querySelector('.table-cli-action-button .octicon-sync')).not.toBeNull();
  });

  it('never executes a copy-only row action', async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, 'clipboard', {
      configurable: true,
      value: { writeText }
    });
    const fetch = vi.fn();
    vi.stubGlobal('fetch', fetch);
    const action = dashboardDocument.dashboard['cli-actions'].find(
      candidate => candidate.id === 'add-marketplace-package'
    );
    if (!action) throw new Error('Missing Marketplace package action.');
    setDeclaredCliActions([{
      id: action.id,
      label: action.label,
      icon: action.icon,
      command: action.command,
      placement: 'row',
      'copy-only': action['copy-only']
    }], { canExecute: true });
    const rendered = renderRowCliAction('add-marketplace-package', {
      'package-source': 'octo/packages/demo@abc123'
    });
    document.body.append(/** @type {HTMLElement} */ (rendered));

    rendered?.querySelector('button')?.click();
    expect(rendered?.querySelector('.cli-action-confirm')?.textContent).toContain('Copy command');
    rendered?.querySelector('.cli-action-confirm')?.dispatchEvent(new MouseEvent('click'));

    await vi.waitFor(() => expect(writeText).toHaveBeenCalledWith(
      'gh cao add octo/packages/demo@abc123'
    ));
    expect(fetch).not.toHaveBeenCalled();
  });

  it('is disabled by default (no debug output) when the debug query is absent', async () => {
    const output = { debug: vi.fn() };
    vi.doMock('../../src/debug.js', async () => {
      const actual = /** @type {typeof import('../../src/debug.js')} */ (
        await vi.importActual('../../src/debug.js')
      );
      return {
        ...actual,
        createDebug: (/** @type {string} */ category) => actual.createDebug(category, { search: () => '', output })
      };
    });
    vi.resetModules();
    const { renderCliActions: renderCliActionsWithoutDebug } = await import('../../src/components/cli-actions.js');

    const fetch = vi.fn().mockResolvedValue({
      ok: true,
      body: new ReadableStream({
        start(controller) {
          controller.enqueue(new TextEncoder().encode('{"type":"complete","result":{"ok":true,"stdout":"","stderr":""}}\n'));
          controller.close();
        }
      })
    });
    vi.stubGlobal('fetch', fetch);
    const rendered = renderCliActionsWithoutDebug([{
      id: 'compile-workflows',
      label: 'Compile workflows',
      icon: 'play',
      command: 'gh aw compile --strict'
    }]);
    document.body.append(/** @type {HTMLElement} */ (rendered));
    rendered?.querySelector('.cli-action-trigger')?.dispatchEvent(new MouseEvent('click'));
    rendered?.querySelector('.cli-action-confirm')?.dispatchEvent(new MouseEvent('click'));
    await vi.waitFor(() => expect(queue.enqueue).toHaveBeenCalledTimes(1));

    expect(output.debug).not.toHaveBeenCalled();

    vi.doUnmock('../../src/debug.js');
    vi.resetModules();
  });

  it('logs only scalar metadata under its predictable category when enabled', async () => {
    queue.enqueue.mockResolvedValueOnce({ id: 'command-1', status: 'queued' })
      .mockRejectedValueOnce(new Error('boom'));
    const output = { debug: vi.fn() };
    vi.doMock('../../src/debug.js', async () => {
      const actual = /** @type {typeof import('../../src/debug.js')} */ (
        await vi.importActual('../../src/debug.js')
      );
      return {
        ...actual,
        createDebug: (/** @type {string} */ category) =>
          actual.createDebug(category, { search: () => '?debug=cli-actions', output })
      };
    });
    vi.resetModules();
    const { renderCliActions: renderCliActionsWithDebug } = await import('../../src/components/cli-actions.js');

    const fetch = vi.fn()
      .mockResolvedValueOnce({
        ok: true,
        body: new ReadableStream({
          start(controller) {
            controller.enqueue(new TextEncoder().encode('{"type":"complete","result":{"ok":true,"stdout":"","stderr":""}}\n'));
            controller.close();
          }
        })
      })
      .mockResolvedValueOnce({ ok: false, json: async () => ({ error: 'boom' }) });
    vi.stubGlobal('fetch', fetch);
    const rendered = renderCliActionsWithDebug([{
      id: 'compile-workflows',
      label: 'Compile workflows',
      icon: 'play',
      command: 'gh aw compile --strict'
    }]);
    document.body.append(/** @type {HTMLElement} */ (rendered));

    rendered?.querySelector('.cli-action-trigger')?.dispatchEvent(new MouseEvent('click'));
    rendered?.querySelector('.cli-action-confirm')?.dispatchEvent(new MouseEvent('click'));
    await vi.waitFor(() => expect(output.debug).toHaveBeenCalledWith('[cao:cli-actions]', {
      event: 'execute-queued', actionId: 'compile-workflows'
    }));
    expect(output.debug).toHaveBeenCalledWith('[cao:cli-actions]', {
      event: 'execute-started', actionId: 'compile-workflows'
    });

    rendered?.querySelector('.cli-action-dialog-close')?.dispatchEvent(new MouseEvent('click'));
    rendered?.querySelector('.cli-action-trigger')?.dispatchEvent(new MouseEvent('click'));
    rendered?.querySelector('.cli-action-confirm')?.dispatchEvent(new MouseEvent('click'));
    await vi.waitFor(() => expect(output.debug).toHaveBeenCalledWith('[cao:cli-actions]', {
      event: 'execute-failed', actionId: 'compile-workflows', errorName: 'Error'
    }));

    for (const call of output.debug.mock.calls) {
      const metadata = call[1];
      expect(Object.values(metadata).every((value) => typeof value !== 'object')).toBe(true);
    }

    vi.doUnmock('../../src/debug.js');
    vi.resetModules();
  });

  it('stops updating the command preview after the control is detached', async () => {
    const rendered = renderCliActions([{
      id: 'compile-workflows',
      label: 'Compile workflows',
      icon: 'play',
      command: 'gh aw compile --strict',
      arguments: [{
        id: 'pre-releases',
        label: 'Include pre-releases',
        type: 'boolean',
        flag: '--pre-releases',
        default: false
      }]
    }]);
    const root = /** @type {HTMLElement} */ (rendered);
    document.body.append(root);

    root.querySelector('.cli-action-trigger')?.dispatchEvent(new MouseEvent('click'));
    const command = root.querySelector('.cli-action-command');
    const checkbox = /** @type {HTMLInputElement} */ (root.querySelector('.cli-action-argument input'));
    expect(command?.textContent).toBe('gh aw compile --strict');

    root.remove();
    await new Promise((resolve) => queueMicrotask(() => resolve(undefined)));
    checkbox.checked = true;
    checkbox.dispatchEvent(new Event('change'));

    expect(command?.textContent).toBe('gh aw compile --strict');
  });
});
