import { h } from '../dom.js';
import { octicon } from '../octicons.js';
import { cliActionTemplateFields, renderCliActionCommand } from '../cli-action-template.js';
import { createCopyControl, createModalDialog, renderCloseButton, renderLiveRegion } from './ui-primitives.js';
import { createDebug } from '../debug.js';
import { createFactoryScope } from './factory-elements.js';
import { effect, state } from '../reactive.js';
import { assertActionLevel, normalizeAction, actionPresentation } from '../action-model.js';
import { createDialogSelector } from './dialog-selector.js';
import { enqueueCliCommand, attachCliCommand, cliQueueAvailable, subscribeCanonicalDashboardView } from '../data-processor.js';
import { publishNotification } from '../notification-service.js';

const cliCommandSummaryContext = { pages: [] };
const debugCliActions = createDebug('cli-actions');

/** @type {Array<{ id: string, level?: import('../action-model.js').ActionLevel, verb?: string, label: string, description?: string, icon: string, command: string, 'preview-command'?: string, placement?: 'toolbar'|'settings'|'view'|'row', 'copy-only'?: boolean, selector?: import('../dialog-selector.js').DialogSelector, arguments?: Array<{ id: string, label: string, description?: string, type: 'boolean', flag: string, default?: boolean }> }>} */
let declaredCliActions = [];
let declaredCliActionsCanExecute = true;
/** @type {Record<string, string>} */
let declaredCliActionTemplateValues = {};
/** @type {Record<string, string>} */
let declaredCliActionContext = {};

/** @param {typeof declaredCliActions} actions @param {{ canExecute?: boolean, templateValues?: Record<string, string>, context?: Record<string, string> }} [options] */
export function setDeclaredCliActions(actions, options = {}) {
  declaredCliActions = Array.isArray(actions)
    ? actions.map((action) => ({ ...normalizeAction(action, { id: action.id, type: 'cli' }), ...action })) : [];
  declaredCliActionsCanExecute = options.canExecute !== false;
  declaredCliActionTemplateValues = options.templateValues ?? {};
  declaredCliActionContext = options.context ?? {};
}

/**
 * Resolve an interaction element's explicit action reference using the same host declarations.
 * @param {string} id
 * @param {Record<string, string>} values
 * @param {Record<string, boolean>} [args]
 */
export function declaredCliActionCommand(id, values, args = {}) {
  const action = declaredCliActions.find((candidate) => candidate.id === id);
  if (!action || action['copy-only'] === true) throw new Error('CLI action is unavailable.');
  return commandPreview(action, args, values, declaredCliActionContext);
}

/**
 * @param {string} id
 * @param {Record<string, string>} values
 * @param {{ arguments?: Record<string, boolean>, input?: string, signal: AbortSignal, onOutput: (event: { stream: 'stdout'|'stderr', data: string }) => void }} options
 */
export function executeDeclaredCliAction(id, values, options) {
  if (!declaredCliActionsCanExecute || !cliQueueAvailable()) throw new Error('Authorized local CLI actions are unavailable.');
  declaredCliActionCommand(id, values, options.arguments);
  return executeAction(id, options.arguments ?? {}, values, options.onOutput, options.input, options.signal);
}

/** @returns {string | undefined} */
export function declaredAgentTaskActionId() {
  return declaredCliActions.find((action) => action.command === 'gh agent-task create --from-file -'
    && action.placement === 'row')?.id;
}

/**
 * @param {string} id
 * @param {Record<string, boolean>} argumentsValue
 * @param {Record<string, string>} templateValues
 * @param {(event: { stream: 'stdout'|'stderr', data: string }) => void} onOutput
 * @param {string | undefined} [input]
 * @param {AbortSignal} [signal]
 */
async function executeAction(id, argumentsValue, templateValues, onOutput, input, signal) {
  /** @type {Record<string, unknown>} */
  const payload = { id, arguments: argumentsValue, idempotencyKey: crypto.randomUUID() };
  if (Object.keys(templateValues).length > 0) payload.values = templateValues;
  if (input !== undefined) payload.input = input;
  const command = await enqueueCliCommand(payload, signal);
  const complete = await attachCliCommand(command.id, (event) => {
    if (event.type === 'status') return;
    onOutput(/** @type {{ stream: 'stdout'|'stderr', data: string }} */ (event));
  }, signal);
  if (complete.command?.status === 'canceled') {
    return { ...complete.result, error: 'Cancelled. Completed changes are not undone.' };
  }
  return complete.result;
}

/** @param {{ stdout?: string, stderr?: string }} result */
function resultText(result) {
  const sections = [];
  if (result.stdout) sections.push(result.stdout.trimEnd());
  if (result.stderr) sections.push(result.stderr.trimEnd());
  return sections.filter(Boolean).join('\n') || 'Command completed without output.';
}

/**
 * Create the canvas-only control that starts an agent task from a generated prompt.
 * @param {string} actionId
 * @param {() => string} getPrompt
 * @param {{ level?: import('../action-model.js').ActionLevel, label?: string }} [options]
 */
export function createPromptCliActionControl(actionId, getPrompt, options = {}) {
  assertActionLevel(options.level ?? 'propose', 'prompt');
  const action = declaredCliActions.find((candidate) => candidate.id === actionId);
  if (!declaredCliActionsCanExecute || !cliQueueAvailable() || !action || action['copy-only'] === true) return null;
  const scope = createFactoryScope();
  const status = /** @type {HTMLOutputElement} */ (renderLiveRegion('output', 'table-intent-copy-status'));
  const output = h('pre', { className: 'cli-action-output', hidden: true });

  // `busy`, `statusText`, `outputVisible`, and `outputText` are this control's
  // entire visible state; the effect below is the only place that writes
  // them onto the owned `button`/`status`/`output` nodes.
  const busy = state(false);
  const statusText = state('');
  const outputVisible = state(false);
  const outputText = state('');

  const button = /** @type {HTMLButtonElement} */ (h('button', {
    type: 'button',
    className: 'table-intent-copy-button',
    onClick: async () => {
      if (scope.signal.aborted || busy.get()) return;
      busy.set(true);
      statusText.set('Starting agent task…');
      outputText.set('');
      outputVisible.set(true);
      try {
        const result = await executeAction(action.id, {}, {}, ({ data }) => {
          if (scope.signal.aborted) return;
          outputText.set((current) => current + data);
          output.scrollTop = output.scrollHeight;
        }, getPrompt(), scope.signal);
        if (scope.signal.aborted) return;
        statusText.set(result.ok ? 'Agent task started.' : (result.error || 'Could not start agent task.'));
        if (!outputText.get()) outputText.set(resultText(result));
      } catch (error) {
        if (scope.signal.aborted) return;
        statusText.set(error instanceof Error ? error.message : 'Could not start agent task.');
      } finally {
        if (!scope.signal.aborted) busy.set(false);
      }
    }
  }, options.level === 'operate' ? options.label ?? action.label : 'Start agent task'));
  effect(() => {
    button.disabled = busy.get();
    status.textContent = statusText.get();
    output.hidden = !outputVisible.get();
    output.textContent = outputText.get();
  }, { signal: scope.signal });
  scope.bind(button);
  return {
    button,
    status,
    output,
    reset() {
      statusText.set('');
      outputText.set('');
      outputVisible.set(false);
      busy.set(false);
    }
  };
}

/**
 * @param {{ command: string, arguments?: Array<{ id: string, flag: string }> }} action
 * @param {Record<string, boolean>} values
 * @param {Record<string, string>} templateValues
 * @param {Record<string, string>} context
 */
function commandPreview(action, values, templateValues, context) {
  const command = [
    renderCliActionCommand(action.command, templateValues, context),
    ...(action.arguments ?? []).filter((argument) => values[argument.id]).map((argument) => argument.flag)
  ].join(' ');
  const userAgentData = typeof navigator === 'undefined'
    ? undefined
    : /** @type {{ platform?: unknown }} */ (Reflect.get(navigator, 'userAgentData'));
  const platform = typeof userAgentData?.platform === 'string'
    ? userAgentData.platform
    : typeof navigator === 'undefined' ? '' : navigator.platform || navigator.userAgent;
  const userAgent = typeof navigator === 'undefined' ? '' : navigator.userAgent;
  // Windows terminals need an explicit Bash interpreter; leave other commands untouched.
  return (/^win/i.test(platform) || /windows/i.test(userAgent))
    && /^(?:"[^"]+\.sh"|'[^']+\.sh'|\S+\.sh)(?:\s|$)/.test(command)
    ? `bash ${command}`
    : command;
}

/**
 * @param {{ id: string, label: string, description?: string, icon: string, command: string, 'preview-command'?: string, 'copy-only'?: boolean, selector?: import('../dialog-selector.js').DialogSelector, arguments?: Array<{ id: string, label: string, description?: string, type: 'boolean', flag: string, default?: boolean }> }} action
 * @param {{ presentation?: 'menu'|'settings'|'row', templateValues?: Record<string, string>, context?: Record<string, string>, canExecute?: boolean, showRowLabel?: boolean, batch?: { targets: Array<Record<string, string>>, error?: string, warning?: string } }} [options]
 */
function renderCliActionControl(action, options = {}) {
  const presentation = actionPresentation(normalizeAction(action, { id: action.id, type: 'cli' }));
  const settingsPresentation = options.presentation === 'settings';
  const rowPresentation = options.presentation === 'row';
  const canExecute = options.canExecute !== false && cliQueueAvailable() && action['copy-only'] !== true;
  const confirmationLabel = presentation.label;
  const templateValues = { ...options.templateValues };
  const context = options.context ?? declaredCliActionContext;
  const fields = cliActionTemplateFields(action.command);
  const missingContext = cliActionTemplateFields(action.command, { includeContext: true })
    .find((field) => !fields.includes(field) && !context[field]);
  const commandError = options.batch?.error ?? (missingContext
    ? `CAO command context "${missingContext}" is unavailable. Configure the public CAO URL and control repository.`
    : undefined);
  const templateTargets = options.batch?.targets ?? [templateValues];
  const preview = () => commandError ?? (action.selector && !templateValues[action.selector.mapping.target]
    ? 'Select a repository to review the command.'
    : templateTargets.map((values) => commandPreview(action, argumentValues, values, context)).join('\n'));
  const argumentValues = Object.fromEntries(
    (action.arguments ?? []).map((argument) => [argument.id, argument.default === true])
  );
  /** @type {HTMLButtonElement} */
  let trigger;
  /** @type {HTMLElement} */
  let command;
  const commandPreviewText = state(preview());
  const inputs = (action.arguments ?? []).map((argument) => {
    const input = /** @type {HTMLInputElement} */ (h('input', {
      type: 'checkbox',
      checked: argument.default === true,
      onChange: (/** @type {Event} */ event) => {
        argumentValues[argument.id] = /** @type {HTMLInputElement} */ (event.currentTarget).checked;
        commandPreviewText.set(preview());
      }
    }));
    return {
      argument,
      input,
      element: h(
        'label',
        { className: 'cli-action-argument' },
        input,
        h(
          'span',
          null,
          h('strong', null, argument.label),
          argument.description ? h('small', null, argument.description) : null
        )
      )
    };
  });
  const resetArguments = () => {
    for (const { argument, input } of inputs) {
      const checked = argument.default === true;
      argumentValues[argument.id] = checked;
      input.checked = checked;
    }
    commandPreviewText.set(preview());
  };
  const { dialog, open, close: closeModal } = createModalDialog({
    className: 'cli-action-dialog',
    ariaLabel: `Approve ${action.label}`,
    onFallbackClose: () => trigger.focus()
  });
  const close = () => {
    selector?.cancel();
    closeModal();
  };
  const copyControl = canExecute ? null : createCopyControl({
    getContent: () => commandPreviewText.get(),
    label: 'Copy command',
    buttonClassName: 'cli-action-confirm',
    statusClassName: 'cli-action-status',
    successText: 'Command copied.',
    failureText: 'Could not copy command.',
    trackState: true
  });
  const status = copyControl?.status ?? /** @type {HTMLOutputElement} */ (renderLiveRegion('output', 'cli-action-status'));
  const scope = createFactoryScope();
  const busy = state(false);
  const statusText = state('');
  const enqueued = state(false);
  const selectionReady = state(!action.selector && !commandError);
  const selector = action.selector ? createDialogSelector(action.id, action.selector, {
    signal: scope.signal,
    select: (value) => {
      const target = action.selector?.mapping.target;
      if (!target) return;
      statusText.set('');
      if (value === null) delete templateValues[target];
      else templateValues[target] = value;
      try {
        commandPreviewText.set(preview());
        selectionReady.set(value !== null && !commandError);
      } catch (error) {
        delete templateValues[target];
        selectionReady.set(false);
        statusText.set(error instanceof Error ? error.message : 'Invalid selection.');
        commandPreviewText.set(preview());
      }
    }
  }) : null;

  const enqueueBatch = async () => {
    if (busy.get() || commandError) return;
    busy.set(true);
    try {
      const results = await Promise.allSettled(templateTargets.map((values) => enqueueCliCommand({
        id: action.id,
        arguments: { ...argumentValues },
        values,
        idempotencyKey: crypto.randomUUID()
      }, scope.signal)));
      if (scope.signal.aborted) return;
      const admitted = results.filter((result) => result.status === 'fulfilled').length;
      const failed = results.length - admitted;
      publishNotification({
        message: failed
          ? `${action.label}: ${admitted} commands queued; ${failed} could not be queued.`
          : `${action.label}: ${admitted} commands queued.`,
        href: '#page-commands',
        tone: failed ? 'error' : 'success'
      });
      if (admitted) {
        enqueued.set(true);
        close();
        window.location.hash = 'page-commands';
      }
    } finally {
      busy.set(false);
    }
  };
  const cancel = /** @type {HTMLButtonElement} */ (h('button', {
    type: 'button',
    className: 'cli-action-cancel cli-action-detach',
    onClick: close
  }, 'Cancel'));
  const confirm = copyControl?.button ?? /** @type {HTMLButtonElement} */ (h('button', {
      type: 'button',
      className: 'cli-action-confirm',
      onClick: async () => {
        if (busy.get() || enqueued.get() || !selectionReady.get()) return;
        if (options.batch) {
          await enqueueBatch();
          return;
        }
        busy.set(true);
        statusText.set('Queuing command…');
        debugCliActions({ event: 'execute-started', actionId: action.id });
        try {
          const command = await enqueueCliCommand({
            id: action.id,
            arguments: { ...argumentValues },
            ...(Object.keys(templateValues).length ? { values: { ...templateValues } } : {}),
            idempotencyKey: crypto.randomUUID()
          }, scope.signal);
          if (scope.signal.aborted) return;
          enqueued.set(true);
          debugCliActions({ event: 'execute-queued', actionId: action.id });
          close();
          window.location.hash = `page-command?command=${encodeURIComponent(command.id)}`;
        } catch (error) {
          if (scope.signal.aborted) return;
          const message = error instanceof Error ? error.message : 'Could not queue command.';
          statusText.set(message);
          debugCliActions({
            event: 'execute-failed',
            actionId: action.id,
            errorName: error instanceof Error ? error.name : 'UnknownError'
          });
        } finally {
          busy.set(false);
        }
      }
    }, 'Run action'));
  if (commandError) confirm.disabled = true;
  if (canExecute) {
    effect(() => {
      confirm.disabled = busy.get() || enqueued.get() || !selectionReady.get();
      cancel.disabled = busy.get();
      for (const { input } of inputs) input.disabled = busy.get();
      selector?.setDisabled(busy.get());
      status.textContent = statusText.get();
      confirm.textContent = confirmationLabel;
    }, { signal: scope.signal });
  }
  trigger = /** @type {HTMLButtonElement} */ (h(
    'button',
    {
      type: 'button',
      className: rowPresentation
        ? 'cli-action-trigger table-cli-action-button'
        : settingsPresentation
        ? 'cli-action-trigger configuration-cli-action'
        : 'cli-action-trigger',
      title: action.label,
      'aria-label': action.label,
      onClick: async () => {
        if (options.batch && canExecute && inputs.length === 0) {
          await enqueueBatch();
          return;
        }
        copyControl?.reset();
        if (canExecute) {
          statusText.set('');
          enqueued.set(false);
        }
        resetArguments();
        selector?.reset();
        open();
        selector?.focus();
      }
    },
    octicon(presentation.icon),
    h(
      'span',
      { className: 'cli-action-trigger-copy' },
      h('strong', null, presentation.label),
      !rowPresentation && action.description ? h('small', null, action.description) : null
    )
  ));
  if (options.batch && canExecute) {
    effect(() => {
      trigger.disabled = busy.get() || Boolean(options.batch?.error);
      if (busy.get()) trigger.setAttribute('aria-busy', 'true');
      else trigger.removeAttribute('aria-busy');
      trigger.title = options.batch?.error ?? action.label;
    }, { signal: scope.signal });
  }
  command = h('code', { className: 'cli-action-command' }, commandPreviewText.get());
  const compactCommand = options.batch && action['preview-command'] && !commandError
    ? h('code', { className: 'cli-action-preview-command' })
    : null;
  const commandDetails = compactCommand ? h('details', { className: 'cli-action-command-details' },
    h('summary', null, `Show all ${templateTargets.length} full commands`),
    command
  ) : null;
  effect(() => {
    command.textContent = commandPreviewText.get();
    if (compactCommand) {
      compactCommand.textContent = commandPreview(
        { ...action, command: action['preview-command'] ?? action.command },
        argumentValues, templateTargets[0], context
      );
    }
  }, { signal: scope.signal });
  dialog.append(
    h(
      'header',
      { className: 'cli-action-dialog-header' },
      h('h2', null, presentation.label),
      renderCloseButton({
        className: 'cli-action-dialog-close',
        label: 'Close action approval',
        onClick: close
      })
    ),
    h(
      'div',
      { className: 'cli-action-dialog-body' },
      action.description ? h('p', null, action.description) : null,
      options.batch?.warning ? h('p', { className: 'view-metadata', role: 'note' }, options.batch.warning) : null,
      selector?.root,
      inputs.length > 0
        ? h('fieldset', { className: 'cli-action-arguments' },
          h('legend', null, 'Options'),
          ...inputs.map(({ element }) => element))
        : null,
      action.selector ? null : h('p', null, canExecute
        ? presentation.confirmation
          ? [
            `Confirm the exact effect of ${presentation.label} on the selected targets. Each listed command will execute once after approval. Actions that use `,
            h('code', null, 'gh aw'),
            ' may install the pinned extension first.'
          ]
          : 'This command is declared as read-only investigation. Review its exact target and command before running.'
        : compactCommand ? 'Copy all commands and run them in your terminal.' : 'Copy this command and run it in your terminal.'),
      compactCommand ? h('p', null, `Preview for the first of ${templateTargets.length} targets:`) : null,
      compactCommand,
      commandDetails ?? command
    ),
    h('footer', { className: 'cli-action-dialog-footer' }, status, cancel, confirm)
  );
  dialog.addEventListener('close', () => {
    selector?.cancel();
    commandDetails?.removeAttribute('open');
    trigger.focus();
  });
  dialog.addEventListener('cancel', (event) => {
    event.preventDefault();
    close();
  });
  scope.bind(trigger);
  return { trigger, dialog };
}

/**
 * Render one row-scoped CLI action with template values sourced from the row.
 * @param {string} actionId
 * @param {Record<string, string>} templateValues
 * @param {{ showLabel?: boolean, level?: import('../action-model.js').ActionLevel, verb?: string, label?: string, icon?: string }} [options]
 */
export function renderRowCliAction(actionId, templateValues, options = {}) {
  const action = declaredCliActions.find((candidate) => candidate.id === actionId);
  if (!action) return null;
  const { trigger, dialog } = renderCliActionControl({
    ...action,
    ...(options.level && { level: options.level }),
    ...(options.verb && { verb: options.verb }),
    ...(options.label && { label: options.label }),
    ...(options.icon && { icon: options.icon })
  }, {
    presentation: 'row',
    templateValues,
    canExecute: declaredCliActionsCanExecute,
    showRowLabel: options.showLabel === true
  });
  return h('span', { className: 'table-cli-action-control' }, trigger, dialog);
}

/**
 * Render one declared CLI action with its authored label and description.
 * @param {string} actionId
 * @param {Record<string, string>} [templateValues]
 * @param {{ rows: Array<Record<string, unknown>>, fields: string[], available: boolean, warning?: string }} [binding]
 */
export function renderDeclaredCliAction(actionId, templateValues = {}, binding) {
  const action = declaredCliActions.find((candidate) => candidate.id === actionId);
  if (!action || action.selector && !declaredCliActionsCanExecute) return null;
  /** @type {{ targets: Array<Record<string, string>>, error?: string, warning?: string } | undefined} */
  let batch;
  if (binding) {
    batch = { targets: [], warning: binding.warning };
    try {
      if (!binding.available || binding.rows.length === 0) {
        throw new Error('The complete target list is unavailable. No commands can be approved.');
      }
      for (const row of binding.rows) {
        const values = Object.fromEntries(binding.fields.map((field) => {
          const value = row[field];
          if (typeof value !== 'string') throw new Error(`CLI action context "${field}" is missing or invalid.`);
          return [field, value];
        }));
        renderCliActionCommand(action.command, values, declaredCliActionContext);
        batch.targets.push(values);
      }
    } catch (error) {
      batch.error = error instanceof Error ? error.message : 'The command target list is invalid.';
      batch.targets = [];
    }
  }
  const rendered = renderCliActionControl(action, {
    templateValues: { ...declaredCliActionTemplateValues, ...templateValues },
    canExecute: declaredCliActionsCanExecute,
    batch
  });
  return h('span', { className: 'declared-cli-action' }, rendered.trigger, rendered.dialog);
}

/**
 * Render dashboard-declared CLI actions. Every invocation requires a fresh,
 * explicit confirmation; approval is never persisted or inferred.
 * @param {Array<{ id: string, label: string, description?: string, icon: string, command: string, 'preview-command'?: string, 'copy-only'?: boolean, selector?: import('../dialog-selector.js').DialogSelector, arguments?: Array<{ id: string, label: string, description?: string, type: 'boolean', flag: string, default?: boolean }> }> | undefined} actions
 * @param {{ presentation?: 'menu'|'settings', templateValues?: Record<string, string>, context?: Record<string, string>, canExecute?: boolean }} [options]
 * @returns {HTMLElement | null}
 */
export function renderCliActions(actions, options = {}) {
  if (!Array.isArray(actions) || actions.length === 0) return null;
  const visibleActions = actions.filter((action) => !action.selector || options.canExecute !== false);
  if (!visibleActions.length) return null;

  const settingsPresentation = options.presentation === 'settings';
  const settingsList = settingsPresentation
    ? h('div', { className: 'configuration-cli-action-list' })
    : null;
  const root = settingsPresentation
    ? h(
      'section',
      {
        className: 'configuration-browser-settings cli-actions-settings',
        'aria-labelledby': 'configuration-cli-actions-heading'
      },
      h(
        'div',
        { className: 'configuration-browser-settings-heading' },
        h(
          'div',
          null,
          h('h3', { id: 'configuration-cli-actions-heading' }, 'Agentic Workflows'),
          h('p', null, 'Update installed campaigns or upgrade this repository’s Agentic Workflows.')
        )
      ),
      settingsList
    )
    : h(
      'details',
      { className: 'cli-actions-menu' },
      h(
        'summary',
        { className: 'cli-actions-toggle', title: 'Dashboard actions' },
        octicon('play'),
        h('span', { className: 'action-label' }, 'Actions')
      )
    );
  const list = settingsPresentation
    ? settingsList
    : h('div', { className: 'cli-actions-list' });
  if (!list) return null;
  if (!settingsPresentation) root.append(list);

  for (const declared of visibleActions) {
    const action = { ...normalizeAction(declared, { id: declared.id, type: 'cli' }), ...declared };
    const { trigger, dialog } = renderCliActionControl(action, options);
    list.append(trigger);
    root.append(dialog);
  }
  return root;
}

export function renderSettingsCliActions() {
  return renderCliActions(
    declaredCliActions.filter((action) => action.placement === 'settings'),
    {
      presentation: 'settings',
      templateValues: declaredCliActionTemplateValues,
      canExecute: declaredCliActionsCanExecute
    }
  );
}

/**
 * Attach dashboard-declared CLI actions to their toolbar and settings placements.
 * View and row actions are rendered by their declaring views.
 * @param {HTMLElement} dashboard
 * @param {typeof declaredCliActions} actions
 * @param {{ repository?: string, context?: Record<string, string>, canExecute?: boolean }} [options]
 */
export function attachCliActions(dashboard, actions, options = {}) {
  const canExecute = options.canExecute !== false;
  /** @type {Record<string, string>} */
  const templateValues = {};
  if (typeof options.repository === 'string') templateValues.repository = options.repository;
  setDeclaredCliActions(actions, { canExecute, templateValues, context: options.context });
  const toolbarActions = renderCliActions(
    actions.filter((action) => (action.placement ?? 'toolbar') === 'toolbar'),
    { templateValues, canExecute }
  );
  if (toolbarActions) dashboard.querySelector('.report-actions')?.prepend(toolbarActions);
  if (canExecute && cliQueueAvailable() && !dashboard.querySelector('.cli-commands-navigation')) {
    const scope = createFactoryScope();
    const navigation = h('a', {
      href: '#page-commands', className: 'cli-actions-toggle cli-commands-navigation',
      'aria-label': 'Commands running', title: 'Commands running', hidden: true,
      dataset: { mobileNavPageId: 'commands' }
    }, octicon('terminal'));
    dashboard.querySelector('.report-actions')?.prepend(navigation);
    scope.bind(navigation);
    subscribeCanonicalDashboardView('cli-commands-navigation', ['cli-command-summary'], cliCommandSummaryContext, (sources) => {
      const running = sources['cli-command-summary']?.rows[0]?.running;
      navigation.hidden = !(typeof running === 'number' && running > 0);
    }, undefined, { signal: scope.signal });
  }

  const settingsActions = renderSettingsCliActions();
  const existingSettingsActions = dashboard.querySelector('.configuration-view > .cli-actions-settings');
  if (!settingsActions) {
    existingSettingsActions?.remove();
    return;
  }
  const dialogs = [...settingsActions.querySelectorAll('dialog')];
  if (existingSettingsActions) {
    existingSettingsActions.replaceWith(settingsActions);
  } else {
    dashboard.querySelector('.configuration-view > .configuration-browser-settings')?.before(settingsActions);
  }
  for (const dialog of dialogs) dashboard.append(dialog);
}
