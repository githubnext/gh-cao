import { createHash } from 'node:crypto';
import { mkdir, readFile, rename, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { queryDashboardSourceObservations } from '../dashboard/site/src/data/queries/ingestion.js';
import {
  VALUE_COLLECTION_REASONS, validateWorkflowValueCollection, workflowValueCollectionId,
} from '../dashboard/site/src/data/model/workflow-value-collection.js';

const same = (a, b) => typeof a === 'string' && typeof b === 'string' && a.toLowerCase() === b.toLowerCase();
export const VALUE_DEFERRALS = new Set(['api-budget', 'budget-unavailable', 'shared-budget-unavailable',
  'fast-mode', 'public-only', 'batch-budget']);
export const valuePolicyDigest = document => createHash('sha256').update(JSON.stringify(document)).digest('hex');

export function valueRepositoryInScope(document, repositories, repository, allowActivityFallback = false) {
  const control = document['control-plane'];
  if (!control) throw new Error('Control policy is required');
  const scope = control.scope ?? {};
  const enrolled = control.activity?.repositories
    ?? (allowActivityFallback ? scope['allowed-repositories'] ?? [] : []);
  return repositories.some(target => same(target, repository))
    && enrolled.some(target => same(target, repository))
    && (!scope['allowed-repositories'] || scope['allowed-repositories'].some(target => same(target, repository)))
    && (!scope['allowed-owners'] || scope['allowed-owners'].some(owner => same(owner, repository.split('/')[0])));
}

export function workflowValueSelections(document, repositories) {
  return (document['control-plane']?.campaigns?.activity?.['workflow-values'] ?? [])
    .filter(entry => valueRepositoryInScope(document, repositories, entry.repository));
}

export function workflowValueApiHeadroom(document, repositories) {
  if (document['control-plane']?.campaigns?.activity?.enabled === false) return 0;
  return workflowValueSelections(document, repositories).reduce((total, entry) =>
    total + (entry.enabled !== false && entry.collector ? entry['api-requests'] ?? 0 : 0), 0);
}

export async function readValueEnvelopes(filename) {
  try {
    return (await readFile(filename, 'utf8')).split('\n').filter(Boolean).map(line => JSON.parse(line));
  } catch (error) {
    if (error.code === 'ENOENT') return [];
    throw error;
  }
}

export async function writeValueEnvelopes(filename, envelopes) {
  await mkdir(path.dirname(filename), { recursive: true });
  const pending = `${filename}.pending`;
  await writeFile(pending, envelopes.map(value => JSON.stringify(value)).join('\n')
    + (envelopes.length ? '\n' : ''), { mode: 0o600 });
  await rename(pending, filename);
}

export async function workflowValueStatusContext(options, document, retained) {
  const output = options.healthOutput ?? options.output.replace(/(?:\.jsonl)?$/, '-collections.jsonl');
  const previous = new Map((await readValueEnvelopes(output)).flatMap(envelope => {
    if (envelope.schema_version !== 2 || envelope.kind !== 'workflow_value_collection') {
      throw new Error('Invalid retained workflow Value collection envelope');
    }
    const record = validateWorkflowValueCollection(envelope.workflow_value_collection);
    return valueRepositoryInScope(document, options.repositories, record.repository) ? [[record.id, record]] : [];
  }));
  const registrations = workflowValueSelections(document, options.repositories);
  const targets = new Map();
  const include = ({ repository, workflow }) => {
    if (!valueRepositoryInScope(document, options.repositories, repository)) return;
    targets.set(workflowValueCollectionId(repository, workflow), { repository, workflow });
  };
  for (const record of previous.values()) include(record);
  for (const envelope of retained) include(envelope.operational_value_evidence);
  if (options.inventory) {
    const inventory = JSON.parse(await readFile(options.inventory, 'utf8'));
    const sources = {};
    for (const name of ['repositories', 'workflows']) {
      if (inventory[name]?.metadata?.availability === 'unavailable') {
        console.warn(`Workflow Value inventory unavailable: ${name}; registration decisions still use reviewed policy`);
      } else sources[name] = inventory[name];
    }
    const { observations } = queryDashboardSourceObservations({
      ...sources,
    });
    const repositories = new Map(observations.filter(value => value.kind === 'repository')
      .map(value => [value.data.id, `${value.data.owner}/${value.data.name}`]));
    for (const { data } of observations.filter(value => value.kind === 'workflow')) {
      const workflow = /^\.github\/workflows\/([a-z0-9]+(?:-[a-z0-9]+)*)\.md$/.exec(String(data.path))?.[1];
      const repository = repositories.get(data.repositoryId);
      if (workflow && repository) include({ repository, workflow });
    }
  }
  for (const entry of registrations) targets.set(workflowValueCollectionId(entry.repository, entry.workflow), entry);
  if (targets.size > 10000) throw new Error('Workflow Value collection inventory exceeds its row budget');
  return {
    output, previous, targets: [...targets.values()],
    policyDigest: valuePolicyDigest(document),
    activityDisabled: document['control-plane']?.campaigns?.activity?.enabled === false,
  };
}

export function valueCollectionDecision(context, entry, {
  state, reasonCode, observedAt, lastAttemptAt, lastAttemptOutcome,
}) {
  const id = workflowValueCollectionId(entry.repository, entry.workflow);
  const previous = context.previous.get(id);
  const record = validateWorkflowValueCollection({
    schemaVersion: 1, id, repository: entry.repository.toLowerCase(), workflow: entry.workflow,
    sourcePath: `.github/workflows/${entry.workflow}.md`,
    policyDigest: context.policyDigest, observedAt,
    registration: entry.enabled === false ? 'disabled' : entry.collector ? 'enabled' : 'unconfigured',
    ...(entry.collector ? { collector: entry.collector } : {}),
    state, reasonCode, reason: VALUE_COLLECTION_REASONS[reasonCode],
    ...(lastAttemptAt || previous?.lastAttemptAt ? {
      lastAttemptAt: lastAttemptAt ?? previous.lastAttemptAt,
      lastAttemptOutcome: lastAttemptOutcome ?? previous.lastAttemptOutcome,
    } : {}),
  });
  context.previous.set(id, record);
  return record;
}

export async function saveValueCollectionDecisions(context) {
  await writeValueEnvelopes(context.output, [...context.previous.values()]
    .sort((left, right) => left.id.localeCompare(right.id))
    .map(record => ({ schema_version: 2, kind: 'workflow_value_collection', workflow_value_collection: record })));
}
