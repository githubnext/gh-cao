package collectnative

import (
	"context"
	"fmt"
	"strings"
)

func prettyAuditTool(name string) string {
	if strings.HasPrefix(name, "mcp__") {
		parts := strings.Split(name, "__")
		if len(parts) >= 3 {
			return parts[1] + "_" + strings.Join(parts[2:], "_")
		}
		return strings.TrimPrefix(name, "mcp__")
	}
	if strings.EqualFold(name, "bash") {
		return "bash"
	}
	return name
}

func shortAuditCommand(command string) string {
	command = strings.ReplaceAll(command, "\n", " ")
	if len(command) > 20 {
		return command[:20] + "..."
	}
	return command
}

func codexAuditTool(data map[string]any) string {
	name, server := text(data["toolName"]), text(data["mcpServerName"])
	if name == "bash" {
		if command, ok := data["command"].(string); ok {
			return "bash_" + shortAuditCommand(command)
		}
		return "bash"
	}
	if server != "" && name != "" {
		name = server + "." + name
	}
	return strings.ReplaceAll(prettyAuditTool(strings.TrimSpace(name)), ".", "_")
}

// Keys are transient: Claude/Codex command-shaped tool names must not be
// retained in the evidence envelope. Only their cardinality is published.
func engineAuditTools(ctx context.Context, engine string, records []map[string]any, limits Limits) (map[string]int, error) {
	tools, items := map[string]int{}, map[string]bool{}
	work, scope := 0, 0
	add := func(name string) error {
		work++
		if work > limits.defaults().Entries {
			return ErrCapacity
		}
		if name != "" {
			tools[name]++
		}
		return nil
	}
	for index, record := range records {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		switch engine {
		case "copilot", "claude":
			if record["type"] != "assistant" {
				continue
			}
			for _, block := range array(object(record["message"])["content"]) {
				tool := object(block)
				if tool["type"] != "tool_use" {
					continue
				}
				name := text(tool["name"])
				if engine == "claude" {
					name = prettyAuditTool(name)
					if tool["name"] == "Bash" {
						if command, ok := object(tool["input"])["command"].(string); ok {
							name = "bash_" + shortAuditCommand(command)
						}
					}
				}
				if err := add(name); err != nil {
					return nil, err
				}
			}
		case "gemini":
			for name := range object(object(record["stats"])["tools"]) {
				if err := add(name); err != nil {
					return nil, err
				}
			}
		case "pi":
			switch record["type"] {
			case "tool_use":
				if err := add(text(record["name"])); err != nil {
					return nil, err
				}
			case "tool_execution_start":
				id := scalar(record["toolCallId"])
				if !items[id] && text(record["toolName"]) != "" {
					items[id] = true
					if err := add(text(record["toolName"])); err != nil {
						return nil, err
					}
				}
			}
		case "codex":
			kind := text(record["type"])
			if kind == "thread.started" || kind == "session.init" {
				scope++
			}
			data := object(record["data"])
			id := fmt.Sprintf("%d:%d", scope, index)
			switch {
			case strings.HasPrefix(kind, "item."):
				item := object(record["item"])
				if value := item["id"]; value != nil {
					id = fmt.Sprintf("%d:%v", scope, value)
				}
				switch item["type"] {
				case "command_execution":
					if _, present := item["command"]; !present {
						continue
					}
					data = map[string]any{"toolName": "bash", "command": item["command"]}
				case "mcp_tool_call":
					if _, present := item["arguments"]; !present {
						continue
					}
					data = map[string]any{"toolName": item["tool"], "mcpServerName": item["server"]}
				default:
					continue
				}
			case kind == "tool.execution_start":
				if value := data["toolCallId"]; value != nil {
					id = fmt.Sprintf("%d:%v", scope, value)
				}
			default:
				continue
			}
			if !items[id] {
				items[id] = true
				if err := add(codexAuditTool(data)); err != nil {
					return nil, err
				}
			}
		}
	}
	return tools, nil
}

func mergeAuditTools(to map[string]int, from map[string]int) {
	for name, count := range from {
		to[name] += count
	}
}

func engineToolTypes(fields map[string]any, tools map[string]int) int {
	return len(mergedAuditTools(fields, tools))
}
