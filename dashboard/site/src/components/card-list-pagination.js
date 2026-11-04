import { h } from '../dom.js';
import { CONTINUATION_PAGE_SIZE } from '../data/continuation.js';
import { createDebug } from '../debug.js';
import { effect, state } from '../reactive.js';
import { createFactoryScope } from './factory-elements.js';
import { observeLoadMoreBoundary } from './ui-primitives.js';

const debug = createDebug('render:card-list');
/** @type {WeakMap<HTMLElement, (count: number) => Promise<void>>} */
const restorers = new WeakMap();

/**
 * Progressively appends already-ordered query pages without rebuilding existing cards.
 * @param {HTMLElement} section
 * @param {{
 *   rows: Array<Record<string, unknown>>,
 *   renderItems: (rows: Array<Record<string, unknown>>, offset: number) => HTMLElement[],
 *   continuation?: import('./data-view.js').DataViewContext['continuation']
 * }} options
 */
export function enableCardListPagination(section, { rows, renderItems, continuation }) {
  const list = section.querySelector('.entity-card-list');
  if (!list || (rows.length <= CONTINUATION_PAGE_SIZE && !continuation?.token)) return;
  const scope = createFactoryScope();
  scope.bind(section);
  const button = /** @type {HTMLButtonElement} */ (h('button', { type: 'button', className: 'entity-card-list-load-more' }, 'Load more'));
  const status = h('span', { className: 'entity-card-list-load-status', role: 'status' });
  const boundary = h('li', { className: 'entity-card-list-boundary', 'data-card-list-boundary': '' }, button, status);
  list.append(boundary);
  let buffer = rows;
  let offset = Math.min(rows.length, CONTINUATION_PAGE_SIZE);
  let token = continuation?.token;
  const total = continuation ? continuation.totalRows : rows.length;
  const progress = state({
    phase: /** @type {'idle'|'loading'|'error'|'complete'} */ ('idle'),
    count: offset
  });
  effect(() => {
    const { phase, count } = progress.get();
    boundary.dataset.loadState = phase;
    boundary.dataset.renderedCount = String(count);
    section.dataset.cardListRestoreCount = String(Math.max(count, Number(section.dataset.cardListRestoreCount) || 0));
    button.disabled = phase === 'loading';
    button.hidden = phase === 'complete';
    button.textContent = phase === 'loading' ? 'Loading...' : phase === 'error' ? 'Try again' : 'Load more';
    status.textContent = phase === 'error'
      ? 'Could not load more items. Try again.'
      : Number.isSafeInteger(total)
        ? `Showing ${count} of ${total} items`
        : `Showing ${count} items`;
  }, { signal: scope.signal });

  const appendNext = async () => {
    const current = progress.get();
    if (scope.signal.aborted || current.phase === 'loading' || current.phase === 'complete') return;
    progress.set({ ...current, phase: 'loading' });
    try {
      if (offset >= buffer.length && token && continuation) {
        const next = await continuation.load(token);
        if (scope.signal.aborted) return;
        buffer = next.rows;
        offset = 0;
        token = next.continuationToken;
      }
      const nextRows = buffer.slice(offset, offset + CONTINUATION_PAGE_SIZE);
      boundary.before(...renderItems(nextRows, current.count));
      offset += nextRows.length;
      const complete = offset >= buffer.length && !token;
      progress.set({ phase: complete ? 'complete' : 'idle', count: current.count + nextRows.length });
      if (complete) observer?.disconnect();
    } catch (error) {
      if (scope.signal.aborted) return;
      debug({ event: 'continuation-failed', errorName: error instanceof Error ? error.name : 'unknown' });
      progress.set({ ...current, phase: 'error' });
    }
  };
  /** @type {Promise<void> | undefined} */
  let pending;
  const loadMore = () => {
    pending ??= appendNext().finally(() => { pending = undefined; });
    return pending;
  };
  button.addEventListener('click', () => void loadMore(), { signal: scope.signal });
  const observer = observeLoadMoreBoundary(section.ownerDocument.defaultView?.IntersectionObserver, boundary, () => {
    if (progress.get().phase === 'idle') void loadMore();
  }, { rootMargin: '200px 0px' });
  restorers.set(section, async (count) => {
    while (!scope.signal.aborted && progress.get().count < count) {
      const previous = progress.get().count;
      await loadMore();
      if (progress.get().count === previous) break;
    }
  });
  scope.signal.addEventListener('abort', () => {
    observer?.disconnect();
    restorers.delete(section);
  }, { once: true });
}

/**
 * Restores presentation depth from fresh query pages after navigation or a live update.
 * @param {HTMLElement} previous
 * @param {HTMLElement} replacement
 */
export function restoreCardListPagination(previous, replacement) {
  const candidates = [replacement, ...replacement.querySelectorAll('[data-view-id]')];
  for (const view of [previous, ...previous.querySelectorAll('[data-view-id]')]) {
    const viewId = view.getAttribute('data-view-id');
    const count = Number(view.getAttribute('data-card-list-restore-count'));
    if (!viewId || !Number.isSafeInteger(count) || count <= CONTINUATION_PAGE_SIZE) continue;
    const next = candidates.find((candidate) => candidate.getAttribute('data-view-id') === viewId);
    if (next instanceof HTMLElement) {
      next.dataset.cardListRestoreCount = String(count);
      void restorers.get(next)?.(count);
    }
  }
}
