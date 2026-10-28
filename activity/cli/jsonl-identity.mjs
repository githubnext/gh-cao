import { jsonlRecords } from './files.mjs';
import { UsageError } from './options.mjs';

export function recordRepository(record) {
  const run = record?.run;
  const candidates = [record?.request?.repository, run?.repository_full_name];
  if (typeof run?.repository === 'string') {
    candidates.push(run.repository.includes('/') ? run.repository
      : typeof run.organization === 'string' ? `${run.organization}/${run.repository}` : null);
  }
  let identity = null;
  for (const candidate of candidates) {
    if (candidate === undefined || candidate === null || candidate === '') continue;
    if (typeof candidate !== 'string'
        || !/^[A-Za-z0-9][A-Za-z0-9-]*\/[A-Za-z0-9._-]+$/.test(candidate)) {
      throw new UsageError('Shard contains an invalid repository identity');
    }
    const repository = candidate.toLowerCase();
    if (identity !== null && identity !== repository) {
      throw new UsageError('Record contains conflicting repository identities; compaction requires operator repair');
    }
    identity = repository;
  }
  return identity;
}

function recordRunIds(record) {
  if (record?.kind === 'workflow_runs' && Array.isArray(record.payload)) {
    return record.payload.map((run) => run?.databaseId).filter((id) => id != null).map(String);
  }
  const id = record?.run?.run_id ?? record?.safe_output?.run_id ?? record?.observation?.optimizerRunId;
  return id == null ? [] : [String(id)];
}

export function inspectShardRepositories(filePath, { excludeUnscopedRateLimits = false } = {}) {
  const repositories = new Set();
  const runs = new Map();
  for (const { value } of jsonlRecords([filePath])) {
    const repository = recordRepository(value);
    if (!repository) continue;
    repositories.add(repository);
    for (const id of recordRunIds(value)) {
      if (!runs.has(id)) runs.set(id, repository);
      else if (runs.get(id) !== repository) runs.set(id, null);
    }
  }
  const mixed = repositories.size > 1;
  const repositoryFor = (record) => {
    const repository = recordRepository(record);
    if (repository || !mixed) return repository ?? [...repositories][0] ?? null;
    if (excludeUnscopedRateLimits && record?.kind === 'github_api_rate_limit') return null;
    const identities = recordRunIds(record).map((id) => runs.get(id));
    if (!identities.length || !identities[0] || identities.some((identity) => identity !== identities[0])) {
      throw new UsageError('Shard contains conflicting repository identities with unattributable records; compaction requires operator repair');
    }
    return identities[0];
  };
  // Validate every association before any compaction output is written.
  if (mixed) {
    for (const { value } of jsonlRecords([filePath])) repositoryFor(value);
  }
  return { repositories, mixed, repositoryFor };
}
