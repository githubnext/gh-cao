import { afterEach, describe, expect, it, vi } from 'vitest';

/** @param {string} search */
async function loadOverlay(search) {
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
  const module = await import('../../src/data/storage/sqlite-overlay.js');
  return { ...module, output };
}

/** @param {string | undefined} revision */
function connection(revision) {
  return /** @type {any} */ ({
    prepare: () => ({ run: () => {}, get: () => (revision === undefined ? undefined : { snapshot_revision: revision }) }),
    exec: () => {}
  });
}

const overlay = { snapshotPath: '/secret/path/snapshot.db', snapshotRevision: 'rev-secret' };

afterEach(() => {
  vi.doUnmock('../../src/debug.js');
  vi.resetModules();
});

describe('sqlite-overlay debug logging', () => {
  it('is disabled by default', async () => {
    const { attachSqliteOverlay, output } = await loadOverlay('');
    attachSqliteOverlay(connection('rev-secret'), overlay);
    expect(output.debug).not.toHaveBeenCalled();
  });

  it('logs attachment under the sqlite-overlay category without sensitive values', async () => {
    const { attachSqliteOverlay, output } = await loadOverlay('?debug=sqlite-overlay');
    attachSqliteOverlay(connection('rev-secret'), overlay);
    expect(output.debug).toHaveBeenCalledWith('[cao:sqlite-overlay]', { event: 'attached', status: 'ok' });
    expect(JSON.stringify(output.debug.mock.calls)).not.toMatch(/secret/);
  });

  it('logs a revision mismatch and still throws', async () => {
    const { attachSqliteOverlay, output } = await loadOverlay('?debug=sqlite-*');
    expect(() => attachSqliteOverlay(connection('other-secret'), overlay)).toThrow('does not match');
    expect(output.debug).toHaveBeenCalledWith('[cao:sqlite-overlay]', {
      event: 'revision-mismatch', status: 'rejected', metadataPresent: true
    });
    expect(JSON.stringify(output.debug.mock.calls)).not.toMatch(/secret/);
  });
});
