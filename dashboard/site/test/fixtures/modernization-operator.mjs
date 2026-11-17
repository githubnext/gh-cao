import { createServer } from 'node:http';
import { randomBytes } from 'node:crypto';
import { mkdtemp, writeFile, rm } from 'node:fs/promises';
import { join } from 'node:path';

/** Explicit native-adapter fixture; never dispatches GitHub work.
 * @param {string} repository
 */
export async function startModernizationOperator(repository) {
  const directory = await mkdtemp(join(process.cwd(), '.modernization-operator-'));
  const prefix = `/${randomBytes(24).toString('hex')}`;
  const requests = /** @type {Array<{args: string[], input?: string}>} */ ([]);
  const previews = new Map();
  const common = { schema: 'cao-modernization-operator/v1', status: 'available',
    controlRepository: repository, controlSha: 'a'.repeat(40), repository };
  const task = { id: 't1', title: 'Synthetic operator task: characterize the legacy SOAP contract and database schema',
    repository: 'octodemo/synthetic-service-backend-template-20261116184218',
    phase: 'issue', state: 'ready', issueUrl: null, pullRequestUrl: null };
  const plan = {
    schema: 'cao-modernization/v1', id: 'synthetic-plan', objective: 'Preserve the synthetic service contract.',
    repositories: [repository, task.repository].map(repository => ({
      repository, base_sha: 'e'.repeat(40), base_branch: 'main'
    })),
    tasks: [{ id: task.id, title: task.title, repository: task.repository,
      objective: 'Characterize the synthetic API before changes.', acceptance: ['Synthetic API behavior is documented.'],
      depends_on: [], specialists: ['assessor', 'validator'] }],
    integration_acceptance: ['Synthetic services remain compatible.']
  };
  const server = createServer(async (request, response) => {
    if (request.method !== 'POST' || request.url !== `${prefix}/execute`) {
      response.writeHead(request.url === `${prefix}/cancel` ? 202 : 404).end();
      return;
    }
    let body = '';
    for await (const chunk of request) body += chunk;
    const payload = JSON.parse(body);
    requests.push(payload);
    const operation = payload.args[1];
    let result;
    if (operation === 'status') result = { ...common, kind: 'status', capabilities: Object.fromEntries(
      ['assess', 'approve-plan', 'run', 'verify'].map(action => [action, { authorized: true, reason: 'Synthetic reviewed fixture' }])
    ), plan, planDigest: 'b'.repeat(64), repositories: [repository, task.repository],
    tasks: [task], evidence: { available: true }, links: { issuesUrl: `https://github.com/${repository}/issues` } };
    else if (operation === 'preview') {
      const action = payload.args[payload.args.indexOf('--action') + 1];
      const mode = payload.args[payload.args.indexOf('--mode') + 1];
      const previewId = randomBytes(24).toString('hex');
      result = { ...common, kind: 'preview', previewId, action, mode, canExecute: true,
        planDigest: 'b'.repeat(64), batchDigest: 'c'.repeat(64), repositories: [repository, task.repository],
        tasks: [task], effects: ['Synthetic adapter only; no GitHub write'],
        warnings: [], expiresAt: new Date(Date.now() + 60_000).toISOString() };
      previews.set(previewId, result);
    } else {
      const previewId = payload.args[payload.args.indexOf('--preview') + 1];
      const preview = previews.get(previewId);
      if (!preview) { response.writeHead(409).end('Unknown fixture preview'); return; }
      result = { ...common, kind: 'execution', accepted: true, action: preview.action,
        workflowUrl: `https://github.com/${repository}/actions`,
        ...(preview.action === 'approve-plan' ? { approvalCommitUrl: `https://github.com/${repository}/commit/${'d'.repeat(40)}` } : {}) };
      previews.delete(previewId);
    }
    response.writeHead(200, { 'Content-Type': 'application/x-ndjson' });
    response.end(`${JSON.stringify({ stream: 'stdout', data: `CAO_MODERNIZATION_RESULT:${JSON.stringify(result)}\n` })}\n${JSON.stringify({ complete: true, exitCode: 0 })}\n`);
  });
  await new Promise(resolve => server.listen(0, '127.0.0.1', () => resolve(undefined)));
  const address = server.address();
  if (!address || typeof address === 'string') throw new Error('Synthetic native adapter did not bind loopback.');
  const operatorConfig = join(directory, 'operator.json');
  await writeFile(operatorConfig, JSON.stringify({
    adapterUrl: `http://127.0.0.1:${address.port}${prefix}`, operatorActions: { repositories: [repository] }
  }), { mode: 0o600 });
  return {
    operatorConfig, requests, plan,
    async close() {
      await new Promise((resolve, reject) => server.close(error => error ? reject(error) : resolve(undefined)));
      await rm(directory, { recursive: true, force: true });
    }
  };
}
