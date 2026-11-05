package collectnative

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var routingIdentifier = regexp.MustCompile(`^[A-Za-z0-9_.:/-]{1,128}$`)
var routingVersion = regexp.MustCompile(`^model-routing/v(\d+)\.(\d+)\.(\d+)$`)

func identifier(value any) any {
	if value == "[redacted]" {
		return "redacted"
	}
	if s, ok := value.(string); ok && routingIdentifier.MatchString(s) {
		return s
	}
	return nil
}

func modelName(value any) any {
	if s, ok := identifier(value).(string); ok {
		parts := strings.Split(s, "/")
		return parts[len(parts)-1]
	}
	return nil
}

func field(data map[string]any, name string) any {
	var snake strings.Builder
	for _, c := range name {
		if c >= 'A' && c <= 'Z' {
			snake.WriteByte('_')
			c += 'a' - 'A'
		}
		snake.WriteRune(c)
	}
	return first(data[name], data[snake.String()])
}

func routingMetadata(kind string, data map[string]any) ([]map[string]any, bool) {
	switch kind {
	case "firewall.model_routing", "firewall.token_usage", "usage.report", "firewall.steering", "firewall.event", "firewall.upstream_error",
		"workflow.info", "workflow.job_result", "model.config", "model.inventory", "model.baseline_estimate", "model.resolution", "model_routing.outcome",
		"model_endpoint.mismatch", "session.collection", "session.collection_warning", "session.format", "subagent.request", "subagent.started",
		"subagent.configured", "subagent.completed", "subagent.failed", "subagent.model_unavailable":
	default:
		return nil, false
	}
	if kind == "model.inventory" || kind == "firewall.event" {
		return []map[string]any{}, true
	}
	routing := data
	if kind == "workflow.info" {
		routing = object(field(data, "modelRouting"))
	}
	objective, config := object(routing["objective"]), object(data["routing"])
	labels, router := object(routing["labels"]), object(routing["router"])
	selection, steering := object(field(data, "modelSelection")), object(data["steering"])
	fields := map[string]any{
		"routingStage": identifier(routing["stage"]), "routingSchema": identifier(first(routing["schema"], routing["_schema"])),
		"routingGoal": identifier(first(objective["goal"], config["goal"])), "routingConfiguredMode": identifier(first(objective["mode"], config["mode"])),
		"routingMode": identifier(routing["mode"]), "routingSelectedModel": modelName(first(field(routing, "selectedModel"), field(routing, "wireModel"))),
		"routingSelectedEffort": identifier(field(routing, "selectedEffort")), "routingRequestedModel": modelName(field(data, "requestedModel")),
		"routingRequestedEffort": identifier(field(data, "requestedEffort")), "routingAppliedEffort": identifier(field(routing, "appliedEffort")),
		"routingTaskType": identifier(field(labels, "taskType")), "routingScope": identifier(labels["scope"]),
		"routingComplexity": identifier(field(labels, "taskComplexity")), "routingPurpose": identifier(data["purpose"]),
		"routingAgentName": identifier(first(field(data, "agentName"), field(data, "agentDisplayName"))), "routingAgentId": identifier(field(data, "agentId")),
		"routingRouted": identifier(data["routed"]), "routingOutcome": identifier(data["outcome"]),
		"routingRouterVersion": identifier(first(router["version"], field(routing, "routerVersion"))),
		"routingAwfVersion":    identifier(field(data, "awfVersion")), "routingClassifierModel": modelName(field(data, "classifierModel")),
		"routingClassifierEffort": identifier(field(data, "classifierEffort")), "routingClassifierAttempts": nonnegative(field(data, "classifierAttempts")),
		"routingLatencyMs": nonnegative(field(data, "latencyMs")), "routingDegradedReason": identifier(field(data, "degradedReason")),
		"routingSubstitution": identifier(selection["status"]), "routingEffectiveModel": modelName(field(selection, "effectiveModel")),
		"routingSteeringType": identifier(first(steering["type"], data["steeringType"])), "routingSteeringThreshold": nonnegative(steering["threshold"]),
		"routingMaxCredits": nonnegative(field(data, "maxAiCredits")), "model": modelName(first(data["model"], field(data, "wireModel"))),
		"effort": identifier(first(data["effort"], field(data, "reasoningEffort"))), "provider": identifier(data["provider"]),
		"correlationId": identifier(field(data, "requestId")), "code": identifier(first(data["code"], field(data, "errorCode"), field(data, "failureCode"))),
		"name": identifier(first(field(data, "jobId"), data["event"])), "status": identifier(first(data["status"], data["result"])),
		"durationMs": nonnegative(field(data, "durationMs")), "httpStatus": nonnegative(field(data, "httpStatus")),
		"threshold": nonnegative(data["threshold"]), "aic": nonnegative(first(data["aic"], data["ai_credits_this_response"])),
		"totalAic": nonnegative(field(data, "totalAic")), "requestAttempt": nonnegative(data["attempt"]), "sourceEngine": identifier(field(data, "engineId")),
	}
	if n, ok := number(data["status"]); ok {
		fields["status"] = identifier(fmt.Sprint(n))
		fields["httpStatus"] = nonnegative(first(field(data, "httpStatus"), data["status"]))
	}
	for native, canonical := range tokenFields {
		if native == "totalTokens" {
			continue
		}
		value := field(object(data["usage"]), native)
		if canonical == "cacheReadTokens" || canonical == "cacheWriteTokens" {
			value = first(value, field(object(data["usage"]), canonical))
		}
		fields[canonical] = nonnegative(value)
	}
	if value, ok := field(data, "degradedClassification").(bool); ok {
		fields["routingDegraded"] = value
	}
	if value, ok := config["enabled"].(bool); ok {
		fields["routingEnabled"] = value
	}
	if data["legacyEndpointNormalization"] == true {
		fields["evidenceState"] = "legacy-endpoint-normalization"
	}
	if kind == "workflow.info" {
		fields["routingStatus"] = identifier(routing["status"])
		fields["routingSelectedModel"] = modelName(first(field(routing, "selectedModel"), field(routing, "wireModel"), routing["model"]))
		fields["routingSelectedEffort"] = identifier(first(field(routing, "selectedEffort"), routing["effort"]))
		_, err := strconv.ParseUint(scalar(field(data, "runId")), 10, 64)
		fields["required"] = identifier(data["repository"]) != nil && err == nil
	}
	if kind == "session.format" {
		fields["status"] = "unsupported"
		if scalar(data["version"]) == "1" {
			fields["status"] = "supported"
		}
	}
	if kind == "model.baseline_estimate" {
		fields["routingBaselineModel"] = identifier(field(data, "baselineModel"))
		if value, ok := data["complete"].(bool); ok {
			fields["routingBaselineComplete"] = value
		}
		fields["routingBaselineMainRequests"] = nonnegative(field(data, "mainRequests"))
	}
	if kind == "firewall.model_routing" && data["stage"] == "selection" {
		for _, name := range []string{"eligibleChoices", "catalogueOverlap"} {
			fields[name] = nonnegative(integer(field(data, name)))
		}
	}
	if values, ok := data["deviations"].([]any); ok {
		deviations := []string{}
		for _, value := range values {
			if s, ok := identifier(value).(string); ok {
				deviations = append(deviations, s)
			}
		}
		fields["routingDeviations"] = strings.Join(deviations, "/")
	}
	if kind == "model.resolution" {
		fields = map[string]any{"sourceEngine": identifier(data["engine"]), "resolutionRole": identifier(data["role"]),
			"resolutionConfiguredModel": identifier(field(data, "configuredModel")), "resolutionResolvedModel": identifier(field(data, "resolvedModel")),
			"provider": identifier(data["provider"])}
		if status := data["status"]; status == "resolved" || status == "exact" || status == "failed" {
			fields["status"] = status
		}
		if cause := data["cause"]; cause == "model_unavailable" || cause == "catalog_unavailable" {
			fields["code"] = cause
		}
	}
	if kind == "session.collection" {
		entries := []map[string]any{{"key": "summary", "fields": clean(map[string]any{"status": "collection-summary",
			"errorsCount": nonnegative(data["warnings"]), "totalEvents": nonnegative(field(data, "untimedEvents"))})}}
		for i, candidate := range array(data["sources"]) {
			source := object(candidate)
			var name any
			if path := text(source["path"]); strings.HasSuffix(path, ".jsonl") || strings.HasSuffix(path, ".json") {
				parts := strings.Split(path, "/")
				name = identifier(parts[len(parts)-1])
			}
			entries = append(entries, map[string]any{"key": fmt.Sprintf("source:%d", i),
				"fields": clean(map[string]any{"name": name, "component": identifier(source["component"]), "phase": identifier(source["phase"]),
					"totalEvents": nonnegative(source["events"]), "status": "collected"})})
		}
		for i, component := range array(field(data, "absentComponents")) {
			entries = append(entries, map[string]any{"key": fmt.Sprintf("absent:%d", i),
				"fields": clean(map[string]any{"component": identifier(component), "status": "absent"})})
		}
		return entries, true
	}
	return []map[string]any{{"fields": clean(fields)}}, true
}

func normalizeLegacyRouting(records []sessionRecord) {
	var selection *sessionRecord
	for i := range records {
		record := &records[i]
		if record.kind == "firewall.model_routing" && record.provenance["phase"] == "agent" && record.data["stage"] == "selection" &&
			(selection == nil || record.timestamp >= selection.timestamp) {
			selection = record
		}
	}
	if selection == nil {
		return
	}
	version := routingVersion.FindStringSubmatch(scalar(first(selection.data["schema"], selection.data["_schema"])))
	if version == nil {
		return
	}
	major, _ := strconv.Atoi(version[1])
	minor, _ := strconv.Atoi(version[2])
	patch, _ := strconv.Atoi(version[3])
	if major != 0 || minor > 28 || (minor == 28 && patch >= 39) {
		return
	}
	for _, record := range records {
		deviations := array(record.data["deviations"])
		if record.kind == "firewall.model_routing" && record.data["stage"] == "request" && record.data["routed"] == "deviated" &&
			len(deviations) == 1 && deviations[0] == "endpoint" {
			record.data["routed"], record.data["legacyEndpointNormalization"] = "as_selected", true
		}
	}
}
