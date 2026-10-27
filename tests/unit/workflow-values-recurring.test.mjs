import assert from 'node:assert/strict';
import { mkdtemp, mkdir, readFile, readdir, rm, symlink, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { tmpdir } from 'node:os';
import test from 'node:test';
import { assertValueHistory, confinedCollector, runWorkflowValues, workflowValueRegistrations } from '../../activity/workflow-values.mjs';
import { controlSettings, parsePolicy } from '../../.github/workflows/shared/policy.mjs';
import { activityCollectionPlan } from '../../activity/authentication.mjs';
import { adaptCachedGhAwJsonl } from '../../dashboard/site/src/data/adapters/gh-aw-logs.js';
import { normalize } from '../../dashboard/site/src/data/normalize/index.js';
import { localValuePlan, retainLocalValues } from '../../activity/local-values.mjs';

async function collection(options) {
  return JSON.parse(await readFile(options.output.replace(/\.jsonl$/, '-collections.jsonl'), 'utf8')).workflow_value_collection;
}

const entry = { repository: 'org/repo', workflow: 'daily-review', collector: 'collectors/value.mjs' };
function policy() {
  return { version: 1, 'gh-aw-version': 'v0.92.5', 'control-plane': {
    scope: { 'allowed-owners': ['org'], 'allowed-repositories': ['org/repo'] },
    activity: { repositories: ['org/repo'] }, campaigns: { activity: { 'workflow-values': [{ ...entry }] } },
  } };
}
function packet(observedAt = '2026-10-20T12:00:00.000Z') {
  return { schema_version: 2, kind: 'operational_value_evidence', operational_value_evidence: {
    schemaVersion: 1, repository: 'org/repo', workflow: 'daily-review', sourcePath: '.github/workflows/daily-review.md',
    id: 'operational-value-evidence:org%2Frepo%3Adaily-review%3Alines%3Av1',
    observedAt, windowStart: '2026-10-01T00:00:00.000Z', windowEnd: '2026-10-20T00:00:00.000Z',
    contractDigest: 'a'.repeat(64), facts: [
      { 'fact-kind': 'contract', 'fact-id': 'contract', goal: 'Remove waste', acceptance: 'Accepted deletion',
        'workflow-name': 'Review', 'metric-id': 'lines', 'metric-name': 'Lines', 'metric-unit': 'lines',
        'metric-direction': 'increase', 'metric-version': 'v1', 'metric-formula': 'accepted removed lines',
        'measurement-basis': 'workflow-contribution', 'cadence-days': 1,
        'outputs-available': false, 'repository-state-available': false, 'checks-available': false },
      { 'fact-kind': 'coverage', 'fact-id': 'window', at: '2026-10-01T00:00:00.000Z',
        'end-at': '2026-10-20T00:00:00.000Z', 'coverage-status': 'complete' },
    ],
  } };
}

async function fixture(t, body) {
  const root = await mkdtemp(path.join(tmpdir(), 'cao-workflow-values-test-'));
  t.after(() => rm(root, { recursive: true, force: true }));
  await mkdir(path.join(root, 'collectors'));
  const policyPath = path.join(root, 'cao.json');
  await writeFile(policyPath, JSON.stringify(policy()));
  const definition = { schemaVersion: 3, repository: entry.repository, slug: entry.workflow,
    sourcePath: '.github/workflows/daily-review.md', adoption: { adoptedAt: '2026-10-01T00:00:00.000Z' },
    evidence: { repositories: ['org/repo'], window: { cadenceDays: 1, maturationDays: 0 } } };
  await writeFile(path.join(root, entry.collector), `
import {readFile,writeFile} from 'node:fs/promises';
import path from 'node:path';
export const definition=${JSON.stringify(definition)};
export async function collectValueEvidence(request) {
 await writeFile(path.join(request.workspace.directory,'proof.json'),JSON.stringify({original:true}));
 ${body ?? `return ${JSON.stringify(packet())};`}
}
`);
  return { root, policy: policyPath, database: path.join(root, 'activity.sqlite'),
    repositories: ['org/repo'], workspace: path.join(root, 'proofs'), output: path.join(root, 'values.jsonl'),
    observedAt: '2026-10-20T12:00:00.000Z', timeout: 5000 };
}

test('reviewed opt-ins intersect both effective Activity enrollment and explicit runtime repository scope', () => {
  const document = parsePolicy(JSON.stringify(policy()));
  assert.deepEqual(workflowValueRegistrations(document, ['ORG/REPO']), [entry]);
  assert.deepEqual(workflowValueRegistrations(document, ['org/other']), []);
  document['control-plane'].activity.repositories = ['org/other'];
  assert.deepEqual(workflowValueRegistrations(document, ['org/repo']), []);
  for (const collector of ['../escape.mjs', '/absolute.mjs', 'a//b.mjs', 'https://host/file.mjs']) {
    const invalid = policy();
    invalid['control-plane'].campaigns.activity['workflow-values'][0] = { ...entry, collector };
    assert.throws(() => parsePolicy(JSON.stringify(invalid)));
  }
  for (const evidence of [
    { path: '../evidence.jsonl', sha256: 'a'.repeat(64) },
    { path: '.github/value-evidence/evidence.jsonl', sha256: 'invalid' },
    { path: 'https://host/evidence.jsonl', sha256: 'a'.repeat(64) },
  ]) {
    const invalid = policy();
    invalid['control-plane'].campaigns.activity['workflow-values'][0] = { ...entry, evidence };
    assert.throws(() => parsePolicy(JSON.stringify(invalid)));
  }
  for (const requests of [0, 1001, 1.5]) {
    const invalid = policy();
    invalid['control-plane'].campaigns.activity['workflow-values'][0] = { ...entry, 'api-requests': requests };
    assert.throws(() => parsePolicy(JSON.stringify(invalid)));
  }
  const withoutCollector = policy();
  withoutCollector['control-plane'].campaigns.activity['workflow-values'][0] = {
    repository: entry.repository, workflow: entry.workflow, 'api-requests': 1,
  };
  assert.throws(() => parsePolicy(JSON.stringify(withoutCollector)));
  const duplicate = policy();
  duplicate['control-plane'].campaigns.activity['workflow-values'] = [entry, { ...entry, repository: 'ORG/REPO' }];
  assert.throws(() => parsePolicy(JSON.stringify(duplicate)), /duplicate/);
});

test('dogfood File Diet registration pins existing effective Activity scope without adding targets', async () => {
  const document = parsePolicy(await readFile(new URL('../../.github/workflows/cao.json', import.meta.url), 'utf8'));
  const control = document['control-plane'];
  assert.deepEqual(control.activity.repositories, control.scope['allowed-repositories']);
  const before = structuredClone(document);
  delete before['control-plane'].activity;
  const auth = { authMode: 'pat', controlRepository: 'githubnext/gh-cao',
    patRepositoryMap: JSON.stringify(Object.fromEntries(control.activity.repositories.map(repository =>
      [repository, `GH_AW_GITHUB_READ_PAT_${repository.split('/')[0].toUpperCase()}`]))) };
  assert.deepEqual(activityCollectionPlan(controlSettings(document, auth.controlRepository), auth),
    activityCollectionPlan(controlSettings(before, auth.controlRepository), auth));
  assert.deepEqual(workflowValueRegistrations(document, ['github/gh-aw']), [{
    'api-requests': 700,
    repository: 'github/gh-aw', workflow: 'daily-file-diet', collector: '.github/value-collectors/daily-file-diet.mjs',
    evidence: {
      path: '.github/value-evidence/daily-file-diet.jsonl.gz',
      sha256: '9afaf3b49a02c1eda0c634016bae98bf0be0a5c1b63f1ae6c6353b151301d19e',
    },
  }]);
  assert.equal(control.campaigns.dashboard.deploy, false);
});

test('reviewed remote collection requires full post-Activity capacity before invocation', async t => {
  const options = await fixture(t);
  const remote = policy();
  remote['control-plane'].campaigns.activity['workflow-values'][0]['api-requests'] = 3;
  await writeFile(options.policy, JSON.stringify(remote));
  options.apiBudget = path.join(options.root, 'api-budget.json');
  options.apiReserve = 1000;
  options.apiExecute = () => ({ status: 0, stdout: JSON.stringify({ resources: { core: {
    limit: 5000, remaining: 1002, used: 3998, reset: 1790000000,
  } } }), stderr: '' });
  let result = await runWorkflowValues(options);
  assert.equal(result.deferred, 1);
  assert.equal(result.collected, 0);
  assert.equal((await collection(options)).reasonCode, 'api-budget');
  assert.equal((await collection(options)).lastAttemptAt, undefined);
  options.apiExecute = () => ({ status: 0, stdout: JSON.stringify({ resources: { core: {
    limit: 5000, remaining: 1003, used: 3997, reset: 1790000000,
  } } }), stderr: '' });
  result = await runWorkflowValues(options);
  assert.equal(result.collected, 1);
  assert.equal(JSON.parse(await readFile(options.apiBudget, 'utf8')).granted, 3);
});

test('collector request exhaustion is counted before execution and reported explicitly', async t => {
  const budgetModule = new URL('../../activity/github-api-budget.mjs', import.meta.url).href;
  const options = await fixture(t, `
 const {consumeGithubApiBudget}=await import(${JSON.stringify(budgetModule)});
 consumeGithubApiBudget(process.env.CAO_GITHUB_API_BUDGET_FILE);
 consumeGithubApiBudget(process.env.CAO_GITHUB_API_BUDGET_FILE);
 return ${JSON.stringify(packet())};
`);
  const remote = policy();
  remote['control-plane'].campaigns.activity['workflow-values'][0]['api-requests'] = 1;
  await writeFile(options.policy, JSON.stringify(remote));
  options.apiBudget = path.join(options.root, 'api-budget.json');
  options.apiReserve = 1000;
  options.apiExecute = () => ({ status: 0, stdout: JSON.stringify({ resources: { core: {
    limit: 5000, remaining: 4000, used: 1000, reset: 1790000000,
  } } }), stderr: '' });
  const result = await runWorkflowValues(options);
  assert.equal(result.failed, 1);
  assert.equal((await collection(options)).reasonCode, 'api-budget');
  assert.equal(JSON.parse(await readFile(options.apiBudget, 'utf8')).consumed, 1);
  await assert.rejects(readFile(options.output, 'utf8'), /ENOENT/);
});

test('recurring producer emits original typed facts, retains proof, skips satisfied cadence and preserves disabled history', async (t) => {
  const options = await fixture(t);
  assert.equal((await runWorkflowValues(options)).collected, 1);
  const original = await readFile(options.output, 'utf8');
  assert.deepEqual(JSON.parse(original), packet());
  const canonical = normalize(adaptCachedGhAwJsonl(original).observations);
  assert.equal(canonical.operationalValueEvidence.length, 1);
  assert.equal((await runWorkflowValues(options)).skipped, 1);
  assert.equal(await readFile(options.output, 'utf8'), original);
  const [directory] = await readdir(options.workspace);
  assert.deepEqual(await readdir(path.join(options.workspace, directory)), ['proof.json']);
  const revoked = policy();
  revoked['control-plane'].campaigns.activity['workflow-values'] = [{ ...entry, enabled: false }];
  await writeFile(options.policy, JSON.stringify(revoked));
  assert.equal((await runWorkflowValues(options)).collected, 0);
  assert.equal(await readFile(options.output, 'utf8'), original);
});

test('partial acquisition, changed identity and timeout retain accepted evidence without fresh clocks or zero', async (t) => {
  for (const body of [
    `const e=${JSON.stringify(packet())}; e.operational_value_evidence.facts[1]['coverage-status']='partial'; return e;`,
    `const e=${JSON.stringify(packet())}; e.operational_value_evidence.repository='org/other'; return e;`,
    'await new Promise(resolve=>setTimeout(resolve,5000));',
  ]) {
    const options = await fixture(t, body);
    options.timeout = 100;
    const original = `${JSON.stringify(packet('2026-10-19T12:00:00.000Z'))}\n`;
    await writeFile(options.output, original);
    // Older coverage does not satisfy the current cadence.
    const previous = JSON.parse(original);
    previous.operational_value_evidence.windowEnd = '2026-10-19T00:00:00.000Z';
    previous.operational_value_evidence.facts[1]['end-at'] = '2026-10-19T00:00:00.000Z';
    const retained = `${JSON.stringify(previous)}\n`;
    await writeFile(options.output, retained);
    const result = await runWorkflowValues(options);
    assert.equal(result.collected, 0);
    assert.equal(result.warnings.length, 1);
    assert.equal(await readFile(options.output, 'utf8'), retained);
  }
});

test('local collector path rejects symlinks and immutable accepted facts cannot be rewritten', async (t) => {
  const options = await fixture(t);
  await symlink(path.join(options.root, 'collectors'), path.join(options.root, 'link'));
  await assert.rejects(confinedCollector(options.root, 'link/value.mjs'), /symlink/);
  const old = packet().operational_value_evidence;
  const incoming = structuredClone(old);
  incoming.observedAt = '2026-10-21T12:00:00.000Z';
  incoming.facts[0]['metric-formula'] = 'redefined';
  assert.throws(() => assertValueHistory(old, incoming), /Immutable/);
});

test('fresh capture can finish after its requested cutoff but cannot claim future observation or coverage', async (t) => {
  const capture = packet(new Date(Date.now() - 1000).toISOString());
  const options = await fixture(t, `return ${JSON.stringify(capture)};`);
  const result = await runWorkflowValues(options);
  assert.equal(result.collected, 1);
  assert.deepEqual(result.warnings, []);
  for (const change of [
    value => { value.operational_value_evidence.observedAt = new Date(Date.now() + 3600000).toISOString(); },
    value => {
      value.operational_value_evidence.windowEnd = '2026-10-21T00:00:00.000Z';
      value.operational_value_evidence.facts[1]['end-at'] = '2026-10-21T00:00:00.000Z';
    },
  ]) {
    const invalid = structuredClone(capture);
    change(invalid);
    const rejected = await fixture(t, `return ${JSON.stringify(invalid)};`);
    const result = await runWorkflowValues(rejected);
    assert.equal(result.collected, 0);
    assert.equal(result.warnings.length, 1);
  }
});

test('a due capture reuses workflow proofs, preserves original facts, and advances only to the acquired cutoff', async (t) => {
  const options = await fixture(t, `
 const file=path.join(request.workspace.directory,'count.json');
 let count=0; try {count=JSON.parse(await readFile(file,'utf8'));} catch {}
 await writeFile(file,JSON.stringify(count+1));
 const e=${JSON.stringify(packet())};
 if(request.previous){
   if(count!==1) throw new Error('evidence workspace was not reused');
   e.operational_value_evidence=structuredClone(request.previous);
   e.operational_value_evidence.observedAt='2026-10-21T12:00:00.000Z';
   e.operational_value_evidence.windowEnd='2026-10-21T00:00:00.000Z';
   e.operational_value_evidence.facts[1]['end-at']='2026-10-21T00:00:00.000Z';
 }
 return e;
`);
  assert.equal((await runWorkflowValues(options)).collected, 1);
  options.observedAt = '2026-10-21T12:00:00.000Z';
  const result = await runWorkflowValues(options);
  assert.equal(result.collected, 1);
  assert.deepEqual(result.warnings, []);
  const lines = (await readFile(options.output, 'utf8')).trim().split('\n');
  assert.equal(lines.length, 1, 'superseded cumulative packets must not create quadratic retained evidence');
  const evidence = JSON.parse(lines[0]).operational_value_evidence;
  assert.equal(evidence.observedAt, options.observedAt);
  assert.equal(evidence.windowStart, packet().operational_value_evidence.windowStart);
  assert.deepEqual(evidence.facts[0], packet().operational_value_evidence.facts[0]);
});

test('first-capture deferrals publish enabled registration without importing a collector or reserving shared quota', async (t) => {
  const options = await fixture(t);
  await writeFile(path.join(options.root, entry.collector), "throw new Error('must not import');");
  for (const reason of ['api-budget', 'shared-budget-unavailable', 'fast-mode', 'public-only']) {
    const result = await runWorkflowValues({ ...options, defer: reason });
    assert.equal(result.deferred, 1);
    assert.equal(result.failed, 0);
    const fact = await collection(options);
    assert.equal(fact.registration, 'enabled');
    assert.equal(fact.state, 'deferred');
    assert.equal(fact.reasonCode, reason);
    assert.equal(fact.lastAttemptAt, undefined);
    await assert.rejects(readFile(options.output), { code: 'ENOENT' });
    await assert.rejects(readdir(options.workspace), { code: 'ENOENT' });
    const batch = normalize(adaptCachedGhAwJsonl(`${JSON.stringify({
      schema_version: 2, kind: 'workflow_value_collection', workflow_value_collection: fact,
    })}\n`).observations);
    assert.equal(batch.workflowValueCollections.length, 1);
    assert.equal(batch.workflows[0].state, 'unknown');
    assert.equal(batch.runs.length, 0);
  }
});

test('a newer failure, deferral, disable and removal retain capture, proof and the actual last attempt separately', async (t) => {
  const options = await fixture(t);
  let clock = '2026-10-23T10:00:00.000Z';
  options.now = () => clock;
  await runWorkflowValues(options);
  const original = await readFile(options.output, 'utf8');
  const [proof] = await readdir(options.workspace);
  const proofPath = path.join(options.workspace, proof, 'proof.json');
  const originalProof = await readFile(proofPath, 'utf8');
  options.observedAt = '2026-10-21T12:00:00.000Z';
  options.timeout = 50;
  await writeFile(path.join(options.root, entry.collector), 'await new Promise(resolve => setTimeout(resolve, 10000));');
  clock = '2026-10-23T11:00:00.000Z';
  await runWorkflowValues(options);
  const failed = await collection(options);
  assert.equal(failed.reasonCode, 'timeout');
  assert.equal(failed.lastAttemptOutcome, 'failed');
  assert.equal(failed.lastAttemptAt, clock);
  clock = '2026-10-23T12:00:00.000Z';
  await runWorkflowValues({ ...options, defer: 'api-budget' });
  const deferred = await collection(options);
  assert.equal(deferred.observedAt, clock);
  assert.equal(deferred.lastAttemptAt, failed.lastAttemptAt);
  assert.equal(deferred.lastAttemptOutcome, 'failed');
  const disabled = policy();
  disabled['control-plane'].campaigns.activity['workflow-values'] = [{ ...entry, enabled: false }];
  await writeFile(options.policy, JSON.stringify(disabled));
  clock = '2026-10-23T13:00:00.000Z';
  await runWorkflowValues({ ...options, defer: 'api-budget' });
  assert.equal((await collection(options)).registration, 'disabled');
  assert.equal((await collection(options)).state, 'disabled');
  disabled['control-plane'].campaigns.activity['workflow-values'] = [];
  await writeFile(options.policy, JSON.stringify(disabled));
  clock = '2026-10-23T14:00:00.000Z';
  await runWorkflowValues({ ...options, defer: 'api-budget' });
  assert.equal((await collection(options)).state, 'unconfigured');
  assert.equal((await collection(options)).collector, undefined);
  assert.equal((await collection(options)).lastAttemptAt, failed.lastAttemptAt);
  assert.equal(await readFile(options.output, 'utf8'), original);
  assert.equal(await readFile(proofPath, 'utf8'), originalProof);
});

test('missing inventory availability does not hide a reviewed registration and local eligibility respects every existing gate', async (t) => {
  const options = await fixture(t);
  const snapshot = path.join(options.root, 'snapshot');
  const inventory = path.join(options.root, 'inventory.json');
  await writeFile(inventory, JSON.stringify({
    repositories: { rows: [], metadata: { availability: 'unavailable' } },
    workflows: { rows: [], metadata: { availability: 'unavailable' } },
  }));
  for (const reason of ['shared-budget-unavailable', 'api-budget', 'fast-mode', 'public-only']) {
    const localRequest = path.join(snapshot, 'gh-aw-logs-shards', 'workflow-value-local.json');
    await runWorkflowValues({ ...options, inventory, defer: reason, localRequest, snapshot: '1-1' });
    assert.equal((await collection(options)).state, 'deferred');
    const plan = await localValuePlan({ policy: options.policy, snapshot, snapshotKey: '1-1' });
    assert.deepEqual(plan.repositories, reason === 'shared-budget-unavailable' ? ['org/repo'] : []);
    assert.deepEqual((await localValuePlan({ policy: options.policy, snapshot, snapshotKey: '1-2' })).repositories, []);
  }
});

test('a bounded batch defers without an invocation and withdrawn scope is not republished', async (t) => {
  const options = await fixture(t);
  await runWorkflowValues({ ...options, batchTimeout: 0 });
  assert.equal((await collection(options)).reasonCode, 'batch-budget');
  assert.equal((await collection(options)).lastAttemptAt, undefined);
  await writeFile(options.output, `${JSON.stringify(packet())}\n`);
  await runWorkflowValues({ ...options, repositories: ['org/other'], defer: 'shared-budget-unavailable' });
  assert.equal(await readFile(options.output, 'utf8'), '');
  assert.equal(await readFile(options.output.replace(/\.jsonl$/, '-collections.jsonl'), 'utf8'), '');
});

test('local scope keeps the explicit allowed-repository fallback without widening rich collector opt-in', async (t) => {
  const options = await fixture(t);
  const document = policy();
  delete document['control-plane'].activity;
  document['control-plane'].scope = { 'allowed-repositories': ['org/repo'] };
  await writeFile(options.policy, JSON.stringify(document));
  const snapshot = path.join(options.root, 'snapshot');
  await runWorkflowValues({
    ...options, defer: 'shared-budget-unavailable',
    localRequest: path.join(snapshot, 'gh-aw-logs-shards', 'workflow-value-local.json'), snapshot: '1-1',
  });
  assert.deepEqual((await localValuePlan({ policy: options.policy, snapshot, snapshotKey: '1-1' })).repositories, ['org/repo']);
  assert.equal(await readFile(options.output.replace(/\.jsonl$/, '-collections.jsonl'), 'utf8'), '');
  await assert.rejects(readdir(options.workspace), { code: 'ENOENT' });
});

test('an explicit disable without a collector remains disabled rather than unconfigured', async (t) => {
  const options = await fixture(t);
  const document = policy();
  document['control-plane'].campaigns.activity['workflow-values'] = [{
    repository: entry.repository, workflow: entry.workflow, enabled: false,
  }];
  await writeFile(options.policy, JSON.stringify(document));
  await runWorkflowValues({ ...options, defer: 'shared-budget-unavailable' });
  const fact = await collection(options);
  assert.equal(fact.state, 'disabled');
  assert.equal(fact.registration, 'disabled');
  assert.equal(fact.collector, undefined);
});

test('completed local imports validate the canonical envelope, retain known zero and reject invalid optional replacements', async (t) => {
  const options = await fixture(t);
  const input = path.join(options.root, 'incoming.jsonl');
  const output = path.join(options.root, 'local.jsonl');
  const original = { schema_version: 2, kind: 'operational_value', operational_value: {
    repository: 'org/repo', campaign: 'example', value_id: 'daily-review.count', value: 0,
    timestamp: '2026-10-20T12:00:00.000Z',
  } };
  await writeFile(output, JSON.stringify(original) + '\n');
  await writeFile(input, JSON.stringify({ ...original, operational_value: { ...original.operational_value, value: null } }));
  await retainLocalValues({ policy: options.policy, input, output });
  assert.deepEqual(JSON.parse(await readFile(output, 'utf8')), original);
  const withdrawn = structuredClone(original);
  withdrawn.operational_value.repository = 'outside/scope';
  await writeFile(input, JSON.stringify(withdrawn));
  await retainLocalValues({ policy: options.policy, input, output });
  assert.deepEqual(JSON.parse(await readFile(output, 'utf8')), original);
  const conflicting = structuredClone(original);
  conflicting.operational_value.value = 7;
  const later = structuredClone(original);
  later.operational_value.timestamp = '2026-10-21T12:00:00.000Z';
  await writeFile(input, [later, conflicting].map(record => JSON.stringify(record)).join('\n'));
  await retainLocalValues({ policy: options.policy, input, output });
  assert.deepEqual(JSON.parse(await readFile(output, 'utf8')), original);
  await writeFile(input, [original, later, original].map(record => JSON.stringify(record)).join('\n'));
  await retainLocalValues({ policy: options.policy, input, output });
  assert.deepEqual((await readFile(output, 'utf8')).trim().split('\n').map(line => JSON.parse(line)), [original, later]);
});
