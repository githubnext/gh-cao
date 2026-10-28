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

function repositoryDependencyError(record, runIds, identities) {
  const kind = typeof record?.kind === 'string' && /^[A-Za-z0-9_-]+$/.test(record.kind)
    ? record.kind : 'unknown';
  const dependency = runIds.length === 0
    ? 'no run identity'
    : identities.some((identity) => identity === null)
      ? 'conflicting run identity'
      : 'missing run identity';
  return new UsageError(
    `Shard contains conflicting repository identities with unattributable records; `
    + `compaction requires operator repair (kind: ${kind}; dependency: ${dependency})`
  );
}

export function indexRunRepositories(filePaths) {
  const runs = new Map();
  for (const filePath of filePaths) {
    for (const { value } of jsonlRecords([filePath])) {
      const repository = recordRepository(value);
      if (!repository) continue;
      for (const id of recordRunIds(value)) {
        if (!runs.has(id)) runs.set(id, repository);
        else if (runs.get(id) !== repository) runs.set(id, null);
      }
    }
  }
  return runs;
}

export function inspectShardRepositories(
  filePath,
  { excludeUnscopedRateLimits = false, runRepositories } = {}
) {
  const repositories = new Set();
  for (const { value } of jsonlRecords([filePath])) {
    const repository = recordRepository(value);
    if (!repository) continue;
    repositories.add(repository);
  }
  const runs = runRepositories ?? indexRunRepositories([filePath]);
  const mixed = repositories.size > 1;
  const repositoryFor = (record, { allowShardFallback = true } = {}) => {
    const repository = recordRepository(record);
    if (repository) return repository;
    if (excludeUnscopedRateLimits && record?.kind === 'github_api_rate_limit') return null;
    const runIds = recordRunIds(record);
    const identities = runIds.map((id) => runs.get(id));
    if (identities.length > 0 && identities[0]
        && identities.every((identity) => identity === identities[0])) {
      return identities[0];
    }
    if (!mixed && allowShardFallback && !identities.some((identity) => identity === null)) {
      return [...repositories][0] ?? null;
    }
    throw repositoryDependencyError(record, runIds, identities);
  };
  const associatedRepositories = new Set(repositories);
  for (const { value } of jsonlRecords([filePath])) {
    const repository = repositoryFor(value);
    if (repository) associatedRepositories.add(repository);
  }
  return { repositories, associatedRepositories, mixed, repositoryFor };
}
