package collectnative

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
)

// Pinned copilot_logs.go retains output-only wire stubs across the repeated
// conversation history. Sizes are transient and used only to avoid recounting.
func copilotDebugMetrics(ctx context.Context, content []byte, limits Limits) (engineAuditMetrics, error) {
	result := engineAuditMetrics{tools: map[string]int{}}
	type tool struct{ count, input, output int }
	tools := map[string]*tool{}
	get := func(name string) *tool {
		if tools[name] == nil {
			tools[name] = &tool{}
		}
		return tools[name]
	}
	var block strings.Builder
	kind, work := "", 0
	step := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		work++
		if work > limits.defaults().Entries {
			return ErrCapacity
		}
		return nil
	}
	flush := func() error {
		current := kind
		kind = ""
		if block.Len() == 0 {
			return nil
		}
		raw := block.String()
		block.Reset()
		start := strings.IndexByte(raw, '{')
		if start < 0 {
			return errors.New("invalid engine debug block")
		}
		var data map[string]any
		decoder := json.NewDecoder(strings.NewReader(raw[start:]))
		decoder.UseNumber()
		if decoder.Decode(&data) != nil || data == nil {
			return errors.New("invalid engine debug block")
		}
		if current == "wire" {
			inputs := array(data["input"])
			names := map[string]string{}
			for _, value := range inputs {
				if err := step(); err != nil {
					return err
				}
				item := object(value)
				if item["type"] == "function_call" && text(item["call_id"]) != "" && text(item["name"]) != "" {
					names[text(item["call_id"])] = text(item["name"])
				}
			}
			for _, value := range inputs {
				if err := step(); err != nil {
					return err
				}
				item := object(value)
				name, output := names[text(item["call_id"])], text(item["output"])
				if item["type"] == "function_call_output" && name != "" && output != "" {
					row := get(name)
					if row.count == 0 {
						row.count = 1
					}
					row.output = max(row.output, len(output))
				}
			}
			return nil
		}
		if err := validateEngineCounters(data); err != nil {
			return err
		}
		if err := validateEngineCounters(object(data["usage"])); err != nil {
			return err
		}
		if err := addEngineCounter(&result.tokens, jsonEngineTokens(legacyNumericRecord(data))); err != nil {
			return err
		}
		result.cost += legacyEngineCost(data)
		if math.IsInf(result.cost, 0) {
			return ErrCapacity
		}
		messages := []any{data["message"]}
		for _, value := range array(data["choices"]) {
			if err := step(); err != nil {
				return err
			}
			messages = append(messages, object(value)["message"])
		}
		for _, value := range messages {
			for _, value := range array(object(value)["tool_calls"]) {
				if err := step(); err != nil {
					return err
				}
				function := object(object(value)["function"])
				if name := text(function["name"]); name != "" {
					row := get(name)
					if row.count != 1 || row.input != 0 || row.output == 0 {
						row.count++
					}
					row.input = max(row.input, len(text(function["arguments"])))
				}
			}
		}
		return nil
	}
	err := scanEvidence(ctx, content, limits, func(_ int, raw []byte) error {
		line := string(raw)
		clock, message, timestamped := strings.Cut(strings.TrimLeft(line, " \t"), " ")
		timestamped = timestamped && strings.Contains(clock, "T") && strings.Contains(clock, ":")
		if timestamped && strings.HasPrefix(message, "[DEBUG] data:") {
			if kind == "wire" {
				if err := flush(); err != nil {
					return err
				}
			}
			kind = "data"
			block.Reset()
			result.turns++
			return nil
		}
		if timestamped && strings.HasPrefix(message, "[DEBUG] Wire request:") {
			if err := flush(); err != nil {
				return err
			}
			kind = "wire"
			if index := strings.IndexByte(line, '{'); index >= 0 {
				block.WriteString(line[index:])
				block.WriteByte('\n')
			}
			return nil
		}
		switch kind {
		case "data":
			value := line
			if _, after, ok := strings.Cut(line, "[DEBUG]"); ok {
				value = strings.TrimSpace(after)
				if value == "" || !strings.ContainsAny(value[:1], "{}[]\"") {
					if err := flush(); err != nil {
						return err
					}
					value = ""
				}
			}
			if kind == "data" && value != "" {
				block.WriteString(value)
				block.WriteByte('\n')
			}
		case "wire":
			if timestamped && (strings.HasPrefix(message, "[DEBUG]") || strings.HasPrefix(message, "[INFO]")) {
				if err := flush(); err != nil {
					return err
				}
			} else {
				block.WriteString(line)
				block.WriteByte('\n')
			}
		}
		if _, name, found := strings.Cut(line, "Executing tool:"); found && strings.TrimSpace(name) != "" {
			if err := step(); err != nil {
				return err
			}
			get(strings.TrimSpace(strings.Split(name, "Executing tool:")[0])).count++
		}
		return nil
	})
	if err == nil {
		err = flush()
	}
	if err != nil {
		return engineAuditMetrics{}, err
	}
	for name, row := range tools {
		result.tools[name] = row.count
	}
	return result, nil
}
