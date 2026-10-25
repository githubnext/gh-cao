import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { parse } from 'yaml';
import {
  contract, runConformance, validateContract, verifyBrowserResults, verifyGoResults,
  verifyNodeResults, verifyVitestResults
} from '../../scripts/run-daily-run-counts-conformance.mjs';

test('daily run-count conformance maps every implemented requirement to runnable existing tests', () => {
  const suites = validateContract(contract);
  assert.deepEqual(suites.map((suite) => suite.id), [
    'coverage-contract', 'activity-sqlite', 'activity-actions-runtime', 'indexeddb-worker', 'chromium-worker', 'native-cli', 'hosted-sqlite', 'postgresql'
  ]);
  for (const id of ['indexeddb-worker', 'postgresql']) {
    const suite = suites.find((entry) => entry.id === id);
    for (const requirement of ['classification', 'reconciliation', 'retention', 'backfill', 'availability']) {
      assert.ok(suite.coverage[requirement], `${id} must cover ${requirement}`);
    }
  }
  const worker = suites.find((suite) => suite.id === 'indexeddb-worker');
  for (const requirement of ['workflow-health', 'daily-chart', 'repository-chart']) {
    assert.ok(worker.coverage[requirement], `worker must cover ${requirement}`);
  }
  const { scripts } = JSON.parse(readFileSync(new URL('../../package.json', import.meta.url), 'utf8'));
  assert.equal(scripts['test:conformance:daily-run-counts'], 'node scripts/run-daily-run-counts-conformance.mjs');
  assert.match(contract.scope, /not the proposed three-tier architecture/);
  assert.ok(contract.deferred.includes('native weekly aggregate partitions'));
});

test('daily run-count coverage fails closed for missing evidence, excluded selectors and untested requirements', () => {
  const missing = structuredClone(contract);
  missing.suites[1].coverage.retention = 'nonexistent conformance test';
  assert.throws(() => validateContract(missing), /Missing coverage evidence/);
  const excluded = structuredClone(contract);
  excluded.suites.at(-1).pattern = '^TestNothing$';
  assert.throws(() => validateContract(excluded), /Test selector excludes/);
  const noBrowser = structuredClone(contract);
  noBrowser.suites = noBrowser.suites.filter((suite) => suite.runner !== 'playwright');
  assert.throws(() => validateContract(noBrowser), /Uncovered requirement: native-browser/);
  const uncovered = structuredClone(contract);
  uncovered.requirements.unimplemented = 'must not silently claim conformance';
  assert.throws(() => validateContract(uncovered), /Uncovered requirement/);
});

test('daily run-count conformance requires PostgreSQL before executing any suites', () => {
  assert.throws(() => runConformance({}), /POSTGRES_URL is required/);
});

test('daily run-count Go conformance rejects successful commands with skipped or absent tests', () => {
  const events = (...rows) => rows.map((row) => JSON.stringify(row)).join('\n');
  assert.throws(() => verifyGoResults(events({ Action: 'pass', Package: 'postgresx' })), /No Go conformance tests/);
  assert.throws(() => verifyGoResults(events(
    { Action: 'skip', Test: 'TestDailyCounts' }, { Action: 'pass', Package: 'postgresx' }
  )), /does not permit skipped/);
  assert.doesNotThrow(() => verifyGoResults(events(
    { Action: 'pass', Test: 'TestDailyCounts' }, { Action: 'pass', Package: 'postgresx' }
  )));
  assert.throws(() => verifyGoResults(events(
    { Action: 'pass', Test: 'TestUnrelated' }, { Action: 'pass', Package: 'postgresx' }
  ), ['TestDailyCounts']), /Required conformance test did not pass: TestDailyCounts/);
});

test('daily run-count native browser conformance rejects skipped, empty, failed or flaky reports', () => {
  const report = (stats) => JSON.stringify({ stats });
  assert.equal(verifyBrowserResults(report({ expected: 1, skipped: 0, unexpected: 0, flaky: 0 })), 1);
  for (const stats of [
    {}, { expected: 0 }, { expected: 1, skipped: 1 },
    { expected: 1, unexpected: 1 }, { expected: 1, flaky: 1 }
  ]) {
    assert.throws(() => verifyBrowserResults(report(stats)), /without skips or retries/);
  }
});

test('daily run-count Node reports reject skipped, todo, empty and omitted required tests despite successful exit', () => {
  const report = (...tests) => tests.map((data) => JSON.stringify({ type: 'test:pass', data })).join('\n');
  assert.equal(verifyNodeResults(report({ name: 'required' }), ['required']), 1);
  for (const output of ['', report({ name: 'required', skip: true }), report({ name: 'required', todo: true })]) {
    assert.throws(() => verifyNodeResults(output, ['required']), /without skips or todos/);
  }
  assert.throws(() => verifyNodeResults(report({ name: 'unrelated' }), ['required']), /Required conformance test did not pass/);
});

test('daily run-count Vitest reports reject successful skipped, todo and omitted required tests', () => {
  const report = (assertionResults) => JSON.stringify({ testResults: [{ assertionResults }] });
  const passed = { title: 'retains rows in sqlite', status: 'passed' };
  assert.equal(verifyVitestResults(report([passed]), ['retains rows in %s']), 1);
  for (const tests of [[], [{ ...passed, status: 'skipped' }], [{ ...passed, status: 'pending' }], [{ ...passed, status: 'todo' }]]) {
    assert.throws(() => verifyVitestResults(report(tests), ['retains rows in %s']), /without skips or todos/);
  }
  assert.throws(() => verifyVitestResults(report([passed]), ['required']), /Required conformance test did not pass/);
});

test('daily run-count browser reports require every declared test to pass, not just unrelated success statistics', () => {
  const report = (title, expectedStatus = 'passed') => JSON.stringify({
    stats: { expected: 1, skipped: 0, unexpected: 0, flaky: 0 },
    suites: [{ suites: [{ specs: [{ title, tests: [{ expectedStatus, results: [{ status: expectedStatus }] }] }] }] }]
  });
  assert.equal(verifyBrowserResults(report('required'), ['required']), 1);
  assert.throws(() => verifyBrowserResults(report('unrelated'), ['required']), /Required conformance test did not pass/);
  assert.throws(() => verifyBrowserResults(report('required', 'skipped'), ['required']), /Required conformance test did not pass/);
});

test('daily run-count CI keeps cross-backend checks off pull requests but available on main and explicit invocation', () => {
  const workflow = parse(readFileSync(new URL('../../.github/workflows/cgo.yml', import.meta.url), 'utf8'));
  const job = workflow.jobs['daily-run-counts-conformance'];
  assert.ok(workflow.on.pull_request.paths.includes('tests/contracts/daily-run-counts.json'));
  assert.ok(workflow.on.pull_request.paths.includes('scripts/run-daily-run-counts-conformance.mjs'));
  assert.deepEqual(workflow.on.push.branches, ['main']);
  for (const path of ['tests/contracts/daily-run-counts.json', 'scripts/run-daily-run-counts-conformance.mjs']) {
    assert.ok(workflow.on.push.paths.includes(path));
  }
  assert.ok(Object.hasOwn(workflow.on, 'workflow_dispatch'));
  assert.ok(Object.hasOwn(workflow.on, 'workflow_call'));
  assert.equal(job.services.postgres.image, 'postgres:16-alpine');
  assert.equal(job.env?.POSTGRES_URL, undefined);
  assert.ok(job.steps.some((step) => step.run === 'npx playwright install --with-deps chromium'));
  assert.ok(job.steps.some((step) => step.run === 'docker pull node:24-bookworm'));
  const conformance = job.steps.find((step) => step.run === 'npm run test:conformance:daily-run-counts');
  assert.ok(conformance);
  assert.match(conformance.env.POSTGRES_URL, /job\.services\.postgres\.ports/);
  assert.equal(job.if, "github.event_name != 'pull_request'");
});
