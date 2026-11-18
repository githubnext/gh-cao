import { h } from '../dom.js';
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
  const enabled = state(false);
  const busy = state(false);
  const error = state('');
  const invalid = state('');
  const revision = state(0);
  /** @type {Map<string, Record<string, unknown>>} */
  let selected = new Map();
  /** @type {Array<{ id: string, row: Record<string, unknown>, input: HTMLInputElement, label: HTMLElement }>} */
  const loaded = [];
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
  const mode = /** @type {HTMLButtonElement} */ (h('button', {
    type: 'button', className: 'entity-card-selection-toggle', 'aria-pressed': 'false',
    onClick: () => {
      enabled.set(!enabled.get());
      selected = new Map();
      error.set('');
      changed();
    }
  }, 'Select items'));
  const allLoaded = /** @type {HTMLInputElement} */ (h('input', {
    type: 'checkbox',
    onChange: () => {
      for (const item of loaded) {
        if (allLoaded.checked) selected.set(item.id, item.row);
        else selected.delete(item.id);
      }
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
    onClick: () => { selected = new Map(); error.set(''); changed(); }
  }, 'Clear selection');
  const commands = h('span', { className: 'document-list-actions' });
  const message = h('span', { role: 'alert' });
  const controls = h('div', { className: 'entity-card-selection-controls', hidden: true },
    h('label', { className: 'entity-card-selection-loaded' }, allLoaded, 'Select all loaded items'),
    count, selectQuery, clear, commands);
  const toolbar = h('div', { className: 'entity-card-selection' }, mode, controls, message);

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
        const input = /** @type {HTMLInputElement} */ (h('input', {
          type: 'checkbox', 'aria-label': `Select ${card.querySelector('.entity-card-list-title')?.textContent ?? options.key.map((field) => row[field]).join(' / ')}`,
          onChange: () => {
            if (input.checked) selected.set(id, row);
            else selected.delete(id);
            changed();
          }
        }));
        const label = h('label', { className: 'entity-card-selection-item', hidden: !enabled.get(), onClick: (/** @type {Event} */ event) => event.stopPropagation() }, input);
        card.prepend(label);
        loaded.push({ id, row, input, label });
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
        const active = enabled.get();
        const loading = busy.get();
        const failure = invalid.get() || error.get();
        mode.textContent = active ? 'Cancel selection' : 'Select items';
        mode.setAttribute('aria-pressed', String(active));
        const selectable = available && !invalid.get() && loaded.length > 0;
        mode.disabled = loading || !active && !selectable;
        controls.hidden = !active;
        allLoaded.checked = loaded.length > 0 && loaded.every((item) => selected.has(item.id));
        allLoaded.indeterminate = !allLoaded.checked && loaded.some((item) => selected.has(item.id));
        allLoaded.disabled = loading || !selectable;
        for (const item of loaded) {
          item.label.hidden = !active;
          item.input.checked = selected.has(item.id);
          item.input.disabled = loading || !selectable;
        }
        count.textContent = loading ? 'Selecting all query results...' : `${selected.size} selected`;
        selectQuery.textContent = options.total === undefined ? 'Select all results' : `Select all ${options.total}`;
        selectQuery.hidden = !allLoaded.checked || !options.canSelectAll
          || options.total !== undefined && selected.size === options.total;
        selectQuery.disabled = loading || !selectable;
        clear.hidden = selected.size === 0;
        clear.toggleAttribute('disabled', loading);
        message.textContent = failure;
        message.hidden = !failure;
        const rows = [...selected.values()];
        commands.replaceChildren(...options.commands.flatMap((id) => {
          const action = options.actions.find((candidate) => candidate.action === id);
          if (!action) return [];
          const control = renderDeclaredCliAction(id, {}, {
            rows, fields: action.context, when: action.when,
            available: available && active && !loading && !failure,
            warning: options.metadata.completeness === 'partial'
              ? 'Inventory evidence is partial. Only the explicitly listed selected targets are included.' : undefined
          });
          return control ? [control] : [];
        }));
      }, { signal: scope.signal });
    }
  };
}
