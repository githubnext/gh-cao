import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { IDBFactory, IDBKeyRange } from 'fake-indexeddb';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { parseUnifiedSessionDocument } from '../../src/data/adapters/unified-session.js';
import { parseExperimentAssignments, experimentAssignmentArtifact } from '../../src/data/adapters/experiment-evidence.js';
import { adaptCachedGhAwJsonl, adaptGhAwLogs } from '../../src/data/adapters/gh-aw-logs.js';
import { normalize } from '../../src/data/normalize/index.js';
import { relationshipErrors } from '../../src/data/model/schema.js';
import { ingestCachedGhAwJsonl, ingestGhAwLogs } from '../../src/data/ingest/coordinator.js';
import { readCollections, readCanonicalBatch } from '../../src/data/storage/indexeddb.js';
import { renderDashboard, disposeDashboard } from '../../src/presenter.js';
import { resetSourceStore } from '../../src/source-store.js';
import { DataRequestWorker } from '../data-request-worker.js';
import { processDataRequest } from '../../src/data-worker.js';
import { authoritativeDashboard } from '../authoritative-dashboard.js';
import contract from '../fixtures/evaluation-evidence-contract.json' with { type: 'json' };

const content = readFileSync(resolve('test/fixtures/evaluation-session.jsonl'), 'utf8');
const projection = () => ({
  version: 1, path: 'usage/aw_session.jsonl',
  ...parseUnifiedSessionDocument(content, 'usage/aw_session.jsonl', { runId: '303', attempt: 1 })
});
const cached = () => `${JSON.stringify({
  schema_version: 5, kind: 'run', run: {
    run_id: 303, run_attempt: 1, organization: 'octo', repository: 'octo/api',
    workflow_name: 'Worker', workflow_path: '.github/workflows/worker.lock.yml',
    created_at: '2026-10-15T10:00:00Z', updated_at: '2026-10-15T10:01:00Z',
    status: 'completed', conclusion: 'success',
    experiments: { assignments: { legacy: 'control' } },
    graders: { results: [{ id: 'legacy', value: 1, status: 'fail' }] },
    evals: [{ id: 'legacy', answer: 'NO' }],
    cao_unified_session: projection()
  }
})}\n`;
/** @param {{ path: string, content: string }[]} files */
const artifactInput = (files) => ({
  observedAt: '2026-10-15T10:01:00Z',
  repository: { githubId: 1, owner: 'octo', name: 'api' },
  workflow: { githubId: 2, name: 'Worker', path: '.github/workflows/worker.lock.yml' },
  run: { githubRunId: 303, attempt: 1, createdAt: '2026-10-15T10:00:00Z', url: 'https://github.com/octo/api/actions/runs/303' },
  files
});
const evalArtifact = {
  path: 'usage/evals.jsonl',
  content: [
    { id: 'quality', answer: 'YES', runid: '303', timestamp: '2026-10-15T10:00:30Z', prompt: 'PRIVATE_PROMPT' },
    { id: 'correctness', answer: 'NO', runId: '303', runAttempt: 1 },
    { id: 'uncertain', answer: 'UNKNOWN' }
  ].map((row) => JSON.stringify(row)).join('\n')
};

afterEach(() => vi.unstubAllGlobals());

describe('unified evaluation evidence', () => {
  it.each([false, true])('ingests v2 split graders without duplicating session built-ins: %s', (withSession) => {
    const builtin = { id: 'loops', value: 0, status: 'pass', source: 'builtin' };
    const files = [
      { path: 'worker-graders/grader_manifest.json', content: JSON.stringify({
        version: 2, graders: [{ id: 'custom-count', unit: 'count', direction: 'higher_is_better',
          threshold: 1, script: 'PRIVATE_SCRIPT' }]
      }) },
      { path: 'worker-graders/grader_results.json', content: JSON.stringify({
        version: 2, run: { id: '303', attempt: 1 }, results: [
          { ...builtin, value: withSession ? 99 : 0 },
          { id: 'custom-count', value: null, status: 'unavailable', source: 'inline',
            metrics: [{ id: 'primary', value: null }, { id: 'diagnostic', value: 7 }],
            implementation: { id: 'gh-aw/custom-graders', version: 2, digest: 'sha256:frozen' },
            message: 'PRIVATE_MESSAGE', error: 'PRIVATE_ERROR' }
        ]
      }) }
    ];
    if (withSession) files.push({ path: 'usage/aw_session.jsonl', content: [
      { type: 'session.format', data: { version: 1 } },
      { type: 'grader.result', data: { version: 2, run: { id: '303', attempt: 1 }, results: [builtin] } }
    ].map((event) => JSON.stringify(event)).join('\n') });
    const batch = normalize(adaptGhAwLogs(artifactInput(files)).observations);
    expect(batch.graders?.map((row) => row.name).sort()).toEqual(['custom-count', 'loops']);
    const custom = batch.graders?.find((row) => row.name === 'custom-count');
    const observed = batch.graderObservations?.find((row) => row.graderId === custom?.id);
    expect(observed).toMatchObject({
      value: null,
      evaluatorDigest: 'sha256:frozen',
      metrics: [{ id: 'primary', value: null }, { id: 'diagnostic', value: 7 }]
    });
    expect(batch.graderObservations?.find((row) => row.graderId !== custom?.id)?.value).toBe(0);
    expect(custom).toMatchObject({ direction: 'higher_is_better', unit: 'count', threshold: 1 });
    expect(batch.graderObservations?.every((row) => batch.audits.some((audit) => audit.id === row.auditId))).toBe(true);
    expect(relationshipErrors(batch)).toEqual([]);
    expect(JSON.stringify(batch)).not.toContain('PRIVATE_');
  });

  it('rejects ambiguous, malformed and cross-run grader artifacts', () => {
    const file = { path: 'graders/grader_results.json',
      content: JSON.stringify({ version: 2, results: [{ id: 'loops', value: 0 }] }) };
    expect(() => adaptGhAwLogs(artifactInput([file, { ...file, path: 'worker-graders/grader_results.json' }])))
      .toThrow('Multiple grader result artifacts');
    for (const output of [
      { version: 3, results: [] },
      { version: 2, run: { id: '404', attempt: 1 }, results: [] },
      { version: 2, run: { id: '303', attempt: 2 }, results: [] },
      { version: 2, results: [{ id: 'quality', value: 1 }, { id: 'quality', value: 2 }] }
    ]) {
      expect(() => adaptGhAwLogs(artifactInput([{ ...file, content: JSON.stringify(output) }]))).toThrow();
    }
  });

  it.each(['', undefined])('binds an unspecified grader run ID to verified artifact context: %s', (id) => {
    const batch = normalize(adaptGhAwLogs(artifactInput([{
      path: 'usage/graders/grader_results.json',
      content: JSON.stringify({
        version: 2, run: { id }, results: [{ id: 'loops', value: 0, status: 'pass', source: 'builtin' }]
      })
    }])).observations);
    expect(batch.graderObservations).toMatchObject([{ value: 0, runId: batch.runs[0].id }]);
    expect(relationshipErrors(batch)).toEqual([]);
  });

  it('retains split graders idempotently through the worker query boundary', async () => {
    const indexedDB = new IDBFactory();
    vi.stubGlobal('indexedDB', indexedDB);
    vi.stubGlobal('IDBKeyRange', IDBKeyRange);
    const input = artifactInput([{
      path: 'usage/aw_session.jsonl', content: [
        { type: 'session.format', data: { version: 1 } },
        { type: 'grader.result', data: { version: 2, results: [
          { id: 'loops', value: 0, status: 'pass', source: 'builtin' }
        ] } }
      ].map((record) => JSON.stringify(record)).join('\n')
    }, {
      path: 'graders/grader_results.json', content: JSON.stringify({
        version: 2, run: { id: '303', attempt: 1 }, results: [
          { id: 'loops', value: 99, status: 'fail', source: 'builtin' },
          { id: 'quality', value: 0.75, status: 'pass', source: 'inline', unit: 'ratio' }
        ]
      })
    }]);
    for (let repeat = 0; repeat < 2; repeat++) {
      await ingestGhAwLogs(indexedDB, input, { now: Date.parse('2026-10-16T00:00:00Z') });
    }
    const dashboard = authoritativeDashboard.dashboard;
    const sources = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (
      await processDataRequest({
        operation: 'query-canonical-dashboard',
        sourceNames: [contract.graders.query, contract.graders.scatter.query],
        context: { queries: dashboard.queries, pages: dashboard.pages }
      })
    );
    expect(sources[contract.graders.query].rows).toHaveLength(2);
    expect(sources[contract.graders.query].rows).toEqual(expect.arrayContaining([
      expect.objectContaining({ grader: 'loops', value: 0 }),
      expect.objectContaining({ grader: 'quality', value: 0.75 })
    ]));
    expect(sources[contract.graders.scatter.query].rows).toMatchObject([
      { grader: 'quality', value: 0.75, 'max-grader-value': 0.75, 'normalized-value': 1 }
    ]);
  });

  it('rejects invalid or ambiguous standalone assignments without exposing their contents', () => {
    for (const content of ['PRIVATE_INVALID_JSON', 'null', '[]', '{"prompt":100}', '{"prompt":""}']) {
      expect(() => parseExperimentAssignments(content, 'experiment/assignments.json'))
        .toThrow(/^experiment\/assignments.json must contain/);
    }
    expect(() => experimentAssignmentArtifact([
      { path: 'usage/experiment/assignments.json', content: '{"prompt":"candidate"}' },
      { path: 'experiment/assignments.json', content: '{"prompt":"control"}' }
    ])).toThrow('Multiple experiment assignment artifacts found for one run');
    expect(() => experimentAssignmentArtifact([
      { path: 'usage/experiment/assignments.json', content: '{"prompt":"candidate"}' },
      { path: 'worker-experiment/assignments.json', content: 'PRIVATE_INVALID_JSON' }
    ])).toThrow('worker-experiment/assignments.json must contain valid experiment assignment JSON');
    expect(parseExperimentAssignments('{}', 'experiment/assignments.json')).toEqual({ assignments: {} });
  });

  it('deduplicates identical current-run assignments in compact and named experiment artifacts', () => {
    const batch = normalize(adaptGhAwLogs(artifactInput([
      { path: 'usage/experiment/assignments.json', content: '{"prompt":"candidate","style":"concise"}' },
      { path: 'worker-experiment/assignments.json', content: '{"style":"concise","prompt":"candidate"}' }
    ])).observations);
    expect(batch.experiments?.map((row) => row.name)).toEqual(['prompt', 'style']);
    expect(batch.experimentAssignments?.map((row) => row.variant)).toEqual(['candidate', 'concise']);
    expect(relationshipErrors(batch)).toEqual([]);
  });

  it.each([false, true])('ingests standalone eval artifacts with a session lacking evals: %s', (withSession) => {
    const files = [evalArtifact];
    if (withSession) files.push({
      path: 'usage/aw_session.jsonl', content: '{"type":"session.format","data":{"version":1}}\n'
    });
    const batch = normalize(adaptGhAwLogs(artifactInput(files)).observations);
    expect(batch.evalObservations?.map((row) => row.evalResult).sort()).toEqual(['NO', 'UNKNOWN', 'YES']);
    expect(batch.graders).toEqual([]);
    expect(batch.graderObservations).toEqual([]);
    expect(batch.audits.filter((row) => row.type === 'eval.result')).toHaveLength(3);
    expect(batch.audits.find((row) => row.type === 'eval.result' && row.evalId === 'quality')?.payloadRef)
      .toBe('usage/evals.jsonl#L1');
    expect(relationshipErrors(batch)).toEqual([]);
    expect(JSON.stringify(batch)).not.toContain('PRIVATE_PROMPT');
  });

  it.each([false, true])('ingests standalone experiments and evals together with an optional session: %s', (withSession) => {
    const files = [
      evalArtifact,
      { path: 'usage/experiment/assignments.json', content: '{"prompt":"candidate"}' }
    ];
    if (withSession) files.push({
      path: 'usage/aw_session.jsonl', content: '{"type":"session.format","data":{"version":1}}\n'
    });
    const batch = normalize(adaptGhAwLogs(artifactInput(files)).observations);
    expect(batch.experiments?.map((row) => row.name)).toEqual(['prompt']);
    expect(batch.experimentAssignments).toMatchObject([{ variant: 'candidate', runId: batch.runs[0].id }]);
    expect(batch.evalObservations?.map((row) => row.evalResult).sort()).toEqual(['NO', 'UNKNOWN', 'YES']);
    expect(relationshipErrors(batch)).toEqual([]);
  });

  it('prefers session evals without parsing a conflicting standalone artifact', () => {
    const batch = normalize(adaptGhAwLogs(artifactInput([
      { path: 'usage/aw_session.jsonl', content },
      { path: 'usage/evals.jsonl', content: 'PRIVATE_INVALID_JSON' }
    ])).observations);
    expect(batch.evals?.map((row) => row.name)).toEqual(['quality', 'uncertain']);
    expect(batch.evalObservations).toHaveLength(2);
    expect(relationshipErrors(batch)).toEqual([]);
  });

  it('rejects conflicting run identities and attempts without inferring eval answers', () => {
    const batch = normalize(adaptGhAwLogs(artifactInput([{
      path: 'evals/evals.jsonl',
      content: [
        { id: 'other-run', answer: 'YES', runid: '404' },
        { id: 'conflict', answer: 'NO', runId: '303', run_id: '404' },
        { id: 'other-attempt', answer: 'YES', run_attempt: 2 },
        { id: 'invalid', answer: 'MAYBE' }
      ].map((row) => JSON.stringify(row)).join('\n')
    }])).observations);
    expect(batch.evals).toEqual([]);
    expect(batch.evalObservations).toEqual([]);
    expect(batch.audits.filter((row) => row.type === 'workflow_run_eval_unclassified')).toHaveLength(4);
    expect(relationshipErrors(batch)).toEqual([]);
    expect(() => adaptGhAwLogs(artifactInput([evalArtifact, {
      ...evalArtifact, path: 'evals/evals.jsonl'
    }]))).toThrow('Multiple eval artifacts');
  });

  it('persists artifact evals idempotently and renders its worker payload', async () => {
    const indexedDB = new IDBFactory();
    vi.stubGlobal('indexedDB', indexedDB);
    vi.stubGlobal('IDBKeyRange', IDBKeyRange);
    vi.stubGlobal('Worker', DataRequestWorker);
    const input = artifactInput([evalArtifact]);
    const options = { now: Date.parse('2026-10-16T00:00:00Z') };
    await ingestGhAwLogs(indexedDB, input, options);
    await ingestGhAwLogs(indexedDB, input, options);
    const persisted = indexedDB;
    vi.stubGlobal('indexedDB', persisted);
    const batch = await readCanonicalBatch(persisted);
    expect(batch.evals).toHaveLength(3);
    expect(batch.evalObservations).toHaveLength(3);
    expect(batch.evalObservations?.every((row) => batch.audits.some((audit) => audit.id === row.auditId))).toBe(true);
    expect(relationshipErrors(batch)).toEqual([]);
    const dashboard = authoritativeDashboard.dashboard;
    const sources = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (
      await processDataRequest({
        operation: 'query-canonical-dashboard', sourceNames: [contract.evals.query, contract.evals.distribution.query],
        pageId: 'evals',
        context: { queries: dashboard.queries, pages: dashboard.pages }
      })
    );
    expect(Object.values(sources).some((source) => source.rows.length === 3)).toBe(true);
    const page = dashboard.pages.find((/** @type {{ id: string }} */ page) => page.id === 'evals');
    const rendered = renderDashboard({
      document: { ...authoritativeDashboard, dashboard: {
        ...dashboard, pages: [page], navigation: [{ label: 'Evidence', pages: ['evals'] }]
      } },
      sources
    });
    document.body.append(rendered);
    try {
      const view = rendered.querySelector(`[data-view-id="${contract.evals.view}"]`);
      expect(view).not.toBeNull();
      expect(view?.textContent).toContain('quality');
      expect(view?.textContent).toContain('correctness');
      expect(view?.textContent).toContain('uncertain');
      expect(view?.textContent).toContain('UNKNOWN');
      expect(view?.textContent).toContain('.github/workflows/worker.md');
      expect(view?.textContent).toContain('api');
      expect(view?.querySelector('a[href="https://github.com/octo/api/actions/runs/303"]')).not.toBeNull();
      const chart = rendered.querySelector(`[data-view-id="${contract.evals.distribution.view}"]`);
      expect(chart?.querySelector('[data-chart-widget="horizontal-bar"]')).not.toBeNull();
      const sections = chart?.querySelectorAll('.horizontal-bar-chart-section');
      expect(sections).toHaveLength(3);
      for (const section of sections ?? []) {
        expect([...section.querySelectorAll('.horizontal-bar-chart-label')].map((label) => label.textContent))
          .toEqual([...contract.evals.distribution.answers].sort());
        expect([...section.querySelectorAll('.horizontal-bar-chart-value')].map((value) => Number.parseFloat(value.textContent ?? '')))
          .toEqual(expect.arrayContaining([100, 0, 0]));
      }
    } finally {
      disposeDashboard(rendered);
      rendered.remove();
      resetSourceStore();
    }
  });

  it('computes complete per-eval percentages through the canonical worker without merging workflows', async () => {
    vi.stubGlobal('indexedDB', new IDBFactory());
    vi.stubGlobal('IDBKeyRange', IDBKeyRange);
    for (const [index, answer] of ['YES', 'YES', 'NO', 'UNKNOWN'].entries()) {
      const input = artifactInput([{
        path: 'usage/evals.jsonl',
        content: JSON.stringify({ id: 'quality', answer })
      }]);
      input.run.githubRunId = 400 + index;
      await ingestGhAwLogs(indexedDB, input, { now: Date.parse('2026-10-16T00:00:00Z') });
    }
    const other = artifactInput([{
      path: 'usage/evals.jsonl', content: JSON.stringify({ id: 'quality', answer: 'NO' })
    }]);
    other.run.githubRunId = 500;
    other.workflow = { githubId: 3, name: 'Other', path: '.github/workflows/other.lock.yml' };
    await ingestGhAwLogs(indexedDB, other, { now: Date.parse('2026-10-16T00:00:00Z') });
    const dashboard = authoritativeDashboard.dashboard;
    const sources = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (
      await processDataRequest({
        operation: 'query-canonical-dashboard', sourceNames: [contract.evals.distribution.query],
        context: { queries: dashboard.queries, pages: dashboard.pages }
      })
    );
    const rows = sources[contract.evals.distribution.query].rows;
    expect(rows).toHaveLength(6);
    const labels = new Set(rows.map((row) => row['eval-label']));
    expect(labels.size).toBe(2);
    for (const label of labels) {
      const group = rows.filter((row) => row['eval-label'] === label);
      expect(group.map((row) => row['eval-result'])).toEqual(contract.evals.distribution.answers);
      expect(group.reduce((sum, row) => sum + Number(row['answer-percent']), 0)).toBeCloseTo(100);
      expect(group.map((row) => row['answer-percent'])).toEqual(
        String(group[0].workflow).includes('other') ? [0, 100, 0] : [50, 25, 25]
      );
    }
  });

  it('limits complete eval groups rather than truncating their three answer bars', async () => {
    vi.stubGlobal('indexedDB', new IDBFactory());
    vi.stubGlobal('IDBKeyRange', IDBKeyRange);
    await ingestGhAwLogs(indexedDB, artifactInput([{
      path: 'usage/evals.jsonl',
      content: Array.from({ length: 40 }, (_, index) => JSON.stringify({
        id: `quality-${String(index).padStart(2, '0')}`, answer: 'UNKNOWN'
      })).join('\n')
    }]), { now: Date.parse('2026-10-16T00:00:00Z') });
    const dashboard = authoritativeDashboard.dashboard;
    const sources = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (
      await processDataRequest({
        operation: 'query-canonical-dashboard', sourceNames: [contract.evals.distribution.query],
        context: { queries: dashboard.queries, pages: dashboard.pages }
      })
    );
    const rows = sources[contract.evals.distribution.query].rows;
    expect(rows).toHaveLength(99);
    const labels = new Set(rows.map((row) => row['eval-label']));
    expect(labels.size).toBe(33);
    for (const label of labels) {
      const group = rows.filter((row) => row['eval-label'] === label);
      expect(group.map((row) => row['eval-result'])).toEqual(contract.evals.distribution.answers);
      expect(group.map((row) => row['answer-percent'])).toEqual([0, 0, 100]);
    }
  });

  it('retains untimed typed evidence, enriches grader metadata and never stores private bodies', () => {
    const parsed = projection();
    expect(parsed.events).toMatchObject([{ type: 'session.format', fields: { status: 'supported' } }]);
    expect(parsed.events[0].timestamp).toBeUndefined();
    expect(parsed).toMatchObject({
      experiments: { assignments: { prompt: 'candidate' } },
      graders: { results: [{
        id: 'quality', value: 0.93, status: 'pass', name: 'Quality',
        unit: 'ratio', direction: 'higher_is_better', threshold: 0.8,
        evaluatorDigest: 'sha256:quality-v1', sessionPayloadRef: 'usage/aw_session.jsonl#L6'
      }] },
      evals: [{ id: 'quality', answer: 'YES' }, { id: 'uncertain', answer: 'UNKNOWN' }]
    });
    expect(JSON.stringify(parsed)).not.toMatch(/PRIVATE_|other-run|stale/);
  });

  it('prefers each session category over conflicting legacy evidence without mixing entity kinds', () => {
    const batch = normalize(adaptCachedGhAwJsonl(cached()).observations);
    expect(batch.experiments?.map((row) => row.name)).toEqual(['prompt']);
    expect(batch.experimentAssignments).toMatchObject([{ variant: 'candidate' }]);
    expect(batch.graders?.map((row) => row.name)).toEqual(['quality']);
    expect(batch.graderObservations).toMatchObject([{ value: 0.93, status: 'pass' }]);
    expect(batch.evals?.map((row) => row.name)).toEqual(['quality', 'uncertain']);
    expect(batch.evalObservations?.map((row) => row.evalResult)).toEqual(['YES', 'UNKNOWN']);
    expect(batch.graders?.[0].id).not.toBe(batch.evals?.[0].id);
    expect(batch.evalObservations?.every((row) => batch.audits.some((audit) => audit.id === row.auditId))).toBe(true);
    expect(batch.audits.filter((row) => row.type === 'grader.result')).toHaveLength(1);
    expect(batch.audits.find((row) => row.type === 'eval.result')?.payloadRef).toBe('usage/aw_session.jsonl#L7');
    expect(relationshipErrors(batch)).toEqual([]);
  });

  it('ingests the same unified evidence through the direct artifact adapter', () => {
    const batch = normalize(adaptGhAwLogs({
      observedAt: '2026-10-15T10:01:00Z',
      repository: { githubId: 1, owner: 'octo', name: 'api' },
      workflow: { githubId: 2, name: 'Worker', path: '.github/workflows/worker.lock.yml' },
      run: { githubRunId: 303, attempt: 1, createdAt: '2026-10-15T10:00:00Z',
        url: 'https://github.com/octo/api/actions/runs/303' },
      files: [{ path: 'usage/aw_session.jsonl', content }]
    }).observations);
    expect(batch.graderObservations).toMatchObject([{ value: 0.93 }]);
    expect(batch.evalObservations).toHaveLength(2);
    expect(batch.experimentAssignments).toHaveLength(1);
    expect(relationshipErrors(batch)).toEqual([]);
  });

  it.each(['browser'].flatMap((backend) =>
    ['experiment.assignment', 'experiment.state', 'artifact'].map((source) => ({ backend, source }))
  ))('persists $source experiments and resolves every Experiments view source through the $backend worker', async ({ source }) => {
    const indexedDB = new IDBFactory();
    vi.stubGlobal('indexedDB', indexedDB);
    vi.stubGlobal('IDBKeyRange', IDBKeyRange);
    const input = {
      observedAt: '2026-10-15T10:01:00Z',
      repository: { githubId: 1, owner: 'octo', name: 'api' },
      workflow: { githubId: 2, name: 'Worker', path: '.github/workflows/worker.lock.yml' },
      run: { githubRunId: 303, attempt: 1, createdAt: '2026-10-15T10:00:00Z',
        url: 'https://github.com/octo/api/actions/runs/303' },
      files: source === 'artifact' ? [
        { path: 'usage/experiment/assignments.json', content: '{"prompt":"candidate","style":"concise"}' },
        { path: 'worker-experiment/assignments.json', content: '{"style":"concise","prompt":"candidate"}' }
      ] : [
        { path: 'usage/aw_session.jsonl', content: [
          { type: 'session.format', data: { version: 1 } },
          { type: source, data: { runId: '303', runAttempt: 1,
            assignments: { prompt: 'candidate', style: 'concise' } } }
        ].map((record) => JSON.stringify(record)).join('\n') },
        { path: 'usage/experiment/assignments.json', content: 'PRIVATE_INVALID_FALLBACK' }
      ]
    };
    await ingestGhAwLogs(indexedDB, input, { now: Date.parse('2026-10-16T00:00:00Z') });
    const stored = await readCollections(indexedDB, ['experiments', 'experimentAssignments']);
    expect(stored.experiments).toHaveLength(2);
    expect(stored.experimentAssignments).toHaveLength(2);
    const dashboard = authoritativeDashboard.dashboard;
    const sources = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (
      await processDataRequest({
        operation: 'query-canonical-dashboard',
        sourceNames: [contract.experiments.query, contract.experiments.coverage.query, contract.experiments.definitions.query],
        context: { queries: dashboard.queries, pages: dashboard.pages }
      })
    );
    expect(sources[contract.experiments.query].rows).toMatchObject([
      { experiment: 'prompt', variant: 'candidate', run: '303', repository: 'api',
        workflow: '.github/workflows/worker.md', 'assignment-link': 'https://github.com/octo/api/actions/runs/303' },
      { experiment: 'style', variant: 'concise', run: '303', repository: 'api' }
    ]);
    expect(sources[contract.experiments.coverage.query].rows).toMatchObject([
      { experiment: 'prompt', assignments: 1 }, { experiment: 'style', assignments: 1 }
    ]);
    expect(sources[contract.experiments.definitions.query].rows.map((row) => row['experiment-name']))
      .toEqual(['prompt', 'style']);
    const page = dashboard.pages.find((/** @type {{ id: string }} */ page) => page.id === 'experiments');
    const rendered = renderDashboard({
      document: { ...authoritativeDashboard, dashboard: {
        ...dashboard, pages: [page], navigation: [{ label: 'Evidence', pages: ['experiments'] }]
      } },
      sources
    });
    document.body.append(rendered);
    try {
      const view = rendered.querySelector(`[data-view-id="${contract.experiments.view}"]`);
      expect(view?.textContent).toContain('candidate');
      expect(view?.textContent).toContain('concise');
      expect(view?.querySelector('a[href="https://github.com/octo/api/actions/runs/303"]')).not.toBeNull();
    } finally {
      disposeDashboard(rendered);
      rendered.remove();
      resetSourceStore();
    }
    await ingestGhAwLogs(indexedDB, input, { now: Date.parse('2026-10-16T00:00:00Z') });
    expect((await readCollections(indexedDB, ['experimentAssignments'])).experimentAssignments).toHaveLength(2);
  });

  it('prefers explicit unified assignments, including empty maps, over standalone artifacts', () => {
    for (const assignments of [{ prompt: 'control' }, {}]) {
      const batch = normalize(adaptGhAwLogs({
        observedAt: '2026-10-15T10:01:00Z',
        repository: { githubId: 1, owner: 'octo', name: 'api' },
        workflow: { githubId: 2, name: 'Worker', path: '.github/workflows/worker.lock.yml' },
        run: { githubRunId: 303, attempt: 1, createdAt: '2026-10-15T10:00:00Z' },
        files: [
          { path: 'usage/aw_session.jsonl', content: [
            { type: 'session.format', data: { version: 1 } },
            { type: 'experiment.assignment', data: { assignments } }
          ].map((record) => JSON.stringify(record)).join('\n') },
          { path: 'usage/experiment/assignments.json', content: 'PRIVATE_INVALID_FALLBACK' }
        ]
      }).observations);
      expect(batch.experimentAssignments?.map((row) => row.variant)).toEqual(Object.values(assignments));
    }
  });

  it('falls back independently by category while honoring explicit empty session results', () => {
    const envelope = JSON.parse(cached());
    envelope.run.cao_unified_session = {
      version: 1, path: 'usage/aw_session.jsonl',
      ...parseUnifiedSessionDocument([
        { type: 'session.format', data: { version: 1 } },
        { type: 'grader.result', data: { run: { id: '303', attempt: 1 }, results: [] } }
      ].map((row) => JSON.stringify(row)).join('\n'), 'usage/aw_session.jsonl', { runId: '303', attempt: 1 })
    };
    const batch = normalize(adaptCachedGhAwJsonl(`${JSON.stringify(envelope)}\n`).observations);
    expect(batch.graderObservations).toHaveLength(0);
    expect(batch.graders).toHaveLength(0);
    expect(batch.evals?.map((row) => row.name)).toEqual(['legacy']);
    expect(batch.experiments?.map((row) => row.name)).toEqual(['legacy']);
  });

  it('ignores other-run and other-attempt evidence, and never turns state counters into assignments', () => {
    const parsed = parseUnifiedSessionDocument([
      { type: 'session.format', data: { version: 1 } },
      { type: 'experiment.state', data: { counts: { prompt: { candidate: 3 } } } },
      { type: 'experiment.assignment', data: { runAttempt: 2, assignments: { stale: 'control' } } },
      { type: 'grader.manifest', data: { graders: [{ id: 'unobserved', unit: 'score' }] } },
      { type: 'grader.result', data: { run: { id: '303', attempt: 2 }, results: [{ id: 'stale' }] } },
      { type: 'grader.result', data: { id: 'other-run', value: 1, run_id: '404' } },
      { type: 'grader.result', data: { id: 'quality', value: 0.93, runId: '303', metrics: [
        { id: 'accuracy', value: 0.93, message: 'PRIVATE_METRIC' },
        { id: 'error', value: 'PRIVATE_ERROR' }, { value: 1 }
      ] } },
      { type: 'eval.result', data: { id: 'wrong-attempt', answer: 'YES', runId: '303', runAttempt: 2 } },
      { type: 'eval.result', data: { id: 'conflicting', answer: 'YES', runId: '303', run_id: '404' } }
    ].map((row) => JSON.stringify(row)).join('\n'), 'usage/aw_session.jsonl', { runId: '303', attempt: 1 });
    expect(parsed).toMatchObject({ graders: { results: [{ id: 'quality', value: 0.93, metrics: [{ id: 'accuracy', value: 0.93 }] }] } });
    expect(JSON.stringify(parsed)).not.toMatch(/PRIVATE_|stale|other-run|unobserved|wrong-attempt|conflicting/);
    expect(parsed).not.toHaveProperty('experiments');
    expect(parsed).not.toHaveProperty('evals');
  });

  it('resolves separate score and binary-answer views through canonical ingestion and the production worker', async () => {
    const indexedDB = new IDBFactory();
    vi.stubGlobal('indexedDB', indexedDB);
    vi.stubGlobal('IDBKeyRange', IDBKeyRange);
    await ingestCachedGhAwJsonl(indexedDB, cached(), { now: Date.parse('2026-10-16T00:00:00Z') });
    const dashboard = authoritativeDashboard.dashboard;
    const sources = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (
      await processDataRequest({
        operation: 'query-canonical-dashboard',
        sourceNames: [contract.graders.query, contract.graders.scatter.query, contract.evals.query,
          'experiment-assignment-coverage', 'eval-observations'],
        context: { queries: dashboard.queries, pages: dashboard.pages }
      })
    );
    expect(sources[contract.graders.query].rows).toMatchObject([
      { grader: 'quality', value: 0.93, unit: 'ratio', status: 'pass', repository: 'api' }
    ]);
    expect(sources[contract.graders.scatter.query].rows).toMatchObject([
      { grader: 'quality', value: 0.93, 'max-grader-value': 0.93, 'normalized-value': 1 }
    ]);
    expect(sources[contract.evals.query].rows).toMatchObject([
      { eval: 'quality', 'eval-result': 'YES' }, { eval: 'uncertain', 'eval-result': 'UNKNOWN' }
    ]);
    expect(sources['eval-observations'].rows).toHaveLength(2);
    expect(sources['eval-observations'].rows.every((row) => typeof row['audit-id'] === 'string')).toBe(true);
    expect(sources['experiment-assignment-coverage'].rows).toMatchObject([{ experiment: 'prompt', assignments: 1 }]);
  });

  it('normalizes scatter values per canonical grader through the worker without losing zero or missing evidence', async () => {
    const indexedDB = new IDBFactory();
    vi.stubGlobal('indexedDB', indexedDB);
    vi.stubGlobal('IDBKeyRange', IDBKeyRange);
    const results = [
      { run: 401, grader: 'quality', value: 2 },
      { run: 402, grader: 'quality', value: 4 },
      { run: 403, grader: 'quality', value: null },
      { run: 404, grader: 'zero', value: 0 },
      { run: 405, grader: 'zero', value: 0 },
      { run: 406, grader: 'negative', value: -8 },
      { run: 407, grader: 'negative', value: -4 },
      { run: 408, grader: 'quality', value: 100, workflow: 'other' }
    ];
    const jsonl = results.map((result, index) => JSON.stringify({
      schema_version: 5, kind: 'run',
      run: {
        run_id: result.run, run_attempt: 1, organization: 'octo', repository: 'octo/api',
        workflow_name: result.workflow ?? 'worker',
        workflow_path: `.github/workflows/${result.workflow ?? 'worker'}.lock.yml`,
        created_at: `2026-10-15T10:0${index}:00Z`, updated_at: `2026-10-15T10:0${index}:30Z`,
        status: 'completed', conclusion: 'success',
        graders: { results: [{ id: result.grader, value: result.value, unit: 'score', status: 'observed' }] }
      }
    })).join('\n');
    await ingestCachedGhAwJsonl(indexedDB, jsonl, { now: Date.parse('2026-10-16T00:00:00Z') });
    const dashboard = authoritativeDashboard.dashboard;
    const sources = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (
      await processDataRequest({
        operation: 'query-canonical-dashboard',
        sourceNames: [contract.graders.scatter.query, contract.graders.query, 'grader-normalized-observations'],
        context: { queries: dashboard.queries, pages: dashboard.pages }
      })
    );
    const points = sources[contract.graders.scatter.query].rows;
    expect(points.map((row) => row['normalized-value'])).toEqual([0.5, 1, 2, 1, 1]);
    expect(points.map((row) => row['max-grader-value'])).toEqual([4, 4, -4, -4, 100]);
    expect(points.every((row) => typeof row['observed-at'] === 'string')).toBe(true);
    expect(sources[contract.graders.query].rows).toHaveLength(8);
    expect(sources['grader-normalized-observations'].rows.filter((row) => row['normalized-value'] === null))
      .toHaveLength(3);
  });
});
