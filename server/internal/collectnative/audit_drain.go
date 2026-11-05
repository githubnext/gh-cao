package collectnative

import (
	"context"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

type transientAuditTools struct{ tools map[string]int }
type transientAuditReports struct{ items []map[string]any }

type drainAuditEvent struct {
	stage  string
	fields map[string]string
}

type drainAuditCluster struct {
	template []string
	size     int
}

var auditDrainMasks = []struct {
	pattern     *regexp.Regexp
	replacement string
}{
	{regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`), "<UUID>"},
	{regexp.MustCompile(`session=[a-z0-9]+`), "session=<*>"},
	{regexp.MustCompile(`=\d+`), "=<NUM>"},
	{regexp.MustCompile(`https?://[^\s]+`), "<URL>"},
	{regexp.MustCompile(`"[^"]*"`), `"<*>"`},
	{regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})?`), "<TIMESTAMP>"},
}

func drainAuditTools(fields map[string]any) map[string]int {
	var tools map[string]int
	if native, ok := object(fields["audit"])["native_tools"].(transientAuditTools); ok {
		tools = native.tools
	}
	return mergedAuditTools(fields, tools)
}

func drainAuditEvents(fields map[string]any) []drainAuditEvent {
	audit := object(fields["audit"])
	metrics := object(audit["metrics"])
	turns := engineCounter(first(metrics["turns"], fields["turns"]))
	tokens := engineCounter(first(metrics["token_usage"], fields["token_usage"]))
	errors := engineCounter(fields["error_count"])
	if fields["conclusion"] == "failure" && errors == 0 {
		errors = 1
	}
	events := []drainAuditEvent{}
	if turns > 0 {
		events = append(events, drainAuditEvent{"plan", map[string]string{"turns": strconv.Itoa(turns), "errors": strconv.Itoa(errors)}})
	}
	tools := drainAuditTools(fields)
	names := slices.Sorted(maps.Keys(tools))
	slices.SortStableFunc(names, func(a, b string) int { return tools[b] - tools[a] })
	for _, name := range names {
		events = append(events, drainAuditEvent{"tool_call", map[string]string{"tool": name, "calls": strconv.Itoa(tools[name])}})
	}
	for _, candidate := range array(audit["mcp_failures"]) {
		failure := object(candidate)
		events = append(events, drainAuditEvent{"error", map[string]string{"type": "mcp_failure",
			"server": text(failure["server_name"]), "status": text(failure["status"])}})
	}
	inputs, _ := audit["native_reports"].(transientAuditReports)
	for _, kind := range []string{"missing_tool", "missing_data", "noop"} {
		for _, item := range inputs.items {
			if text(item["type"]) != kind && text(item["type"]) != "report_"+kind {
				continue
			}
			switch kind {
			case "missing_tool":
				events = append(events, drainAuditEvent{"error", map[string]string{"type": kind, "tool": text(item["tool"]), "reason": text(item["reason"])}})
			case "missing_data":
				events = append(events, drainAuditEvent{"error", map[string]string{"type": kind, "data_type": text(item["data_type"]), "reason": text(item["reason"])}})
			case "noop":
				events = append(events, drainAuditEvent{"tool_result", map[string]string{"status": "noop", "message": text(item["message"])}})
			}
		}
	}
	conclusion := text(first(fields["conclusion"], fields["status"]))
	if conclusion != "" || tokens > 0 {
		events = append(events, drainAuditEvent{"finish", map[string]string{"status": conclusion, "tokens": strconv.Itoa(tokens)}})
	}
	return events
}

// Adapted from pinned pkg/agentdrain. A collector owns one local miner per
// stage; no process lock, filesystem weights or mutable cross-run state is used.
// The v1.0.1 embedded weights have only these two trained baseline templates.
func drainAuditInsights(ctx context.Context, fields map[string]any, limits Limits) ([]any, error) {
	clusters := map[string][]*drainAuditCluster{
		"plan":   {{template: []string{"stage=plan", "errors=<NUM>", "turns=<NUM>"}, size: 57}},
		"finish": {{template: []string{"stage=finish", "<*>", "tokens=<NUM>"}, size: 410}},
	}
	events := drainAuditEvents(fields)
	if len(events) == 0 {
		return nil, nil
	}
	if len(events) > limits.defaults().Entries {
		return nil, ErrCapacity
	}
	work, anomalies := 0, 0
	for _, event := range events {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		parts := []string{"stage=" + event.stage}
		for _, key := range slices.Sorted(maps.Keys(event.fields)) {
			parts = append(parts, key+"="+event.fields[key])
		}
		line := strings.Join(parts, " ")
		if len(line) > limits.defaults().LineBytes {
			return nil, ErrCapacity
		}
		for _, rule := range auditDrainMasks {
			line = rule.pattern.ReplaceAllString(line, rule.replacement)
		}
		tokens := strings.Fields(line)
		var best *drainAuditCluster
		score := -1.0
		for _, cluster := range clusters[event.stage] {
			if len(cluster.template) != len(tokens) {
				continue
			}
			matches, fixed := 0, 0
			for i, token := range cluster.template {
				work++
				if work > limits.defaults().Entries {
					return nil, ErrCapacity
				}
				if token != "<*>" {
					fixed++
					if token == tokens[i] {
						matches++
					}
				}
			}
			similarity := 1.0
			if fixed > 0 {
				similarity = float64(matches) / float64(fixed)
			}
			if similarity > score {
				best, score = cluster, similarity
			}
		}
		if best == nil || score < .4 {
			clusters[event.stage] = append(clusters[event.stage], &drainAuditCluster{template: tokens, size: 1})
			anomalies++
		} else {
			for i, token := range best.template {
				if token == "<*>" || tokens[i] == "<*>" || token != tokens[i] {
					best.template[i] = "<*>"
				}
			}
			best.size++
		}
	}
	insights := []any{map[string]any{"category": "execution", "severity": "info", "title": "Log template patterns mined"}}
	if anomalies > 0 {
		severity := "low"
		if anomalies >= 3 {
			severity = "high"
		} else if anomalies == 2 {
			severity = "medium"
		}
		insights = append(insights, map[string]any{"category": "reliability", "severity": severity,
			"title": fmt.Sprintf("%d anomalous event pattern(s) detected", anomalies)})
	}
	return append(insights, map[string]any{"category": "execution", "severity": "info", "title": "Agent stage sequence"}), nil
}
