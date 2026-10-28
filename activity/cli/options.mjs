export const DAY_MS = 24 * 60 * 60 * 1000;

// Intentional CLI misuse that should report a message without an internal stack trace.
export class UsageError extends Error {}

export function parseOptions(arguments_) {
  const aliases = { '-R': 'repo', '-w': 'workflow', '-s': 'status', '-L': 'limit' };
  /** @type {Record<string, string | string[]>} */
  const options = {};
  for (let index = 0; index < arguments_.length; index += 1) {
    const argument = arguments_[index];
    if (!argument.startsWith('--') && !aliases[argument]) throw new UsageError(`Unexpected argument: ${argument}`);
    const name = aliases[argument] ?? argument.slice(2);
    if (name === 'help' || name === 'stdin' || name === 'json' || name === 'keep' || name === 'diagnose'
      || name === 'dry-run' || name === 'no-open' || name === 'keep-existing' || name === 'replace-existing'
      || name === 'strict-warnings' || name === 'offline' || name === 'local-only'
      || name === 'exclude-out-of-scope') {
      options[name] = 'true';
      continue;
    }
    const value = arguments_[index + 1];
    if (!value || value.startsWith('--')) throw new UsageError(`Missing value for --${name}`);
    index += 1;
    if (name === 'where' || name === 'param' || name === 'group' || name === 'repository' || name === 'write-repository') {
      const existing = options[name];
      options[name] = [...(Array.isArray(existing) ? existing : []), value];
    } else if (options[name] !== undefined) {
      throw new UsageError(`Option --${name} may only be specified once`);
    } else {
      options[name] = value;
    }
  }
  return options;
}

export async function rawQueryFromStdin(options, input) {
  for (const name of ['collection', 'id', 'where', 'limit']) {
    if (options[name] !== undefined) {
      throw new UsageError(`Option --${name} cannot be combined with --stdin`);
    }
  }

  let content = '';
  for await (const chunk of input) content += chunk;
  if (!content.trim()) throw new UsageError('--stdin requires a JSON object');

  let query;
  try {
    query = JSON.parse(content);
  } catch (error) {
    throw new Error(`Invalid query JSON from stdin: ${error instanceof Error ? error.message : String(error)}`);
  }
  if (!query || typeof query !== 'object' || Array.isArray(query)) {
    throw new UsageError('--stdin requires a JSON object');
  }
  if (typeof query.name !== 'string' || typeof query.from !== 'string') {
    throw new UsageError('--stdin query requires string fields "name" and "from"');
  }
  return query;
}

export function option(options, name, required = true) {
  const value = options[name];
  if (Array.isArray(value)) throw new UsageError(`Option --${name} may only be specified once`);
  if (required && !value) throw new UsageError(`Missing required option --${name}`);
  return value;
}

export function rejectUnknownOptions(options, allowed) {
  for (const name of Object.keys(options)) {
    if (!allowed.includes(name)) throw new UsageError(`Unknown option --${name}`);
  }
}

export function queryLimit(options) {
  const value = option(options, 'limit', false);
  if (!value) return undefined;
  const limit = Number(value);
  if (!Number.isInteger(limit) || limit < 1) throw new UsageError('--limit must be a positive integer');
  return limit;
}

export function ttlDays(options) {
  const value = option(options, 'ttl-days', false);
  if (!value) return undefined;
  if (value === 'all') return 'all';
  const days = Number(value);
  if (!Number.isFinite(days) || days <= 0) throw new UsageError('--ttl-days must be a positive number or all');
  return days;
}

export function retentionWindowMs(options) {
  const value = option(options, 'retention-days', false);
  if (!value) return undefined;
  if (value === 'all') return Number.MAX_SAFE_INTEGER;
  const days = Number(value);
  const milliseconds = days * DAY_MS;
  if (!Number.isFinite(days) || days <= 0 || !Number.isSafeInteger(milliseconds)) {
    throw new UsageError('--retention-days must be a positive number or all');
  }
  return milliseconds;
}

export function runRetentionWindowMs(options) {
  const value = option(options, 'run-retention-days', false);
  if (!value) return undefined;
  if (value === 'all') return Number.MAX_SAFE_INTEGER;
  const days = Number(value);
  const milliseconds = days * DAY_MS;
  if (!Number.isFinite(days) || days <= 0 || !Number.isSafeInteger(milliseconds)) {
    throw new UsageError('--run-retention-days must be a positive number or all');
  }
  return milliseconds;
}

export function runTtlDays(options) {
  const value = option(options, 'run-ttl-days', false);
  if (!value) return undefined;
  if (value === 'all') return 'all';
  const days = Number(value);
  if (!Number.isFinite(days) || days <= 0) {
    throw new UsageError('--run-ttl-days must be a positive number or all');
  }
  return days;
}

export function boundedPositiveInteger(value, name, defaultValue, maximum) {
  if (value === undefined) return defaultValue;
  const parsed = Number(value);
  if (!Number.isSafeInteger(parsed) || parsed < 1 || parsed > maximum) {
    throw new UsageError(`--${name} must be an integer from 1 to ${maximum}`);
  }
  return parsed;
}

export function nonNegativeInteger(value, name, defaultValue) {
  if (value === undefined) return defaultValue;
  const parsed = Number(value);
  if (!Number.isSafeInteger(parsed) || parsed < 0) {
    throw new UsageError(`--${name} must be a non-negative integer`);
  }
  return parsed;
}
