// Shapes calibrated against bounded gh-cao usage artifacts; all values are generated.
export function enterpriseSessionEvents(run, sample, complexity) {
  const start = Date.parse(run.started_at);
  const end = Date.parse(run.updated_at);
  const elapsed = end - start;
  const pending = run.status !== "completed";
  const approval = run.conclusion === "action_required";
  const cancelled = run.conclusion === "cancelled";
  const failed = ["failure", "timed_out"].includes(run.conclusion);
  const interrupted = pending || cancelled || run.conclusion === "timed_out";
  const identity = { runId: String(run.run_id), runAttempt: Number(run.run_attempt) };
  const sessionId = `synthetic-session-${run.run_id}`;
  const routed = sample.random() < 0.7;
  const events = [];
  const emit = (type, data, position, phase = "agent", component = "agent") => {
    events.push({
      type, data: { ...identity, ...data },
      ...(position === null ? {} : { timestamp: new Date(start + Math.round(elapsed * position)).toISOString() }),
      provenance: { phase, component },
    });
  };
  emit("session.format", { version: 1 }, null, "conclusion", "collector");
  emit("workflow.info", {
    repository: run.repository, engineId: run.engine_id, agentVersion: run.agent_version,
    cliVersion: run.agent_version, awfVersion: run.firewall_version, mcpgVersion: run.gateway_version,
    requestedModel: routed ? "auto" : "medium", model: run.model, modelSelection: { effectiveModel: run.model },
    triggerType: run.event,
  }, 0);
  emit("workflow.job_result", { jobId: "activation", result: approval ? "action_required" : "success",
    durationMs: Math.round(elapsed * 0.08) }, 0.08, "activation", "workflow");
  if (!approval) {
    const agentEnd = interrupted ? 1 : 0.7;
    const turns = Math.min(20, Math.max(2, 2 + sample.poisson(4 * complexity)));
    emit("model.config", { routing: { enabled: routed } }, null, "agent", "workflow");
    if (!routed) emit("model.resolution", { engine: "copilot", role: "main", configured_model: "medium",
      resolved_model: run.model, provider: "copilot", status: "resolved" }, null, "agent", "workflow");
    emit("session.start", { sessionId, sourceEngine: "copilot", model: run.model, reasoningEffort: "high" }, 0.1);
    emit("user.message", {}, 0.1);
    emit("experiment.assignment", { assignments: run.experiments.assignments }, 0.1, "agent", "experiment");
    const calls = run.mcp_tool_usage.tool_calls;
    const servers = [...new Set(calls.filter(call => call.tool_name !== "bash").map(call => call.server_name))];
    emit("mcp.configuration", { servers: servers.map(serverName => ({
      serverName, transport: "stdio", required: true,
      tools: [...new Set(calls.filter(call => call.server_name === serverName).map(call => call.tool_name))],
    })) }, 0.1, "agent", "mcp");
    emit("skill.inventory", { skills: [run.workflow_path.split("/").at(-1).replace(".md", ""), "test-validation"]
      .map(name => ({ name, source: "repository", scope: "project" })) }, null, "agent", "skills");
    if (routed) emit("firewall.model_routing", { stage: "selection", selectedModel: run.model, selectedEffort: "high",
      eligibleChoices: 3, catalogueOverlap: 3, mode: "balanced",
      labels: { task_type: "coding", scope: "repository", task_complexity: complexity > 1.5 ? "high" : "medium" },
    }, 0.11, "agent", "firewall");

    const fields = {
      inputTokens: "total_input_tokens", outputTokens: "total_output_tokens",
      cacheReadInputTokens: "total_cache_read_tokens", cacheCreationInputTokens: "total_cache_write_tokens",
      reasoningOutputTokens: "total_reasoning_tokens",
    };
    const totals = run.token_usage_summary;
    const detectionShare = interrupted ? 0 : 0.12;
    const detection = Object.fromEntries(Object.entries(fields)
      .map(([field, total]) => [field, Math.floor(totals[total] * detectionShare)]));
    const agent = Object.fromEntries(Object.entries(fields)
      .map(([field, total]) => [field, totals[total] - detection[field]]));
    const detectionAic = Math.round(totals.total_aic * detectionShare * 10_000);
    const agentAic = Math.round(totals.total_aic * 10_000) - detectionAic;
    // Prompt tokens concentrate in the first request; cached context grows over later turns.
    const split = (total, index, weights) => {
      const sum = weights.reduce((sum, weight) => sum + weight, 0);
      const before = weights.slice(0, index).reduce((sum, weight) => sum + weight, 0);
      return (index === weights.length - 1 ? total : Math.floor(total * (before + weights[index]) / sum))
        - Math.floor(total * before / sum);
    };
    const promptWeights = Array.from({ length: turns }, (_, index) => index ? 1 : turns * 20);
    const cacheWeights = Array.from({ length: turns }, (_, index) => index);
    const outputWeights = Array.from({ length: turns }, () => sample.lognormal(1, 0.5));
    const measured = sample.random() < 0.25;
    let cumulativeAic = 0;
    for (let turn = 0; turn < turns; turn++) {
      const position = 0.12 + (agentEnd - 0.14) * (turn + 1) / turns;
      const requestId = `synthetic-request-${run.run_id}-${turn}`;
      const usage = Object.fromEntries(Object.entries(agent).map(([field, total]) => [field,
        split(total, turn, field === "inputTokens" ? promptWeights
          : field === "cacheReadInputTokens" ? cacheWeights : outputWeights)]));
      const requestAic = split(agentAic, turn, outputWeights);
      cumulativeAic += requestAic;
      emit("assistant.reasoning", {}, position - 0.005);
      emit("assistant.message", {}, position);
      if (routed) emit("firewall.model_routing", { stage: "request", requestId, requestedModel: run.model,
        selectedModel: run.model, selectedEffort: "high", appliedEffort: "high", purpose: "main",
        routed: "as_selected", outcome: "success" }, position, "agent", "firewall");
      const data = { requestId, provider: "copilot", model: run.model, purpose: "main", usage,
        aic: requestAic / 10_000, totalAic: cumulativeAic / 10_000 };
      emit("firewall.token_usage", data, position, "agent", "firewall");
      if (measured) emit("usage.report", data, position, "agent", "usage");
    }
    calls.forEach((call, index) => {
      const position = 0.14 + (agentEnd - 0.18) * index / Math.max(1, calls.length);
      const duration = Math.min(elapsed * 0.01, sample.lognormal(600 * complexity, 0.8));
      const tool = {
        sessionId, toolCallId: call.tool_call_id, toolName: call.tool_name,
        ...(call.tool_name === "bash" ? {} : { mcpServerName: call.server_name }),
      };
      if (call.tool_name === "bash") {
        emit("tool.execution_start", tool, position);
        emit("tool.execution_complete", { ...tool, success: call.status === "success", durationMs: Math.round(duration) },
          position + (elapsed ? duration / elapsed : 0));
      } else {
        emit("mcp.tool_call", { serverName: call.server_name, toolName: call.tool_name, toolCallId: call.tool_call_id,
          status: call.status === "success" ? "success" : "error",
          inputSize: call.input_size, outputSize: call.output_size, durationMs: Math.round(duration) },
        position + (elapsed ? duration / elapsed : 0), "agent", "mcp");
      }
    });
    for (const skill of run.skill_activations) {
      for (let index = 0; index < skill.invocation_count; index++) {
        emit("skill.invoked", { name: skill.name, source: skill.source, success: !(skill.failed_count && index === 0) },
          0.12 + 0.01 * index, "agent", "skills");
      }
    }
    for (const [host, counts] of Object.entries(run.firewall_analysis.requests_by_domain)) {
      for (const [decision, count] of Object.entries(counts)) {
        for (let index = 0; index < count; index++) {
          emit("firewall.http_access", { host, method: "GET", status: decision === "blocked" ? 403 : 200,
            decision: decision === "blocked" ? "denied" : "allowed",
            bytes: decision === "blocked" ? 0 : Math.round(sample.lognormal(1200, 0.8)),
            durationMs: Math.round(sample.lognormal(80, 0.5)) },
          0.13 + (agentEnd - 0.15) * (index + 1) / (count + 1), "agent", "firewall");
        }
      }
    }
    emit("github_api.rate_limit", { resource: "core", operation: "metadata", source: "github",
      limit: 5000, used: run.github_api_calls, remaining: 5000 - run.github_api_calls, status: "observed" },
    agentEnd - 0.01, "workflow", "github");
    if (failed) emit("session.error", { code: run.failure_kind, model: run.model }, agentEnd);
    if (!pending) {
      if (!interrupted) {
        emit("session.result", { sessionId, sourceEngine: "copilot", status: failed ? "error" : "success",
          numTurns: turns, durationMs: Math.round(elapsed * (agentEnd - 0.1)), usage: agent }, agentEnd);
      }
      emit("session.shutdown", { shutdownType: cancelled ? "cancelled" : failed ? "error" : "completed",
        currentModel: run.model }, agentEnd);
      emit("execution.result", { outcome: run.conclusion, exitCode: failed ? 1 : cancelled ? 130 : 0,
        durationMs: Math.round(elapsed * (agentEnd - 0.1)) }, agentEnd, "agent", "execution");
      emit("workflow.job_result", { jobId: "agent", result: run.conclusion,
        durationMs: Math.round(elapsed * (agentEnd - 0.1)) }, agentEnd, "agent", "workflow");
    }
    if (!interrupted) {
      emit("firewall.token_usage", { requestId: `synthetic-detection-${run.run_id}`, provider: "copilot",
        model: "claude-haiku-5.5", purpose: "detection",
        usage: detection, aic: detectionAic / 10_000, totalAic: detectionAic / 10_000 },
      0.9, "detection", "firewall");
      emit("detection.result", { jobResult: "success", conclusion: "success", promptInjection: false,
        secretLeak: false, maliciousPatch: false }, 0.92, "detection", "detection");
      emit("workflow.job_result", { jobId: "detection", result: "success",
        durationMs: Math.round(elapsed * 0.22) }, 0.92, "detection", "workflow");
      emit("grader.manifest", { graders: run.graders.results.map(({ id, name, unit, direction, threshold }) =>
        ({ id, name, unit, direction, threshold })) }, null, "conclusion", "graders");
      emit("grader.result", { results: run.graders.results }, 0.93, "conclusion", "graders");
      for (const result of run.evals) emit("eval.result", result, 0.93, "conclusion", "evals");
      for (const output of run.safe_outputs) {
        emit("safe_output.request", { type: output.type, repo: output.repo }, 0.95, "safe_outputs", "safe-outputs");
        emit("safe_output.result", { type: output.type, repo: output.repo, number: output.number, url: output.url },
          1, "safe_outputs", "safe-outputs");
      }
      emit("workflow.job_result", { jobId: "safe_outputs", result: "success",
        durationMs: Math.round(elapsed * 0.08) }, 1, "safe_outputs", "workflow");
    }
  }
  if (!pending) {
    const sources = new Map();
    for (const event of events) {
      const { phase, component } = event.provenance;
      const key = `${phase}:${component}`;
      if (!sources.has(key)) sources.set(key, { phase, component, events: 0 });
      sources.get(key).events++;
    }
    emit("session.collection", { sources: [...sources.values()], warnings: 0,
      untimedEvents: events.filter(event => !event.timestamp).length }, 1, "conclusion", "collector");
  }
  // Untimed metadata keeps source order; timed observations use the recorded clock.
  return events.toSorted((left, right) =>
    Date.parse(left.timestamp ?? run.started_at) - Date.parse(right.timestamp ?? run.started_at));
}
