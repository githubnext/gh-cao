import 'fake-indexeddb/auto';
import { rmSync } from 'node:fs';
import { IDBFactory } from 'fake-indexeddb';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { normalize } from '../../src/data/normalize/index.js';
import { readProtectedRunCountBytes } from '../../src/data/model/daily-run-counts.js';
import { CANONICAL_SCHEMA_VERSION, relationshipErrors } from '../../src/data/model/schema.js';
import { finalizeNormalizedJsonlIngestion, ingestNormalizedJsonl } from '../../src/data/ingest/coordinator.js';
import {
  maintainCanonicalDatabase, openCanonicalDatabase, prepareCanonicalRecord,
  readCanonicalBatch, readCollection, upsertCanonicalBatch
} from '../../src/data/storage/indexeddb.js';
import { processDataRequest } from '../../src/data-worker.js';
import sharedEvents from '../../dashboard-fragments/shared-events.json' with { type: 'json' };

const now = Date.parse('2026-10-20T12:00:00Z');
const options = { now, maxDatabaseBytes: Number.MAX_SAFE_INTEGER };
const directories = /** @type {string[]} */ ([]);

afterEach(() => {
  vi.unstubAllGlobals();
  for (const directory of directories.splice(0)) rmSync(directory, { recursive: true, force: true });
});

/** @param {string[]} conclusions */
function fixture(conclusions = ['success', 'failure', 'failure', 'success', 'failure', 'failure']) {
  const batch = normalize([]);
  batch.repositories = [{ id: 'repo:1', owner: 'octo', name: 'control' }];
  batch.workflows = [{
    id: 'workflow:1', repositoryId: 'repo:1', role: 'worker', path: '.github/workflows/worker.md'
  }];
  batch.runs = conclusions.map((conclusion, index) => {
    const timestamp = new Date(now - (10 - index) * 60_000).toISOString();
    return {
      id: `github:run:octo/control:${index + 1}`, githubRunId: index + 1, attempt: 1,
      repositoryId: 'repo:1', workflowId: 'workflow:1', owner: 'octo', repository: 'control',
      workflowPath: '.github/workflows/worker.md', targetRepository: 'octo/target',
      event: 'workflow_dispatch', branch: 'main', rolloutMode: 'review',
      status: 'completed', conclusion, startedAt: timestamp, completedAt: timestamp, observedAt: timestamp,
      aicTotal: index + 1, inputTokens: index * 100, operationalGrader: index === 2 ? null : 0.8,
      failureKind: conclusion === 'failure' ? 'driver_exit' : null,
      failureMessage: conclusion === 'failure' ? 'Agent exited' : null,
      ...(conclusion === 'failure' ? { failureLog: `run ${index + 1}\n${'diagnostic line\n'.repeat(300)}` } : {})
    };
  });
  batch.audits = batch.runs.map((run) => ({
    id: `audit:${run.githubRunId}`, runId: run.id, timestamp: run.startedAt, source: 'audit',
    type: 'audit.finding', status: 'high', summary: 'Specific diagnostic', code: 'driver_exit'
  }));
  return batch;
}

/** @param {IDBFactory} indexedDB */
async function storedTotals(indexedDB) {
  const database = await openCanonicalDatabase(indexedDB);
  try {
    return await new Promise((resolve, reject) => {
      const request = database.transaction('storageShards').objectStore('storageShards').get('totals');
      request.onsuccess = () => resolve(request.result);
      request.onerror = () => reject(request.error);
    });
  } finally {
    database.close();
  }
}

/** @param {IDBFactory} indexedDB @param {Record<string, unknown>[]} runs @param {string} identity */
async function importRuns(indexedDB, runs, identity) {
  const content = [
    { kind: 'metadata', schemaVersion: CANONICAL_SCHEMA_VERSION, ingestionVersion: 4, phase: 'runs', records: runs.length },
    ...runs.map((record) => ({ kind: 'record', collection: 'runs', record }))
  ].map((row) => JSON.stringify(row)).join('\n');
  async function* chunks() { yield content; }
  await ingestNormalizedJsonl(indexedDB, chunks(), {
    ...options, payloadIdentity: identity, payloadScope: identity, expectedPhase: 'runs', deferMaintenance: true
  });
  return finalizeNormalizedJsonlIngestion(indexedDB, options);
}

describe('resolved failure logs in IndexedDB', () => {
  function database() {
    return new IDBFactory();
  }

  it('reclaims persisted log bytes without changing statistical or comparison evidence', async () => {
    const indexedDB = database();
    vi.stubGlobal('indexedDB', indexedDB);
    const input = fixture();
    const runId = input.runs[1].id;
    const timestamp = input.runs[1].startedAt;
    const provenance = { source: 'fixture', sourceId: 'comparison', observedAt: timestamp };
    input.tools.push({ id: 'tool:1', runId, timestamp, type: 'mcp.tool_call', source: 'mcp',
      mcpServer: 'github', mcpTool: 'search', requestBytes: 10, responseBytes: 20 });
    input.domains.push({ id: 'domain:1', runId, timestamp, type: 'firewall.allow', domain: 'github.com' });
    input.friction.push({ id: 'friction:1', runId, timestamp, aic: 0.25, measurementState: 'measured' });
    input.experiments?.push({ id: 'experiment:1', workflowId: 'workflow:1', name: 'prompt', observedAt: timestamp, provenance });
    input.experimentAssignments?.push({ id: 'assignment:1', runId, experimentId: 'experiment:1',
      variant: 'control', timestamp, observedAt: timestamp, provenance, auditId: 'audit:2' });
    input.graders?.push({ id: 'grader:1', workflowId: 'workflow:1', name: 'quality', observedAt: timestamp, provenance });
    input.graderObservations?.push({ id: 'grade:1', runId, graderId: 'grader:1', value: null,
      timestamp, observedAt: timestamp, provenance, auditId: 'audit:2', evaluatorDigest: 'sha256:frozen' });
    input.evals?.push({ id: 'eval:1', workflowId: 'workflow:1', name: 'correctness', observedAt: timestamp, provenance });
    input.evalObservations?.push({ id: 'eval-result:1', runId, evalId: 'eval:1', evalResult: 'UNKNOWN',
      timestamp, observedAt: timestamp, provenance, auditId: 'audit:2' });
    input.audits.push({ id: 'comparison:1', runId, timestamp, source: 'token-intervention-lifecycle',
      type: 'optimization.intervention.updated', interventionState: 'awaiting-evidence', comparisonId: 'comparison:1' });
    await upsertCanonicalBatch(indexedDB, input);
    const statistics = () => processDataRequest({
      operation: 'load-dashboard-query-sources', sources: {}, queries: sharedEvents.queries,
      sourceNames: ['workflow-run-totals', 'workflow-aic-run-totals', 'grader-observations',
        'eval-observations', 'experiment-assignments', 'friction', 'tools', 'domains', 'audits']
    });
    const before = await statistics();
    expect(before).toMatchObject({
      'workflow-run-totals': {
        rows: [{ runs: 6, 'successful-runs': 2, 'failed-runs': 4, 'imported-runs': 6 }],
        metadata: { availability: 'available' }
      },
      'workflow-aic-run-totals': { rows: [{ runs: 6, 'aic-runs': 6, aic: 21 }] }
    });
    const bytes = (await storedTotals(indexedDB)).bytes;
    const result = await maintainCanonicalDatabase(indexedDB, options);
    expect(result).toMatchObject({ prunedFailureLogs: 2, deletedRecords: 0 });
    const retained = await readCanonicalBatch(indexedDB);
    expect(retained.runs.filter((run) => typeof run.failureLog === 'string').map((run) => run.githubRunId)).toEqual([5, 6]);
    const expected = structuredClone(input);
    delete expected.runs[1].failureLog;
    delete expected.runs[2].failureLog;
    expect(retained).toEqual(expected);
    expect(relationshipErrors(retained)).toEqual([]);
    expect(await statistics()).toEqual(before);
    const connection = await openCanonicalDatabase(indexedDB);
    try {
      const stored = await new Promise((resolve, reject) => {
        const request = connection.transaction('runs').objectStore('runs').get(String(input.runs[1].id));
        request.onsuccess = () => resolve(request.result);
        request.onerror = () => reject(request.error);
      });
      expect(stored).not.toHaveProperty('failureLog');
      expect(stored._storage.failureLogPruned).toBe(true);
    } finally {
      connection.close();
    }
    const totals = await storedTotals(indexedDB);
    expect(bytes - totals.bytes).toBe(2 * (JSON.stringify({ failureLog: input.runs[1].failureLog }).length - 1));
    expect(totals.bytes).toBe(Object.entries(retained).reduce((sum, [store, rows]) =>
      sum + rows.reduce((bytes, row) => bytes + prepareCanonicalRecord(store, row)._storage.bytes, 0), 0));
    expect((await maintainCanonicalDatabase(indexedDB, options)).prunedFailureLogs).toBe(0);
  });

  it('handles incremental recovery, out-of-order imports, cached replay and stale attempts', async () => {
    const indexedDB = database();
    const input = fixture();
    await upsertCanonicalBatch(indexedDB, { ...input, runs: [], audits: [] });
    await importRuns(indexedDB, input.runs.slice(0, 3), 'before-recovery');
    expect((await readCollection(indexedDB, 'runs')).filter((run) => run.failureLog)).toHaveLength(2);
    expect((await importRuns(indexedDB, [input.runs[3]], 'recovery')).prunedFailureLogs).toBe(2);
    await importRuns(indexedDB, input.runs, 'older-and-current');
    await importRuns(indexedDB, input.runs, 'older-and-current');
    expect((await readCollection(indexedDB, 'runs')).filter((run) => run.failureLog).map((run) => run.githubRunId)).toEqual([5, 6]);
    await importRuns(indexedDB, [{ ...input.runs[3], attempt: 2 }], 'winning-attempt');
    await importRuns(indexedDB, [{ ...input.runs[3], conclusion: 'failure', failureLog: 'obsolete attempt' }], 'stale-attempt');
    expect((await readCollection(indexedDB, 'runs')).find((run) => run.githubRunId === 4))
      .toMatchObject({ attempt: 2, conclusion: 'success' });
    await importRuns(indexedDB, [{ ...input.runs[1], attempt: 2, startedAt: new Date(now).toISOString() }], 'new-failure-attempt');
    expect((await readCollection(indexedDB, 'runs')).find((run) => run.githubRunId === 2)).toHaveProperty('failureLog');
  });

  it('prunes late-delivered failures and never restores their excerpts after recovery evidence changes', async () => {
    const indexedDB = database();
    const input = fixture(['failure', 'success']);
    await upsertCanonicalBatch(indexedDB, { ...input, runs: [input.runs[1]], audits: [] });
    await importRuns(indexedDB, [input.runs[0]], 'late-failure');
    expect((await readCollection(indexedDB, 'runs'))[0]).not.toHaveProperty('failureLog');
    await importRuns(indexedDB, [{ ...input.runs[1], attempt: 2, conclusion: 'failure' }], 'recovery-rerun');
    await importRuns(indexedDB, [input.runs[0]], 'failure-replay');
    expect((await readCollection(indexedDB, 'runs'))[0]).not.toHaveProperty('failureLog');
    await importRuns(indexedDB, [{ ...input.runs[0], attempt: undefined }], 'unknown-attempt-replay');
    expect((await readCollection(indexedDB, 'runs'))[0]).toMatchObject({ attempt: 1 });
    expect((await readCollection(indexedDB, 'runs'))[0]).not.toHaveProperty('failureLog');
    const fresh = { ...input.runs[0], id: 'github:run:octo/control:3', githubRunId: 3 };
    await importRuns(indexedDB, [fresh], 'unresolved-failure');
    await importRuns(indexedDB, [input.runs[1]], 'obsolete-success');
    expect((await readCollection(indexedDB, 'runs')).find((run) => run.githubRunId === 3)).toHaveProperty('failureLog');
  });

  it('does not use an older observation of the winning attempt as recovery', async () => {
    const indexedDB = database();
    const input = fixture(['failure', 'success']);
    await upsertCanonicalBatch(indexedDB, {
      ...input, runs: [input.runs[0], { ...input.runs[1], conclusion: 'failure', observedAt: new Date(now).toISOString() }]
    });
    await importRuns(indexedDB, [input.runs[1]], 'older-observation');
    expect((await readCollection(indexedDB, 'runs'))[0]).toHaveProperty('failureLog');
    expect((await readCollection(indexedDB, 'runs'))[1].conclusion).toBe('failure');
  });

  it.each([
    { repositoryId: 'repo:other' }, { workflowId: 'workflow:other' },
    { targetRepository: 'octo/another' }, { targetRepository: null },
    { event: 'push' }, { branch: 'feature' }, { rolloutMode: 'live' },
    { conclusion: 'cancelled' }, { conclusion: null }, { status: 'in_progress' },
    { terminalOutcome: 'report_incomplete' }, { startedAt: null, createdAt: null, updatedAt: null }
  ])('does not mistake unrelated or incomplete evidence for recovery: %j', async (different) => {
    const indexedDB = database();
    const input = fixture(['failure', 'success']);
    Object.assign(input.runs[1], different);
    await upsertCanonicalBatch(indexedDB, input, { validateRelationships: false });
    await maintainCanonicalDatabase(indexedDB, options);
    expect((await readCollection(indexedDB, 'runs'))[0]).toHaveProperty('failureLog');
  });

  it.each(['branch', 'rolloutMode', 'unknown-role'])('does not equate two unknown scope dimensions: %s', async (dimension) => {
    const indexedDB = database();
    const input = fixture(['failure', 'success']);
    if (dimension === 'unknown-role') {
      delete input.workflows[0].role;
      input.runs.forEach((run) => { delete run.targetRepository; });
    } else {
      input.runs.forEach((run) => { delete run[dimension]; });
    }
    await upsertCanonicalBatch(indexedDB, input);
    expect((await maintainCanonicalDatabase(indexedDB, options)).prunedFailureLogs).toBe(0);
  });

  it('uses run-date and numeric run-ID ordering, not arrival or completion time', async () => {
    const indexedDB = database();
    const input = fixture(['failure', 'success']);
    input.runs[0].githubRunId = 9;
    input.runs[1].githubRunId = 10;
    input.runs[0].completedAt = new Date(now).toISOString();
    input.runs[1].startedAt = input.runs[0].startedAt;
    await upsertCanonicalBatch(indexedDB, input);
    expect((await maintainCanonicalDatabase(indexedDB, options)).prunedFailureLogs).toBe(1);
  });

  it('keeps no-success and current streaks within existing detail and database limits', async () => {
    const indexedDB = database();
    const input = fixture(['failure', 'failure']);
    const old = new Date(now - 8 * 86_400_000).toISOString();
    Object.assign(input.runs[0], { startedAt: old, completedAt: old, observedAt: old });
    input.audits = [];
    await upsertCanonicalBatch(indexedDB, input);
    expect((await maintainCanonicalDatabase(indexedDB, options)).prunedFailureLogs).toBe(1);
    const retained = await readCollection(indexedDB, 'runs');
    expect(retained).toHaveLength(2);
    expect(retained[0]).not.toHaveProperty('failureLog');
    expect(retained[1]).toHaveProperty('failureLog');
    await maintainCanonicalDatabase(indexedDB, { ...options, maxDatabaseBytes: 0 });
    expect(await readCollection(indexedDB, 'runs')).toEqual([]);
  });

  it.each([false, true])('reclaims excerpts before whole-Run size eviction (repair=%s)', async (repairAuditCuration) => {
    const indexedDB = database();
    await maintainCanonicalDatabase(indexedDB, options);
    const input = fixture(['failure', 'success']);
    await upsertCanonicalBatch(indexedDB, input);
    const bytes = (await storedTotals(indexedDB)).bytes;
    const connection = await openCanonicalDatabase(indexedDB);
    const protectedBytes = await readProtectedRunCountBytes(connection);
    connection.close();
    const savings = JSON.stringify({ failureLog: input.runs[0].failureLog }).length - 2;
    const result = await maintainCanonicalDatabase(indexedDB, {
      ...options, repairAuditCuration, maxDatabaseBytes: Math.ceil((bytes - savings + protectedBytes + 10) / 0.75)
    });
    expect(result).toMatchObject({ deletedRecords: 0, prunedFailureLogs: 1 });
    expect(await readCollection(indexedDB, 'runs')).toHaveLength(2);
  });

  it('discovers excerpts in an existing cache without requiring a changed source receipt', async () => {
    const indexedDB = database();
    const input = fixture(['failure', 'success']);
    await upsertCanonicalBatch(indexedDB, input);
    const totals = await storedTotals(indexedDB);
    delete totals.failureLogs;
    const connection = await openCanonicalDatabase(indexedDB);
    try {
      await new Promise((resolve, reject) => {
        const transaction = connection.transaction('storageShards', 'readwrite');
        transaction.objectStore('storageShards').put(totals);
        transaction.oncomplete = resolve;
        transaction.onabort = () => reject(transaction.error);
      });
    } finally {
      connection.close();
    }
    expect((await finalizeNormalizedJsonlIngestion(indexedDB, options)).prunedFailureLogs).toBe(1);
  });

  it('preserves the explicitly unlimited detail profile for a separate historical archive', async () => {
    const indexedDB = database();
    const input = fixture(['failure', 'success']);
    await upsertCanonicalBatch(indexedDB, input);
    const result = await maintainCanonicalDatabase(indexedDB, {
      ...options, retentionWindowMs: Number.MAX_SAFE_INTEGER
    });
    expect(result.prunedFailureLogs).toBe(0);
    expect((await readCollection(indexedDB, 'runs'))[0]).toHaveProperty('failureLog');
  });

  it('reports excerpt-only changes when a direct normalized import is already cached', async () => {
    const indexedDB = database();
    const input = fixture(['failure', 'success']);
    await upsertCanonicalBatch(indexedDB, { ...input, runs: [input.runs[0]], audits: [] });
    await importRuns(indexedDB, [input.runs[0]], 'cached-failure');
    await upsertCanonicalBatch(indexedDB, { ...input, runs: [input.runs[1]], audits: [] });
    async function* unread() { yield Promise.reject(new Error('Cached input must not be parsed')); }
    const result = await ingestNormalizedJsonl(indexedDB, unread(), {
      ...options, payloadIdentity: 'cached-failure', payloadScope: 'cached-failure', expectedPhase: 'runs'
    });
    expect(result).toMatchObject({ skipped: true, updated: true, committedRecords: 0 });
    expect((await readCollection(indexedDB, 'runs'))[0]).not.toHaveProperty('failureLog');
  });
});
