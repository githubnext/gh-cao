package topology

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"gopkg.in/yaml.v3"
)

type Features struct {
	Deployment      string `json:"deployment" yaml:"deployment"`
	Acquisition     string `json:"acquisition" yaml:"acquisition"`
	Ingress         string `json:"ingress" yaml:"ingress"`
	Analytics       string `json:"analytics" yaml:"analytics"`
	Telemetry       string `json:"telemetry" yaml:"telemetry"`
	SessionDataLake bool   `json:"sessionDataLake" yaml:"sessionDataLake"`
	WebhookGateway  bool   `json:"webhookGateway" yaml:"webhookGateway"`
}

type Requirements struct {
	Version  int      `json:"version" yaml:"version"`
	Backend  string   `json:"backend" yaml:"backend"`
	Features Features `json:"features" yaml:"features"`
	Azure    struct {
		Parameters    map[string]any    `json:"parameters" yaml:"parameters"`
		SecretVaultID string            `json:"secretVaultId,omitempty" yaml:"secretVaultId,omitempty"`
		Secrets       map[string]string `json:"secrets,omitempty" yaml:"secrets,omitempty"`
		Hosts         *Hosts            `json:"hosts,omitempty" yaml:"hosts,omitempty"`
	} `json:"azure" yaml:"azure"`
}

type Hosts struct {
	SSHPublicKey              string   `json:"sshPublicKey" yaml:"sshPublicKey"`
	Administrator             string   `json:"administrator,omitempty" yaml:"administrator,omitempty"`
	VMSize                    string   `json:"vmSize,omitempty" yaml:"vmSize,omitempty"`
	ArchiveAllowedSourceCIDRs []string `json:"archiveAllowedSourceCidrs,omitempty" yaml:"archiveAllowedSourceCidrs,omitempty"`
	GatewayAllowedSourceCIDRs []string `json:"gatewayAllowedSourceCidrs,omitempty" yaml:"gatewayAllowedSourceCidrs,omitempty"`
	ArchiveRepositories       []string `json:"archiveRepositories,omitempty" yaml:"archiveRepositories,omitempty"`
}

type Node struct {
	ID        string   `json:"id"`
	Kind      string   `json:"kind"`
	DependsOn []string `json:"dependsOn"`
}

type Topology struct {
	Version  int      `json:"version"`
	Backend  string   `json:"backend"`
	Features Features `json:"features"`
	Nodes    []Node   `json:"nodes"`
	Order    []string `json:"order"`
}

type ParameterDefinition struct {
	Type          string          `json:"type"`
	DefaultValue  json.RawMessage `json:"defaultValue"`
	AllowedValues []any           `json:"allowedValues"`
	MinLength     *int            `json:"minLength"`
	MaxLength     *int            `json:"maxLength"`
	MinValue      *float64        `json:"minValue"`
	MaxValue      *float64        `json:"maxValue"`
}

type Parameter struct {
	Value     any
	Reference *SecretReference
}

type SecretReference struct {
	KeyVault struct {
		ID string `json:"id"`
	} `json:"keyVault"`
	SecretName string `json:"secretName"`
}

func (p Parameter) MarshalJSON() ([]byte, error) {
	if p.Reference != nil {
		return json.Marshal(map[string]any{"reference": p.Reference})
	}
	return json.Marshal(map[string]any{"value": p.Value})
}

type Parameters struct {
	Schema         string               `json:"$schema"`
	ContentVersion string               `json:"contentVersion"`
	Parameters     map[string]Parameter `json:"parameters"`
}

func parameterDocument(values map[string]Parameter) Parameters {
	return Parameters{
		Schema:         "https://schema.management.azure.com/schemas/2019-04-01/deploymentParameters.json#",
		ContentVersion: "1.0.0.0", Parameters: values,
	}
}

func Parse(content []byte) (Requirements, error) {
	var result Requirements
	result.Features = Features{Deployment: "azure-functions", Acquisition: "actions", Ingress: "public", Analytics: "none", Telemetry: "none"}
	if len(content) > 1<<20 {
		return result, fmt.Errorf("requirements exceed 1 MiB")
	}
	var document yaml.Node
	if err := yaml.Unmarshal(content, &document); err != nil {
		return result, fmt.Errorf("invalid requirements YAML")
	}
	var inspect func(*yaml.Node) error
	inspect = func(node *yaml.Node) error {
		if node.Kind == yaml.AliasNode {
			return fmt.Errorf("YAML aliases are unsupported")
		}
		if node.Tag == "!!null" {
			return fmt.Errorf("null configuration values are unsupported")
		}
		for _, child := range node.Content {
			if err := inspect(child); err != nil {
				return err
			}
		}
		return nil
	}
	if err := inspect(&document); err != nil {
		return result, err
	}
	decoder := yaml.NewDecoder(bytes.NewReader(content))
	decoder.KnownFields(true)
	if err := decoder.Decode(&result); err != nil {
		return result, fmt.Errorf("invalid requirements fields or types; use 'gh cao deploy init' for the supported schema")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return result, fmt.Errorf("requirements must contain exactly one YAML document")
	}
	return result, nil
}
