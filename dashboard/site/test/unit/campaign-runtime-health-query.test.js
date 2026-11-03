import { expect, it } from 'vitest';
import { executeDashboardQueries } from '../../src/data/queries/declarative.js';
import { authoritativeDashboard } from '../authoritative-dashboard.js';

const dashboard = authoritativeDashboard.dashboard;
const problemGroupsQuery = dashboard.queries.find(
  (/** @type {{ name?: string }} */ candidate) => candidate.name === 'campaign-problem-error-groups'
);
const campaignRunsQuery = dashboard.queries.find(
  (/** @type {{ name?: string }} */ candidate) => candidate.name === 'campaign-runs'
);
const targetErrorQueries = dashboard.queries.filter(
  (/** @type {{ name?: string }} */ candidate) => [
    'campaign-problem-latest-target-runs',
    'campaign-problem-error-groups',
    'campaign-current-problem-error-groups'
  ].includes(candidate.name ?? '')
);
const campaignDiagnosisQueries = dashboard.queries.filter(
  (/** @type {{ name?: string }} */ candidate) => [
    'run-incomplete-outcomes',
    'campaign-runs'
  ].includes(candidate.name ?? '')
);
const overviewInventoryQueries = dashboard.queries.filter(
  (/** @type {{ name?: string }} */ candidate) => [
    'overview-registered-repository-summary'
  ].includes(candidate.name ?? '')
);

/** @type {import('../../src/presenter.js').SourceMetadata} */
const metadata = {
  'source-id': 'fixture',
  'source-kind': 'fixture',
  'as-of': '2026-09-22T12:00:00Z',
  'retrieved-at': '2026-09-22T12:00:00Z',
  availability: 'available',
  completeness: 'complete',
  freshness: 'fresh'
};

it('counts logical repository identities without internal database ids', () => {
  const sources = executeDashboardQueries(overviewInventoryQueries, {
    repositories: {
      source: 'repositories',
      metadata,
      rows: [
        { organization: 'octo', repository: 'api', 'repository-coordinate': 'octo/api' },
        { organization: 'octo', repository: 'web', 'repository-coordinate': 'octo/web' }
      ]
    }
  }, [
    'overview-registered-repository-summary'
  ]);

  expect(sources['overview-registered-repository-summary'].rows).toEqual([
    { 'registered-repositories': 2 }
  ]);
});

it('keeps campaign error groups scoped to their target repository', () => {
  expect(problemGroupsQuery).toMatchObject({
    aggregate: {
      by: expect.arrayContaining(['runtime-repository', 'target-repository', 'error-signature'])
    }
  });
  expect(campaignRunsQuery).toMatchObject({
    select: expect.arrayContaining([
      { field: 'target-repository' }
    ])
  });
});

it('keeps only the newest error signature for each target repository', () => {
  const rows = [
    {
      campaign: 'dependabot',
      workflow: '.github/workflows/dependabot-update-planner.md',
      'workflow-name': 'Dependabot / Update Planner',
      'runtime-repository': 'githubnext/gh-cao',
      'target-repository': 'octo-org/service-api',
      'rollout-mode': 'live',
      'failure-count': 3,
      'problem-kind': 'failure',
      'error-signature': 'agent_logic',
      'error-signature-label': 'Agent Logic',
      run: '100'
    },
    {
      campaign: 'dependabot',
      workflow: '.github/workflows/dependabot-update-planner.md',
      'workflow-name': 'Dependabot / Update Planner',
      'runtime-repository': 'githubnext/gh-cao',
      'target-repository': 'octo-org/service-api',
      'rollout-mode': 'live',
      'failure-count': 3,
      'problem-kind': 'failure',
      'error-signature': 'driver_exit',
      'error-signature-label': 'Driver Exit',
      run: '101'
    },
    {
      campaign: 'dependabot',
      workflow: '.github/workflows/dependabot-update-planner.md',
      'workflow-name': 'Dependabot / Update Planner',
      'runtime-repository': 'githubnext/gh-cao',
      'target-repository': 'octo-org/web-app',
      'rollout-mode': 'review',
      'failure-count': 1,
      'problem-kind': 'failure',
      'error-signature': 'agent_logic',
      'error-signature-label': 'Agent Logic',
      run: '102'
    }
  ];
  const sources = executeDashboardQueries(
    targetErrorQueries,
    {
      'campaign-problem-run-evidence': {
        source: 'campaign-problem-run-evidence',
        metadata,
        rows
      }
    },
    ['campaign-current-problem-error-groups']
  );

  expect(sources['campaign-current-problem-error-groups'].rows).toEqual([
    expect.objectContaining({
      'target-repository': 'octo-org/service-api',
      'error-signature': 'driver_exit',
      'latest-run': 101,
      'rollout-mode': 'live'
    }),
    expect.objectContaining({
      'target-repository': 'octo-org/web-app',
      'error-signature': 'agent_logic',
      'latest-run': 102,
      'rollout-mode': 'review'
    })
  ]);
});

it('diagnoses report-incomplete outcomes instead of presenting driver exit as the problem', () => {
  const sources = executeDashboardQueries(
    campaignDiagnosisQueries,
    {
      runs: {
        source: 'runs',
        metadata,
        rows: [{
          organization: 'githubnext',
          repository: 'gh-cao',
          workflow: '.github/workflows/dependabot-update-planner.lock.yml',
          run: '35754799033',
          'run-attempt': 1,
          'run-status': 'completed',
          'run-conclusion': 'failure',
          'failure-kind': 'driver_exit',
          'terminal-outcome': 'report_incomplete',
          'terminal-outcome-detail': 'Dependabot alert evidence was unavailable for the target repository.'
        }]
      },
      workflows: {
        source: 'workflows',
        metadata,
        rows: [{
          organization: 'githubnext',
          repository: 'gh-cao',
          workflow: '.github/workflows/dependabot-update-planner.lock.yml',
          campaign: 'dependabot',
          'campaign-name': 'Dependabot',
          'workflow-name': 'Dependabot / Update Planner',
          'workflow-role': 'worker'
        }]
      }
    },
    ['campaign-runs']
  );

  expect(sources['campaign-runs'].rows).toEqual([
    expect.objectContaining({
      'error-signature': 'incomplete_evidence',
      'error-signature-label': 'Incomplete Evidence',
      'status-detail': 'Dependabot alert evidence was unavailable for the target repository.',
      'outcome-kind': 'report_incomplete'
    })
  ]);
});

it('diagnoses report-incomplete tool evidence when the safe-output payload is unavailable', () => {
  const sources = executeDashboardQueries(
    campaignDiagnosisQueries,
    {
      runs: {
        source: 'runs',
        metadata,
        rows: [{
          organization: 'githubnext',
          repository: 'gh-cao',
          workflow: '.github/workflows/dependabot-update-planner.lock.yml',
          run: '35818772349',
          'run-attempt': 1,
          'run-status': 'completed',
          'run-conclusion': 'failure',
          'failure-kind': 'driver_exit',
          'terminal-outcome': 'report_incomplete',
          'terminal-outcome-detail': 'The agent reported that required evidence or access was unavailable.'
        }]
      },
      workflows: {
        source: 'workflows',
        metadata,
        rows: [{
          organization: 'githubnext',
          repository: 'gh-cao',
          workflow: '.github/workflows/dependabot-update-planner.lock.yml',
          campaign: 'dependabot',
          'campaign-name': 'Dependabot',
          'workflow-name': 'Dependabot / Update Planner',
          'workflow-role': 'worker'
        }]
      },
      audits: {
        source: 'audits',
        metadata,
        rows: []
      },
      tools: { source: 'tools', metadata, rows: [] }
    },
    ['campaign-runs']
  );

  expect(sources['campaign-runs'].rows).toEqual([
    expect.objectContaining({
      'error-signature': 'incomplete_evidence',
      'error-signature-label': 'Incomplete Evidence',
      'status-detail': 'The agent reported that required evidence or access was unavailable.'
    })
  ]);
});

it('counts campaign runs by status in the worker query', () => {
  const query = dashboard.queries.find(
    (/** @type {{ name?: string }} */ candidate) => candidate.name === 'campaign-run-status-counts'
  );
  const rows = [
    { campaign: 'a', status: 'success', 'started-at': '2026-09-01T00:00:00Z' },
    { campaign: 'a', status: 'success', 'started-at': '2026-09-02T00:00:00Z' },
    { campaign: 'a', status: 'failure', 'started-at': '2026-09-03T00:00:00Z' },
    { campaign: 'b', status: 'success', 'started-at': '2026-09-04T00:00:00Z' }
  ];
  const result = executeDashboardQueries([query], {
    'campaign-runs': { source: 'campaign-runs', metadata, rows }
  }, ['campaign-run-status-counts']);
  const counts = result['campaign-run-status-counts'].rows
    .map((/** @type {Record<string, unknown>} */ row) => `${row.campaign}:${row.status}:${row.runs}`)
    .sort();
  expect(counts).toEqual(['a:failure:1', 'a:success:2', 'b:success:1']);
});

it('counts failed campaign dispatches by reason in the worker query', () => {
  const query = dashboard.queries.find(
    (/** @type {{ name?: string }} */ candidate) => candidate.name === 'campaign-dispatch-failure-reasons'
  );
  const rows = [
    { campaign: 'a', run: '1', status: 'failure', 'status-detail': 'Timeout' },
    { campaign: 'a', run: '2', status: 'failure', 'status-detail': 'Timeout' },
    { campaign: 'a', run: '3', status: 'stale', 'status-detail': 'Stale' },
    { campaign: 'a', run: '4', status: 'success', 'status-detail': 'Ok' },
    { campaign: 'b', run: '5', status: 'timed-out', 'status-detail': 'Timeout' }
  ];
  const result = executeDashboardQueries([query], {
    dispatches: { source: 'dispatches', metadata, rows }
  }, ['campaign-dispatch-failure-reasons']);
  expect(result['campaign-dispatch-failure-reasons'].rows.map(
    (/** @type {Record<string, unknown>} */ row) => `${row.campaign}:${row['status-detail']}:${row['failed-dispatches']}`
  ).sort()).toEqual(['a:Stale:1', 'a:Timeout:2', 'b:Timeout:1']);
  expect(result['campaign-dispatch-failure-reasons'].rows[0]['failed-dispatches']).toBe(2);
});
