import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdtemp, readFile, rm, stat, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { adaptCachedGhAwJsonl } from '../../dashboard/site/src/data/adapters/gh-aw-logs.js';
import { normalize } from '../../dashboard/site/src/data/normalize/index.js';
import { behaviorEvidence, fitBehaviorModel } from '../../activity/empirical-behavior.mjs';

const scope = {
  repository: 'githubnext/gh-cao',
  since: '2026-11-01T00:00:00Z',
  until: '2026-11-03T00:00:00Z',
};
function run(id, extra = {}) {
  return {
    schema_version: 2, kind: 'run',
    run: {
      run_id: id, run_attempt: 1, organization: 'githubnext', repository: 'gh-cao',
      workflow_path: '.github/workflows/test.md', workflow_name: 'Test',
      created_at: '2026-11-01T00:00:00Z', started_at: '2026-11-01T00:00:00Z',
      updated_at: '2026-11-01T00:01:00Z', status: 'completed', conclusion: 'success',
      ...extra,
    },
  };
}
function fit(records) {
  const adapted = adaptCachedGhAwJsonl(records.map(JSON.stringify).join('\n'));
  const evidence = behaviorEvidence(normalize(adapted.observations), scope);
  return { evidence, model: fitBehaviorModel(evidence, scope) };
}

test('production normalization and query engine enforce exact scope and half-open UTC window', () => {
  const { evidence } = fit([
    run(1), run(2, { created_at: scope.until }),
    run(3, { organization: 'another' }),
    run(4, { created_at: '2026-10-31T23:59:59Z' }),
    run(5, { workflow_path: '.github/workflows/ci.yml' }),
    run(6, { created_at: '2026-11-02T23:59:59.999Z' }),
  ]);
  assert.equal(evidence.runs.length, 2);
  assert.ok(evidence.runs[0].id.endsWith(':1'));
  assert.ok(evidence.runs[1].id.endsWith(':6'));
});

test('missing retained output detail is unknown; zero requires reconciled item evidence', () => {
  const { model } = fit([
    run(1, { safe_items_count: 0 }),
    run(2),
    run(3, { safe_items_count: 2, safe_outputs: [{ type: 'noop' }] }),
    run(4, { safe_items_count: 1, safe_outputs: [
      { type: 'create_pull_request', url: 'https://github.com/githubnext/gh-cao/pull/42' },
    ] }),
    run(5, { safe_items_count: 1, safe_outputs: [{ type: 'dispatch_workflow' }] }),
    run(6, { status: 'in_progress', conclusion: null, safe_items_count: 0 }),
  ]);
  const workflow = model.workflows[0];
  assert.equal(workflow.completed, 5);
  assert.equal(workflow.nonterminal, 1);
  assert.equal(workflow.zeroCreatedIssueOrPR.n, 3);
  assert.equal(workflow.zeroCreatedIssueOrPR.missing, 2);
  assert.equal(workflow.zeroCreatedIssueOrPR.successes, 2);
  assert.equal(workflow.metrics.createdEntities.n, 3);
  assert.equal(workflow.createdEntityMix.n, 1);
  assert.equal(workflow.createdEntityMix.weights[0].value, 'pull_request');
  assert.equal(workflow.metrics.inputTokens.n, 0);
  assert.equal(workflow.metrics.inputTokens.median, null);
});

test('weights reconcile to denominators, retain measured zero and preserve joint associations', () => {
  const { model } = fit(Array.from({ length: 12 }, (_, index) => run(index + 1, {
    conclusion: index < 3 ? 'failure' : 'success',
    aic: index < 3 ? 0 : 20,
    safe_items_count: 0,
    mcp_tool_usage: { tool_calls: [] },
    token_usage_summary: { total_input_tokens: index < 3 ? 0 : 10000 },
  })));
  const workflow = model.workflows[0];
  assert.equal(workflow.status, 'observed');
  assert.equal(workflow.metrics.aicTotal.n, 12);
  assert.equal(workflow.metrics.aicTotal.median, 20);
  assert.equal(workflow.joint.cells.reduce((sum, cell) => sum + cell.count, 0), 12);
  assert.equal(workflow.joint.cells.reduce((sum, cell) => sum + cell.weight, 0), 1);
  assert.equal(workflow.joint.cells.find((cell) => cell.outcome === 'failure').aic, '0');
  assert.equal(workflow.outcomes.weights.find((cell) => cell.value === 'failure').weight, 0.25);
  assert.equal(workflow.activity.quietDayProbability, null);
  assert.equal(workflow.closure.acceptanceDelay, null);
  assert.equal(workflow.metrics.mcpToolCalls.n, 12);
  assert.equal(workflow.tools.runsWithRecords, 0);
  assert.equal(workflow.zeroCreatedIssueOrPR.probability, 1);
  assert.ok(workflow.zeroCreatedIssueOrPR.wilson95[0] < 1);
  assert.equal(workflow.outcomeConditionals.find((group) => group.outcome === 'failure').recordedAic.median, 0);
  assert.deepEqual(workflow.metrics.safeItemsCount.pmf, [{ value: 0, count: 12, weight: 1 }]);
});

test('canonical reruns converge instead of counting attempts as independent runs', () => {
  const { model } = fit([
    run(1, { conclusion: 'failure', aic: 100 }),
    run(1, { run_attempt: 2, conclusion: 'success', aic: 2 }),
  ]);
  assert.equal(model.runs, 1);
  assert.equal(model.workflows[0].metrics.aicTotal.median, 2);
  assert.equal(model.workflows[0].outcomes.weights[0].value, 'success');
  assert.equal(model.workflows[0].status, 'small-sample');
});

test('partial positive observations do not bias zero rates and closures are not inferred acceptance', () => {
  const { model } = fit([
    run(1, { safe_items_count: 0 }),
    run(2, { safe_items_count: 2, safe_outputs: [{
      type: 'create_issue', url: 'https://github.com/githubnext/gh-cao/issues/42',
      github_issue_status: { closed: true, closed_at: '2026-11-02T00:00:00Z' },
    }] }),
  ]);
  const workflow = model.workflows[0];
  assert.equal(workflow.zeroCreatedIssueOrPR.n, 1);
  assert.equal(workflow.zeroCreatedIssueOrPR.missing, 1);
  assert.equal(workflow.zeroCreatedIssueOrPR.probability, 1);
  assert.equal(workflow.outputCoverage.runsWithObservedCreation, 1);
  assert.equal(workflow.closure.observedClosed, 1);
  assert.equal(workflow.closure.delayHoursGivenObservedClosure, null);
  assert.equal(workflow.closure.acceptanceDelay, null);
});

test('empty scope and invalid windows fail instead of manufacturing fitted weights', () => {
  assert.throws(() => fit([run(1, { repository: 'elsewhere' })]), /No observed/);
  assert.throws(() => behaviorEvidence({}, { ...scope, until: scope.since }), /increasing ISO/);
  assert.throws(() => behaviorEvidence({}, { ...scope, since: '2026-11-01' }), /increasing ISO/);
});

test('offline CLI writes a reproducible private aggregate without raw payloads and refuses overwrite', async () => {
  const directory = await mkdtemp(path.join(tmpdir(), 'cao-empirical-test-'));
  try {
    const input = path.join(directory, 'input.jsonl');
    const output = path.join(directory, 'model.json');
    await writeFile(input, JSON.stringify(run(1, { safe_items_count: 0, display_title: 'PRIVATE BODY' })));
    const args = ['scripts/fit-aw-behavior.mjs', '--input', input, '--repo', scope.repository,
      '--since', scope.since, '--until', scope.until, '--output', output];
    execFileSync(process.execPath, args, { stdio: 'pipe' });
    const content = await readFile(output, 'utf8');
    const model = JSON.parse(content);
    assert.equal(model.runs, 1);
    assert.match(model.scope.inputSha256, /^[a-f0-9]{64}$/);
    assert.doesNotMatch(content, /PRIVATE BODY|github:run:|logs_path|payloadRef/);
    assert.equal((await stat(output)).mode & 0o777, 0o600);
    assert.throws(() => execFileSync(process.execPath, args, { stdio: 'pipe' }), /EEXIST/);
  } finally {
    await rm(directory, { recursive: true, force: true });
  }
});
