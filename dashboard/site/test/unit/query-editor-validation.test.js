import { describe, expect, it } from 'vitest';
import { processDataRequest } from '../../src/data-worker.js';
import { validateDashboardDocument } from '../../src/validator.js';
import previewFixture from '../fixtures/query-editor.json' with { type: 'json' };
import layeredFixture from '../fixtures/chart-layer-facet-contract.json' with { type: 'json' };

const previewDocument = () => structuredClone(previewFixture);

/** @param {unknown} document */
const validate = (document) => processDataRequest({ operation: 'validate-query-editor-document', content: JSON.stringify(document) });

describe('query editor worker validation', () => {
  it('accepts complete bounded Dashboard Language and reports preview sources', async () => {
    expect(await validate(previewDocument())).toMatchObject({
      ok: true, pageId: 'query-preview', sourceNames: ['failure-counts'], errors: []
    });
  });

  it('rejects shell ingestion bindings in isolated preview documents', async () => {
    const fixture = previewDocument();
    const document = {
      ...fixture,
      dashboard: {
        ...fixture.dashboard,
        'data-status': { 'freshness-source': 'freshness', 'stale-after-hours': 24 },
        queries: [...fixture.dashboard.queries, {
          name: 'freshness', subject: 'Workflow evidence timestamp.', from: 'runs', limit: 1,
          select: [{ field: 'updated-at', as: 'latest-data-at' }]
        }]
      }
    };
    expect(validateDashboardDocument(JSON.stringify(document)).ok).toBe(true);
    expect(await validate(document)).toMatchObject({
      ok: false, errors: [expect.objectContaining({ code: 'QUERY-EDITOR', message: expect.stringContaining('data status') })]
    });
  });

  it('explains the exact encoding property required even for a pre-bucketed temporal query', async () => {
    const fixture = previewDocument();
    const x = { field: 'date', type: 'temporal' };
    const document = {
      ...fixture,
      dashboard: {
        ...fixture.dashboard,
        queries: [
          ...fixture.dashboard.queries,
          {
            name: 'daily-runs', subject: 'Native run counts by UTC day.', from: 'runs', limit: 200,
            compute: [{ as: 'date', function: 'date-bucket', args: [{ field: 'started-at' }, { value: 'day' }] }],
            aggregate: { by: ['date'], values: [{ field: 'run', as: 'runs', reducer: 'count' }] }
          }
        ],
        pages: [{
          ...fixture.dashboard.pages[0],
          views: [
            ...fixture.dashboard.pages[0].views,
            {
              id: 'daily-trend', mark: 'chart', chart: 'line',
              data: { source: 'daily-runs', limit: 200 },
              encoding: { x, y: { field: 'runs', type: 'quantitative' } }
            }
          ]
        }]
      }
    };
    const result = await validate(document);
    expect(result).toMatchObject({
      ok: false,
      errors: expect.arrayContaining([expect.objectContaining({
        code: 'DLS-E010', path: '$.dashboard.pages[0].views[1].encoding.x',
        message: expect.stringContaining('encoding.x.time-unit')
      })])
    });
    for (const hint of [
      'hour, day, week, month',
      'add "time-unit": "day" inside encoding.x',
      'pre-bucketing the source query does not replace'
    ]) {
      expect(result).toMatchObject({
        errors: expect.arrayContaining([expect.objectContaining({
          path: '$.dashboard.pages[0].views[1].encoding.x', message: expect.stringContaining(hint)
        })])
      });
    }
    Object.assign(x, { 'time-unit': 'day' });
    expect(await validate(document)).toMatchObject({ ok: true });
  });

  it('accepts bounded layered facets without a forbidden view-level limit', async () => {
    const document = structuredClone(layeredFixture);
    document.dashboard.pages[0].id = 'query-preview';
    expect(await validate(document)).toMatchObject({ ok: true });
    document.dashboard.queries[0].limit = 201;
    expect(await validate(document)).toMatchObject({ ok: false });
  });

  it('fails closed on unknown fields, cycles, invalid YAML, and oversized input', async () => {
    const badField = previewDocument();
    badField.dashboard.queries[0].aggregate.by = ['made-up-field'];
    expect(await validate(badField)).toMatchObject({ ok: false });
    const cycle = previewDocument();
    cycle.dashboard.queries[0].from = 'failure-counts';
    expect(await validate(cycle)).toMatchObject({ ok: false });
    for (const content of ['[invalid', 'x'.repeat(131073)]) {
      expect(await processDataRequest({ operation: 'validate-query-editor-document', content })).toMatchObject({ ok: false });
    }
  });

  it('rejects unbounded views, other page IDs, and new capabilities', async () => {
    const unbounded = previewDocument();
    unbounded.dashboard.pages[0].views[0].data.limit = 201;
    expect(await validate(unbounded)).toMatchObject({ ok: false, errors: [expect.objectContaining({ code: 'QUERY-EDITOR' })] });
    const unboundedQuery = previewDocument();
    unboundedQuery.dashboard.queries[0].limit = 201;
    expect(await validate(unboundedQuery)).toMatchObject({ ok: false });
    const otherPage = previewDocument();
    otherPage.dashboard.pages[0].id = 'overview';
    expect(await validate(otherPage)).toMatchObject({ ok: false });
    const actions = {
      ...previewDocument(),
      dashboard: { ...previewDocument().dashboard, 'cli-actions': [{ id: 'write', label: 'Write', icon: 'code', command: 'gh aw update' }] }
    };
    expect(await validate(actions)).toMatchObject({ ok: false });
  });
});
