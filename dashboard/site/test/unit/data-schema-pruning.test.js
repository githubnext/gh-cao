import { rmSync } from 'node:fs';
import { IDBFactory } from 'fake-indexeddb';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { processDataRequest } from '../../src/data-worker.js';
import { PRUNED_CANONICAL_FIELDS, pruneCanonicalRecord } from '../../src/data/model/fields.js';
import { normalize } from '../../src/data/normalize/index.js';
import { queryDatabaseSources } from '../../src/data/queries/database.js';
import definitions from '../../src/data/queries/database.json' with { type: 'json' };
import { readCollection, replaceCanonicalBatch, upsertCanonicalBatch } from '../../src/data/storage/indexeddb.js';

const directories = /** @type {string[]} */ ([]);
const observedAt = '2026-10-02T00:00:00.000Z';

afterEach(() => {
  vi.unstubAllGlobals();
  for (const directory of directories.splice(0)) rmSync(directory, { recursive: true, force: true });
});

function batch() {
  const value = normalize([]);
  value.repositories.push({ id: 'repository:1', owner: 'owner', name: 'repo', observedAt });
  value.workflows.push({
    id: 'workflow:1', repositoryId: 'repository:1', path: '.github/workflows/demo.md',
    observedAt, ghAwMetadata: { raw: 'metadata' }, ghAwManifest: { raw: 'manifest' }
  });
  value.runs.push({
    id: 'github:run:owner/repo:1', githubRunId: '1', attempt: 2, owner: 'owner', repository: 'repo',
    repositoryId: 'repository:1', workflowId: 'workflow:1', observedAt,
    startedAt: observedAt, aicTotal: 2.5, aic: 2.5,
    inputTokens: 100, outputTokens: 20, tokenUsage: { raw: 'usage' },
    graders: { results: [{ id: 'quality', value: 1 }] }, context: { raw: 'context' },
    logsPayload: { raw: 'logs' }, customEvidence: { retained: true }
  });
  value.tools.push({
    id: 'tool:1', runId: value.runs[0].id, timestamp: observedAt, observedAt,
    type: 'mcp.tool_call', name: 'search', mcpServer: 'github', mcpTool: 'search',
    toolType: 'mcp', isSkill: false, requestBytes: 42, responseBytes: 0,
    correlationId: 'call-1', provenance: { source: 'gh-aw-logs', sourceId: 'call-1', observedAt }
  });
  value.issues.push({
    id: 'issue:1', runId: value.runs[0].id, timestamp: observedAt, observedAt,
    state: 'closed', closed: true, stateReason: null, issueState: 'closed',
    issueClosed: true, issueStateReason: null, issueClosedAt: null, closedAt: null
  });
  return value;
}

describe('canonical field pruning', () => {
  it('never prunes a field required by a canonical database query', () => {
    for (const definition of definitions) {
      const stores = [definition.from.slice(1)];
      const derived = new Set([
        ...(definition.joins ?? []).flatMap((join) => join.fields.map((field) => field.as ?? field.field)),
        ...(definition.compute ?? []).map((field) => field.as)
      ]);
      const fields = [
        ...(definition.select ?? []).map((field) => field.field),
        ...(definition.compute ?? []).flatMap((field) => (field.args ?? []).flatMap((arg) => 'field' in arg ? [arg.field] : [])),
        ...(definition.filter?.predicates ?? []).map((field) => field.field),
        ...(definition.joins ?? []).flatMap((join) => join.on.map((key) => key.left))
      ].filter((field) => typeof field === 'string' && !derived.has(field));
      for (const store of stores) {
        for (const field of fields) expect(PRUNED_CANONICAL_FIELDS[store] ?? []).not.toContain(field);
      }
      for (const relation of definition.joins ?? []) {
        for (const field of [...relation.on.map((key) => key.right), ...relation.fields.map((field) => field.field)]) {
          expect(PRUNED_CANONICAL_FIELDS[relation.source.slice(1)] ?? []).not.toContain(field);
        }
      }
    }
  });

  it('is non-mutating and idempotent and preserves unknown evidence, null and provenance', () => {
    const record = batch().runs[0];
    const pruned = pruneCanonicalRecord('runs', record);
    expect(record).toHaveProperty('tokenUsage');
    expect(pruned).not.toHaveProperty('tokenUsage');
    expect(pruned).toMatchObject({ aicTotal: 2.5, attempt: 2, customEvidence: { retained: true } });
    expect(pruneCanonicalRecord('runs', pruned)).toBe(pruned);
    const audit = { id: 'audit:1', tokenUsage: { retained: true }, state: null };
    expect(pruneCanonicalRecord('audits', audit)).toBe(audit);
    expect(pruneCanonicalRecord('issues', batch().issues[0])).toMatchObject({
      state: 'closed', closed: true, stateReason: null, closedAt: null
    });
  });

  it('prunes raw observations during normalization without discarding separate evidence', () => {
    const run = batch().runs[0];
    const value = normalize([
      { kind: 'run', source: 'gh-aw-logs', sourceId: 'run:1', observedAt, data: run },
      { kind: 'grader-observation', source: 'gh-aw-logs', sourceId: 'grade:1', observedAt,
        data: { id: 'grade:1', runId: run.id, graderId: 'grader:quality', value: 1 } }
    ]);
    expect(value.runs[0]).not.toHaveProperty('graders');
    expect(value.runs[0]).toMatchObject({ aicTotal: 2.5, inputTokens: 100, outputTokens: 20 });
    expect(value.graderObservations).toEqual([expect.objectContaining({ value: 1, graderId: 'grader:quality' })]);
    expect(value.runs[0].provenance).toEqual({ source: 'gh-aw-logs', sourceId: 'run:1', observedAt });
  });

  it('retains original facts before removing recomputable source copies', () => {
    const audit = { id: 'audit:1', targetRepo: 'octo/api' };
    expect(pruneCanonicalRecord('audits', audit)).toEqual({
      id: 'audit:1', targetOrganization: 'octo', targetRepository: 'api'
    });
    expect(audit).toEqual({ id: 'audit:1', targetRepo: 'octo/api' });
    expect(pruneCanonicalRecord('repositories', { fullName: 'octo/api' })).toEqual({ owner: 'octo', name: 'api' });
    expect(pruneCanonicalRecord('runs', { repositoryFullName: 'octo/api' })).toEqual({ owner: 'octo', repository: 'api' });
    expect(pruneCanonicalRecord('graders', { name: 'quality', sourceGraderId: 'quality-v1' }))
      .toEqual({ name: 'quality', sourceGraderId: 'quality-v1' });
    expect(pruneCanonicalRecord('evals', { name: 'correctness', sourceEvalId: 'correctness-v1' }))
      .toEqual({ name: 'correctness', sourceEvalId: 'correctness-v1' });
    expect(pruneCanonicalRecord('evalObservations', {
      resultTimestamp: observedAt, answer: 'YES', requestedModel: 'model'
    })).toEqual({ timestamp: observedAt, evalResult: 'YES' });
    expect(pruneCanonicalRecord('graderObservations', {
      timestamp: observedAt, resultTimestamp: '2026-10-02T00:00:00Z'
    })).toEqual({ timestamp: observedAt });
  });

  it('rejects conflicting evidence rather than silently discarding it', () => {
    expect(() => pruneCanonicalRecord('audits', { targetRepo: 'invalid' })).toThrow('owner/repository');
    expect(() => pruneCanonicalRecord('audits', {
      targetRepo: 'octo/api', targetOrganization: 'different'
    })).toThrow('conflicts');
    expect(() => pruneCanonicalRecord('graderObservations', {
      timestamp: null, resultTimestamp: observedAt
    })).toThrow('conflicts');
    expect(() => pruneCanonicalRecord('evalObservations', { evalResult: 'YES', answer: 'NO' })).toThrow('conflicts');
  });
});

describe('pruning at the IndexedDB storage boundary', () => {
  it('reconstructs removed fields through the production worker query boundary', async () => {
    const indexedDB = new IDBFactory();
    vi.stubGlobal('indexedDB', indexedDB);
    const input = batch();
    const provenance = { source: 'test', sourceId: 'projection', observedAt };
    input.campaigns.push({
      id: 'campaign:1', slug: 'demo', name: 'Current campaign', icon: 'goal', readmePath: 'demo/README.md',
      aiCreditAllowance: 1.5, workerCount: 0, inventoryWarnings: 2, observedAt
    });
    Object.assign(input.workflows[0], {
      campaignId: 'campaign:1', campaign: 'demo', campaignName: 'Stale copy', campaignIcon: 'old',
      campaignReadmePath: 'old.md', campaignAiCreditAllowance: 100, campaignWorkerCount: 100,
      campaignInventoryWarnings: 100, ghAwVersion: 'v1', ghAwCurrentVersion: 'v1', ghAwVersionLabel: 'stale'
    });
    Object.assign(input.runs[0], { requestedModel: 'requested', resolvedModel: 'resolved' });
    input.graders?.push({ id: 'grader:1', workflowId: 'workflow:1', name: 'quality', sourceGraderId: 'quality-v1', observedAt, provenance });
    input.graderObservations?.push({
      id: 'grade:1', graderId: 'grader:1', runId: input.runs[0].id,
      timestamp: observedAt, resultTimestamp: observedAt, sourceGraderId: 'quality-v1', value: 0.8, observedAt, provenance
    });
    input.evals?.push({ id: 'eval:1', workflowId: 'workflow:1', name: 'correctness', sourceEvalId: 'correctness-v1', observedAt, provenance });
    input.evalObservations?.push({
      id: 'eval-result:1', evalId: 'eval:1', runId: input.runs[0].id,
      timestamp: observedAt, resultTimestamp: observedAt, sourceEvalId: 'correctness-v1',
      auditId: 'audit:eval',
      evalResult: 'YES', answer: 'YES', requestedModel: 'unused-copy', resolvedModel: 'unused-copy', observedAt, provenance
    });
    input.audits.push({
      id: 'audit:target', runId: input.runs[0].id, source: 'token-intervention-lifecycle',
      type: 'token_efficiency.intervention', targetRepo: 'octo/api', timestamp: observedAt, observedAt
    }, {
      id: 'audit:partial', runId: input.runs[0].id, source: 'token-intervention-lifecycle',
      type: 'token_efficiency.intervention', targetOrganization: 'octo', timestamp: observedAt, observedAt
    });
    await upsertCanonicalBatch(indexedDB, input);
    for (const store of /** @type {const} */ ([
      'workflows', 'graders', 'graderObservations', 'evals', 'evalObservations', 'audits'
    ])) {
      for (const record of await readCollection(indexedDB, store)) {
        for (const field of PRUNED_CANONICAL_FIELDS[store] ?? []) expect(record).not.toHaveProperty(field);
      }
    }
    const sources = /** @type {Record<string, import('../../src/presenter.js').LogicalSourceInput>} */ (
      await processDataRequest({
        operation: 'load-dashboard-query-sources', sources: {},
        sourceNames: ['workflows', 'overview-runs', 'graders', 'grader-observations', 'evals', 'eval-observations', 'audits']
      })
    );
    expect(sources.workflows.rows[0]).toMatchObject({
      'campaign-name': 'Current campaign', 'campaign-icon': 'goal',
      'campaign-readme-path': 'demo/README.md', 'campaign-aic-allowance': 1.5,
      'campaign-worker-count': 0, 'campaign-inventory-warnings': 2, 'gh-aw-version-label': 'v1 (current)'
    });
    expect(sources['overview-runs'].rows[0]).toMatchObject({ 'campaign-name': 'Current campaign' });
    expect(sources.graders.rows[0]).toMatchObject({ grader: 'quality', 'source-grader-id': 'quality-v1' });
    expect(sources['grader-observations'].rows[0]).toMatchObject({
      'source-grader-id': 'quality-v1', 'result-timestamp': observedAt, 'observed-at': observedAt
    });
    expect(sources.evals.rows[0]).toMatchObject({ eval: 'correctness', 'source-eval-id': 'correctness-v1' });
    expect(sources['eval-observations'].rows[0]).toMatchObject({
      'source-eval-id': 'correctness-v1', 'result-timestamp': observedAt, 'eval-result': 'YES',
      'audit-id': 'audit:eval',
      'requested-model': 'requested', 'resolved-model': 'resolved'
    });
    expect(sources.audits.rows.find((row) => row.id === 'audit:target')).toMatchObject({ 'target-repo': 'octo/api' });
    expect(sources.audits.rows.find((row) => row.id === 'audit:partial')).toMatchObject({ 'target-repo': null });
  });

  it('prunes published records on upsert and replacement while preserving query payloads', async () => {
    const indexedDB = new IDBFactory();
    const input = batch();
    await upsertCanonicalBatch(indexedDB, input);
    const before = await queryDatabaseSources(indexedDB, {}, ['runs', 'tools', 'issues']);
    for (const store of /** @type {const} */ (['runs', 'workflows', 'tools', 'issues'])) {
      const [record] = await readCollection(indexedDB, store);
      for (const field of PRUNED_CANONICAL_FIELDS[store]) expect(record).not.toHaveProperty(field);
    }
    expect(before.runs.rows[0]).toMatchObject({ 'aic-total': 2.5, 'run-attempt': 2, 'input-tokens': 100 });
    expect(before.tools.rows[0]).toMatchObject({ 'mcp-tool': 'search', 'request-bytes': 42, 'correlation-id': 'call-1' });
    expect(before.tools.rows[0]).not.toHaveProperty('is-skill');
    expect(before.issues.rows[0]).toMatchObject({ 'issue-state': 'closed', 'issue-state-reason': null });
    await replaceCanonicalBatch(indexedDB, input, { previousBatch: input });
    const after = await queryDatabaseSources(indexedDB, {}, ['runs', 'tools', 'issues']);
    expect(after).toEqual(before);
    expect((await readCollection(indexedDB, 'runs'))[0]).not.toHaveProperty('logsPayload');
    expect(input.runs[0]).toHaveProperty('logsPayload');
  });
});
