// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';
import { renderDataView } from '../../src/components/data-view.js';
import { setDeclaredCliActions } from '../../src/components/cli-actions.js';

const metadata = /** @type {import('../../src/presenter.js').SourceMetadata} */ ({
  'source-id': 'selection', 'source-kind': 'fixture',
  'as-of': '2026-11-18T00:00:00Z', 'retrieved-at': '2026-11-18T00:00:00Z',
  availability: 'available', completeness: 'complete', freshness: 'fresh'
});
const rows = Array.from({ length: 61 }, (_, index) => ({
  repository: `octo/repo-${index}`, workflow: 'review.md', eligible: index !== 60
}));

/** @param {string} selector @param {ParentNode} [root] */
function button(selector, root = document) {
  const element = root.querySelector(selector);
  if (!(element instanceof HTMLButtonElement)) throw new Error(`Missing button ${selector}`);
  return element;
}

/** @param {number} index */
function item(index) {
  const element = document.querySelectorAll('.entity-card-selection-item input')[index];
  if (!(element instanceof HTMLInputElement)) throw new Error(`Missing checkbox ${index}`);
  return element;
}

/** @param {Partial<import('../../src/components/data-view.js').DataViewContext>} [options] */
function renderList(options = {}) {
  setDeclaredCliActions([{
    id: 'update', label: 'Update', icon: 'sync', placement: 'row',
    command: 'gh aw update {{workflow}} --repo {{repository}}'
  }], { canExecute: false });
  const root = renderDataView('list', {
    pageId: 'selection', title: 'Selection', sourceName: 'fixture',
    view: {
      mark: 'list', 'lazy-list': true, layout: 'full-view',
      list: { style: 'entity-cards', icon: 'repo', card: 'target',
        selection: { key: ['repository', 'workflow'], commands: ['update'] } },
      encoding: { columns: [{ field: 'repository' }, { field: 'workflow' }] }
    },
    cardTemplates: {
      target: {
        icon: 'repo', title: { field: 'repository' }, labels: [], details: [],
        drill: { type: 'external', field: 'repository-url' },
        actions: [{ action: 'update', context: ['repository', 'workflow'], when: { field: 'eligible', equals: true } }]
      }
    },
    rows, metadata, contextDetails: [], headingTag: 'h3',
    prepareTableRows: (values) => values, buildChartPoints: () => [], prepareChartPoints: () => [], toText: String,
    ...options
  });
  if (!root) throw new Error('Missing list');
  document.body.append(root);
  return root;
}

function selectLoaded() {
  const checkbox = document.querySelector('.entity-card-selection-loaded input');
  if (!(checkbox instanceof HTMLInputElement)) throw new Error('Missing select loaded');
  checkbox.click();
  return checkbox;
}

afterEach(() => {
  document.body.replaceChildren();
  setDeclaredCliActions([]);
});

describe('lazy card checkbox selection', () => {
  it('selects only rendered cards, preserves individual choices while paging, and resets on leaving mode', async () => {
    const root = renderList();
    expect(root.querySelectorAll('.entity-card-list-card')).toHaveLength(25);
    expect(root.querySelector('.entity-card-selection-controls')?.hasAttribute('hidden')).toBe(true);
    button('.entity-card-selection-toggle').click();
    item(0).focus();
    item(0).click();
    expect(document.activeElement).toBe(item(0));
    expect(root.querySelector('.entity-card-selection-loaded input')?.getAttribute('type')).toBe('checkbox');
    const all = selectLoaded();
    expect(root.querySelector('[role="status"]')?.textContent).toBe('25 selected');
    expect(item(24).checked).toBe(true);
    expect(root.querySelector('.entity-card-selection-link')?.textContent).toBe('Select all 61');
    button('.entity-card-list-load-more').click();
    await vi.waitFor(() => expect(root.querySelectorAll('.entity-card-list-card')).toHaveLength(50));
    expect(item(25).checked).toBe(false);
    expect(all.indeterminate).toBe(true);
    expect(root.querySelector('[role="status"]')?.textContent).toBe('25 selected');
    button('.entity-card-selection-toggle').click();
    button('.entity-card-selection-toggle').click();
    expect(item(0).checked).toBe(false);
    expect(root.querySelector('[role="status"]')?.textContent).toBe('0 selected');
  });

  it('drains the same query cursor without rendering offscreen cards, then allows deselection and condition-gated commands', async () => {
    const load = vi.fn()
      .mockResolvedValueOnce({ rows: rows.slice(25, 50), continuationToken: 'last', metadata })
      .mockResolvedValueOnce({ rows: rows.slice(50), metadata });
    const root = renderList({ rows: rows.slice(0, 25), sourceTruncated: true, continuation: { token: 'next', totalRows: 61, load } });
    button('.entity-card-selection-toggle').click();
    selectLoaded();
    button('.entity-card-selection-link').click();
    await vi.waitFor(() => expect(root.querySelector('[role="status"]')?.textContent).toBe('61 selected'));
    expect(load.mock.calls).toEqual([['next'], ['last']]);
    expect(root.querySelectorAll('.entity-card-list-card')).toHaveLength(25);
    expect(root.querySelector('.entity-card-selection-controls .cli-action-trigger')?.hasAttribute('disabled')).toBe(true);
    expect(root.querySelector('.entity-card-selection-controls .cli-action-command')?.textContent).toContain('not available for every selected item');
    item(0).click();
    expect(root.querySelector('[role="status"]')?.textContent).toBe('60 selected');
    button('.entity-card-list-load-more').click();
    await vi.waitFor(() => expect(root.querySelectorAll('.entity-card-list-card')).toHaveLength(50));
    expect(item(25).checked).toBe(true);
    expect(load).toHaveBeenCalledTimes(2);
    button('.entity-card-selection-controls button:nth-of-type(2)').click();
    expect(root.querySelector('[role="status"]')?.textContent).toBe('0 selected');
  });

  it('keeps exact per-repository workflow contexts and no control-repository fallback', () => {
    const root = renderList({ rows: rows.slice(0, 2) });
    button('.entity-card-selection-toggle').click();
    selectLoaded();
    const control = root.querySelector('.entity-card-selection-controls');
    if (!control) throw new Error('Missing controls');
    button('.cli-action-trigger', control).click();
    expect(control.querySelector('.cli-action-command')?.textContent).toBe(
      'gh aw update review.md --repo octo/repo-0\ngh aw update review.md --repo octo/repo-1'
    );
    expect(control.querySelector('.cli-action-confirm')?.hasAttribute('disabled')).toBe(false);
  });

  it('preserves explicit selection on a failed full-query load and retries without reusing consumed tokens', async () => {
    const load = vi.fn()
      .mockResolvedValueOnce({ rows: rows.slice(25, 50), continuationToken: 'last', metadata })
      .mockRejectedValueOnce(new Error('Query page unavailable'))
      .mockResolvedValueOnce({ rows: rows.slice(50), metadata });
    const root = renderList({ rows: rows.slice(0, 25), continuation: { token: 'next', totalRows: 61, load } });
    button('.entity-card-selection-toggle').click();
    selectLoaded();
    button('.entity-card-selection-link').click();
    await vi.waitFor(() => expect(root.querySelector('[role="alert"]')?.textContent).toBe('Query page unavailable'));
    expect(root.querySelector('[role="status"]')?.textContent).toBe('25 selected');
    button('.entity-card-selection-link').click();
    await vi.waitFor(() => expect(root.querySelector('[role="status"]')?.textContent).toBe('61 selected'));
    expect(load.mock.calls).toEqual([['next'], ['last'], ['last']]);
  });

  it('fails closed on duplicate or missing keys and on mismatched full-query counts', async () => {
    const invalid = renderList({ rows: [rows[0], rows[0], { workflow: 'review.md' }] });
    expect(button('.entity-card-selection-toggle').disabled).toBe(true);
    expect(invalid.querySelector('[role="alert"]')?.textContent).toMatch(/missing or invalid/);
    invalid.remove();
    const load = vi.fn().mockResolvedValue({ rows: [rows[25]], metadata });
    const root = renderList({ rows: rows.slice(0, 25), continuation: { token: 'next', totalRows: 61, load } });
    button('.entity-card-selection-toggle').click();
    selectLoaded();
    button('.entity-card-selection-link').click();
    await vi.waitFor(() => expect(root.querySelector('[role="alert"]')?.textContent).toContain('result count changed'));
    expect(root.querySelector('[role="status"]')?.textContent).toBe('25 selected');
  });

  it('disables selection after a duplicate arrives while still allowing cancellation', async () => {
    const root = renderList({ rows: [...rows.slice(0, 25), rows[0]] });
    button('.entity-card-selection-toggle').click();
    selectLoaded();
    button('.entity-card-list-load-more').click();
    await vi.waitFor(() => expect(root.querySelector('[role="alert"]')?.textContent).toContain('not unique'));
    expect(item(0).disabled).toBe(true);
    expect(root.querySelector('.entity-card-selection-loaded input')?.hasAttribute('disabled')).toBe(true);
    expect(button('.entity-card-selection-toggle').disabled).toBe(false);
    button('.entity-card-selection-toggle').click();
    expect(root.querySelector('.entity-card-selection-controls')?.hasAttribute('hidden')).toBe(true);
    expect(button('.entity-card-selection-toggle').disabled).toBe(true);
  });

  it('shares an in-flight continuation between load more and full-query selection', async () => {
    /** @type {(value: { rows: typeof rows, metadata: typeof metadata }) => void} */
    let finish = () => {};
    const load = vi.fn(() => new Promise((resolve) => { finish = resolve; }));
    const root = renderList({ rows: rows.slice(0, 25), continuation: { token: 'next', totalRows: 61, load } });
    button('.entity-card-selection-toggle').click();
    selectLoaded();
    button('.entity-card-list-load-more').click();
    button('.entity-card-selection-link').click();
    expect(load).toHaveBeenCalledTimes(1);
    finish({ rows: rows.slice(25), metadata });
    await vi.waitFor(() => expect(root.querySelector('[role="status"]')?.textContent).toBe('61 selected'));
    expect(root.querySelectorAll('.entity-card-list-card')).toHaveLength(50);
    expect(item(25).checked).toBe(true);
  });

  it('blocks unknown evidence and preserves partial-evidence command warnings', () => {
    const unavailable = renderList({ rows: rows.slice(0, 2), metadata: { ...metadata, completeness: 'unknown' } });
    expect(button('.entity-card-selection-toggle').disabled).toBe(true);
    unavailable.remove();
    const partial = renderList({ rows: rows.slice(0, 2), metadata: { ...metadata, completeness: 'partial' } });
    button('.entity-card-selection-toggle').click();
    selectLoaded();
    button('.entity-card-selection-controls .cli-action-trigger').click();
    expect(partial.textContent).toContain('Inventory evidence is partial');
  });

  it('ignores a late full-query result after detachment', async () => {
    /** @type {(value: { rows: typeof rows }) => void} */
    let finish = () => {};
    const load = vi.fn(() => new Promise((resolve) => { finish = resolve; }));
    const root = renderList({ rows: rows.slice(0, 25), continuation: { token: 'next', totalRows: 61, load } });
    button('.entity-card-selection-toggle').click();
    selectLoaded();
    button('.entity-card-selection-link').click();
    root.remove();
    await new Promise((resolve) => setTimeout(resolve, 0));
    finish({ rows: rows.slice(25) });
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(root.querySelector('[role="status"]')?.textContent).not.toBe('61 selected');
  });
});
