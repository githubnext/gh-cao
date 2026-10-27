import {
  issueId,
  issueCoordinates,
  repositoryCoordinateId,
  runId,
  sourceId,
  workflowCoordinateId,
  workflowSourcePath
} from '../model/ids.js';
import { canonicalTimestamp, OPERATIONAL_VALUE_PERIOD_FIELDS, operationalValuePeriod, requiredString } from '../model/schema.js';
import { validateOperationalValueEvidence } from '../model/operational-value-evidence.js';
import { validateWorkflowValueCollection } from '../model/workflow-value-collection.js';
import cachedJsonlExpression from '../ingest/expressions/gh-aw-logs-v2.json' with { type: 'json' };
import { createDebug } from '../../debug.js';
import { normalizeSessionMcpEvents, parseUnifiedSession, parseUnifiedSessionDocument } from './unified-session.js';
import { experimentAssignmentArtifact, parseExperimentAssignments } from './experiment-evidence.js';
import { parseGraderArtifacts } from './grader-evidence.js';
import { unifiedSessionEventType } from '../model/session-types.js';

const debug = createDebug('data:ingestion:jsonl');

const OBSERVATION_SOURCE = 'gh-aw-logs';
const REPOSITORY_COORDINATE_PATTERN = /^[A-Za-z0-9][A-Za-z0-9-]*\/[A-Za-z0-9._-]+$/;

/** @param {unknown} version */
export function isSupportedGhAwJsonlSchemaVersion(version) {
  return cachedJsonlExpression.sourceSchemaVersions.some((supported) => supported === version);
}

/** @param {unknown} value @param {string} field */
function objectValue(value, field) {
  if (!value || typeof value !== 'object' || Array.isArray(value)) {
    throw new TypeError(`${field} must be an object`);
  }
  return /** @type {Record<string, unknown>} */ (value);
}

/** @param {unknown} value @param {string} field */
function identifier(value, field) {
  if ((typeof value !== 'string' && typeof value !== 'number') || !String(value).trim()) {
    throw new TypeError(`${field} is required`);
  }
  return String(value).trim();
}

/** @param {unknown} value @param {string} field */
function positiveInteger(value, field) {
  const number = Number(value);
  if (!Number.isInteger(number) || number < 1) throw new TypeError(`${field} must be a positive integer`);
  return number;
}

/** @param {unknown} value */
function optionalString(value) {
  return value === undefined || value === null || value === '' ? undefined : String(value);
}

/** @param {Record<string, unknown>} record */
function safeOutputGithubEntityType(record) {
  if (optionalString(record.provider)?.toLowerCase() !== 'github' && record.provider !== undefined) {
    return undefined;
  }
  const target = record.target && typeof record.target === 'object' && !Array.isArray(record.target)
    ? /** @type {Record<string, unknown>} */ (record.target)
    : {};
  const explicitKind = optionalString(target.kind);
  if (explicitKind) return explicitKind.toLowerCase().replaceAll('-', '_');

  const url = optionalString(record.url);
  if (url) {
    try {
      const parsed = new URL(url);
      if (parsed.hostname.toLowerCase() === 'github.com') {
        if (/\/pull\/\d+(?:\/|$)/.test(parsed.pathname)) return 'pull_request';
        if (/\/issues\/\d+(?:\/|$)/.test(parsed.pathname)) return 'issue';
        if (/\/discussions\/\d+(?:\/|$)/.test(parsed.pathname)) return 'discussion';
        if (/\/projects\/\d+(?:\/|$)/.test(parsed.pathname)) return 'project';
      }
    } catch {
      // Fall through to the safe-output action.
    }
  }

  const action = optionalString(record.type)?.toLowerCase();
  if (!action) return undefined;
  if (action.includes('pull_request_review_comment')) return 'pull_request_review_comment';
  if (action.includes('pull_request_review')) return 'pull_request_review';
  if (action.includes('pull_request')) return 'pull_request';
  if (action.includes('discussion')) return 'discussion';
  if (action.includes('project_status_update')) return 'project_status_update';
  if (action.includes('project')) return 'project';
  if (action.includes('code_scanning_alert')) return 'code_scanning_alert';
  if (action.includes('issue')) return 'issue';
  return undefined;
}

/** @param {unknown} title @param {unknown} event */
function dispatchTargetRepository(title, event) {
  if (event !== 'workflow_dispatch' || typeof title !== 'string') return undefined;
  const parts = title.split('·').map((part) => part.trim());
  if (parts.length !== 3 || !['review', 'live'].includes(parts[2])) return undefined;
  return REPOSITORY_COORDINATE_PATTERN.test(parts[1]) ? parts[1] : undefined;
}

/** @param {unknown} title @param {unknown} event */
function dispatchRolloutMode(title, event) {
  if (event !== 'workflow_dispatch' || typeof title !== 'string') return undefined;
  const parts = title.split('·').map((part) => part.trim());
  return parts.length === 3 && ['review', 'live'].includes(parts[2]) ? parts[2] : undefined;
}

/** @param {Record<string, unknown>} run */
function hasReportIncompleteOutcome(run) {
  const audit = run.audit && typeof run.audit === 'object' && !Array.isArray(run.audit)
    ? /** @type {Record<string, unknown>} */ (run.audit)
    : {};
  const usages = [run.mcp_tool_usage, audit.mcp_tool_usage]
    .filter((value) => value && typeof value === 'object' && !Array.isArray(value))
    .map((value) => /** @type {Record<string, unknown>} */ (value));
  const calls = [
    ...(Array.isArray(run.tool_calls) ? run.tool_calls : []),
    ...usages.flatMap((usage) => Array.isArray(usage.tool_calls) ? usage.tool_calls : [])
  ];
  return calls.some((value) => {
    if (!value || typeof value !== 'object' || Array.isArray(value)) return false;
    const call = /** @type {Record<string, unknown>} */ (value);
    return firstOptionalString(call.tool, call.tool_name) === 'report_incomplete'
      && optionalString(call.status) !== 'success';
  });
}

/** @param {...unknown} values */
function firstOptionalString(...values) {
  return values.map(optionalString).find((value) => value !== undefined);
}

/** @param {string} fallback @param {...unknown} values */
function normalizedId(fallback, ...values) {
  return values
    .map(optionalString)
    .find((value) => value?.trim())
    ?.trim() ?? fallback;
}

/** @param {Record<string, unknown>} value */
function withoutUndefined(value) {
  return Object.fromEntries(Object.entries(value).filter(([, field]) => field !== undefined));
}

/** @param {unknown} value */
function finiteNumber(value) {
  if (value === undefined || value === null || value === '') return null;
  const number = Number(value);
  return Number.isFinite(number) ? number : null;
}

/** @param {unknown} value @param {string} field @param {string[]} allowed */
function enumValue(value, field, allowed) {
  const normalized = requiredString(value, field);
  if (!allowed.includes(normalized)) {
    throw new TypeError(`${field} must be one of ${allowed.join(', ')}`);
  }
  return normalized;
}

/** @param {unknown} value @param {string} field */
function optionalTimestamp(value, field) {
  return value === undefined || value === null
    ? undefined
    : canonicalTimestamp(value, field);
}

/** @param {unknown} value @param {string} field */
function optionalIdentifierArray(value, field) {
  if (value === undefined || value === null) return undefined;
  if (!Array.isArray(value)) throw new TypeError(`${field} must be an array`);
  return value.map((item, index) => identifier(item, `${field}[${index}]`));
}

/** @param {Record<string, unknown>} run */
function runMetadata(run) {
  const awInfo = run.aw_info && typeof run.aw_info === 'object' && !Array.isArray(run.aw_info)
    ? /** @type {Record<string, unknown>} */ (run.aw_info)
    : {};
  const tokenUsage = run.token_usage_summary ?? run.token_usage ?? null;
  const summary = tokenUsage && typeof tokenUsage === 'object' && !Array.isArray(tokenUsage)
    ? /** @type {Record<string, unknown>} */ (tokenUsage)
    : {};
  const byModel = summary.by_model && typeof summary.by_model === 'object' && !Array.isArray(summary.by_model)
    ? /** @type {Record<string, unknown>} */ (summary.by_model)
    : {};
  const dominantModel = Object.entries(byModel)
    .map(([model, usage]) => {
      const record = usage && typeof usage === 'object' && !Array.isArray(usage)
        ? /** @type {Record<string, unknown>} */ (usage)
        : {};
      return { model, aic: finiteNumber(record.aic) ?? 0 };
    })
    .sort((left, right) => right.aic - left.aic || left.model.localeCompare(right.model))[0]?.model;
  const aicTotal = finiteNumber(summary.total_aic) ?? finiteNumber(run.aic);
  /** @type {number|null} */
  let reasoningTokens = finiteNumber(summary.total_reasoning_tokens);
  for (const usage of Object.values(byModel)) {
    if (summary.total_reasoning_tokens !== undefined) break;
    const record = usage && typeof usage === 'object' && !Array.isArray(usage)
      ? /** @type {Record<string, unknown>} */ (usage)
      : {};
    const count = finiteNumber(record.reasoning_tokens);
    if (count !== null) reasoningTokens = (reasoningTokens ?? 0) + count;
  }
  return {
    agentId: normalizedId('copilot', run.agent_id, run.agent, run.engine_id, awInfo.engine_id),
    agentVersion: firstOptionalString(
      run.agent_version,
      run.engine_version,
      awInfo.agent_version,
      awInfo.version
    ),
    modelId: normalizedId('auto', run.model_id, run.resolved_model, run.model, awInfo.model, dominantModel),
    ghAwVersion: firstOptionalString(
      run.gh_aw_version,
      run.ghAwVersion,
      run.cli_version,
      run.version,
      awInfo.cli_version
    ),
    engine: firstOptionalString(run.engine, awInfo.engine_name),
    engineId: firstOptionalString(run.engine_id, awInfo.engine_id),
    engineVersion: firstOptionalString(
      run.engine_version,
      awInfo.agent_version,
      awInfo.version
    ),
    requestedModel: firstOptionalString(run.requested_model, run.requestedModel, run.model, awInfo.model),
    resolvedModel: firstOptionalString(
      run.resolved_model,
      run.resolvedModel,
      run.model_resolved,
      run.model,
      awInfo.model,
      dominantModel
    ),
    agentRuntime: firstOptionalString(run.agent_runtime, awInfo.agent_runtime),
    firewallVersion: firstOptionalString(run.firewall_version, awInfo.firewall_version, awInfo.awf_version),
    gatewayVersion: firstOptionalString(run.gateway_version, awInfo.awmg_version),
    aicTotal,
    inputTokens: finiteNumber(summary.total_input_tokens),
    outputTokens: finiteNumber(summary.total_output_tokens),
    cacheReadTokens: finiteNumber(summary.total_cache_read_tokens),
    cacheWriteTokens: finiteNumber(summary.total_cache_write_tokens),
    reasoningTokens
  };
}

/** @param {Record<string, unknown>} raw @param {Record<string, unknown>} enriched */
function runFailureMetadata(raw, enriched) {
  const jobs = Array.isArray(enriched.job_details) ? enriched.job_details : [];
  const failedJob = jobs.find((job) => {
    if (!job || typeof job !== 'object' || Array.isArray(job)) return false;
    return ['failure', 'timed_out', 'startup_failure'].includes(String(job.conclusion ?? ''));
  });
  const job = failedJob && typeof failedJob === 'object' && !Array.isArray(failedJob)
    ? /** @type {Record<string, unknown>} */ (failedJob)
    : {};
  const steps = Array.isArray(job.steps) ? job.steps : [];
  const failedStep = steps.find((step) => {
    if (!step || typeof step !== 'object' || Array.isArray(step)) return false;
    return ['failure', 'timed_out', 'startup_failure'].includes(String(step.conclusion ?? ''));
  });
  const step = failedStep && typeof failedStep === 'object' && !Array.isArray(failedStep)
    ? /** @type {Record<string, unknown>} */ (failedStep)
    : {};
  const failureLog = firstOptionalString(
    enriched.failure_log,
    enriched.failed_step_log,
    raw.failureLog,
    raw.failure_log
  );
  return {
    failureJob: firstOptionalString(enriched.failure_job, raw.failureJob, raw.failure_job, job.name),
    failureMessage: firstOptionalString(
      enriched.failure_message,
      enriched.failure_detail,
      raw.failureMessage,
      raw.failure_message
    ),
    failureStep: firstOptionalString(enriched.failure_step, raw.failureStep, raw.failure_step, step.name),
    failureLog
  };
}

/** @param {Record<string, unknown>} run */
function runAggregates(run) {
  const audit = run.audit && typeof run.audit === 'object' && !Array.isArray(run.audit)
    ? /** @type {Record<string, unknown>} */ (run.audit)
    : {};
  const firewallValue = Object.hasOwn(run, 'firewall_analysis')
    ? run.firewall_analysis
    : audit.firewall_analysis;
  const firewall = firewallValue && typeof firewallValue === 'object' && !Array.isArray(firewallValue)
    ? /** @type {Record<string, unknown>} */ (firewallValue)
    : {};
  const requestsByDomain = firewall.requests_by_domain
    && typeof firewall.requests_by_domain === 'object'
    && !Array.isArray(firewall.requests_by_domain)
    ? /** @type {Record<string, unknown>} */ (firewall.requests_by_domain)
    : {};
  const hasFirewallAggregate = firewallValue !== null && typeof firewallValue === 'object'
    && !Array.isArray(firewallValue);
  let firewallAllowedCalls = 0;
  let firewallBlockedCalls = 0;
  for (const value of Object.values(requestsByDomain)) {
    if (!value || typeof value !== 'object' || Array.isArray(value)) continue;
    const counts = /** @type {Record<string, unknown>} */ (value);
    firewallAllowedCalls += Math.max(0, finiteNumber(counts.allowed) ?? 0);
    firewallBlockedCalls += Math.max(0, finiteNumber(counts.blocked) ?? 0);
  }

  const mcpValue = Object.hasOwn(run, 'mcp_tool_usage')
    ? run.mcp_tool_usage
    : audit.mcp_tool_usage;
  const mcp = mcpValue && typeof mcpValue === 'object' && !Array.isArray(mcpValue)
    ? /** @type {Record<string, unknown>} */ (mcpValue)
    : {};
  const toolCalls = Array.isArray(mcp.tool_calls) ? mcp.tool_calls : [];
  const hasMcpAggregate = mcpValue !== null && typeof mcpValue === 'object'
    && !Array.isArray(mcpValue);
  const mcpResponseBytes = toolCalls.reduce((total, value) => {
    if (!value || typeof value !== 'object' || Array.isArray(value)) return total;
  const call = /** @type {Record<string, unknown>} */ (value);
  return total + Math.max(0, finiteNumber(call.output_size ?? call.response_size) ?? 0);
  }, 0);

  const graders = run.graders && typeof run.graders === 'object' && !Array.isArray(run.graders)
    ? /** @type {Record<string, unknown>} */ (run.graders)
    : {};
  const operationalGraderResults = (Array.isArray(graders.results) ? graders.results : [])
    .filter((value) => value && typeof value === 'object' && !Array.isArray(value))
    .map((value) => /** @type {Record<string, unknown>} */ (value))
    .filter((value) => value.id === 'operational-value' || value.source === 'operational-value')
    .map((value) => {
      const primaryMetric = Array.isArray(value.metrics) ? value.metrics[0] : undefined;
      return finiteNumber(primaryMetric && typeof primaryMetric === 'object' ? primaryMetric.value : value.value);
    })
    .filter((value) => value !== null);

  const auditItems = ['key_findings', 'observability_insights', 'recommendations']
    .flatMap((field) => Array.isArray(audit[field]) ? audit[field] : [])
    .filter((value) => value && typeof value === 'object' && !Array.isArray(value))
    .map((value) => /** @type {Record<string, unknown>} */ (value));
  const hasPriorityAggregate = ['key_findings', 'observability_insights', 'recommendations']
    .some((field) => Array.isArray(audit[field]));
  /** @param {string} priority */
  const priorityCount = (priority) => auditItems.filter((item) =>
    optionalString(item.severity ?? item.priority)?.toLowerCase() === priority).length;
  const startedAt = timestamp(run.started_at ?? run.created_at);
  const completedAt = run.status === 'completed' ? timestamp(run.completed_at ?? run.updated_at) : null;

  return {
    agenticDurationSeconds: startedAt && completedAt
      ? Math.max(0, (Date.parse(completedAt) - Date.parse(startedAt)) / 1000)
      : null,
    firewallAllowedCalls: hasFirewallAggregate ? firewallAllowedCalls : null,
    firewallBlockedCalls: hasFirewallAggregate ? firewallBlockedCalls : null,
    mcpToolCalls: hasMcpAggregate ? toolCalls.length : null,
    mcpResponseBytes: hasMcpAggregate ? mcpResponseBytes : null,
    operationalGrader: operationalGraderResults.length === 1 ? operationalGraderResults[0] : null,
    highPriorityAuditItems: hasPriorityAggregate ? priorityCount('high') : null,
    mediumPriorityAuditItems: hasPriorityAggregate ? priorityCount('medium') : null
  };
}

/** @param {string} content @param {string} filePath */
function parseJsonl(content, filePath) {
  return content.split(/\r?\n/).flatMap((line, index) => {
    if (!line.trim()) return [];
    let value;
    try {
      value = JSON.parse(line);
    } catch (error) {
      throw new TypeError(`${filePath}:${index + 1} must contain valid JSON`, { cause: error });
    }
    return [{ value: objectValue(value, `${filePath}:${index + 1}`), line: index + 1 }];
  });
}

/** @param {unknown} value */
function timestamp(value) {
  if (typeof value === 'number' && Number.isFinite(value) && value > 0) {
    return new Date(value * 1000).toISOString();
  }
  if (typeof value !== 'string' || !value.trim() || !Number.isFinite(Date.parse(value))) return null;
  return new Date(value).toISOString();
}

/** @param {unknown} candidate @param {string} githubRunId @param {string} canonicalRunId @param {string} observedAt @param {unknown} [attempt] */
export function classifiedEval(candidate, githubRunId, canonicalRunId, observedAt, attempt) {
  if (!candidate || typeof candidate !== 'object' || Array.isArray(candidate)) return null;
  const record = /** @type {Record<string, unknown>} */ (candidate);
  const name = typeof record.id === 'string' && record.id.trim() ? record.id : undefined;
  const answer = typeof record.answer === 'string' ? record.answer.trim().toUpperCase() : undefined;
  const eventAt = timestamp(record.timestamp === undefined ? observedAt : record.timestamp);
  if (!name || !['YES', 'NO', 'UNKNOWN'].includes(answer ?? '') || !eventAt) return null;
  for (const field of ['runId', 'run_id', 'runid']) {
    if (record[field] === undefined) continue;
    const reported = record[field];
    if ((typeof reported !== 'string' && typeof reported !== 'number')
        || (String(reported) !== githubRunId && String(reported) !== canonicalRunId)) return null;
  }
  for (const field of ['runAttempt', 'run_attempt']) {
    if (record[field] !== undefined && (!Number.isInteger(Number(record[field]))
        || Number(record[field]) < 1 || Number(record[field]) !== Number(attempt))) return null;
  }
  return { name, answer, eventAt };
}

/** @param {Record<string, unknown>} record @param {Record<string, unknown>} assignments @param {string} workflow */
function experimentLink(record, assignments, workflow) {
  const name = record.experimentId ?? record.experiment_id;
  const assigned = typeof name === 'string' && Object.hasOwn(assignments, name) ? assignments[name] : undefined;
  if (typeof name !== 'string' || !name.trim() || typeof assigned !== 'string' || !assigned.trim()
      || (record.variant !== undefined && record.variant !== assigned)) return {};
  return { experimentId: sourceId('experiment', OBSERVATION_SOURCE, `${workflow}:${name}`), variant: assigned };
}

/** @param {Record<string, unknown>} run */
function sessionRunEvidence(run) {
  const session = run.cao_unified_session;
  if (session === undefined) return run;
  const evidence = objectValue(session, 'run.cao_unified_session');
  if (evidence.version !== 1) throw new TypeError('Unsupported unified session evidence version');
  const projected = { ...run };
  for (const field of ['experiments', 'graders', 'evals']) {
    if (evidence[field] !== undefined) projected[field] = evidence[field];
  }
  if (evidence.run !== undefined) {
    const metadata = objectValue(evidence.run, 'unified session run metadata');
    for (const field of ['engine_id', 'agent_version', 'cli_version', 'firewall_version', 'gateway_version',
      'model', 'requested_model', 'resolved_model', 'safe_outputs', 'safe_items_count']) {
      if (metadata[field] !== undefined) projected[field] = metadata[field];
    }
    if (metadata.engine_id !== undefined) projected.agent_id = metadata.engine_id;
    if (metadata.resolved_model !== undefined || metadata.model !== undefined) {
      projected.model_id = metadata.resolved_model ?? metadata.model;
    }
    if (metadata.token_usage_summary !== undefined) {
      const existing = projected.token_usage_summary ?? projected.token_usage;
      projected.token_usage_summary = {
        ...(existing && typeof existing === 'object' && !Array.isArray(existing) ? existing : {}),
        ...objectValue(metadata.token_usage_summary, 'session token usage')
      };
    }
  }
  return projected;
}

/** @param {string} runKey @param {number} index @param {string} name */
function evalAuditId(runKey, index, name) {
  return sourceId('audit', OBSERVATION_SOURCE,
    `${runKey}:agentic:workflow_run_eval:${stableDigest({ type: 'eval', index, name })}`);
}

/** @param {unknown} value */
function text(value) {
  return value === undefined || value === null ? '' : String(value);
}

/** @param {unknown} value */
function firewallDomain(value) {
  const host = text(value).trim();
  if (!host || host === '-') return '';
  try {
    return new URL(host.includes('://') ? host : `https://${host}`).hostname;
  } catch {
    return host.replace(/:\d+$/, '');
  }
}

/** @param {string[]} parts */
function detail(parts) {
  return parts.filter(Boolean).join('/');
}

/** @param {unknown} value */
function stableDigest(value) {
  const input = JSON.stringify(value);
  let hash = 0x811c9dc5;
  for (let index = 0; index < input.length; index += 1) {
    hash ^= input.charCodeAt(index);
    hash = Math.imul(hash, 0x01000193);
  }
  return (hash >>> 0).toString(16).padStart(8, '0');
}

/**
 * @param {string} type
 * @param {Record<string, unknown>} fields
 * @returns {'domain' | 'tool' | 'skill' | 'friction' | 'audit' | 'issue'}
 */
function recordKind(type, fields) {
  if (type === 'firewall.http_access') return 'domain';
  if (type === 'safe_output.result'
    && ['issue', 'pull_request'].includes(String(fields.githubEntityType))
    && typeof fields.correlationId === 'string') {
    try {
      issueCoordinates(fields.correlationId);
      return 'issue';
    } catch {
      return 'audit';
    }
  }
  if (type === 'skill.invoked') return 'skill';
  if (type === 'workflow_run_friction') return 'friction';
  if (fields.source === 'mcp'
    || type === 'mcp.tool_call'
    || (type === 'mcp.rpc.request' && fields.source === 'gateway')
    || type === 'tool.execution_start'
    || type === 'tool.execution_complete'
    || type === 'mcp.guard.blocked'
    || type === 'mcp.difc.filtered') return 'tool';
  return 'audit';
}

/** @param {string} type @param {Record<string, unknown>} fields @param {'domain' | 'tool' | 'skill' | 'friction' | 'audit' | 'issue'} kind */
function specializedFields(type, fields, kind) {
  if (kind === 'issue') {
    const url = requiredString(fields.correlationId, 'safe output URL');
    const { owner, repository, number } = issueCoordinates(url);
    return {
      id: issueId(owner, repository, number),
      owner,
      repository,
      repositoryFullName: `${owner}/${repository}`,
      number,
      isPullRequest: fields.githubEntityType === 'pull_request',
      url,
      state: optionalString(fields.issueState),
      closed: typeof fields.issueClosed === 'boolean' ? fields.issueClosed : undefined,
      stateReason: fields.issueStateReason === null ? null : optionalString(fields.issueStateReason),
      closedAt: fields.issueClosedAt === null ? null : optionalString(fields.issueClosedAt),
      statusObservedAt: optionalString(fields.issueStatusObservedAt)
    };
  }
  if (kind === 'tool') {
    const name = optionalString(fields.mcpTool ?? fields.toolName ?? fields.summary) ?? 'unknown';
    const isBash = /(^|[/.:_-])(bash|shell)(?:$|[/.:_-])/i.test(name);
    return {
      toolType: isBash ? 'bash' : 'mcp',
      name
    };
  }
  if (kind === 'skill') {
    return {
      name: optionalString(fields.name ?? fields.summary) ?? 'unknown',
      invocationCount: Math.max(1, finiteNumber(fields.invocationCount) ?? 1),
      failedCount: Math.max(0, finiteNumber(fields.failedCount) ?? 0),
      activationSource: optionalString(fields.activationSource)
    };
  }
  return {};
}

/** @param {string} repositoryName @param {string} [fallbackOwner] */
function repositoryCoordinates(repositoryName, fallbackOwner = '') {
  const parts = repositoryName.split('/').filter(Boolean);
  const owner = parts.length === 2 ? parts[0] : fallbackOwner;
  const name = parts.length === 2 ? parts[1] : repositoryName;
  if (!owner || !name) throw new TypeError(`Repository must use OWNER/REPOSITORY form: ${repositoryName}`);
  return { owner, name, fullName: `${owner}/${name}` };
}

/**
 * @template {{ line: number, observedAt: string, value: Record<string, unknown> }} T
 * @param {T} left
 * @param {T} right
 * @returns {T}
 */
function preferNewer(left, right) {
  const rightIsNewer = Date.parse(right.observedAt) > Date.parse(left.observedAt)
    || (right.observedAt === left.observedAt && right.line > left.line);
  const newer = rightIsNewer ? right : left;
  const older = rightIsNewer ? left : right;
  const attempt = (/** @type {Record<string, unknown>} */ value) => Number(value.run_attempt ?? value.attempt ?? 1);
  if (attempt(newer.value) !== attempt(older.value)) return newer;
  const value = { ...older.value, ...newer.value };
  for (const field of ['aic', 'token_usage_summary', 'token_usage']) {
    if (value[field] == null && older.value[field] != null) value[field] = older.value[field];
  }
  for (const field of ['token_usage_summary', 'token_usage']) {
    const previous = older.value[field];
    const current = newer.value[field];
    if (!previous || typeof previous !== 'object' || Array.isArray(previous)
        || !current || typeof current !== 'object' || Array.isArray(current)) continue;
    const previousSummary = objectValue(previous, `run.${field}`);
    const summary = { ...previousSummary, ...objectValue(current, `run.${field}`) };
    for (const key of ['total_aic', 'total_input_tokens', 'total_output_tokens',
      'total_cache_read_tokens', 'total_cache_write_tokens']) {
      if (summary[key] == null && typeof previousSummary[key] === 'number' && Number.isFinite(previousSummary[key])) {
        summary[key] = previousSummary[key];
      }
    }
    value[field] = summary;
  }
  const currentAic = runMetadata(newer.value).aicTotal;
  const summaryField = value.token_usage_summary != null ? 'token_usage_summary' : 'token_usage';
  const summary = value[summaryField];
  if (currentAic !== null && summary && typeof summary === 'object' && !Array.isArray(summary)) {
    value[summaryField] = { ...objectValue(summary, `run.${summaryField}`), total_aic: currentAic };
  }
  return {
    ...newer,
    value
  };
}

/** @param {unknown} input */
function logFiles(input) {
  if (!Array.isArray(input)) throw new TypeError('gh-aw logs files must be an array');
  return input.map((candidate, index) => {
    const file = objectValue(candidate, `gh-aw logs file ${index}`);
    const content = file.content;
    if (typeof content !== 'string') throw new TypeError(`gh-aw logs file ${index}.content must be a string`);
    return {
      path: requiredString(file.path, `gh-aw logs file ${index}.path`).replaceAll('\\', '/'),
      content
    };
  });
}

/**
 * @param {string} runId
 * @param {string} filePath
 * @param {number} line
 * @param {string} eventTimestamp
 * @param {string} eventSource
 * @param {string} type
 * @param {Record<string, unknown>} [fields]
 * @returns {import('../model/schema.js').CanonicalObservation}
 */
function eventObservation(runId, filePath, line, eventTimestamp, eventSource, type, fields = {}) {
  const recordFields = { source: eventSource, ...fields };
  const canonicalType = unifiedSessionEventType(type);
  const kind = recordKind(canonicalType, recordFields);
  return {
    kind,
    source: OBSERVATION_SOURCE,
    sourceId: `${runId}:${filePath}:${line}`,
    observedAt: eventTimestamp,
    data: {
      runId,
      timestamp: eventTimestamp,
      source: eventSource,
      type: canonicalType,
      payloadRef: `${filePath}#L${line}`,
      sourceSequence: line,
      ...fields,
      ...specializedFields(canonicalType, recordFields, kind)
    }
  };
}

/** @param {string} runId @param {{ path: string, content: string }} file */
function agentEvents(runId, file) {
  let turn = 0;
  return parseJsonl(file.content, file.path).flatMap(({ value, line }) => {
    const eventTimestamp = timestamp(value.timestamp);
    if (!eventTimestamp) return [];
    const data = value.data && typeof value.data === 'object' && !Array.isArray(value.data)
      ? /** @type {Record<string, unknown>} */ (value.data)
      : {};
    switch (value.type) {
      case 'user.message':
        turn += 1;
        return [eventObservation(runId, file.path, line, eventTimestamp, 'agent', 'agent_turn', {
          summary: `turn ${turn}`
        })];
      case 'assistant.message':
        return [eventObservation(runId, file.path, line, eventTimestamp, 'agent', 'assistant_message')];
      case 'reasoning':
      case 'assistant.reasoning':
        return [eventObservation(runId, file.path, line, eventTimestamp, 'agent', 'reasoning')];
      case 'tool.execution_start':
        return [eventObservation(runId, file.path, line, eventTimestamp, 'agent', 'agent_tool_start', {
          summary: detail([text(data.mcpServerName), text(data.toolName)]),
          correlationId: optionalString(data.toolCallId)
        })];
      case 'tool.execution_complete':
        return [eventObservation(runId, file.path, line, eventTimestamp, 'agent', 'agent_tool_done', {
          summary: detail([text(data.mcpServerName), text(data.toolName)]),
          status: data.success === true ? 'success' : 'error',
          correlationId: optionalString(data.toolCallId)
        })];
      default:
        return [];
    }
  });
}

/** @param {string} runId @param {{ path: string, content: string }} file @param {boolean} rpc */
function gatewayEvents(runId, file, rpc) {
  return parseJsonl(file.content, file.path).flatMap(({ value, line }) => {
    const eventTimestamp = timestamp(value.timestamp);
    if (!eventTimestamp) return [];
    if (rpc) {
      if (value.type === 'DIFC_FILTERED') {
        return [eventObservation(runId, file.path, line, eventTimestamp, 'gateway', 'difc_filtered', {
          summary: detail([text(value.server_id), text(value.tool_name)]),
          status: optionalString(value.reason)
        })];
      }
      if (value.type === 'REQUEST' && value.direction === 'OUT') {
        return [eventObservation(runId, file.path, line, eventTimestamp, 'gateway', 'tool_call', {
          summary: optionalString(value.method)
        })];
      }
      return [];
    }
    if (value.type === 'DIFC_FILTERED') {
      return [eventObservation(runId, file.path, line, eventTimestamp, 'gateway', 'difc_filtered', {
        summary: detail([text(value.server_id ?? value.server_name), text(value.tool_name)]),
        status: optionalString(value.reason)
      })];
    }
    if (value.type === 'GUARD_POLICY_BLOCKED') {
      return [eventObservation(runId, file.path, line, eventTimestamp, 'gateway', 'guard_blocked', {
        summary: detail([text(value.server_id ?? value.server_name), text(value.tool_name)]),
        status: optionalString(value.reason)
      })];
    }
    if (value.event === 'tool_call') {
      return [eventObservation(runId, file.path, line, eventTimestamp, 'gateway', 'tool_call', {
        summary: detail([text(value.server_name), text(value.tool_name)]),
        status: value.error ? 'error' : optionalString(value.status),
        correlationId: optionalString(value.tool_call_id)
      })];
    }
    return [];
  });
}

/** @param {string} runId @param {{ path: string, content: string }} file */
function firewallEvents(runId, file) {
  return parseJsonl(file.content, file.path).flatMap(({ value, line }) => {
    const eventTimestamp = timestamp(value.ts);
    const host = text(value.host ?? value.domain);
    const domain = firewallDomain(host);
    if (!eventTimestamp || !domain || value.url === 'error:transaction-end-before-headers') return [];
    const decision = text(value.decision ?? value.squid_request_status);
    const status = Number(value.status ?? value.http_status);
    const blocked = /denied|blocked|reject/i.test(decision)
      || (Number.isFinite(status) && status >= 400 && status < 600);
    return [eventObservation(runId, file.path, line, eventTimestamp, 'firewall', blocked ? 'net_blocked' : 'net_allowed', {
      summary: [host, text(value.method)].filter(Boolean).join(' '),
      status: Number.isFinite(status) && status > 0 ? String(status) : blocked ? 'blocked' : 'allowed',
      domain,
      decision: blocked ? 'denied' : 'allowed',
      requestCount: 1
    })];
  });
}

/**
 * Prefers gh-aw's published unified session, with raw agent, gateway, and
 * firewall JSONL as the legacy fallback for one canonical Run.
 *
 * @param {unknown} input
 * @param {string} runId
 * @returns {import('../model/schema.js').CanonicalObservation[]}
 */
export function adaptGhAwTimelineFiles(input, runId) {
  const canonicalRunId = requiredString(runId, 'gh-aw logs runId');
  const files = logFiles(input);
  const sessions = files.filter((file) => /(^|\/)aw_session\.jsonl$/.test(file.path));
  if (sessions.length > 1) throw new TypeError('Multiple unified sessions found for one run');
  if (sessions.length === 1) {
    const file = sessions[0];
    return sessionObservations(parseUnifiedSession(file.content, file.path), file.path, canonicalRunId);
  }
  const gateway = files.find((file) => /(^|\/)gateway\.jsonl$/.test(file.path));
  const rpc = gateway ? undefined : files.find((file) => /(^|\/)rpc-messages\.jsonl$/.test(file.path));
  return [
    ...(gateway ? gatewayEvents(canonicalRunId, gateway, false) : rpc ? gatewayEvents(canonicalRunId, rpc, true) : []),
    ...files.filter((file) => /firewall.*\/audit\.jsonl$/.test(file.path)).flatMap((file) => firewallEvents(canonicalRunId, file)),
    ...files.filter((file) => /copilot-session-state\/[^/]+\/events\.jsonl$/.test(file.path)).flatMap((file) => agentEvents(canonicalRunId, file))
  ];
}

/** @param {unknown} input @param {string} path @param {string} runId @param {string} [observedAt] */
function sessionObservations(input, path, runId, observedAt) {
  if (!Array.isArray(input)) throw new TypeError('Unified session evidence must be an array');
  const events = input.map((value) => {
    const event = objectValue(value, 'unified session evidence');
    return {
      line: positiveInteger(event.line, 'session line'),
      timestamp: event.timestamp === undefined ? undefined : canonicalTimestamp(event.timestamp, 'session timestamp'),
      source: requiredString(event.source, 'session source'),
      type: requiredString(event.type, 'session type'),
      fields: objectValue(event.fields, 'session fields'),
      ...(event.key === undefined ? {} : { key: requiredString(event.key, 'session event key') })
    };
  });
  return normalizeSessionMcpEvents(events).map((event) => {
    const observation = eventObservation(
      runId, path, event.line,
      canonicalTimestamp(event.timestamp ?? observedAt, 'session observation timestamp'),
      event.source, event.type, event.fields
    );
    if (event.timestamp === undefined) delete observation.data.timestamp;
    if (event.key !== undefined) observation.sourceId += `:${event.key}`;
    return observation;
  });
}

/**
 * Converts raw gh-aw run artifact files into a complete canonical observation
 * graph. The input carries GitHub identity context; events come from the
 * published unified session or legacy agent, gateway, and firewall files.
 *
 * @param {unknown} input
 * @returns {{ observations: import('../model/schema.js').CanonicalObservation[] }}
 */
export function adaptGhAwLogs(input) {
  const document = objectValue(input, 'gh-aw logs input');
  const observedAt = canonicalTimestamp(document.observedAt, 'gh-aw logs observedAt');
  const repository = objectValue(document.repository, 'gh-aw logs repository');
  const workflow = objectValue(document.workflow, 'gh-aw logs workflow');
  const run = objectValue(document.run, 'gh-aw logs run');

  const repositoryGithubId = identifier(repository.githubId, 'repository.githubId');
  const repositoryOwner = requiredString(repository.owner, 'repository.owner');
  const repositoryName = requiredString(repository.name, 'repository.name');
  const canonicalRepositoryId = repositoryCoordinateId(repositoryOwner, repositoryName);
  const workflowGithubId = identifier(workflow.githubId, 'workflow.githubId');
  const workflowPath = requiredString(workflow.path, 'workflow.path');
  const canonicalWorkflowId = workflowCoordinateId(repositoryOwner, repositoryName, workflowPath);
  const githubRunId = identifier(run.githubRunId, 'run.githubRunId');
  const attempt = positiveInteger(run.attempt, 'run.attempt');
  const canonicalRunId = runId(repositoryOwner, repositoryName, githubRunId);
  const files = logFiles(document.files);
  const sessions = files.filter((file) => /(^|\/)aw_session\.jsonl$/.test(file.path));
  if (sessions.length > 1) throw new TypeError('Multiple unified sessions found for one run');
  const unified = sessions[0];
  const sessionEvidence = unified
    ? parseUnifiedSessionDocument(unified.content, unified.path, { runId: githubRunId, attempt }) : undefined;
  const graders = parseGraderArtifacts(files, { runId: githubRunId, attempt },
    /** @type {{ results: Record<string, unknown>[] } | undefined} */ (sessionEvidence?.graders));
  if (sessionEvidence && graders) sessionEvidence.graders = graders;
  const assignmentArtifact = sessionEvidence?.experiments === undefined
    ? experimentAssignmentArtifact(files) : undefined;
  const experiments = assignmentArtifact
    ? parseExperimentAssignments(assignmentArtifact.content, assignmentArtifact.path) : undefined;
  const evalFiles = !sessionEvidence || !Object.hasOwn(sessionEvidence, 'evals')
    ? files.filter((file) => /(^|\/)evals\.jsonl$/.test(file.path)) : [];
  if (evalFiles.length > 1) throw new TypeError('Multiple eval artifacts found for one run');
  const evalFile = evalFiles[0];
  const evals = evalFile ? parseJsonl(evalFile.content, evalFile.path).map(({ value, line }) => ({
    ...value, sessionPayloadRef: `${evalFile.path}#L${line}`
  })) : undefined;
  const events = unified && sessionEvidence
    ? sessionObservations(sessionEvidence.events, unified.path, canonicalRunId, observedAt)
    : adaptGhAwTimelineFiles(document.files, canonicalRunId);

  /** @type {import('../model/schema.js').CanonicalObservation[]} */
  const observations = [
    {
      kind: 'repository', source: OBSERVATION_SOURCE, sourceId: `repository:${repositoryGithubId}`, observedAt,
      data: {
        id: canonicalRepositoryId,
        githubId: repositoryGithubId,
        owner: repositoryOwner,
        name: repositoryName,
        fullName: `${repositoryOwner}/${repositoryName}`,
        visibility: optionalString(repository.visibility) ?? 'unknown'
      }

    },
    {
      kind: 'workflow', source: OBSERVATION_SOURCE, sourceId: `workflow:${workflowGithubId}`, observedAt,
      data: {
        id: canonicalWorkflowId,
        githubId: workflowGithubId,
        repositoryId: canonicalRepositoryId,
        name: requiredString(workflow.name, 'workflow.name'),
        path: workflowSourcePath(workflowPath),
        state: optionalString(workflow.state) ?? 'unknown'
      }
    },
    {
      kind: 'run', source: OBSERVATION_SOURCE, sourceId: `run:${githubRunId}:${attempt}`, observedAt,
      data: {
        githubRunId,
        attempt,
        owner: repositoryOwner,
        repository: repositoryName,
        repositoryFullName: `${repositoryOwner}/${repositoryName}`,
        repositoryId: canonicalRepositoryId,
        workflowId: canonicalWorkflowId,
        workflowPath: workflowSourcePath(workflowPath),
        event: optionalString(run.event) ?? 'unknown',
        status: optionalString(run.status) ?? 'unknown',
        conclusion: optionalString(run.conclusion) ?? null,
        createdAt: optionalString(run.createdAt) ?? null,
        startedAt: optionalString(run.startedAt) ?? null,
        completedAt: optionalString(run.completedAt) ?? null,
        runLink: optionalString(run.url) ?? null
      }
    }
  ];
  observations.push(...events);
  if (unified || experiments || graders || evalFile) {
    const adapted = adaptCachedGhAwJsonl(`${JSON.stringify({
      schema_version: 5, kind: 'run', run: {
        run_id: githubRunId, run_attempt: attempt, organization: repositoryOwner, repository: repositoryName,
        workflow_name: workflow.name, workflow_path: workflowPath, created_at: run.createdAt,
        updated_at: observedAt, experiments, graders,
        ...(evals === undefined ? {} : { evals }),
        ...(unified ? { cao_unified_session: { version: 1, path: unified.path, ...sessionEvidence } } : {})
      }
    })}\n`);
    const nativeRun = adapted.observations.find((record) => record.kind === 'run');
    if (nativeRun) {
      const target = observations.find((record) => record.kind === 'run');
      if (target) {
        for (const field of ['agentId', 'agentVersion', 'modelId', 'ghAwVersion', 'engineId', 'engineVersion',
          'requestedModel', 'resolvedModel', 'firewallVersion', 'gatewayVersion', 'aicTotal',
          'inputTokens', 'outputTokens', 'cacheReadTokens', 'cacheWriteTokens', 'reasoningTokens', 'safeItemsCount']) {
          if (nativeRun.data[field] != null) target.data[field] = nativeRun.data[field];
        }
      }
    }
    observations.push(...adapted.observations.filter((record) =>
      ['experiment', 'experiment-assignment', 'grader', 'grader-observation', 'eval', 'eval-observation'].includes(record.kind)
        || record.kind === 'issue'
        || (record.kind === 'audit' && ['grader.result', 'eval.result', 'workflow_run_eval_unclassified', 'safe_output.result'].includes(String(record.data.type)))));
  }
  return { observations };
}

/**
 * @param {string | Uint8Array} content
 * @param {{ context?: unknown, workflowHints?: { owner: string, repository: string, name: string, path: string }[] }} [options]
 * @returns {{
 *   observations: import('../model/schema.js').CanonicalObservation[],
 *   records: number,
 *   rawPayloadRecords: number,
 *   rawRuns: number,
 *   agenticRunRecords: number,
 *   agenticRuns: number,
 *   duplicateRawRunObservations: number,
 *   duplicateAgenticRunObservations: number,
 *   unenrichedRuns: number,
 *   recordsByKind: Record<string, number>,
 *   safeOutputItems: number,
 *   mappedSafeOutputItems: number,
 *   rateLimits: number,
 *   mappedRateLimits: number
 * }}
 */
export function adaptCachedGhAwJsonl(content, options = {}) {
  const binaryContent = typeof content !== 'string'
    && ArrayBuffer.isView(content)
    && content.BYTES_PER_ELEMENT === 1;
  if (typeof content !== 'string' && !binaryContent) {
    throw new TypeError('gh-aw JSONL must be a string or Uint8Array');
  }
  if (cachedJsonlExpression.contract !== 'gh-aw-cao.jsonl-ingestion'
    || cachedJsonlExpression.version !== 1) {
    throw new TypeError('Unsupported cached gh-aw ingestion expression');
  }
  const knownKinds = new Set(Object.keys(cachedJsonlExpression.variants));
  const decoder = new TextDecoder();
  /** @param {string} line @param {number} lineNumber */
  const parseEnvelope = (line, lineNumber) => {
    let envelope;
    try { envelope = objectValue(JSON.parse(line), `gh-aw JSONL line ${lineNumber}`); } catch (error) {
      throw new TypeError(`gh-aw JSONL line ${lineNumber} must contain valid JSON`, { cause: error });
    }
    if (!isSupportedGhAwJsonlSchemaVersion(envelope.schema_version)) {
      throw new TypeError(
        `Unsupported gh-aw JSONL schema version at line ${lineNumber}: ${String(envelope.schema_version)}`
      );
    }
    const kind = requiredString(envelope.kind, `gh-aw JSONL line ${lineNumber}.kind`);
    return knownKinds.has(kind) ? { envelope, line: lineNumber } : null;
  };
  function* envelopes() {
    let start = 0;
    let lineNumber = 0;
    while (start <= content.length) {
      lineNumber += 1;
      const end = typeof content === 'string'
        ? content.indexOf('\n', start)
        : content.indexOf(10, start);
      const boundary = end === -1 ? content.length : end;
      const line = typeof content === 'string'
        ? content.slice(start, boundary)
        : decoder.decode(content.subarray(start, boundary));
      if (line.trim()) {
        const parsed = parseEnvelope(line, lineNumber);
        if (parsed) yield parsed;
      }
      if (end === -1) break;
      start = end + 1;
    }
  }
  const accumulator = createCachedGhAwJsonlAccumulator(options);
  for (const envelope of envelopes()) accumulator.accept(envelope);
  return accumulator.finish();
}

export function createCachedJsonlPayloadHasher() {
  const hashes = Array.from({ length: 8 }, (_, index) => (0x811c9dc5 ^ (index * 0x9e3779b9)) >>> 0);
  return {
    /** @param {Uint8Array} bytes */
    update(bytes) {
      for (const byte of bytes) {
        for (let index = 0; index < hashes.length; index += 1) {
          hashes[index] = Math.imul(hashes[index] ^ byte, 0x01000193 + (index * 2)) >>> 0;
        }
      }
    },
    digest() {
      return hashes.map((hash) => hash.toString(16).padStart(8, '0')).join('');
    }
  };
}

/** @param {string | Uint8Array} content */
export function cachedJsonlPayloadIdentity(content) {
  const hasher = createCachedJsonlPayloadHasher();
  hasher.update(typeof content === 'string' ? new TextEncoder().encode(content) : content);
  return hasher.digest();
}

/**
 * @param {AsyncIterable<string | Uint8Array>} chunks
 * @param {{ context?: unknown, workflowHints?: { owner: string, repository: string, name: string, path: string }[], onProgress?: (progress: { bytesProcessed: number, linesProcessed: number, recordsIngested: number }) => void, payloadIdentity?: string }} [options]
 */
export async function adaptCachedGhAwJsonlStream(chunks, options = {}) {
  if (cachedJsonlExpression.contract !== 'gh-aw-cao.jsonl-ingestion'
    || cachedJsonlExpression.version !== 1) {
    throw new TypeError('Unsupported cached gh-aw ingestion expression');
  }
  const knownKinds = new Set(Object.keys(cachedJsonlExpression.variants));
  const decoder = new TextDecoder();
  const encoder = new TextEncoder();
  const hasher = options.payloadIdentity === undefined ? createCachedJsonlPayloadHasher() : null;
  const accumulator = createCachedGhAwJsonlAccumulator(options);
  let pending = '';
  let lineNumber = 0;
  let bytesProcessed = 0;
  let recordsIngested = 0;
  let chunksProcessed = 0;
  debug('started streaming JSONL adaptation', { workflowHints: options.workflowHints?.length ?? 0 });
  /** @param {string} line */
  const accept = (line) => {
    lineNumber += 1;
    if (!line.trim()) return;
    let envelope;
    try { envelope = objectValue(JSON.parse(line), `gh-aw JSONL line ${lineNumber}`); } catch (error) {
      throw new TypeError(`gh-aw JSONL line ${lineNumber} must contain valid JSON`, { cause: error });
    }
    if (!isSupportedGhAwJsonlSchemaVersion(envelope.schema_version)) {
      throw new TypeError(
        `Unsupported gh-aw JSONL schema version at line ${lineNumber}: ${String(envelope.schema_version)}`
      );
    }
    const kind = requiredString(envelope.kind, `gh-aw JSONL line ${lineNumber}.kind`);
    if (knownKinds.has(kind)) {
      accumulator.accept({ envelope, line: lineNumber });
      recordsIngested += 1;
    }
  };
  for await (const chunk of chunks) {
    chunksProcessed += 1;
    const bytes = typeof chunk === 'string' ? encoder.encode(chunk) : chunk;
    bytesProcessed += bytes.byteLength;
    hasher?.update(bytes);
    pending += decoder.decode(bytes, { stream: true });
    let start = 0;
    let newline;
    while ((newline = pending.indexOf('\n', start)) !== -1) {
      accept(pending.slice(start, newline));
      start = newline + 1;
    }
    if (start > 0) pending = pending.slice(start);
    debug('adapted JSONL chunk', {
      chunksProcessed,
      chunkBytes: bytes.byteLength,
      bytesProcessed,
      linesProcessed: lineNumber,
      recordsIngested,
      pendingCharacters: pending.length
    });
    options.onProgress?.({ bytesProcessed, linesProcessed: lineNumber, recordsIngested });
  }
  pending += decoder.decode();
  if (pending) accept(pending);
  options.onProgress?.({ bytesProcessed, linesProcessed: lineNumber, recordsIngested });
  const result = {
    ...accumulator.finish(),
    payloadIdentity: options.payloadIdentity ?? hasher?.digest()
  };
  debug('completed streaming JSONL adaptation', {
    chunksProcessed,
    bytesProcessed,
    linesProcessed: lineNumber,
    recordsIngested
  });
  return result;
}

/**
 * @param {{ context?: unknown, workflowHints?: { owner: string, repository: string, name: string, path: string }[] }} options
 */
function createCachedGhAwJsonlAccumulator(options) {

  /**
   * @typedef {{
   *   line: number,
   *   observedAt: string,
   *   value: Record<string, unknown>,
   *   owner: string,
   *   name: string,
   *   fullName: string,
   *   workflowName: string,
   *   workflowPath?: string
   * }} CachedRun
   */

  /** @type {Map<string, CachedRun>} */
  const enrichedRuns = new Map();
  /** @type {{ envelope: Record<string, unknown>, line: number }[]} */
  const rateLimitEnvelopes = [];
  /** @type {Map<string, { value: Record<string, unknown>, line: number }[]>} */
  const safeOutputItemsByRun = new Map();
  /** @type {Map<string, Record<string, unknown>[]>} */
  const tokenEfficiencyObservationsByRun = new Map();
  /** @type {Map<string, Record<string, unknown>[]>} */
  const tokenEfficiencyLifecycleObservationsByRun = new Map();
  /** @type {{ value: Record<string, unknown>, line: number }[]} */
  const operationalValues = [];
  /** @type {Record<string, any>[]} */
  const evidencePackets = [];
  /** @type {Record<string, any>[]} */
  const valueCollections = [];
  /** @type {Map<string, string>} */
  const latestRunByGithubId = new Map();
  /** @type {Map<string, CachedRun>} */
  const rawRuns = new Map();
  const reportIncompleteRunIds = new Set();
  let records = 0;
  let agenticRunRecords = 0;
  let rawPayloadRecords = 0;
  let safeOutputItems = 0;
  /** @type {Map<string, Set<string>>} */
  const workflowPaths = new Map();
  for (const hint of options.workflowHints ?? []) {
    const coordinates = repositoryCoordinates(`${hint.owner}/${hint.repository}`);
    const lookup = `${coordinates.fullName.toLowerCase()}:${hint.name.toLowerCase()}`;
    const paths = workflowPaths.get(lookup) ?? new Set();
    paths.add(hint.path);
    workflowPaths.set(lookup, paths);
  }
  /** @param {{ envelope: Record<string, unknown>, line: number }} record */
  const accept = ({ envelope, line }) => {
    records += 1;
    if (envelope.kind === 'operational_value_evidence') {
      evidencePackets.push(validateOperationalValueEvidence(objectValue(envelope.operational_value_evidence, 'operational_value_evidence')));
      return;
    }
    if (envelope.kind === 'workflow_value_collection') {
      valueCollections.push(validateWorkflowValueCollection(objectValue(envelope.workflow_value_collection, 'workflow_value_collection')));
      return;
    }
    if (envelope.kind === 'github_api_rate_limit') {
      rateLimitEnvelopes.push({ envelope, line });
    }
    if (envelope.kind === 'operational_value') {
      operationalValues.push({
        value: objectValue(envelope.operational_value, `gh-aw JSONL line ${line}.operational_value`),
        line
      });
      return;
    }
    if (envelope.kind === 'safe_output_item') {
      safeOutputItems += 1;
      const safeOutput = objectValue(envelope.safe_output, `gh-aw JSONL line ${line}.safe_output`);
      const githubRunId = identifier(
        safeOutput.run_id,
        `gh-aw JSONL line ${line}.safe_output.run_id`
      );
      const parentRunId = latestRunByGithubId.get(String(githubRunId));
      if (parentRunId) {
        const items = safeOutputItemsByRun.get(parentRunId) ?? [];
        items.push({ value: safeOutput, line });
        safeOutputItemsByRun.set(parentRunId, items);
      }
      return;
    }
    if (envelope.kind === 'token_efficiency_observation') {
      const observation = objectValue(
        envelope.observation,
        `gh-aw JSONL line ${line}.observation`
      );
      const optimizerRunId = identifier(
        observation.optimizerRunId,
        `gh-aw JSONL line ${line}.observation.optimizerRunId`
      );
      positiveInteger(observation.runAttempt ?? 1, `gh-aw JSONL line ${line}.observation.runAttempt`);
      const optimizerRunKey = String(optimizerRunId);
      const observations = tokenEfficiencyObservationsByRun.get(optimizerRunKey) ?? [];
      observations.push({ ...observation, __line: line });
      tokenEfficiencyObservationsByRun.set(optimizerRunKey, observations);
      return;
    }
    if (envelope.kind === 'token_efficiency_lifecycle_observation') {
      const observation = objectValue(
        envelope.observation,
        `gh-aw JSONL line ${line}.observation`
      );
      const optimizerRunId = identifier(
        observation.optimizerRunId,
        `gh-aw JSONL line ${line}.observation.optimizerRunId`
      );
      positiveInteger(
        observation.optimizerRunAttempt,
        `gh-aw JSONL line ${line}.observation.optimizerRunAttempt`
      );
      const optimizerRunKey = String(optimizerRunId);
      const observations = tokenEfficiencyLifecycleObservationsByRun.get(optimizerRunKey) ?? [];
      observations.push({ ...observation, __line: line });
      tokenEfficiencyLifecycleObservationsByRun.set(optimizerRunKey, observations);
      return;
    }
    if (envelope.kind === 'run' || envelope.kind === 'token_efficiency_run_context') {
      if (envelope.kind === 'run') agenticRunRecords += 1;
      const run = objectValue(envelope.run, `gh-aw JSONL line ${line}.run`);
      const organization = requiredString(run.organization, `gh-aw JSONL line ${line}.run.organization`);
      const coordinates = repositoryCoordinates(
        requiredString(run.repository, `gh-aw JSONL line ${line}.run.repository`),
        organization
      );
      const workflowName = requiredString(
        run.workflow_name,
        `gh-aw JSONL line ${line}.run.workflow_name`
      );
      const workflowPath = requiredString(
        run.workflow_path,
        `gh-aw JSONL line ${line}.run.workflow_path`
      );
      const githubRunId = identifier(run.run_id, `gh-aw JSONL line ${line}.run.run_id`);
      const runAttempt = positiveInteger(
        run.run_attempt ?? 1,
        `gh-aw JSONL line ${line}.run.run_attempt`
      );
      const id = runId(coordinates.owner, coordinates.name, githubRunId);
      if (hasReportIncompleteOutcome(run)) reportIncompleteRunIds.add(`${id}:${runAttempt}`);
      const observedAt = canonicalTimestamp(
        run.updated_at ?? run.created_at,
        `gh-aw JSONL line ${line}.run.updated_at`
      );
      const candidate = {
        line,
        observedAt,
        value: run,
        ...coordinates,
        workflowName,
        workflowPath
      };
      enrichedRuns.set(id, enrichedRuns.has(id)
        ? preferNewer(/** @type {CachedRun} */ (enrichedRuns.get(id)), candidate)
        : candidate);
      latestRunByGithubId.set(String(githubRunId), id);
      const lookup = `${coordinates.fullName.toLowerCase()}:${workflowName.toLowerCase()}`;
      const paths = workflowPaths.get(lookup) ?? new Set();
      paths.add(workflowSourcePath(workflowPath));
      workflowPaths.set(lookup, paths);
      return;
    }
    if (envelope.kind !== 'workflow_runs') return;
    const request = objectValue(envelope.request, `gh-aw JSONL line ${line}.request`);
    const coordinates = repositoryCoordinates(
      requiredString(request.repository, `gh-aw JSONL line ${line}.request.repository`)
    );
    if (!Array.isArray(envelope.payload)) {
      throw new TypeError(`gh-aw JSONL line ${line}.payload must be an array`);
    }
    for (const [payloadIndex, candidateValue] of envelope.payload.entries()) {
      rawPayloadRecords += 1;
      const run = objectValue(candidateValue, `gh-aw JSONL line ${line}.payload[${payloadIndex}]`);
      const githubRunId = identifier(
        run.databaseId,
        `gh-aw JSONL line ${line}.payload[${payloadIndex}].databaseId`
      );
      positiveInteger(
        run.attempt ?? 1,
        `gh-aw JSONL line ${line}.payload[${payloadIndex}].attempt`
      );
      const workflowName = requiredString(
        run.workflowName,
        `gh-aw JSONL line ${line}.payload[${payloadIndex}].workflowName`
      );
      const observedAt = canonicalTimestamp(
        run.updatedAt ?? run.createdAt,
        `gh-aw JSONL line ${line}.payload[${payloadIndex}].updatedAt`
      );
      const id = runId(coordinates.owner, coordinates.name, githubRunId);
      const candidate = {
        line,
        observedAt,
        value: run,
        ...coordinates,
        workflowName,
        workflowPath: undefined
      };
      rawRuns.set(id, rawRuns.has(id)
        ? preferNewer(/** @type {CachedRun} */ (rawRuns.get(id)), candidate)
        : candidate);
    }
  };

  const finish = () => {
    for (const run of rawRuns.values()) {
      const paths = workflowPaths.get(
        `${run.fullName.toLowerCase()}:${run.workflowName.toLowerCase()}`
      );
      run.workflowPath = paths?.size === 1 ? [...paths][0] : undefined;
    }
  /** @type {import('../model/schema.js').CanonicalObservation[]} */
  const observations = [];
  /** @type {Map<string, import('../model/schema.js').CanonicalObservation>} */
  const repositories = new Map();
  /** @type {Map<string, import('../model/schema.js').CanonicalObservation>} */
  const workflows = new Map();
  for (const packet of evidencePackets) {
    const coordinates = repositoryCoordinates(packet.repository);
    const parentId = repositoryCoordinateId(coordinates.owner, coordinates.name);
    const workflowId = workflowCoordinateId(coordinates.owner, coordinates.name, packet.sourcePath);
    const contract = packet.facts.find((/** @type {Record<string, unknown>} */ fact) => fact['fact-kind'] === 'contract');
    repositories.set(parentId, {
      kind: 'repository', source: OBSERVATION_SOURCE, sourceId: packet.repository, observedAt: packet.observedAt,
      data: { id: parentId, owner: coordinates.owner, name: coordinates.name }
    });
    workflows.set(workflowId, {
      kind: 'workflow', source: OBSERVATION_SOURCE, sourceId: packet.sourcePath, observedAt: packet.observedAt,
      data: { id: workflowId, repositoryId: parentId, name: contract['workflow-name'] ?? packet.workflow,
        path: packet.sourcePath, state: 'unknown' }
    });
    observations.push({
      kind: 'operational-value-evidence', source: OBSERVATION_SOURCE, sourceId: packet.id,
      observedAt: packet.observedAt, data: { ...packet, repositoryId: parentId, workflowId }
    });
  }
  for (const record of valueCollections) {
    const [owner, repository] = record.repository.split('/');
    const parentId = repositoryCoordinateId(owner, repository);
    const workflowId = workflowCoordinateId(owner, repository, record.sourcePath);
    observations.push({
      kind: 'repository', source: OBSERVATION_SOURCE, sourceId: record.repository, observedAt: record.observedAt,
      data: { id: parentId, owner, name: repository }
    }, {
      kind: 'workflow', source: OBSERVATION_SOURCE, sourceId: record.sourcePath, observedAt: record.observedAt,
      data: { id: workflowId, repositoryId: parentId, name: record.workflow, path: record.sourcePath, state: 'unknown' }
    }, {
      kind: 'workflow-value-collection', source: OBSERVATION_SOURCE, sourceId: record.id, observedAt: record.observedAt,
      data: { ...record, repositoryId: parentId, workflowId }
    });
  }
  for (const { value, line } of operationalValues) {
    const repository = requiredString(value.repository, `gh-aw JSONL line ${line}.operational_value.repository`);
    const coordinates = repositoryCoordinates(repository);
    const observedAt = canonicalTimestamp(
      value.timestamp,
      `gh-aw JSONL line ${line}.operational_value.timestamp`
    );
    const valueId = requiredString(value.value_id, `gh-aw JSONL line ${line}.operational_value.value_id`);
    const campaign = typeof value.campaign === 'string' && value.campaign.trim()
      ? value.campaign.trim()
      : undefined;
    const campaignId = typeof value.campaign_id === 'string' && value.campaign_id.trim()
      ? value.campaign_id.trim()
      : campaign ? sourceId('campaign', 'inventory', campaign) : undefined;
    const period = operationalValuePeriod(Object.fromEntries(
      Object.entries(OPERATIONAL_VALUE_PERIOD_FIELDS).flatMap(([key, canonical]) => {
        const transport = canonical.replaceAll('-', '_');
        return Object.hasOwn(value, transport) ? [[key, value[transport]]] : [];
      })
    ), observedAt, `gh-aw JSONL line ${line}.operational_value`);
    const metric = finiteNumber(value.value);
    const unavailable = value.value === null && period.windowStart
      && period.missingReason && value.maturity_status === 'unavailable';
    if (metric === null && !unavailable) {
      throw new TypeError(`gh-aw JSONL line ${line}.operational_value.value must be a finite number`);
    }
    const hasRollupNumerator = Object.hasOwn(value, 'rollup_numerator');
    const cadenceDays = value.cadence_days;
    if (Object.hasOwn(value, 'cadence_days') && (
      typeof cadenceDays !== 'number' || !Number.isFinite(cadenceDays) || cadenceDays <= 0
    )) {
      throw new TypeError(`gh-aw JSONL line ${line}.operational_value.cadence_days must be a positive finite number`);
    }
    const evaluatorDigest = value.evaluator_digest;
    if (Object.hasOwn(value, 'evaluator_digest') && (
      typeof evaluatorDigest !== 'string' || !/^[0-9a-f]{64}$/.test(evaluatorDigest)
    )) {
      throw new TypeError(`gh-aw JSONL line ${line}.operational_value.evaluator_digest must be a SHA-256 fingerprint`);
    }
    const hasRollupDenominator = Object.hasOwn(value, 'rollup_denominator');
    const rollupNumerator = finiteNumber(value.rollup_numerator);
    const rollupDenominator = finiteNumber(value.rollup_denominator);
    if (
      hasRollupNumerator !== hasRollupDenominator
      || (metric === null && hasRollupNumerator)
      || (hasRollupNumerator && (
        rollupNumerator === null
        || rollupDenominator === null
        || rollupNumerator < 0
        || rollupDenominator <= 0
      ))
    ) {
      throw new TypeError(`gh-aw JSONL line ${line}.operational_value must contain a valid rollup numerator and denominator together`);
    }
    const repositoryId = repositoryCoordinateId(coordinates.owner, coordinates.name);
    if (value.workflow_path !== undefined) {
      const slug = requiredString(value.workflow_slug, 'operational value workflow filename stem');
      const workflowPath = requiredString(value.workflow_path, 'operational value workflow path');
      if (!/^[a-z0-9]+(?:-[a-z0-9]+)*$/.test(slug) || workflowPath !== `.github/workflows/${slug}.md`) {
        throw new TypeError('Operational value workflow path must match its observed filename stem');
      }
      const workflowId = workflowCoordinateId(coordinates.owner, coordinates.name, workflowPath);
      workflows.set(workflowId, {
        kind: 'workflow', source: OBSERVATION_SOURCE, sourceId: workflowPath, observedAt,
        data: { id: workflowId, repositoryId, name: optionalString(value.workflow_name) ?? slug,
          path: workflowPath, state: 'unknown' }
      });
    }
    repositories.set(repositoryId, {
      kind: 'repository',
      source: OBSERVATION_SOURCE,
      sourceId: `repository:${coordinates.fullName.toLowerCase()}`,
      observedAt,
      data: {
        id: repositoryId,
        owner: coordinates.owner,
        name: coordinates.name,
        fullName: coordinates.fullName,
        visibility: 'unknown'
      }
    });
    observations.push({
      kind: 'operational-value',
      source: OBSERVATION_SOURCE,
      sourceId: `operational-value:${coordinates.fullName.toLowerCase()}:${valueId}:${observedAt}`,
      observedAt,
      data: {
        repositoryId,
        repository: coordinates.fullName,
        campaignId,
        campaign,
        valueId,
        value: metric,
        'operational-value-role': optionalString(value.metric_role) ?? 'primary',
        'operational-value-name': optionalString(value.metric_name) ?? valueId,
        'operational-value-unit': optionalString(value.metric_unit),
        'operational-value-direction': optionalString(value.metric_direction) ?? 'increase',
        'maturity-status': optionalString(value.maturity_status) ?? 'matured',
        'adoption-at': optionalString(value.adoption_at),
        'evaluation-mode': optionalString(value.evaluation_mode),
        'workflow-slug': optionalString(value.workflow_slug),
        'workflow-name': optionalString(value.workflow_name),
        'evaluator-digest': evaluatorDigest,
        ...Object.fromEntries(Object.entries(OPERATIONAL_VALUE_PERIOD_FIELDS)
          .flatMap(([key, canonical]) => Object.hasOwn(period, key) ? [[canonical, period[key]]] : [])),
        ...(cadenceDays !== undefined ? { 'cadence-days': cadenceDays } : {}),
        ...(hasRollupNumerator ? {
          'rollup-numerator': rollupNumerator,
          'rollup-denominator': rollupDenominator
        } : {}),
        timestamp: observedAt
      }
    });
  }

  /** @param {CachedRun} candidate */
  const structuralIds = (candidate) => {
    const repositorySourceId = candidate.fullName.toLowerCase();
    const canonicalWorkflowPath = candidate.workflowPath
      ? workflowSourcePath(candidate.workflowPath)
      : undefined;
    const workflowCoordinate = canonicalWorkflowPath
      ? `${repositorySourceId}:path:${canonicalWorkflowPath}`
      : `${repositorySourceId}:name:${candidate.workflowName.toLowerCase()}`;
    const repository = repositoryCoordinateId(candidate.owner, candidate.name);
    const workflow = canonicalWorkflowPath
      ? workflowCoordinateId(candidate.owner, candidate.name, canonicalWorkflowPath)
      : sourceId('workflow', OBSERVATION_SOURCE, workflowCoordinate);
    repositories.set(repository, {
      kind: 'repository',
      source: OBSERVATION_SOURCE,
      sourceId: `repository:${repositorySourceId}`,
      observedAt: candidate.observedAt,
      data: {
        id: repository,
        owner: candidate.owner,
        name: candidate.name,
        fullName: candidate.fullName,
        visibility: 'unknown'
      }
    });
    workflows.set(workflow, {
      kind: 'workflow',
      source: OBSERVATION_SOURCE,
      sourceId: `workflow:${workflowCoordinate}`,
      observedAt: candidate.observedAt,
      data: {
        id: workflow,
        repositoryId: repository,
        name: candidate.workflowName,
        path: canonicalWorkflowPath,
        state: 'unknown'
      }
    });
    return { repository, workflow };
  };

  const runIds = new Set([...rawRuns.keys(), ...enrichedRuns.keys()]);
  for (const id of [...runIds].sort()) {
    const raw = rawRuns.get(id);
    const enriched = enrichedRuns.get(id);
    const structural = /** @type {CachedRun} */ (enriched ?? raw);
    const { repository, workflow } = structuralIds(structural);
    const rawValue = raw?.value ?? {};
    const enrichedValue = enriched && (!raw || Number(raw.value.attempt ?? 1) === Number(enriched.value.run_attempt ?? 1))
      ? sessionRunEvidence(enriched.value) : {};
    const githubRunId = identifier(
      rawValue.databaseId ?? enrichedValue.run_id,
      `${id}.githubRunId`
    );
    const attempt = positiveInteger(
      rawValue.attempt ?? enrichedValue.run_attempt ?? 1,
      `${id}.attempt`
    );
    const metadata = runMetadata(enrichedValue);
    const failure = runFailureMetadata(rawValue, enrichedValue);
    const aggregates = runAggregates(enrichedValue);
    const reportIncomplete = reportIncompleteRunIds.has(`${id}:${attempt}`);
    const title = optionalString(rawValue.displayTitle)
      ?? optionalString(enrichedValue.display_title)
      ?? `Run ${githubRunId}`;
    const event = optionalString(rawValue.event)
      ?? optionalString(enrichedValue.event)
      ?? optionalString(enrichedValue.event_name)
      ?? 'unknown';
    const status = optionalString(rawValue.status)
      ?? optionalString(enrichedValue.status)
      ?? 'unknown';
    const updatedAt = optionalString(rawValue.updatedAt)
      ?? optionalString(enrichedValue.updated_at)
      ?? null;
    const observedAt = raw && enriched
      ? (Date.parse(raw.observedAt) >= Date.parse(enriched.observedAt) ? raw.observedAt : enriched.observedAt)
      : /** @type {CachedRun} */ (raw ?? enriched).observedAt;
    observations.push({
      kind: 'run',
      source: OBSERVATION_SOURCE,
      sourceId: `run:${githubRunId}:${attempt}`,
      observedAt,
      data: withoutUndefined({
        id,
        repositoryId: repository,
        workflowId: workflow,
        owner: structural.owner,
        repository: structural.name,
        repositoryFullName: structural.fullName,
        workflowPath: structural.workflowPath ? workflowSourcePath(structural.workflowPath) : undefined,
        githubRunId,
        attempt,
        title,
        event,
        targetRepository: dispatchTargetRepository(title, event),
        rolloutMode: dispatchRolloutMode(title, event)
          ?? firstOptionalString(rawValue.rolloutMode, enrichedValue.rollout_mode, enrichedValue.rolloutMode),
        status,
        conclusion: optionalString(rawValue.conclusion)
          ?? optionalString(enrichedValue.conclusion)
          ?? null,
        branch: optionalString(rawValue.headBranch) ?? optionalString(enrichedValue.branch),
        headSha: optionalString(rawValue.headSha) ?? optionalString(enrichedValue.head_sha),
        createdAt: optionalString(rawValue.createdAt)
          ?? optionalString(enrichedValue.created_at)
          ?? null,
        startedAt: optionalString(rawValue.startedAt)
          ?? optionalString(enrichedValue.started_at)
          ?? null,
        completedAt: status === 'completed' ? updatedAt : null,
        updatedAt,
        runLink: optionalString(rawValue.url) ?? optionalString(enrichedValue.url) ?? null,
        classification: optionalString(enrichedValue.classification),
        failureKind: optionalString(enrichedValue.failure_kind),
        failureJob: failure.failureJob,
        failureMessage: failure.failureMessage,
        failureStep: failure.failureStep,
        failureLog: failure.failureLog,
        failureDetail: failure.failureMessage ?? failure.failureStep,
        terminalOutcome: reportIncomplete ? 'report_incomplete' : undefined,
        terminalOutcomeDetail: reportIncomplete
          ? 'The agent reported that required evidence or access was unavailable.'
          : undefined,
        duration: optionalString(enrichedValue.duration),
        actionMinutes: finiteNumber(enrichedValue.action_minutes),
        agentId: metadata.agentId ?? null,
        agentVersion: metadata.agentVersion ?? null,
        modelId: metadata.modelId ?? null,
        ...aggregates,
        ghAwVersion: metadata.ghAwVersion ?? null,
        engine: metadata.engine ?? 'unknown',
        engineId: metadata.engineId,
        engineVersion: metadata.engineVersion,
        requestedModel: metadata.requestedModel,
        resolvedModel: metadata.resolvedModel,
        agentRuntime: metadata.agentRuntime,
        firewallVersion: metadata.firewallVersion,
        gatewayVersion: metadata.gatewayVersion,
        aicTotal: metadata.aicTotal,
        inputTokens: metadata.inputTokens,
        outputTokens: metadata.outputTokens,
        cacheReadTokens: metadata.cacheReadTokens,
        cacheWriteTokens: metadata.cacheWriteTokens,
        reasoningTokens: metadata.reasoningTokens,
        githubApiCalls: finiteNumber(enrichedValue.github_api_calls),
        safeItemsCount: finiteNumber(enrichedValue.safe_items_count),
        errorCount: finiteNumber(enrichedValue.error_count)
      })
    });
    if (enriched) {
      const runKey = runId(structural.owner, structural.name, githubRunId);
      const experimentData = enrichedValue.experiments && typeof enrichedValue.experiments === 'object'
        && !Array.isArray(enrichedValue.experiments)
        ? /** @type {Record<string, unknown>} */ (enrichedValue.experiments)
        : {};
      const assignments = experimentData.assignments && typeof experimentData.assignments === 'object'
        && !Array.isArray(experimentData.assignments)
        ? /** @type {Record<string, unknown>} */ (experimentData.assignments)
        : {};
      for (const [name, variant] of Object.entries(assignments).sort(([a], [b]) => a.localeCompare(b))) {
        if (!name.trim() || typeof variant !== 'string' || !variant.trim()) continue;
        const experimentId = sourceId('experiment', OBSERVATION_SOURCE, `${workflow}:${name}`);
        observations.push({
          kind: 'experiment', source: OBSERVATION_SOURCE,
          sourceId: `${workflow}:experiment:${name}`, observedAt: enriched.observedAt,
          data: { id: experimentId, workflowId: workflow, name, timestamp: enriched.observedAt }
        }, {
          kind: 'experiment-assignment', source: OBSERVATION_SOURCE,
          sourceId: `${runKey}:experiment:${name}`, observedAt: enriched.observedAt,
          data: {
            id: sourceId('experiment-assignment', OBSERVATION_SOURCE, `${runKey}:${name}`),
            runId: runKey, experimentId, variant, timestamp: enriched.observedAt
          }
        });
      }
      const graderData = enrichedValue.graders && typeof enrichedValue.graders === 'object'
        && !Array.isArray(enrichedValue.graders)
        ? /** @type {Record<string, unknown>} */ (enrichedValue.graders)
        : {};
      for (const [index, result] of (Array.isArray(graderData.results) ? graderData.results : []).entries()) {
        if (!result || typeof result !== 'object' || Array.isArray(result)) continue;
        const record = /** @type {Record<string, unknown>} */ (result);
        const name = optionalString(record.id);
        if (!name) continue;
        const resultAt = record.timestamp === undefined
          ? enriched.observedAt : timestamp(record.timestamp);
        if (!resultAt) continue;
        const graderId = sourceId('grader', OBSERVATION_SOURCE, `${workflow}:${name}`);
        observations.push({
          kind: 'grader', source: OBSERVATION_SOURCE,
          sourceId: `${workflow}:grader:${name}`, observedAt: resultAt,
          data: {
            id: graderId, workflowId: workflow, name, sourceGraderId: name,
            displayName: optionalString(record.name) ?? name,
            direction: optionalString(record.direction),
            unit: optionalString(record.unit),
            threshold: finiteNumber(record.threshold),
            timestamp: resultAt
          }
        }, {
          kind: 'grader-observation', source: OBSERVATION_SOURCE,
          sourceId: `${runKey}:grader:${name}:${index}`, observedAt: resultAt,
          data: {
            id: sourceId('grader-observation', OBSERVATION_SOURCE, `${runKey}:${name}:${index}`),
            runId: runKey, graderId, sourceGraderId: name,
            ...experimentLink(record, assignments, workflow),
            value: finiteNumber(record.value),
            status: optionalString(record.status)
              ?? (record.passed === true ? 'passed' : record.passed === false ? 'failed' : 'unavailable'),
            included: typeof record.included === 'boolean' ? record.included : undefined,
            exclusionReason: optionalString(record.exclusionReason ?? record.exclusion_reason),
            evaluatorDigest: optionalString(record.evaluatorDigest ?? record.evaluator_digest),
            graderSource: optionalString(record.source),
            metrics: Array.isArray(record.metrics) ? record.metrics : undefined,
            auditId: sourceId('audit', OBSERVATION_SOURCE,
              `${runKey}:agentic:workflow_run_grader:${stableDigest({ type: 'grader', index, record })}`),
            resultTimestamp: resultAt,
            timestamp: resultAt
          }
        });
      }
      for (const [index, candidate] of (Array.isArray(enrichedValue.evals) ? enrichedValue.evals : []).entries()) {
        const result = classifiedEval(candidate, String(githubRunId), runKey, enriched.observedAt, attempt);
        if (!result) continue;
        const evalId = sourceId('eval', OBSERVATION_SOURCE, `${workflow}:${result.name}`);
        observations.push({
          kind: 'eval', source: OBSERVATION_SOURCE,
          sourceId: `${workflow}:eval:${result.name}`, observedAt: result.eventAt,
          data: {
            id: evalId, workflowId: workflow, name: result.name, sourceEvalId: result.name,
            timestamp: result.eventAt
          }
        }, {
          kind: 'eval-observation', source: OBSERVATION_SOURCE,
          sourceId: `${runKey}:eval:${result.name}:${index}`, observedAt: result.eventAt,
          data: {
            id: sourceId('eval-observation', OBSERVATION_SOURCE, `${runKey}:${result.name}:${index}`),
            runId: runKey, evalId, sourceEvalId: result.name, answer: result.answer,
            ...experimentLink(objectValue(candidate, 'eval'), assignments, workflow),
            evalResult: result.answer,
            status: result.answer === 'YES' ? 'pass' : result.answer === 'NO' ? 'fail' : 'unavailable',
            requestedModel: metadata.requestedModel,
            resolvedModel: metadata.resolvedModel,
            auditId: evalAuditId(runKey, index, result.name),
            resultTimestamp: result.eventAt,
            timestamp: result.eventAt
          }
        });
      }
    }
  }

  observations.unshift(...repositories.values(), ...workflows.values());

  for (const [id, enriched] of [...enrichedRuns.entries()].sort()) {
    const run = sessionRunEvidence(enriched.value);
    const raw = rawRuns.get(id);
    if (raw && Number(raw.value.attempt ?? 1) !== Number(run.run_attempt ?? 1)) continue;
    const session = run.cao_unified_session === undefined ? null
      : objectValue(run.cao_unified_session, 'run.cao_unified_session');
    if (session) {
      const sessionPath = requiredString(session.path, 'run.cao_unified_session.path');
      if (session.version !== 1) throw new TypeError('Unsupported unified session evidence version');
      observations.push(...sessionObservations(session.events, sessionPath, id, enriched.observedAt));
    }
    const awInfo = run.aw_info && typeof run.aw_info === 'object' && !Array.isArray(run.aw_info)
      ? /** @type {Record<string, unknown>} */ (run.aw_info)
      : {};
    const eventScopeId = `${id}:agentic`;
    const startedAt = timestamp(run.started_at ?? run.created_at) ?? enriched.observedAt;
    const completedAt = run.status === 'completed'
      ? timestamp(run.updated_at) ?? enriched.observedAt
      : null;
    let sourceSequence = enriched.line * 1000;
    /**
     * @param {string} type
     * @param {string | null} eventTimestamp
     * @param {string} summary
     * @param {string | undefined} status
     * @param {unknown} identity
     * @param {Record<string, unknown> & { source?: string, correlationId?: string }} [fields]
     */
    const emitEvent = (type, eventTimestamp, summary, status, identity = type, fields = {}) => {
      if (!eventTimestamp) return;
      const recordFields = {
        source: fields.source ?? 'gh-aw-logs',
        summary,
        ...fields
      };
      const canonicalType = unifiedSessionEventType(type);
      const kind = recordKind(canonicalType, recordFields);
      observations.push({
        kind,
        source: OBSERVATION_SOURCE,
        sourceId: `${eventScopeId}:${type}:${stableDigest(identity)}`,
        observedAt: enriched.observedAt,
        data: withoutUndefined({
          runId: id,
          timestamp: eventTimestamp,
          source: fields.source ?? 'gh-aw-logs',
          type: canonicalType,
          summary,
          status,
          correlationId: fields.correlationId,
          payloadRef: `gh-aw-logs-shards#L${enriched.line}`,
          sourceSequence,
          ...fields,
          ...specializedFields(canonicalType, recordFields, kind)
        })
      });
      sourceSequence += 1;
    };

    emitEvent(
      'workflow_run_started',
      startedAt,
      detail([optionalString(run.workflow_name) ?? '', optionalString(run.display_title) ?? '']),
      optionalString(run.status),
      { type: 'started', startedAt }
    );
    emitEvent(
      'agent.session',
      startedAt,
      detail([
        optionalString(run.engine) ?? optionalString(awInfo.engine_name) ?? '',
        optionalString(run.model) ?? optionalString(awInfo.model) ?? ''
      ]),
      optionalString(run.status),
      { type: 'agent-session', engine: run.engine_id, model: run.model },
      { source: 'agent' }
    );
    if (completedAt) {
      emitEvent(
        'workflow_run_completed',
        completedAt,
        optionalString(run.classification) ?? optionalString(run.conclusion) ?? 'completed',
        optionalString(run.conclusion) ?? optionalString(run.status),
        { type: 'completed', completedAt, conclusion: run.conclusion }
      );
    }
    if (run.conclusion === 'failure' || run.failure_kind !== undefined) {
      emitEvent(
        'workflow_run_failed',
        completedAt ?? enriched.observedAt,
        optionalString(run.failure_kind) ?? 'workflow run failed',
        'failure',
        { type: 'failure', failureKind: run.failure_kind, errorCount: run.error_count }
      );
    }
    if (run.token_usage_summary !== undefined || run.token_usage !== undefined || run.aic !== undefined) {
      const tokenSummary = run.token_usage_summary && typeof run.token_usage_summary === 'object'
        && !Array.isArray(run.token_usage_summary)
        ? /** @type {Record<string, unknown>} */ (run.token_usage_summary)
        : {};
      emitEvent(
        'workflow_run_usage',
        completedAt ?? enriched.observedAt,
        `AIC ${String(finiteNumber(tokenSummary.total_aic) ?? finiteNumber(run.aic) ?? 'unknown')}`,
        'observed',
        { type: 'usage', tokenUsage: run.token_usage_summary ?? run.token_usage, aic: run.aic }
      );
    }
    if (run.working_set !== undefined) {
      emitEvent(
        'workflow_run_working_set',
        completedAt ?? enriched.observedAt,
        'Working set measured',
        'observed',
        { type: 'working-set', value: run.working_set }
      );
    }
    if (run.behavior_fingerprint !== undefined || run.task_domain !== undefined) {
      const taskDomain = run.task_domain && typeof run.task_domain === 'object'
        && !Array.isArray(run.task_domain)
        ? /** @type {Record<string, unknown>} */ (run.task_domain)
        : {};
      emitEvent(
        'workflow_run_behavior',
        completedAt ?? enriched.observedAt,
        optionalString(taskDomain.label) ?? 'Behavior classified',
        'observed',
        { type: 'behavior', fingerprint: run.behavior_fingerprint, taskDomain: run.task_domain }
      );
    }
    if (Array.isArray(run.agentic_assessments)) {
      run.agentic_assessments.forEach((assessment, index) => {
        const record = assessment && typeof assessment === 'object' && !Array.isArray(assessment)
          ? /** @type {Record<string, unknown>} */ (assessment)
          : {};
        emitEvent(
          'workflow_run_assessment',
          completedAt ?? enriched.observedAt,
          optionalString(record.summary) ?? optionalString(record.kind) ?? `Assessment ${index + 1}`,
          optionalString(record.severity),
          { type: 'assessment', index, record }
        );
      });
    }
    const graders = run.graders && typeof run.graders === 'object' && !Array.isArray(run.graders)
      ? /** @type {Record<string, unknown>} */ (run.graders)
      : {};
    const graderResults = Array.isArray(graders.results)
      ? graders.results
      : [];
    graderResults.forEach((grader, index) => {
      const record = grader && typeof grader === 'object' && !Array.isArray(grader)
        ? /** @type {Record<string, unknown>} */ (grader)
        : {};
      emitEvent(
        'workflow_run_grader',
        completedAt ?? enriched.observedAt,
        optionalString(record.name) ?? optionalString(record.id) ?? `Grader ${index + 1}`,
        optionalString(record.status) ?? (record.passed === true ? 'passed' : record.passed === false ? 'failed' : undefined),
        { type: 'grader', index, record },
        {
          source: 'grader',
          grader: optionalString(record.id) ?? `grader-${index + 1}`,
          graderName: optionalString(record.name),
          graderSource: optionalString(record.source),
          value: finiteNumber(record.value),
          unit: optionalString(record.unit),
          direction: optionalString(record.direction),
          message: optionalString(record.message),
          error: optionalString(record.error),
          implementation: record.implementation,
          observation: record.observation,
          metrics: record.metrics,
          diagnostics: record.diagnostics,
          payloadRef: optionalString(record.sessionPayloadRef),
          baselineValue: finiteNumber(record.baselineValue),
          deltaFromBaseline: finiteNumber(record.deltaFromBaseline)
        }
      );
    });
    if (Array.isArray(run.evals)) {
      run.evals.forEach((candidate, index) => {
        const result = classifiedEval(candidate, String(run.run_id), id, enriched.observedAt, run.run_attempt ?? 1);
        emitEvent(
          result ? 'workflow_run_eval' : 'workflow_run_eval_unclassified',
          result?.eventAt ?? completedAt ?? enriched.observedAt,
          result ? `Evaluation ${result.name}` : 'Evaluation evidence is not classified',
          result ? (result.answer === 'YES' ? 'pass' : result.answer === 'NO' ? 'fail' : 'unavailable')
            : 'unavailable',
          result ? { type: 'eval', index, name: result.name } : { type: 'unclassified-eval', index },
          {
            source: 'audit', evidenceState: result ? 'classified' : 'unclassified',
            evalId: result?.name, answer: result?.answer,
            payloadRef: candidate && typeof candidate === 'object'
              ? optionalString(/** @type {Record<string, unknown>} */ (candidate).sessionPayloadRef) : undefined
          }
        );
      });
    }
    const audit = run.audit && typeof run.audit === 'object' && !Array.isArray(run.audit)
      ? /** @type {Record<string, unknown>} */ (run.audit)
      : {};
    const frictionValue = Object.hasOwn(run, 'friction') ? run.friction : audit.friction;
    if (frictionValue && typeof frictionValue === 'object' && !Array.isArray(frictionValue)) {
      const friction = /** @type {Record<string, unknown>} */ (frictionValue);
      const cost = friction.cost && typeof friction.cost === 'object' && !Array.isArray(friction.cost)
        ? /** @type {Record<string, unknown>} */ (friction.cost)
        : {};
      const tokens = cost.tokens && typeof cost.tokens === 'object' && !Array.isArray(cost.tokens)
        ? /** @type {Record<string, unknown>} */ (cost.tokens)
        : {};
      emitEvent(
        'workflow_run_friction',
        completedAt ?? enriched.observedAt,
        'Execution friction cost',
        optionalString(friction.measurement_state),
        { type: 'friction' },
        {
          source: 'audit',
          measurementState: optionalString(friction.measurement_state),
          canonicalUnit: optionalString(friction.canonical_unit),
          sources: Array.isArray(friction.sources) ? friction.sources : undefined,
          totalEvents: finiteNumber(friction.total_events),
          totalOccurrences: finiteNumber(friction.total_occurrences),
          countedOccurrences: finiteNumber(friction.counted_occurrences),
          suppressedOccurrences: finiteNumber(friction.suppressed_occurrences),
          linkedInvocations: finiteNumber(friction.linked_invocations),
          unattributedOccurrences: finiteNumber(friction.unattributed_occurrences),
          aic: finiteNumber(cost.aic),
          inputTokens: finiteNumber(tokens.input),
          outputTokens: finiteNumber(tokens.output),
          cacheReadTokens: finiteNumber(tokens.cache_read),
          cacheWriteTokens: finiteNumber(tokens.cache_write),
          reasoningTokens: finiteNumber(tokens.reasoning),
          totalTokens: finiteNumber(tokens.total),
          turns: finiteNumber(cost.turns),
          toolCalls: finiteNumber(cost.tool_calls),
          latencyMs: finiteNumber(cost.latency_ms),
          totalRunAic: finiteNumber(friction.total_run_aic),
          frictionRatio: finiteNumber(friction.friction_ratio),
          derived: friction.derived === true,
          dimensionStates: friction.dimension_states,
          uncertainty: friction.uncertainty,
          drivers: friction.drivers,
          groups: friction.groups,
          events: friction.events,
          eventsTruncated: friction.events_truncated === true,
          unmeasuredDrivers: friction.unmeasured_drivers
        }
      );
    }
    const explicitSafeOutputs = safeOutputItemsByRun.get(id) ?? [];
    const nestedSafeOutputs = Array.isArray(run.safe_outputs)
      ? run.safe_outputs
      : Array.isArray(audit.created_items) ? audit.created_items : [];
    const sessionSafeOutputs = session?.run && typeof session.run === 'object' && !Array.isArray(session.run)
      ? /** @type {Record<string, unknown>} */ (session.run).safe_outputs : undefined;
    const safeOutputs = Array.isArray(sessionSafeOutputs)
      ? sessionSafeOutputs.map((value) => {
          const existing = explicitSafeOutputs.find((entry) => entry.value.url === value.url);
          return { value: { ...value, ...(existing?.value.github_issue_status
            ? { github_issue_status: existing.value.github_issue_status } : {}) }, line: enriched.line };
        })
      : explicitSafeOutputs.length > 0
      ? explicitSafeOutputs
      : nestedSafeOutputs.map((value) => ({ value, line: enriched.line }));
    const githubRunKey = String(run.run_id);
    for (const tokenObservation of tokenEfficiencyObservationsByRun.get(githubRunKey) ?? []) {
      const observed = timestamp(tokenObservation.observedAt) ?? completedAt ?? enriched.observedAt;
      const targetRepo = requiredString(tokenObservation.targetRepo, 'token observation targetRepo').toLowerCase();
      if (!REPOSITORY_COORDINATE_PATTERN.test(targetRepo)) continue;
      const targetCoordinates = repositoryCoordinates(targetRepo);
      const targetWorkflowPath = requiredString(
        tokenObservation.workflowPath,
        'token observation workflowPath'
      );
      const opportunityId = requiredString(
        tokenObservation.opportunityId,
        'token observation opportunityId'
      );
      const interventionId = requiredString(
        tokenObservation.interventionId,
        'token observation interventionId'
      );
      emitEvent(
        'token_efficiency.opportunity',
        observed,
        optionalString(tokenObservation.opportunityKind) ?? 'Token-efficiency opportunity',
        optionalString(tokenObservation.evidenceState) ?? 'unavailable',
        { type: 'token-efficiency-opportunity', opportunityId },
        {
          source: 'token-optimizer-observation',
          targetRepo,
          targetOrganization: targetCoordinates.owner,
          targetRepository: targetCoordinates.name,
          targetWorkflowPath,
          opportunityId,
          opportunityKind: optionalString(tokenObservation.opportunityKind),
          assignmentRunId: optionalString(tokenObservation.assignmentRunId),
          experimentId: optionalString(tokenObservation.experimentId),
          evidenceWindowStart: optionalString(tokenObservation.evidenceWindowStart),
          evidenceWindowEnd: optionalString(tokenObservation.evidenceWindowEnd),
          evidenceState: optionalString(tokenObservation.evidenceState),
          evidenceConfidence: finiteNumber(tokenObservation.evidenceConfidence),
          costGrain: optionalString(tokenObservation.costGrain),
          evidenceProvenance: tokenObservation.evidenceProvenance,
          payloadRef: `gh-aw-logs-shards#L${String(tokenObservation.__line)}`
        }
      );
      emitEvent(
        'token_efficiency.intervention',
        observed,
        optionalString(tokenObservation.opportunityKind) ?? 'Token-efficiency intervention',
        optionalString(tokenObservation.interventionState) ?? 'proposed',
        { type: 'token-efficiency-intervention', interventionId },
        {
          source: 'token-optimizer-observation',
          targetRepo,
          targetOrganization: targetCoordinates.owner,
          targetRepository: targetCoordinates.name,
          targetWorkflowPath,
          opportunityId,
          interventionId,
          interventionState: optionalString(tokenObservation.interventionState),
          recommendationDisposition: optionalString(tokenObservation.recommendationDisposition),
          supersedesInterventionId: optionalString(tokenObservation.supersedesInterventionId),
          supersededByInterventionId: optionalString(tokenObservation.supersededByInterventionId),
          experimentId: optionalString(tokenObservation.experimentId),
          controlVariant: optionalString(tokenObservation.controlVariant),
          optimizedVariant: optionalString(tokenObservation.optimizedVariant),
          proposedSavingsAic: finiteNumber(tokenObservation.proposedSavingsAic),
          attributableRunIds: tokenObservation.attributableRunIds,
          payloadRef: `gh-aw-logs-shards#L${String(tokenObservation.__line)}`
        }
      );
      const supersedesInterventionId = optionalString(tokenObservation.supersedesInterventionId);
      if (supersedesInterventionId) {
        emitEvent(
          'token_efficiency.intervention',
          observed,
          'Token-efficiency intervention superseded',
          'rejected',
          {
            type: 'token-efficiency-intervention-superseded',
            interventionId: supersedesInterventionId,
            supersededByInterventionId: interventionId
          },
          {
            source: 'token-optimizer-observation',
            targetRepo,
            targetOrganization: targetCoordinates.owner,
            targetRepository: targetCoordinates.name,
            targetWorkflowPath,
            opportunityId,
            interventionId: supersedesInterventionId,
            interventionState: 'rejected',
            recommendationDisposition: 'superseded',
            supersededByInterventionId: interventionId,
            supersededAt: observed,
            evidenceState: 'complete',
            payloadRef: `gh-aw-logs-shards#L${String(tokenObservation.__line)}`
          }
        );
      }
    }
    for (const lifecycle of tokenEfficiencyLifecycleObservationsByRun.get(githubRunKey) ?? []) {
      const observed = canonicalTimestamp(
        lifecycle.observedAt,
        'token lifecycle observedAt'
      );
      const targetRepo = requiredString(lifecycle.targetRepo, 'token lifecycle targetRepo').toLowerCase();
      if (!REPOSITORY_COORDINATE_PATTERN.test(targetRepo)) {
        throw new TypeError('token lifecycle targetRepo must be an owner/repository coordinate');
      }
      const targetCoordinates = repositoryCoordinates(targetRepo);
      const targetWorkflowPath = requiredString(
        lifecycle.workflowPath,
        'token lifecycle workflowPath'
      );
      const opportunityId = requiredString(
        lifecycle.opportunityId,
        'token lifecycle opportunityId'
      );
      const interventionId = requiredString(
        lifecycle.interventionId,
        'token lifecycle interventionId'
      );
      const lifecycleObservationId = requiredString(
        lifecycle.lifecycleObservationId,
        'token lifecycle lifecycleObservationId'
      );
      const previousInterventionState = enumValue(
        lifecycle.previousInterventionState,
        'token lifecycle previousInterventionState',
        ['proposed', 'accepted', 'running', 'verified', 'regressed', 'inconclusive', 'rejected']
      );
      const interventionState = enumValue(
        lifecycle.interventionState,
        'token lifecycle interventionState',
        ['proposed', 'accepted', 'running', 'verified', 'regressed', 'inconclusive', 'rejected']
      );
      const previousRecommendationDisposition = enumValue(
        lifecycle.previousRecommendationDisposition,
        'token lifecycle previousRecommendationDisposition',
        ['applied', 'superseded', 'outdated', 'duplicate', 'unapplied', 'failed-start', 'rejected']
      );
      const recommendationDisposition = enumValue(
        lifecycle.recommendationDisposition,
        'token lifecycle recommendationDisposition',
        ['applied', 'superseded', 'outdated', 'duplicate', 'unapplied', 'failed-start', 'rejected']
      );
      const evidenceState = enumValue(
        lifecycle.evidenceState,
        'token lifecycle evidenceState',
        ['complete', 'incomplete', 'unavailable']
      );
      emitEvent(
        'token_efficiency.intervention',
        observed,
        optionalString(lifecycle.missingReason) ?? 'Token-efficiency lifecycle updated',
        interventionState,
        { type: 'token-efficiency-lifecycle', lifecycleObservationId },
        {
          source: 'token-intervention-lifecycle',
          optimizerRunAttempt: positiveInteger(
            lifecycle.optimizerRunAttempt,
            'token lifecycle optimizerRunAttempt'
          ),
          optimizerWorkflowPath: requiredString(
            lifecycle.optimizerWorkflowPath,
            'token lifecycle optimizerWorkflowPath'
          ),
          optimizerWorkflowName: requiredString(
            lifecycle.optimizerWorkflowName,
            'token lifecycle optimizerWorkflowName'
          ),
          targetRepo,
          targetOrganization: targetCoordinates.owner,
          targetRepository: targetCoordinates.name,
          targetWorkflowPath,
          opportunityId,
          interventionId,
          experimentId: optionalString(lifecycle.experimentId),
          controlVariant: optionalString(lifecycle.controlVariant),
          optimizedVariant: optionalString(lifecycle.optimizedVariant),
          proposedSavingsAic: finiteNumber(lifecycle.proposedSavingsAic),
          supersedesInterventionId: optionalString(lifecycle.supersedesInterventionId),
          recommendationChurnCount: finiteNumber(lifecycle.recommendationChurnCount),
          recommendationChurnRate: finiteNumber(lifecycle.recommendationChurnRate),
          lifecycleObservationId,
          previousInterventionState,
          interventionState,
          previousRecommendationDisposition,
          recommendationDisposition,
          evidenceState,
          missingReason: optionalString(lifecycle.missingReason),
          safeOutputId: requiredString(lifecycle.safeOutputId, 'token lifecycle safeOutputId'),
          safeOutputUrl: requiredString(lifecycle.safeOutputUrl, 'token lifecycle safeOutputUrl'),
          implementationChangeId: optionalString(lifecycle.implementationChangeId),
          implementationPullRequestUrl: optionalString(lifecycle.implementationPullRequestUrl),
          implementationRunIds: optionalIdentifierArray(
            lifecycle.implementationRunIds,
            'token lifecycle implementationRunIds'
          ),
          acceptedAt: optionalTimestamp(lifecycle.acceptedAt, 'token lifecycle acceptedAt'),
          implementationStartedAt: optionalTimestamp(
            lifecycle.implementationStartedAt,
            'token lifecycle implementationStartedAt'
          ),
          implementationCompletedAt: optionalTimestamp(
            lifecycle.implementationCompletedAt,
            'token lifecycle implementationCompletedAt'
          ),
          rejectedAt: optionalTimestamp(lifecycle.rejectedAt, 'token lifecycle rejectedAt'),
          supersededAt: optionalTimestamp(lifecycle.supersededAt, 'token lifecycle supersededAt'),
          supersededByInterventionId: optionalString(lifecycle.supersededByInterventionId),
          claimRunId: identifier(lifecycle.claimRunId, 'token lifecycle claimRunId'),
          claimRunAttempt: positiveInteger(
            lifecycle.claimRunAttempt,
            'token lifecycle claimRunAttempt'
          ),
          actor: requiredString(lifecycle.actor, 'token lifecycle actor'),
          sourceProvenance: objectValue(
            lifecycle.sourceProvenance,
            'token lifecycle sourceProvenance'
          ),
          payloadRef: `gh-aw-logs-shards#L${String(lifecycle.__line)}`
        }
      );
    }
    const runMcpToolUsage = run.mcp_tool_usage && typeof run.mcp_tool_usage === 'object'
      && !Array.isArray(run.mcp_tool_usage)
      ? /** @type {Record<string, unknown>} */ (run.mcp_tool_usage)
      : {};
    const auditMcpToolUsage = audit.mcp_tool_usage && typeof audit.mcp_tool_usage === 'object'
      && !Array.isArray(audit.mcp_tool_usage)
      ? /** @type {Record<string, unknown>} */ (audit.mcp_tool_usage)
      : {};
    const mcpToolUsage = Array.isArray(runMcpToolUsage.tool_calls)
      && runMcpToolUsage.tool_calls.length > 0
      ? runMcpToolUsage
      : auditMcpToolUsage;
    const toolCalls = !session && Array.isArray(mcpToolUsage.tool_calls) ? mcpToolUsage.tool_calls : [];
    toolCalls.forEach((toolCall, index) => {
      const record = toolCall && typeof toolCall === 'object' && !Array.isArray(toolCall)
        ? /** @type {Record<string, unknown>} */ (toolCall)
        : {};
      const eventTimestamp = timestamp(record.timestamp) ?? completedAt ?? enriched.observedAt;
      const correlationId = optionalString(record.tool_call_id) ?? `${id}:mcp:${index}`;
      const summary = detail([
        optionalString(record.server_name) ?? 'unknown',
        optionalString(record.tool_name) ?? 'unknown'
      ]);
      const status = optionalString(record.status) ?? 'unknown';
      const requestSize = finiteNumber(record.input_size ?? record.request_size);
      const responseSize = finiteNumber(record.output_size ?? record.response_size);
      const requestBytes = requestSize === null ? undefined : Math.max(0, requestSize);
      const responseBytes = responseSize === null ? undefined : Math.max(0, responseSize);
      emitEvent(
        'tool.call',
        eventTimestamp,
        summary,
        'started',
        { type: 'mcp-call', index, server: record.server_name, tool: record.tool_name },
        {
          source: 'mcp',
          correlationId,
          mcpServer: optionalString(record.server_name),
          mcpTool: optionalString(record.tool_name),
          requestBytes,
          responseBytes
        }
      );
      emitEvent(
        status === 'success' ? 'tool.result' : 'tool.error',
        eventTimestamp,
        summary,
        status,
        { type: 'mcp-outcome', index, status },
        {
          source: 'mcp',
          correlationId,
          mcpServer: optionalString(record.server_name),
          mcpTool: optionalString(record.tool_name),
          requestBytes,
          responseBytes
        }
      );
    });
    /**
     * @param {string} field
     * @param {string} type
     * @param {string} summaryField
     * @param {string} statusField
     */
    const emitAuditEvents = (field, type, summaryField, statusField) => {
      const entries = Array.isArray(audit[field]) ? audit[field] : [];
      entries.forEach((entry, index) => {
        const record = entry && typeof entry === 'object' && !Array.isArray(entry)
          ? /** @type {Record<string, unknown>} */ (entry)
          : {};
        emitEvent(
          type,
          timestamp(record.timestamp) ?? completedAt ?? enriched.observedAt,
          optionalString(record[summaryField]) ?? type,
          optionalString(record[statusField]),
          { type, index, summary: record[summaryField], status: record[statusField] },
          {
            source: 'audit',
            code: optionalString(record.code)
          }
        );
      });
    };
    emitAuditEvents('key_findings', 'audit.finding', 'title', 'severity');
    emitAuditEvents('observability_insights', 'audit.observability', 'title', 'severity');
    emitAuditEvents('recommendations', 'audit.recommendation', 'action', 'priority');
    emitAuditEvents('missing_tools', 'audit.missing_tool', 'tool', 'status');
    emitAuditEvents('missing_data', 'audit.missing_data', 'data_type', 'status');
    emitAuditEvents('noops', 'audit.noop', 'message', 'status');
    emitAuditEvents('mcp_failures', 'audit.mcp_failure', 'server_name', 'status');
    const skillActivationsValue = Object.hasOwn(run, 'skill_activations')
      ? run.skill_activations
      : audit.skill_activations;
    const skillActivations = !session && Array.isArray(skillActivationsValue) ? skillActivationsValue : [];
    skillActivations.forEach((entry, index) => {
      const record = entry && typeof entry === 'object' && !Array.isArray(entry)
        ? /** @type {Record<string, unknown>} */ (entry)
        : {};
      const name = optionalString(record.name) ?? `Skill ${index + 1}`;
      emitEvent(
        'audit.skill_activation',
        timestamp(record.timestamp) ?? completedAt ?? enriched.observedAt,
        name,
        optionalString(record.status) ?? 'invoked',
        { type: 'audit.skill_activation', index, name },
        {
          source: 'audit',
          name,
          invocationCount: finiteNumber(record.invocation_count) ?? 1,
          failedCount: finiteNumber(record.failed_count) ?? 0,
          activationSource: optionalString(record.source)
        }
      );
    });
    const steeringEventsValue = run.gateway_steering_events ?? audit.gateway_steering_events;
    const steeringEvents = Array.isArray(steeringEventsValue) ? steeringEventsValue : [];
    steeringEvents.forEach((entry, index) => {
      const record = entry && typeof entry === 'object' && !Array.isArray(entry)
        ? /** @type {Record<string, unknown>} */ (entry)
        : {};
      const steeringType = optionalString(record.type);
      emitEvent(
        'audit.gateway_steering',
        timestamp(record.timestamp) ?? completedAt ?? enriched.observedAt,
        optionalString(record.message) ?? steeringType ?? 'gateway steering',
        steeringType,
        { type: 'audit.gateway_steering', index, steeringType, message: record.message },
        { source: 'audit', code: steeringType }
      );
    });
    const firewallAnalysisValue = run.firewall_analysis ?? audit.firewall_analysis;
    const firewallAnalysis = firewallAnalysisValue && typeof firewallAnalysisValue === 'object'
      && !Array.isArray(firewallAnalysisValue)
      ? /** @type {Record<string, unknown>} */ (firewallAnalysisValue)
      : {};
    const requestsByDomain = firewallAnalysis.requests_by_domain
      && typeof firewallAnalysis.requests_by_domain === 'object'
      && !Array.isArray(firewallAnalysis.requests_by_domain)
      ? /** @type {Record<string, unknown>} */ (firewallAnalysis.requests_by_domain)
      : {};
    for (const [host, counts] of Object.entries(session ? {} : requestsByDomain)) {
      const domain = firewallDomain(host);
      if (!domain || !counts || typeof counts !== 'object' || Array.isArray(counts)) continue;
      const record = /** @type {Record<string, unknown>} */ (counts);
      for (const [decision, field, type] of [
        ['allowed', 'allowed', 'net_allowed'],
        ['denied', 'blocked', 'net_blocked']
      ]) {
        const requestCount = finiteNumber(record[field]);
        if (!requestCount || requestCount < 0) continue;
        emitEvent(
          type,
          completedAt ?? enriched.observedAt,
          host,
          decision,
          { type: 'firewall', host, decision },
          { source: 'firewall', domain, decision, requestCount }
        );
      }
    }
    safeOutputs.forEach((safeOutput, index) => {
      const record = safeOutput.value && typeof safeOutput.value === 'object'
        && !Array.isArray(safeOutput.value)
        ? /** @type {Record<string, unknown>} */ (safeOutput.value)
        : {};
      const issueStatus = record.github_issue_status
        && typeof record.github_issue_status === 'object'
        && !Array.isArray(record.github_issue_status)
        ? /** @type {Record<string, unknown>} */ (record.github_issue_status)
        : {};
      emitEvent(
        'safe_output.created',
        timestamp(record.timestamp) ?? completedAt ?? enriched.observedAt,
        optionalString(record.type) === 'report_incomplete'
          ? optionalString(record.reason) ?? 'Required evidence was unavailable.'
          : detail([
              optionalString(record.type) ?? 'safe output',
              optionalString(record.repo) ?? '',
              optionalString(record.number) ?? ''
            ]),
        'created',
        { type: 'safe-output', index, outputType: record.type, url: record.url },
        {
          source: 'safe-output',
          correlationId: optionalString(record.url ?? record.temporaryId),
          safeOutputType: optionalString(record.type),
          githubEntityType: safeOutputGithubEntityType(record),
          issueState: optionalString(issueStatus.state),
          issueClosed: typeof issueStatus.closed === 'boolean' ? issueStatus.closed : undefined,
          issueStateReason: issueStatus.state_reason === null
            ? null
            : optionalString(issueStatus.state_reason),
          issueClosedAt: issueStatus.closed_at === null ? null : optionalString(issueStatus.closed_at),
          issueStatusObservedAt: optionalString(issueStatus.observed_at),
          payloadRef: optionalString(record.sessionPayloadRef) ?? `gh-aw-logs-shards#L${safeOutput.line}`
        }
      );
    });
    if (run.safe_items_count !== undefined) {
      emitEvent(
        'workflow_run_safe_outputs',
        completedAt ?? enriched.observedAt,
        `${String(run.safe_items_count)} safe output items`,
        'observed',
        { type: 'safe-outputs', count: run.safe_items_count }
      );
    }
    if (run.comparison !== undefined) {
      const comparison = run.comparison && typeof run.comparison === 'object'
        && !Array.isArray(run.comparison)
        ? /** @type {Record<string, unknown>} */ (run.comparison)
        : {};
      emitEvent(
        'workflow_run_comparison',
        completedAt ?? enriched.observedAt,
        comparison.baseline_found === true ? 'Baseline comparison available' : 'No baseline comparison',
        comparison.baseline_found === true ? 'available' : 'unavailable',
        { type: 'comparison', value: run.comparison }
      );
    }
  }
  const unmatchedLifecycleRuns = [...tokenEfficiencyLifecycleObservationsByRun.keys()]
    .filter((id) => !latestRunByGithubId.has(id));
  if (unmatchedLifecycleRuns.length > 0) {
    throw new TypeError(
      `Token lifecycle observations require retained optimizer runs: ${unmatchedLifecycleRuns.join(', ')}`
    );
  }

  let mappedRateLimits = 0;
  if (rateLimitEnvelopes.length > 0 && options.context !== undefined) {
    const context = objectValue(options.context, 'gh-aw JSONL collection context');
    const observedAt = canonicalTimestamp(
      context.observedAt,
      'gh-aw JSONL collection context observedAt'
    );
    const contextRun = objectValue(context.run, 'gh-aw JSONL collection context run');
    const contextGithubRunId = identifier(
      contextRun.githubRunId,
      'gh-aw JSONL collection context run.githubRunId'
    );
    /** @type {{ repository: Record<string, unknown>, owner: string, name: string } | undefined} */
    let contextCoordinates;
    const resolveContextRepository = () => {
      if (contextCoordinates) return contextCoordinates;
      const repository = objectValue(
        context.repository,
        'gh-aw JSONL collection context repository'
      );
      contextCoordinates = {
        repository,
        owner: requiredString(
          repository.owner,
          'gh-aw JSONL collection context repository.owner'
        ),
        name: requiredString(
          repository.name,
          'gh-aw JSONL collection context repository.name'
        )
      };
      return contextCoordinates;
    };
    let collectionRunId = latestRunByGithubId.get(String(contextGithubRunId));
    if (!collectionRunId) {
      const { owner, name } = resolveContextRepository();
      collectionRunId = runId(owner, name, contextGithubRunId);
    }
    if (!runIds.has(collectionRunId)) {
      const { repository: contextRepository, owner, name } = resolveContextRepository();
      const contextWorkflow = objectValue(
        context.workflow,
        'gh-aw JSONL collection context workflow'
      );
      const repositoryGithubId = identifier(
        contextRepository.githubId,
        'gh-aw JSONL collection context repository.githubId'
      );
      const workflowGithubId = identifier(
        contextWorkflow.githubId,
        'gh-aw JSONL collection context workflow.githubId'
      );
      const repository = repositoryCoordinateId(owner, name);
      const contextWorkflowPath = requiredString(
        contextWorkflow.path,
        'gh-aw JSONL collection context workflow.path'
      );
      const workflow = workflowCoordinateId(owner, name, contextWorkflowPath);
      observations.push(
        {
          kind: 'repository',
          source: OBSERVATION_SOURCE,
          sourceId: `collection-repository:${repositoryGithubId}`,
          observedAt,
          data: {
            id: repository,
            githubId: repositoryGithubId,
            owner,
            name,
            fullName: `${owner}/${name}`,
            visibility: optionalString(contextRepository.visibility) ?? 'unknown'
          }
        },
        {
          kind: 'workflow',
          source: OBSERVATION_SOURCE,
          sourceId: `collection-workflow:${workflowGithubId}`,
          observedAt,
          data: {
            id: workflow,
            githubId: workflowGithubId,
            repositoryId: repository,
            name: requiredString(
              contextWorkflow.name,
              'gh-aw JSONL collection context workflow.name'
            ),
            path: workflowSourcePath(contextWorkflowPath),
            state: optionalString(contextWorkflow.state) ?? 'unknown'
          }
        },
        {
          kind: 'run',
          source: OBSERVATION_SOURCE,
          sourceId: `collection-run:${collectionRunId}`,
          observedAt,
          data: {
            githubRunId: identifier(
              contextRun.githubRunId,
              'gh-aw JSONL collection context run.githubRunId'
            ),
            attempt: positiveInteger(
              contextRun.attempt,
              'gh-aw JSONL collection context run.attempt'
            ),
            repositoryId: repository,
            workflowId: workflow,
            owner,
            repository: name,
            repositoryFullName: `${owner}/${name}`,
            workflowPath: requiredString(
              contextWorkflow.path,
              'gh-aw JSONL collection context workflow.path'
            ),
            event: optionalString(contextRun.event) ?? 'unknown',
            status: optionalString(contextRun.status) ?? 'unknown',
            conclusion: optionalString(contextRun.conclusion) ?? null,
            startedAt: optionalString(contextRun.startedAt) ?? null,
            completedAt: optionalString(contextRun.completedAt) ?? null
          }
        }
      );
      runIds.add(collectionRunId);
    }

    const eventScopeId = `${collectionRunId}:github-api-collection`;
    for (const { envelope, line } of rateLimitEnvelopes) {
      const rateLimit = objectValue(envelope.rate_limit, `gh-aw JSONL line ${line}.rate_limit`);
      const end = objectValue(rateLimit.end, `gh-aw JSONL line ${line}.rate_limit.end`);
      const remaining = finiteNumber(end.remaining);
      const limit = finiteNumber(end.limit);
      observations.push({
        kind: 'audit',
        source: OBSERVATION_SOURCE,
        sourceId: `${eventScopeId}:github_api_rate_limit:${line}`,
        observedAt,
        data: {
          runId: collectionRunId,
          timestamp: observedAt,
          source: 'github-api',
          type: 'github_api_rate_limit',
          summary: `${String(remaining ?? 'unknown')} of ${String(limit ?? 'unknown')} requests remaining`,
          status: remaining !== null && remaining > 0 ? 'available' : 'exhausted',
          correlationId: optionalString(rateLimit.host),
          payloadRef: `gh-aw-logs-shards#L${line}`,
          sourceSequence: line
        }
      });
      mappedRateLimits += 1;
    }
  }

  return {
    observations,
    records,
    rawPayloadRecords,
    rawRuns: rawRuns.size,
    agenticRunRecords,
    agenticRuns: enrichedRuns.size,
    duplicateRawRunObservations: rawPayloadRecords - rawRuns.size,
    duplicateAgenticRunObservations: agenticRunRecords - enrichedRuns.size,
    unenrichedRuns: runIds.size - enrichedRuns.size,
    recordsByKind: observations.reduce((counts, observation) => {
      if (observation.kind === 'domain') counts.domains += 1;
      if (observation.kind === 'tool') counts.tools += 1;
      if (observation.kind === 'skill') counts.skills += 1;
      if (observation.kind === 'friction') counts.friction += 1;
      if (observation.kind === 'audit') counts.audits += 1;
      if (observation.kind === 'issue') counts.issues += 1;
      return counts;
    }, { domains: 0, tools: 0, skills: 0, friction: 0, audits: 0, issues: 0 }),
    safeOutputItems,
    mappedSafeOutputItems: [...safeOutputItemsByRun.values()]
      .reduce((total, items) => total + items.length, 0),
    rateLimits: rateLimitEnvelopes.length,
    mappedRateLimits
  };
  };
  return { accept, finish };
}
