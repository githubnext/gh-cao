// @vitest-environment node
import 'fake-indexeddb/auto';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { adaptCachedGhAwJsonl } from '../../src/data/adapters/gh-aw-logs.js';
import { CANONICAL_SCHEMA_VERSION } from '../../src/data/model/schema.js';
import { normalize } from '../../src/data/normalize/index.js';
import { DATABASE_NAME, readTransaction } from '../../src/data/storage/indexeddb.js';

vi.mock('../../src/retry.js', async (importOriginal) => {
  const retry = /** @type {typeof import('../../src/retry.js')} */ (await importOriginal());
  return {
    ...retry,
    withRetries: (
      /** @type {(attempt: number) => Promise<unknown>} */ operation,
      /** @type {{ attempts?: number, delayMs?: number }} */ options = {}
    ) => retry.withRetries(operation, { ...options, delayMs: 0 })
  };
});

beforeEach(async () => {
  await new Promise((resolve, reject) => {
    const request = indexedDB.deleteDatabase(DATABASE_NAME);
    request.onsuccess = () => resolve(undefined);
    request.onerror = () => reject(request.error);
  });
});

describe('canonical dashboard worker ingestion order', () => {
  it.each([1, 100])('stores JSONL run records once and reserves preparation progress with %i shards', async (shardCount) => {
    /** @type {Map<string, (event: { data: Record<string, unknown> }) => void>} */
    const listeners = new Map();
    /** @type {Record<string, unknown>[]} */
    const posted = [];
    /** @type {(value: Record<string, unknown>) => void} */
    let resolveResponse;
    const response = new Promise((resolve) => {
      resolveResponse = resolve;
    });
    globalThis.self = /** @type {typeof globalThis.self} */ (/** @type {unknown} */ ({
      addEventListener: (/** @type {string} */ type, /** @type {(event: { data: Record<string, unknown> }) => void} */ listener) => {
        listeners.set(type, listener);
      },
      postMessage: (/** @type {Record<string, unknown>} */ message) => {
        posted.push(structuredClone(message));
        if (message.id === 1) resolveResponse(message);
      }
    }));
    vi.resetModules();
    await import('../../src/data-worker.js');

    const run = {
      schema_version: 2,
      kind: 'run',
      run: {
        run_id: 303,
        run_attempt: 1,
        organization: 'githubnext',
        repository: 'gh-cao',
        workflow_name: 'Dashboard',
        workflow_path: '.github/workflows/dashboard.md',
        status: 'completed',
        classification: 'success',
        created_at: '2026-09-09T05:00:00Z'
      }
    };
    const batch = normalize(adaptCachedGhAwJsonl(`${JSON.stringify(run)}\n`).observations);
    /** @type {(keyof import('../../src/data/model/schema.js').CanonicalBatch)[]} */
    const runCollections = ['campaigns', 'repositories', 'workflows', 'runs'];
    const runRecords = runCollections.flatMap((collection) =>
      (batch[collection] ?? []).map((record) => ({ kind: 'record', collection, record }))
    );
    const normalizedRuns = [
      {
        kind: 'metadata',
        schemaVersion: CANONICAL_SCHEMA_VERSION,
        ingestionVersion: 4,
        sourceRecords: 1,
        phase: 'runs',
        records: runRecords.length
      },
      ...runRecords
    ].map((line) => JSON.stringify(line)).join('\n') + '\n';
    const runNames = Array.from({ length: shardCount }, (_, index) =>
      `gh-aw-logs-runs/logs-${'a'.repeat(64)}-${index.toString(16).padStart(16, '0')}.jsonl`);
    const inventory = {
      repositories: {
        rows: [{ organization: 'githubnext', repository: 'gh-cao' }],
        metadata: { 'as-of': '2026-09-09T05:00:00Z' }
      }
    };
    /** @type {string[]} */
    const requestedUrls = [];
    /** @type {(RequestInit | undefined)[]} */
    const inventoryRequests = [];
    let inventoryAttempts = 0;
    globalThis.fetch = /** @type {typeof fetch} */ (async (input, init) => {
      const url = String(input);
      requestedUrls.push(url);
      if (url.endsWith('/inventory-sources.json')) {
        inventoryRequests.push(init);
        inventoryAttempts += 1;
        if (inventoryAttempts < 3) throw new TypeError('temporary network failure');
        return Response.json(inventory);
      }
      if (url.endsWith('/payload-hashes.json')) {
        return Response.json(Object.fromEntries(runNames.map((name) => [name, 'a'.repeat(64)])));
      }
      return new Response(normalizedRuns);
    });

    /** @type {string[]} */
    const storedRunIds = [];
    const originalPut = IDBObjectStore.prototype.put;
    const put = vi.spyOn(IDBObjectStore.prototype, 'put').mockImplementation(/** @this {IDBObjectStore} */ function (value, key) {
      if (this.name === 'runs') storedRunIds.push(String(value.id));
      return key === undefined
        ? originalPut.call(this, value)
        : originalPut.call(this, value, key);
    });
    try {
      listeners.get('message')?.({
        data: {
          id: 1,
          operation: 'load-canonical-dashboard',
          sourceUrl: 'https://dashboard.example/payload-hashes.json',
          sourceNames: [],
          context: { pages: [], queries: [] }
        }
      });
      await response;
    } finally {
      put.mockRestore();
    }

    expect(posted.find((message) => message.id === 1)?.error).toBeUndefined();
    await expect(readTransaction(indexedDB, 'dashboard-snapshot:complete')).resolves.toMatchObject({
      kind: 'dashboard-snapshot',
      createdAt: expect.any(String)
    });
    expect(requestedUrls.slice(0, 4)).toEqual([
      'https://dashboard.example/inventory-sources.json',
      'https://dashboard.example/inventory-sources.json',
      'https://dashboard.example/inventory-sources.json',
      'https://dashboard.example/payload-hashes.json'
    ]);
    expect(inventoryRequests).toEqual([
      expect.objectContaining({ cache: 'no-store', signal: expect.any(AbortSignal) }),
      expect.objectContaining({ cache: 'no-store', signal: expect.any(AbortSignal) }),
      expect.objectContaining({ cache: 'no-store', signal: expect.any(AbortSignal) })
    ]);
    expect(storedRunIds).toEqual(['github:run:githubnext/gh-cao:303']);
    const updates = posted.filter((message) => message.type === 'loading-progress'
      && /** @type {{ phase?: string }} */ (message.state)?.phase === 'update')
      .map((message) => /** @type {{ completed: number, total: number, stage: string }} */ (message.state));
    const preparationStepWeight = Math.max(1, Math.ceil(shardCount / 7));
    const totalSteps = shardCount + 3 * preparationStepWeight;
    expect(updates.some(({ stage, completed, total }) =>
      stage === 'files' && completed === shardCount && total === totalSteps)).toBe(true);
    expect(updates.some(({ stage, completed, total }) =>
      stage === 'maintenance' && completed > shardCount + preparationStepWeight
      && completed < shardCount + 2 * preparationStepWeight && total === totalSteps)).toBe(true);
    const inventoryProgress = updates.find(({ stage }) => stage === 'inventory');
    const maintenanceProgress = updates.find(({ stage }) => stage === 'maintenance');
    const queryProgress = updates.at(-1);
    expect(inventoryProgress).toMatchObject({
      stage: 'inventory', completed: shardCount, total: totalSteps
    });
    expect(maintenanceProgress).toMatchObject({
      stage: 'maintenance', completed: shardCount + preparationStepWeight, total: totalSteps
    });
    expect(queryProgress).toMatchObject({
      stage: 'queries', completed: shardCount + 2 * preparationStepWeight, total: totalSteps
    });
    if (!inventoryProgress || !maintenanceProgress || !queryProgress) throw new Error('Missing post-shard progress.');
    const stageBoundaries = [
      shardCount,
      maintenanceProgress.completed,
      queryProgress.completed,
      totalSteps
    ].map((completed) => completed / totalSteps);
    for (let index = 1; index < stageBoundaries.length; index += 1) {
      expect(stageBoundaries[index] - stageBoundaries[index - 1]).toBeGreaterThanOrEqual(0.1 - Number.EPSILON);
    }
    expect(updates.every(({ completed, total }) => completed < total)).toBe(true);
    expect(posted.findIndex((message) => message.type === 'loading-progress'
      && /** @type {{ phase?: string }} */ (message.state)?.phase === 'complete'))
      .toBeGreaterThan(posted.findIndex((message) => message.type === 'loading-progress'
        && /** @type {{ stage?: string }} */ (message.state)?.stage === 'queries'));
    requestedUrls.length = 0;
    const { processDataRequest } = await import('../../src/data-worker.js');
    await processDataRequest({
      operation: 'load-canonical-dashboard',
      sourceUrl: 'https://dashboard.example/payload-hashes.json',
      sourceNames: [],
      context: { pages: [], queries: [] }
    });
    expect(requestedUrls).toEqual([
      'https://dashboard.example/inventory-sources.json',
      'https://dashboard.example/payload-hashes.json'
    ]);
  }, 30_000);

  it('recovers from transient manifest and shard failures without accepting a persistently missing shard', async () => {
    globalThis.self = /** @type {typeof globalThis.self} */ (/** @type {unknown} */ ({
      addEventListener: () => {},
      postMessage: () => {}
    }));
    vi.resetModules();
    const { processDataRequest } = await import('../../src/data-worker.js');
    const shardName = `gh-aw-logs-runs/logs-${'a'.repeat(64)}-${'b'.repeat(16)}.jsonl`;
    const sourceUrl = 'https://dashboard.example/payload-hashes.json';
    const shardUrl = `https://dashboard.example/${shardName}`;
    const normalizedRuns = `${JSON.stringify({
      kind: 'metadata',
      schemaVersion: CANONICAL_SCHEMA_VERSION,
      ingestionVersion: 4,
      sourceRecords: 0,
      phase: 'runs',
      records: 0
    })}\n`;
    let manifestAttempts = 0;
    let shardAttempts = 0;
    globalThis.fetch = /** @type {typeof fetch} */ (async (input, init) => {
      const url = String(input);
      if (url.endsWith('/inventory-sources.json')) return new Response(null, { status: 404 });
      if (url === sourceUrl) {
        manifestAttempts += 1;
        if (manifestAttempts === 1) return new Response(null, { status: 503 });
        return Response.json({ [shardName]: 'a'.repeat(64) });
      }
      if (url === shardUrl && init?.method === 'HEAD') return new Response(null, { status: 200 });
      if (url === shardUrl) {
        expect(init?.cache).toBe('no-store');
        shardAttempts += 1;
        if (shardAttempts === 1) return new Response(null, { status: 404 });
        return new Response(normalizedRuns);
      }
      throw new Error(`Unexpected dashboard request: ${url}`);
    });
    const request = {
      operation: 'load-canonical-dashboard',
      sourceUrl,
      sourceNames: [],
      context: { pages: [], queries: [] },
      reportActivation: true
    };

    await expect(processDataRequest(request)).resolves.toMatchObject({ changed: true });
    expect(manifestAttempts).toBe(2);
    expect(shardAttempts).toBe(2);
    const snapshot = await readTransaction(indexedDB, 'dashboard-snapshot:complete');
    expect(snapshot).toMatchObject({ kind: 'dashboard-snapshot' });

    globalThis.fetch = /** @type {typeof fetch} */ (async (input, init) => {
      const url = String(input);
      if (url.endsWith('/inventory-sources.json')) return new Response(null, { status: 404 });
      if (url === sourceUrl) return Response.json({ [shardName]: 'c'.repeat(64) });
      if (url === shardUrl && init?.method === 'HEAD') return new Response(null, { status: 200 });
      if (url === shardUrl) {
        shardAttempts += 1;
        return new Response(null, { status: 404 });
      }
      throw new Error(`Unexpected dashboard request: ${url}`);
    });
    await expect(processDataRequest(request)).rejects.toThrow(`Unable to load activity shard ${shardName}: 404`);
    expect(shardAttempts).toBe(5);
    expect(await readTransaction(indexedDB, 'dashboard-snapshot:complete')).toEqual(snapshot);

    let failedManifestAttempts = 0;
    globalThis.fetch = /** @type {typeof fetch} */ (async (input) => {
      if (String(input).endsWith('/inventory-sources.json')) return new Response(null, { status: 404 });
      if (String(input) === sourceUrl) {
        failedManifestAttempts += 1;
        return new Response(null, { status: 503 });
      }
      throw new Error(`Unexpected dashboard request: ${String(input)}`);
    });
    await expect(processDataRequest(request)).rejects.toThrow('Unable to load activity shard manifest: 503');
    expect(failedManifestAttempts).toBe(3);
    expect(await readTransaction(indexedDB, 'dashboard-snapshot:complete')).toEqual(snapshot);
  });
});
