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
import { workflowValueApiHeadroom } from '../../activity/workflow-value-status.mjs';
import {
  compileValueAcquisitionPlan, normalizeValueAcquisition, valueAcquisitionDigest,
  valueAcquisitionRequirementId,
} from '../../activity/value-acquisition.mjs';

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
  for (const acquisition of [
    { requirements: [] },
    { requirements: [{ source: 'github', pool: 'local', primitive: 'github.repository.v1', scope: 'repository', cache: 'invocation' }] },
    { requirements: [{ source: 'github', pool: 'core', primitive: 'UPPER', scope: 'repository', cache: 'invocation' }] },
  ]) {
    const invalid = policy();
    invalid['control-plane'].campaigns.activity['workflow-values'][0] = { ...entry, acquisition };
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

test('acquisition plans canonicalize and union shared repository requirements', () => {
  const shared = {
    source: 'github', pool: 'core', primitive: 'github.repository-metadata.v1',
    scope: 'repository', cache: 'invocation',
  };
  const acquisition = normalizeValueAcquisition({ requirements: [
    { source: 'git', pool: 'git', primitive: 'git.commit-inventory.v1', scope: 'source', cache: 'immutable' },
    shared,
  ] });
  assert.deepEqual(acquisition.requirements.map(({ primitive }) => primitive), [
    'git.commit-inventory.v1', 'github.repository-metadata.v1',
  ]);
  assert.match(valueAcquisitionDigest(acquisition), /^[a-f0-9]{64}$/);
  assert.equal(valueAcquisitionRequirementId(shared),
    'f2ef7a7b2549d6403c6be2b24bdb1cd38bc71c94589f1bf8c09ebc0c49c7acb5');
  assert.deepEqual(compileValueAcquisitionPlan([
    { repository: 'ORG/REPO', workflow: 'one', acquisition },
    { repository: 'org/repo', workflow: 'two', acquisition: { requirements: [shared] } },
  ]), [
    {
      repository: 'org/repo', source: 'git', pool: 'git', primitive: 'git.commit-inventory.v1',
      scope: 'source', cache: 'immutable', workflows: ['one'],
    },
    { repository: 'org/repo', ...shared, workflows: ['one', 'two'] },
  ]);
});

test('collector acquisition requirements must match the reviewed registration exactly', async t => {
  const options = await fixture(t);
  const document = policy();
  document['control-plane'].campaigns.activity['workflow-values'][0].acquisition = {
    requirements: [{
      source: 'github', pool: 'core', primitive: 'github.repository-metadata.v1',
      scope: 'repository', cache: 'invocation',
    }],
  };
  await writeFile(options.policy, JSON.stringify(document));
  const result = await runWorkflowValues(options);
  assert.equal(result.collected, 0);
  assert.equal(result.failed, 1);
  assert.match(result.warnings[0], /previous evidence retained/);
  assert.match(result.warnings[0], /definition or recurring evidence interface/);
});

test('multi-metric collectors publish atomically and retain every accepted packet on failure', async t => {
  const options = await fixture(t);
  const second = packet();
  second.operational_value_evidence.id = second.operational_value_evidence.id.replace('lines', 'files');
  second.operational_value_evidence.facts[0]['metric-id'] = 'files';
  const envelopes = [packet(), second];
  const filename = path.join(options.root, entry.collector);
  const original = await readFile(filename, 'utf8');
  await writeFile(filename, original.replace('collectValueEvidence(request)', 'collectValueEvidenceBatch(request)')
    .replace(`return ${JSON.stringify(packet())};`, `return ${JSON.stringify(envelopes)};`));
  assert.equal((await runWorkflowValues(options)).collected, 1);
  const retained = await readFile(options.output, 'utf8');
  assert.equal(retained.trim().split('\n').length, 2);
  options.observedAt = '2026-10-21T12:00:00.000Z';
  const later = structuredClone(envelopes);
  for (const envelope of later) envelope.operational_value_evidence.observedAt = options.observedAt;
  for (const output of [
    later.slice(0, 1), [later[0], later[0]],
    [later[0], { ...later[1], kind: 'invalid' }],
  ]) {
    await writeFile(filename, original.replace('collectValueEvidence(request)', 'collectValueEvidenceBatch(request)')
      .replace(`return ${JSON.stringify(packet())};`, `return ${JSON.stringify(output)};`));
    assert.equal((await runWorkflowValues(options)).failed, 1);
    assert.equal(await readFile(options.output, 'utf8'), retained);
  }
  await writeFile(filename, original.replace('collectValueEvidence(request)', 'collectValueEvidenceBatch(request)')
    .replace(`return ${JSON.stringify(packet())};`,
      `if(request.previous.length!==2)throw new Error('incomplete previous batch');return null;`));
  assert.equal((await runWorkflowValues(options)).skipped, 1);
  assert.equal(await readFile(options.output, 'utf8'), retained);
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
  const registrations = workflowValueRegistrations(document, ['github/gh-aw'])
    .filter(registration => registration.workflow === 'daily-file-diet');
  assert.equal(registrations.length, 1);
  assert.deepEqual({ ...registrations[0], acquisition: undefined }, {
    'api-requests': 700,
    repository: 'github/gh-aw', workflow: 'daily-file-diet', collector: '.github/value-collectors/daily-file-diet.mjs',
    evidence: {
      path: '.github/value-evidence/daily-file-diet.jsonl.gz',
      sha256: '9afaf3b49a02c1eda0c634016bae98bf0be0a5c1b63f1ae6c6353b151301d19e',
    },
    acquisition: undefined,
  });
  assert.equal(registrations[0].acquisition.requirements.length, 11);
  assert.deepEqual(registrations[0].acquisition.requirements
    .filter(requirement => requirement.source === 'github')
    .map(requirement => requirement.primitive).toSorted(), [
    'github.actions-artifact-receipts.v1',
    'github.actions-run-provenance.v1',
    'github.issue-pull-relationships.v1',
    'github.issue-search.v1',
    'github.issue-timeline.v1',
    'github.pull-metadata-files.v1',
    'github.repository-commit-history.v1',
    'github.repository-metadata.v1',
    'github.workflow-source-history.v1',
  ]);
  assert.equal(control.campaigns.dashboard.deploy, false);
  assert.equal(workflowValueApiHeadroom(document, ['github/gh-aw']), 811);
  assert.equal(workflowValueApiHeadroom(document, ['githubnext/gh-cao']), 0);
  const disabled = structuredClone(document);
  disabled['control-plane'].campaigns.activity['workflow-values'][0].enabled = false;
  assert.equal(workflowValueApiHeadroom(disabled, ['github/gh-aw']), 111);
});

test('reviewed remote collection uses reserved capacity after ordinary Activity reaches its floor', async t => {
  const budgetModule = new URL('../../activity/github-api-budget.mjs', import.meta.url).href;
  const options = await fixture(t, `
 const {consumeGithubApiBudget}=await import(${JSON.stringify(budgetModule)});
 consumeGithubApiBudget(process.env.CAO_GITHUB_API_BUDGET_FILE);
 consumeGithubApiBudget(process.env.CAO_GITHUB_API_BUDGET_FILE);
 consumeGithubApiBudget(process.env.CAO_GITHUB_API_BUDGET_FILE);
 return ${JSON.stringify(packet())};
`);
  options.defer = 'api-budget';
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
  assert.equal(JSON.parse(await readFile(options.apiBudget, 'utf8')).consumed, 3);
  assert.equal(result.cost.apiRequests, 3);
  assert.equal(result.cost.capacityChecks, 1);
  const [directory] = await readdir(options.workspace);
  const history = JSON.parse(await readFile(path.join(options.workspace, directory, 'collection-cost.json')));
  assert.equal(history.totals.apiRequests, 3);
  assert.equal(history.totals.capacityChecks, 1);
  assert.equal(history.totals.attempts, 1);
  assert.ok(history.recent[0].wallMs >= 0);
});

test('API-limited Activity still defers collectors without a reviewed remote grant', async t => {
  const options = await fixture(t);
  options.defer = 'api-budget';
  const result = await runWorkflowValues(options);
  assert.equal(result.deferred, 1);
  assert.equal(result.collected, 0);
  assert.equal((await collection(options)).reasonCode, 'api-budget');
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
  assert.equal(result.cost.apiRequests, 1);
  const [directory] = await readdir(options.workspace);
  const history = JSON.parse(await readFile(path.join(options.workspace, directory, 'collection-cost.json')));
  assert.equal(history.totals.apiRequests, 1);
  assert.equal(history.recent[0].exitStatus, 1);
  await assert.rejects(readFile(options.output, 'utf8'), /ENOENT/);
});

test('collector failures retain evidence and expose only a bounded safe diagnostic', async t => {
  const options = await fixture(t, `
 const error=new Error('secret command output must not escape');
 error.code='ETIMEDOUT';
 error.killed=true;
 error.signal='SIGTERM';
 throw error;
`);
  const previous = packet('2026-10-19T12:00:00.000Z');
  previous.operational_value_evidence.windowEnd = '2026-10-19T00:00:00.000Z';
  previous.operational_value_evidence.facts[1]['end-at'] = '2026-10-19T00:00:00.000Z';
  const retained = `${JSON.stringify(previous)}\n`;
  await writeFile(options.output, retained);
  const result = await runWorkflowValues(options);
  assert.equal(result.failed, 1);
  assert.equal((await collection(options)).reasonCode, 'timeout');
  assert.deepEqual(result.warnings, [
    'org/repo/daily-review: collector command timed out (SIGTERM); previous evidence retained',
  ]);
  assert.doesNotMatch(JSON.stringify(result), /secret command output/);
  assert.equal(await readFile(options.output, 'utf8'), retained);
});

test('GitHub acquisition failures report transport and authorization without command or response contents', async t => {
  const options = await fixture(t, `
 const error=new Error('secret command and query');
 error.stderr='gh: Resource protected by organization SAML enforcement. (HTTP 403) secret response https://private.example/token';
 error.githubEvidenceTransport='search';
 throw error;
`);
  const previous = packet('2026-10-19T12:00:00.000Z');
  previous.operational_value_evidence.windowEnd = '2026-10-19T00:00:00.000Z';
  previous.operational_value_evidence.facts[1]['end-at'] = '2026-10-19T00:00:00.000Z';
  const retained = `${JSON.stringify(previous)}\n`;
  await writeFile(options.output, retained);
  const result = await runWorkflowValues(options);
  assert.equal(result.failed, 1);
  assert.deepEqual(result.warnings, [
    'org/repo/daily-review: GitHub Search evidence request failed (HTTP 403); organization SSO authorization required; previous evidence retained',
  ]);
  assert.doesNotMatch(JSON.stringify(result), /secret|private\.example|token/);
  assert.equal(await readFile(options.output, 'utf8'), retained);
  assert.equal((await collection(options)).reasonCode, 'collector-failed');
});

test('confirmed GitHub secondary rate limits remain API-budget failures rather than authentication guesses', async t => {
  const options = await fixture(t, `
 const error=new Error('private command');
 error.stderr='gh: You have exceeded a secondary rate limit. (HTTP 403) private details';
 error.githubEvidenceTransport='search';
 throw error;
`);
  const result = await runWorkflowValues(options);
  assert.equal(result.failed, 1);
  assert.equal((await collection(options)).reasonCode, 'api-budget');
  assert.match(result.warnings[0], /GitHub Search evidence request failed \(HTTP 403\); API rate limit exhausted/);
  assert.doesNotMatch(JSON.stringify(result), /private|SSO/);
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
  assert.deepEqual(await readdir(path.join(options.workspace, directory)), ['collection-cost.json', 'proof.json']);
  const cost = JSON.parse(await readFile(path.join(options.workspace, directory, 'collection-cost.json')));
  assert.equal(cost.totals.attempts, 2);
  assert.equal(cost.totals.unmeteredAttempts, 2);
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
