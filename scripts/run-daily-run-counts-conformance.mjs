import { spawnSync } from 'node:child_process';
import { mkdirSync, readFileSync, rmSync } from 'node:fs';
import { dirname, relative, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '..');
export const contract = JSON.parse(readFileSync(resolve(root, 'tests/contracts/daily-run-counts.json'), 'utf8'));
export const statisticalContract = JSON.parse(readFileSync(resolve(root, 'tests/contracts/statistical-sampling.json'), 'utf8'));

export function validateContract(value) {
  if (value.version !== 1 || !Object.keys(value.requirements ?? {}).length) {
    throw new Error('Expected versioned dashboard conformance requirements');
  }
  const ids = new Set();
  const covered = new Set();
  for (const suite of value.suites) {
    if (ids.has(suite.id)) throw new Error(`Duplicate conformance suite: ${suite.id}`);
    ids.add(suite.id);
    if (!['node', 'vitest', 'playwright', 'go', 'tlc'].includes(suite.runner) || !suite.files.length) {
      throw new Error(`Invalid runner or empty files: ${suite.id}`);
    }
    const sources = suite.files.map((file) => readFileSync(resolve(root, file), 'utf8'));
    for (const [requirement, name] of Object.entries(suite.coverage)) {
      if (!Object.hasOwn(value.requirements, requirement)) throw new Error(`Unknown requirement: ${requirement}`);
      if (!sources.some((source) => source.includes(name))) throw new Error(`Missing coverage evidence: ${suite.id}: ${name}`);
      if (['go', 'playwright'].includes(suite.runner) && !new RegExp(suite.pattern).test(name)) {
        throw new Error(`Test selector excludes coverage evidence: ${name}`);
      }
      covered.add(requirement);
    }
  }
  for (const id of value.requiredSuites ?? []) {
    if (!ids.has(id)) throw new Error(`Missing required conformance suite: ${id}`);
  }
  for (const requirement of Object.keys(value.requirements)) {
    if (!covered.has(requirement)) throw new Error(`Uncovered requirement: ${requirement}`);
  }
  return value.suites;
}

function requireEvidence(passed, expected) {
  for (const name of expected) {
    const pattern = new RegExp(`^${name.replace(/[.*+?^${}()|[\]\\]/g, '\\$&').replaceAll('%s', '.+?')}$`);
    if (!passed.some((test) => pattern.test(test))) throw new Error(`Required conformance test did not pass: ${name}`);
  }
}

export function verifyGoResults(output, expected = []) {
  const events = output.split('\n').filter(Boolean).map((line) => JSON.parse(line));
  if (events.some((event) => event.Action === 'skip')) throw new Error('Conformance does not permit skipped Go tests');
  const passed = events.filter((event) => event.Action === 'pass' && event.Test).map((event) => event.Test);
  if (!passed.length) throw new Error('No Go conformance tests executed');
  requireEvidence(passed, expected);
  return passed.length;
}

export function verifyBrowserResults(output, expected = []) {
  const { stats, suites = [] } = JSON.parse(output);
  if (!(stats?.expected > 0) || stats.skipped || stats.unexpected || stats.flaky) {
    throw new Error('Native browser conformance requires passing tests without skips or retries');
  }
  const passed = [];
  const visit = (entries) => {
    for (const suite of entries) {
      for (const spec of suite.specs ?? []) {
        if (spec.tests?.every((test) => test.expectedStatus === 'passed' &&
          test.results.length === 1 && test.results[0].status === 'passed')) passed.push(spec.title);
      }
      visit(suite.suites ?? []);
    }
  };
  visit(suites);
  requireEvidence(passed, expected);
  return stats.expected;
}

export function verifyNodeResults(output, expected = []) {
  const events = output.split('\n').filter(Boolean).map((line) => JSON.parse(line));
  const tests = events.filter((event) => ['test:pass', 'test:fail'].includes(event.type));
  if (!tests.length || tests.some((event) => event.type === 'test:fail' || event.data.skip || event.data.todo)) {
    throw new Error('Node conformance requires passing tests without skips or todos');
  }
  requireEvidence(tests.map((event) => event.data.name), expected);
  return tests.length;
}

export function verifyVitestResults(output, expected = []) {
  const report = JSON.parse(output);
  const tests = (report.testResults ?? []).flatMap((file) => file.assertionResults ?? []);
  if (!tests.length || tests.some((test) => test.status !== 'passed')) {
    throw new Error('Vitest conformance requires passing tests without skips or todos');
  }
  requireEvidence(tests.map((test) => test.title), expected);
  return tests.length;
}

export function verifyTLCResults(output) {
  if (!output.includes('Model checking completed. No error has been found.') ||
      !/\d+ distinct states found, 0 states left on queue/.test(output) ||
      output.includes('Error:')) {
    throw new Error('TLC conformance requires a complete state-space check without violations');
  }
  return 1;
}

export default async function* nodeConformanceReporter(events) {
  for await (const event of events) {
    if (['test:pass', 'test:fail'].includes(event.type)) {
      yield `${JSON.stringify(event, (_key, value) => value instanceof Error
        ? { ...value, message: value.message, stack: value.stack } : value)}\n`;
    }
  }
}

export function runConformance(environment = process.env, value = contract) {
  const suites = validateContract(value);
  if (!environment.POSTGRES_URL) {
    throw new Error('POSTGRES_URL is required: use an isolated PostgreSQL database with schema-creation permission; skipped PostgreSQL tests are not conformance');
  }
  if (suites.some((suite) => suite.runner === 'tlc') && !environment.TLA2TOOLS_JAR) {
    throw new Error('TLA2TOOLS_JAR is required: formal conformance must execute TLC, not skip it');
  }
  const title = value.title ?? 'Daily run-count';
  // Chromium's Unix socket path must fit the platform's short pathname limit.
  const scratch = resolve(root, '.tmp/drc');
  mkdirSync(scratch, { recursive: true });
  const env = { ...environment, TMPDIR: scratch, TMP: scratch, TEMP: scratch };
  for (const suite of suites) {
    console.log(`\n${title} conformance: ${suite.id}`);
    let command = process.execPath;
    let args = ['--test', `--test-reporter=${fileURLToPath(import.meta.url)}`, ...suite.files];
    let cwd = root;
    let reportPath;
    if (suite.runner === 'vitest') {
      cwd = resolve(root, 'dashboard/site');
      reportPath = resolve(scratch, 'vitest.json');
      rmSync(reportPath, { force: true });
      args = [
        resolve(cwd, 'node_modules/vitest/vitest.mjs'), 'run', '--environment', 'jsdom', '--reporter=json',
        '--outputFile', reportPath,
        ...suite.files.map((file) => relative(cwd, resolve(root, file)))
      ];
    } else if (suite.runner === 'playwright') {
      cwd = resolve(root, 'dashboard/site');
      args = [
        resolve(cwd, 'node_modules/@playwright/test/cli.js'), 'test',
        '--config=playwright.config.mjs', '--project=desktop-chrome', '--workers=1',
        '--grep', suite.pattern, '--reporter=json',
        ...suite.files.map((file) => relative(cwd, resolve(root, file)))
      ];
    } else if (suite.runner === 'go') {
      command = 'go';
      cwd = resolve(root, suite.directory);
      args = ['test', '-json', '-count=1', ...suite.packages, '-run', suite.pattern];
    } else if (suite.runner === 'tlc') {
      command = 'java';
      args = ['-XX:+UseParallelGC', '-Xmx1g', '-cp', environment.TLA2TOOLS_JAR,
        'tlc2.TLC', '-workers', '2', '-metadir', resolve(scratch, suite.id),
        '-config', suite.files.find((file) => file.endsWith('.cfg')),
        suite.files.find((file) => file.endsWith('.tla'))];
    }
    const result = spawnSync(command, args, {
      cwd, env, encoding: 'utf8', stdio: 'pipe',
      maxBuffer: 16 * 1024 * 1024
    });
    if (suite.runner === 'go') {
      for (const line of (result.stdout ?? '').split('\n').filter(Boolean)) {
        const event = JSON.parse(line);
        if (event.Output) process.stdout.write(event.Output);
      }
    } else {
      if (result.status !== 0) process.stdout.write(result.stdout ?? '');
    }
    if (result.stderr) process.stderr.write(result.stderr);
    if (result.error) throw result.error;
    if (result.status !== 0) throw new Error(`Conformance suite failed: ${suite.id}`);
    const verify = {
      node: verifyNodeResults, vitest: verifyVitestResults,
      go: verifyGoResults, playwright: verifyBrowserResults, tlc: verifyTLCResults
    }[suite.runner];
    const passed = verify(reportPath ? readFileSync(reportPath, 'utf8') : result.stdout, Object.values(suite.coverage));
    console.log(`${passed} ${suite.runner} test result(s) passed; every declared coverage test executed.`);
  }
  console.log(`\nAll implemented ${title.toLowerCase()} conformance suites passed (no skips).`);
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const args = process.argv.slice(2);
  if (args.length && (args.length !== 1 || args[0] !== '--statistical')) {
    throw new Error('Expected no arguments or --statistical');
  }
  runConformance(process.env, args.length ? statisticalContract : contract);
}
