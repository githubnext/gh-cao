package collectnative

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Adapted from gh-aw v1.0.1 codex_logs{,_legacy}.go and claude_logs.go.
var (
	codexFrame   = regexp.MustCompile(`^\[[^\]]+\]\s+`)
	codexTrace   = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\S+\s+(?:DEBUG|INFO|WARN|ERROR)\s+\S+:\s*`)
	codexInline  = regexp.MustCompile(`(?i)^tokens\s+used[:\s]+([\d,]+)\s*$`)
	codexTotal   = regexp.MustCompile(`\btotal_tokens:\s*([\d,]+)`)
	codexResult  = regexp.MustCompile(`^(?:(.*?)\s+)?(success|succeeded|failure|failed)\s+in\s+(\d+(?:\.\d+)?)(ms|s):?\s*$`)
	codexToolOld = regexp.MustCompile(`\] tool ([^(]+)\(`)
	codexToolNew = regexp.MustCompile(`^tool ([^(]+)\(`)
	codexTool    = regexp.MustCompile(`^ToolCall:\s+([\w-]+)__([\w-]+)\s+`)
	groupedInt   = regexp.MustCompile(`^\d+(?:,\d{3})*$`)
)

func codexPayload(line string) string {
	return codexTrace.ReplaceAllString(codexFrame.ReplaceAllString(strings.TrimSpace(line), ""), "")
}

func stripCodexToolResults(ctx context.Context, content []byte, limits Limits) ([]byte, error) {
	if !utf8.Valid(content) || bytes.IndexByte(content, 0) >= 0 {
		return nil, ErrIdentity
	}
	if err := scanEvidence(ctx, content, limits, func(_ int, _ []byte) error { return nil }); err != nil {
		return nil, err
	}
	clean := bytes.Clone(content)
	for offset := 0; offset < len(clean); {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		end := bytes.IndexByte(clean[offset:], '\n')
		if end < 0 {
			break
		}
		end += offset
		line := codexPayload(string(clean[offset:end]))
		offset = end + 1
		if !codexResult.MatchString(line) {
			continue
		}
		tail := bytes.TrimSpace(clean[offset:])
		if len(tail) == 0 || (tail[0] != '{' && tail[0] != '[') {
			continue
		}
		decoder := json.NewDecoder(bytes.NewReader(clean[offset:]))
		var result json.RawMessage
		if decoder.Decode(&result) != nil {
			continue
		}
		end = offset + int(decoder.InputOffset())
		for i := offset; i < end; i++ {
			if clean[i] != '\n' && clean[i] != '\r' {
				clean[i] = ' '
			}
		}
		offset = end
	}
	return clean, nil
}

func legacyEngineMetrics(ctx context.Context, engine string, content []byte, records []map[string]any, identity Identity, limits Limits) (engineAuditMetrics, error) {
	if engine == "codex" {
		clean, err := stripCodexToolResults(ctx, content, limits)
		if err != nil {
			return engineAuditMetrics{}, err
		}
		content, records = clean, nil
		err = agentLogRecords(ctx, content, identity, limits, func(record map[string]any) error {
			records = append(records, record)
			if len(records) > limits.defaults().Entries {
				return ErrCapacity
			}
			return nil
		})
		if err != nil {
			return engineAuditMetrics{}, err
		}
	}
	metrics, err := nativeEngineMetrics(ctx, engine, records, limits)
	if err != nil {
		return engineAuditMetrics{}, err
	}
	if engine == "claude" {
		for _, record := range records {
			if record["type"] == "result" {
				return metrics, nil
			}
		}
		metrics.tokens = 0
		err := scanEvidence(ctx, content, limits, func(_ int, raw []byte) error {
			start, end := bytes.IndexByte(raw, '{'), bytes.LastIndexByte(raw, '}')
			if start < 0 || end <= start {
				return nil
			}
			var record map[string]any
			parsed := decode(raw[start:end+1], &record) == nil ||
				decode(bytes.ReplaceAll(raw[start:end+1], []byte("'"), []byte(`"`)), &record) == nil
			if !parsed {
				return nil
			}
			if !matches(record, identity) {
				return ErrIdentity
			}
			metrics.tokens = max(metrics.tokens, jsonEngineTokens(legacyNumericRecord(record)))
			if metrics.cost == 0 {
				metrics.cost = legacyEngineCost(record)
			}
			return nil
		})
		return metrics, err
	}
	if engine != "codex" {
		return metrics, nil
	}
	metrics.tools, err = engineAuditTools(ctx, engine, records, limits)
	if err != nil || json.Valid(content) {
		return metrics, err
	}
	legacy := engineAuditMetrics{tools: map[string]int{}}
	thinking, overflowed := false, false
	var snapshot *int
	var previous string
	previousLine := 0
	err = scanEvidence(ctx, content, limits, func(lineNumber int, raw []byte) error {
		if json.Valid(raw) {
			previous = ""
			return nil
		}
		line := string(raw)
		payload := codexPayload(line)
		if strings.Contains(line, "] thinking") || strings.TrimSpace(line) == "thinking" {
			if !thinking {
				legacy.turns++
				thinking = true
			}
		} else if strings.Contains(line, "] tool") || strings.Contains(line, "] exec") ||
			strings.Contains(line, "] codex") || strings.HasPrefix(payload, "tool ") || strings.HasPrefix(payload, "exec ") {
			thinking = false
		}
		name := ""
		for _, pattern := range []*regexp.Regexp{codexToolOld, codexToolNew} {
			if match := pattern.FindStringSubmatch(strings.TrimSpace(line)); len(match) > 1 {
				name = strings.TrimSpace(match[1])
				break
			}
		}
		if name == "" {
			if match := codexTool.FindStringSubmatch(payload); len(match) > 2 {
				name = match[1] + "." + match[2]
			}
		}
		if name != "" {
			legacy.tools[strings.ReplaceAll(prettyAuditTool(name), ".", "_")]++
		} else if command, ok := strings.CutPrefix(payload, "exec "); ok {
			if index := strings.LastIndex(command, " in /"); index >= 0 {
				command = command[:index]
			}
			if command = strings.TrimSpace(command); command != "" {
				legacy.tools["bash_"+shortAuditCommand(command)]++
			}
		}
		inline := codexInline.FindStringSubmatch(payload)
		total := codexTotal.FindStringSubmatch(payload)
		switch {
		case len(inline) > 1:
			if count, ok := legacyGroupedCount(inline[1]); ok && !overflowed {
				if addEngineCounter(&legacy.tokens, count) != nil {
					legacy.tokens, overflowed = 0, true
				}
			}
		case len(total) > 1 && (strings.HasPrefix(payload, "total_tokens:") || strings.Contains(payload, "TokenCount")):
			if count, ok := legacyGroupedCount(total[1]); ok {
				snapshot = &count
			}
		case lineNumber == previousLine+1 && strings.EqualFold(previous, "tokens used"):
			if count, ok := legacyGroupedCount(strings.TrimSpace(line)); ok {
				snapshot = &count
			}
		}
		previous, previousLine = payload, lineNumber
		return nil
	})
	if err != nil {
		return engineAuditMetrics{}, err
	}
	if snapshot != nil {
		legacy.tokens = *snapshot
	}
	hasTokens, hasTurns := false, false
	for _, record := range records {
		kind := text(record["type"])
		data, turn := record, "num_turns"
		if kind == "session.result" {
			data, turn = object(record["data"]), "numTurns"
		}
		if kind == "turn.completed" {
			hasTurns = true
		}
		if kind == "result" || kind == "session.result" {
			hasTurns = hasTurns || (integer(data[turn]) != nil && nonnegative(data[turn]) != nil)
		}
		if kind == "turn.completed" || kind == "result" || kind == "session.result" {
			usage := object(data["usage"])
			for key, alias := range map[string]string{"input_tokens": "inputTokens", "output_tokens": "outputTokens",
				"total_tokens": "totalTokens", "cache_read_input_tokens": "cacheReadInputTokens",
				"cache_creation_input_tokens": "cacheCreationInputTokens"} {
				value := nativeAlias(usage, key, alias)
				hasTokens = hasTokens || (integer(value) != nil && nonnegative(value) != nil)
			}
			hasTokens = hasTokens || len(array(nativeAlias(usage, "overflowed_tokens", "overflowedTokens"))) > 0
		}
	}
	if !hasTokens {
		metrics.tokens = legacy.tokens
	}
	if !hasTurns {
		metrics.turns = legacy.turns
	}
	mergeAuditTools(metrics.tools, legacy.tools)
	return metrics, nil
}

func legacyGroupedCount(value string) (int, bool) {
	if !groupedInt.MatchString(value) {
		return 0, false
	}
	count, err := strconv.Atoi(strings.ReplaceAll(value, ",", ""))
	return count, err == nil && count <= 9007199254740991
}

func legacyNumericRecord(record map[string]any) map[string]any {
	numbers := func(source map[string]any) map[string]any {
		result := map[string]any{}
		for _, key := range []string{"input_tokens", "output_tokens", "prompt_tokens", "completion_tokens",
			"cache_creation_input_tokens", "cache_read_input_tokens", "tokens", "token_count", "total_tokens"} {
			value := source[key]
			if raw, ok := value.(string); ok {
				if n, err := strconv.Atoi(raw); err == nil && n >= 0 && n <= 9007199254740991 {
					value = json.Number(raw)
				}
			}
			result[key] = value
		}
		return result
	}
	result := numbers(record)
	result["usage"] = numbers(object(record["usage"]))
	result["delta"] = map[string]any{"usage": numbers(object(object(record["delta"])["usage"]))}
	return result
}

func legacyEngineCost(record map[string]any) float64 {
	for _, source := range []map[string]any{record, object(record["billing"])} {
		for _, key := range []string{"total_cost_usd", "cost", "price", "amount", "total_cost", "estimated_cost"} {
			n, ok := number(source[key])
			if raw, isString := source[key].(string); isString {
				var err error
				n, err = strconv.ParseFloat(raw, 64)
				ok = err == nil
			}
			if ok && n > 0 && !math.IsInf(n, 0) && !math.IsNaN(n) {
				return n
			}
		}
	}
	return 0
}
