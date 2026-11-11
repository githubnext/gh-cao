import { createHash } from "node:crypto";
import { buildInventoryDashboardSources } from "../activity/inventory-sources.mjs";
import { parsePackageManifest } from "../activity/marketplace.mjs";
import { parseUnifiedSessionDocument } from "../dashboard/site/src/data/adapters/unified-session.js";
import { operationalValueEvidenceId } from "../dashboard/site/src/data/model/operational-value-evidence.js";
import { VALUE_COLLECTION_REASONS, workflowValueCollectionId } from "../dashboard/site/src/data/model/workflow-value-collection.js";

const owner = "sample-enterprise";
const catalog = `${owner}/automation-catalog`;
const dayMs = 86_400_000;
const sha = value => createHash("sha256").update(value).digest("hex");
const revision = sha("synthetic-enterprise-catalog-v2").slice(0, 40);
const previousRevision = sha("synthetic-enterprise-catalog-v1").slice(0, 40);
const workflows = [
  { id: "dependency-review", name: "Dependency review", campaign: "dependency-health", role: "orchestrator", goal: "Keep dependencies supported and patched", metric: "Dependency repairs" },
  { id: "repository-assistant", name: "Repository assistant", campaign: "dependency-health", role: "worker", goal: "Resolve actionable repository maintenance", metric: "Maintenance repairs" },
  { id: "security-review", name: "Security review", campaign: "secure-delivery", role: "orchestrator", goal: "Reduce verified security findings", metric: "Security repairs" },
  { id: "documentation", name: "Documentation maintenance", campaign: "secure-delivery", role: "worker", goal: "Keep service runbooks current", metric: "Runbook updates" },
];
const campaigns = [
  { id: "dependency-health", name: "Dependency health", description: "Synthetic coordinated dependency upgrades across the enterprise.", icon: "package" },
  { id: "secure-delivery", name: "Secure delivery", description: "Synthetic security reviews and verified operational runbooks.", icon: "shield-check" },
];

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
  const bundles = campaigns.map(campaign => {
    const members = workflows.filter(workflow => workflow.campaign === campaign.id
      && !(standaloneDocumentation && workflow.id === "documentation"));
    return {
      ...campaign, workflow: `.github/workflows/${members[0].id}.md`,
      source: `${catalog}/${campaign.id}@${revision}`, version: index % 3 ? revision : previousRevision,
      minVersion: "1.2.2", compiled: true, maxAiCredits: 25, missingWorkers: [],
      readmePath: `${campaign.id}/README.md`, readme: `# ${campaign.name}\n\n${campaign.description}\n\nGenerated demonstration only; no live execution.`,
      workers: members.slice(1).map(workflow => ({
        id: workflow.id, sourcePath: `.github/workflows/${workflow.id}.md`, compiled: true, maxAiCredits: 50,
      })),
    };
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
      workflows: workflows.map(workflow => ({
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

function runSample(repository, workflow, workflowIndex, runId, day, now) {
  const startedAt = new Date(now - day * dayMs - (workflowIndex + 1) * 3_600_000).toISOString();
  const durationSeconds = 45 + runId % 240;
  const completedAt = new Date(Date.parse(startedAt) + durationSeconds * 1000).toISOString();
  const pending = day === 0 && workflowIndex === 0 && runId % 112 < 56;
  const approvalRequired = day === 0 && workflowIndex === 2;
  const failed = runId % 11 === 0 && !pending && !approvalRequired;
  const candidate = day % 2 === 0;
  const model = ["gpt-5.3-codex", "claude-sonnet-5.5", "claude-haiku-5.5"][runId % 3];
  const aic = (0.5 + runId % 30 / 4) * (candidate ? 0.8 : 1);
  const tokens = { total_input_tokens: 8000 + runId % 4000, total_output_tokens: 500 + runId % 1500,
    total_cache_read_tokens: 2000 + runId % 2000, total_cache_write_tokens: 1000,
    total_reasoning_tokens: 250 + runId % 300, total_aic: aic };
  const pullRequest = runId % 2 === 0;
  const closed = day > 2;
  const toolName = ["get_file_contents", "list_issues", "search_code", "get_pull_request"][workflowIndex];
  const run = {
    run_id: runId, run_attempt: "1", organization: owner, repository,
    workflow_name: workflow.name, workflow_path: `.github/workflows/${workflow.id}.md`,
    display_title: workflow.role === "worker"
      ? `Synthetic ${workflow.name} · ${repository} · ${runId % 3 ? "live" : "review"}`
      : `Synthetic ${workflow.name}: ${workflow.goal}`,
    event: workflow.role === "worker" ? "workflow_dispatch" : "schedule", status: pending ? "in_progress" : "completed",
    conclusion: pending ? null : approvalRequired ? "action_required" : failed ? "failure" : "success",
    classification: pending ? "pending" : approvalRequired ? "action_required" : failed ? "failure" : "success",
    created_at: startedAt, started_at: startedAt, updated_at: completedAt,
    branch: "main", head_sha: sha(`${repository}:${runId}`).slice(0, 40),
    url: new URL(`/${repository}/actions/runs/${runId}`, "https://github.com").href,
    rollout_mode: runId % 3 ? "live" : "review", duration: `${durationSeconds}s`, action_minutes: durationSeconds / 60,
    engine: "Copilot", engine_id: "copilot", agent_id: "copilot", agent_version: "1.0.106",
    engine_version: "1.0.106", gh_aw_version: "1.2.2", agent_runtime: "actions",
    model, model_id: model, requested_model: "auto", resolved_model: model,
    firewall_version: "0.9.0", gateway_version: "0.8.0",
    token_usage_summary: tokens, github_api_calls: 8 + runId % 12, safe_items_count: 1,
    error_count: failed ? 1 : 0,
    ...(failed ? { failure_kind: "tool_error", failure_job: "agent", failure_step: "Review dependencies",
      failure_message: "Synthetic package lookup exceeded its request budget." } : {}),
    mcp_tool_usage: { tool_calls: [
      { tool_call_id: `sample-read-${runId}`, server_name: "github", tool_name: toolName,
        timestamp: startedAt, status: failed ? "failure" : "success", input_size: 100 + runId % 200, output_size: 900 + runId % 500 },
      { tool_call_id: `sample-shell-${runId}`, server_name: "bash", tool_name: "bash",
        timestamp: completedAt, status: "success", input_size: 40, output_size: 200 },
    ] },
    skill_activations: [{ name: workflow.id, source: "repository", timestamp: startedAt,
      invocation_count: 2 + runId % 4, failed_count: failed ? 1 : 0, status: failed ? "failed" : "invoked" }],
    firewall_analysis: { requests_by_domain: {
      "api.github.com": { allowed: 15 + runId % 20, blocked: 0 },
      "registry.npmjs.org": { allowed: 5 + runId % 5, blocked: 0 },
      ...(runId % 7 === 0 ? { "unapproved.example.invalid": { allowed: 0, blocked: 1 + runId % 3 } } : {}),
    } },
    safe_outputs: [{
      type: pullRequest ? "create_pull_request" : "create_issue", repo: repository, number: runId,
      url: `https://github.com/${repository}/${pullRequest ? "pull" : "issues"}/${runId}`, timestamp: completedAt,
      github_issue_status: { state: closed ? "closed" : "open", closed, state_reason: closed ? "completed" : null,
        closed_at: closed ? new Date(Date.parse(completedAt) + 3600_000).toISOString() : null, observed_at: new Date(now).toISOString() },
    }],
    audit: {
      key_findings: failed ? [{ title: "Synthetic package access needs review", severity: "warning", timestamp: completedAt }] : [],
      recommendations: runId % 5 === 0 ? [{ action: "Reduce repeated metadata requests with one bounded batch", priority: "medium" }] : [],
      noops: runId % 13 === 0 ? [{ message: "Dependencies already meet the synthetic baseline", status: "success" }] : [],
    },
    friction: {
      measurement_state: "measured", canonical_unit: "aic", sources: ["agent"], total_events: 2,
      total_occurrences: 2, counted_occurrences: 2, suppressed_occurrences: 0, linked_invocations: 2,
      unattributed_occurrences: 0, cost: { aic: aic * 0.08, tokens: { input: 400, output: 100, total: 500 },
        turns: 1, tool_calls: 2, latency_ms: 750 }, total_run_aic: aic, friction_ratio: 0.08,
    },
    experiments: { assignments: { "review-strategy": candidate ? "focused" : "baseline" } },
    graders: { results: [{
      id: "change-quality", name: "Change quality", source: "workflow", value: failed ? 0.65 : 0.85 + runId % 10 / 100,
      status: failed ? "fail" : "pass", passed: !failed, unit: "ratio", direction: "higher_is_better",
      threshold: 0.8, timestamp: completedAt, included: true, evaluatorDigest: sha("synthetic-quality-v1"),
      experimentId: "review-strategy", variant: candidate ? "focused" : "baseline",
    }] },
    evals: [{ id: "behavior-preserved", answer: failed ? "NO" : "YES", runId: String(runId),
      timestamp: completedAt, experimentId: "review-strategy", variant: candidate ? "focused" : "baseline" }],
  };
  if (pending || approvalRequired) {
    run.safe_outputs = [];
    run.safe_items_count = 0;
    run.graders = { results: [] };
    run.evals = [];
  }
  // Routing samples use gh-aw's actual unified-session parser, never canonical-row fixtures.
  const events = [
    { type: "session.format", data: { version: 1 } },
    { type: "workflow.info", data: { runId: String(runId), engineId: "copilot", model } },
    { type: "model.config", data: { routing: { enabled: true } } },
    { type: "firewall.model_routing", data: { stage: "selection", selectedModel: model, selectedEffort: "high",
      eligibleChoices: 3, catalogueOverlap: 3, mode: "balanced",
      labels: { task_type: "coding", scope: "repository", task_complexity: workflowIndex % 2 ? "medium" : "high" } } },
    { type: "firewall.model_routing", data: { stage: "request", requestId: `request-${runId}`,
      requestedModel: model, selectedModel: model, routed: failed ? "deviated" : "as_selected", outcome: failed ? "failed" : "success" } },
    { type: "firewall.token_usage", data: { requestId: `request-${runId}`, model, aic,
      usage: { inputTokens: tokens.total_input_tokens, outputTokens: tokens.total_output_tokens } } },
    { type: "mcp.tool_call", data: { serverName: "github", toolName, toolCallId: `sample-read-${runId}`,
      status: failed ? "error" : "success", inputSize: 160, outputSize: 1200, durationMs: 125 + runId % 100 } },
    { type: "skill.invoked", data: { name: workflow.id, source: "repository" } },
    { type: "firewall.http_access", data: { host: "api.github.com", method: "GET", status: 200, bytes: 1200, durationMs: 40 } },
    { type: "workflow.job_result", data: { jobId: "agent", result: pending ? "in_progress" : run.conclusion } },
    { type: "session.collection", data: { sources: [{ component: "firewall", phase: "agent", path: "firewall/token-usage.jsonl", events: 1 }] } },
  ].map(event => ({ ...event, timestamp: completedAt, provenance: { phase: event.type === "session.collection" ? "conclusion" : "agent" } }));
  const session = parseUnifiedSessionDocument(events.map(event => JSON.stringify(event)).join("\n"),
    "usage/aw_session.jsonl", { runId: String(runId), attempt: 1 });
  // Keep the summary's independently supplied outputs/graders and use parsed routing
  // events on half the runs; the remainder demonstrate summary-only ingestion.
  if (runId % 2 === 0) run.cao_unified_session = { version: 1, path: "usage/aw_session.jsonl", events: session.events };
  return run;
}

function valueSample(repository, workflow, now, index) {
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
  for (let day = 12; day >= 2; day -= 2) {
    const output = `proposal-${day}`;
    const implementation = `repair-${day}`;
    facts.push(
      { "fact-kind": "output", "fact-id": output, label: "Maintenance proposal", title: `${workflow.metric}: batch ${day}`,
        at: instant(day), state: "closed", origin: "corroborated", url: `https://example.invalid/${repository}/issues/${day}` },
      { "fact-kind": "implementation", "fact-id": implementation, label: `${workflow.metric} accepted`, at: instant(day - 1),
        "accepted-at": instant(day - 1), value: 1 + index % 3, ref: sha(`${repository}:${workflow.id}:${day}`),
        origin: "corroborated", url: `https://example.invalid/${repository}/pull/${day}` },
      { "fact-kind": "output-implementation", "fact-id": implementation, "parent-id": output, "related-id": implementation },
      { "fact-kind": "implementation-lineage", "fact-id": output, "parent-id": implementation, "related-id": output, label: "connected" },
      { "fact-kind": "snapshot", "fact-id": `backlog-${day}`, at: instant(day), value: 5 + day + index % 5,
        ref: sha(`backlog-${day}`), "items-available": true },
    );
  }
  const packet = {
    schemaVersion: 1, id: operationalValueEvidenceId(repository, workflow.id, "accepted-repairs", "v1"),
    repository, workflow: workflow.id, sourcePath: `.github/workflows/${workflow.id}.md`,
    observedAt: instant(0), windowStart: instant(14), windowEnd: instant(0), contractDigest: sha(JSON.stringify(header)), facts,
  };
  const deferred = index % 3 === 0;
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
    ...Array.from({ length: 14 }, (_, day) => ({
      schema_version: 2, kind: "operational_value", operational_value: {
        repository, campaign: workflow.campaign, value_id: `${workflow.id}-backlog`,
        timestamp: new Date(Math.floor(now / dayMs) * dayMs - day * dayMs).toISOString(),
        value: 8 + day + index, metric_name: "Actionable backlog", metric_unit: "items", metric_direction: "decrease",
        workflow_slug: workflow.id, workflow_path: packet.sourcePath, workflow_name: workflow.name,
        cadence_days: 1, evaluator_digest: sha("synthetic-backlog-v1"), maturity_status: "matured", evaluation_mode: "observed",
      },
    })),
  ];
}

export function enterpriseSample(now) {
  const generatedAt = new Date(now).toISOString();
  const sources = {};
  const records = [];
  let runId = 0;
  let repositoryCount = 0;
  for (const department of ["commerce", "platform", "analytics", "workplace"]) {
    for (const service of ["api", "web", "infrastructure"]) {
      const repository = `${owner}/${department}-${service}`;
      const inventory = inventoryFor(repository, repositoryCount++, generatedAt);
      for (const [name, source] of Object.entries(inventory)) {
        sources[name] ??= { ...source, rows: [], metadata: {
          ...source.metadata, "source-kind": "synthetic", "collection-operation": "synthetic-enterprise-demo",
          "collection-reason": "Generated sample enterprise; not observed GitHub activity.",
        } };
        // Catalog and campaign identity is enterprise-wide, not per repository.
        if (["campaigns", "marketplace-packages", "marketplace-registries", "configuration-policy"].includes(name) && repositoryCount > 1) continue;
        sources[name].rows.push(...source.rows);
      }
      for (const [index, workflow] of workflows.entries()) {
        for (let day = 0; day < 14; day++) {
          records.push({ schema_version: 2, kind: "run", run: runSample(repository, workflow, index, ++runId, day, now) });
        }
        records.push(...valueSample(repository, workflow, now, repositoryCount));
      }
    }
  }
  const memory = campaigns.map(campaign => {
    const content = [
      { repository: `${owner}/commerce-api`, observation: "Synthetic dependency upgrade accepted", nextReview: generatedAt, status: "verified" },
      { repository: `${owner}/platform-web`, observation: "Synthetic review retained for approval", nextReview: generatedAt, status: "review" },
    ].map(record => JSON.stringify(record)).join("\n") + "\n";
    return { campaign: campaign.id, path: "observations.jsonl", content, sha256: sha(content), oid: sha(content).slice(0, 40) };
  });
  return { generatedAt, sources, records, repositories: repositoryCount, runs: runId, memory };
}
