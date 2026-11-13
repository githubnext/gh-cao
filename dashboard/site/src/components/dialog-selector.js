import { h } from '../dom.js';
import { debounce } from '../debounce.js';
import { createDebug, diagnosticErrorName } from '../debug.js';
import { effect, render, state } from '../reactive.js';
import { renderLiveRegion } from './ui-primitives.js';
import { REPOSITORY_PATTERN } from '../dialog-selector.js';

const debugSelector = createDebug('components:dialog-selector');

/**
 * A transient external selector: its server-declared query and mapping own the
 * choices. This component only presents them and manages local selection.
 * @param {string} actionId
 * @param {import('../dialog-selector.js').DialogSelector} definition
 * @param {{ signal: AbortSignal, select: (value: string | null) => void }} options
 */
export function createDialogSelector(actionId, definition, { signal, select }) {
  const choices = state(/** @type {Array<{ value: string, label: string, description: string }>} */ ([]));
  const selected = state(/** @type {string | null} */ (null));
  const message = state('Type at least 2 characters to search GitHub repositories.');
  const busy = state(false);
  const disabled = state(false);
  let generation = 0;
  /** @type {AbortController | undefined} */
  let request;
  /** @type {ReturnType<typeof setTimeout> | undefined} */
  let slowSearchTimer;
  const clearSlowSearch = () => {
    clearTimeout(slowSearchTimer);
    slowSearchTimer = undefined;
  };
  const invalidate = () => {
    generation += 1;
    request?.abort();
    request = undefined;
    lookup.cancel();
    clearSlowSearch();
    busy.set(false);
  };
  const lookup = debounce(async (/** @type {string} */ text) => {
    const current = generation;
    request = new AbortController();
    busy.set(true);
    debugSelector({ event: 'search.start', textLength: text.length });
    message.set('Searching repositories...');
    slowSearchTimer = setTimeout(() => {
      if (current !== generation || signal.aborted) return;
      message.set('GitHub search is taking longer than usual. You can keep typing to refine your search or cancel.');
    }, 2000);
    try {
      const response = await fetch('./__dialog_selector', {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ id: actionId, text }), signal: request.signal
      });
      const result = await response.json();
      if (!response.ok) throw new Error(result.error || 'Repository search failed.');
      if (current !== generation || signal.aborted) return;
      choices.set(result.options);
      debugSelector({ event: 'search.result', status: 'ok', count: result.options.length, limit: definition.query.limit });
      message.set(result.options.length === definition.query.limit
        ? `Showing the first ${definition.query.limit} repositories. Refine your search to narrow the results.`
        : result.options.length ? 'Select a repository below.' : 'No repositories found.');
    } catch (error) {
      if (current !== generation || signal.aborted) return;
      debugSelector({ event: 'search.result', status: 'error', error: diagnosticErrorName(error) });
      message.set(error instanceof Error ? error.message : 'Repository search failed.');
    } finally {
      if (current === generation && !signal.aborted) {
        clearSlowSearch();
        busy.set(false);
      }
    }
  }, definition['debounce-ms']);
  const input = /** @type {HTMLInputElement} */ (h('input', {
    type: 'search', className: 'dialog-selector-input', 'aria-label': definition.label,
    placeholder: definition.placeholder ?? 'Search repositories', maxLength: 200,
    onInput: () => {
      invalidate();
      results.scrollTop = 0;
      choices.set([]);
      selected.set(null);
      select(null);
      const text = input.value.trim();
      if (definition['allow-patterns'] && REPOSITORY_PATTERN.test(text)) {
        choices.set([{ value: text, label: text, description: text.includes('*')
          ? 'Use wildcard: resolve accessible matches to exact repositories in review mode.'
          : 'Use this exact repository; access is verified before enrollment.' }]);
        message.set('Select this repository or pattern to review the command. No policy changes have been made.');
        return;
      }
      if (definition['allow-patterns'] && text.includes('*')) {
        message.set('Enter an owner/repository pattern, for example octo/* or octo/service-*.');
        return;
      }
      message.set(text.length < 2 ? 'Type at least 2 characters to search GitHub repositories.' : 'Waiting to search...');
      if (text.length >= 2) lookup(text);
    }
  }));
  const status = renderLiveRegion('output', 'dialog-selector-status');
  const results = h('div', { className: 'dialog-selector-options', role: 'group', 'aria-label': 'Search results' });
  const root = h('div', { className: 'dialog-selector' }, h('label', null, definition.label, input), status, results);
  effect(() => {
    input.disabled = disabled.get();
    status.textContent = message.get();
    results.setAttribute('aria-busy', String(busy.get()));
  }, { signal });
  render(results, () => choices.get().map((option) => h('button', {
      type: 'button', className: 'dialog-selector-option', 'aria-pressed': String(selected.get() === option.value),
      dataset: { key: option.value },
      disabled: disabled.get(),
      onClick: () => { selected.set(option.value); select(option.value); }
    }, h('strong', null, option.label), option.description ? h('small', null, option.description) : null)), { signal });
  signal.addEventListener('abort', invalidate, { once: true });
  return {
    root,
    focus: () => input.focus(),
    setDisabled: (/** @type {boolean} */ value) => disabled.set(value),
    cancel: invalidate,
    reset() {
      invalidate();
      input.value = '';
      results.scrollTop = 0;
      selected.set(null);
      choices.set([]);
      message.set('Type at least 2 characters to search GitHub repositories.');
      select(null);
    }
  };
}
