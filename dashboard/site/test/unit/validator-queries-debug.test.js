import { afterEach, describe, expect, it, vi } from 'vitest';

const validQuery = { name: 'q1', subject: 'test query', from: 'runs', select: [{ field: 'run' }] };

// Intentionally not a mapping, exercising the per-query rejection path.
const invalidQuery = 'not-a-mapping';

const materializationDashboard = {
  queries: [validQuery],
  pages: [
    {
      kind: 'custom',
      id: 'p1',
      title: 'P1',
      views: [
        { id: 'v1', data: { source: 'q1' }, mark: 'metric', encoding: { value: { field: 'run', aggregate: 'count' } } }
      ]
    }
  ]
};

/**
 * Loads validator-queries.js (and its validator-state dependency) with a
 * stubbed debug output so assertions can inspect emitted metadata without
 * depending on module state left over from other tests.
 * @param {string} search
 */
async function loadValidatorQueriesWithDebug(search) {
  const output = { debug: vi.fn() };
  vi.doMock('../../src/debug.js', async () => {
    const actual = /** @type {typeof import('../../src/debug.js')} */ (
      await vi.importActual('../../src/debug.js')
    );
    return {
      ...actual,
      createDebug: (/** @type {string} */ category) => actual.createDebug(category, { search: () => search, output })
    };
  });
  vi.resetModules();
  const validatorQueries = await import('../../src/validator-queries.js');
  const { state } = await import('../../src/validator-state.js');
  return { ...validatorQueries, state, output };
}

afterEach(() => {
  vi.doUnmock('../../src/debug.js');
  vi.resetModules();
});

describe('validator-queries debug logging', () => {
  it('accepts only deterministic workflow-subject page boundaries', async () => {
    const { validateQueries } = await loadValidatorQueriesWithDebug('');
    const page = {
      keys: ['id', 'organization', 'repository'],
      revision: 'workflow-subjects',
      comparator: 'utf8-bytewise-v1'
    };
    const order = page.keys.map((field) => ({ field, direction: 'asc' }));
    const pageQuery = {
      ...validQuery,
      select: page.keys.map((field) => ({ field }))
    };
    const validErrors = /** @type {import('../../src/validator.js').ValidationError[]} */ ([]);
    validateQueries([{ ...pageQuery, page, 'order-by': order }], null, validErrors);
    expect(validErrors).toEqual([]);

    for (const invalid of [
      { ...pageQuery, page, 'order-by': order.slice(0, 2) },
      { ...pageQuery, page, 'order-by': order, limit: 25 },
      { ...pageQuery, page: { ...page, comparator: 'locale' }, 'order-by': order },
      { ...pageQuery, page: { ...page, revision: 'canonical' }, 'order-by': order }
    ]) {
      const errors = /** @type {import('../../src/validator.js').ValidationError[]} */ ([]);
      validateQueries([invalid], null, errors);
      expect(errors.length).toBeGreaterThan(0);
    }
  });

  it('is disabled by default (no debug output) when the debug query is absent', async () => {
    const { validateQueries, validateViewQueryMaterialization, state, output } =
      await loadValidatorQueriesWithDebug('');

    /** @type {import('../../src/validator.js').ValidationError[]} */
    const errors = [];
    state.declaredQueries = validateQueries([validQuery, invalidQuery], null, errors);
    validateViewQueryMaterialization(materializationDashboard, []);

    expect(output.debug).not.toHaveBeenCalled();
  });

  it('logs per-query validation outcomes under its predictable category when enabled', async () => {
    const { validateQueries, state, output } = await loadValidatorQueriesWithDebug('?debug=validator-queries');

    /** @type {import('../../src/validator.js').ValidationError[]} */
    const errors = [];
    state.declaredQueries = validateQueries([validQuery], null, errors);
    expect(errors.length).toBe(0);
    expect(output.debug).toHaveBeenCalledWith(
      '[cao:validator-queries]',
      { operation: 'validate-query', index: 0, status: 'ok' }
    );

    output.debug.mockClear();
    /** @type {import('../../src/validator.js').ValidationError[]} */
    const invalidErrors = [];
    validateQueries([invalidQuery], null, invalidErrors);
    expect(invalidErrors.length).toBeGreaterThan(0);
    expect(output.debug).toHaveBeenCalledWith(
      '[cao:validator-queries]',
      { operation: 'validate-query', index: 0, status: 'invalid' }
    );
  });

  it('logs view-query materialization outcomes under its predictable category when enabled', async () => {
    const { validateQueries, validateViewQueryMaterialization, state, output } =
      await loadValidatorQueriesWithDebug('?debug=validator-queries');

    state.declaredQueries = validateQueries([validQuery], null, []);
    output.debug.mockClear();

    const materializationErrors = /** @type {import('../../src/validator.js').ValidationError[]} */ ([]);
    validateViewQueryMaterialization(materializationDashboard, materializationErrors);

    expect(materializationErrors.length).toBe(0);
    expect(output.debug).toHaveBeenCalledWith(
      '[cao:validator-queries]',
      { operation: 'materialize-view-queries', pageIndex: 0, aliasCount: 1, status: 'ok' }
    );
  });

  it('never includes query names, descriptions, or source rows in logged metadata', async () => {
    const { validateQueries, output } = await loadValidatorQueriesWithDebug('?debug=validator-queries');

    validateQueries([{ ...validQuery, description: 'sensitive text should not be logged' }], null, []);

    for (const call of output.debug.mock.calls) {
      const payload = call[1];
      expect(payload).not.toHaveProperty('name');
      expect(payload).not.toHaveProperty('description');
      expect(JSON.stringify(payload)).not.toContain('sensitive text');
    }
  });
});
