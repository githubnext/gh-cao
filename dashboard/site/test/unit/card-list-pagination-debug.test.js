import { afterEach, describe, expect, it, vi } from 'vitest';

afterEach(() => {
  vi.doUnmock('../../src/debug.js');
  vi.unstubAllGlobals();
  vi.resetModules();
  document.body.replaceChildren();
});

/** @param {string} search */
async function loadPagination(search) {
  const output = { debug: vi.fn() };
  const categories = /** @type {string[]} */ ([]);
  vi.doMock('../../src/debug.js', async () => {
    const actual = /** @type {typeof import('../../src/debug.js')} */ (
      await vi.importActual('../../src/debug.js')
    );
    return {
      ...actual,
      createDebug: (/** @type {string} */ category) => {
        categories.push(category);
        return actual.createDebug(category, { search: () => search, output });
      }
    };
  });
  vi.resetModules();
  const { h } = await import('../../src/dom.js');
  const module = await import('../../src/components/card-list-pagination.js');
  return { ...module, h, output, categories };
}

/** @param {typeof import('../../src/dom.js').h} h */
function setup(h) {
  const rows = Array.from({ length: 60 }, (_, index) => ({ name: `Secret package ${index + 1}` }));
  const renderItems = (/** @type {Record<string, unknown>[]} */ items) =>
    items.map((row) => h('li', { className: 'entity-card-list-card' }, String(row.name)));
  const list = h('ul', { className: 'entity-card-list' }, ...renderItems(rows.slice(0, 25)));
  const section = h('section', { 'data-view-id': 'packages' }, list);
  document.body.append(section);
  return { rows, renderItems, section };
}

describe('card list pagination debug logging', () => {
  it('uses the category derived from the filename', async () => {
    const { categories } = await loadPagination('');
    expect(categories).toContain('card-list-pagination');
    expect(categories).not.toContain('render:card-list');
  });

  it('emits nothing unless the category is enabled', async () => {
    vi.stubGlobal('IntersectionObserver', undefined);
    const { enableCardListPagination, h, output } = await loadPagination('?debug=auth');
    const { rows, renderItems, section } = setup(h);
    enableCardListPagination(section, { rows, renderItems });
    /** @type {HTMLButtonElement} */ (section.querySelector('button')).click();
    await Promise.resolve();
    expect(output.debug).not.toHaveBeenCalled();
  });

  it('logs buffered and continuation page appends as scalar metadata only', async () => {
    vi.stubGlobal('IntersectionObserver', undefined);
    const { enableCardListPagination, h, output } = await loadPagination('?debug=card-list-pagination');
    const { rows, renderItems, section } = setup(h);
    const load = vi.fn().mockResolvedValue({ rows: rows.slice(50), continuationToken: undefined });
    enableCardListPagination(section, {
      rows: rows.slice(0, 50),
      renderItems,
      continuation: { token: 'secret-continuation-token', totalRows: 60, load }
    });
    const button = /** @type {HTMLButtonElement} */ (section.querySelector('button'));
    button.click();
    await vi.waitFor(() => expect(output.debug).toHaveBeenCalledTimes(1));
    button.click();
    await vi.waitFor(() => expect(output.debug).toHaveBeenCalledTimes(2));

    expect(output.debug.mock.calls).toEqual([
      ['[cao:card-list-pagination]', { event: 'page-appended', source: 'buffer', appended: 25, rendered: 50, complete: false }],
      ['[cao:card-list-pagination]', { event: 'page-appended', source: 'continuation', appended: 10, rendered: 60, complete: true }]
    ]);
    const logged = JSON.stringify(output.debug.mock.calls);
    expect(logged).not.toContain('Secret package');
    expect(logged).not.toContain('secret-continuation-token');
  });

  it('logs depth restoration requests without view identifiers', async () => {
    vi.stubGlobal('IntersectionObserver', undefined);
    const { enableCardListPagination, restoreCardListPagination, h, output } = await loadPagination('?debug=card-list-pagination');
    const { rows, renderItems, section: previous } = setup(h);
    previous.dataset.cardListRestoreCount = '50';
    const replacement = setup(h).section;
    enableCardListPagination(replacement, { rows, renderItems });

    restoreCardListPagination(previous, replacement);

    expect(output.debug).toHaveBeenCalledWith('[cao:card-list-pagination]', { event: 'restore-requested', restoreCount: 50, restorable: true });
    expect(JSON.stringify(output.debug.mock.calls)).not.toContain('packages');
  });
});
