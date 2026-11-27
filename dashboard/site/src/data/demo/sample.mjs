import { createDebug } from "../../debug.js";
import { demoSha256 as sha } from "./hash.mjs";
import { enterpriseSessionEvents } from "./session.mjs";
import { buildInventoryDashboardSources } from "../../../../report/inventory-sources.mjs";
import { parsePackageManifest } from "../../../../report/package-manifest.mjs";
import { parseUnifiedSessionDocument } from "../adapters/unified-session.js";
import { operationalValueEvidenceId } from "../model/operational-value-evidence.js";
import { VALUE_COLLECTION_REASONS, workflowValueCollectionId } from "../model/workflow-value-collection.js";

const debugSample = createDebug("sample");

const owner = "contoso";
export const enterpriseRepositoryVocabulary = Object.freeze(`
acorn alder almond amber anchor apple apricot arch aspen aster atlas autumn bamboo banana barley basin
basket bay beacon bean beech bell berry birch biscuit blossom bluebonnet blueberry board book bottle boulder
bowl branch breeze brick bridge brook brush bubble bucket buckle bud butter button cabin cactus calendar
camellia candle canopy canyon canvas caramel card carpet carrot carton cascade cedar celery chalk cherry chestnut
chime cinnamon circle citrine clay clover cloud cobalt cocoa coconut comet compass coral cork corn cosmos
cotton cove crane crescent crocus crystal cup current daffodil dahlia daisy dawn dew diamond dimple dish
dock dome dune elm emerald evening fern field fig finch fir flannel flax fleece flint flora
flower flute forest fountain frame freesia frost garden garnet gate gazebo gem ginger glacier glass glen
globe glow gold granite grape grass grove guava harbor harvest hazel hearth heather hill holly honey
horizon hyacinth iceberg iris island ink ivy jade jasmine jasper jelly jewel juniper kale kettle key
lagoon lake lantern larch laurel lavender leaf lemon lentil lilac lily lime linen lotus magnolia maple
marble meadow melon mesa mint mist moon moss mug mulberry mushroom nectar nutmeg oak oat ocean
olive onyx opal orange orchard orchid oregano paper paprika parsley pastel path peach pear pebble pecan
petal pine pistachio plum pond poplar poppy porcelain prism pumpkin quartz quilt radish rain rainbow reed
reef ribbon rice ridge river robin rock rose rosemary ruby rye saffron sage sail sand sapling
satin sea seed shell shore silk sky slate snow spruce star stone summit sunflower thyme willow
`.trim().split(/\s+/));
export const enterpriseBlockedDomains = Object.freeze(enterpriseRepositoryVocabulary.slice(0, 128)
  .map(word => `unapproved-${word}.example.invalid`));
// Public identifiers observed in bounded live usage; volumes remain illustrative.
const approvedDomains = [
  ["api.githubcopilot.com", 2],
  ["github.com", 0.6],
  ["packagefeedproxy.microsoft.io", 1.5],
  ["proxy.golang.org", 1],
  ["sum.golang.org", 0.5],
  ["storage.googleapis.com", 0.2],
];
const observedMcpTools = [
  ["github", "issue_read"],
  ["github", "pull_request_read"],
  ["github", "search_repositories"],
  ["github", "actions_list"],
  ["github", "actions_get"],
  ["github", "get_commit"],
  ["github", "list_commits"],
  ["github", "list_pull_requests"],
  ["github", "get_job_logs"],
  ["github", "get_global_security_advisory"],
  ["agenticworkflows", "health"],
  ["agenticworkflows", "audit"],
];
const catalog = `${owner}/automation-catalog`;
const dayMs = 86_400_000;
const runHistoryDays = 21;
const revision = sha("synthetic-enterprise-catalog-v2").slice(0, 40);
const previousRevision = sha("synthetic-enterprise-catalog-v1").slice(0, 40);
const workflows = [
  { id: "dependency-review", name: "Dependency review", campaign: "dependency-health", role: "orchestrator", goal: "Keep dependencies supported and patched", metric: "Dependency repairs" },
  { id: "repository-assistant", name: "Repository assistant", campaign: "dependency-health", role: "worker", goal: "Resolve actionable repository maintenance", metric: "Maintenance repairs" },
  { id: "security-review", name: "Security review", campaign: "secure-delivery", role: "orchestrator", goal: "Reduce verified security findings", metric: "Security repairs" },
  { id: "documentation", name: "Documentation maintenance", campaign: "knowledge-care", role: "orchestrator", goal: "Keep service runbooks current", metric: "Runbook updates" },
];
const workflowNames = [
  [
    "Dependency review", "Dependency health check", "Package version review", "Package support check",
    "Library update review", "Dependency catalog check", "Package freshness review", "Library compatibility check",
    "Dependency inventory review", "Package manifest check", "Dependency planning review", "Library support review",
    "Package alignment check", "Dependency baseline review", "Package maintenance review", "Library version check",
  ],
  [
    "Repository assistant", "Repository upkeep", "Build health review", "Test maintenance",
    "Workspace cleanup", "Configuration review", "Code organization", "Service maintenance",
    "Repository hygiene", "Build configuration check", "Test reliability review", "Maintenance planning",
    "Workspace health check", "Service readiness review", "Repository baseline check", "Project upkeep",
  ],
  [
    "Security review", "Access policy review", "Permission baseline check", "Dependency safety review",
    "Configuration safety check", "Network policy review", "Artifact integrity check", "Release safety review",
    "Supply chain review", "Policy alignment check", "Repository safeguards", "Trust boundary review",
    "Secret handling review", "Build safety check", "Service protection review", "Security baseline check",
  ],
  [
    "Documentation maintenance", "Runbook refresh", "Guide consistency check", "Reference update review",
    "Project notes review", "Service handbook refresh", "Documentation link check", "Readme maintenance",
    "Knowledge base review", "Setup guide refresh", "Example catalog review", "Documentation coverage check",
    "Operations guide review", "Reference consistency check", "Handbook maintenance", "Getting started review",
  ],
];
export const enterpriseWorkflowNames = Object.freeze(workflowNames.flat());

function workflowsFor(repository) {
  return workflows.map((workflow, index) => ({
    ...workflow,
    name: workflowNames[index][Number.parseInt(sha(`${repository}:${workflow.id}:name`).slice(0, 2), 16) % workflowNames[index].length],
  }));
}
const campaigns = [
  { id: "dependency-health", name: "Dependency health", description: "Synthetic coordinated dependency upgrades across the enterprise.", icon: "package" },
  { id: "secure-delivery", name: "Secure delivery", description: "Synthetic security reviews and verified operational runbooks.", icon: "shield-check" },
  { id: "knowledge-care", name: "Knowledge care", description: "Synthetic maintenance of enterprise service runbooks.", icon: "book" },
];

// Keyed streams keep repository traits and run samples reproducible across rebuilds.
function distributionFor(key) {
  // Keep the established workload distributions independent of the display rename.
  const seed = key.replaceAll(`${owner}/`, "sample-enterprise/");
  const digest = sha(`enterprise-distributions-v1:${seed}`);
  let state = Number.parseInt(digest.slice(0, 8).match(/../g).reverse().join(""), 16);
  const random = () => {
    state = (state + 0x6d2b79f5) >>> 0;
    let value = Math.imul(state ^ state >>> 15, state | 1);
    value ^= value + Math.imul(value ^ value >>> 7, value | 61);
    return ((value ^ value >>> 14) >>> 0) / 4294967296;
  };
  const lognormal = (median, spread) => median * Math.exp(spread
    * Math.sqrt(-2 * Math.log(1 - random())) * Math.cos(2 * Math.PI * random()));
  return {
    random, lognormal,
    poisson(mean) {
      const limit = Math.exp(-mean);
      let product = 1;
      let count = 0;
      do { product *= 1 - random(); count++; } while (product > limit);
      return count - 1;
    },
    weighted(choices) {
      let remainder = random() * choices.reduce((sum, [, weight]) => sum + weight, 0);
      for (const [value, weight] of choices) {
        remainder -= weight;
        if (remainder < 0) return value;
      }
      return choices.at(-1)[0];
    },
  };
}

function repositoryProfile(repository, department, service) {
  const sample = distributionFor(repository);
  return {
    activity: ({ commerce: 1.5, platform: 1.2, analytics: 0.8, workplace: 0.55 })[department]
      * ({ api: 1.4, web: 1, infrastructure: 0.65 })[service] * Math.min(20, sample.lognormal(1, 1)),
    complexity: sample.lognormal(service === "infrastructure" ? 1.5 : 1, 0.3),
    failureRate: 0.035 + sample.random() * 0.09,
    acceptanceRate: 0.45 + sample.random() * 0.4,
  };
}

function workflowProfile(repository, workflow, profile) {
  const sample = distributionFor(`${repository}:${workflow.id}:behavior`);
  const traits = {
    "dependency-review": { activity: 1.5, outputRate: 0.8, batchSize: 2.5, pullRequestRate: 0.12, resolutionDays: 5, complexity: 0.8 },
    "repository-assistant": { activity: 1, outputRate: 0.65, batchSize: 0.3, pullRequestRate: 0.9, resolutionDays: 2, complexity: 1.4 },
    "security-review": { activity: 0.8, outputRate: 0.45, batchSize: 1.8, pullRequestRate: 0.08, resolutionDays: 7, complexity: 1.6 },
    documentation: { activity: 0.6, outputRate: 0.3, batchSize: 0.15, pullRequestRate: 0.95, resolutionDays: 1, complexity: 0.6 },
  }[workflow.id];
  const quiet = sample.random() < 0.18;
  return {
    ...profile,
    activity: profile.activity * traits.activity * Math.min(3, sample.lognormal(1, 0.65)),
    complexity: profile.complexity * traits.complexity,
    failureRate: Math.min(0.25, profile.failureRate * sample.lognormal(1, 0.45)),
    outputRate: quiet ? 0.015 : Math.min(0.95, traits.outputRate * sample.lognormal(1, 0.45)),
    batchSize: traits.batchSize * Math.min(3, sample.lognormal(1, 0.65)),
    pullRequestRate: Math.min(0.99, traits.pullRequestRate * sample.lognormal(1, 0.2)),
    resolutionDays: traits.resolutionDays * sample.lognormal(1, 0.5),
    acceptanceRate: Math.min(0.95, profile.acceptanceRate * sample.lognormal(1, 0.3)),
    toolIntensity: sample.lognormal(1, 0.45),
    skillRate: 0.35 + sample.random() * 0.6,
    evaluationRate: 0.3 + sample.random() * 0.65,
  };
}

function inventoryFor(repository, index, generatedAt) {
  const standaloneDocumentation = index % 3 === 2;
  const campaignPolicy = Object.fromEntries(campaigns.map(campaign => [campaign.id, {
    enabled: true, mode: index % 3 ? "live" : "review", icon: campaign.icon,
    "max-repositories": 12, "rollout-percent": 100, "monthly-ai-credit-budget": 5000,
    target_policies: { [repository]: { mode: index % 3 ? "live" : "review" } },
    worker_policies: Object.fromEntries(workflows.filter(workflow => workflow.campaign === campaign.id && workflow.role === "worker")
      .map(workflow => [workflow.id, { worker: workflow.id, enabled: true, max_mode: "live" }])),
  }]));
  const policy = { scope: { "allowed-repositories": [repository] }, campaigns: campaignPolicy };
  const bundles = campaigns.flatMap(campaign => {
    const members = workflows.filter(workflow => workflow.campaign === campaign.id
      && !(standaloneDocumentation && workflow.id === "documentation"));
    if (!members.length) return [];
    return [{
      ...campaign, workflow: `.github/workflows/${members[0].id}.md`,
      source: `${catalog}/${campaign.id}@${revision}`, version: index % 3 ? revision : previousRevision,
      minVersion: "1.2.2", compiled: true, maxAiCredits: 25, missingWorkers: [],
      readmePath: `${campaign.id}/README.md`, readme: `# ${campaign.name}\n\n${campaign.description}\n\nGenerated demonstration only; no live execution.`,
      workers: members.slice(1).map(workflow => ({
        id: workflow.id, sourcePath: `.github/workflows/${workflow.id}.md`, compiled: true, maxAiCredits: 50,
      })),
    }];
  });
  const packages = campaigns.map(campaign => parsePackageManifest(
    `name: ${campaign.name}\ndescription: ${campaign.description}\nicon: ${campaign.icon}\nversion: 2.0.0\nincludes:\n  - .github/workflows/${workflows.find(workflow => workflow.campaign === campaign.id).id}.md\n`,
    { repository: catalog, registryId: "enterprise-demo", registryName: "Synthetic enterprise catalog",
      precedence: 1, ref: "v2.0.0", resolvedCommit: revision, path: `${campaign.id}/aw.yml`,
      readme: `# ${campaign.name}\n\n${campaign.description}`, readmePath: `${campaign.id}/README.md`,
      repositoryCounts: { stars: 128, forks: 24 } },
  ));
  return buildInventoryDashboardSources({
    repository, generatedAt,
    discoveredRepositories: [{ full_name: repository, visibility: index % 2 ? "private" : "internal" }],
    inventory: {
      workflows: workflowsFor(repository).map(workflow => ({
        ...workflow, role: standaloneDocumentation && workflow.id === "documentation" ? "standalone" : workflow.role,
        sourcePath: `.github/workflows/${workflow.id}.md`, compiled: true,
        ghAwVersion: index % 3 ? "1.2.2" : "1.2.1", maxAiCredits: 50,
        source: `${catalog}/.github/workflows/${workflow.id}.md@${revision}`, version: revision,
      })),
      bundles,
    },
    controlSettings: {
      ...policy, allowed_repositories: [repository],
      policy_document: { version: 1, "control-plane": policy },
      policy_source: ".github/workflows/cao.json",
      policy_resolution: { status: "available" },
    },
    latestGhAwVersion: "1.2.2", latestCampaignResolution: { commits: { [catalog]: revision } },
    marketplace: {
      packages,
      diagnostics: [{ id: "enterprise-demo", name: "Synthetic enterprise catalog", repository: catalog,
        status: "available", packages: packages.length, "observed-at": generatedAt }],
    },
  });
}

function runSample(repository, workflow, workflowIndex, runId, day, ordinal, now, profile, coverageDomain) {
  const sample = distributionFor(`${repository}:${workflow.id}:${day}:${ordinal}`);
  const candidate = sample.random() < 0.5;
  const complexity = Math.min(12, Math.max(0.15, sample.lognormal(profile.complexity, 0.8)));
  const incident = day === 5 || day === 6;
  const outcome = sample.weighted([
    ["success", 1 - profile.failureRate * (incident ? 2.5 : 1)],
    ["failure", profile.failureRate * (incident ? 2.5 : 1)],
    ["timed_out", 0.012 * complexity], ["cancelled", 0.025],
    ["action_required", day < 3 ? 0.06 : 0],
    ["pending", day === 0 ? 0.12 : 0],
  ]);
  const pending = outcome === "pending";
  const approvalRequired = outcome === "action_required";
  const failed = outcome === "failure" || outcome === "timed_out";
  const successful = outcome === "success";
  const model = sample.weighted(complexity > 1.5
    ? [["gpt-5.3-codex", 0.6], ["claude-sonnet-5.5", 0.35], ["claude-haiku-5.5", 0.05]]
    : [["gpt-5.3-codex", 0.2], ["claude-sonnet-5.5", 0.3], ["claude-haiku-5.5", 0.5]]);
  const plannedDuration = Math.round(Math.min(7200, Math.max(15,
    sample.lognormal(260 * complexity * (candidate ? 0.85 : 1) * (failed ? 1.6 : 1), 0.5))));
  const today = Math.floor(now / dayMs) * dayMs;
  const dayStart = today - day * dayMs;
  const availableTime = day === 0 ? now - today : dayMs;
  const latestHour = Math.max(0, availableTime - plannedDuration * 1000) / 3600_000;
  const hour = latestHour > 8 && sample.random() < 0.8
    ? 8 + sample.random() * (Math.min(19, latestHour) - 8) : sample.random() * latestHour;
  const start = pending ? now - Math.round(sample.random() * plannedDuration * 800)
    : dayStart + Math.round(hour * 3600_000);
  const durationSeconds = pending ? (now - start) / 1000 : Math.min(plannedDuration, (now - start) / 1000);
  const startedAt = new Date(start).toISOString();
  const completedAt = new Date(start + durationSeconds * 1000).toISOString();
  const input = Math.round(sample.lognormal(8500 * complexity, 0.35));
  const output = Math.round(sample.lognormal(900 * complexity, 0.45));
  const cacheRead = Math.round(input * (1.5 + sample.random() * 5));
  const cacheWrite = Math.round(input * (1 + sample.random() * 2));
  // Illustrative model-relative prices, not a quotation of provider billing.
  const price = { "gpt-5.3-codex": 1.4, "claude-sonnet-5.5": 1, "claude-haiku-5.5": 0.3 }[model];
  const aic = Number(((input + cacheRead * 0.1 + cacheWrite * 1.25 + output * 4) / 6000 * price).toFixed(4));
  const tokens = { total_input_tokens: input, total_output_tokens: output,
    total_cache_read_tokens: cacheRead, total_cache_write_tokens: cacheWrite,
    total_reasoning_tokens: Math.round(output * sample.random() * 0.6), total_aic: aic };
  const outputCount = successful && sample.random() < profile.outputRate
    ? Math.min(8, 1 + sample.poisson(profile.batchSize)) : 0;
  const safeOutputs = Array.from({ length: outputCount }, (_, index) => {
    const pullRequest = sample.random() < profile.pullRequestRate;
    const resolutionHours = sample.lognormal(profile.resolutionDays * 24 * (pullRequest ? 0.7 : 1), 1);
    const closed = resolutionHours * 3600_000 < now - Date.parse(completedAt);
    const number = runId * 100 + index + 1;
    return {
      type: pullRequest ? "create_pull_request" : "create_issue", repo: repository, number,
      url: `https://github.com/${repository}/${pullRequest ? "pull" : "issues"}/${number}`, timestamp: completedAt,
      github_issue_status: { state: closed ? "closed" : "open", closed, state_reason: closed ? "completed" : null,
        closed_at: closed ? new Date(Date.parse(completedAt) + resolutionHours * 3600_000).toISOString() : null,
        observed_at: new Date(now).toISOString() },
    };
  });
  const mode = sample.random() < 0.7 ? "live" : "review";
  let apiCalls = Math.max(2, sample.poisson(3 * complexity));
  const blocked = sample.random() < (incident ? 0.18 : 0.06) || Boolean(coverageDomain);
  const blockedDomain = coverageDomain ?? enterpriseBlockedDomains[runId % enterpriseBlockedDomains.length];
  const frictionEvents = sample.poisson(1 + complexity * (failed ? 3 : 1));
  const frictionRatio = frictionEvents ? Math.min(0.7, sample.lognormal(failed ? 0.22 : 0.045, 0.65)) : 0;
  const quality = Number(Math.min(0.99, Math.max(0.2, sample.lognormal(failed ? 0.58 : 0.9, 0.09))).toFixed(3));
  const passed = quality >= 0.8;
  const failureKind = outcome === "timed_out" ? "timeout" : sample.weighted([
    ["tool_error", 0.5], ["rate_limit", incident ? 0.4 : 0.15], ["validation_error", 0.25],
  ]);
  const toolCount = Math.min(24, sample.poisson(4 * complexity * profile.toolIntensity));
  const toolChoices = [
    [["github", "get_file_contents"], 3],
    [["github", "list_issues"], workflow.role === "orchestrator" ? 4 : 1],
    [["github", "search_code"], workflow.id === "security-review" ? 5 : 2],
    [["github", "pull_request_read"], workflow.role === "worker" ? 3 : 1],
    [["bash", "bash"], workflow.role === "worker" ? 4 : 1],
    ...observedMcpTools.map(tool => [tool, 0.5]),
  ];
  const toolCalls = Array.from({ length: toolCount }, (_, index) => {
    const [serverName, toolName] = sample.weighted(toolChoices);
    return {
      tool_call_id: `sample-tool-${runId}-${index}`, server_name: serverName,
      tool_name: toolName, timestamp: new Date(start + durationSeconds * 1000 * (index + 1) / (toolCount + 1)).toISOString(),
      status: failed && index === 0 ? "failure" : "success",
      input_size: Math.round(sample.lognormal(100 * complexity, 0.4)),
      output_size: Math.round(sample.lognormal(1000 * complexity, 0.7)),
    };
  });
  apiCalls = Math.max(apiCalls, toolCalls.filter(call => call.server_name === "github").length);
  const approvedRequests = Object.fromEntries(approvedDomains
    .map(([domain, rate]) => [domain, { allowed: sample.poisson(rate * Math.min(3, complexity)), blocked: 0 }])
    .filter(([, counts]) => counts.allowed > 0));
  const skillActivations = [workflow.id, "test-validation"].filter(() => sample.random() < profile.skillRate)
    .map(name => ({
      name, source: "repository", timestamp: startedAt, invocation_count: Math.min(5, 1 + sample.poisson(complexity)),
      failed_count: failed ? 1 : 0, status: failed ? "failed" : "invoked",
    }));
  const run = {
    run_id: runId, run_attempt: "1", organization: owner, repository,
    workflow_name: workflow.name, workflow_path: `.github/workflows/${workflow.id}.md`,
    display_title: workflow.role === "worker"
      ? `Synthetic ${workflow.name} · ${repository} · ${mode}`
      : `Synthetic ${workflow.name}: ${workflow.goal}`,
    event: workflow.role === "worker" ? "workflow_dispatch" : "schedule", status: pending ? "in_progress" : "completed",
    conclusion: pending ? null : outcome,
    classification: outcome,
    created_at: startedAt, started_at: startedAt, updated_at: completedAt,
    branch: "main", head_sha: sha(`${repository}:${runId}`).slice(0, 40),
    url: new URL(`/${repository}/actions/runs/${runId}`, "https://github.com").href,
    rollout_mode: mode, duration: `${durationSeconds}s`, action_minutes: durationSeconds / 60,
    engine: "Copilot", engine_id: "copilot", agent_id: "copilot", agent_version: "1.0.106",
    engine_version: "1.0.106", gh_aw_version: "1.2.2", agent_runtime: "actions",
    model, model_id: model, requested_model: "auto", resolved_model: model,
    firewall_version: "0.9.0", gateway_version: "0.8.0",
    token_usage_summary: tokens, github_api_calls: apiCalls, safe_items_count: safeOutputs.length,
    error_count: failed ? 1 : 0,
    ...(failed ? { failure_kind: failureKind, failure_job: "agent", failure_step: workflow.name,
      failure_message: `Synthetic ${workflow.name} interrupted by ${failureKind.replaceAll("_", " ")}.` } : {}),
    mcp_tool_usage: { tool_calls: toolCalls },
    skill_activations: skillActivations,
    firewall_analysis: { requests_by_domain: {
      "api.github.com": { allowed: apiCalls, blocked: 0 },
      "registry.npmjs.org": { allowed: sample.poisson(workflowIndex === 0 ? 12 : 2), blocked: 0 },
      ...approvedRequests,
      ...(blocked ? { [blockedDomain]: { allowed: 0, blocked: 1 + sample.poisson(1) } } : {}),
    } },
    safe_outputs: safeOutputs,
    audit: {
      key_findings: failed ? [{ title: "Synthetic package access needs review", severity: "warning", timestamp: completedAt }] : [],
      recommendations: frictionRatio > 0.1 ? [{ action: "Reduce repeated metadata requests with one bounded batch", priority: "medium" }] : [],
      noops: successful && !outputCount ? [{ message: "Repository already meets the synthetic baseline", status: "success" }] : [],
    },
    friction: {
      measurement_state: "measured", canonical_unit: "aic", sources: ["agent"], total_events: frictionEvents,
      total_occurrences: frictionEvents, counted_occurrences: frictionEvents, suppressed_occurrences: 0, linked_invocations: frictionEvents,
      unattributed_occurrences: 0, cost: { aic: aic * (frictionEvents ? frictionRatio : 0),
        tokens: { input: Math.round(input * frictionRatio), output: Math.round(output * frictionRatio),
          total: Math.round(input * frictionRatio) + Math.round(output * frictionRatio) },
        turns: frictionEvents, tool_calls: frictionEvents, latency_ms: Math.round(durationSeconds * 1000 * frictionRatio) },
      total_run_aic: aic, friction_ratio: frictionEvents ? frictionRatio : 0,
    },
    experiments: { assignments: { "review-strategy": candidate ? "focused" : "baseline" } },
    graders: { results: [{
      id: "change-quality", name: "Change quality", source: "workflow", value: quality,
      status: passed ? "pass" : "fail", passed, unit: "ratio", direction: "higher_is_better",
      threshold: 0.8, timestamp: completedAt, included: true, evaluatorDigest: sha("synthetic-quality-v1"),
      experimentId: "review-strategy", variant: candidate ? "focused" : "baseline",
    }] },
    evals: [{ id: "behavior-preserved", answer: passed ? "YES" : "NO", runId: String(runId),
      timestamp: completedAt, experimentId: "review-strategy", variant: candidate ? "focused" : "baseline" }],
  };
  if (pending || approvalRequired || outcome === "cancelled" || outcome === "timed_out") {
    run.safe_outputs = [];
    run.safe_items_count = 0;
    run.graders = { results: [] };
    run.evals = [];
  } else if (sample.random() >= profile.evaluationRate) {
    run.graders = { results: [] };
    run.evals = [];
  }
  if (approvalRequired) {
    run.token_usage_summary = Object.fromEntries(Object.keys(tokens).map(key => [key, 0]));
    run.mcp_tool_usage.tool_calls = [];
    run.skill_activations = [];
    run.firewall_analysis.requests_by_domain = {};
    run.github_api_calls = 0;
    run.friction = { ...run.friction, total_events: 0, total_occurrences: 0, counted_occurrences: 0,
      linked_invocations: 0, total_run_aic: 0, friction_ratio: 0,
      cost: { aic: 0, tokens: { input: 0, output: 0, total: 0 }, turns: 0, tool_calls: 0, latency_ms: 0 } };
  }
  const events = enterpriseSessionEvents(run, distributionFor(`${repository}:${runId}:session`), complexity);
  const session = parseUnifiedSessionDocument(events.map(event => JSON.stringify(event)).join("\n"),
    "usage/aw_session.jsonl", { runId: String(runId), attempt: 1 });
  // Match the production session envelope; lifecycle observations remain separate.
  run.cao_unified_session = { version: 1, path: "usage/aw_session.jsonl", ...session };
  return run;
}

function valueSample(repository, workflow, now, profile) {
  const sample = distributionFor(`${repository}:${workflow.id}:value`);
  const instant = days => new Date(now - days * dayMs).toISOString();
  const header = {
    "fact-kind": "contract", "fact-id": "contract", goal: workflow.goal,
    acceptance: "Synthetic change accepted with independent behavior checks.",
    "workflow-name": workflow.name, "metric-id": "accepted-repairs", "metric-name": workflow.metric,
    "metric-unit": "repairs", "metric-direction": "increase", "metric-version": "v1",
    "metric-formula": "Count accepted, independently verified synthetic repairs once.",
    "measurement-basis": "workflow-contribution", "result-label": "accepted repairs",
    "snapshot-id": "backlog", "snapshot-name": "Actionable backlog", "snapshot-unit": "items",
    "snapshot-direction": "decrease", "snapshot-version": "v1", "snapshot-formula": "Count unresolved synthetic findings.",
    "outputs-available": true, "repository-state-available": true, "checks-available": true,
    "cadence-days": 1, "maturation-days": 0, "output-unit": "proposals", "implementation-unit": "merged changes",
    "output-scope": "Synthetic enterprise maintenance", "output-coverage": "complete",
  };
  const facts = [header,
    { "fact-kind": "coverage", "fact-id": "coverage", at: instant(14), "end-at": instant(0), "coverage-status": "complete" },
    { "fact-kind": "intent-source", "fact-id": "intent", label: "Synthetic workflow intent", url: `https://example.invalid/${repository}/blob/${revision}/${workflow.id}.md` },
    { "fact-kind": "quality-check", "fact-id": "behavior", label: "Behavior preserved", status: "verified", detail: "Synthetic checks passed at each accepted change." },
  ];
  let backlog = sample.poisson(18 * profile.complexity);
  const dailyValues = [];
  const acceptedRepairs = new Map();
  for (let day = 13; day >= 0; day--) {
    const proposals = sample.random() < profile.outputRate
      ? sample.poisson(profile.activity * (1 + profile.batchSize)) : 0;
    for (let proposal = 0; proposal < proposals; proposal++) {
      const output = `proposal-${day}-${proposal}`;
      const implementation = `repair-${day}-${proposal}`;
      const acceptanceDelay = sample.lognormal(profile.resolutionDays, 0.8);
      const accepted = acceptanceDelay < day && sample.random() < profile.acceptanceRate;
      const units = 1 + sample.poisson(1.5);
      facts.push({ "fact-kind": "output", "fact-id": output, label: "Maintenance proposal",
        title: `${workflow.metric}: ${output}`, at: instant(day), state: accepted ? "closed" : "open",
        origin: "corroborated", url: `https://example.invalid/${repository}/issues/${workflow.id}-${day}-${proposal}` });
      if (accepted) {
        const acceptanceDay = Math.floor(day - acceptanceDelay);
        acceptedRepairs.set(acceptanceDay, (acceptedRepairs.get(acceptanceDay) ?? 0) + units);
        facts.push(
          { "fact-kind": "implementation", "fact-id": implementation, label: `${workflow.metric} accepted`,
            at: instant(day - acceptanceDelay), "accepted-at": instant(day - acceptanceDelay), value: units,
            ref: sha(`${repository}:${workflow.id}:${day}:${proposal}`),
            origin: "corroborated", url: `https://example.invalid/${repository}/pull/${workflow.id}-${day}-${proposal}` },
          { "fact-kind": "output-implementation", "fact-id": implementation, "parent-id": output, "related-id": implementation },
          { "fact-kind": "implementation-lineage", "fact-id": output, "parent-id": implementation, "related-id": output, label: "connected" },
        );
      }
    }
    backlog = Math.max(0, backlog + sample.poisson(profile.activity * 1.5) - (acceptedRepairs.get(day) ?? 0));
    facts.push({ "fact-kind": "snapshot", "fact-id": `backlog-${day}`, at: instant(day), value: backlog,
      ref: sha(`${repository}:${workflow.id}:backlog-${day}`), "items-available": true });
    dailyValues.push({ day, value: backlog });
  }
  const packet = {
    schemaVersion: 1, id: operationalValueEvidenceId(repository, workflow.id, "accepted-repairs", "v1"),
    repository, workflow: workflow.id, sourcePath: `.github/workflows/${workflow.id}.md`,
    observedAt: instant(0), windowStart: instant(14), windowEnd: instant(0), contractDigest: sha(JSON.stringify(header)), facts,
  };
  const deferred = sample.random() < 0.2;
  const collection = {
    schemaVersion: 1, id: workflowValueCollectionId(repository, workflow.id),
    repository, workflow: workflow.id, sourcePath: packet.sourcePath, observedAt: instant(0),
    registration: "enabled", collector: "sample/collect-value.mjs", state: deferred ? "deferred" : "available",
    reasonCode: deferred ? "api-budget" : "captured", reason: VALUE_COLLECTION_REASONS[deferred ? "api-budget" : "captured"],
    policyDigest: sha("synthetic-value-policy"), lastAttemptAt: instant(deferred ? 1 : 0), lastAttemptOutcome: "available",
  };
  return [
    { schema_version: 2, kind: "operational_value_evidence", operational_value_evidence: packet },
    { schema_version: 2, kind: "workflow_value_collection", workflow_value_collection: collection },
    ...dailyValues.map(({ day, value }) => ({
      schema_version: 2, kind: "operational_value", operational_value: {
        repository, campaign: workflow.campaign, value_id: `${workflow.id}-backlog`,
        timestamp: new Date(Math.floor(now / dayMs) * dayMs - day * dayMs).toISOString(),
        value, metric_name: "Actionable backlog", metric_unit: "items", metric_direction: "decrease",
        workflow_slug: workflow.id, workflow_path: packet.sourcePath, workflow_name: workflow.name,
        cadence_days: 1, evaluator_digest: sha("synthetic-backlog-v1"), maturity_status: "matured", evaluation_mode: "observed",
      },
    })),
  ];
}

export const enterpriseDemo = Object.freeze({
  version: 12, repositories: 1_000, runs: 5_000, campaigns: 3, installedWorkflows: 4_000,
  activeRepositories: 1_000, installedRepositories: 1_000,
});

export function enterpriseRepository(index) {
  if (!Number.isSafeInteger(index) || index < 0 || index > 0xffff_ffff) {
    throw new RangeError("Synthetic repository index must be an unsigned 32-bit integer.");
  }
  // This bijective 32-bit mix fills all four word positions without naming collisions.
  let value = index ^ 0xa511e9b3;
  value = Math.imul(value ^ value >>> 16, 0x7feb352d);
  value = Math.imul(value ^ value >>> 15, 0x846ca68b);
  value = (value ^ value >>> 16) >>> 0;
  return `${owner}/${[24, 16, 8, 0].map(shift => enterpriseRepositoryVocabulary[value >>> shift & 255]).join("-")}`;
}

export function enterpriseRepositorySources(start, count, now) {
  const sources = buildInventoryDashboardSources({
    inventory: {}, generatedAt: new Date(now).toISOString(),
    discoveredRepositories: Array.from({ length: count }, (_, offset) => ({
      full_name: enterpriseRepository(start + offset),
      visibility: (start + offset) % 2 ? "private" : "internal",
    })),
  });
  return { repositories: { ...sources.repositories, metadata: {
    ...sources.repositories.metadata, "source-kind": "synthetic",
    "collection-operation": "synthetic-enterprise-demo",
    "collection-reason": "Generated sample enterprise; not observed GitHub activity.",
  } } };
}

export function enterpriseMemory(now) {
  const generatedAt = new Date(now).toISOString();
  return campaigns.map(campaign => {
    const content = [
      { repository: enterpriseRepository(0), observation: "Synthetic dependency upgrade accepted", nextReview: generatedAt, status: "verified" },
      { repository: enterpriseRepository(4), observation: "Synthetic review retained for approval", nextReview: generatedAt, status: "review" },
    ].map(record => JSON.stringify(record)).join("\n") + "\n";
    return { campaign: campaign.id, path: "observations.jsonl", content, sha256: sha(content), oid: sha(content).slice(0, 40) };
  });
}

function allocateRuns(slots, totalRuns) {
  const weight = slots.reduce((sum, slot) => sum + slot.weight, 0);
  if (!weight) throw new Error("Synthetic activity schedule has no eligible run slots.");
  for (const slot of slots) slot.count = Math.floor(totalRuns * slot.weight / weight);
  const remainder = totalRuns - slots.reduce((sum, slot) => sum + slot.count, 0);
  const ranked = slots.toSorted((left, right) =>
    totalRuns * right.weight % weight - totalRuns * left.weight % weight);
  for (let index = 0; index < remainder; index++) ranked[index].count++;
}

function activitySchedule(now, totalRuns, repositories) {
  if (!Number.isSafeInteger(totalRuns) || totalRuns < 0) throw new TypeError("Synthetic run count must be a non-negative safe integer.");
  if (!Number.isSafeInteger(repositories) || repositories < 1 || repositories > enterpriseDemo.repositories) {
    throw new TypeError("Synthetic repository count must be within the demo fleet.");
  }
  const fleet = [];
  for (let coordinate = 0; coordinate < repositories; coordinate++) {
    const repository = enterpriseRepository(coordinate);
    const department = ["commerce", "platform", "analytics", "workplace"][Math.floor(coordinate / 3) % 4];
    const service = ["api", "web", "infrastructure"][coordinate % 3];
    const profile = repositoryProfile(repository, department, service);
    const installedWorkflows = workflowsFor(repository);
    const behaviors = workflows.map(workflow => workflowProfile(repository, workflow, profile));
    const slots = [];
    for (const [index, workflow] of workflows.entries()) {
      const behavior = behaviors[index];
      for (let day = 0; day < runHistoryDays; day++) {
        const sample = distributionFor(`${repository}:${workflow.id}:${day}:cadence`);
        const date = Math.floor(now / dayMs) * dayMs - day * dayMs;
        const weekday = new Date(date).getUTCDay();
        const weekdayWeight = weekday === 0 || weekday === 6 ? 0.3 : 1.2;
        const partialDay = day === 0 ? (now - date) / dayMs : 1;
        const rate = behavior.activity * Math.min(3, sample.lognormal(1, 0.5)) * weekdayWeight
          * (0.85 + 0.5 * (runHistoryDays - 1 - day) / (runHistoryDays - 1)) * partialDay;
        slots.push({ index, day, weight: sample.poisson(rate) + rate * 0.1, count: 0, firstRunId: 0 });
      }
    }
    fleet.push({ coordinate, repository, workflows: installedWorkflows, behaviors, slots, weight: profile.activity, count: 0 });
  }
  // Reserve fleet coverage when the budget permits, then preserve log-normal activity shares.
  const minimum = totalRuns >= repositories ? 1 : 0;
  allocateRuns(fleet, totalRuns - minimum * repositories);
  let firstRunId = 1;
  for (const member of fleet) {
    const coverage = minimum && member.coordinate < enterpriseBlockedDomains.length
      ? member.slots.find(slot => slot.day === 3 && slot.index === member.coordinate % workflows.length) : null;
    allocateRuns(member.slots, member.count + minimum - Number(Boolean(coverage)));
    if (coverage) {
      coverage.count++;
      coverage.blockedDomain = enterpriseBlockedDomains[member.coordinate];
    }
    for (const slot of member.slots) {
      slot.firstRunId = firstRunId;
      firstRunId += slot.count;
    }
    shuffle(member.slots, `${member.repository}:ingestion-order`);
  }
  return shuffle(fleet, "fleet-ingestion-order");
}

function shuffle(values, key) {
  const sample = distributionFor(key);
  for (let index = values.length - 1; index > 0; index--) {
    const selected = Math.floor(sample.random() * (index + 1));
    [values[index], values[selected]] = [values[selected], values[index]];
  }
  return values;
}

export function* enterpriseActivityBatches(now, {
  start = 0, repositories = enterpriseDemo.activeRepositories, count = repositories - start, runs = enterpriseDemo.runs,
} = {}) {
  const fleet = activitySchedule(now, runs, repositories)
    .filter(member => member.coordinate >= start && member.coordinate < start + count)
    .map(member => ({
      ...member, slot: 0, ordinal: 0,
      remaining: member.slots.reduce((sum, slot) => sum + slot.count, 0),
    }));
  const total = fleet.reduce((sum, member) => sum + member.remaining, 0);
  const coverage = fleet.filter(member => member.remaining > 0);
  debugSample({ event: "activity-plan", repositories: fleet.length, covered: coverage.length, runs: total });
  const coverageWindow = Math.min(total, coverage.length * 3);
  const reserved = new Set(shuffle(Array.from({ length: coverageWindow }, (_, index) => index),
    "fleet-ingestion-coverage").slice(0, coverage.length));
  for (const member of coverage) member.remaining--;
  let available = total - coverage.length;
  let covered = 0;
  const sample = distributionFor("fleet-ingestion-sampling");
  let records = [];
  let runCount = 0;
  for (let emitted = 0; emitted < total; emitted++) {
    let member;
    if (reserved.has(emitted)) {
      member = coverage[covered++];
    } else {
      let draw = sample.random() * available;
      member = fleet.find(candidate => (draw -= candidate.remaining) < 0);
      if (!member) {
        debugSample({ event: "sampler-exhausted", emitted, runs: total, repositories: fleet.length });
        throw new Error("Synthetic activity sampler exhausted its run allocation.");
      }
      member.remaining--;
      available--;
    }
    while (member.slot < member.slots.length && member.ordinal >= member.slots[member.slot].count) {
      member.slot++;
      member.ordinal = 0;
    }
    const slot = member.slots[member.slot];
    const ordinal = member.ordinal++;
    const run = runSample(member.repository, member.workflows[slot.index], slot.index,
      slot.firstRunId + ordinal, slot.day, ordinal, now, member.behaviors[slot.index],
      ordinal === 0 ? slot.blockedDomain : undefined);
    records.push({ schema_version: 2, kind: "run", run },
      ...run.safe_outputs.map(output => ({ schema_version: 2, kind: "safe_output_item",
        safe_output: { ...output, run_id: run.run_id } })));
    if (++runCount === 100) {
      yield { records, runs: runCount };
      records = [];
      runCount = 0;
    }
  }
  if (records.length) yield { records, runs: runCount };
  records = [];
  for (const member of fleet) {
    for (const [index, workflow] of member.workflows.entries()) {
      const workflowRuns = member.slots.reduce((sum, slot) => sum + (slot.index === index ? slot.count : 0), 0);
      const value = valueSample(member.repository, workflow, now, {
        ...member.behaviors[index], activity: workflowRuns / runHistoryDays,
      });
      if (records.length + value.length > 100) {
        yield { records, runs: 0 };
        records = [];
      }
      records.push(...value);
    }
  }
  if (records.length) yield { records, runs: 0 };
}

export function enterpriseSample(now, {
  start = 0, repositories = enterpriseDemo.activeRepositories, count = repositories - start,
  includeHistory = true, runs = enterpriseDemo.runs,
} = {}) {
  const generatedAt = new Date(now).toISOString();
  const sources = {};
  const records = [];
  let runCount = 0;
  let repositoryCount = 0;
  for (let coordinate = start; coordinate < start + count; coordinate++) {
    const repository = enterpriseRepository(coordinate);
    const inventory = inventoryFor(repository, coordinate, generatedAt);
    repositoryCount++;
    for (const [name, source] of Object.entries(inventory)) {
      sources[name] ??= { ...source, rows: [], metadata: {
        ...source.metadata, "source-kind": "synthetic", "collection-operation": "synthetic-enterprise-demo",
        "collection-reason": "Generated sample enterprise; not observed GitHub activity.",
      } };
      // Catalog and campaign identity is enterprise-wide, not per repository.
      if (["campaigns", "marketplace-packages", "marketplace-registries", "configuration-policy"].includes(name) && repositoryCount > 1) continue;
      sources[name].rows.push(...source.rows);
    }
  }
  if (includeHistory) {
    for (const batch of enterpriseActivityBatches(now, { start, count, runs, repositories })) {
      records.push(...batch.records);
      runCount += batch.runs;
    }
  }
  debugSample({ event: "sample-generated", repositories: repositoryCount, runs: runCount, records: records.length, includeHistory });
  return { generatedAt, sources, records, repositories: repositoryCount, runs: runCount, memory: enterpriseMemory(now) };
}
