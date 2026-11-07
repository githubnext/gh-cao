package collectnative

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// Accounting state machines adapted from github/gh-aw v1.0.1
// pkg/workflow/{codex_logs_jsonl,pi_logs_metrics,stats_jsonl_logs}.go.
// Only counters survive; message identities are bounded ephemeral digests.
func nativeEngineMetrics(ctx context.Context, engine string, records []map[string]any, limits Limits) (engineAuditMetrics, error) {
	switch engine {
	case "claude":
		return claudeLogMetrics(ctx, records)
	case "codex":
		return codexNativeMetrics(ctx, records)
	case "pi":
		return piNativeMetrics(ctx, records, limits)
	case "gemini":
		result := engineAuditMetrics{}
		work := 0
		for _, record := range records {
			if err := ctx.Err(); err != nil {
				return engineAuditMetrics{}, err
			}
			if text(record["response"]) != "" {
				result.turns = 1
			}
			for _, value := range object(object(record["stats"])["models"]) {
				work++
				if work > limits.defaults().Entries {
					return engineAuditMetrics{}, ErrCapacity
				}
				if err := addEngineCounter(&result.tokens, usagePair(object(value), "input_tokens", "output_tokens")); err != nil {
					return engineAuditMetrics{}, err
				}
			}
		}
		return result, nil
	default:
		return engineAuditMetrics{}, ErrUnsupported
	}
}

func codexNativeMetrics(ctx context.Context, records []map[string]any) (engineAuditMetrics, error) {
	result := engineAuditMetrics{}
	usage := map[string]any{}
	seen := map[string]bool{}
	scope := 0
	for index, record := range records {
		if err := ctx.Err(); err != nil {
			return engineAuditMetrics{}, err
		}
		kind := text(record["type"])
		incremental := false
		data := record
		switch kind {
		case "thread.started", "session.init":
			scope++
			continue
		case "turn.completed":
			key := fmt.Sprintf("%d:%d", scope, index)
			if id := first(record["turn_id"], record["id"]); id != nil {
				encoded, _ := json.Marshal(id)
				key = fmt.Sprintf("%d:%s", scope, encoded)
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			result.turns++
			incremental = true
		case "result", "session.result":
			field := "num_turns"
			if kind == "session.result" {
				data, field = object(record["data"]), "numTurns"
			}
			if integer(data[field]) != nil && nonnegative(data[field]) != nil {
				result.turns = engineCounter(data[field])
			}
			if kind == "result" {
				result.cost = legacyEngineCost(data)
			} else if cost, ok := number(data["totalCostUsd"]); ok && cost >= 0 {
				result.cost = cost
			}
		default:
			continue
		}
		report := object(data["usage"])
		overflow := map[string]bool{}
		aliases := map[string]string{"input_tokens": "inputTokens", "output_tokens": "outputTokens",
			"total_tokens": "totalTokens", "cache_read_input_tokens": "cacheReadInputTokens",
			"cache_creation_input_tokens": "cacheCreationInputTokens"}
		for _, value := range array(nativeAlias(report, "overflowed_tokens", "overflowedTokens")) {
			for key, alias := range aliases {
				if value == key || value == alias {
					delete(usage, key)
					overflow[key] = true
				}
			}
		}
		for key, alias := range aliases {
			value := nativeAlias(report, key, alias)
			if overflow[key] || integer(value) == nil || nonnegative(value) == nil {
				continue
			}
			count := engineCounter(value)
			if incremental {
				previous := engineCounter(usage[key])
				if err := addEngineCounter(&previous, count); err != nil {
					delete(usage, key)
					continue
				}
				count = previous
			}
			usage[key] = json.Number(strconv.Itoa(count))
		}
		if include, ok := nativeAlias(report, "input_tokens_include_cache", "inputTokensIncludeCache").(bool); ok {
			usage["input_tokens_include_cache"] = include
		}

		if integer(usage["total_tokens"]) != nil {
			result.tokens = engineCounter(usage["total_tokens"])
		} else {
			result.tokens = usagePair(usage, "input_tokens", "output_tokens")
			if usage["input_tokens_include_cache"] == false {
				result.tokens += usagePair(usage, "cache_read_input_tokens", "cache_creation_input_tokens")
			}
		}
	}
	return result, nil
}

func nativeAlias(record map[string]any, key, alias string) any {
	if value, present := record[key]; present {
		return value
	}
	return record[alias]
}

func inferNativeAuditMetrics(ctx context.Context, content []byte, records []map[string]any, identity Identity, limits Limits) (engineAuditMetrics, string, error) {
	best, engine, score := engineAuditMetrics{}, "", -1
	// The pinned global registry returns IDs in sorted order. Equal scores
	// retain the first parser; turns dominate tokens, then tool-map breadth.
	for _, candidate := range []string{"claude", "codex", "copilot", "gemini", "pi"} {
		var metrics engineAuditMetrics
		var err error
		if candidate == "copilot" {
			metrics, err = copilotLogMetrics(ctx, content, records, limits)
		} else {
			metrics, err = legacyEngineMetrics(ctx, candidate, content, records, identity, limits)
		}
		if err != nil {
			return engineAuditMetrics{}, "", err
		}
		if metrics.tools == nil {
			metrics.tools = map[string]int{}
		}
		if candidate != "codex" && candidate != "copilot" {
			tools, err := engineAuditTools(ctx, candidate, records, limits)
			if err != nil {
				return engineAuditMetrics{}, "", err
			}
			mergeAuditTools(metrics.tools, tools)
		}
		nextScore := metrics.tokens + metrics.turns*100000 + len(metrics.tools)*1000
		if nextScore > score {
			best, engine, score = metrics, candidate, nextScore
		}
	}
	if best.tokens == 0 && best.turns == 0 && len(best.tools) == 0 {
		return engineAuditMetrics{}, "", nil
	}
	return best, engine, nil
}

func piNativeMetrics(ctx context.Context, records []map[string]any, limits Limits) (engineAuditMetrics, error) {
	result := engineAuditMetrics{}
	tokens := map[[32]byte]int{}
	costs := map[[32]byte]float64{}
	turn, compactions, work := 0, 0, 0
	recordUsage := func(key string, usage map[string]any) error {
		work++
		if work > limits.defaults().Entries {
			return ErrCapacity
		}
		digest := sha256.Sum256([]byte(key))
		count := piUsageTokens(usage)
		result.tokens += count - tokens[digest]
		tokens[digest] = count
		if cost, ok := number(object(usage["cost"])["total"]); ok && cost >= 0 && !math.IsInf(cost, 0) {
			result.cost += cost - costs[digest]
			costs[digest] = cost
		}
		if result.tokens < 0 || result.tokens > 9007199254740991 {
			return ErrCapacity
		}
		return nil
	}
	message := func(record map[string]any) error {
		role := text(record["role"])
		if role != "assistant" && role != "toolResult" {
			return nil
		}
		key := ""
		if role == "toolResult" {
			key = "tool:" + fmt.Sprint(record["toolCallId"])
		} else {
			for _, field := range []string{"id", "responseId", "timestamp"} {
				if value, present := record[field]; present {
					key = "assistant:" + field + ":" + fmt.Sprint(value)
					break
				}
			}
			if key == "" {
				content, _ := json.Marshal(record["content"])
				key = fmt.Sprintf("assistant:turn:%d:%s", turn, content)
			}
			if _, seen := tokens[sha256.Sum256([]byte(key))]; !seen {
				result.turns++
			}
		}
		return recordUsage(key, object(record["usage"]))
	}
	for _, record := range records {
		if err := ctx.Err(); err != nil {
			return engineAuditMetrics{}, err
		}
		var err error
		switch record["type"] {
		case "turn_start":
			turn++
		case "message_end", "turn_end":
			err = message(object(record["message"]))
		case "agent_end":
			for _, candidate := range array(record["messages"]) {
				if err = message(object(candidate)); err != nil {
					break
				}
			}
		case "compaction_end":
			err = recordUsage(fmt.Sprintf("compaction:%d", compactions), object(object(record["result"])["usage"]))
			compactions++
		case "entry_appended":
			entry := object(record["entry"])
			if entry["type"] == "usage" {
				err = recordUsage(fmt.Sprintf("usage:%v", entry["id"]), object(entry["usage"]))
			}
		case "assistant":
			if record["delta"] != true && strings.TrimSpace(text(record["content"])) != "" {
				result.turns++
			}
		case "result":
			err = addEngineCounter(&result.tokens, piUsageTokens(object(record["stats"])))
		}
		if err != nil {
			return engineAuditMetrics{}, err
		}
	}
	return result, nil
}

func piUsageTokens(usage map[string]any) int {
	for _, field := range []string{"totalTokens", "total_tokens"} {
		if integer(usage[field]) != nil && nonnegative(usage[field]) != nil {
			return engineCounter(usage[field])
		}
	}
	total := 0
	for _, fields := range [][2]string{{"input", "input_tokens"}, {"output", "output_tokens"},
		{"cacheRead", "cache_read_input_tokens"}, {"cacheWrite", "cache_creation_input_tokens"}} {
		for _, field := range fields {
			if integer(usage[field]) != nil && nonnegative(usage[field]) != nil {
				total += engineCounter(usage[field])
				break
			}
		}
	}
	return total
}
