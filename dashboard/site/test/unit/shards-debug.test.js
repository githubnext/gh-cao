import 'fake-indexeddb/auto';
import { IDBFactory } from 'fake-indexeddb';
import { afterEach, describe, expect, it, vi } from 'vitest';

const SECRET = 'secret-token-value';

afterEach(() => {
  vi.doUnmock('../../src/debug.js');
  vi.resetModules();
});

/** @param {ReturnType<typeof vi.fn>} debugFn @param {string} search */
function mockDebugModule(debugFn, search) {
  const output = /** @type {Pick<Console, 'debug'>} */ ({ debug: debugFn });
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
}

/** @param {string} search */
async function rebuildWithRecords(search) {
  const debugFn = vi.fn();
  mockDebugModule(debugFn, search);
  const { openCanonicalDatabase, upsertCanonicalBatch } = await import('../../src/data/storage/indexeddb.js');
  const { normalize } = await import('../../src/data/normalize/index.js');
  const { rebuildStorageShards } = await import('../../src/data/storage/shards.js');
  const indexedDB = new IDBFactory();
  await upsertCanonicalBatch(indexedDB, { ...normalize([]), repositories: [{ id: `repo:${SECRET}` }] });
  const database = await openCanonicalDatabase(indexedDB);
  try {
    await rebuildStorageShards(database, ['repositories']);
    return { debugFn, database, rebuildStorageShards };
  } finally {
    database.close();
  }
}

describe('shards debug logging', () => {
  it('is disabled by default', async () => {
    const { debugFn } = await rebuildWithRecords('');
    expect(debugFn).not.toHaveBeenCalled();
  });

  it('logs a rebuild under the predictable shards category without record content', async () => {
    const { debugFn } = await rebuildWithRecords('?debug=shards');
    expect(debugFn).toHaveBeenCalledWith(
      '[cao:shards]',
      { event: 'rebuilt', storeCount: 1, durationMs: expect.any(Number) }
    );
    expect(JSON.stringify(debugFn.mock.calls)).not.toContain(SECRET);
  });

  it('does not log when the category is excluded', async () => {
    const { debugFn } = await rebuildWithRecords('?debug=*,-shards');
    expect(debugFn).not.toHaveBeenCalledWith('[cao:shards]', expect.anything());
  });

  it('logs an aborted write with a sanitized error name and no message', async () => {
    const debugFn = vi.fn();
    mockDebugModule(debugFn, '?debug=shards');
    const { rebuildStorageShards } = await import('../../src/data/storage/shards.js');
    const transaction = {
      onabort: /** @type {(() => void) | null} */ (null),
      error: null,
      objectStore: () => { throw Object.assign(new Error(`failure ${SECRET}`), { name: 'InvalidStateError' }); },
      abort() { queueMicrotask(() => this.onabort?.()); }
    };
    const failingDatabase = /** @type {IDBDatabase} */ (/** @type {unknown} */ ({ transaction: () => transaction }));
    await expect(rebuildStorageShards(failingDatabase, [])).rejects.toThrow();
    expect(debugFn).toHaveBeenCalledWith(
      '[cao:shards]',
      { event: 'write-aborted', storeCount: 0, errorName: 'InvalidStateError' }
    );
    expect(JSON.stringify(debugFn.mock.calls)).not.toContain(SECRET);
  });
});
