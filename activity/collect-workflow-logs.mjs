import { execFile, spawn } from 'node:child_process';
import path from 'node:path';
import { pathToFileURL } from 'node:url';
import { promisify } from 'node:util';
import { GithubApiCapacityError, isGithubRateLimitError } from './github-telemetry.mjs';
import { assertMonitoringRepository } from './repository-visibility.mjs';

const execute = promisify(execFile);

export function workflowLogTargets(pages, repository) {
  if (!Array.isArray(pages) || pages.some((page) => !page || !Array.isArray(page.workflows))) {
    throw new TypeError('Remote workflow discovery must return paginated workflow lists');
  }
  const targets = new Set();
  for (const page of pages) {
    for (const workflow of page.workflows) {
      if (typeof workflow?.path !== 'string') throw new TypeError('Remote workflow path is required');
      if (!workflow.path.endsWith('.lock.yml')) continue;
      if (!/^\.github\/workflows\/[A-Za-z0-9_.-]+\.lock\.yml$/.test(workflow.path)) {
        throw new TypeError('Remote agentic workflow path is invalid');
      }
      targets.add(`${repository}/${workflow.path}`);
    }
  }
  return [...targets].sort();
}

export async function collectWorkflowLogs(binary, args, {
  publicOnly = process.env.REPORT_PUBLIC_ONLY === 'true',
  controlRepository = process.env.GITHUB_REPOSITORY,
} = {}) {
  const index = args.indexOf('--repo');
  const repository = args[index + 1];
  if (index < 0 || args.lastIndexOf('--repo') !== index
      || !/^[A-Za-z0-9][A-Za-z0-9-]*\/[A-Za-z0-9._-]+$/.test(repository ?? '')) {
    throw new TypeError('Workflow log collection requires exactly one repository');
  }
  const timeoutIndex = args.indexOf('--timeout');
  const minutes = timeoutIndex < 0 ? 0 : Number(args[timeoutIndex + 1]);
  // Auditing and writing hundreds of downloaded runs can outlast the download deadline.
  const signal = Number.isSafeInteger(minutes) && minutes > 0
    ? AbortSignal.timeout((minutes + 5) * 60_000) : undefined;
  if (publicOnly) {
    // A missing repository or visibility change must stop publication, not be
    // downgraded to an unavailable artifact.
    const metadata = await execute(binary, ['api', `repos/${repository}`], {
      encoding: 'utf8', maxBuffer: 1024 * 1024, signal,
    }).catch(() => { throw new Error(`Public visibility could not be verified for ${repository}`); });
    assertMonitoringRepository(JSON.parse(metadata.stdout), repository, controlRepository);
  }
  let response;
  try {
    response = await execute(binary, [
      'api', `repos/${repository}/actions/workflows?per_page=100`, '--paginate', '--slurp'
    ], { encoding: 'utf8', maxBuffer: 16 * 1024 * 1024, signal });
  } catch (error) {
    if (signal?.aborted) throw new Error('Workflow log collection exceeded its timeout', { cause: error });
    if (isGithubRateLimitError(String(error.stderr ?? error.message))) {
      throw new GithubApiCapacityError('Remote workflow discovery failed: HTTP 429: API rate limit exceeded');
    }
    if (publicOnly) {
      console.error(`Public workflow evidence is unavailable for ${repository}; collection is incomplete`);
      return 76;
    }
    throw new Error('Remote workflow discovery failed', { cause: error });
  }
  let pages;
  try {
    pages = JSON.parse(response.stdout);
  } catch (error) {
    throw new TypeError('Remote workflow discovery must return valid JSON', { cause: error });
  }
  const targets = workflowLogTargets(pages, repository);
  if (!targets.length) {
    console.log(`No agentic workflows found for ${repository}; skipping log collection`);
    return 0;
  }
  // Bare --repo discovery in gh-aw filters against the collector's local catalog.
  return new Promise((resolve, reject) => {
    const child = spawn(binary, ['aw', 'logs', ...args, ...targets], { stdio: 'inherit', signal });
    child.once('error', () => reject(new Error(signal?.aborted
      ? 'Workflow log collection exceeded its timeout' : 'gh-aw log collection could not be started')));
    child.once('exit', (code) => resolve(publicOnly && code !== 0 && code !== 75 ? 76 : code ?? 1));
  });
}

if (process.argv[1] && import.meta.url === pathToFileURL(path.resolve(process.argv[1])).href) {
  const [binary, ...args] = process.argv.slice(2);
  collectWorkflowLogs(binary, args).then((code) => { process.exitCode = code; }).catch((error) => {
    console.error(error.message);
    process.exitCode = error instanceof GithubApiCapacityError ? 75 : 1;
  });
}
