import { h } from '../dom.js';
import { octicon } from '../octicons.js';
import { state, effect } from '../reactive.js';
import { attachCliCommand, retryCliCommand, requestCliQueue, cliQueueAvailable } from '../data-processor.js';
import { createFactoryScope } from './factory-elements.js';
import { createCliTerminal } from './cli-terminal.js';
import { copyTextToClipboard } from './ui-primitives.js';
import { publishNotification } from '../notification-service.js';
import { createDebug, diagnosticErrorName } from '../debug.js';

const debugCommandTerminal = createDebug('command-terminal');

/** @param {import('./ui-elements.js').ElementRenderContext} context */
export function renderCommandTerminal(context) {
  const scope = createFactoryScope();
  const id = context.routeParameters?.command;
  const message = state('Loading command…');
  const title = state(context.title);
  const status = state('');
  const busy = state(false);
  const pullRequestUrl = state('');
  const hasOutput = state(false);
  let transcript = '';
  const output = h('div', { className: 'cli-action-terminal-output' });
  const statusNode = h('output', { className: 'sr-only', role: 'status', 'aria-live': 'polite' });
  /** @type {AbortController | undefined} */
  let attachment;
  /** @type {Awaited<ReturnType<typeof createCliTerminal>> | undefined} */
  let terminal;
  /** @type {Promise<Awaited<ReturnType<typeof createCliTerminal>>> | undefined} */
  let terminalPromise;
  let currentId = id;
  const write = (/** @type {string} */ data, /** @type {'stdout'|'stderr'} */ stream = 'stdout') => {
    transcript += data;
    terminal?.write(data, stream);
    hasOutput.set(Boolean(transcript));
  };
  const attach = async () => {
    if (!currentId || scope.signal.aborted) return;
    attachment?.abort();
    const controller = new AbortController();
    attachment = controller;
    const abort = () => controller.abort();
    scope.signal.addEventListener('abort', abort, { once: true });
    try {
      const command = await requestCliQueue({ action: 'get', commandId: currentId }, { signal: controller.signal });
      if (controller.signal.aborted) return;
      status.set(command.status);
      pullRequestUrl.set(command.result?.pullRequestUrl || '');
      title.set(command.command || context.title);
      message.set(`${command.id} · ${command.status}`);
      terminalPromise ??= createCliTerminal(scope.signal).catch((error) => {
        terminalPromise = undefined;
        throw error;
      });
      const mountedTerminal = await terminalPromise;
      if (controller.signal.aborted) return;
      terminal = mountedTerminal;
      output.append(terminal.root);
      terminal.reset();
      transcript = '';
      hasOutput.set(false);
      if (command.command) write(`$ ${command.command}\r\n`);
      const complete = await attachCliCommand(currentId, (event) => {
        if (controller.signal.aborted) return;
        if (event.type === 'status') {
          status.set(event.command.status);
          message.set(`${event.command.id} · ${event.command.status}`);
        } else write(event.data, event.stream);
      }, controller.signal);
      if (controller.signal.aborted) return;
      status.set(complete.command.status);
      pullRequestUrl.set(complete.result.pullRequestUrl || complete.command.result?.pullRequestUrl || '');
      message.set(`${complete.command.id} · ${complete.command.status}`);
      debugCommandTerminal({ event: 'attach', outcome: 'complete', status: complete.command.status, failed: Boolean(complete.result.error) });
      if (complete.result.error) write(`\r\n${complete.result.error}\r\n`, 'stderr');
    } catch (error) {
      if (!controller.signal.aborted) {
        debugCommandTerminal({ event: 'attach', outcome: 'error', error: diagnosticErrorName(error) });
        const text = error instanceof Error ? error.message : 'Command output unavailable.';
        message.set(text);
        if (terminal) write(`\r\n${text}\r\n`, 'stderr');
      }
    } finally {
      scope.signal.removeEventListener('abort', abort);
    }
  };
  const copy = h('button', {
    type: 'button', className: 'cli-action-confirm command-terminal-copy',
    onClick: async () => {
      if (busy.get() || scope.signal.aborted) return;
      busy.set(true);
      try {
        const url = pullRequestUrl.get();
        const text = url || terminal?.plainText(transcript);
        const copied = text !== undefined && await copyTextToClipboard(text);
        if (scope.signal.aborted) return;
        publishNotification({
          message: copied ? (url ? 'Pull request URL copied.' : 'Command output copied.') : 'Copy unavailable.',
          tone: copied ? 'success' : 'error'
        });
      } finally { busy.set(false); }
    }
  }, octicon('copy'), 'Copy');
  const retry = h('button', {
    type: 'button', className: 'cli-action-confirm command-terminal-retry',
    onClick: async () => {
      if (!currentId || busy.get() || scope.signal.aborted) return;
      busy.set(true);
      try {
        const command = await retryCliCommand(currentId, scope.signal);
        if (scope.signal.aborted) return;
        const previousId = currentId;
        currentId = command.id;
        window.location.hash = `page-command?command=${encodeURIComponent(command.id)}`;
        if (previousId === currentId) void attach();
      } catch (error) {
        if (!scope.signal.aborted) publishNotification({ message: error instanceof Error ? error.message : 'Retry failed.', tone: 'error' });
      } finally { busy.set(false); }
    }
  }, 'Retry command');
  const pullRequest = h('a', {
    className: 'cli-action-confirm', target: '_blank', rel: 'noopener noreferrer'
  }, 'Open pull request');
  const actions = h('div', { className: 'command-terminal-actions' }, pullRequest, copy, retry);
  const root = h('section', { className: 'command-terminal', 'aria-label': 'Command console log' },
    statusNode,
    h('div', { className: 'cli-action-dialog-body' }, output),
    actions
  );
  effect(() => {
    statusNode.textContent = message.get();
    root.dispatchEvent(new CustomEvent('dashboard-route-allocation', {
      bubbles: true,
      detail: { title: title.get(), description: message.get() }
    }));
    retry.hidden = !['failed', 'timed-out'].includes(status.get());
    const url = pullRequestUrl.get();
    pullRequest.hidden = !url;
    if (url) pullRequest.setAttribute('href', url);
    else pullRequest.removeAttribute('href');
    copy.setAttribute('aria-label', url ? 'Copy pull request URL' : 'Copy command output');
    /** @type {HTMLButtonElement} */ (copy).disabled = busy.get() || (!url && !hasOutput.get());
    /** @type {HTMLButtonElement} */ (retry).disabled = busy.get();
  }, { signal: scope.signal });
  scope.bind(root);
  if (!cliQueueAvailable()) {
    message.set('Commands are available only in an authorized local admin or canvas dashboard.');
  } else if (!id) {
    message.set('Select a command from Maintenance → Commands.');
  } else void attach();
  return root;
}
