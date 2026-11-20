// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from 'vitest';

const metadata = /** @type {import('../../src/presenter.js').SourceMetadata} */ ({
  'source-id': 'selection', 'source-kind': 'fixture',
  'as-of': '2026-11-18T00:00:00Z', 'retrieved-at': '2026-11-18T00:00:00Z',
  availability: 'available', completeness: 'complete', freshness: 'fresh'
});

afterEach(() => {
  vi.doUnmock('../../src/debug.js');
  vi.resetModules();
});

/** @param {string} search */
async function load(search) {
  const debugFn = vi.fn();
  const output = /** @type {Pick<Console, 'debug'>} */ ({ debug: debugFn });
  vi.doMock('../../src/debug.js', async () => {
    const actual = /** @type {typeof import('../../src/debug.js')} */ (await vi.importActual('../../src/debug.js'));
    return { ...actual, createDebug: (/** @type {string} */ category) => actual.createDebug(category, { search: () => search, output }) };
  });
  vi.resetModules();
  const { createCardListSelection } = await import('../../src/components/card-list-selection.js');
  return { debugFn, createCardListSelection };
}

/** @param {typeof import('../../src/components/card-list-selection.js').createCardListSelection} create */
function selection(create) {
  const section = document.createElement('section');
  section.innerHTML = '<div class="entity-card-list"></div>';
  document.body.append(section);
  const controller = create({
    key: ['repository'], commands: [], actions: [], metadata, total: 3, canSelectAll: true
  });
  return { controller, section };
}

/** @param {HTMLElement} section @param {string} selector */
function button(section, selector) {
  const element = section.querySelector(selector);
  if (!(element instanceof HTMLButtonElement)) throw new Error(`Missing button ${selector}`);
  return element;
}

/** @param {Array<Record<string, unknown>>} rows */
function cards(rows) {
  return rows.map(() => document.createElement('article'));
}

/**
 * @param {ReturnType<typeof selection>['controller']} controller
 * @param {HTMLElement} section
 * @param {() => Promise<Array<Record<string, unknown>>>} collectAll
 */
async function selectQuery(controller, section, collectAll) {
  controller.decorate(cards([{ repository: 'octo/a' }]), [{ repository: 'octo/a' }]);
  controller.bind(section, { collectAll });
  const all = section.querySelector('.entity-card-selection-loaded input');
  if (!(all instanceof HTMLInputElement)) throw new Error('Missing select loaded');
  all.click();
  button(section, '.entity-card-selection-link').click();
}

afterEach(() => {
  document.body.replaceChildren();
});

describe('card-list-selection debug logging', () => {
  it('is silent without a matching debug query and preserves behavior', async () => {
    const { debugFn, createCardListSelection } = await load('');
    const { controller, section } = selection(createCardListSelection);
    await selectQuery(controller, section, async () => [{ repository: 'octo/a' }, { repository: 'octo/b' }, { repository: 'octo/c' }]);
    await vi.waitFor(() => expect(section.querySelector('[role="status"]')?.textContent).toBe('3 selected'));
    expect(debugFn.mock.calls.filter(([prefix]) => prefix === '[cao:card-list-selection]')).toEqual([]);
  });

  it('logs full-query selection counts under its predictable category', async () => {
    const { debugFn, createCardListSelection } = await load('?debug=card-list-selection');
    const { controller, section } = selection(createCardListSelection);
    await selectQuery(controller, section, async () => [{ repository: 'octo/a' }, { repository: 'octo/b' }, { repository: 'octo/c' }]);
    await vi.waitFor(() => expect(debugFn).toHaveBeenCalledWith('[cao:card-list-selection]', {
      event: 'select-all-query', status: 'selected', count: 3, total: 3
    }));
  });

  it('logs failures and invalid keys without messages or row values', async () => {
    const { debugFn, createCardListSelection } = await load('?debug=card-list-*');
    const { controller, section } = selection(createCardListSelection);
    await selectQuery(controller, section, async () => { throw new Error('SECRET-token octo/private'); });
    await vi.waitFor(() => expect(debugFn).toHaveBeenCalledWith('[cao:card-list-selection]', {
      event: 'select-all-query', status: 'failed', errorName: 'Error'
    }));
    controller.decorate(cards([{ repository: 'SECRET-row' }, {}]), [{ repository: 'SECRET-row' }, {}]);
    expect(debugFn).toHaveBeenCalledWith('[cao:card-list-selection]', {
      event: 'selection-key', status: 'invalid', errorName: 'Error', loaded: 2
    });
    expect(JSON.stringify(debugFn.mock.calls)).not.toMatch(/SECRET|octo\/private|repository/);
  });

  it('is excluded by a negated pattern', async () => {
    const { debugFn, createCardListSelection } = await load('?debug=*,-card-list-selection');
    const { controller } = selection(createCardListSelection);
    controller.decorate(cards([{}]), [{}]);
    expect(debugFn.mock.calls.filter(([prefix]) => prefix === '[cao:card-list-selection]')).toEqual([]);
  });
});
