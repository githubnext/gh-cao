import { describe, expect, it } from 'vitest';
import dashboard from '../../dashboard.json' with { type: 'json' };
import fragment from '../../dashboard-fragments/workflow-value.json' with { type: 'json' };
import contract from '../fixtures/workflow-value-view-contract.json' with { type: 'json' };
import { executeDashboardQueries } from '../../src/data/queries/declarative.js';

/** @typedef {{ id: string, data?: { source?: string }, list?: { card?: string, drill?: { field?: string } }, "lazy-list"?: boolean }} View */
/** @typedef {{ id: string, route?: unknown, sections?: unknown, views?: View[] }} Page */
/** @typedef {{ id: string, "icon-field"?: string, title?: { field?: string }, status?: { field?: string } }} CardTemplate */

/** @type {import('../../src/presenter.js').SourceMetadata} */
const metadata = {
  'source-id': 'workflow-value-view-contract',
  'source-kind': 'fixture',
  'as-of': '2026-11-15T00:00:00Z',
  'retrieved-at': '2026-11-15T00:00:00Z',
  completeness: 'complete',
  freshness: 'unknown',
  availability: 'available'
};

/**
 * @param {string} name
 * @param {Record<string, unknown>[]} rows
 * @returns {import('../../src/presenter.js').LogicalSourceInput}
 */
const source = (name, rows) => ({ source: name, rows, metadata: { ...metadata, 'source-id': name } });

describe('unified Value view contract', () => {
  it('declares one top-level Value page without entity or review-state tabs', () => {
    const pages = new Map(fragment.pages.map(raw => {
      const page = /** @type {Page} */ (raw);
      return [page.id, page];
    }));
    const page = pages.get(contract.overviewPage);
    expect(page?.route).toBeUndefined();
    expect(page?.sections).toBeUndefined();
    expect(page?.views).toHaveLength(1);
    const view = page?.views?.[0];
    expect(view).toMatchObject({
      id: contract.overviewView,
      data: { source: contract.overviewQuery },
      list: { card: contract.overviewCard, drill: { field: 'value-href' } }
    });
    expect(view?.['lazy-list']).toBeUndefined();
    for (const removed of contract.removedPages) expect(pages.has(removed)).toBe(false);

    const cards = /** @type {CardTemplate[]} */ (dashboard.dashboard['card-templates']);
    const card = cards.find(template => template.id === contract.overviewCard);
    expect(card).toMatchObject({
      'icon-field': 'value-icon',
      title: { field: 'value-title' },
      status: { field: 'value-status' }
    });
  });

  it('rolls campaign Value up only from workers and keeps standalone workflows separate', () => {
    const names = new Set([
      'value-campaign-complete-topology',
      'value-campaign-complete-deployments',
      'value-campaign-coverage-deployments',
      'value-campaign-totals',
      'value-campaign-overview',
      'value-standalone-entries',
      'value-overview'
    ]);
    const queries = fragment.queries.filter(query => names.has(query.name));
    const sources = {
      workflows: source('workflows', [
        {
          organization: 'octo', repository: 'control', workflow: '.github/workflows/alpha-orchestrator.md',
          'workflow-role': 'orchestrator', campaign: 'alpha', 'campaign-name': 'Alpha', 'campaign-icon': 'goal'
        },
        {
          organization: 'octo', repository: 'one', workflow: '.github/workflows/alpha-worker.md',
          'workflow-role': 'worker', campaign: 'alpha', 'campaign-name': 'Alpha', 'campaign-icon': 'goal'
        },
        {
          organization: 'octo', repository: 'two', workflow: '.github/workflows/alpha-worker.md',
          'workflow-role': 'worker', campaign: 'alpha', 'campaign-name': 'Alpha', 'campaign-icon': 'goal'
        }
      ]),
      'value-collection-health': source('value-collection-health', [{
        'workflow-route': 'octo/one:.github/workflows/alpha-worker.md',
        registration: 'enabled',
        'collection-state': 'available'
      }]),
      'value-headlines': source('value-headlines', [{
        'workflow-route': 'octo/one:.github/workflows/alpha-worker.md',
        'headline-id': 'worker-one',
        'headline-trend-movement': 'Better',
        'headline-unavailable': false
      }, {
        'workflow-route': 'octo/two:.github/workflows/alpha-worker.md',
        'headline-id': 'worker-two',
        'headline-trend-movement': 'Measurement unavailable',
        'headline-unavailable': true
      }]),
      'value-workflow-overview': source('value-workflow-overview', [
        {
          'workflow-route': 'octo/one:.github/workflows/alpha-worker.md',
          'workflow-role': 'worker',
          'overview-title': 'octo/one · Alpha worker'
        },
        {
          'workflow-route': 'octo/solo:.github/workflows/value.md',
          'workflow-role': 'standalone',
          'repository-coordinate': 'octo/solo',
          'overview-title': 'octo/solo · Value',
          'trend-movement': 'Measurement unavailable',
          'overview-state': 'unknown',
          'overview-evidence': 'Latest assessment unavailable',
          'trend-label': 'Not measured',
          'attention-reason': 'No retained Value result',
          'overview-href': '#page-workflow-value?workflow=octo%2Fsolo',
          'overview-captured': null,
          'overview-cutoff': null
        }
      ])
    };

    const rows = executeDashboardQueries(queries, sources, ['value-overview'])['value-overview'].rows;
    expect(rows.map(row => row['value-kind'])).toEqual(contract.overviewKinds);

    const campaign = rows[0];
    expect(campaign).toMatchObject({
      campaign: 'alpha',
      'value-deployments': 2,
      'value-repositories': 2,
      'current-evidence-deployments': 1,
      'missing-evidence-deployments': 1,
      'unknown-deployments': 1,
      'value-context': '2 workers · 2 repositories',
      'value-evidence': '1 current results · 1 unavailable',
      'value-href': '#page-campaign-value?campaign=alpha'
    });

    const standalone = rows[1];
    expect(standalone).toMatchObject({
      'workflow-role': 'standalone',
      'value-title': 'octo/solo · Value',
      'value-evidence': 'Latest assessment unavailable',
      'value-href': '#page-workflow-value?workflow=octo%2Fsolo'
    });
    expect(rows.some(row => row['workflow-role'] === 'worker' || row['workflow-role'] === 'orchestrator')).toBe(false);
  });
});
