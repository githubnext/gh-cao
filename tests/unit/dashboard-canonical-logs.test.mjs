import assert from 'node:assert/strict';
import { execFile } from 'node:child_process';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import test from 'node:test';
import { promisify } from 'node:util';

const execFileAsync = promisify(execFile);


test('explicit raw-log ingestion preserves default and configured retention policies', async (t) => {
  const fixture = path.resolve('dashboard/site/test/fixtures/gh-aw-logs');
  const directory = await mkdtemp(path.join(tmpdir(), 'cao-cli-log-retention-'));
  t.after(() => rm(directory, { recursive: true, force: true }));
  for (const [name, now, options, expected] of [
    ['defaults', '2026-09-20', [], { runs: 1, domains: 0, tools: 0, audits: 0 }],
    ['all', '2026-10-22', ['--retention-days', 'all', '--run-retention-days', 'all'], { runs: 1, domains: 1, tools: 3, audits: 2 }],
    ['expired-defaults', '2026-10-22', [], { runs: 0, domains: 0, tools: 0, audits: 0 }],
    ['all-runs', '2026-10-22', ['--retention-days', '7', '--run-retention-days', 'all'], { runs: 1, domains: 0, tools: 0, audits: 0 }],
  ]) {
    const clock = `data:text/javascript,Date.now=()=>Date.parse("${now}T00:00:00Z")`;
    const { stdout } = await execFileAsync(process.execPath, [
      '--import', clock, path.resolve('activity/cao.mjs'), 'ingest',
      '--database', path.join(directory, `${name}.sqlite`),
      '--context', path.join(fixture, 'context.json'),
      '--logs', path.join(fixture, 'run-303'), ...options,
    ]);
    const output = JSON.parse(stdout);
    assert.deepEqual(Object.keys(output).sort(), ['counts', 'result']);
    for (const [collection, count] of Object.entries(expected)) {
      assert.equal(output.counts[collection], count, `${name}: ${collection}`);
    }
  }
});