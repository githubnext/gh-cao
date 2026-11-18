import { h } from '../dom.js';
import { octicon } from '../octicons.js';
import { effect, state } from '../reactive.js';
import { createFactoryScope } from './factory-elements.js';
import { renderDeclaredCliAction } from './cli-actions.js';

/**
 * Selection is interaction state only. The query cursor, not the DOM or a new
 * unscoped query, supplies targets beyond the rendered cards.
 * @param {{
 *   key: string[], commands: string[],
 *   actions: Array<{ action: string, context: string[], when?: { field: string, equals: unknown } }>,
 *   metadata: import('../presenter.js').SourceMetadata,
 *   total?: number, canSelectAll: boolean
 * }} options
 */
export function createCardListSelection(options) {
  const scope = createFactoryScope();
  const busy = state(false);
  const error = state('');
  const invalid = state('');
  const revision = state(0);
  /** @type {Map<string, Record<string, unknown>>} */
  let selected = new Map();
  /** @type {Array<{ id: string, row: Record<string, unknown>, input: HTMLInputElement, card: HTMLElement }>} */
  const loaded = [];
  let anchor = -1;
  /** @type {{ collectAll: () => Promise<Array<Record<string, unknown>>> } | undefined} */
  let pagination;
  const available = options.metadata.availability === 'available'
    && ['complete', 'partial'].includes(options.metadata.completeness);

  /** @param {Record<string, unknown>} row */
  const identity = (row) => JSON.stringify(options.key.map((field) => {
    const value = row[field];
    if ((typeof value !== 'string' || value.length === 0) && (typeof value !== 'number' || !Number.isFinite(value))) {
      throw new Error(`Selection key "${field}" is missing or invalid.`);
    }
    return value;
  }));
  const changed = () => revision.set((value) => value + 1);
  const allLoaded = /** @type {HTMLInputElement} */ (h('input', {
    type: 'checkbox',
    onChange: () => {
      if (allLoaded.checked) {
        for (const item of loaded) selected.set(item.id, item.row);
      } else {
        selected = new Map();
      }
      anchor = -1;
      error.set('');
      changed();
    }
  }));
  const count = h('span', { role: 'status' });
  const selectQuery = /** @type {HTMLButtonElement} */ (h('button', {
    type: 'button', className: 'entity-card-selection-link',
    onClick: async () => {
      if (busy.get() || !pagination) return;
      busy.set(true);
      error.set('');
      try {
        const rows = await pagination.collectAll();
        if (scope.signal.aborted) return;
        const next = new Map();
        for (const row of rows) {
          const id = identity(row);
          if (next.has(id)) throw new Error('Selection keys are not unique. No full-query selection was applied.');
          next.set(id, row);
        }
        selected = next;
        changed();
      } catch (failure) {
        if (!scope.signal.aborted) error.set(failure instanceof Error ? failure.message : 'Could not select the complete query. Try again.');
      } finally {
        if (!scope.signal.aborted) busy.set(false);
      }
    }
  }));
  const clear = h('button', {
    type: 'button', className: 'entity-card-selection-link',
    onClick: () => { selected = new Map(); anchor = -1; error.set(''); changed(); }
  }, 'Clear selection');
  const commandList = h('div', { className: 'cli-actions-list' });
  const commandToggle = h('summary', { className: 'cli-actions-toggle' }, 'Commands', octicon('chevron-down'));
  const commands = /** @type {HTMLDetailsElement} */ (h('details', {
    className: 'cli-actions-menu entity-card-selection-commands',
    onKeyDown: (/** @type {KeyboardEvent} */ event) => {
      if (event.key !== 'Escape' || event.target instanceof Element && event.target.closest('dialog')) return;
      commands.open = false;
      commandToggle.focus();
    }
  }, commandToggle, commandList));
  commands.ownerDocument.addEventListener('click', (event) => {
    if (event.target instanceof Node && !commands.contains(event.target)) commands.open = false;
  }, { signal: scope.signal });
  const message = h('span', { role: 'alert' });
  const header = h('div', { className: 'entity-card-selection-header' },
    h('label', { className: 'entity-card-selection-loaded', title: 'Select all loaded items' },
      allLoaded, h('span', { className: 'sr-only' }, 'Select all loaded items')),
    count);
  const actions = h('div', { className: 'entity-card-selection-actions', hidden: true },
    selectQuery, clear, commands);
  const controls = h('div', { className: 'entity-card-selection-controls' }, header, actions);
  const toolbar = h('div', { className: 'entity-card-selection' }, controls, message);

  return {
    /** @param {HTMLElement[]} cards @param {Array<Record<string, unknown>>} rows */
    decorate(cards, rows) {
      for (const [index, card] of cards.entries()) {
        const row = rows[index];
        /** @type {string} */
        let id;
        try {
          id = identity(row);
          if (loaded.some((item) => item.id === id)) throw new Error('Selection keys are not unique. Refresh the list.');
        } catch (failure) {
          invalid.set(failure instanceof Error ? failure.message : 'Invalid selection key.');
          continue;
        }
        const position = loaded.length;
        const input = /** @type {HTMLInputElement} */ (h('input', {
          type: 'checkbox', 'aria-label': `Select ${card.querySelector('.entity-card-list-title')?.textContent ?? options.key.map((field) => row[field]).join(' / ')}`,
          onClick: (/** @type {MouseEvent} */ event) => {
            const first = event.shiftKey && anchor >= 0 ? Math.min(anchor, position) : position;
            const last = event.shiftKey && anchor >= 0 ? Math.max(anchor, position) : position;
            for (const item of loaded.slice(first, last + 1)) {
              if (input.checked) selected.set(item.id, item.row);
              else selected.delete(item.id);
            }
            anchor = position;
            error.set('');
            changed();
          }
        }));
        const label = h('label', { className: 'entity-card-selection-item', onClick: (/** @type {Event} */ event) => event.stopPropagation() }, input);
        card.classList.add('entity-card-selectable');
        card.prepend(label);
        loaded.push({ id, row, input, card });
      }
      changed();
    },
    /** @param {HTMLElement} section @param {{ collectAll: () => Promise<Array<Record<string, unknown>>> } | undefined} controller */
    bind(section, controller) {
      pagination = controller;
      const list = section.querySelector('.entity-card-list, .document-list-empty');
      list?.before(toolbar);
      scope.bind(section);
      effect(() => {
        revision.get();
        const loading = busy.get();
        const failure = invalid.get() || error.get();
        const selectable = available && !invalid.get() && loaded.length > 0;
        allLoaded.checked = loaded.length > 0 && loaded.every((item) => selected.has(item.id));
        allLoaded.indeterminate = !allLoaded.checked && loaded.some((item) => selected.has(item.id));
        allLoaded.disabled = loading || !selectable;
        for (const item of loaded) {
          item.input.checked = selected.has(item.id);
          item.input.disabled = loading || !selectable;
          item.card.classList.toggle('entity-card-selected', selected.has(item.id));
        }
        count.textContent = loading ? 'Selecting all query results...'
          : selected.size ? `${selected.size} selected`
            : `${options.total ?? loaded.length} items`;
        selectQuery.textContent = options.total === undefined ? 'Select all results' : `Select all ${options.total}`;
        selectQuery.hidden = !allLoaded.checked || !options.canSelectAll
          || options.total !== undefined && selected.size === options.total;
        selectQuery.disabled = loading || !selectable;
        clear.hidden = selected.size === 0;
        clear.toggleAttribute('disabled', loading);
        actions.hidden = selected.size === 0;
        commands.hidden = selected.size === 0;
        if (commands.hidden) commands.open = false;
        message.textContent = failure;
        message.hidden = !failure;
        const rows = [...selected.values()];
        commandList.replaceChildren(...(selected.size ? options.commands : []).flatMap((id) => {
          const action = options.actions.find((candidate) => candidate.action === id);
          if (!action) return [];
          const control = renderDeclaredCliAction(id, {}, {
            rows, fields: action.context, when: action.when,
            available: available && !loading && !failure,
            warning: options.metadata.completeness === 'partial'
              ? 'Inventory evidence is partial. Only the explicitly listed selected targets are included.' : undefined
          });
          control?.querySelector('dialog')?.addEventListener('close', () => {
            commands.open = false;
            commandToggle.focus();
          }, { signal: scope.signal });
          return control ? [control] : [];
        }));
      }, { signal: scope.signal });
    }
  };
}
