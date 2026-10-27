// @vitest-environment node
import { readFileSync } from 'node:fs';
import { IDBFactory } from 'fake-indexeddb';
import { describe, expect, it } from 'vitest';
import { adaptCachedGhAwJsonl } from '../../src/data/adapters/gh-aw-logs.js';
import { ingestCachedGhAwJsonl } from '../../src/data/ingest/coordinator.js';
import { mergeWorkflowValueCollection, validateWorkflowValueCollection } from '../../src/data/model/workflow-value-collection.js';
import { maintainCanonicalDatabase, readCollection } from '../../src/data/storage/indexeddb.js';

const source = readFileSync(new URL('../fixtures/workflow-value-collection.jsonl', import.meta.url), 'utf8');
const fixture = JSON.parse(source);
const orders = [[0, 1, 2], [0, 2, 1], [1, 0, 2], [1, 2, 0], [2, 0, 1], [2, 1, 0]];

describe('canonical workflow Value collection facts', () => {
  it('fails closed on contradictory states, clocks, identities and measurements disguised as health', () => {
    const record = fixture.workflow_value_collection;
    expect(validateWorkflowValueCollection(record)).toEqual(record);
    expect(validateWorkflowValueCollection({ ...record, state: 'failed' }))
      .toEqual({ ...record, state: 'failed' });
    for (const changes of [
      { registration: 'disabled' }, { state: 'available' }, { reason: 'invented' },
      { lastAttemptAt: '2026-10-24T12:00:00Z' }, { lastAttemptOutcome: 'deferred' },
      { id: 'wrong' }, { collector: '../escape.mjs' }, { value: 0 }, { capturedAt: record.observedAt },
      { workflowId: 'workflow:other' }, { schemaVersion: '1' },
      { observedAt: '2026-10-23T14:00:00.000001Z' }, { observedAt: '2026-10-23T14:00:00+00:00' },
      { lastAttemptAt: '2026-10-22T12:00:00.000001Z' }
    ]) expect(() => validateWorkflowValueCollection({ ...record, ...changes })).toThrow();
    expect(adaptCachedGhAwJsonl(source).observations.map(item => item.kind))
      .toEqual(['repository', 'workflow', 'workflow-value-collection']);
  });

  it('preserves independent attempt clocks, rejects conflicting decisions, and retains structural parents without runs', async () => {
    const database = new IDBFactory();
    const now = Date.parse('2026-10-25T00:00:00Z');
    await ingestCachedGhAwJsonl(database, source, { now, payloadScope: 'health' });
    const older = structuredClone(fixture);
    older.workflow_value_collection.observedAt = '2026-10-22T13:00:00Z';
    await ingestCachedGhAwJsonl(database, JSON.stringify(older), { now, payloadScope: 'older' });
    const [retained] = await readCollection(database, 'workflowValueCollections');
    expect(retained.observedAt).toBe(fixture.workflow_value_collection.observedAt);
    const conflicting = structuredClone(fixture);
    conflicting.workflow_value_collection.policyDigest = 'c'.repeat(64);
    await expect(ingestCachedGhAwJsonl(database, JSON.stringify(conflicting), { now, payloadScope: 'conflict' })).rejects.toThrow();
    const regressed = structuredClone(fixture);
    regressed.workflow_value_collection.observedAt = '2026-10-24T00:00:00Z';
    regressed.workflow_value_collection.lastAttemptAt = '2026-10-21T12:00:00Z';
    await ingestCachedGhAwJsonl(database, JSON.stringify(regressed), { now, payloadScope: 'regression' });
    expect((await readCollection(database, 'workflowValueCollections'))[0].lastAttemptAt)
      .toBe(fixture.workflow_value_collection.lastAttemptAt);
    const unknown = structuredClone(fixture);
    unknown.workflow_value_collection.observedAt = '2026-10-24T00:00:00Z';
    delete unknown.workflow_value_collection.lastAttemptAt;
    delete unknown.workflow_value_collection.lastAttemptOutcome;
    await ingestCachedGhAwJsonl(database, JSON.stringify(unknown), { now, payloadScope: 'unknown-attempt' });
    const [preserved] = await readCollection(database, 'workflowValueCollections');
    expect(preserved.observedAt).toBe(new Date(unknown.workflow_value_collection.observedAt).toISOString());
    expect(preserved.lastAttemptAt).toBe(fixture.workflow_value_collection.lastAttemptAt);
    expect(preserved.lastAttemptOutcome).toBe(fixture.workflow_value_collection.lastAttemptOutcome);
    await maintainCanonicalDatabase(database, {
      now: now + 365 * 86400000, retentionWindowMs: 86400000, maxDatabaseBytes: Number.MAX_SAFE_INTEGER,
    });
    expect(await readCollection(database, 'workflowValueCollections')).toHaveLength(1);
    expect(await readCollection(database, 'repositories')).toHaveLength(1);
    expect(await readCollection(database, 'workflows')).toHaveLength(1);
    expect(await readCollection(database, 'runs')).toEqual([]);
  });

  it('converges across ordering, duplicates, batch grouping and cache-loss decisions', async () => {
    const older = fixture.workflow_value_collection;
    const attempted = { ...older, observedAt: '2026-10-23T15:00:00.000Z',
      lastAttemptAt: '2026-10-23T14:30:00.000Z', lastAttemptOutcome: 'available' };
    const deferred = { ...older, observedAt: '2026-10-24T14:00:00.000Z' };
    delete deferred.lastAttemptAt;
    delete deferred.lastAttemptOutcome;
    const records = [older, attempted, deferred];
    const expected = { ...deferred, lastAttemptAt: attempted.lastAttemptAt, lastAttemptOutcome: attempted.lastAttemptOutcome };
    const envelope = (/** @type {Record<string, any>} */ record) => JSON.stringify({ ...fixture, workflow_value_collection: record });
    for (const order of orders) {
      const [a, b, c] = order.map(index => records[index]);
      const forward = mergeWorkflowValueCollection(mergeWorkflowValueCollection(a, b), c);
      expect(forward).toEqual(expected);
      expect(mergeWorkflowValueCollection(a, mergeWorkflowValueCollection(b, c))).toEqual(expected);
      expect(mergeWorkflowValueCollection(forward, forward)).toEqual(expected);
      const database = new IDBFactory();
      for (const [index, record] of [...order, ...order].map(index => records[index]).entries()) {
        await ingestCachedGhAwJsonl(database, envelope(record), {
          now: Date.parse('2026-10-25T00:00:00Z'), payloadScope: `permutation-${index}`,
        });
      }
      expect((await readCollection(database, 'workflowValueCollections'))[0]).toMatchObject(expected);
      const grouped = new IDBFactory();
      await ingestCachedGhAwJsonl(grouped, order.map(index => envelope(records[index])).join('\n'), {
        now: Date.parse('2026-10-25T00:00:00Z'), payloadScope: 'grouped',
      });
      expect((await readCollection(grouped, 'workflowValueCollections'))[0]).toMatchObject(expected);
    }
  });

  it('rejects contradictory decision or attempt revisions in either order and normalizes equivalent clocks', () => {
    const original = fixture.workflow_value_collection;
    const conflict = { ...original, observedAt: '2026-10-24T14:00:00.000Z', lastAttemptOutcome: 'available' };
    for (const [left, right] of [[original, conflict], [conflict, original]]) {
      expect(() => mergeWorkflowValueCollection(left, right)).toThrow(/same attempt/);
    }
    const equivalent = { ...original, observedAt: '2026-10-23T14:00:00Z', lastAttemptAt: '2026-10-22T12:00:00Z' };
    expect(mergeWorkflowValueCollection(original, equivalent)).toEqual(original);
    expect(mergeWorkflowValueCollection(equivalent, original)).toEqual(original);
    const decision = { ...original, policyDigest: 'c'.repeat(64) };
    expect(() => mergeWorkflowValueCollection(original, decision)).toThrow(/decisions/);
    expect(() => mergeWorkflowValueCollection(decision, original)).toThrow(/decisions/);
  });
});
