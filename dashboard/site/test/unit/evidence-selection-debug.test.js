import { afterEach, describe, expect, it, vi } from 'vitest';

afterEach(() => {
  vi.doUnmock('../../src/debug.js');
  vi.doUnmock('../../src/data/storage/indexeddb.js');
  vi.resetModules();
});

/**
 * @param {string} search
 * @param {Array<Record<string, unknown>>} rows
 */
async function load(search, rows) {
  const output = { debug: vi.fn() };
  vi.doMock('../../src/debug.js', async () => {
    const actual = /** @type {typeof import('../../src/debug.js')} */ (await vi.importActual('../../src/debug.js'));
    return { ...actual, createDebug: (/** @type {string} */ category) => actual.createDebug(category, { search: () => search, output }) };
  });
  vi.doMock('../../src/data/storage/indexeddb.js', () => ({ queryCollection: vi.fn(async () => rows) }));
  vi.resetModules();
  const module = await import('../../src/data/queries/evidence-selection.js');
  return { ...module, output };
}

const fakeIndexedDb = /** @type {IDBFactory} */ (/** @type {unknown} */ ({}));
const rows = [{ id: 'secret-id', facts: [1, 2], body: 'SECRET-BODY' }];
const selections = [{ field: /** @type {'id'} */ ('id'), value: 'secret-value' }];

describe('evidence-selection debug logging', () => {
  it('is silent by default', async () => {
    const { readEvidencePackets, output } = await load('', rows);
    await readEvidencePackets(fakeIndexedDb, selections);
    expect(output.debug).not.toHaveBeenCalled();
  });

  it('logs scalar metadata under the evidence-selection category without sensitive values', async () => {
    const { readEvidencePackets, output } = await load('?debug=evidence-selection', rows);
    await readEvidencePackets(fakeIndexedDb, selections);
    expect(output.debug).toHaveBeenCalledTimes(2);
    const text = JSON.stringify(output.debug.mock.calls);
    expect(text).toContain('evidence-selection');
    expect(text).toContain('read-started');
    expect(text).toContain('read-completed');
    expect(text).not.toContain('secret');
    expect(text).not.toContain('SECRET');
  });

  it('is excluded by another category filter', async () => {
    const { readEvidencePackets, output } = await load('?debug=auth', rows);
    await readEvidencePackets(fakeIndexedDb, null);
    expect(output.debug).not.toHaveBeenCalled();
  });
});
