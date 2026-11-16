import { expect, it } from 'vitest';
import { executeDashboardQueries } from '../../src/data/queries/declarative.js';
import { authoritativeDashboard } from '../authoritative-dashboard.js';
import contract from '../fixtures/overview-onboarding-contract.json' with { type: 'json' };

const queries = authoritativeDashboard.dashboard.queries;

/** @param {string} source @param {Record<string, unknown>[]} rows */
function input(source, rows) {
  return {
    source,
    rows,
    metadata: /** @type {import('../../src/presenter.js').SourceMetadata} */ ({
      'source-id': `${source}-fixture`,
      'source-kind': 'fixture',
      'as-of': '2026-11-16T12:00:00Z',
      'retrieved-at': '2026-11-16T12:00:00Z',
      availability: 'available',
      completeness: 'complete',
      freshness: 'fresh'
    })
  };
}

for (const testCase of contract.cases) {
  it(`classifies ${testCase.name}`, () => {
    const result = executeDashboardQueries(queries, {
      repositories: input('repositories', testCase.repositories),
      workflows: input('workflows', testCase.workflows),
      campaigns: input('campaigns', testCase.campaigns)
    }, ['overview-onboarding-state']);

    expect(result['overview-onboarding-state'].rows).toEqual([testCase.expected]);
  });
}
