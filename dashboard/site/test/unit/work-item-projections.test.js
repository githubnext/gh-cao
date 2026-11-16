import { IDBFactory } from 'fake-indexeddb';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { processDataRequest } from '../../src/data-worker.js';
import { replaceCanonicalBatch } from '../../src/data/storage/indexeddb.js';
import { TABLE_FIELDS } from '../../src/specification.js';
import fixture from '../fixtures/canonical-work-items.json' with { type: 'json' };

/** @param {typeof fixture} batch */
async function query(batch) {
  const indexedDB = new IDBFactory();
  vi.stubGlobal('indexedDB', indexedDB);
  await replaceCanonicalBatch(indexedDB, batch);
  return /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (await processDataRequest({
    operation: 'load-dashboard-query-sources',
    sourceNames: ['work-items'],
    sources: {
      'work-items': { rows: [{ 'work-item-id': 'stale-report', 'lifecycle-state': 'accepted' }] }
    }
  }));
}

afterEach(() => vi.unstubAllGlobals());

/** @param {Record<string, import('../../src/presenter.js').LogicalSourceInput>} sources */
function currentItem(sources) {
  return sources['work-items'].rows.find((row) => row['work-item-id'] === 'owner/repo:.github/workflows/worker.md');
}

describe('canonical work item payloads', () => {
  it('preserves the view contract and latest-attempt output evidence through the worker', async () => {
    const sources = await query(structuredClone(fixture));
    expect(Object.keys(sources)).toEqual(['work-items']);
    expect(sources['work-items'].metadata).toMatchObject({
      availability: 'available', 'source-kind': 'database-query'
    });
    expect(sources['work-items'].rows).toHaveLength(2);
    expect(sources['work-items'].rows).toEqual(expect.arrayContaining([
      expect.objectContaining({
        'work-item-id': 'owner/repo:.github/workflows/worker.md', run: '42',
        name: 'Worker · Current attempt', 'lifecycle-state': 'review',
        'next-actor': 'reviewer', 'waiting-on': 'reviewer decision',
        'safe-output-kind': 'issue', 'outcome-state': 'pending', 'verification-state': 'pending',
        'repository-link': { href: 'https://github.com/owner/repo', label: 'owner/repo' },
        'evidence-link': { href: 'https://github.com/owner/repo/issues/7', label: 'View evidence' }
      }),
      expect.objectContaining({
        'work-item-id': 'owner/repo:.github/workflows/idle.md', 'lifecycle-state': 'unknown',
        'outcome-state': 'unknown', 'reason-evidence-class': 'inferred', 'consequence-tier': 'high',
        'repository-link': null
      })
    ]));
    for (const row of sources['work-items'].rows) {
      expect(Object.keys(row)).toEqual(expect.arrayContaining(TABLE_FIELDS['work-items']));
    }
  });

  it.each(['CLOSED', 'UNKNOWN'])('keeps delivery pending without authoritative adoption for %s issue state', async (state) => {
    const batch = structuredClone(fixture);
    batch.issues[1].state = state;
    const sources = await query(batch);
    expect(currentItem(sources)).toMatchObject({
      'lifecycle-state': 'review', 'outcome-state': 'pending', 'verification-state': 'pending',
      'evidence-link': { href: 'https://github.com/owner/repo/issues/7' }
    });
  });

  it('breaks timestamp ties by run identity before attempt and ignores older high run IDs', async () => {
    const batch = structuredClone(fixture);
    batch.runs.push(
      { ...batch.runs[1], id: 'run:43:1', githubRunId: '43', attempt: 1, conclusion: 'failure' },
      { ...batch.runs[1], id: 'run:99:9', githubRunId: '99', attempt: 9, startedAt: '2026-09-16T11:00:00Z' }
    );
    const sources = await query(batch);
    expect(currentItem(sources)).toMatchObject({
      run: '43', 'lifecycle-state': 'blocked', 'next-actor': 'maintainer',
      'waiting-on': 'scheduled run'
    });
  });

  it.each([
    ['queued', '', 'waiting', 'scheduler'],
    ['in-progress', '', 'active', 'agent'],
    ['completed', 'action-required', 'blocked', 'maintainer']
  ])('preserves %s/%s lifecycle priority over pending outputs', async (status, conclusion, lifecycle, actor) => {
    const batch = structuredClone(fixture);
    Object.assign(batch.runs[1], { status, conclusion });
    const sources = await query(batch);
    expect(currentItem(sources)).toMatchObject({ 'lifecycle-state': lifecycle, 'next-actor': actor });
  });

  it('keeps denied admission blocked and falls back to run creation without a run start', async () => {
    const batch = structuredClone(fixture);
    Object.assign(batch.runs[1], { startedAt: null, admissionStatus: 'denied', admissionReason: 'Policy denied' });
    const sources = await query(batch);
    expect(currentItem(sources)).toMatchObject({
      'lifecycle-state': 'blocked', reason: 'Policy denied', 'waiting-on': 'scheduled run',
      'started-at': '2026-09-17T12:00:00Z'
    });
  });

  it('does not rank a run using workflow observation time when run timestamps are missing', async () => {
    const batch = structuredClone(fixture);
    Object.assign(batch.runs[1], { startedAt: null, createdAt: null });
    const sources = await query(batch);
    expect(currentItem(sources)).toMatchObject({
      name: 'Worker · First attempt', 'lifecycle-state': 'blocked',
      'started-at': '2026-09-17T11:00:00Z'
    });
  });

  it('scopes same-path workflows and their pending outputs by repository', async () => {
    const batch = structuredClone(fixture);
    batch.repositories.push({ ...batch.repositories[0], id: 'repo:owner/other', name: 'other' });
    batch.workflows.push({
      ...batch.workflows[0], id: 'workflow:other', repositoryId: 'repo:owner/other'
    });
    batch.runs.push({
      ...batch.runs[1], id: 'run:other:42:2', repositoryId: 'repo:owner/other',
      workflowId: 'workflow:other', repository: 'other'
    });
    const sources = await query(batch);
    expect(sources['work-items'].rows).toContainEqual(expect.objectContaining({
      'work-item-id': 'owner/other:.github/workflows/worker.md',
      'lifecycle-state': 'completed', 'outcome-state': 'unknown'
    }));
  });
});
