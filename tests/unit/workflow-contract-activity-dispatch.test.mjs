import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdirSync, mkdtempSync, readFileSync, readdirSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { parse } from 'yaml';

const root = path.resolve(import.meta.dirname, '../..');
const workflow = parse(readFileSync(path.join(root, '.github/workflows/cao-activity.yml'), 'utf8'));
const step = (job, name) => workflow.jobs[job].steps.find((entry) => entry.name === name);

test('manual fast collection narrows download budgets without changing scope or retention', () => {
  for (const name of ['fast', 'force-recycle', 'recycle-only']) {
    assert.deepEqual(
      { type: workflow.on.workflow_dispatch.inputs[name].type, default: workflow.on.workflow_dispatch.inputs[name].default },
      { type: 'boolean', default: false }
    );
  }
  const collection = step('collect', 'Download owner-scoped agentic workflow logs');
  assert.equal(collection.env.REPORT_RUN_LIMIT, "${{ inputs.fast == true && '10' || '10000' }}");
  assert.equal(collection.env.REPORT_MAX_STORAGE, "${{ inputs.fast == true && '100' || '1200' }}");
  assert.equal(collection.env.REPORT_LOG_TIMEOUT, "${{ inputs.fast == true && '5' || '10' }}");
  assert.equal(collection.env.REPORT_DETAIL_WINDOW_DAYS, "${{ inputs.fast == true && '1' || '7' }}");
  assert.equal(collection.env.REPORT_RUN_WINDOW_DAYS, '30');
  assert.equal(collection.env.REPORT_TARGET_REPOSITORIES_JSON, '${{ toJSON(matrix.repositories) }}');
  assert.equal(collection.env.REPORT_INCLUDE_CONTROL_REPOSITORY, '0');
  assert.doesNotMatch(step('plan', 'Resolve activity collection plan').run, /inputs\.fast/);
  assert.deepEqual(workflow.jobs.collect.permissions, {
    actions: 'read', contents: 'read', issues: 'read', 'pull-requests': 'read', 'security-events': 'read'
  });
});

test('fast collection preserves Value history and publishes a deferred decision without acquisition', (t) => {
  const directory = mkdtempSync(path.join(tmpdir(), 'activity-fast-'));
  t.after(() => rmSync(directory, { recursive: true, force: true }));
  const computation = step('collect', 'Retain Value evidence and publish collection health');
  assert.equal(computation.env.REPORT_FAST_MODE, '${{ inputs.fast == true }}');
  const seed = path.join(directory, 'seed');
  const shards = path.join(directory, 'shards');
  mkdirSync(seed);
  mkdirSync(shards);
  writeFileSync(path.join(seed, 'operational-values-example.jsonl'), 'retained evidence\n');
  const inventory = path.join(directory, 'inventory.json');
  writeFileSync(inventory, JSON.stringify({ repositories: { rows: [] }, workflows: { rows: [] } }));
  assert.match(computation.run, /cp "\$retained_values" "\$value_output"[\s\S]*?REPORT_FAST_MODE" == "true" \]\]; then reason=fast-mode/);
  assert.equal(computation['continue-on-error'], true);
  assert.equal(computation['timeout-minutes'], 20);
  const output = execFileSync('bash', ['-euc', computation.run], {
    cwd: root, encoding: 'utf8',
    env: {
      ...process.env, REPORT_FAST_MODE: 'true', ACTIVITY_ARTIFACT: 'example',
      RUNNER_TEMP: directory, REPORT_PUBLIC_ONLY: 'false', REPORT_RATE_LIMITED: 'false',
      REPORT_TARGET_REPOSITORIES_JSON: '["github/gh-aw"]',
      ACTIVITY_DATABASE: path.join(directory, 'unused.sqlite'), REPORT_INVENTORY_SOURCES: inventory,
      GITHUB_RUN_ID: '1', GITHUB_RUN_ATTEMPT: '1', GH_TOKEN: '', GITHUB_TOKEN: '',
      REPORT_VALUE_API_RESERVE: '100',
      REPORT_VALUE_SEED_SHARDS: seed, REPORT_GH_AW_LOGS_SHARDS: shards,
    }
  });
  const result = JSON.parse(output);
  assert.equal(result.imported, 1);
  assert.equal(result.deferred, 1);
  assert.equal(readFileSync(path.join(shards, 'operational-values-example.jsonl'), 'utf8'), 'retained evidence\n');
  const evidence = JSON.parse(readFileSync(path.join(shards, 'workflow-values-example.jsonl'), 'utf8'));
  assert.equal(evidence.operational_value_evidence.observedAt, '2026-10-18T12:05:52.580Z');
  assert.equal(evidence.operational_value_evidence.windowEnd, '2026-10-18T11:54:09Z');
  const health = JSON.parse(readFileSync(path.join(shards, 'workflow-values-example-collections.jsonl'), 'utf8'));
  assert.equal(health.workflow_value_collection.repository, 'github/gh-aw');
  assert.equal(health.workflow_value_collection.workflow, 'daily-file-diet');
  assert.equal(health.workflow_value_collection.registration, 'enabled');
  assert.equal(health.workflow_value_collection.state, 'deferred');
  assert.equal(health.workflow_value_collection.reasonCode, 'fast-mode');
  assert.equal(health.workflow_value_collection.lastAttemptAt, undefined);
  assert.equal(JSON.parse(readFileSync(path.join(shards, 'workflow-value-local.json'), 'utf8'))[0].eligible, false);
});

test('forced recycling runs before new downloads and safely clears only disposable payloads', (t) => {
  for (const [job, download] of [
    ['collect', 'Download owner-scoped agentic workflow logs'],
    ['index', 'Download owner-scoped activity fragments']
  ]) {
    const steps = workflow.jobs[job].steps;
    const maintenance = step(job, 'Force recycling of restored activity cache');
    assert.equal(maintenance.if, job === 'collect'
      ? '${{ inputs.force-recycle == true }}'
      : '${{ inputs.force-recycle == true || inputs.recycle-only == true }}');
    assert.ok(steps.indexOf(maintenance) > steps.findIndex((entry) => entry.name === 'Restore activity cache'));
    assert.ok(steps.indexOf(maintenance) < steps.findIndex((entry) => entry.name === download));
    assert.match(maintenance.run, /--retention-days 30 --max-bytes "\$REPORT_MAX_CACHE_BYTES"/);
    const directory = mkdtempSync(path.join(tmpdir(), `activity-force-${job}-`));
    t.after(() => rmSync(directory, { recursive: true, force: true }));
    const shards = path.join(directory, 'cao-activity/gh-aw-logs-shards');
    mkdirSync(shards, { recursive: true });
    const current = { kind: 'run', run: { run_id: 1, status: 'completed', created_at: new Date().toISOString() } };
    const expired = { kind: 'run', run: { run_id: 2, status: 'completed', created_at: '2020-01-01T00:00:00Z' } };
    writeFileSync(path.join(shards, 'mixed.jsonl'), `${JSON.stringify(current)}\n${JSON.stringify(expired)}\n`);
    const payloads = path.join(directory, 'cao-activity/gh-aw-logs-runs/.payloads');
    mkdirSync(payloads, { recursive: true });
    writeFileSync(path.join(payloads, 'disposable.jsonl'), 'disposable');
    execFileSync('bash', ['-euc', maintenance.run], {
      cwd: root, env: { ...process.env, RUNNER_TEMP: directory, REPORT_MAX_CACHE_BYTES: '1048576' }
    });
    assert.equal(readFileSync(path.join(shards, 'mixed.jsonl'), 'utf8'), `${JSON.stringify(current)}\n`);
    assert.deepEqual(readdirSync(payloads), []);
  }
});

test('recycle-only bypasses repository collection and API evidence requests but requires coherent publication', () => {
  assert.equal(workflow.concurrency.group, "${{ github.event_name == 'workflow_dispatch' && github.ref_name != github.event.repository.default_branch && format('central-agentic-ops-activity-preview-{0}', github.ref) || 'central-agentic-ops-activity' }}");
  assert.equal(workflow.concurrency['cancel-in-progress'], false);
  assert.equal(workflow.jobs.collect.if, '${{ inputs.recycle-only != true }}');
  assert.match(workflow.jobs['notify-failure'].if, /always\(\) && inputs\.recycle-only != true/);
  assert.equal(step('plan', 'Resolve activity collection plan').if, '${{ inputs.recycle-only != true }}');
  assert.match(workflow.jobs.index.if, /!cancelled\(\).*needs\.plan\.result == 'success'.*needs\.collect\.result == 'success' \|\| inputs\.recycle-only == true/);
  for (const name of [
    'Download owner-scoped activity fragments', 'Merge owner-scoped activity fragments',
    'Fetch repository memory refs into a separate checkout', 'Collect campaign repository memory'
  ]) assert.equal(step('index', name).if, '${{ inputs.recycle-only != true }}');
  const steps = workflow.jobs.index.steps;
  const verify = step('index', 'Validate restored snapshot for recycle-only maintenance');
  const reset = step('index', 'Rebuild recycled snapshot projection from retained evidence');
  assert.equal(verify.if, '${{ inputs.recycle-only == true }}');
  assert.equal(reset.if, '${{ inputs.recycle-only == true }}');
  assert.ok(steps.indexOf(verify) < steps.indexOf(step('index', 'Force recycling of restored activity cache')));
  assert.ok(steps.indexOf(reset) < steps.indexOf(step('index', 'Generate phased activity shards')));
  for (const name of [
    'Generate phased activity shards', 'Ingest activity database', 'Repair activity database and apply retention',
    'Hash activity payloads', 'Upload activity snapshot'
  ]) {
    assert.equal(step('index', name).if, undefined);
    assert.ok(steps.indexOf(reset) < steps.indexOf(step('index', name)));
  }
  assert.equal(workflow.jobs.cache.needs, 'index');
  assert.equal(workflow.jobs.cache.if, "${{ !cancelled() && needs.index.result == 'success' }}");
  const cache = workflow.jobs.cache.steps;
  assert.ok(cache.indexOf(step('cache', 'Verify activity snapshot')) < cache.indexOf(step('cache', 'Save activity cache')));
  assert.equal(workflow.jobs.index.permissions.actions, 'read');
  assert.equal(workflow.jobs.cache.permissions.actions, 'write');
  assert.ok(!JSON.stringify(workflow.jobs.index).includes('GH_TOKEN'));
});
