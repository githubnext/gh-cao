package collectnative

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type sessionRecord struct {
	kind       string
	data       map[string]any
	provenance map[string]any
	line       int
	timestamp  string
}

func first(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func text(value any) string {
	result, _ := value.(string)
	return result
}

func finite(value any) bool {
	n, ok := number(value)
	return ok && !math.IsNaN(n) && !math.IsInf(n, 0)
}

func nonnegative(value any) any {
	n, ok := number(value)
	if !ok || !finite(value) || n < 0 {
		return nil
	}
	return value
}

func integer(value any) any {
	n, ok := number(value)
	if !ok || !finite(value) || math.Trunc(n) != n || math.Abs(n) > 9007199254740991 {
		return nil
	}
	return value
}

func clean(fields map[string]any) map[string]any {
	result := map[string]any{}
	for field, value := range fields {
		switch value.(type) {
		case string, bool, int, int64, float64:
			result[field] = value
		case json.Number:
			if finite(value) {
				result[field] = value
			}
		}
	}
	return result
}

func selected(data map[string]any, names ...string) map[string]any {
	result := map[string]any{}
	for _, name := range names {
		if value, ok := data[name]; ok {
			if value == nil {
				result[name] = nil
			} else if _, ok := clean(map[string]any{name: value})[name]; ok {
				result[name] = value
			}
		}
	}
	return result
}

func merge(target, source map[string]any) {
	for key, value := range source {
		target[key] = value
	}
}

func array(value any) []any {
	result, _ := value.([]any)
	return result
}

func eventClock(value any) string {
	var at time.Time
	if n, ok := number(value); ok && finite(value) && math.Abs(n) <= 8640000000000000 {
		at = time.UnixMilli(int64(n))
	} else if s, ok := value.(string); ok {
		parsed, err := time.Parse(time.RFC3339Nano, s)
		if err != nil {
			return ""
		}
		at = parsed
	} else {
		return ""
	}
	return at.UTC().Format("2006-01-02T15:04:05.000Z")
}

// ProjectSession implements the metadata-only unified-session boundary. Unknown
// events are omitted and explicitly reported, never persisted by generic copying.
func ProjectSession(ctx context.Context, content []byte, location string, identity Identity, limits Limits) (map[string]any, []string, error) {
	limits = limits.defaults()
	if identity.Validate() != nil {
		return nil, nil, ErrIdentity
	}
	if len(content) > limits.Bytes || !utf8.Valid(content) {
		return nil, nil, ErrCapacity
	}
	scanner := bufio.NewScanner(bytes.NewReader(content))
	scanner.Buffer(make([]byte, min(4096, limits.LineBytes)), limits.LineBytes)
	records := []sessionRecord{}
	line := 0
	for scanner.Scan() {
		line++
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		if len(bytes.TrimSpace(scanner.Bytes())) == 0 {
			continue
		}
		var event map[string]any
		if err := decode(scanner.Bytes(), &event); err != nil {
			return nil, nil, errors.New("invalid unified session JSON")
		}
		data := object(event["data"])
		kind := text(event["type"])
		if data == nil || kind == "" {
			return nil, nil, errors.New("invalid unified session envelope")
		}
		if len(records) >= limits.Entries {
			return nil, nil, ErrCapacity
		}
		provenance := object(event["provenance"])
		records = append(records, sessionRecord{kind, data, provenance, line,
			eventClock(first(provenance["timestampMs"], event["timestamp"]))})
		if len(records) == 1 && kind == "session.format" && scalar(data["version"]) != "1" {
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, nil, err
	}
	if len(records) == 0 {
		return nil, nil, errors.New("empty unified session")
	}
	normalizeLegacyRouting(records)
	document := map[string]any{"version": 1, "path": location}
	events := []map[string]any{}
	gaps := []string{}
	tools := map[string][2]string{}
	rpcs := map[string][2]string{}
	native := map[string]bool{}
	callKey := func(server, tool string) string { return server + "\x00" + tool }
	for _, record := range records {
		if record.kind == "mcp.tool_call" && matches(record.data, identity) {
			native[callKey(text(first(record.data["serverName"], record.data["mcpServerName"])), text(record.data["toolName"]))] = true
		}
	}
	turn, clock := 0, ""
	for _, record := range records {
		data, kind := record.data, record.kind
		if !matches(data, identity) {
			continue
		}
		if record.timestamp != "" && (clock == "" || record.timestamp < clock) {
			clock = record.timestamp
		}
		toolKey, _ := json.Marshal([]any{data["sessionId"], data["agentId"], data["parentToolCallId"], data["toolCallId"]})
		rpcKey, _ := json.Marshal([]any{data["serverName"], first(data["rpcId"], data["requestId"])})
		previous := [2]string{}
		switch kind {
		case "tool.execution_complete":
			previous = tools[string(toolKey)]
		case "mcp.rpc.response":
			previous = rpcs[string(rpcKey)]
		}
		server := text(first(data["mcpServerName"], data["serverName"]))
		tool := text(data["toolName"])
		if server == "" {
			server = previous[0]
		}
		if tool == "" {
			tool = previous[1]
		}
		if kind == "tool.execution_start" && text(data["toolCallId"]) != "" {
			tools[string(toolKey)] = [2]string{server, tool}
		}
		if kind == "mcp.rpc.request" && data["method"] == "tools/call" {
			rpcs[string(rpcKey)] = [2]string{server, tool}
		}
		summary := strings.Trim(server+"/"+tool, "/")
		fields := map[string]any{}
		source := "agent"
		var expanded []map[string]any
		switch kind {
		case "user.message":
			turn++
			fields["summary"] = fmt.Sprintf("turn %d", turn)
		case "assistant.message", "assistant.reasoning":
		case "tool.execution_start", "tool.execution_complete":
			merge(fields, map[string]any{"summary": summary, "toolName": nonempty(tool), "mcpServer": nonempty(server),
				"correlationId": nonempty(text(data["toolCallId"])), "invocationCount": 0, "latencyMs": nonnegative(data["durationMs"])})
			if kind == "tool.execution_start" {
				fields["invocationCount"] = 1
			} else {
				fields["status"] = nonempty(text(data["status"]))
				if data["success"] == true {
					fields["status"] = "success"
				} else if data["success"] == false || data["isError"] == true {
					fields["status"] = "error"
				}
			}
		case "mcp.tool_call", "mcp.difc.filtered", "mcp.guard.blocked":
			source = "mcp"
			merge(fields, map[string]any{"summary": summary, "mcpServer": nonempty(server), "mcpTool": nonempty(tool),
				"correlationId": nonempty(text(data["toolCallId"])), "status": callStatus(data), "invocationCount": 0})
			if kind == "mcp.tool_call" {
				fields["invocationCount"] = 1
			}
			callMetrics(fields, data)
		case "mcp.rpc.request", "mcp.rpc.response":
			direction := strings.ToUpper(text(data["direction"]))
			if !native[callKey(server, tool)] && tool != "" && (data["method"] == "tools/call" || previous != [2]string{}) &&
				(data["direction"] == nil || direction == "OUT" || direction == "OUTBOUND" || direction == "IN" || direction == "INBOUND") {
				source = "mcp"
				status := callStatus(data)
				if status == nil {
					status = "success"
					if kind == "mcp.rpc.request" {
						status = "started"
					}
				}
				merge(fields, map[string]any{"summary": summary, "mcpServer": nonempty(server), "mcpTool": tool, "status": status,
					"correlationId": nonempty(scalar(first(data["toolCallId"], data["requestId"], data["rpcId"]))), "invocationCount": 0})
				if kind == "mcp.rpc.request" {
					fields["invocationCount"] = 1
				}
				callMetrics(fields, data)
			} else {
				source = "session"
				enriched := map[string]any{}
				merge(enriched, data)
				enriched["serverName"], enriched["toolName"] = nonempty(server), nonempty(tool)
				expanded = metadata(kind, enriched)
			}
		case "firewall.http_access":
			host := text(data["host"])
			if !strings.Contains(host, "://") {
				host = "https://" + host
			}
			address, err := url.Parse(host)
			if err != nil || address.Hostname() == "" {
				continue
			}
			domain := strings.ToLower(address.Hostname())
			decision := strings.ToLower(scalar(data["decision"]))
			denied := strings.Contains(decision, "denied") || strings.Contains(decision, "blocked") || strings.Contains(decision, "reject")
			status, _ := number(data["status"])
			if !strings.Contains(decision, "allow") && !strings.Contains(decision, "tcp_miss") && !strings.Contains(decision, "tunnel") && status >= 400 && status < 600 {
				denied = true
			}
			source = "firewall"
			fields = map[string]any{"summary": domain, "domain": domain, "requestCount": 1, "decision": "allowed",
				"status": nonempty(scalar(data["status"])), "method": nonempty(text(data["method"])),
				"responseBytes": nonnegative(integer(data["bytes"])), "latencyMs": nonnegative(data["durationMs"])}
			if denied {
				fields["decision"] = "denied"
			}
		case "skill.invoked":
			source = "audit"
			fields = map[string]any{"name": nonempty(text(first(data["name"], data["skillName"]))),
				"summary": nonempty(text(first(data["name"], data["skillName"]))), "invocationCount": 1, "failedCount": 0,
				"activationSource": nonempty(text(data["source"]))}
			if data["success"] == false {
				fields["failedCount"] = 1
			}
		case "session.error", "assistant.refusal", "session.collection_warning":
			fields = map[string]any{"status": "warning", "code": nonempty(text(first(data["code"], data["policyCategory"])))}
			if kind == "session.error" {
				fields["status"], fields["model"], fields["httpStatus"] = "error", nonempty(text(data["model"])), integer(data["statusCode"])
			}
		default:
			source = "session"
			expanded = metadata(kind, data)
			if expanded == nil && kind != "grader.result" && kind != "grader.manifest" && kind != "eval.result" &&
				kind != "experiment.assignment" && kind != "experiment.state" {
				gaps = append(gaps, "unsupported-session-event")
			}
		}
		if expanded == nil && source == "session" {
			continue
		}
		if expanded == nil {
			expanded = []map[string]any{{"fields": fields}}
		}
		for _, entry := range expanded {
			fields := clean(object(entry["fields"]))
			if source == "session" {
				for _, field := range []string{"component", "phase"} {
					if fields[field] == nil && record.provenance[field] != nil {
						merge(fields, clean(map[string]any{field: record.provenance[field]}))
					}
				}
			}
			event := map[string]any{"line": record.line, "source": source, "type": kind, "fields": fields}
			if record.timestamp != "" {
				event["timestamp"] = record.timestamp
			}
			if entry["key"] != nil {
				event["key"] = entry["key"]
			}
			events = append(events, event)
			if len(events) > limits.Entries {
				return nil, nil, ErrCapacity
			}
		}
	}
	normalizeMCP(events)
	if records[0].kind != "session.format" {
		events = append([]map[string]any{{"line": records[0].line, "source": "session", "type": "session.format",
			"key": "legacy-format", "fields": map[string]any{"status": "legacy"}}}, events...)
	}
	if clock != "" {
		at, _ := time.Parse(time.RFC3339Nano, clock)
		for _, event := range events {
			if event["type"] == "session.format" {
				object(event["fields"])["routingSessionClock"] = at.UnixMilli()
				break
			}
		}
	}
	document["events"] = events
	document["run"] = sessionRun(records, identity, location)
	if err := evaluations(document, records, identity, location); err != nil {
		return nil, nil, err
	}
	return document, gaps, nil
}

func nonempty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func callStatus(data map[string]any) any {
	status, _ := number(data["httpStatus"])
	if status == 429 {
		return "rate-limited"
	}
	if (data["error"] != nil && data["error"] != false) || status >= 400 {
		return "error"
	}
	return nonempty(text(data["status"]))
}

func callMetrics(fields, data map[string]any) {
	for source, target := range map[string]string{"inputSize": "requestBytes", "outputSize": "responseBytes", "durationMs": "latencyMs"} {
		value := nonnegative(data[source])
		if target != "latencyMs" {
			value = integer(value)
		}
		fields[target] = value
	}
}

func normalizeMCP(events []map[string]any) {
	native := map[string]bool{}
	key := func(fields map[string]any) string {
		return scalar(fields["mcpServer"]) + "\x00" + scalar(first(fields["mcpTool"], fields["toolName"])) + "\x00" + scalar(fields["correlationId"])
	}
	for _, event := range events {
		if event["source"] == "mcp" && (event["type"] == "mcp.tool_call" || event["type"] == "mcp.rpc.request") {
			native[key(object(event["fields"]))] = true
		}
	}
	for _, event := range events {
		fields := object(event["fields"])
		server, tool := text(fields["mcpServer"]), text(first(fields["mcpTool"], fields["toolName"]))
		tool = strings.TrimPrefix(tool, server+"-")
		if event["source"] != "agent" || server == "" || tool == "" ||
			(event["type"] != "tool.execution_start" && event["type"] != "tool.execution_complete") {
			continue
		}
		normalized := server + "\x00" + tool + "\x00" + scalar(fields["correlationId"])
		if event["type"] == "tool.execution_start" && fields["correlationId"] != nil && native[normalized] {
			continue
		}
		event["source"] = "mcp"
		fields["summary"], fields["mcpTool"] = server+"/"+tool, tool
	}
}

var graderFields = []string{"id", "name", "status", "value", "score", "unit", "passed", "direction",
	"threshold", "source", "digest", "experimentId", "experiment_id", "variant"}

func evaluations(document map[string]any, records []sessionRecord, identity Identity, location string) error {
	manifest := map[string]map[string]any{}
	results, evals := []map[string]any{}, []map[string]any{}
	for _, record := range records {
		data := record.data
		observation := map[string]any{"sessionPayloadRef": fmt.Sprintf("%s#L%d", location, record.line)}
		if record.timestamp != "" {
			observation["timestamp"] = record.timestamp
		}
		switch record.kind {
		case "experiment.state", "experiment.assignment":
			if !matches(data, identity) || (record.kind == "experiment.state" && scalar(data["runId"]) != strconv.FormatInt(identity.RunID, 10)) {
				continue
			}
			input := object(data["assignments"])
			if input == nil {
				return errors.New("invalid session experiment assignments")
			}
			assignments := map[string]any{}
			for name, value := range input {
				if strings.TrimSpace(name) != "" && strings.TrimSpace(text(value)) != "" {
					assignments[name] = value
				}
			}
			document["experiments"] = map[string]any{"assignments": assignments}
		case "grader.manifest":
			for _, candidate := range array(data["graders"]) {
				grader := object(candidate)
				if id, ok := grader["id"].(string); ok {
					manifest[id] = selected(grader, graderFields...)
				}
			}
		case "grader.result":
			run := object(data["run"])
			if !matches(data, identity) || (run != nil && (scalar(run["id"]) != strconv.FormatInt(identity.RunID, 10) ||
				(run["attempt"] != nil && scalar(run["attempt"]) != strconv.Itoa(identity.Attempt)))) {
				continue
			}
			candidates, ok := data["results"].([]any)
			if !ok {
				candidates = []any{data}
			}
			for _, candidate := range candidates {
				grader := object(candidate)
				if strings.TrimSpace(text(grader["id"])) == "" {
					return errors.New("invalid session grader identity")
				}
				if !matches(grader, identity) {
					continue
				}
				result := selected(grader, graderFields...)
				merge(result, observation)
				if digest, ok := object(grader["implementation"])["digest"].(string); ok {
					result["evaluatorDigest"] = digest
				}
				if input, ok := grader["metrics"].([]any); ok {
					metrics := []map[string]any{}
					for _, candidate := range input {
						metric := object(candidate)
						if strings.TrimSpace(text(metric["id"])) != "" && (metric["value"] == nil || finite(metric["value"])) {
							metrics = append(metrics, map[string]any{"id": metric["id"], "value": metric["value"]})
						}
					}
					result["metrics"] = metrics
				}
				results = append(results, result)
			}
			document["graders"] = map[string]any{"results": results}
		case "eval.result":
			if matches(data, identity) {
				result := selected(data, "id", "answer", "runId", "experimentId", "experiment_id", "variant")
				merge(result, observation)
				evals = append(evals, result)
				document["evals"] = evals
			}
		}
	}
	for _, result := range results {
		for name, value := range manifest[text(result["id"])] {
			if _, present := result[name]; !present {
				result[name] = value
			}
		}
	}
	return nil
}
