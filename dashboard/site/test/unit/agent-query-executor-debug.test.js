import 'fake-indexeddb/auto';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { loadDatabaseQuerySources } from '../../src/data/queries/database.js';
import { DATABASE_NAME } from '../../src/data/storage/indexeddb.js';

const metadata = { 'as-of': '2026-09-09T05:00:00Z', 'artifact-generation': 'generation-a' };

/** @param {number} count */
function runRows(count) {
  return Array.from({ length: count }, (unused, index) => ({
    organization: 'githubnext',
    repository: 'gh-cao',
    workflow: '.github/workflows/dashboard.md',
    run: String(index + 1),
    'run-attempt': 1,
    'run-status': 'completed',
    'run-conclusion': index % 3 === 0 ? 'failure' : 'success',
    'started-at': `2026-09-09T04:0${index % 10}:00Z`,
    'rollout-mode': 'review'
  }));
}

/** @param {number} count */
function fixtureSources(count) {
  return {
    runs: { rows: runRows(count), metadata },
    campaigns: { rows: [], metadata },
    repositories: { rows: [], metadata },
    workflows: { rows: [], metadata },
    tools: { rows: [], metadata },
    audits: { rows: [], metadata },
    domains: { rows: [], metadata },
    issues: { rows: [], metadata }
  };
}

const document = {
  dashboard: {
    id: 'test',
    title: 'Test dashboard',
    navigation: [{ pages: ['runs'] }],
    queries: [
      { name: 'all-runs', subject: 'Show every run.', from: 'runs' },
      { name: 'browser-only', subject: 'Read a browser source.', from: 'work-items' }
    ],
    pages: [
      { id: 'runs', title: 'Runs', views: [{ id: 'runs-table', data: { source: 'all-runs' } }] }
    ]
  }
};

/** @param {number} [count] */
async function ingest(count = 3) {
  await new Promise((resolve, reject) => {
    const request = indexedDB.deleteDatabase(DATABASE_NAME);
    request.onsuccess = () => resolve(undefined);
    request.onerror = () => reject(request.error);
  });
  await loadDatabaseQuerySources(indexedDB, fixtureSources(count), { ingest: true });
}

afterEach(() => {
  vi.doUnmock('../../src/debug.js');
  vi.resetModules();
});

describe('query executor debug logging', () => {
  beforeEach(async () => {
    await ingest();
  });

  it('is disabled by default (no debug output) when the debug query is absent', async () => {
    const output = { debug: vi.fn() };
    vi.doMock('../../src/debug.js', async () => {
      const actual = /** @type {typeof import('../../src/debug.js')} */ (
        await vi.importActual('../../src/debug.js')
      );
      return {
        ...actual,
        createDebug: (/** @type {string} */ category) => actual.createDebug(category, { search: () => '', output })
      };
    });
    vi.resetModules();
    const { executeNamedQuery } = await import('../../src/agent/query-executor.js');

    await executeNamedQuery({ indexedDB, document, queryId: 'all-runs' });
    await expect(executeNamedQuery({ indexedDB, document, queryId: 'not-a-real-query' })).rejects.toThrow();
    await expect(executeNamedQuery({ indexedDB, document, queryId: 'all-runs', limit: 0 })).rejects.toThrow();
    await executeNamedQuery({ indexedDB, document, queryId: 'browser-only' });

    expect(output.debug).not.toHaveBeenCalled();
  });

  it('logs execution, unavailable, and unknown-query outcomes under its predictable category when enabled', async () => {
    const output = { debug: vi.fn() };
    vi.doMock('../../src/debug.js', async () => {
      const actual = /** @type {typeof import('../../src/debug.js')} */ (
        await vi.importActual('../../src/debug.js')
      );
      return {
        ...actual,
        createDebug: (/** @type {string} */ category) =>
          actual.createDebug(category, { search: () => '?debug=query-executor', output })
      };
    });
    vi.resetModules();
    const { executeNamedQuery } = await import('../../src/agent/query-executor.js');

    const result = await executeNamedQuery({ indexedDB, document, queryId: 'all-runs' });
    expect(output.debug).toHaveBeenCalledWith(
      '[cao:query-executor]',
      expect.objectContaining({ query: 'all-runs', outcome: 'executed', 'returned-rows': result.rows.length })
    );

    output.debug.mockClear();
    await executeNamedQuery({ indexedDB, document, queryId: 'browser-only' });
    expect(output.debug).toHaveBeenCalledWith(
      '[cao:query-executor]',
      { query: 'browser-only', outcome: 'unavailable', missingCount: 1 }
    );

    output.debug.mockClear();
    await expect(executeNamedQuery({ indexedDB, document, queryId: 'not-a-real-query' })).rejects.toThrow();
    expect(output.debug).toHaveBeenCalledWith(
      '[cao:query-executor]',
      { query: 'not-a-real-query', outcome: 'unknown-query' }
    );
  });

  it('never logs sensitive payload content, only scalar metadata', async () => {
    const output = { debug: vi.fn() };
    vi.doMock('../../src/debug.js', async () => {
      const actual = /** @type {typeof import('../../src/debug.js')} */ (
        await vi.importActual('../../src/debug.js')
      );
      return {
        ...actual,
        createDebug: (/** @type {string} */ category) =>
          actual.createDebug(category, { search: () => '?debug=query-executor', output })
      };
    });
    vi.resetModules();
    const { executeNamedQuery } = await import('../../src/agent/query-executor.js');

    await executeNamedQuery({ indexedDB, document, queryId: 'all-runs' });
    await executeNamedQuery({ indexedDB, document, queryId: 'browser-only' });
    await expect(executeNamedQuery({ indexedDB, document, queryId: 'not-a-real-query' })).rejects.toThrow();

    for (const call of output.debug.mock.calls) {
      const [, payload] = call;
      for (const value of Object.values(payload)) {
        expect(typeof value === 'string' || typeof value === 'number' || typeof value === 'boolean').toBe(true);
      }
      expect(JSON.stringify(payload)).not.toContain('work-items');
    }
  });

  it('logs only a stable reason when a request is rejected, never parameter names or values', async () => {
    const output = { debug: vi.fn() };
    vi.doMock('../../src/debug.js', async () => {
      const actual = /** @type {typeof import('../../src/debug.js')} */ (
        await vi.importActual('../../src/debug.js')
      );
      return {
        ...actual,
        createDebug: (/** @type {string} */ category) =>
          actual.createDebug(category, { search: () => '?debug=query-executor', output })
      };
    });
    vi.resetModules();
    const { executeNamedQuery } = await import('../../src/agent/query-executor.js');

    await expect(executeNamedQuery({ indexedDB, document, queryId: '  ' })).rejects.toThrow();
    await expect(executeNamedQuery({ indexedDB, document, queryId: 'all-runs', limit: 0 })).rejects.toThrow();
    await expect(executeNamedQuery({
      indexedDB, document, queryId: 'all-runs', parameters: { 'secret-parameter-name': 'secret-value' }
    })).rejects.toThrow();
    await expect(executeNamedQuery({
      indexedDB, document, queryId: 'all-runs', parameters: { 'secret-parameter-name': { nested: 'secret-value' } }
    })).rejects.toThrow();

    expect(output.debug.mock.calls.map(([, payload]) => payload)).toEqual([
      { outcome: 'rejected', reason: 'missing-query-id' },
      { outcome: 'rejected', reason: 'invalid-limit' },
      { outcome: 'rejected', reason: 'unknown-parameter' },
      { outcome: 'rejected', reason: 'unknown-parameter' }
    ]);
    expect(JSON.stringify(output.debug.mock.calls)).not.toContain('secret');
  });
});
