package collectnative

import (
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/githubnext/gh-cao/server/internal/model"
)

func legacyValue(input map[string]any, name string) any {
	if value, present := input[name]; present {
		return value
	}
	return object(input["audit"])[name]
}

func dominantLegacyModel(usage map[string]any) string {
	models := object(usage["by_model"])
	names := make([]string, 0, len(models))
	for name := range models {
		names = append(names, name)
	}
	slices.Sort(names)
	winner, highest := "", -1.0
	for _, name := range names {
		aic, _ := number(object(models[name])["aic"])
		if aic > highest {
			winner, highest = name, aic
		}
	}
	return winner
}

func legacyAggregates(run model.Row, input map[string]any) {
	if firewall := object(legacyValue(input, "firewall_analysis")); firewall != nil {
		allowed, blocked := 0.0, 0.0
		for _, candidate := range object(firewall["requests_by_domain"]) {
			counts := object(candidate)
			a, _ := number(counts["allowed"])
			b, _ := number(counts["blocked"])
			allowed += max(0, a)
			blocked += max(0, b)
		}
		run["firewallAllowedCalls"], run["firewallBlockedCalls"] = allowed, blocked
	}
	if mcp := object(legacyValue(input, "mcp_tool_usage")); mcp != nil {
		calls := array(mcp["tool_calls"])
		response := 0.0
		for _, candidate := range calls {
			call := object(candidate)
			value, _ := number(first(call["output_size"], call["response_size"]))
			response += max(0, value)
		}
		run["mcpToolCalls"], run["mcpResponseBytes"] = len(calls), response
	}
	audit := object(input["audit"])
	high, medium, present := 0, 0, false
	for _, name := range []string{"key_findings", "observability_insights", "recommendations"} {
		if _, ok := audit[name].([]any); ok {
			present = true
		}
		for _, candidate := range array(audit[name]) {
			item := object(candidate)
			switch strings.ToLower(text(first(item["severity"], item["priority"]))) {
			case "high":
				high++
			case "medium":
				medium++
			}
		}
	}
	if present {
		run["highPriorityAuditItems"], run["mediumPriorityAuditItems"] = high, medium
	}
	if usage := object(input["token_usage_summary"]); usage != nil && usage["total_reasoning_tokens"] == nil {
		total, present := 0.0, false
		for _, candidate := range object(usage["by_model"]) {
			if n, ok := number(object(candidate)["reasoning_tokens"]); ok {
				total += n
				present = true
			}
		}
		if present {
			run["reasoningTokens"] = total
		}
	}
}

type emitLegacy func(string, string, string, string, string, any, map[string]any)

func (p *Projection) legacyEvents(input map[string]any, closed string, emit emitLegacy) {
	audit := object(input["audit"])
	if input["cao_unified_session"] == nil {
		mcp := object(input["mcp_tool_usage"])
		if len(array(mcp["tool_calls"])) == 0 {
			mcp = object(audit["mcp_tool_usage"])
		}
		for index, candidate := range array(mcp["tool_calls"]) {
			call := object(candidate)
			status := firstText(call["status"], "unknown")
			fields := map[string]any{"correlationId": firstText(call["tool_call_id"], fmt.Sprintf("%s:mcp:%d", p.RunID, index))}
			for from, to := range map[string]string{"server_name": "mcpServer", "tool_name": "mcpTool"} {
				if value := optionalText(call[from]); value != "" {
					fields[to] = value
				}
			}
			for to, value := range map[string]any{"requestBytes": first(call["input_size"], call["request_size"]),
				"responseBytes": first(call["output_size"], call["response_size"])} {
				if n, ok := number(value); ok {
					fields[to] = max(0, n)
				}
			}
			clock := firstText(eventClock(call["timestamp"]), closed)
			summary := joined(firstText(call["server_name"], "unknown"), firstText(call["tool_name"], "unknown"))
			emit("tool.call", clock, summary, "started", "mcp", struct {
				Type   string `json:"type"`
				Index  int    `json:"index"`
				Server any    `json:"server,omitempty"`
				Tool   any    `json:"tool,omitempty"`
			}{"mcp-call", index, call["server_name"], call["tool_name"]}, fields)
			kind := "tool.error"
			if status == "success" {
				kind = "tool.result"
			}
			emit(kind, clock, summary, status, "mcp", struct {
				Type   string `json:"type"`
				Index  int    `json:"index"`
				Status string `json:"status"`
			}{"mcp-outcome", index, status}, fields)
		}
	}
	for _, declaration := range [][4]string{
		{"key_findings", "audit.finding", "title", "severity"},
		{"observability_insights", "audit.observability", "title", "severity"},
		{"recommendations", "audit.recommendation", "action", "priority"},
		{"missing_tools", "audit.missing_tool", "tool", "status"},
		{"missing_data", "audit.missing_data", "data_type", "status"},
		{"noops", "audit.noop", "message", "status"},
		{"mcp_failures", "audit.mcp_failure", "server_name", "status"},
	} {
		name, kind, summaryKey, statusKey := declaration[0], declaration[1], declaration[2], declaration[3]
		for index, candidate := range array(audit[name]) {
			item := object(candidate)
			fields := map[string]any{}
			if code := optionalText(item["code"]); code != "" {
				fields["code"] = code
			}
			emit(kind, firstText(eventClock(item["timestamp"]), closed), firstText(item[summaryKey], kind),
				text(item[statusKey]), "audit", struct {
					Type    string `json:"type"`
					Index   int    `json:"index"`
					Summary any    `json:"summary,omitempty"`
					Status  any    `json:"status,omitempty"`
				}{kind, index, item[summaryKey], item[statusKey]}, fields)
		}
	}
	if input["cao_unified_session"] != nil {
		return
	}
	for index, candidate := range array(legacyValue(input, "skill_activations")) {
		skill := object(candidate)
		name := firstText(skill["name"], fmt.Sprintf("Skill %d", index+1))
		fields := map[string]any{"name": name, "invocationCount": 1, "failedCount": 0}
		for from, to := range map[string]string{"invocation_count": "invocationCount", "failed_count": "failedCount"} {
			if n, ok := number(skill[from]); ok {
				fields[to] = max(0, n)
			}
		}
		if source := optionalText(skill["source"]); source != "" {
			fields["activationSource"] = source
		}
		emit("audit.skill_activation", firstText(eventClock(skill["timestamp"]), closed), name, firstText(skill["status"], "invoked"),
			"audit", struct {
				Type  string `json:"type"`
				Index int    `json:"index"`
				Name  string `json:"name"`
			}{"audit.skill_activation", index, name}, fields)
	}
	domains := object(object(legacyValue(input, "firewall_analysis"))["requests_by_domain"])
	hosts := make([]string, 0, len(domains))
	for host := range domains {
		hosts = append(hosts, host)
	}
	slices.Sort(hosts)
	for _, host := range hosts {
		if strings.TrimSpace(host) == "" || host == "-" {
			continue
		}
		domain := host
		address := host
		if !strings.Contains(address, "://") {
			address = "https://" + address
		}
		if parsed, err := url.Parse(address); err == nil && parsed.Hostname() != "" {
			domain = parsed.Hostname()
		}
		counts := object(domains[host])
		for _, mapping := range [][3]string{{"allowed", "allowed", "net_allowed"}, {"blocked", "denied", "net_blocked"}} {
			count, ok := number(counts[mapping[0]])
			if !ok || count <= 0 {
				continue
			}
			emit(mapping[2], closed, host, mapping[1], "firewall", struct {
				Type     string `json:"type"`
				Host     string `json:"host"`
				Decision string `json:"decision"`
			}{"firewall", host, mapping[1]}, map[string]any{"domain": domain, "decision": mapping[1], "requestCount": count})
		}
	}
}
