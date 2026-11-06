package collectnative

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

var metadataFields = map[string][]string{
	"agent.execution": {}, "session.init": {"sourceEngine", "model", "reasoningEffort"},
	"session.start": {"sourceEngine", "model", "reasoningEffort"}, "session.info": {"model", "status", "sourceType", "kind"},
	"session.shutdown": {"shutdownType", "currentModel"}, "session.task_complete": {},
	"session.result": {"sourceEngine", "status", "sourceType"}, "prompt.system": {}, "prompt.user": {},
	"assistant.message_update": {"model", "sourceType"}, "tool.execution_update": {"toolName", "status", "taskType", "taskId"},
	"mcp.rpc.request": {"serverName", "method", "direction", "toolName"}, "mcp.rpc.response": {"serverName", "method", "direction", "toolName"},
	"mcp.event": {"serverName", "event", "level", "status"}, "firewall.event": {"event", "level", "status"},
	"safe_output.request": {"type", "repo", "provider", "status", "errorCode"},
	"safe_output.result":  {"type", "repo", "provider", "status", "errorCode"},
	"safe_output.error":   {"type", "repo", "provider", "status", "errorCode"},
	"execution.result":    {"outcome", "conclusion"}, "detection.result": {"jobResult", "conclusion"},
	"guardrail.daily_aic": {"status"}, "plugin.installation": {"plugin", "repository", "ref", "status"},
	"github_api.rate_limit":       {"source", "credentialSource", "operation", "resource", "status"},
	"skill.context_delivered_ref": {"interactionId", "source", "contentId"},
}

var tokenFields = map[string]string{
	"inputTokens": "inputTokens", "outputTokens": "outputTokens", "totalTokens": "totalTokens",
	"cacheReadInputTokens": "cacheReadTokens", "cacheCreationInputTokens": "cacheWriteTokens", "reasoningOutputTokens": "reasoningTokens",
}

func usage(value any) map[string]any {
	result := map[string]any{}
	for native, canonical := range tokenFields {
		if value := nonnegative(integer(object(value)[native])); value != nil {
			result[canonical] = value
		}
	}
	return result
}

func metadata(kind string, data map[string]any) []map[string]any {
	entries := metadataEntries(kind, data)
	for _, entry := range entries {
		fields := object(entry["fields"])
		for _, name := range []string{"exitCode", "httpStatus", "requestBytes", "responseBytes", "errorsCount",
			"permissionDenialsCount", "rateLimit", "remaining", "used", "requestAttempt", "turns", "toolCalls", "totalEvents"} {
			if value, present := fields[name]; present {
				if _, ok := value.(json.Number); ok && integer(value) == nil {
					delete(fields, name)
				}
			}
		}
	}
	return entries
}

func metadataEntries(kind string, data map[string]any) []map[string]any {
	if entries, handled := routingMetadata(kind, data); handled {
		return entries
	}
	entry := func(fields map[string]any) []map[string]any {
		return []map[string]any{{"fields": clean(fields)}}
	}
	switch kind {
	case "mcp.configuration":
		result := []map[string]any{}
		for i, candidate := range array(data["servers"]) {
			server := object(candidate)
			if _, ok := server["serverName"].(string); !ok {
				continue
			}
			fields := map[string]any{"name": server["serverName"], "transport": server["transport"], "status": "configured"}
			if required, ok := server["required"].(bool); ok {
				fields["required"] = required
			}
			result = append(result, map[string]any{"key": fmt.Sprintf("server:%d", i), "fields": clean(fields)})
			index := 0
			for _, tool := range array(server["tools"]) {
				if _, ok := tool.(string); ok {
					result = append(result, map[string]any{"key": fmt.Sprintf("server:%d:tool:%d", i, index),
						"fields": map[string]any{"name": server["serverName"], "summary": tool, "status": "configured-tool"}})
					index++
				}
			}
		}
		return result
	case "skill.inventory":
		result := []map[string]any{}
		for i, candidate := range array(data["skills"]) {
			skill := object(candidate)
			result = append(result, map[string]any{"key": fmt.Sprint(i), "fields": clean(map[string]any{
				"name": nonempty(text(skill["name"])), "scope": nonempty(text(skill["scope"])), "digest": nonempty(text(skill["digest"])),
				"summary": skill["source"], "status": "available"})})
		}
		return result
	case "skill.conflict":
		return entry(map[string]any{"name": nonempty(text(data["name"])), "code": data["kind"], "status": "conflict"})
	}
	if strings.HasPrefix(kind, "dynamicWorkflows.") {
		switch kind {
		case "dynamicWorkflows.task_started", "dynamicWorkflows.task_progress", "dynamicWorkflows.task_updated",
			"dynamicWorkflows.task_notification", "dynamicWorkflows.background_tasks_changed":
		default:
			return nil
		}
		input := make([]any, 1, len(data)+1)
		input[0] = data
		input = append(input, array(data["tasks"])...)
		input = append(input, array(data["workflowProgress"])...)
		result := []map[string]any{}
		for i, candidate := range input {
			value := object(candidate)
			fields := usage(value["usage"])
			merge(fields, map[string]any{"model": nonempty(text(value["model"])), "status": nonempty(text(value["status"])),
				"name": first(value["workflowName"], value["taskType"]), "correlationId": first(value["taskId"], value["toolCallId"], value["agentId"]),
				"durationMs": nonnegative(object(value["usage"])["durationMs"]), "toolCalls": nonnegative(object(value["usage"])["toolUses"]),
				"requestAttempt": nonnegative(value["attempt"]), "decision": value["state"]})
			result = append(result, map[string]any{"key": fmt.Sprint(i), "fields": clean(fields)})
		}
		return result
	}
	names, ok := metadataFields[kind]
	if !ok {
		return nil
	}
	summary := []string{}
	for _, name := range names {
		if s := text(data[name]); s != "" {
			summary = append(summary, name+"="+s)
		}
	}
	fields := usage(data["usage"])
	for _, name := range []string{"provider", "model", "method", "direction", "resource", "status"} {
		fields[name] = nonempty(text(data[name]))
	}
	for _, name := range []string{"durationMs", "httpStatus", "premiumRequests", "totalCostUsd", "remaining", "used", "delayMs", "threshold", "aic", "totalAic"} {
		fields[name] = nonnegative(data[name])
	}
	merge(fields, map[string]any{
		"rateLimit": nonnegative(data["limit"]), "requestAttempt": nonnegative(data["attempt"]),
		"sourceEngine":  first(data["sourceEngine"], data["engineId"]),
		"model":         first(data["model"], data["wireModel"], data["selectedModel"], data["resolved_model"], data["currentModel"]),
		"effort":        first(data["appliedEffort"], data["effort"], data["selectedEffort"], data["reasoningEffort"]),
		"correlationId": first(data["requestId"], data["toolCallId"], data["interactionId"], nonempty(scalar(data["rpcId"]))),
		"requestBytes":  nonnegative(data["inputSize"]), "responseBytes": nonnegative(data["outputSize"]),
		"turns": nonnegative(data["numTurns"]), "exitCode": integer(data["exitCode"]),
		"summary": strings.Join(summary, ", "), "code": first(data["code"], data["errorCode"], data["failureCode"]),
		"name":   first(data["jobId"], data["plugin"], data["kind"]),
		"status": first(data["status"], data["result"], data["outcome"], data["conclusion"], data["shutdownType"]),
	})
	for native, target := range map[string]string{"errors": "errorsCount", "permissionDenials": "permissionDenialsCount"} {
		if values, ok := data[native].([]any); ok {
			fields[target] = len(values)
		}
	}
	if success, ok := data["success"].(bool); ok {
		fields["status"] = "error"
		if success {
			fields["status"] = "success"
		}
	}
	if _, present := data["usageSnapshot"]; present || kind == "session.result" {
		fields["evidenceState"] = "snapshot"
	}
	if kind == "guardrail.daily_aic" {
		fields["value"], fields["threshold"] = nonnegative(data["total"]), nonnegative(data["threshold"])
		if exceeded, ok := data["exceeded"].(bool); ok {
			fields["decision"] = "under-budget"
			if exceeded {
				fields["decision"] = "exceeded"
			}
		}
	}
	if kind == "github_api.rate_limit" {
		if reset, ok := number(data["reset"]); ok && reset > 0 && reset*1000 <= 8640000000000000 {
			fields["resetAt"] = eventClock(json.Number(fmt.Sprint(reset * 1000)))
		}
	}
	result := entry(fields)
	if kind == "detection.result" {
		result[0]["key"] = "0"
		for _, name := range []string{"promptInjection", "secretLeak", "maliciousPatch"} {
			if value, ok := data[name].(bool); ok {
				decision := "clear"
				if value {
					decision = "detected"
				}
				result = append(result, map[string]any{"key": fmt.Sprint(len(result)), "fields": map[string]any{"code": name, "decision": decision}})
			}
		}
	}
	if kind == "agent.execution" {
		result[0]["key"] = "0"
		for _, name := range []string{"categories", "errorCodes", "errorTypes"} {
			for _, value := range array(data[name]) {
				if _, ok := value.(string); ok || finite(value) {
					result = append(result, map[string]any{"key": fmt.Sprint(len(result)),
						"fields": map[string]any{"code": scalar(value), "name": name}})
				}
			}
		}
	}
	return result
}

type usageEntry struct {
	id     string
	values map[string]any
}

func sessionRun(records []sessionRecord, identity Identity, location string) map[string]any {
	run := map[string]any{}
	phases := map[string]map[int][]usageEntry{}
	outputs := []map[string]any{}
	for _, record := range records {
		data := record.data
		if !matches(data, identity) {
			continue
		}
		phase := text(record.provenance["phase"])
		if phase == "" {
			phase = "agent"
		}
		if record.kind == "safe_output.result" && text(data["type"]) != "" && data["error"] == nil && data["errorCode"] == nil {
			status := text(data["status"])
			if status != "error" && status != "failure" && status != "failed" && status != "skipped" {
				output := map[string]any{"sessionPayloadRef": fmt.Sprintf("%s#L%d", location, record.line)}
				for _, field := range []string{"type", "repo", "provider"} {
					if value := text(data[field]); value != "" {
						output[field] = value
					}
				}
				if value := integer(data["number"]); value != nil {
					if n, _ := number(value); n > 0 {
						output["number"] = value
					}
				}
				if address, err := url.Parse(text(data["url"])); err == nil && address.Scheme == "https" && address.Host != "" &&
					address.User == nil && address.RawQuery == "" && address.Fragment == "" {
					output["url"] = address.String()
				}
				outputs = append(outputs, output)
				run["safe_outputs"], run["safe_items_count"] = outputs, len(outputs)
			}
		}
		if record.kind == "workflow.info" {
			for native, target := range map[string]string{"engineId": "engine_id", "agentVersion": "agent_version",
				"cliVersion": "cli_version", "awfVersion": "firewall_version", "mcpgVersion": "gateway_version", "model": "model", "requestedModel": "requested_model"} {
				if value, ok := data[native].(string); ok {
					run[target] = value
				}
			}
			selection := object(data["modelSelection"])
			if value, ok := first(selection["effectiveModel"], selection["resolvedModel"], object(data["modelRouting"])["wireModel"]).(string); ok {
				run["resolved_model"] = value
			}
		}
		if record.kind == "model_routing.outcome" && phase == "agent" {
			if value, ok := data["wireModel"].(string); ok {
				run["resolved_model"] = value
			}
		}
		rank := 0
		switch record.kind {
		case "usage.report":
			rank = 3
		case "firewall.token_usage":
			rank = 2
		case "session.result":
			if data["parentToolCallId"] == nil && data["parentToolUseId"] == nil {
				rank = 1
			}
		}
		if rank == 0 {
			continue
		}
		values := usage(data["usage"])
		if len(values) == 0 && nonnegative(data["aic"]) == nil && nonnegative(data["totalAic"]) == nil {
			continue
		}
		values["aic"], values["totalAic"] = nonnegative(data["aic"]), nonnegative(data["totalAic"])
		id := "snapshot"
		if rank != 1 {
			id = text(data["requestId"])
			if id == "" {
				id = fmt.Sprintf("line:%d", record.line)
			}
		}
		if phases[phase] == nil {
			phases[phase] = map[int][]usageEntry{}
		}
		entries := phases[phase][rank]
		retained := entries[:0]
		for _, entry := range entries {
			if entry.id != id {
				retained = append(retained, entry)
			}
		}
		phases[phase][rank] = append(retained, usageEntry{id, values})
	}
	totals := map[string]any{}
	add := func(field string, n float64) {
		current, _ := totals[field].(float64)
		totals[field] = current + n
	}
	phaseNames := make([]string, 0, len(phases))
	for phase := range phases {
		phaseNames = append(phaseNames, phase)
	}
	sort.Strings(phaseNames)
	for _, phase := range phaseNames {
		ranks := phases[phase]
		// #nosec G101 -- these are public usage counter names, not credentials.
		for source, target := range map[string]string{"inputTokens": "total_input_tokens", "outputTokens": "total_output_tokens",
			"cacheReadTokens": "total_cache_read_tokens", "cacheWriteTokens": "total_cache_write_tokens", "reasoningTokens": "total_reasoning_tokens"} {
			for rank := 3; rank >= 1; rank-- {
				found := false
				for _, entry := range ranks[rank] {
					if n, ok := number(entry.values[source]); ok {
						found = true
						add(target, n)
					}
				}
				if found {
					break
				}
			}
		}
		for rank := 3; rank >= 1; rank-- {
			var total, cumulative float64
			found, hasCumulative := false, false
			for _, entry := range ranks[rank] {
				if n, ok := number(entry.values["aic"]); ok {
					total += n
					found = true
				}
				if n, ok := number(entry.values["totalAic"]); ok {
					cumulative, hasCumulative, found = n, true, true
				}
			}
			if found {
				if hasCumulative {
					total = cumulative
				}
				add("total_aic", total)
				break
			}
		}
	}
	if len(totals) != 0 {
		run["token_usage_summary"] = totals
	}
	return run
}
