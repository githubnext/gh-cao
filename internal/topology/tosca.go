package topology

import (
	"context"
	"fmt"
	"slices"
	"strings"

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

type toscaDocument struct {
	DefinitionsVersion string                   `yaml:"tosca_definitions_version"`
	NodeTypes          map[string]toscaNodeType `yaml:"node_types"`
	TopologyTemplate   struct {
		NodeTemplates map[string]toscaNodeTemplate `yaml:"node_templates"`
	} `yaml:"topology_template"`
}

type toscaNodeType struct {
	DerivedFrom  string                                  `yaml:"derived_from"`
	Requirements []map[string]toscaRequirementDefinition `yaml:"requirements"`
}

type toscaRequirementDefinition struct {
	Capability   string `yaml:"capability"`
	Node         string `yaml:"node"`
	Relationship string `yaml:"relationship"`
	Occurrences  []int  `yaml:"occurrences"`
}

type toscaNodeTemplate struct {
	Type       string `yaml:"type"`
	Properties struct {
		ConfigurationKind string `yaml:"configuration_kind"`
	} `yaml:"properties"`
	Requirements []map[string]toscaRequirementAssignment `yaml:"requirements"`
}

type toscaRequirementAssignment struct {
	Node string `yaml:"node"`
}

func resolveTopology(ctx context.Context, content []byte, features Features) (result Topology, err error) {
	result = Topology{Version: 1, Backend: "azure", Features: features, Nodes: []Node{}}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	var document toscaDocument
	if err := yaml.Unmarshal(content, &document); err != nil {
		return result, fmt.Errorf("parse TOSCA topology: %w", err)
	}
	if document.DefinitionsVersion != "tosca_simple_yaml_1_3" || len(document.TopologyTemplate.NodeTemplates) == 0 {
		return result, fmt.Errorf("unsupported or empty TOSCA topology")
	}
	for id, template := range document.TopologyTemplate.NodeTemplates {
		nodeType, exists := document.NodeTypes[template.Type]
		if !exists {
			return result, fmt.Errorf("TOSCA node %s has unknown type %s", id, template.Type)
		}
		if nodeType.DerivedFrom != "tosca.nodes.Root" {
			return result, fmt.Errorf("TOSCA node type %s has unsupported parent", template.Type)
		}
		requirements := map[string]toscaRequirementDefinition{}
		for _, definitions := range nodeType.Requirements {
			if len(definitions) != 1 {
				return result, fmt.Errorf("TOSCA node type %s has malformed requirement definition", template.Type)
			}
			for name, definition := range definitions {
				if _, exists := requirements[name]; exists {
					return result, fmt.Errorf("TOSCA node type %s has duplicate requirement %s", template.Type, name)
				}
				if definition.Relationship != "tosca.relationships.DependsOn" ||
					definition.Capability != "tosca.capabilities.Node" ||
					len(definition.Occurrences) != 2 || definition.Occurrences[0] != 1 || definition.Occurrences[1] != 1 {
					return result, fmt.Errorf("TOSCA node type %s has unsupported requirement %s", template.Type, name)
				}
				requirements[name] = definition
			}
		}
		node := Node{ID: id, Kind: template.Properties.ConfigurationKind, DependsOn: []string{}}
		assigned := map[string]bool{}
		for _, assignments := range template.Requirements {
			if len(assignments) != 1 {
				return result, fmt.Errorf("TOSCA node %s has malformed requirement assignment", id)
			}
			for name, assignment := range assignments {
				definition, exists := requirements[name]
				if !exists || assigned[name] || assignment.Node == "" {
					return result, fmt.Errorf("TOSCA node %s has invalid requirement assignment %s", id, name)
				}
				target, exists := document.TopologyTemplate.NodeTemplates[assignment.Node]
				if !exists || target.Type != definition.Node {
					return result, fmt.Errorf("TOSCA node %s requirement %s has an unresolved or incompatible target", id, name)
				}
				assigned[name] = true
				node.DependsOn = append(node.DependsOn, assignment.Node)
			}
		}
		if len(assigned) != len(requirements) {
			return result, fmt.Errorf("TOSCA node %s is missing required assignments", id)
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
	if err := ctx.Err(); err != nil {
		return result, err
	}
	return result, nil
}
