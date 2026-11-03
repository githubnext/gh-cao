import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { gunzipSync } from "node:zlib";

const MAX_REPOSITORIES = 1_000_000;
const DEFAULT_EVIDENCE = ".github/value-evidence/daily-file-diet.jsonl.gz";

const finite = (value, name, minimum = 0) => {
  const number = Number(value);
  if (!Number.isFinite(number) || number < minimum) throw new Error(`${name} must be at least ${minimum}`);
  return number;
};

const integer = (value, name, minimum = 0) => {
  const number = finite(value, name, minimum);
  if (!Number.isSafeInteger(number)) throw new Error(`${name} must be an integer`);
  return number;
};

function operationDemand(operation, repositories, workflowsPerRepository) {
  const scale = operation.scale ?? {};
  const raw = finite(scale.fixed ?? 0, `${operation.id}.scale.fixed`)
    + finite(scale.perRepository ?? 0, `${operation.id}.scale.perRepository`) * repositories
    + finite(scale.perWorkflow ?? 0, `${operation.id}.scale.perWorkflow`)
      * repositories * workflowsPerRepository;
  const hitRate = finite(operation.cacheHitRate ?? 0, `${operation.id}.cacheHitRate`);
  if (hitRate > 1) throw new Error(`${operation.id}.cacheHitRate must not exceed 1`);
  return raw * (1 - hitRate);
}

function normalizedScenario(input) {
  assert.equal(input?.schemaVersion, 1, "API cost scenario schemaVersion must be 1");
  if (typeof input.name !== "string" || !input.name.trim()) throw new Error("API cost scenario requires a name");
  const scenario = structuredClone(input);
  scenario.repositories = integer(scenario.repositories ?? 1, "repositories", 1);
  scenario.workflowsPerRepository = integer(
    scenario.workflowsPerRepository ?? 1,
    "workflowsPerRepository",
    1,
  );
  scenario.horizonHours = finite(scenario.horizonHours ?? 24, "horizonHours", Number.EPSILON);
  scenario.collectionWindowHours = finite(
    scenario.collectionWindowHours ?? 1,
    "collectionWindowHours",
    Number.EPSILON,
  );
  scenario.quotas ??= {};
  for (const [id, quota] of Object.entries(scenario.quotas)) {
    quota.limit = finite(quota.limit, `${id}.limit`, Number.EPSILON);
    quota.reserve = finite(quota.reserve ?? 0, `${id}.reserve`);
    quota.windowHours = finite(quota.windowHours ?? 1, `${id}.windowHours`, Number.EPSILON);
    if (quota.reserve >= quota.limit) throw new Error(`${id}.reserve must be below its limit`);
  }
  if (!Array.isArray(scenario.operations) || !scenario.operations.length) {
    throw new Error("API cost scenario requires operations");
  }
  for (const operation of scenario.operations) {
    if (typeof operation.id !== "string" || !operation.id) throw new Error("Each operation requires an id");
    if (!scenario.quotas[operation.pool]) throw new Error(`${operation.id} references unknown pool ${operation.pool}`);
    operation.cadenceHours = finite(
      operation.cadenceHours ?? scenario.horizonHours,
      `${operation.id}.cadenceHours`,
      Number.EPSILON,
    );
    operation.burstHours = finite(
      operation.burstHours ?? scenario.collectionWindowHours,
      `${operation.id}.burstHours`,
      Number.EPSILON,
    );
    operation.requestCount = finite(operation.requestCount ?? 1, `${operation.id}.requestCount`);
    operation.unitCost = finite(operation.unitCost ?? 1, `${operation.id}.unitCost`);
  }
  for (const admission of scenario.admissions ?? []) {
    if (!scenario.quotas[admission.pool]) throw new Error(`${admission.id} references unknown pool ${admission.pool}`);
    admission.grant = finite(admission.grant, `${admission.id}.grant`, Number.EPSILON);
    admission.actualPoolUnitsPerInstance = finite(
      admission.actualPoolUnitsPerInstance,
      `${admission.id}.actualPoolUnitsPerInstance`,
      Number.EPSILON,
    );
    admission.actualRequestsPerInstance = finite(
      admission.actualRequestsPerInstance,
      `${admission.id}.actualRequestsPerInstance`,
      Number.EPSILON,
    );
    admission.instancesPerRepository = finite(
      admission.instancesPerRepository ?? scenario.workflowsPerRepository,
      `${admission.id}.instancesPerRepository`,
      Number.EPSILON,
    );
    admission.cadenceHours = finite(
      admission.cadenceHours ?? scenario.horizonHours,
      `${admission.id}.cadenceHours`,
      Number.EPSILON,
    );
  }
  if (scenario.runner) {
    scenario.runner.minutesPerInstance = finite(
      scenario.runner.minutesPerInstance,
      "runner.minutesPerInstance",
      Number.EPSILON,
    );
    scenario.runner.instancesPerRepository = finite(
      scenario.runner.instancesPerRepository ?? scenario.workflowsPerRepository,
      "runner.instancesPerRepository",
      Number.EPSILON,
    );
    scenario.runner.parallelism = integer(scenario.runner.parallelism ?? 1, "runner.parallelism", 1);
  }
  return scenario;
}

function evaluateAt(scenario, repositories, mode = "modeled") {
  const workflows = repositories * scenario.workflowsPerRepository;
  const pools = Object.fromEntries(Object.entries(scenario.quotas).map(([id, quota]) => {
    const availablePerWindow = quota.limit - quota.reserve;
    return [id, {
      id,
      limit: quota.limit,
      reserve: quota.reserve,
      windowHours: quota.windowHours,
      availablePerWindow,
      capacityOverHorizon: availablePerWindow * scenario.horizonHours / quota.windowHours,
      collectionWindows: Math.max(1, scenario.collectionWindowHours / quota.windowHours),
      demandOverHorizon: 0,
      demandPerCycle: 0,
      requestsOverHorizon: 0,
      requestsPerCycle: 0,
      peakDemandPerWindow: 0,
    }];
  }));
  const operations = scenario.operations.map((operation) => {
    const countPerCycle = operationDemand(operation, repositories, scenario.workflowsPerRepository);
    const singleRepositoryCount = operationDemand(operation, 1, scenario.workflowsPerRepository);
    const cycles = scenario.horizonHours / operation.cadenceHours;
    const unitsPerCycle = countPerCycle * operation.unitCost;
    const requestsPerCycle = countPerCycle * operation.requestCount;
    const pool = pools[operation.pool];
    const fleetWindows = Math.max(1, scenario.collectionWindowHours / pool.windowHours);
    const operationWindows = Math.max(1, operation.burstHours / pool.windowHours);
    const peakUnits = Math.ceil(Math.max(
      unitsPerCycle / fleetWindows,
      singleRepositoryCount * operation.unitCost / operationWindows,
    ));
    pool.demandPerCycle += unitsPerCycle;
    pool.demandOverHorizon += unitsPerCycle * cycles;
    pool.requestsPerCycle += requestsPerCycle;
    pool.requestsOverHorizon += requestsPerCycle * cycles;
    pool.peakDemandPerWindow += peakUnits;
    return {
      id: operation.id,
      pool: operation.pool,
      countPerCycle,
      unitsPerCycle,
      requestsPerCycle,
      cadenceHours: operation.cadenceHours,
      burstHours: operation.burstHours,
      peakUnits,
    };
  });
  const violations = [];
  for (const pool of Object.values(pools)) {
    pool.sustainedUtilization = pool.demandOverHorizon / pool.capacityOverHorizon;
    pool.peakUtilization = pool.peakDemandPerWindow / pool.availablePerWindow;
    if (pool.sustainedUtilization > 1) violations.push(`${pool.id}:sustained`);
    if (pool.peakUtilization > 1) violations.push(`${pool.id}:peak`);
  }
  const admissions = (scenario.admissions ?? []).map((admission) => {
    const quota = scenario.quotas[admission.pool];
    const available = quota.limit - quota.reserve;
    const collectionWindows = Math.max(1, scenario.collectionWindowHours / quota.windowHours);
    const instances = repositories * admission.instancesPerRepository;
    const peakInstances = Math.ceil(instances / collectionWindows);
    const modeledPerWindow = admission.grant > available ? 0
      : Math.floor((available - admission.grant) / admission.actualPoolUnitsPerInstance) + 1;
    const guaranteedPerWindow = Math.floor(available / admission.grant);
    const grantExceeded = admission.actualRequestsPerInstance > admission.grant;
    const supportedPerWindow = mode === "guaranteed" ? guaranteedPerWindow : modeledPerWindow;
    if (grantExceeded) violations.push(`${admission.id}:collector-grant`);
    if (peakInstances > supportedPerWindow) violations.push(`${admission.id}:${mode}-admission`);
    return {
      id: admission.id,
      pool: admission.pool,
      grant: admission.grant,
      instances,
      peakInstances,
      actualPoolUnitsPerInstance: admission.actualPoolUnitsPerInstance,
      actualRequestsPerInstance: admission.actualRequestsPerInstance,
      modeledPerWindow,
      guaranteedPerWindow,
      grantExceeded,
    };
  });
  let runner = null;
  if (scenario.runner) {
    const instances = repositories * scenario.runner.instancesPerRepository;
    const demandMinutes = instances * scenario.runner.minutesPerInstance;
    const capacityMinutes = scenario.collectionWindowHours * 60 * scenario.runner.parallelism;
    runner = {
      instances,
      demandMinutes,
      capacityMinutes,
      utilization: demandMinutes / capacityMinutes,
      minutesPerInstance: scenario.runner.minutesPerInstance,
      parallelism: scenario.runner.parallelism,
    };
    if (runner.utilization > 1) violations.push("runner:peak");
  }
  return {
    repositories,
    workflows,
    supported: violations.length === 0,
    violations,
    pools,
    operations,
    admissions,
    runner,
  };
}

function maximumRepositories(scenario, mode) {
  if (!evaluateAt(scenario, 1, mode).supported) return { value: 0, capped: false };
  let low = 1;
  let high = 2;
  while (high < MAX_REPOSITORIES && evaluateAt(scenario, high, mode).supported) {
    low = high;
    high *= 2;
  }
  if (high >= MAX_REPOSITORIES && evaluateAt(scenario, MAX_REPOSITORIES, mode).supported) {
    return { value: MAX_REPOSITORIES, capped: true };
  }
  high = Math.min(high, MAX_REPOSITORIES);
  while (low + 1 < high) {
    const middle = Math.floor((low + high) / 2);
    if (evaluateAt(scenario, middle, mode).supported) low = middle;
    else high = middle;
  }
  return { value: low, capped: false };
}

export function simulateApiCost(input) {
  const scenario = normalizedScenario(input);
  const requested = evaluateAt(scenario, scenario.repositories);
  const modeledMaximum = maximumRepositories(scenario, "modeled");
  const guaranteedMaximum = maximumRepositories(scenario, "guaranteed");
  const next = modeledMaximum.capped ? null : evaluateAt(scenario, modeledMaximum.value + 1);
  return {
    schemaVersion: 1,
    name: scenario.name,
    profile: scenario.profile ?? "custom",
    scenario: scenario.scenario ?? "custom",
    assumptions: scenario.assumptions ?? [],
    detail: scenario.detail ?? null,
    inputs: {
      repositories: scenario.repositories,
      workflowsPerRepository: scenario.workflowsPerRepository,
      horizonHours: scenario.horizonHours,
      collectionWindowHours: scenario.collectionWindowHours,
    },
    requested,
    capacity: {
      modeledMaximumRepositories: modeledMaximum.value,
      modeledMaximumWorkflows: modeledMaximum.value * scenario.workflowsPerRepository,
      modeledMaximumIsLowerBound: modeledMaximum.capped,
      guaranteedMaximumRepositories: guaranteedMaximum.value,
      guaranteedMaximumWorkflows: guaranteedMaximum.value * scenario.workflowsPerRepository,
      guaranteedMaximumIsLowerBound: guaranteedMaximum.capped,
      bottlenecks: next?.violations ?? [],
    },
  };
}

async function retainedDailyFileDietCounts(evidencePath) {
  const bytes = await readFile(evidencePath);
  const content = evidencePath.endsWith(".gz") ? gunzipSync(bytes).toString("utf8") : bytes.toString("utf8");
  const envelopes = content.trim().split("\n").filter(Boolean).map((line) => JSON.parse(line));
  const packet = envelopes.find((item) => item?.operational_value_evidence?.workflow === "daily-file-diet")
    ?.operational_value_evidence;
  if (!packet) throw new Error(`No Daily File Diet evidence in ${evidencePath}`);
  return {
    outputs: packet.facts.filter((fact) => fact["fact-kind"] === "output").length,
    implementations: packet.facts.filter((fact) => fact["fact-kind"] === "implementation").length,
    observedAt: packet.observedAt,
    windowEnd: packet.windowEnd,
  };
}

export async function dailyFileDietApiCostScenario(options = {}) {
  const scenarioName = options.scenario ?? "retained";
  if (!["retained", "hard-bound"].includes(scenarioName)) {
    throw new Error("Daily File Diet scenario must be retained or hard-bound");
  }
  let retained = { outputs: 95, implementations: 51, observedAt: null, windowEnd: null };
  let retainedSource = "profile-default";
  if (scenarioName === "retained") {
    try {
      retained = await retainedDailyFileDietCounts(options.evidencePath ?? DEFAULT_EVIDENCE);
      retainedSource = options.evidencePath ?? DEFAULT_EVIDENCE;
    } catch (error) {
      if (options.evidencePath || error?.code !== "ENOENT") throw error;
    }
  }
  const hard = scenarioName === "hard-bound";
  const trackedIssues = integer(options.trackedIssues ?? (hard ? 200 : retained.outputs), "trackedIssues");
  const newIssues = integer(options.newIssues ?? (hard ? 200 : 0), "newIssues");
  const mutablePulls = integer(options.mutablePulls ?? (hard ? 100 : 0), "mutablePulls");
  const newReceipts = integer(options.newReceipts ?? (hard ? 200 : 0), "newReceipts");
  const searchPagesPerTerm = integer(options.searchPagesPerTerm ?? (hard ? 10 : 1), "searchPagesPerTerm", 1);
  const timelinePagesPerIssue = integer(options.timelinePagesPerIssue ?? (hard ? 10 : 1), "timelinePagesPerIssue", 1);
  const pullFilePages = integer(options.pullFilePages ?? (hard ? 10 : 1), "pullFilePages", 1);
  const snapshotReads = integer(options.snapshotReads ?? 1, "snapshotReads");
  const wallMinutes = finite(options.wallMinutes ?? 10, "wallMinutes", Number.EPSILON);
  const burstHours = wallMinutes / 60;
  const graphqlPointsPerRequest = finite(options.graphqlPointsPerRequest ?? 1, "graphqlPointsPerRequest", Number.EPSILON);
  const corePerWorkflow = 2 + 1 + trackedIssues * timelinePagesPerIssue + newIssues * 2
    + mutablePulls * (1 + pullFilePages) + newReceipts * 2 + snapshotReads;
  const searchRequestsPerWorkflow = 6 * searchPagesPerTerm;
  const graphqlRequestsPerWorkflow = Math.ceil(trackedIssues / 10) + mutablePulls;
  const totalRequestsPerWorkflow = corePerWorkflow + searchRequestsPerWorkflow + graphqlRequestsPerWorkflow;
  return {
    schemaVersion: 1,
    name: `Daily File Diet GitHub API capacity (${scenarioName})`,
    profile: "daily-file-diet",
    scenario: scenarioName,
    repositories: integer(options.repositories ?? 1, "repositories", 1),
    workflowsPerRepository: 1,
    horizonHours: finite(options.horizonHours ?? 24, "horizonHours", Number.EPSILON),
    collectionWindowHours: finite(options.collectionWindowHours ?? 1, "collectionWindowHours", Number.EPSILON),
    quotas: {
      core: {
        limit: finite(options.coreLimit ?? 5000, "coreLimit", Number.EPSILON),
        reserve: finite(options.coreReserve ?? 2000, "coreReserve"),
        windowHours: 1,
      },
      search: {
        limit: finite(options.searchLimit ?? 30, "searchLimit", Number.EPSILON),
        reserve: finite(options.searchReserve ?? 0, "searchReserve"),
        windowHours: finite(options.searchWindowMinutes ?? 1, "searchWindowMinutes", Number.EPSILON) / 60,
      },
      graphql: {
        limit: finite(options.graphqlLimit ?? 5000, "graphqlLimit", Number.EPSILON),
        reserve: finite(options.graphqlReserve ?? 500, "graphqlReserve"),
        windowHours: 1,
      },
    },
    operations: [
      { id: "repository-aliases-and-source-history", pool: "core", scale: { perWorkflow: 3 }, cadenceHours: 24, burstHours },
      {
        id: "tracked-issue-timelines",
        pool: "core",
        scale: { perWorkflow: trackedIssues * timelinePagesPerIssue },
        cadenceHours: 24,
        burstHours,
      },
      { id: "new-output-provenance", pool: "core", scale: { perWorkflow: newIssues * 2 }, cadenceHours: 24, burstHours },
      {
        id: "mutable-pull-details-and-files",
        pool: "core",
        scale: { perWorkflow: mutablePulls * (1 + pullFilePages) },
        cadenceHours: 24,
        burstHours,
      },
      { id: "new-receipt-metadata-and-download", pool: "core", scale: { perWorkflow: newReceipts * 2 }, cadenceHours: 24, burstHours },
      { id: "snapshot-cutoff", pool: "core", scale: { perWorkflow: snapshotReads }, cadenceHours: 24, burstHours },
      {
        id: "output-discovery-search",
        pool: "search",
        scale: { perWorkflow: searchRequestsPerWorkflow },
        cadenceHours: 24,
        burstHours,
      },
      {
        id: "issue-and-pull-relationship-graphs",
        pool: "graphql",
        scale: { perWorkflow: graphqlRequestsPerWorkflow },
        unitCost: graphqlPointsPerRequest,
        cadenceHours: 24,
        burstHours,
      },
    ],
    admissions: [{
      id: "reviewed-collector-grant",
      pool: "core",
      grant: finite(options.collectorGrant ?? 700, "collectorGrant", Number.EPSILON),
      actualPoolUnitsPerInstance: corePerWorkflow,
      actualRequestsPerInstance: totalRequestsPerWorkflow,
      instancesPerRepository: 1,
      cadenceHours: 24,
    }],
    runner: {
      minutesPerInstance: wallMinutes,
      instancesPerRepository: 1,
      parallelism: integer(options.parallelism ?? 1, "parallelism", 1),
    },
    assumptions: [
      "All due collectors are evenly spread across the collection window; peak demand is still checked per quota window.",
      "The retained scenario refreshes every tracked issue timeline but reuses merged pull, receipt, Git inventory, and blob evidence.",
      `The scenario uses ${searchPagesPerTerm} Search page(s) per term, ${timelinePagesPerIssue} timeline page(s) per issue, and ${graphqlPointsPerRequest} GraphQL point(s) per request.`,
      `It includes ${newIssues} new issue(s), ${mutablePulls} mutable pull request(s), and ${newReceipts} new receipt(s) per repository and cadence.`,
      "Git transport is excluded from GitHub REST, Search, and GraphQL quotas and remains separately bounded.",
      "The guaranteed capacity assumes every admitted collector consumes its full reviewed request grant.",
      `The retained population came from ${retainedSource}.`,
      ...(retained.observedAt ? [`Retained evidence observed ${retained.observedAt} with cutoff ${retained.windowEnd}.`] : []),
    ],
    detail: {
      trackedIssues,
      retainedImplementations: retained.implementations,
      newIssues,
      mutablePulls,
      newReceipts,
      searchPagesPerTerm,
      timelinePagesPerIssue,
      pullFilePages,
      snapshotReads,
      corePerWorkflow,
      searchRequestsPerWorkflow,
      graphqlRequestsPerWorkflow,
      totalRequestsPerWorkflow,
    },
  };
}

export async function readApiCostScenario(filename) {
  return JSON.parse(await readFile(filename, "utf8"));
}

export function formatApiCostMarkdown(report) {
  const rows = Object.values(report.requested.pools).map((pool) =>
    `| ${pool.id} | ${pool.demandOverHorizon.toFixed(1)} | ${pool.capacityOverHorizon.toFixed(1)} | ${(pool.sustainedUtilization * 100).toFixed(1)}% | ${pool.peakDemandPerWindow.toFixed(1)} | ${pool.availablePerWindow.toFixed(1)} | ${(pool.peakUtilization * 100).toFixed(1)}% |`);
  return [
    `# ${report.name}`,
    "",
    `Requested scale: **${report.inputs.repositories} repositories / ${report.requested.workflows} workflows**.`,
    `Modeled maximum: **${report.capacity.modeledMaximumRepositories} repositories**.`,
    `Guaranteed at declared grants: **${report.capacity.guaranteedMaximumRepositories} repositories**.`,
    `Bottleneck at the next repository: **${report.capacity.bottlenecks.join(", ") || "none within search bound"}**.`,
    "",
    "| Pool | Horizon demand | Horizon capacity | Sustained | Peak demand/window | Available/window | Peak |",
    "| --- | ---: | ---: | ---: | ---: | ---: | ---: |",
    ...rows,
    "",
    ...(report.requested.runner ? [
      `Runner: ${report.requested.runner.demandMinutes.toFixed(1)} / ${report.requested.runner.capacityMinutes.toFixed(1)} minutes (${(report.requested.runner.utilization * 100).toFixed(1)}%).`,
      "",
    ] : []),
    ...report.assumptions.map((assumption) => `- ${assumption}`),
    "",
  ].join("\n");
}
