import { afterEach, describe, expect, it, vi } from 'vitest';

/** @param {string} search @param {Record<string, unknown>[]} rows */
async function load(search, rows) {
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
  vi.doMock('../../src/data/storage/indexeddb.js', () => ({
    queryCollection: async (/** @type {unknown} */ _db, /** @type {unknown} */ _name,
      /** @type {unknown} */ _ops, /** @type {{ keyFilter: (key: string) => boolean }} */ options) => {
      options.keyFilter('a');
      return rows;
    }
  }));
  vi.resetModules();
  const module = await import('../../src/data/queries/measurement-selection.js');
  return { ...module, output };
}

afterEach(() => {
  vi.doUnmock('../../src/debug.js');
  vi.doUnmock('../../src/data/storage/indexeddb.js');
  vi.resetModules();
});

const indexedDB = /** @type {IDBFactory} */ ({});

describe('measurement-selection debug logging', () => {
  it('is disabled by default', async () => {
    const { readMeasurements, output } = await load('', [{ id: 'one' }]);
    await readMeasurements(indexedDB, null);
    expect(output.debug).not.toHaveBeenCalled();
  });

  it('logs scalar read metadata without row contents or selection values', async () => {
    const { readMeasurements, output } = await load('?debug=measurement-selection', [{ id: 'secret-id', note: 'private text' }]);
    await readMeasurements(indexedDB, [{ field: 'id', value: 'secret-value' }]);
    expect(output.debug).toHaveBeenCalledTimes(1);
    const [category, metadata] = output.debug.mock.calls[0];
    expect(category).toBe('[cao:measurement-selection]');
    expect(metadata).toMatchObject({
      event: 'read', mode: 'targeted', selectionCount: 1, keysScanned: 1, recordsRead: 1, rowCount: 1
    });
    expect(JSON.stringify(output.debug.mock.calls)).not.toMatch(/secret|private/);
  });

  it('can be excluded by category', async () => {
    const { readMeasurements, output } = await load('?debug=*,-measurement-selection', [{ id: 'one' }]);
    await readMeasurements(indexedDB, null);
    expect(output.debug).not.toHaveBeenCalled();
  });
});
