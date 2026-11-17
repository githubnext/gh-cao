import { IDBFactory } from 'fake-indexeddb';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { processDataRequest } from '../../src/data-worker.js';
import { replaceCanonicalBatch } from '../../src/data/storage/indexeddb.js';
import { compileDashboardViewPayloadQueries } from '../../src/data/queries/view-payload-compiler.js';
import { authoritativeDashboard } from '../authoritative-dashboard.js';
import { opsEvidence } from '../fixtures/ops-evidence.js';
import contract from '../fixtures/ops-view-contract.json' with { type: 'json' };

const dashboard = authoritativeDashboard.dashboard;
const page = dashboard.pages.find((/** @type {{ id: string }} */ page) => page.id === contract.page);

/** @param {string} viewId */
function payload(viewId) {
  return compileDashboardViewPayloadQueries(page, page.id, {
    viewId, sourceNames: [viewId], queries: dashboard.queries
  });
}

/** @param {string} viewId */
async function canonicalView(viewId) {
  const compiled = payload(viewId);
  const result = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (
    await processDataRequest({
      operation: 'query-canonical-dashboard', sourceNames: compiled.aliases,
      context: { queries: compiled.queries, pages: dashboard.pages }
    })
  );
  return result[compiled.aliases[0]];
}

afterEach(() => vi.unstubAllGlobals());

describe('Ops retained-evidence contract', () => {
  it('uses shared page subscriptions and exposes no mutation actions', () => {
    expect(dashboard.navigation.find((/** @type {{ label: string }} */ section) => section.label === 'Experimental').pages).toContain(page.id);
    expect(page).toMatchObject({ experimental: true, 'query-binding': 'page' });
    expect(page.views.map((/** @type {{ id: string }} */ view) => view.id)).toEqual(contract.views);
    for (const view of page.views) {
      expect(view.actions).toBeUndefined();
      expect(view.mark).toBe('table');
      expect(view['lazy-list']).toBe(true);
    }
  });

  it('resolves latest-workflow attention, native attempts, coverage and adoption through canonical worker queries', async () => {
    const indexedDB = new IDBFactory();
    vi.stubGlobal('indexedDB', indexedDB);
    await replaceCanonicalBatch(indexedDB, opsEvidence());
    const attention = await canonicalView('ops-attention');
    expect(attention.metadata.availability).toBe('available');
    expect(attention.rows.map(row => row['lifecycle-state'])).toEqual(contract['attention-states']);
    expect(attention.rows.at(-1)?.['ops-reason']).toContain('unknown');
    expect((await canonicalView('ops-in-flight')).rows.map(row => row.run)).toEqual(contract['in-flight-runs']);
    const repositories = (await canonicalView('ops-repositories')).rows;
    expect(repositories).toContainEqual(expect.objectContaining(contract.repository));
    expect(repositories.find(row => row['ops-repository'] === 'octodemo/ops-synthetic')?.['repository-dashboard-link'])
      .toMatchObject({ 'dashboard-href': '#page-repository-detail?repository=octodemo%2Fops-synthetic' });
    expect(repositories.find(row => row['ops-repository'] === 'octodemo/no-evidence')).toMatchObject({
      'ops-coverage': 'No retained run evidence', 'observed-workflows': null, blocked: null
    });
    const outputs = (await canonicalView('ops-recent-outputs')).rows;
    expect(outputs).toHaveLength(2);
    expect(outputs.find(row => row['safe-output'] === 'output:pending')).toMatchObject({
      'delivery-state': 'delivered', 'adoption-state': 'pending',
      'ops-output-href': 'https://github.com/octodemo/target/pull/42',
      'external-link': { href: 'https://github.com/octodemo/target/pull/42' }
    });
    expect(outputs.find(row => row['safe-output'] === 'output:fallback')).toMatchObject({
      'adoption-state': 'accepted', 'adoption-signal': 'delivery_fallback',
      'adoption-source': 'adoption-registry', 'survival-state': 'unknown', 'external-link': null
    });
  });

  it.each(contract['active-run-statuses'])('includes native and retained active run status %s through the worker', async status => {
    const indexedDB = new IDBFactory();
    vi.stubGlobal('indexedDB', indexedDB);
    const batch = opsEvidence();
    batch.runs[2].status = status;
    await replaceCanonicalBatch(indexedDB, batch);
    const flight = (await canonicalView('ops-in-flight')).rows;
    expect(flight.map(row => row.run)).toEqual(contract['in-flight-runs']);
    expect(flight.find(row => row.run === '102')?.['run-status']).toBe(status);
  });

  it('keeps same-path workflows in different repositories separate and replaces obsolete blockers reactively', async () => {
    const indexedDB = new IDBFactory();
    vi.stubGlobal('indexedDB', indexedDB);
    const batch = opsEvidence();
    batch.workflows.push({
      ...batch.workflows[0], id: 'workflow:other', repositoryId: 'repo:octodemo/no-evidence'
    });
    await replaceCanonicalBatch(indexedDB, batch);
    expect((await canonicalView('ops-attention')).rows).toHaveLength(4);
    batch.runs[0].conclusion = 'success';
    await replaceCanonicalBatch(indexedDB, batch);
    const attention = await canonicalView('ops-attention');
    expect(attention.rows).toHaveLength(3);
    expect(attention.rows.some(row => row['lifecycle-state'] === 'blocked')).toBe(false);
    expect((await canonicalView('ops-repositories')).rows.find(row => row['ops-repository'] === 'octodemo/no-evidence'))
      .toMatchObject({ blocked: 0, unknown: 1, 'workflows-with-runs': 0 });
  });

  it.each(contract.views)('preserves empty, partial, stale and unavailable %s instead of manufacturing health', async viewId => {
    vi.stubGlobal('indexedDB', new IDBFactory());
    const empty = await canonicalView(viewId);
    expect(empty.rows).toEqual([]);
    const compiled = payload(viewId);
    const inputs = ['repositories', 'work-items', 'runs', 'outcomes'];
    for (const availability of ['available', 'unavailable']) {
      const source = {
        rows: [], metadata: {
          availability, completeness: 'partial', freshness: 'stale',
          'as-of': '2026-11-16T12:00:00Z'
        }
      };
      const results = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (
        processDataRequest({
          operation: 'execute-dashboard-queries', queries: compiled.queries, sourceNames: compiled.aliases,
          sources: Object.fromEntries(inputs.map(name => [name, source]))
        })
      );
      expect(results[compiled.aliases[0]].metadata).toMatchObject({
        ...(availability === 'unavailable'
          ? { availability, completeness: 'unknown', freshness: 'unknown' }
          : { completeness: 'partial', freshness: 'stale' })
      });
      expect(results[compiled.aliases[0]].rows).toEqual([]);
    }
  });
});
