import { executeDashboardQuery } from '../dashboard/site/src/data/queries/declarative.js';

const DAY = 86400000;
const MIN_SAMPLE = 10;
const METRICS = {
  safeItemsCount: [0, 1, 2, 5, 10],
  agenticDurationSeconds: [0, 60, 300, 900, 1800, 3600],
  inputTokens: [0, 10000, 100000, 1000000],
  outputTokens: [0, 1000, 10000, 100000],
  aicTotal: [0, 1, 5, 20, 100],
  mcpToolCalls: [0, 1, 10, 50, 200],
};
const finite = (value) => typeof value === 'number' && Number.isFinite(value) && value >= 0;
const timestamp = (value) => typeof value === 'string' ? Date.parse(value) : NaN;
const round = (value) => Number(value.toFixed(8));
const sampleStatus = (n) => n >= MIN_SAMPLE ? 'observed' : n ? 'small-sample' : 'unavailable';

function category(value, boundaries) {
  if (!finite(value)) return 'unknown';
  if (value === 0) return '0';
  const index = boundaries.findIndex((limit) => value <= limit);
  return index < 0 ? `>${boundaries.at(-1)}` : `(${boundaries[index - 1]},${boundaries[index]}]`;
}

function weights(values) {
  const counts = new Map();
  for (const value of values) counts.set(value, (counts.get(value) ?? 0) + 1);
  return [...counts].sort(([a], [b]) => String(a).localeCompare(String(b)))
    .map(([value, count]) => ({ value, count, weight: round(count / values.length) }));
}

function quantile(sorted, p) {
  if (!sorted.length) return null;
  const position = (sorted.length - 1) * p;
  const lower = Math.floor(position);
  return sorted[lower] + (sorted[Math.ceil(position)] - sorted[lower]) * (position - lower);
}

function numeric(values, boundaries) {
  const observed = values.filter(finite).sort((a, b) => a - b);
  return {
    n: observed.length,
    missing: values.length - observed.length,
    status: sampleStatus(observed.length),
    median: quantile(observed, 0.5),
    p90: quantile(observed, 0.9),
    histogram: weights(observed.map((value) => category(value, boundaries))),
  };
}

function binary(values) {
  const observed = values.filter((value) => typeof value === 'boolean');
  const successes = observed.filter(Boolean).length;
  const n = observed.length;
  if (!n) return { n: 0, missing: values.length, status: 'unavailable', probability: null, wilson95: null };
  const p = successes / n;
  const z = 1.959963984540054;
  const denominator = 1 + z * z / n;
  const center = (p + z * z / (2 * n)) / denominator;
  const half = z * Math.sqrt(p * (1 - p) / n + z * z / (4 * n * n)) / denominator;
  return {
    n, missing: values.length - n, status: sampleStatus(n), successes, probability: round(p),
    wilson95: [round(Math.max(0, center - half)), round(Math.min(1, center + half))],
  };
}

function query(batch, query) {
  const sources = Object.fromEntries(Object.entries(batch).map(([name, rows]) => [
    name, { rows, metadata: { availability: 'available', completeness: 'unknown' } },
  ]));
  const result = executeDashboardQuery(query, sources);
  if (result.metadata?.availability === 'unavailable') {
    throw new Error(String(result.metadata['query-diagnostic'] ?? 'Canonical query unavailable'));
  }
  return result.rows;
}

/** Project only numeric/identifier evidence through the production query engine. */
export function behaviorEvidence(batch, { repository, since, until }) {
  const coordinates = /^([A-Za-z0-9-]+)\/([A-Za-z0-9._-]+)$/.exec(repository ?? '');
  const start = timestamp(since);
  const end = timestamp(until);
  if (!coordinates || !Number.isFinite(start) || !Number.isFinite(end) || start >= end
    || !/Z$/.test(since) || !/Z$/.test(until)) {
    throw new TypeError('An exact OWNER/REPO and increasing ISO since/until timestamps are required');
  }
  const runFields = ['id', 'workflowId', 'workflowPath', 'createdAt', 'createdMs', 'status', 'conclusion',
    'terminalOutcome', 'attempt', ...Object.keys(METRICS)];
  const runs = query(batch, {
    name: 'empirical-aw-runs', from: 'runs',
    filter: { predicates: [
      { field: 'owner', equals: coordinates[1] },
      { field: 'repository', equals: coordinates[2] },
      { field: 'workflowPath', includes: '.md' },
    ] },
    compute: [{ as: 'createdMs', function: 'timestamp', args: [{ field: 'createdAt' }] }],
    select: runFields.map((field) => ({ field })),
    'order-by': [{ field: 'createdAt' }, { field: 'id' }],
  });
  const scopedRuns = query({ scopedRuns: runs }, {
    name: 'empirical-aw-window', from: 'scopedRuns',
    filter: { predicates: [{ field: 'createdMs', gte: start, lt: end }] },
    'order-by': [{ field: 'createdAt' }, { field: 'id' }],
  });
  const selected = { ...batch, selectedRuns: scopedRuns };
  const linked = (from, fields) => query(selected, {
    name: `empirical-aw-${from}`, from,
    joins: [{
      source: 'selectedRuns', type: 'inner', on: [{ left: 'runId', right: 'id' }],
      fields: [{ field: 'workflowId', as: 'workflowId' }],
    }],
    select: ['id', 'runId', ...fields].map((field) => ({ field })),
  });
  return {
    runs: scopedRuns,
    issues: linked('issues', ['type', 'isPullRequest', 'safeOutputType', 'timestamp', 'closedAt', 'statusObservedAt', 'closed']),
    tools: linked('tools', ['name', 'type']),
    skills: linked('skills', ['name', 'invocationCount']),
    outputs: linked('audits', ['type', 'safeOutputType']),
  };
}

function runRecords(records) {
  const groups = new Map();
  for (const record of records) {
    const group = groups.get(record.runId) ?? [];
    group.push(record);
    groups.set(record.runId, group);
  }
  return groups;
}

function fitWorkflow(runs, evidence) {
  const issuesByRun = runRecords(evidence.issues);
  const outputsByRun = runRecords(evidence.outputs);
  const completed = runs.filter((run) => run.status === 'completed');
  const features = completed.map((run) => {
    const outputs = [...(issuesByRun.get(run.id) ?? []), ...(outputsByRun.get(run.id) ?? [])]
      .filter((event) => event.type === 'safe_output.result');
    const creations = outputs.filter((event) =>
      ['create_issue', 'create_pull_request'].includes(event.safeOutputType));
    // Reconcile explicit item counts before treating absent detail as observed zero.
    const completeManifest = Number.isSafeInteger(run.safeItemsCount) && run.safeItemsCount >= 0
      && outputs.length === run.safeItemsCount && outputs.every((event) => event.safeOutputType);
    const noCreation = completeManifest ? creations.length === 0 : null;
    return { ...run, noCreation, observedCreations: creations.length,
      createdEntities: completeManifest ? creations.length : null };
  });
  const ids = new Set(runs.map((run) => run.id));
  const issueRecords = evidence.issues.filter((issue) => ids.has(issue.runId)
    && ['create_issue', 'create_pull_request'].includes(issue.safeOutputType));
  const timedIssues = issueRecords.filter((issue) => Number.isFinite(timestamp(issue.timestamp)));
  const observedClosed = timedIssues.filter((issue) => issue.closed === true
    && Number.isFinite(timestamp(issue.closedAt)));
  const metrics = Object.fromEntries(Object.entries(METRICS).map(([field, bins]) => [
    field, numeric(features.map((run) => run[field]), bins),
  ]));
  metrics.createdEntities = numeric(features.map((run) => run.createdEntities), [0, 1, 2, 5, 10]);
  for (const field of ['safeItemsCount', 'createdEntities']) {
    metrics[field].pmf = weights(features.map((run) => run[field]).filter(finite));
  }
  const times = runs.map((run) => timestamp(run.createdAt)).sort((a, b) => a - b);
  const gaps = times.slice(1).map((time, index) => (time - times[index]) / 3600000);
  const days = new Set(times.map((time) => Math.floor(time / DAY)));
  const cells = features.map((run) => JSON.stringify({
    outcome: run.conclusion || 'unknown',
    noCreation: run.noCreation,
    safeItems: category(run.safeItemsCount, METRICS.safeItemsCount),
    durationSeconds: category(run.agenticDurationSeconds, METRICS.agenticDurationSeconds),
    aic: category(run.aicTotal, METRICS.aicTotal),
    inputTokens: category(run.inputTokens, METRICS.inputTokens),
    mcpCalls: category(run.mcpToolCalls, METRICS.mcpToolCalls),
  }));
  const observedNames = (collection) => {
    const records = evidence[collection].filter((record) => ids.has(record.runId));
    const names = new Map();
    for (const record of records) {
      if (typeof record.name !== 'string' || record.name === 'unknown') continue;
      const observed = names.get(record.name) ?? new Set();
      observed.add(record.runId);
      names.set(record.name, observed);
    }
    return {
      runsWithRecords: new Set(records.map((record) => record.runId)).size,
      denominator: runs.length,
      interpretation: 'observed per-run lower bounds; missing records do not establish non-use',
      names: [...names].sort(([a], [b]) => a.localeCompare(b))
        .map(([name, runIds]) => ({ name, runs: runIds.size, observedShare: round(runIds.size / runs.length) })),
    };
  };
  return {
    workflow: runs[0].workflowPath,
    n: runs.length, completed: completed.length, nonterminal: runs.length - completed.length,
    status: sampleStatus(completed.length),
    outcomes: { n: completed.length, status: sampleStatus(completed.length),
      weights: weights(completed.map((run) => run.conclusion || 'unknown')) },
    zeroCreatedIssueOrPR: binary(features.map((run) => run.noCreation)),
    outputCoverage: {
      completed: features.length,
      reconciledManifests: features.filter((run) => run.noCreation !== null).length,
      runsWithObservedCreation: features.filter((run) => run.observedCreations > 0).length,
      interpretation: 'zero/creation-count estimates use only count-reconciled manifests; observed creations elsewhere remain lower bounds',
    },
    reportIncompleteObserved: {
      runs: features.filter((run) => run.terminalOutcome === 'report_incomplete').length,
      denominator: features.length,
      interpretation: 'observed lower bound; absent terminal evidence does not establish a negative',
    },
    createdEntityMix: {
      n: issueRecords.length,
      status: sampleStatus(issueRecords.length),
      weights: weights(issueRecords.map((issue) => issue.isPullRequest ? 'pull_request' : 'issue')),
      interpretation: 'observed created entities only; not attempted, comment or dispatch safe outputs',
    },
    metrics,
    outcomeConditionals: completed.length >= MIN_SAMPLE
      ? [...new Set(features.map((run) => run.conclusion || 'unknown'))].sort().map((outcome) => {
          const matching = features.filter((run) => (run.conclusion || 'unknown') === outcome);
          return {
            outcome, n: matching.length,
            durationSeconds: numeric(matching.map((run) => run.agenticDurationSeconds), METRICS.agenticDurationSeconds),
            recordedAic: numeric(matching.map((run) => run.aicTotal), METRICS.aicTotal),
            safeItems: numeric(matching.map((run) => run.safeItemsCount), METRICS.safeItemsCount),
          };
        }) : [],
    activity: {
      observedDays: days.size,
      interarrivalHours: numeric(gaps, [0, 1, 6, 12, 24, 72, 168]),
      arrivalHourUTC: weights(times.map((time) => new Date(time).getUTCHours())),
      quietDayProbability: null,
      interpretation: 'sample-internal gaps only; uncollected days are not quiet days; boundary gaps censored',
    },
    closure: {
      outputs: issueRecords.length, timedOutputs: timedIssues.length,
      observedClosed: observedClosed.length,
      observedOpenAtStatusRead: timedIssues.filter((issue) => issue.closed === false
        && Number.isFinite(timestamp(issue.statusObservedAt))).length,
      delayHoursGivenObservedClosure: null,
      acceptanceDelay: null,
      interpretation: 'closure is not acceptance; canonical safe-output timestamps may fall back to run completion, so no authoritative creation-to-close delay is fitted; open entities are right-censored',
    },
    tools: observedNames('tools'), skills: observedNames('skills'),
    joint: {
      n: features.length,
      interpretation: 'resample one joint cell per completed run to preserve observed coarse associations; no causal claims',
      cells: weights(cells).map(({ value, ...cell }) => ({ ...JSON.parse(value), ...cell })),
    },
  };
}

/** Offline statistical estimation, not a dashboard source or production policy. */
export function fitBehaviorModel(evidence, scope) {
  if (!evidence.runs.length) throw new Error('No observed agentic workflow runs in the selected scope/window');
  const seen = new Set();
  const workflows = new Map();
  for (const run of evidence.runs) {
    if (seen.has(run.id)) throw new Error('Evidence must contain unique canonical run identities');
    seen.add(run.id);
    const group = workflows.get(run.workflowId) ?? [];
    group.push(run);
    workflows.set(run.workflowId, group);
  }
  return {
    format: 'cao.empirical-aw-behavior/v1',
    scope,
    estimation: {
      method: 'unsmoothed empirical weights; linear sample quantiles; Wilson marginal intervals',
      minimumDescriptiveSample: MIN_SAMPLE,
      assumptions: [
        'The bounded retained sample describes only itself, not a representative enterprise fleet.',
        'Joint cell resampling assumes sampled runs within one workflow are exchangeable.',
        'Wilson intervals assume independent Bernoulli observations; temporal dependence can understate uncertainty.',
        'No smoothing, pooling, lifecycle extrapolation or missing-as-zero imputation is applied.',
      ],
      limitations: [
        'Newest-run, artifact-retention, download-budget and workflow-frequency selection bias are uncorrected.',
        'Run identity retains the latest observed attempt, not an independent sample of every attempt.',
        'Safe item counts include non-creation outputs and are not issue/PR batch sizes.',
        'Token and AIC measurements are recorded totals, not attested complete-cost accounting.',
        'Durations use canonical run start/completion clocks, not measured agent-only execution time.',
        'No complete collection census: quiet-day probabilities and population activity rates are unavailable.',
        'No inferred acceptance, resolution, tool non-use or skill non-use from absent evidence.',
      ],
    },
    runs: evidence.runs.length,
    workflows: [...workflows].sort(([a], [b]) => String(a).localeCompare(String(b)))
      .map(([workflowId, runs]) => ({ workflowId, ...fitWorkflow(runs, evidence) })),
  };
}
