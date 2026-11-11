package topology

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/tliron/exturl"
	"github.com/tliron/puccini/clout/js"
	"github.com/tliron/puccini/tosca/parser"
	"gopkg.in/yaml.v3"
)

func typeName(id string) string {
	return "cao.nodes." + strings.ReplaceAll(id, "-", "_")
}

func serviceTemplate(features Features) ([]byte, error) {
	return providerServiceTemplate(features, nil, nil)
}

func providerServiceTemplate(features Features, parameters map[string]Parameter, definitions map[string]ParameterDefinition) ([]byte, error) {
	types := map[string]any{}
	templates := map[string]any{}
	for _, id := range componentIDs(features) {
		requirements := []any{}
		assignments := []any{}
		for _, dependency := range dependencies(id, features) {
			requirementName := strings.ReplaceAll(dependency, "-", "_")
			requirements = append(requirements, map[string]any{requirementName: map[string]any{
				"capability":   "tosca.capabilities.Node",
				"node":         typeName(dependency),
				"relationship": "tosca.relationships.DependsOn",
				"occurrences":  []int{1, 1},
			}})
			assignments = append(assignments, map[string]any{requirementName: map[string]any{"node": dependency}})
		}
		kind := "managed"
		if external[id] {
			kind = "external"
		}
		types[typeName(id)] = map[string]any{
			"derived_from": "tosca.nodes.Root",
			"requirements": requirements,
			"properties": map[string]any{
				"configuration_kind": map[string]any{
					"type": "string", "constraints": []any{map[string]any{"valid_values": []string{kind}}},
				},
			},
		}
		templates[id] = map[string]any{
			"type":         typeName(id),
			"properties":   map[string]any{"configuration_kind": kind},
			"requirements": assignments,
		}
	}
	dashboardType := types[typeName("dashboard")].(map[string]any)
	dashboard := templates["dashboard"].(map[string]any)
	for name, parameter := range parameters {
		if parameter.Reference != nil {
			continue
		}
		definition := definitions[name]
		schema := map[string]any{}
		switch strings.ToLower(definition.Type) {
		case "string":
			schema["type"] = "string"
		case "int":
			schema["type"] = "integer"
		case "bool":
			schema["type"] = "boolean"
		case "array":
			if _, ok := list(parameter.Value); !ok {
				// Structured Azure access rules remain the provider adapter's contract.
				continue
			}
			schema["type"] = "list"
			schema["entry_schema"] = map[string]any{"type": "string"}
		default:
			continue
		}
		constraints := []any{}
		if len(definition.AllowedValues) > 0 {
			constraints = append(constraints, map[string]any{"valid_values": definition.AllowedValues})
		}
		if definition.MinLength != nil {
			constraints = append(constraints, map[string]any{"min_length": *definition.MinLength})
		}
		if definition.MaxLength != nil {
			constraints = append(constraints, map[string]any{"max_length": *definition.MaxLength})
		}
		if definition.MinValue != nil {
			constraints = append(constraints, map[string]any{"greater_or_equal": *definition.MinValue})
		}
		if definition.MaxValue != nil {
			constraints = append(constraints, map[string]any{"less_or_equal": *definition.MaxValue})
		}
		if len(constraints) > 0 {
			schema["constraints"] = constraints
		}
		property := "azure_" + name
		dashboardType["properties"].(map[string]any)[property] = schema
		dashboard["properties"].(map[string]any)[property] = parameter.Value
	}
	return yaml.Marshal(map[string]any{
		"tosca_definitions_version": "tosca_simple_yaml_1_3",
		"description":               "CAO review-only configuration. DependsOn relationships order bootstrap; external prerequisites are unverified.",
		"metadata": map[string]string{
			"agent_mcp_contract":   "AGENT-MCP.md",
			"agent_mcp_activation": "documented-not-enabled",
		},
		"node_types":        types,
		"topology_template": map[string]any{"node_templates": templates},
	})
}

type engineMetadata struct {
	Puccini struct {
		Kind string `json:"kind"`
	} `json:"puccini"`
}

type engineVertex struct {
	Metadata   engineMetadata `json:"metadata"`
	Properties struct {
		Name string `json:"name"`
	} `json:"properties"`
	EdgesOut []struct {
		Metadata engineMetadata `json:"metadata"`
		TargetID string         `json:"targetID"`
	} `json:"edgesOut"`
}

func resolveTOSCA(ctx context.Context, content []byte, features Features) (result Topology, err error) {
	result = Topology{Version: 1, Backend: "azure", Features: features, Nodes: []Node{}}
	urlContext := exturl.NewContext()
	defer func() { err = errors.Join(err, urlContext.Release()) }()
	// Only generated, import-free templates enter the engine. Built-in profiles
	// and scriptlets stay inside the Go process; there is no Node.js or user code.
	source := urlContext.NewInternalURL("/cao/topology.yaml")
	source.SetContent(content)
	parserContext := parser.NewParser().NewContext()
	parserContext.URL = source
	normalized, err := parserContext.Parse(ctx)
	if err != nil {
		return result, fmt.Errorf("TOSCA validation failed: %w; %v", err, parserContext.GetProblems())
	}
	clout, err := normalized.Compile()
	if err != nil {
		return result, fmt.Errorf("compile TOSCA: %w", err)
	}
	execution := js.ExecContext{Clout: clout, Problems: parserContext.GetProblems(), URLContext: urlContext, Strict: true, History: false}
	execution.Resolve()
	if !execution.Problems.Empty() {
		return result, fmt.Errorf("TOSCA requirement resolution failed: %v", execution.Problems)
	}
	execution.Coerce()
	if !execution.Problems.Empty() {
		return result, fmt.Errorf("TOSCA constraint validation failed: %v", execution.Problems)
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	content, err = json.Marshal(clout)
	if err != nil {
		return result, err
	}
	var graph struct {
		Vertexes map[string]engineVertex `json:"vertexes"`
	}
	if err := json.Unmarshal(content, &graph); err != nil {
		return result, err
	}
	for _, vertex := range graph.Vertexes {
		if vertex.Metadata.Puccini.Kind != "NodeTemplate" {
			continue
		}
		node := Node{ID: vertex.Properties.Name, Kind: "managed", DependsOn: []string{}}
		if external[node.ID] {
			node.Kind = "external"
		}
		for _, edge := range vertex.EdgesOut {
			if edge.Metadata.Puccini.Kind != "Relationship" {
				continue
			}
			target, exists := graph.Vertexes[edge.TargetID]
			if !exists || target.Properties.Name == "" {
				return result, fmt.Errorf("TOSCA engine returned an unresolved relationship")
			}
			node.DependsOn = append(node.DependsOn, target.Properties.Name)
		}
		slices.Sort(node.DependsOn)
		result.Nodes = append(result.Nodes, node)
	}
	slices.SortFunc(result.Nodes, func(a, b Node) int { return strings.Compare(a.ID, b.ID) })
	result.Order, err = dependencyOrder(result.Nodes)
	if err != nil {
		return result, err
	}
	if err := CheckTopology(result); err != nil {
		return result, err
	}
	return result, nil
}
