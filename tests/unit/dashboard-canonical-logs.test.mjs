import { execFile } from 'node:child_process';
import test from 'node:test';
import { promisify } from 'node:util';
import { verifyGoResults } from '../../scripts/run-daily-run-counts-conformance.mjs';

const execFileAsync = promisify(execFile);
test('native raw-log collection preserves rich canonical evidence, cache ownership and independent daily retention', async () => {
  const required = [
    'TestDashboardHydratesRichCachedAndUnifiedEvidence',
    'TestDashboardArtifactCachesKeepExactScopeAndOwnership',
    'TestDashboardDailyCountsRetentionAndAtomicPublication',
  ];
  const { stdout } = await execFileAsync('go', [
    'test', '-json', '-count=1', './internal/cli', '-run', `^(${required.join('|')})$`,
  ], { timeout: 120_000, maxBuffer: 8 * 1024 * 1024 });
  verifyGoResults(stdout, required);
});