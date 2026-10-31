import nativeMeasureHistoryQuery from '../fixtures/native-measure-history-query.json' with { type: 'json' };
// @vitest-environment jsdom
import { describe, expect, it } from 'vitest';
import { processDataRequest } from '../../src/data-worker.js';
import { authoritativeDashboard } from '../authoritative-dashboard.js';
import hostedQueries from '../../src/agent/queries.generated.json' with { type: 'json' };
import operationalValueObservations from '../fixtures/campaign-operational-value-observations.json' with { type: 'json' };
import { renderMeasureHistory } from '../../src/components/measure-history.js';

const dashboard = authoritativeDashboard.dashboard;
const metadata = {
  'source-id': 'audit-fixture',
  'source-kind': 'fixture',
  'as-of': '2026-09-16T12:00:00Z',
  'retrieved-at': '2026-09-16T12:01:00Z',
  completeness: /** @type {'complete'} */ ('complete'),
  freshness: /** @type {'fresh'} */ ('fresh'),
  availability: /** @type {'available'} */ ('available')
};

describe('Audit dashboard view', () => {
  it('keeps the hosted Value evidence query in sync with the dashboard', () => {
    const names = ['value-evidence-facts'];
    for (const name of names) {
      expect(hostedQueries.find((query) => query.name === name))
        .toEqual(dashboard.queries.find((/** @type {{ name: string }} */ query) => query.name === name));
    }
  });

  it('reuses campaign-filtered Audit views for the campaign Insights facet', () => {
    const insights = dashboard.pages.find((/** @type {{ id: string }} */ candidate) => candidate.id === 'campaign-insights');
    const issues = dashboard.pages.find((/** @type {{ id: string }} */ candidate) => candidate.id === 'campaign-issues');

    expect(insights.views.map((/** @type {{ id: string }} */ view) => view.id)).toEqual([
      'campaign-insights-navigation',
      'campaign-baseline-outcomes',
      'campaign-performance-baseline',
      'campaign-audit-event-table'
    ]);
    expect(insights.views[0].data).toMatchObject({
      sources: [
        'workflows',
        'campaign-insight-tab-counts',
        'campaign-problem-tab-counts',
        'campaign-issue-tab-counts'
      ],
      arguments: [{ name: 'campaign', field: 'campaign' }]
    });
    expect(insights.views.find((/** @type {{ id: string }} */ view) => view.id === 'campaign-audit-event-table'))
      .toMatchObject({
        id: 'campaign-audit-event-table',
        data: {
          source: 'audit-event-summary-buckets',
          'route-field': 'campaign'
        }
      });
    expect(insights.views.filter((/** @type {{ mark: string }} */ view) => view.mark !== 'element')
      .map((/** @type {{ mark: string }} */ view) => view.mark))
      .toEqual(['chart', 'table', 'list']);
    expect(issues.views
      .filter((/** @type {{ data?: { source?: string } }} */ view) => view.data?.source === 'campaign-worker-issues')
      .map((/** @type {{ data: Record<string, string> }} */ view) => view.data['route-field'])).toEqual(['campaign']);
    expect(issues.views.find((/** @type {{ id: string }} */ view) => view.id === 'campaign-issue-status')).toMatchObject({
      data: { source: 'campaign-issue-status-counts', 'route-field': 'campaign' },
      encoding: { y: { field: 'issues' } }
    });
  });

  it('projects native observations, including decreasing positive AIC per successful run', () => {
    const result = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (processDataRequest({
      operation: 'execute-dashboard-queries',
      queries: [...dashboard.queries, nativeMeasureHistoryQuery],
      sourceNames: ['operational-value-plot-series'],
      sources: {
        'operational-values': {
          source: 'operational-values',
          rows: operationalValueObservations,
          metadata
        }
      }
    }));

    expect(result['operational-value-plot-series'].rows).toEqual([
      expect.objectContaining({
        campaign: 'optimization',
        'operational-value-unit': 'aic-per-run',
        series: [expect.objectContaining({
          id: 'githubnext/gh-aw',
          points: [
            expect.objectContaining({ x: '2026-09-22T20:56:21Z', y: 12.5, link: operationalValueObservations[0]['run-link'],
              metadata: expect.objectContaining({ 'adoption-at': '2026-09-15T23:30:36Z' }) }),
            expect.objectContaining({ x: '2026-09-24T20:56:21Z', y: 8.25, link: operationalValueObservations[1]['run-link'] })
          ]
        })]
      })
    ]);
    const rendered = renderMeasureHistory({
      title: 'Repository operational value',
      pageId: 'measurement-query',
      sourceNames: ['operational-value-plot-series'],
      sources: result,
      elementConfig: { 'measure-source': 'operational-value' },
      contextDetails: [],
      headingTag: 'h3'
    });
    expect([...rendered.querySelectorAll('.temporal-plot-point-tooltip a')]
      .map((link) => link.getAttribute('href'))).toEqual([
        'https://github.com/githubnext/gh-cao/actions/runs/101',
        'https://github.com/githubnext/gh-cao/actions/runs/102'
      ]);
    expect(rendered.querySelectorAll('.temporal-plot-point')).toHaveLength(2);
  });

  it('keeps irregular observations partitioned and leaves missing native values unplotted', () => {
    const observations = [
      ['first', 'a', 'm', 10, '2026-01-01T00:00:00Z'],
      ['other-first', 'b', 'm', 5, '2026-01-01T00:00:00Z'],
      ['other-change', 'b', 'm', 15, '2026-01-02T00:00:00Z'],
      ['gap', 'a', 'm', 20, '2026-01-03T00:00:00Z'],
      ['latest', 'a', 'm', 30, '2026-01-04T00:00:00Z'],
      ['missing', 'a', 'm', null, '2026-01-05T00:00:00Z'],
      ['zero-first', 'a', 'zero', -2, '2026-01-01T00:00:00Z'],
      ['zero-last', 'a', 'zero', 0, '2026-01-02T00:00:00Z']
    ].map(([id, repository, definition, value, at]) => ({
      id, campaign: 'sample', organization: 'githubnext', repository,
      'operational-value-role': 'primary',
      'operational-value-definition': definition,
      'operational-value': value,
      'observed-at': at
    }));
    const result = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (processDataRequest({
      operation: 'execute-dashboard-queries',
      queries: [...dashboard.queries, nativeMeasureHistoryQuery],
      sourceNames: ['operational-value-plot-series'],
      sources: { 'operational-values': { source: 'operational-values', rows: observations, metadata } }
    }));
    const rows = result['operational-value-plot-series'].rows;
    const points = rows.flatMap((row) => /** @type {Array<{ id: string, points: Array<{ x: string, y: number }> }>} */ (row.series)
      .flatMap((series) => series.points.map((point) => ({ ...point, color: series.id }))));
    expect(points.filter((point) => point.color === 'githubnext/a').map((point) => point.y)).toEqual([10, 20, 30, -2, 0]);
    expect(points.filter((point) => point.color === 'githubnext/b').map((point) => point.y)).toEqual([5, 15]);
    expect(points.some((point) => point.x === '2026-01-05T00:00:00Z')).toBe(false);
  });

  it('does not join same-named repositories from different organizations into one observation series', () => {
    const result = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (processDataRequest({
      operation: 'execute-dashboard-queries',
      queries: [...dashboard.queries, nativeMeasureHistoryQuery],
      sourceNames: ['operational-value-plot-series'],
      sources: {
        'operational-values': {
          source: 'operational-values',
          rows: [
            { campaign: 'sample', organization: 'one', repository: 'service', 'operational-value-definition': 'm', 'operational-value': 10, 'observed-at': '2026-01-01T00:00:00Z' },
            { campaign: 'sample', organization: 'two', repository: 'service', 'operational-value-definition': 'm', 'operational-value': 100, 'observed-at': '2026-01-01T00:00:00Z' },
            { campaign: 'sample', organization: 'one', repository: 'service', 'operational-value-definition': 'm', 'operational-value': 20, 'observed-at': '2026-01-02T00:00:00Z' },
            { campaign: 'sample', organization: 'two', repository: 'service', 'operational-value-definition': 'm', 'operational-value': 110, 'observed-at': '2026-01-02T00:00:00Z' }
          ].map((row) => ({ ...row, 'operational-value-role': 'primary' })),
          metadata
        }
      }
    }));
    const points = result['operational-value-plot-series'].rows.flatMap(
      (row) => /** @type {Array<{ id: string, points: Array<{ y: number }> }>} */ (row.series)
        .flatMap((series) => series.points.map((point) => ({ ...point, color: series.id })))
    );
    expect(points).toHaveLength(4);
    expect(points.map((point) => point.color).toSorted()).toEqual(['one/service', 'one/service', 'two/service', 'two/service']);
    expect(points.filter((point) => point.color === 'one/service').map((point) => point.y)).toEqual([10, 20]);
    expect(points.filter((point) => point.color === 'two/service').map((point) => point.y)).toEqual([100, 110]);
  });

  it('classifies interim observations independently of their native value', () => {
    const result = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (processDataRequest({
      operation: 'execute-dashboard-queries',
      queries: [...dashboard.queries, nativeMeasureHistoryQuery],
      sourceNames: ['operational-value-plot-series'],
      sources: {
        'operational-values': {
          source: 'operational-values',
          rows: [{
            campaign: 'optimization',
            'operational-value': 0,
            'maturity-status': 'interim',
            'observed-at': '2026-09-23T00:00:00Z'
          }, {
            campaign: 'optimization',
            'operational-value': 0,
            'maturity-status': 'interim',
            'observed-at': '2026-09-24T00:00:00Z'
          }, {
            campaign: 'ambient-context',
            'operational-value': 0,
            'maturity-status': 'matured',
            'observed-at': '2026-09-24T00:00:00Z'
          }, {
            campaign: 'campaign-scout',
            'operational-value': 0.25,
            'maturity-status': 'interim',
            'observed-at': '2026-09-24T00:00:00Z'
          }].map(row => ({
            ...row, organization: 'org', repository: 'repo',
            'operational-value-role': 'primary', 'operational-value-definition': 'measure',
            'operational-value-name': 'Measure', 'operational-value-unit': 'count',
            'operational-value-direction': 'decrease'
          })),
          metadata
        }
      }
    }));

    expect(result['operational-value-plot-series'].metadata.availability).toBe('available');
    const points = result['operational-value-plot-series'].rows.flatMap(row =>
      /** @type {Array<{points: Array<{metadata: Record<string, unknown>}>}>} */ (row.series)
        .flatMap(series => series.points.map(point => ({
          campaign: row.campaign, maturity: point.metadata['maturity-status']
        }))));
    expect(points).toEqual(expect.arrayContaining([
      { campaign: 'optimization', maturity: 'interim' },
      { campaign: 'ambient-context', maturity: 'matured' },
      { campaign: 'campaign-scout', maturity: 'interim' }
    ]));
  });

  it('projects only issue outcomes produced by campaign workers', () => {
    const result = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (processDataRequest({
      operation: 'execute-dashboard-queries',
      queries: [...dashboard.queries, nativeMeasureHistoryQuery],
      sourceNames: ['campaign-worker-issues'],
      sources: {
        outcomes: {
          source: 'outcomes',
          rows: [
            { organization: 'githubnext', repository: 'gh-cao', campaign: 'ambient-context', workflow: '.github/workflows/orchestrator.md', 'safe-output': 'orchestrator-issue', 'outcome-category': 'issue' },
            { organization: 'githubnext', repository: 'gh-cao', campaign: 'ambient-context', workflow: '.github/workflows/worker.md', 'safe-output': 'worker-issue', 'outcome-category': 'issue' },
            { organization: 'githubnext', repository: 'gh-cao', campaign: 'ambient-context', workflow: '.github/workflows/worker.md', 'safe-output': 'worker-pr', 'outcome-category': 'pull-request' }
          ],
          metadata
        },
        workflows: {
          source: 'workflows',
          rows: [
            { organization: 'githubnext', repository: 'gh-cao', workflow: '.github/workflows/orchestrator.md', 'workflow-role': 'orchestrator' },
            { organization: 'githubnext', repository: 'gh-cao', workflow: '.github/workflows/worker.md', 'workflow-role': 'worker' }
          ],
          metadata
        }
      }
    }));

    expect(result['campaign-worker-issues'].rows).toEqual([
      expect.objectContaining({ campaign: 'ambient-context', 'safe-output': 'worker-issue' })
    ]);
  });

  it('filters info events before grouping shared summaries by workflow', () => {
    const result = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (processDataRequest({
      operation: 'execute-dashboard-queries',
      queries: [...dashboard.queries, nativeMeasureHistoryQuery],
      sourceNames: ['audit-event-summary-buckets'],
      sources: {
        audits: {
          source: 'audits',
          rows: [
            { organization: 'githubnext', repository: 'gh-cao', workflow: '.github/workflows/audit.md', event: '1', 'event-type': 'audit.finding', 'event-status': 'high', 'event-summary': 'Repeated finding' },
            { organization: 'githubnext', repository: 'gh-cao', workflow: '.github/workflows/audit.md', event: '2', 'event-type': 'audit.recommendation', 'event-status': 'medium', 'event-summary': 'Repeated finding' },
            { organization: 'githubnext', repository: 'gh-cao', workflow: '.github/workflows/audit.md', event: '3', 'event-type': 'audit.finding', 'event-status': 'info', 'event-summary': 'Repeated finding' },
            { organization: 'githubnext', repository: 'gh-cao', workflow: '.github/workflows/review.md', event: '4', 'event-type': 'audit.finding', 'event-status': 'high', 'event-summary': 'Repeated finding' },
            { organization: 'githubnext', repository: 'gh-cao', workflow: '.github/workflows/audit.md', event: '5', 'event-type': 'mcp.tool_call', 'event-status': 'high', 'event-summary': 'Repeated finding' }
          ],
          metadata
        },
        tools: {
          source: 'tools',
          rows: [
            { organization: 'githubnext', repository: 'gh-cao', workflow: '.github/workflows/audit.md', event: '6', 'event-type': 'skill.invoked', 'event-status': 'medium', 'event-summary': 'Skill activation' }
          ],
          metadata
        },
        workflows: {
          source: 'workflows',
          rows: [
            { organization: 'githubnext', repository: 'gh-cao', workflow: '.github/workflows/audit.md', campaign: 'audit-campaign' },
            { organization: 'githubnext', repository: 'gh-cao', workflow: '.github/workflows/review.md', campaign: 'review-campaign' }
          ],
          metadata
        }
      }
    }));

    expect(result['audit-event-summary-buckets'].rows).toEqual([
      {
        campaign: 'audit-campaign',
        'event-status': 'high',
        workflow: '.github/workflows/audit.md',
        'event-summary': 'Repeated finding',
        events: 1
      },
      {
        campaign: 'review-campaign',
        'event-status': 'high',
        workflow: '.github/workflows/review.md',
        'event-summary': 'Repeated finding',
        events: 1
      },
      {
        campaign: 'audit-campaign',
        'event-status': 'medium',
        workflow: '.github/workflows/audit.md',
        'event-summary': 'Repeated finding',
        events: 1
      },
      {
        campaign: 'audit-campaign',
        'event-status': 'medium',
        workflow: '.github/workflows/audit.md',
        'event-summary': 'Skill activation',
        events: 1
      }
    ]);
  });

  it('excludes experimental operational-value plots from Insights counts', () => {
    const result = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (processDataRequest({
      operation: 'execute-dashboard-queries',
      queries: [...dashboard.queries, nativeMeasureHistoryQuery],
      sourceNames: ['campaign-insight-tab-counts'],
      sources: {
        audits: {
          source: 'audits',
          rows: [
            { organization: 'githubnext', repository: 'gh-cao', workflow: 'combined.md', event: '1', 'event-type': 'audit.finding', 'event-status': 'high', 'event-summary': 'Repeated finding' },
            { organization: 'githubnext', repository: 'gh-cao', workflow: 'combined.md', event: '2', 'event-type': 'audit.recommendation', 'event-status': 'medium', 'event-summary': 'Repeated finding' },
            { organization: 'githubnext', repository: 'gh-cao', workflow: 'audit-only.md', event: '3', 'event-type': 'audit.finding', 'event-status': 'high', 'event-summary': 'Audit-only finding' },
            ...Array.from({ length: 20 }, (_, index) => [0, 1].map((duplicate) => ({
              organization: 'githubnext',
              repository: 'gh-cao',
              workflow: 'noise.md',
              event: `noise-${index}-${duplicate}`,
              'event-type': 'audit.finding',
              'event-status': 'high',
              'event-summary': `Noise finding ${index}`
            }))).flat()
          ],
          metadata
        },
        tools: { source: 'tools', rows: [], metadata },
        workflows: {
          source: 'workflows',
          rows: [
            { organization: 'githubnext', repository: 'gh-cao', workflow: 'combined.md', campaign: 'combined' },
            { organization: 'githubnext', repository: 'gh-cao', workflow: 'audit-only.md', campaign: 'audit-only' },
            { organization: 'githubnext', repository: 'gh-cao', workflow: 'noise.md', campaign: 'noise' },
            { organization: 'githubnext', repository: 'gh-cao', workflow: 'empty.md', campaign: 'empty' }
          ],
          metadata
        },
        'operational-values': {
          source: 'operational-values',
          rows: [{
            campaign: 'combined',
            organization: 'githubnext',
            repository: 'gh-cao',
            'operational-value': 0.5,
            'operational-value-definition': 'combined.value',
            'operational-value-role': 'primary',
            'maturity-status': 'matured',
            'observed-at': '2026-09-16T10:00:00Z'
          }, {
            campaign: 'combined',
            organization: 'githubnext',
            repository: 'gh-cao',
            'operational-value': 1,
            'operational-value-definition': 'combined.value',
            'operational-value-role': 'primary',
            'maturity-status': 'matured',
            'observed-at': '2026-09-17T10:00:00Z'
          }],
          metadata
        }
      }
    }));

    expect(result['campaign-insight-tab-counts'].rows).toHaveLength(3);
    expect(result['campaign-insight-tab-counts'].rows).toEqual(expect.arrayContaining([
      { campaign: 'combined', items: 1 },
      { campaign: 'audit-only', items: 1 },
      { campaign: 'noise', items: 1 }
    ]));
  });
});
