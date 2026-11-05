package collectnative

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"
)

func gatewayDuration(value any) (string, error) {
	if value == nil {
		return "", nil
	}
	n, ok := number(value)
	if !ok || n < 0 || n > float64(math.MaxInt64)/float64(time.Millisecond) {
		return "", errors.New("invalid gateway duration")
	}
	if n == 0 {
		return "", nil
	}
	return time.Duration(n * float64(time.Millisecond)).String(), nil
}

func rpcAuditType(row map[string]any) string {
	if kind := text(row["type"]); kind != "" {
		return kind
	}
	switch row["event"] {
	case "rpc_request":
		return "REQUEST"
	case "rpc_response":
		return "RESPONSE"
	case "difc_filtered":
		return "DIFC_FILTERED"
	default:
		return text(row["event"])
	}
}

func safeGatewayStatus(value any) bool {
	if value == nil || value == "" {
		return true
	}
	return slices.Contains([]string{"success", "error", "unknown", "pending", "failed", "completed",
		"cancelled", "skipped", "timeout", "timed_out", "started", "in_progress"}, text(value))
}

func safeGatewaySummary(row map[string]any, tool bool) (map[string]any, error) {
	if row == nil || !auditReportName.MatchString(text(row["server_name"])) ||
		(tool && !auditReportName.MatchString(text(row["tool_name"]))) {
		return nil, errors.New("invalid gateway summary identity")
	}
	result, err := safeCounters(row, "call_count", "request_count", "tool_call_count", "failed_calls",
		"total_input_size", "total_output_size", "avg_input_size", "avg_output_size", "max_input_size", "max_output_size")
	if err != nil {
		return nil, err
	}
	result["server_name"] = row["server_name"]
	result["error_count"] = engineCounter(result["failed_calls"])
	delete(result, "failed_calls")
	if tool {
		result["tool_name"], result["call_count"] = row["tool_name"], engineCounter(row["call_count"])
	}
	for _, key := range []string{"avg_duration", "max_duration"} {
		if duration, err := gatewayDuration(row[key+"_ms"]); err != nil {
			return nil, err
		} else if duration != "" {
			result[key] = duration
		}
	}
	count := engineCounter(result["call_count"])
	if !tool {
		count = engineCounter(result["request_count"])
	}
	if count > 0 {
		for _, size := range []string{"input_size", "output_size"} {
			if engineCounter(result["avg_"+size]) == 0 {
				result["avg_"+size] = engineCounter(result["total_"+size]) / count
			}
		}
	}
	return result, nil
}

type gatewayAggregate struct {
	input, output, maxInput, maxOutput int
	duration, maxDuration, minDuration time.Duration
}

func (a *gatewayAggregate) add(row map[string]any) error {
	values := [2]int{}
	for i, key := range []string{"input_size", "output_size"} {
		if row[key] != nil {
			if integer(row[key]) == nil || nonnegative(row[key]) == nil {
				return ErrIdentity
			}
			values[i] = engineCounter(row[key])
		}
	}
	if a.input > 9007199254740991-values[0] || a.output > 9007199254740991-values[1] {
		return ErrCapacity
	}
	a.input, a.output = a.input+values[0], a.output+values[1]
	a.maxInput, a.maxOutput = max(a.maxInput, values[0]), max(a.maxOutput, values[1])
	duration, err := gatewayDuration(row["duration"])
	if err != nil {
		return err
	}
	d, _ := time.ParseDuration(duration)
	if d > 0 {
		if a.duration > time.Duration(math.MaxInt64)-d {
			return ErrCapacity
		}
		a.duration += d
		a.maxDuration = max(a.maxDuration, d)
		if a.minDuration == 0 || d < a.minDuration {
			a.minDuration = d
		}
	}
	return nil
}

func (a *gatewayAggregate) apply(row map[string]any, count int) {
	row["total_input_size"], row["total_output_size"] = a.input, a.output
	row["max_input_size"], row["max_output_size"] = a.maxInput, a.maxOutput
	if count > 0 {
		row["avg_input_size"], row["avg_output_size"] = a.input/count, a.output/count
		if a.duration > 0 {
			row["avg_duration"] = (a.duration / time.Duration(count)).String()
		}
	}
	if a.maxDuration > 0 {
		row["max_duration"], row["min_duration"] = a.maxDuration.String(), a.minDuration.String()
	}
}

// Source selection and request/call accounting follow gh-aw v1.0.1
// gateway_logs_{parsing,mcp,rpc}.go. Payloads and errors never survive.
func enrichGatewayAudit(ctx context.Context, fields map[string]any, files map[string][]byte, identity Identity, limits Limits) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	names, rpc := candidates(files, "gateway.jsonl"), false
	names = slices.DeleteFunc(names, excludedAuditPath)
	if len(names) == 0 {
		names, rpc = candidates(files, "rpc-messages.jsonl"), true
		names = slices.DeleteFunc(names, excludedAuditPath)
	}
	if len(names) == 0 {
		return nil, nil
	}
	rank := func(name string) int {
		inMCP := path.Base(path.Dir(name)) == "mcp-logs"
		if inMCP == rpc {
			return 0
		}
		return 1
	}
	slices.SortFunc(names, func(a, b string) int { return rank(a) - rank(b) })
	if len(names) > 1 && rank(names[0]) == rank(names[1]) {
		return []string{"ambiguous-gateway-logs"}, nil
	}
	type pendingCall struct {
		call map[string]any
		at   time.Time
	}
	tools, servers := map[string]map[string]any{}, map[string]map[string]any{}
	toolMetrics, serverMetrics := map[string]*gatewayAggregate{}, map[string]*gatewayAggregate{}
	calls, records := []any{}, []map[string]any{}
	serverFor := func(name string) map[string]any {
		if servers[name] == nil {
			servers[name] = map[string]any{"server_name": name, "request_count": 0, "tool_call_count": 0, "error_count": 0}
			serverMetrics[name] = &gatewayAggregate{}
		}
		return servers[name]
	}
	toolFor := func(server, name string) map[string]any {
		key := server + "\x00" + name
		if tools[key] == nil {
			tools[key] = map[string]any{"server_name": server, "tool_name": name, "call_count": 0, "error_count": 0}
			toolMetrics[key] = &gatewayAggregate{}
		}
		return tools[key]
	}
	increment := func(row map[string]any, key string) { row[key] = engineCounter(row[key]) + 1 }
	err := scanEvidence(ctx, files[names[0]], limits, func(_ int, raw []byte) error {
		var row map[string]any
		if decode(raw, &row) != nil || row == nil || !matches(row, identity) {
			return ErrIdentity
		}
		records = append(records, row)
		return nil
	})
	if err != nil {
		if errors.Is(err, ErrCapacity) || ctx.Err() != nil {
			return nil, err
		}
		return []string{"invalid-gateway-logs"}, nil
	}
	pending := map[string]pendingCall{}
	metricPending := map[string]pendingCall{}
	for _, row := range records {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		server := text(row["server_name"])
		name, method, event := text(row["tool_name"]), text(row["method"]), text(row["event"])
		filtered := row["type"] == "DIFC_FILTERED" || row["type"] == "GUARD_POLICY_BLOCKED"
		if filtered {
			server = firstText(row["server_id"], row["server_name"])
		}
		if rpc {
			server = text(row["server_id"])
			payload := object(row["payload"])
			if server == "" {
				continue
			}
			if !auditReportName.MatchString(server) || !matches(payload, identity) {
				return []string{"invalid-gateway-identifier"}, nil
			}
			if rpcAuditType(row) == "DIFC_FILTERED" {
				increment(serverFor(server), "difc_filtered_count")
				continue
			}
			if row["direction"] == "IN" && rpcAuditType(row) == "RESPONSE" {
				if payload["error"] != nil {
					increment(serverFor(server), "error_count")
					code, ok := number(object(payload["error"])["code"])
					if ok && code >= -32006 && code <= -32001 {
						increment(serverFor(server), "guard_policy_blocked_count")
					}
				}
				key := fmt.Sprint(server, "/", payload["id"])
				if p, ok := metricPending[key]; ok && text(row["timestamp"]) != "" && payload["id"] != nil {
					delete(metricPending, key)
					at, err := time.Parse(time.RFC3339Nano, text(row["timestamp"]))
					if err == nil && !at.Before(p.at) {
						metric := map[string]any{"duration": json.Number(strconv.FormatInt(at.Sub(p.at).Milliseconds(), 10))}
						toolName := text(p.call["tool_name"])
						if err := serverMetrics[server].add(metric); err != nil {
							return nil, err
						}
						if err := toolMetrics[server+"\x00"+toolName].add(metric); err != nil {
							return nil, err
						}
						if payload["error"] != nil {
							increment(toolFor(server, toolName), "error_count")
						}
					}
				}
				continue
			}
			if row["direction"] != "OUT" || rpcAuditType(row) != "REQUEST" || payload["method"] != "tools/call" {
				continue
			}
			name, method, event = text(object(payload["params"])["name"]), "tools/call", "request"
			if name == "" {
				continue
			}
		}
		if server == "" {
			continue
		}
		if !auditReportName.MatchString(server) || (name != "" && !auditReportName.MatchString(name)) {
			return []string{"invalid-gateway-identifier"}, nil
		}
		isError := row["status"] == "error" || text(row["error"]) != "" || row["level"] == "error"
		request := event == "request" || event == "tool_call" || event == "rpc_call"
		tool := event == "tool_call" || method == "tools/call"
		if !request && !isError && !filtered && !tool {
			continue
		}
		s := serverFor(server)
		if !rpc && (row["status"] == "error" || text(row["error"]) != "" || row["level"] == "error") {
			increment(s, "error_count")
			if name != "" {
				increment(toolFor(server, name), "error_count")
			}
		}
		if filtered {
			key := "difc_filtered_count"
			if row["type"] == "GUARD_POLICY_BLOCKED" {
				key = "guard_policy_blocked_count"
			}
			increment(s, key)
			continue
		}
		if request {
			increment(s, "request_count")
			if !rpc {
				if err := serverMetrics[server].add(row); err != nil {
					if errors.Is(err, ErrCapacity) {
						return nil, err
					}
					return []string{"invalid-gateway-counter"}, nil
				}
			}
			if tool {
				if name == "" {
					name = method
				}
				increment(s, "tool_call_count")
				increment(toolFor(server, name), "call_count")
				if !rpc {
					if err := toolMetrics[server+"\x00"+name].add(row); err != nil {
						if errors.Is(err, ErrCapacity) {
							return nil, err
						}
						return []string{"invalid-gateway-counter"}, nil
					}
				}
			}
		}
		if !tool || name == "" {
			continue
		}
		if !safeGatewayStatus(row["status"]) {
			return []string{"invalid-gateway-status"}, nil
		}
		call := map[string]any{"server_name": server, "tool_name": name,
			"timestamp": eventClock(row["timestamp"]), "status": firstText(row["status"], "success"),
			"input_size": json.Number("0"), "output_size": json.Number("0")}
		for _, key := range []string{"input_size", "output_size"} {
			if value := row[key]; value != nil {
				if integer(value) == nil || nonnegative(value) == nil {
					return []string{"invalid-gateway-counter"}, nil
				}
				call[key] = value
			}
		}
		if row["status"] == nil && (text(row["error"]) != "" || row["level"] == "error") {
			call["status"] = "error"
		}
		if duration, err := gatewayDuration(row["duration"]); err != nil {
			return []string{"invalid-gateway-counter"}, nil //nolint:nilerr // Invalid optional evidence produces a gap, never partially applied counters.
		} else if duration != "" {
			call["duration"] = duration
		}
		if !rpc {
			calls = append(calls, call)
			continue
		}
		payload := object(row["payload"])
		call["status"] = "unknown"
		if payload["id"] == nil {
			calls = append(calls, call)
		} else if at, err := time.Parse(time.RFC3339Nano, text(row["timestamp"])); err == nil {
			key := fmt.Sprint(server, "/", payload["id"])
			pending[key], metricPending[key] = pendingCall{call, at}, pendingCall{call, at}
		}
	}
	if rpc {
		processed := map[string]bool{}
		for _, row := range records {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if row["direction"] != "IN" || rpcAuditType(row) != "RESPONSE" {
				continue
			}
			server, payload := text(row["server_id"]), object(row["payload"])
			if server == "" {
				continue
			}
			key := fmt.Sprint(server, "/", payload["id"])
			p, ok := pending[key]
			if !ok || payload["id"] == nil {
				continue
			}
			processed[key] = true
			call := maps.Clone(p.call)
			call["status"] = "success"
			if payload["error"] != nil {
				call["status"] = "error"
			}
			if at, err := time.Parse(time.RFC3339Nano, text(row["timestamp"])); err == nil && !at.Before(p.at) {
				call["duration"] = at.Sub(p.at).String()
			}
			calls = append(calls, call)
		}
		for _, key := range slices.Sorted(maps.Keys(pending)) {
			if !processed[key] {
				calls = append(calls, pending[key].call)
			}
		}
	}
	if len(servers) == 0 {
		return nil, nil
	}
	summaries, health := []any{}, []any{}
	fallback := map[string]map[string]any{}
	for _, candidate := range array(object(fields["mcp_tool_usage"])["summary"]) {
		row := object(candidate)
		fallback[text(row["server_name"])+"\x00"+text(row["tool_name"])] = row
	}
	for _, key := range slices.Sorted(maps.Keys(tools)) {
		toolMetrics[key].apply(tools[key], engineCounter(tools[key]["call_count"]))
		if summary := fallback[key]; summary != nil {
			for _, field := range []string{"avg_input_size", "avg_output_size", "max_input_size",
				"max_output_size", "avg_duration", "max_duration"} {
				value := tools[key][field]
				missing := value == nil
				if strings.HasSuffix(field, "size") {
					missing = engineCounter(value) == 0
				}
				if missing && summary[field] != nil {
					tools[key][field] = summary[field]
				}
			}
		}
		summaries = append(summaries, tools[key])
	}
	for _, key := range slices.Sorted(maps.Keys(servers)) {
		serverMetrics[key].apply(servers[key], engineCounter(servers[key]["request_count"]))
		health = append(health, servers[key])
	}
	if len(calls) == 0 {
		calls = array(object(fields["mcp_tool_usage"])["tool_calls"])
	}
	fields["mcp_tool_usage"] = map[string]any{"summary": summaries, "servers": health, "tool_calls": calls}
	return nil, nil
}

func mergedAuditTools(fields map[string]any, native map[string]int) map[string]int {
	result := map[string]int{}
	for name, count := range native {
		result[prettyAuditTool(name)] += count
	}
	usage := object(fields["mcp_tool_usage"])
	rows, summaries := array(usage["summary"]), true
	if len(rows) == 0 {
		rows, summaries = array(usage["tool_calls"]), false
	}
	for _, candidate := range rows {
		row := object(candidate)
		name := text(row["tool_name"])
		if name == "" {
			continue
		}
		if server := text(row["server_name"]); server != "" {
			name = server + "." + name
		}
		count := 1
		if summaries {
			count = engineCounter(row["call_count"])
		}
		result[prettyAuditTool(strings.TrimSpace(name))] += count
	}
	return result
}
