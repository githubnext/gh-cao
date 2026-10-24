import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import { parse } from 'yaml';
import {
  statisticalContract, validateContract, runConformance, verifyTLCResults
} from '../../scripts/run-daily-run-counts-conformance.mjs';

test('statistical conformance binds every backend to executable requirements', () => {
  const suites = validateContract(statisticalContract);
  assert.deepEqual(suites.map((suite) => suite.id), statisticalContract.requiredSuites);
  for (const id of ['sqlite', 'postgresql', 'indexeddb-worker']) {
    const suite = suites.find((suite) => suite.id === id);
    for (const requirement of ['clock-validity', 'input-budgets']) {
      assert.ok(suite.coverage[requirement], `${id} must exercise ${requirement}`);
    }
  }
  assert.match(statisticalContract.scope, /not hot-tier eviction, daily aggregates, or an online reservoir/);
  const { scripts } = JSON.parse(readFileSync(new URL('../../package.json', import.meta.url), 'utf8'));
  assert.equal(scripts['test:conformance:statistical-sampling'],
    'node scripts/run-daily-run-counts-conformance.mjs --statistical');
  const model = suites.find((suite) => suite.id === 'formal-publication');
  const config = readFileSync(new URL(`../../${model.files.find((file) => file.endsWith('.cfg'))}`, import.meta.url), 'utf8');
  for (const property of ['WholeBundles', 'Bounded', 'UnavailableIsEmpty', 'NoPartialPublication',
    'HonestWeights', 'FrozenSelection', 'SelectionBeforeFilter', 'FrozenAcrossIncremental',
    'RestartPreservesEvidence', 'ExactPublicationIndependent', 'AbortPreservesPublication']) {
    assert.ok(config.includes(property), `TLC must check ${property}`);
  }
});

test('statistical conformance rejects missing backends and mismatched executable coverage', () => {
  for (const id of statisticalContract.requiredSuites) {
    const missing = structuredClone(statisticalContract);
    missing.suites = missing.suites.filter((suite) => suite.id !== id);
    assert.throws(() => validateContract(missing), /Missing required conformance suite/);
  }
  const excluded = structuredClone(statisticalContract);
  excluded.suites.find((suite) => suite.id === 'postgresql').pattern = '^TestNothing$';
  assert.throws(() => validateContract(excluded), /Test selector excludes/);
  const invented = structuredClone(statisticalContract);
  invented.suites.find((suite) => suite.id === 'sqlite').coverage['frame-binding'] = 'TestNotImplemented';
  assert.throws(() => validateContract(invented), /Missing coverage evidence/);
});

test('statistical conformance requires real PostgreSQL and TLC before executing any suite', () => {
  assert.throws(() => runConformance({}, statisticalContract), /POSTGRES_URL is required/);
  assert.throws(() => runConformance({ POSTGRES_URL: 'not-used' }, statisticalContract), /TLA2TOOLS_JAR is required/);
});

test('formal conformance rejects parse failures, violations and incomplete state-space checks', () => {
  const complete = 'Model checking completed. No error has been found.\n7107272 states generated, 359808 distinct states found, 0 states left on queue.';
  assert.equal(verifyTLCResults(complete), 1);
  for (const output of ['', 'Error: Parsing or semantic analysis failed.', complete.replace('0 states left', '1 states left'),
    `${complete}\nError: Invariant violated`]) {
    assert.throws(() => verifyTLCResults(output), /complete state-space check/);
  }
});

test('statistical conformance runs on pull requests with real PostgreSQL, Chromium and checksum-pinned TLC', () => {
  const workflow = parse(readFileSync(new URL('../../.github/workflows/cgo.yml', import.meta.url), 'utf8'));
  const job = workflow.jobs['daily-run-counts-conformance'];
  for (const trigger of [workflow.on.pull_request, workflow.on.push]) {
    assert.ok(trigger.paths.includes('tests/contracts/statistical-sampling.json'));
    assert.ok(trigger.paths.includes('specs/tla/StatisticalSampling.*'));
  }
  assert.equal(job.if, undefined);
  assert.equal(job.services.postgres.image, 'postgres:16-alpine');
  assert.ok(job.steps.some((step) => step.run === 'npx playwright install --with-deps chromium'));
  const checker = job.steps.find((step) => step.name === 'Fetch pinned TLC model checker');
  assert.match(checker.run, /releases\/download\/v1\.7\.4\/tla2tools\.jar/);
  assert.match(checker.run, /[a-f0-9]{64}.*sha256sum --check/);
  const gate = job.steps.find((step) => step.run === 'npm run test:conformance:statistical-sampling');
  assert.match(gate.env.POSTGRES_URL, /job\.services\.postgres\.ports/);
  assert.equal(gate.env.TLA2TOOLS_JAR, checker.env.TLA2TOOLS_JAR);
});
