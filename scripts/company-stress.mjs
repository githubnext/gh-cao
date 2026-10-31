import { spawnSync } from 'node:child_process';
import { readFileSync, mkdirSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { parseArgs } from 'node:util';

const root = fileURLToPath(new URL('../', import.meta.url));
const numeric = {
  repositories: 'repositories',
  developers: 'developers',
  'prs-per-developer-per-day': 'prsPerDeveloperPerDay',
  'aw-runs-per-pr': 'awRunsPerPR',
  days: 'days',
  'aic-per-run': 'aicPerRun',
  seed: 'seed'
};

function main() {
  const { values } = parseArgs({
    options: Object.fromEntries([
      ...Object.keys(numeric).map((name) => [name, { type: 'string' }]),
      ...['preset', 'scenario', 'distribution', 'report-dir', 'timeout'].map((name) => [name, { type: 'string' }]),
      ['help', { type: 'boolean' }]
    ])
  });
  if (values.help) {
    console.log(`Usage: npm run test:stress:company -- [--preset small|medium|large | --scenario FILE]
  --repositories N --developers N --prs-per-developer-per-day N
  --aw-runs-per-pr N --days N --aic-per-run N --seed N
  --distribution uniform|burst --timeout 40m --report-dir PATH
Requires POSTGRES_URL pointing to disposable local PostgreSQL.
Generates synthetic AW records; no agents, GitHub calls, or completion simulation.`);
    return;
  }
  if (values.preset && values.scenario) throw new Error('Choose --preset or --scenario, not both');
  const preset = values.preset ?? 'small';
  if (!values.scenario && !['small', 'medium', 'large'].includes(preset)) throw new Error('Unknown company preset');
  const scenario = JSON.parse(readFileSync(values.scenario
    ? resolve(values.scenario)
    : resolve(root, 'tests/fixtures/company-stress', `${preset}.json`), 'utf8'));
  for (const [option, field] of Object.entries(numeric)) {
    if (values[option] === undefined) continue;
    if (String(values[option]).trim() === '' || !Number.isFinite(Number(values[option]))) {
      throw new Error(`--${option} must be a finite number`);
    }
    scenario[field] = Number(values[option]);
  }
  if (values.distribution) scenario.distribution = values.distribution;
  const reportDir = resolve(values['report-dir'] ?? resolve(root, 'test-results/company-stress'));
  mkdirSync(reportDir, { recursive: true });
  // Save configuration even when prerequisites or the Go test fail.
  writeFileSync(resolve(reportDir, 'scenario.json'), `${JSON.stringify(scenario, null, 2)}\n`);
  writeFileSync(resolve(reportDir, 'company-stress.json'), `${JSON.stringify({
    status: 'incomplete', stage: 'prerequisites', synthetic: true, scenario
  }, null, 2)}\n`);
  const endpoint = new URL(process.env.POSTGRES_URL ?? 'postgres://missing');
  if (!['postgres:', 'postgresql:'].includes(endpoint.protocol) ||
      !['127.0.0.1', '[::1]'].includes(endpoint.hostname)) {
    throw new Error('POSTGRES_URL must point to disposable PostgreSQL on numeric loopback (127.0.0.1 or ::1)');
  }
  const timeout = values.timeout ?? '40m';
  if (!/^[1-9]\d*(s|m|h)$/.test(timeout)) throw new Error('--timeout must be a positive Go duration, e.g. 40m');
  const result = spawnSync('go', [
    '-C', 'server', 'test', './internal/server', '-run', '^TestCompanyStress$', '-count=1',
    `-timeout=${timeout}`, '-v'
  ], {
    cwd: root,
    stdio: 'inherit',
    env: {
      ...process.env,
      GH_TOKEN: '', GITHUB_TOKEN: '', GH_ENTERPRISE_TOKEN: '',
      CAO_COMPANY_STRESS: '1',
      CAO_COMPANY_SCENARIO: JSON.stringify(scenario),
      CAO_COMPANY_REPORT_DIR: reportDir
    }
  });
  if (result.error) throw result.error;
  if (result.signal) throw new Error(`Company stress interrupted by ${result.signal}; report may be incomplete`);
  process.exitCode = result.status ?? 1;
}

try {
  main();
} catch (error) {
  console.error(error instanceof Error ? error.message : String(error));
  process.exitCode = 1;
}
