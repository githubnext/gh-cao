package collectnative

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"path"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

type engineAuditMetrics struct {
	tokens, turns int
	cost          float64
	observed      bool
	tools         map[string]int
}

func engineLogPath(name string) bool {
	if path.Base(name) == "redacted-urls.log" {
		return false
	}
	if firewallLogPath(name) {
		return false
	}
	if !engineEvidencePath(name) {
		return false
	}
	base := strings.ToLower(path.Base(name))
	return (strings.HasSuffix(base, ".log") || (strings.HasSuffix(base, ".txt") && strings.Contains(base, "log"))) &&
		!strings.Contains(base, "aw_output") && !strings.Contains(base, "agent_output") &&
		!strings.Contains(base, "access") && base != "detection.log" && base != "threat-detection.log"
}

func engineEvidencePath(name string) bool {
	if excludedAuditPath(name) {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == "detection" || strings.HasSuffix(part, "-detection") || part == "firewall" {
			return false
		}
	}
	return true
}

// Engine metrics follow pinned extractLogMetrics precedence, never runner
// stdout. Do not turn a single total into fabricated input/output/AIC counters.
func enrichEngineAudit(ctx context.Context, fields map[string]any, files map[string][]byte, identity Identity, limits Limits) ([]string, error) {
	limits = limits.defaults()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateAuditEngine(fields["engine_id"]); err != nil {
		return nil, err
	}
	if len(files) > limits.Entries {
		return nil, ErrCapacity
	}
	gaps := []string{}
	metrics := engineAuditMetrics{tools: map[string]int{}}
	events := []string{}
	for _, name := range candidates(files, "events.jsonl") {
		if engineEvidencePath(name) {
			events = append(events, name)
		}
	}
	if len(events) > 1 {
		gaps = append(gaps, "ambiguous-engine-events")
	} else if len(events) == 1 {
		next, err := copilotEventMetrics(ctx, files[events[0]], identity, limits)
		if err != nil {
			if errors.Is(err, ErrCapacity) || ctx.Err() != nil {
				return nil, err
			}
			gaps = append(gaps, "invalid-engine-events")
		} else {
			metrics = next
		}
	}
	failures := []any{}
	var fallbackContent []byte
	var fallbackRecords []map[string]any
	rawObserved := false
	totalBytes, totalRecords := 0, 0
	for _, name := range events {
		totalBytes += len(files[name])
	}
	for _, name := range slices.Sorted(maps.Keys(files)) {
		if !engineLogPath(name) {
			continue
		}
		totalBytes += len(files[name])
		if totalBytes > limits.Bytes {
			return nil, ErrCapacity
		}
		content := files[name]
		if fields["engine_id"] == "codex" {
			clean, err := stripCodexToolResults(ctx, content, limits)
			if err != nil {
				return nil, err
			}
			content = clean
		}
		records := []map[string]any{}
		err := agentLogRecords(ctx, content, identity, limits, func(record map[string]any) error {
			totalRecords++
			if totalRecords > limits.Entries {
				return ErrCapacity
			}
			records = append(records, record)
			return nil
		})
		if err != nil {
			if errors.Is(err, ErrCapacity) || ctx.Err() != nil {
				return nil, err
			}
			gaps = append(gaps, "invalid-engine-log")
			continue
		}
		stagedFailures := []any{}
		for _, record := range records {
			if record["type"] != "system" || record["subtype"] != "init" {
				continue
			}
			servers := array(record["mcp_servers"])
			totalRecords += len(servers)
			if totalRecords > limits.Entries {
				return nil, ErrCapacity
			}
			for _, candidate := range servers {
				server := object(candidate)
				if server["status"] == "failed" {
					if !auditReportName.MatchString(text(server["name"])) {
						gaps = append(gaps, "invalid-mcp-server-identifier")
						continue
					}
					failure := map[string]any{"server_name": server["name"], "status": "failed"}
					if at := eventClock(record["timestamp"]); at != "" {
						failure["timestamp"] = at
					}
					stagedFailures = append(stagedFailures, failure)
				}
			}
		}
		failures = append(failures, stagedFailures...)
		if path.Base(name) == "agent-stdio.log" && fallbackContent == nil {
			fallbackContent, fallbackRecords = content, records
		}
		if metrics.observed {
			// events.jsonl owns metrics, but startup health still comes from logs.
			continue
		}
		var next engineAuditMetrics
		switch fields["engine_id"] {
		case "copilot":
			next, err = copilotLogMetrics(ctx, files[name], records, limits)
		case "claude", "codex", "pi", "gemini":
			next, err = legacyEngineMetrics(ctx, text(fields["engine_id"]), content, records, identity, limits)
		default:
			gaps = append(gaps, "engine-identity-unavailable")
			continue
		}
		if err != nil {
			if errors.Is(err, ErrCapacity) || ctx.Err() != nil {
				return nil, err
			}
			gaps = append(gaps, "invalid-engine-log-metrics")
			continue
		}
		if next.tools == nil {
			next.tools = map[string]int{}
		}
		if fields["engine_id"] != "codex" && fields["engine_id"] != "copilot" {
			tools, toolErr := engineAuditTools(ctx, text(fields["engine_id"]), records, limits)
			if toolErr != nil {
				return nil, toolErr
			}
			mergeAuditTools(next.tools, tools)
		}
		mergeAuditTools(metrics.tools, next.tools)
		if err := addEngineCounter(&metrics.tokens, next.tokens); err != nil {
			return nil, err
		}
		metrics.turns = max(metrics.turns, next.turns)
		metrics.cost += next.cost
		rawObserved = true
		// Defer observed=true until every raw file has been aggregated.
	}
	metrics.observed = metrics.observed || rawObserved
	turnsAvailable := metrics.observed
	auditTurnsRecovered := false
	if metrics.tokens == 0 {
		usage := object(fields["token_usage_summary"])
		total := engineCounter(usage["total_input_tokens"]) + engineCounter(usage["total_output_tokens"])
		if total > 0 {
			metrics.tokens, metrics.observed = total, true
		}
	}
	if fallbackContent != nil && (metrics.tokens == 0 || metrics.turns == 0) {
		fallback, inferredEngine, err := inferNativeAuditMetrics(ctx, fallbackContent, fallbackRecords, identity, limits)
		if err != nil {
			return nil, err
		}
		if inferredEngine != "" {
			audit := object(fields["audit"])
			if audit == nil {
				audit = map[string]any{}
				fields["audit"] = audit
			}
			tokens, turns := metrics.tokens, metrics.turns
			if tokens == 0 {
				tokens = fallback.tokens
			}
			if turns == 0 {
				turns = fallback.turns
			}
			audit["metrics"] = map[string]any{"token_usage": json.Number(strconv.Itoa(tokens)), "turns": json.Number(strconv.Itoa(turns))}
			audit["inferred_engine"] = inferredEngine
			auditTurnsRecovered = turns > 0
		}
	}
	if metrics.observed {
		audit := object(fields["audit"])
		if audit == nil {
			audit = map[string]any{}
			fields["audit"] = audit
		}
		if turnsAvailable {
			audit["native_tool_types"] = engineToolTypes(fields, metrics.tools)
			audit["native_tools"] = transientAuditTools{tools: metrics.tools}
		}
		for name := range metrics.tools {
			name = strings.ToLower(prettyAuditTool(name))
			if strings.Contains(name, "github_issue_read") || strings.Contains(name, "github-discussion-query") {
				audit["native_issue_response_tool"] = true
			}
		}
		fields["token_usage"] = json.Number(strconv.Itoa(metrics.tokens))
		if turnsAvailable {
			fields["turns"] = json.Number(strconv.Itoa(metrics.turns))
		} else if !auditTurnsRecovered {
			gaps = append(gaps, "legacy-engine-fallback-unavailable")
		}
		if metrics.cost > 0 {
			fields["estimated_cost"] = metrics.cost
		}
	}
	if len(failures) > 0 {
		audit := object(fields["audit"])
		if audit == nil {
			audit = map[string]any{}
		}
		audit["mcp_failures"] = failures
		fields["audit"] = audit
	}
	return gaps, nil
}

func engineCounter(value any) int {
	n, ok := number(value)
	// Aggregated structured usage is already validated and held as float64
	// until the safe envelope is serialized; source JSON still uses Number.
	switch value := value.(type) {
	case float64:
		n, ok = value, true
	case int:
		n, ok = float64(value), true
	}
	if !ok || n < 0 || n > 9007199254740991 || n != float64(int(n)) {
		return 0
	}
	return int(n)
}

func addEngineCounter(total *int, value int) error {
	if value < 0 || value > 9007199254740991-*total {
		return ErrCapacity
	}
	*total += value
	return nil
}

func usagePair(record map[string]any, input, output string) int {
	return engineCounter(record[input]) + engineCounter(record[output])
}

func copilotEventMetrics(ctx context.Context, content []byte, identity Identity, limits Limits) (engineAuditMetrics, error) {
	result := engineAuditMetrics{tools: map[string]int{}}
	fallback, shutdown, work := 0, false, 0
	err := scanEvidence(ctx, content, limits, func(_ int, raw []byte) error {
		var record map[string]any
		if decode(raw, &record) != nil || record == nil || !matches(record, identity) || !matches(object(record["data"]), identity) {
			return errors.New("invalid engine event")
		}
		result.observed = true
		data := object(record["data"])
		usage := object(data["usage"])
		work++
		for _, counters := range []map[string]any{record, data, usage} {
			if err := validateEngineCounters(counters); err != nil {
				return err
			}
		}
		if data["usage"] != nil && usage == nil {
			return errors.New("invalid engine event usage")
		}
		input := firstNonzeroCounter(usage, "input_tokens", "prompt_tokens")
		output := firstNonzeroCounter(usage, "output_tokens", "completion_tokens")
		count := input + output
		if count == 0 {
			count = usagePair(data, "input_tokens", "output_tokens")
		}
		if count == 0 {
			count = usagePair(record, "usage_input_tokens", "usage_output_tokens")
		}
		if count == 0 {
			count = usagePair(record, "usageInputTokens", "usageOutputTokens")
		}
		if err := addEngineCounter(&fallback, count); err != nil {
			return err
		}
		switch record["type"] {
		case "tool.execution_start":
			if name := text(data["toolName"]); name != "" {
				result.tools[name]++
			}
		case "user.message":
			result.turns++
		case "session.shutdown":
			if data["modelMetrics"] != nil {
				models := object(data["modelMetrics"])
				if models == nil {
					return errors.New("invalid engine shutdown metrics")
				}
				shutdown = true
				work += len(models)
				if work > limits.defaults().Entries {
					return ErrCapacity
				}
				for _, value := range models {
					model := object(value)
					usage := object(model["usage"])
					if model == nil || (model["usage"] != nil && usage == nil) {
						return errors.New("invalid engine model usage")
					}
					if err := validateEngineCounters(usage); err != nil {
						return err
					}
					count := usagePair(usage, "inputTokens", "outputTokens")
					if err := addEngineCounter(&result.tokens, count); err != nil {
						return err
					}
				}
			}
		}
		return nil
	})
	if err != nil || !result.observed {
		if err == nil {
			err = errors.New("empty engine event stream")
		}
		return engineAuditMetrics{}, err
	}
	if !shutdown {
		result.tokens = fallback
	}
	return result, nil
}

func validateEngineCounters(record map[string]any) error {
	for _, field := range []string{"input_tokens", "output_tokens", "prompt_tokens", "completion_tokens",
		"inputTokens", "outputTokens", "cache_read_input_tokens", "cache_creation_input_tokens",
		"usage_input_tokens", "usage_output_tokens", "usageInputTokens", "usageOutputTokens", "num_turns",
		"tokens", "token_count", "total_tokens"} {
		if value := record[field]; value != nil && (integer(value) == nil || nonnegative(value) == nil) {
			return errors.New("invalid engine usage counter")
		}
	}
	return nil
}

func firstNonzeroCounter(record map[string]any, primary, alias string) int {
	if n := engineCounter(record[primary]); n != 0 {
		return n
	}
	return engineCounter(record[alias])
}

func agentLogRecords(ctx context.Context, content []byte, identity Identity, limits Limits, visit func(map[string]any) error) error {
	if len(content) > limits.defaults().Bytes {
		return ErrCapacity
	}
	if !utf8.Valid(content) || bytes.IndexByte(content, 0) >= 0 {
		return errors.New("invalid agent log encoding")
	}
	validate := func(record map[string]any) error {
		if !matches(record, identity) || !matches(object(record["data"]), identity) {
			return ErrIdentity
		}
		return visit(record)
	}
	trimmed := bytes.TrimSpace(content)
	if bytes.HasPrefix(trimmed, []byte("[")) {
		var entries []map[string]any
		if decode(trimmed, &entries) == nil {
			if len(entries) > limits.defaults().Entries {
				return ErrCapacity
			}
			for _, record := range entries {
				if err := ctx.Err(); err != nil {
					return err
				}
				if err := validate(record); err != nil {
					return err
				}
			}
			return nil
		}
	}
	if err := scanEvidence(ctx, content, limits, func(_ int, _ []byte) error { return nil }); err != nil {
		return err
	}
	for offset := 0; offset < len(content); {
		if err := ctx.Err(); err != nil {
			return err
		}
		end := bytes.IndexByte(content[offset:], '\n')
		if end < 0 {
			end = len(content)
		} else {
			end += offset
		}
		raw := bytes.TrimSpace(content[offset:end])
		start, next := offset, min(end+1, len(content))
		offset = next
		if len(raw) == 0 || (raw[0] != '{' && raw[0] != '[') {
			continue
		}
		if raw[0] == '[' && len(raw) > 1 && raw[1] != '{' && raw[1] != ']' && raw[1] != '[' && raw[1] != ' ' {
			continue
		}
		decoder := json.NewDecoder(bytes.NewReader(content[start:]))
		decoder.UseNumber()
		var value any
		if decoder.Decode(&value) != nil {
			return errors.New("invalid agent JSON log")
		}
		consumed := start + int(decoder.InputOffset())
		offset = consumed
		for offset < len(content) && content[offset] != '\n' {
			if content[offset] != ' ' && content[offset] != '\r' && content[offset] != '\t' {
				return errors.New("invalid agent JSON suffix")
			}
			offset++
		}
		entries := []any{value}
		if rows, ok := value.([]any); ok {
			entries = rows
		}
		if len(entries) > limits.defaults().Entries {
			return ErrCapacity
		}
		for _, candidate := range entries {
			record := object(candidate)
			if record == nil {
				return errors.New("invalid agent JSON record")
			}
			if err := validate(record); err != nil {
				return err
			}
		}
	}
	return nil
}

func copilotLogMetrics(ctx context.Context, content []byte, _ []map[string]any, limits Limits) (engineAuditMetrics, error) {
	metrics := engineAuditMetrics{tools: map[string]int{}}
	records := []map[string]any{}
	if err := scanEvidence(ctx, content, limits, func(_ int, raw []byte) error {
		var record map[string]any
		if decode(raw, &record) == nil && record != nil {
			records = append(records, record)
		}
		return nil
	}); err != nil {
		return metrics, err
	}
	assistants := 0
	for _, record := range records {
		switch record["type"] {
		case "assistant":
			assistants++
		case "result":
			if record["usage"] != nil && object(record["usage"]) == nil {
				return engineAuditMetrics{}, errors.New("invalid engine result usage")
			}
			if err := validateEngineCounters(record); err != nil {
				return engineAuditMetrics{}, err
			}
			if err := validateEngineCounters(object(record["usage"])); err != nil {
				return engineAuditMetrics{}, err
			}
			if object(record["usage"]) != nil {
				metrics.tokens = usagePair(object(record["usage"]), "input_tokens", "output_tokens")
				metrics.turns = engineCounter(record["num_turns"])
			}
		}
	}
	if len(records) > 0 {
		if metrics.turns == 0 {
			metrics.turns = assistants
		}
		var err error
		metrics.tools, err = engineAuditTools(ctx, "copilot", records, limits)
		return metrics, err
	}
	return copilotDebugMetrics(ctx, content, limits)
}

func jsonEngineTokens(record map[string]any) int {
	if n := usagePair(record, "input_tokens", "output_tokens"); n > 0 {
		return n
	}
	for _, key := range []string{"tokens", "token_count", "total_tokens"} {
		if n := engineCounter(record[key]); n > 0 {
			return n
		}
	}
	usage := object(record["usage"])
	n := firstNonzeroCounter(usage, "input_tokens", "prompt_tokens") +
		firstNonzeroCounter(usage, "output_tokens", "completion_tokens") +
		engineCounter(usage["cache_creation_input_tokens"]) + engineCounter(usage["cache_read_input_tokens"])
	if n > 0 {
		return n
	}
	for _, key := range []string{"tokens", "token_count", "total_tokens"} {
		if n := engineCounter(usage[key]); n > 0 {
			return n
		}
	}
	return usagePair(object(object(record["delta"])["usage"]), "input_tokens", "output_tokens")
}

func claudeLogMetrics(ctx context.Context, records []map[string]any) (engineAuditMetrics, error) {
	sessions := map[string]engineAuditMetrics{}
	hasResult, fallback := false, 0
	for _, record := range records {
		if err := ctx.Err(); err != nil {
			return engineAuditMetrics{}, err
		}
		if record["type"] == "result" {
			hasResult = true
			if text(record["parent_tool_use_id"]) != "" {
				continue
			}
			id := text(record["session_id"])
			value := sessions[id]
			usage := object(record["usage"])
			if record["usage"] != nil && usage == nil {
				return engineAuditMetrics{}, errors.New("invalid engine result usage")
			}
			if err := validateEngineCounters(record); err != nil {
				return engineAuditMetrics{}, err
			}
			if err := validateEngineCounters(usage); err != nil {
				return engineAuditMetrics{}, err
			}
			count := usagePair(usage, "input_tokens", "output_tokens") +
				usagePair(usage, "cache_creation_input_tokens", "cache_read_input_tokens")
			if count > 0 {
				value.tokens = count
			}
			if turns := engineCounter(record["num_turns"]); turns > 0 {
				value.turns = turns
			}
			if cost, ok := number(record["total_cost_usd"]); ok && cost > 0 {
				value.cost = cost
			}
			sessions[id] = value
		}
		fallback = max(fallback, jsonEngineTokens(record))
	}
	result := engineAuditMetrics{}
	for _, metrics := range sessions {
		if err := addEngineCounter(&result.tokens, metrics.tokens); err != nil {
			return engineAuditMetrics{}, err
		}
		result.turns += metrics.turns
		result.cost += metrics.cost
	}
	if !hasResult {
		result.tokens = fallback
	}
	return result, nil
}
