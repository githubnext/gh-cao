import { readFile, stat } from 'node:fs/promises';
import path from 'node:path';
import { pathToFileURL } from 'node:url';
import { isDeepStrictEqual } from 'node:util';
import { loadPolicyFile } from '../.github/workflows/shared/policy.mjs';
import { MAX_LOCAL_VALUE_BYTES as MAX_BYTES, runOperationalValue } from './operational-value.mjs';
import { adaptCachedGhAwJsonl } from '../dashboard/site/src/data/adapters/gh-aw-logs.js';
import { readValueEnvelopes, valuePolicyDigest, valueRepositoryInScope, writeValueEnvelopes } from './workflow-value-status.mjs';

export async function localValuePlan({ policy, snapshot, snapshotKey }) {
  const document = loadPolicyFile(policy);
  const filename = path.join(snapshot, 'gh-aw-logs-shards', 'workflow-value-local.json');
  let requests;
  try {
    if ((await stat(filename)).size > MAX_BYTES) throw new Error('Local Value requests exceed their byte budget');
    requests = JSON.parse(await readFile(filename, 'utf8'));
  } catch (error) {
    if (error.code !== 'ENOENT') throw error;
    console.warn('Local Value deferred: no eligible Activity snapshot request is available');
    return { repositories: [], disabledCampaigns: [], disabledWorkflows: [], disabledCollections: [] };
  }
  if (!Array.isArray(requests) || requests.length > 1000) throw new Error('Invalid local Value requests');
  const repositories = new Set();
  for (const request of requests) {
    if (request.schemaVersion !== 1 || typeof request.eligible !== 'boolean'
      || !Array.isArray(request.repositories) || request.repositories.length > 10000
      || request.repositories.some(repository => typeof repository !== 'string')) {
      throw new Error('Invalid local Value request');
    }
    if (!request.eligible || request.snapshot !== snapshotKey || request.policyDigest !== valuePolicyDigest(document)) continue;
    for (const repository of request.repositories) {
      if (valueRepositoryInScope(document, request.repositories, repository, true)) repositories.add(repository.toLowerCase());
    }
  }
  const control = document['control-plane'];
  return {
    repositories: control.campaigns?.activity?.enabled === false ? [] : [...repositories].sort(),
    disabledCampaigns: Object.entries(control.campaigns ?? {}).filter(([, value]) => value.enabled === false).map(([name]) => name),
    disabledWorkflows: Object.entries(control.workflows ?? {}).filter(([, value]) => value.enabled === false).map(([name]) => name),
    disabledCollections: (control.campaigns?.activity?.['workflow-values'] ?? []).filter(entry => entry.enabled === false)
      .map(entry => `${entry.repository.toLowerCase()}:${entry.workflow}`),
  };
}

export async function computeLocalValues(options) {
  const plan = await localValuePlan(options);
  if (plan.repositories.length === 0) {
    console.warn('Local Value deferred: the snapshot is ineligible or its reviewed policy has changed');
    return { values: [], warnings: [{ message: 'The snapshot is ineligible or its reviewed policy has changed' }] };
  }
  return runOperationalValue({
    ...plan, root: options.root, outputPath: options.output,
    databasePath: options.database ?? (
      process.env.CAO_DASHBOARD_QUERY_SOCKET ? undefined : path.join(options.snapshot, 'gh-aw-logs.sqlite')
    ),
    localOnly: true, retentionWindow: 365 * 86400000,
    signal: options.signal ?? AbortSignal.timeout(120000), workerTimeoutMs: 30000,
  });
}

export async function retainLocalValues({ policy, input, output }) {
  const document = loadPolicyFile(policy);
  const readValues = async filename => {
    try {
      if ((await stat(filename)).size > MAX_BYTES) throw new Error('Local Value output exceeds its byte budget');
    } catch (error) {
      if (error.code === 'ENOENT') return [];
      throw error;
    }
    const envelopes = await readValueEnvelopes(filename);
    if (envelopes.some(envelope => envelope.schema_version !== 2 || envelope.kind !== 'operational_value')) {
      throw new Error('Invalid completed local Value envelope');
    }
    adaptCachedGhAwJsonl(envelopes.map(envelope => JSON.stringify(envelope)).join('\n'));
    return envelopes;
  };
  const retained = await readValues(output);
  const key = value => JSON.stringify([value.campaign, value.repository.toLowerCase(), value.value_id, value.timestamp]);
  let accepted = new Map(retained.flatMap(envelope => {
    const value = envelope.operational_value;
    return valueRepositoryInScope(document, [value.repository], value.repository, true) ? [[key(value), envelope]] : [];
  }));
  try {
    const merged = new Map(accepted);
    for (const envelope of await readValues(input)) {
      const value = envelope.operational_value;
      if (!valueRepositoryInScope(document, [value.repository], value.repository, true)) continue;
      const previous = merged.get(key(value));
      if (previous && !isDeepStrictEqual(previous, envelope)) {
        throw new Error('Conflicting completed local Value evidence at the same observation identity');
      }
      merged.set(key(value), envelope);
    }
    accepted = merged;
  } catch (error) {
    console.warn(`Local Value import deferred: ${error.message}; retained evidence is unchanged`);
  }
  const envelopes = [...accepted.values()];
  if (Buffer.byteLength(envelopes.map(envelope => JSON.stringify(envelope)).join('\n')) + envelopes.length > MAX_BYTES) {
    throw new Error('Combined local Value output exceeds its byte budget; retained evidence is unchanged');
  }
  await writeValueEnvelopes(output, envelopes);
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    const [operation, ...args] = process.argv.slice(2);
    const options = {};
    for (let index = 0; index < args.length; index += 2) {
      const key = args[index].replace(/^--/, '');
      if (!['root', 'policy', 'snapshot', 'snapshotKey', 'database', 'input', 'output'].includes(key) || !args[index + 1]) {
        throw new Error('Invalid local-values option');
      }
      options[key] = args[index + 1];
    }
    if (operation === 'retain') await retainLocalValues(options);
    else if (operation === 'compute') {
      const result = await computeLocalValues(options);
      console.log(JSON.stringify({ ...result, values: result.values.length }));
    }
    else if (operation === 'plan') console.log(JSON.stringify(await localValuePlan(options)));
    else throw new Error('Expected local-values compute, retain, or plan');
  } catch (error) {
    console.error(`Local Value failed: ${error.message}`);
    process.exitCode = 1;
  }
}
