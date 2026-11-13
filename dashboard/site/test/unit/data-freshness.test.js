import 'fake-indexeddb/auto';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { authoritativeDashboard } from '../authoritative-dashboard.js';
import contract from '../fixtures/data-freshness-contract.json' with { type: 'json' };
import { bindSiteCallouts } from '../../src/components/site-callout.js';
import { DataRequestWorker } from '../data-request-worker.js';
import { ingestCachedGhAwJsonl } from '../../src/data/ingest/coordinator.js';
import { DATABASE_NAME, recordTransaction } from '../../src/data/storage/indexeddb.js';
import { processDataRequest } from '../../src/data-worker.js';
import { disposeDashboard, renderDashboard } from '../../src/presenter.js';
import { resolveDashboardDocument, splitDashboardDocument } from '../../src/dashboard-chunks.js';

const now = Date.parse('2026-10-15T12:00:00Z');
const dashboard = authoritativeDashboard.dashboard;
const callout = dashboard.callouts.find((/** @type {{ id: string }} */ entry) => entry.id === contract.callout);
let controller = new AbortController();

/** @param {Record<string, import('../../src/presenter.js').LogicalSourceInput>} input */
function calloutVisibility(input) {
  return /** @type {import('../../src/data/queries/callouts.js').CalloutVisibility[]} */ (processDataRequest({
    operation: 'query-dashboard-callouts', callouts: [callout], sources: input
  }))[0].visibility;
}

/**
 * @param {unknown} timestamp
 * @returns {Record<string, import('../../src/presenter.js').LogicalSourceInput>}
 */
function sources(timestamp) {
  return {
    [contract.source]: {
      source: contract.source, rows: [{ [contract.field]: timestamp }],
      metadata: {
        'source-id': contract.source, 'source-kind': 'database-query',
        'as-of': new Date(now).toISOString(), 'retrieved-at': new Date(now).toISOString(),
        availability: /** @type {const} */ ('available'),
        completeness: /** @type {const} */ ('complete'), freshness: /** @type {const} */ ('fresh')
      }
    }
  };
}

/** @param {string} timestamp */
async function ingest(timestamp) {
  return ingestCachedGhAwJsonl(indexedDB, JSON.stringify({
    schema_version: 2, kind: 'run',
    run: {
      run_id: 1234, run_attempt: 1, organization: 'githubnext', repository: 'gh-cao',
      workflow_name: 'Dashboard', workflow_path: '.github/workflows/dashboard.md',
      status: 'completed', conclusion: 'success', created_at: timestamp, updated_at: timestamp
    }
  }), { now, payloadScope: 'gh-aw-logs-runs/example.jsonl' });
}

async function query() {
  return /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (await processDataRequest({
    operation: 'query-canonical-dashboard',
    sourceNames: [contract.source, contract.shardSource],
    context: dashboard
  }));
}

beforeEach(async () => {
  vi.stubGlobal('Worker', DataRequestWorker);
  vi.spyOn(Date, 'now').mockReturnValue(now);
  await new Promise((resolve, reject) => {
    const request = indexedDB.deleteDatabase(DATABASE_NAME);
    request.onsuccess = () => resolve(undefined);
    request.onerror = () => reject(request.error);
  });
});

afterEach(() => {
  controller.abort();
  controller = new AbortController();
  vi.useRealTimers();
  vi.restoreAllMocks();
  document.body.replaceChildren();
});

describe('declarative data freshness', () => {
  it('declares the global threshold and shard view contract', () => {
    expect(callout['navigation-page']).toBe(contract.page);
    expect(callout['visible-when']).toEqual({
      source: contract.source, field: contract.field, 'older-than-hours': contract.hours
    });
    const page = dashboard.pages.find((/** @type {{ id: string }} */ page) => page.id === contract.page);
    expect(page.views.find((/** @type {{ id: string }} */ view) => view.id === contract.shardView)?.data)
      .toMatchObject({ source: contract.shardSource, 'query-context': false });
    const { core } = splitDashboardDocument(authoritativeDashboard);
    const unloaded = resolveDashboardDocument(core, []);
    expect(unloaded.dashboard.queries?.some((entry) => entry.name === contract.source)).toBe(true);
  });

  for (const entry of contract.cases) {
    it(`uses the exact 24-hour threshold for age ${entry.ageMs}`, () => {
      expect(calloutVisibility(sources(now - entry.ageMs))).toBe(entry.visible ? 'visible' : 'hidden');
    });
  }

  it.each([null, undefined, '', 'invalid', Number.NaN])('keeps unknown timestamps unknown (%s)', (timestamp) => {
    expect(calloutVisibility(sources(timestamp))).toBe('hidden');
  });

  it('never interprets unavailable evidence as stale', () => {
    const input = sources(now - 172800000);
    input[contract.source].metadata = { ...input[contract.source].metadata, availability: 'unavailable' };
    expect(calloutVisibility(input)).toBe('unavailable');
  });

  it('accepts valid ISO instants without treating invalid calendar dates as evidence', () => {
    expect(calloutVisibility(sources('2026-10-14T12:00:00Z'))).toBe('visible');
    expect(calloutVisibility(sources('2026-02-30T12:00:00Z'))).toBe('hidden');
  });

  it('uses upstream evidence, not the current import time, through the production worker boundary', async () => {
    const timestamp = new Date(now - 86400000).toISOString();
    await ingest(timestamp);
    const result = await query();
    expect(result[contract.source].metadata.availability).not.toBe('unavailable');
    expect(result[contract.source].rows).toEqual([{ [contract.field]: Date.parse(timestamp), 'ingested-runs': 1 }]);
    expect(calloutVisibility(result)).toBe('visible');
    expect(result[contract.shardSource].rows[0]).toMatchObject({
      shard: 'gh-aw-logs-runs/example.jsonl', status: 'success',
      'ingested-at': new Date(now).toISOString()
    });
    await ingest(new Date(now - 1000).toISOString());
    const updated = await query();
    expect(calloutVisibility(updated)).toBe('hidden');
  });

  it('exposes browser and hosted shard receipts but excludes maintenance transactions', async () => {
    await recordTransaction(indexedDB, {
      id: 'hosted', kind: 'records', createdAt: new Date(now).toISOString(),
      payloadScope: 'gh-aw-logs-records/hosted.jsonl',
      committedRecords: 42, payloadHash: 'abc'
    });
    await recordTransaction(indexedDB, { id: 'maintenance', kind: 'retention', createdAt: new Date(now).toISOString() });
    const result = await query();
    expect(result[contract.shardSource].metadata.availability).not.toBe('unavailable');
    expect(result[contract.shardSource].rows).toHaveLength(1);
    expect(result[contract.shardSource].rows[0]).toMatchObject({
      id: 'hosted', shard: 'gh-aw-logs-records/hosted.jsonl', 'committed-records': 42, 'payload-hash': 'abc'
    });
    expect(result[contract.source].rows[0]?.[contract.field] ?? null).toBeNull();
  });

  it('crosses the threshold without navigation, reacts to fresh results, and stops on abort', async () => {
    vi.useFakeTimers();
    vi.setSystemTime(now);
    const initial = sources(now - 86400000 + 60_000);
    const subscribe = vi.fn(/** @param {string[]} _names @param {import('../../src/presenter.js').PageSourceLoadOptions} _options */ async (_names, _options) => initial);
    const loader = Object.assign(vi.fn(), { subscribeBackgroundSources: subscribe });
    bindSiteCallouts([callout], initial, loader, controller.signal, [{ id: 'indexing', title: 'Ingestion' }]);
    const notice = () => document.querySelector('[data-notification-id="site-callout:stale-data"]:not(.dashboard-notification-exit)');
    await Promise.resolve();
    expect(notice()).toBeNull();
    vi.spyOn(Date, 'now').mockReturnValue(now + 60_000);
    vi.advanceTimersByTime(60_000);
    await vi.waitFor(() => expect(notice()).not.toBeNull());
    expect(notice()?.querySelector('a')?.textContent).toBe('View Ingestion');
    expect(notice()?.querySelector('a')?.getAttribute('href')).toBe('#page-indexing');
    expect(subscribe).toHaveBeenCalledWith([contract.source], expect.objectContaining({ signal: controller.signal }));
    const update = /** @type {import('../../src/presenter.js').PageSourceLoadOptions} */ (subscribe.mock.calls[0][1]).onUpdate;
    update(sources(now + 60_000));
    await vi.waitFor(() => expect(notice()).toBeNull());
    controller.abort();
    update(initial);
    expect(notice()).toBeNull();
    vi.advanceTimersByTime(180);
    expect(vi.getTimerCount()).toBe(0);
  });

  it('subscribes independently of the active page and disposes with the dashboard', async () => {
    const subscribe = vi.fn(/** @param {string[]} _names @param {import('../../src/presenter.js').PageSourceLoadOptions} _options */ async (_names, _options) => sources(now - 86400000));
    const loader = Object.assign(vi.fn(async () => ({})), { subscribeBackgroundSources: subscribe });
    const root = renderDashboard({
      document: { languageVersion: '0.1.0', dashboard: {
        id: 'notice-lifecycle', title: 'Dashboard', callouts: [callout],
        pages: [{ id: 'empty', kind: 'custom', title: 'Empty', views: [] }, { id: 'indexing', kind: 'custom', title: 'Ingestion', views: [] }]
      } },
      sources: {}, loadPageSources: loader
    });
    document.body.append(root);
    await vi.waitFor(() => expect(document.querySelector('[data-notification-id="site-callout:stale-data"]')).not.toBeNull());
    const options = /** @type {import('../../src/presenter.js').PageSourceLoadOptions} */ (subscribe.mock.calls[0][1]);
    expect(options.signal.aborted).toBe(false);
    disposeDashboard(root);
    expect(options.signal.aborted).toBe(true);
  });

  it('reports a failed notice subscription instead of implying current data', async () => {
    const loader = Object.assign(vi.fn(), {
      subscribeBackgroundSources: vi.fn(async () => { throw new Error('Query unavailable'); })
    });
    bindSiteCallouts([callout], {}, loader, controller.signal, []);
    await vi.waitFor(() => expect(document.querySelector('.dashboard-notifications')?.textContent).toContain('Freshness could not be checked.'));
    expect(document.querySelector('.dashboard-notifications [role="status"]')).not.toBeNull();
  });

  it('does not start rendering or subscriptions with an already aborted lifetime', () => {
    controller.abort();
    const subscribe = vi.fn();
    const loader = Object.assign(vi.fn(), { subscribeBackgroundSources: subscribe });
    expect(bindSiteCallouts([callout], {}, loader, controller.signal, [])).toBeNull();
    expect(subscribe).not.toHaveBeenCalled();
  });
});
