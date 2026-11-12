import 'fake-indexeddb/auto';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import contract from '../fixtures/overview-ingestion-status-contract.json' with { type: 'json' };
import { authoritativeDashboard } from '../authoritative-dashboard.js';
import { dashboardDataStatus } from '../../src/dashboard-data-status.js';
import { renderDashboardCurrentStatus, renderDashboardIngestionStatus } from '../../src/components/refresh-error.js';
import { updateRateLimitNotification } from '../../src/rate-limit-notification.js';
import { DATABASE_NAME, recordTransaction } from '../../src/data/storage/indexeddb.js';
import { processDataRequest } from '../../src/data-worker.js';
import { compileDashboardViewPayloadQueries } from '../../src/data/queries/view-payload-compiler.js';

const snapshot = { createdAt: '2026-09-28T12:00:00.000Z' };
let controller = new AbortController();

afterEach(() => {
  controller.abort();
  controller = new AbortController();
  dashboardDataStatus.set({ phase: 'unknown', snapshot: null });
  updateRateLimitNotification('/api/v1/query', 200);
  document.body.replaceChildren();
});

describe('Local ingestion progress contract', () => {
  const dashboard = authoritativeDashboard.dashboard;
  const page = dashboard.pages.find((/** @type {{ id: string }} */ page) => page.id === 'overview');
  const view = dashboard.views.find((/** @type {{ id: string }} */ view) => view.id === contract.local.view);
  const queryContext = {
    filters: { repository: ['unrelated'] },
    timeWindow: { start: '2099-01-01T00:00:00Z', end: '2099-01-02T00:00:00Z' }
  };

  beforeEach(async () => {
    await new Promise((resolve, reject) => {
      const request = indexedDB.deleteDatabase(DATABASE_NAME);
      request.onsuccess = () => resolve(undefined);
      request.onerror = () => reject(request.error);
    });
  });

  async function query() {
    const compiled = compileDashboardViewPayloadQueries(page, 'overview', {
      queries: dashboard.queries, views: dashboard.views,
      viewId: view.id, sourceNames: [contract.local.source], queryContext
    });
    expect(compiled.aliases).toEqual([]);
    const results = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (await processDataRequest({
      operation: 'query-canonical-dashboard', sourceNames: [contract.local.source],
      context: { ...dashboard, queries: [...dashboard.queries, ...compiled.queries] }
    }));
    return results[contract.local.source];
  }

  it('projects persisted progress through the canonical worker independently of page filters', async () => {
    expect(page.views).toContain(contract.local.view);
    expect(view).toMatchObject({
      mark: 'table', requires: { backend: 'hosted', 'on-unavailable': 'hide' },
      data: { source: contract.local.source, 'query-context': false }
    });
    await recordTransaction(indexedDB, contract.local.transaction);
    const result = await query();
    expect(result.metadata.availability).toBe('available');
    expect(result.rows).toEqual([{
      status: 'paused', message: contract.local.transaction.ingestionMessage, repository: 'octo/api',
      'scanned-hours': 48, 'target-hours': 168, 'api-remaining': 999, 'api-reserve': 1000,
      'api-reset-at': '2026-11-11T13:00:00Z', 'next-attempt-at': '2026-11-11T13:00:00Z',
      'observed-at': '2026-11-11T12:00:00Z'
    }]);
    await recordTransaction(indexedDB, { ...contract.local.transaction, ingestionStatus: 'collecting', scannedHours: 72 });
    expect((await query()).rows[0]).toMatchObject({ status: 'collecting', 'scanned-hours': 72 });
  });

  it('does not fabricate status from unrelated or missing ingestion receipts', async () => {
    expect((await query()).rows).toEqual([]);
    await recordTransaction(indexedDB, {
      id: 'other', kind: 'ingest-jsonl', createdAt: contract.local.transaction.createdAt
    });
    expect((await query()).rows).toEqual([]);
  });
});

describe('Overview ingestion status', () => {
  it('leaves ingestion status to the top-right indicator without adding data queries', () => {
    const page = authoritativeDashboard.dashboard.pages.find((/** @type {{ id: string }} */ page) => page.id === 'overview');
    const header = page.views.find((/** @type {{ id: string }} */ view) => view.id === contract.view);
    expect(header.element).toBe('factory-header');
    expect(header.config[contract.config] === true).toBe(contract.enabled);
    expect(header.data.sources).toEqual(['overview-header-presentation', 'overview-rhythm']);
  });

  for (const entry of contract.cases) {
    it(`shows ${entry.value} for the ${entry.phase} lifecycle state`, () => {
      dashboardDataStatus.set({
        phase: /** @type {ReturnType<typeof dashboardDataStatus.get>['phase']} */ (entry.phase),
        snapshot
      });
      const status = renderDashboardIngestionStatus(controller.signal);
      expect(status.getAttribute('role')).toBe('status');
      expect(status.getAttribute('aria-live')).toBe('polite');
      expect(status.querySelector('span')?.textContent).toBe('Data ingestion');
      expect(status.querySelector('strong')?.textContent).toBe(entry.value);
      expect(status.querySelector(`.octicon-${entry.icon}`)).not.toBeNull();
      expect(status.dataset.tone).toBe(entry.tone);
    });
  }

  it('updates the same owned indicator across success, failure, and retry, then stops on abort', () => {
    const status = renderDashboardIngestionStatus(controller.signal);
    document.body.append(status);
    expect(status.textContent).not.toContain('Current');
    dashboardDataStatus.set({ phase: 'ready', snapshot });
    expect(status.textContent).toContain('Current');
    expect(status.querySelector('p')?.textContent).toContain('current as of');
    dashboardDataStatus.set({ phase: 'stale', snapshot });
    expect(status.textContent).toContain('Refresh failed');
    expect(status.querySelector('p')?.textContent).toContain('last complete snapshot');
    expect(status.querySelector('.octicon-check-circle-fill')).toBeNull();
    dashboardDataStatus.set({ phase: 'cached', snapshot });
    expect(status.textContent).toContain('Refreshing');
    controller.abort();
    dashboardDataStatus.set({ phase: 'ready', snapshot });
    expect(status.querySelector('strong')?.textContent).toBe('Refreshing');
  });

  it('does not invent a timestamp or completed snapshot for a failed first import or hosted success', () => {
    dashboardDataStatus.set({ phase: 'stale', snapshot: null });
    const status = renderDashboardIngestionStatus(controller.signal);
    expect(status.textContent).toContain('A complete dashboard snapshot is not available yet.');
    expect(status.textContent).not.toContain('last complete snapshot');
    dashboardDataStatus.set({ phase: 'ready', snapshot: null });
    expect(status.textContent).toContain('Dashboard data loaded successfully.');
    expect(status.textContent).not.toContain('as of');
  });

  it('shares icons and rate-limit feedback with the top-right indicator and recovers reactively', () => {
    dashboardDataStatus.set({ phase: 'ready', snapshot });
    const status = renderDashboardIngestionStatus(controller.signal);
    const headerStatus = renderDashboardCurrentStatus(snapshot);
    document.body.append(status, headerStatus);
    expect(status.querySelector('.octicon-check-circle-fill')).not.toBeNull();
    expect(headerStatus.querySelector('.octicon-check-circle-fill')).not.toBeNull();
    updateRateLimitNotification('/api/v1/query', 429);
    expect(status.querySelector('strong')?.textContent).toBe('Rate limited');
    expect(status.querySelector('.octicon-alert')).not.toBeNull();
    expect(headerStatus.querySelector('.octicon-stop')).not.toBeNull();
    updateRateLimitNotification('/api/v1/query', 200);
    expect(status.querySelector('strong')?.textContent).toBe('Current');
  });
});
