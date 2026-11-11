package topology

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

func azureParameterDefinitions() (map[string]ParameterDefinition, error) {
	content, err := AzureTemplate()
	if err != nil {
		return nil, err
	}
	var document struct {
		Parameters map[string]ParameterDefinition `json:"parameters"`
	}
	if err := json.Unmarshal(content, &document); err != nil {
		return nil, fmt.Errorf("read embedded Azure parameter contract: %w", err)
	}
	return document.Parameters, nil
}

// ResolveAzure validates the native deployment profile against the embedded
// provider contract and resolves its dependencies. Secret names declare externally
// resolved inputs; no secret values or credential acquisition enter the engine.
func ResolveAzure(ctx context.Context, features Features, values map[string]any, secrets []string) (Topology, error) {
	if err := features.validate(); err != nil {
		return Topology{}, err
	}
	if features.Deployment != "azure-functions" || features.Acquisition != "actions" || features.Ingress != "public" ||
		features.Analytics != "none" || features.SessionDataLake || features.WebhookGateway {
		return Topology{}, fmt.Errorf("native Azure deployment supports only public azure-functions with Actions snapshots and optional OTLP telemetry; use deploy generate for other profiles")
	}
	definitions, err := azureParameterDefinitions()
	if err != nil {
		return Topology{}, err
	}
	parameters := map[string]Parameter{}
	for name, value := range values {
		definition, exists := definitions[name]
		if !exists || strings.HasPrefix(strings.ToLower(definition.Type), "secure") {
			return Topology{}, fmt.Errorf("unknown or secret Azure parameter: %s", name)
		}
		parameters[name] = Parameter{Value: value}
	}
	_, result, err := resolveAzureParameters(ctx, features, parameters, definitions, secrets)
	return result, err
}

func resolveAzureParameters(ctx context.Context, features Features, parameters map[string]Parameter, definitions map[string]ParameterDefinition, secrets []string) ([]byte, Topology, error) {
	for _, name := range secrets {
		if !strings.EqualFold(definitions[name].Type, "securestring") {
			return nil, Topology{}, fmt.Errorf("unknown or non-secret Azure secret: %s", name)
		}
	}
	for name, definition := range definitions {
		parameter, exists := parameters[name]
		if definition.DefaultValue == nil && !exists && !slices.Contains(secrets, name) {
			return nil, Topology{}, fmt.Errorf("azure dependency missing: %s", name)
		}
		if exists && parameter.Reference == nil {
			if err := validateParameter(name, parameter.Value, definition); err != nil {
				return nil, Topology{}, err
			}
		}
	}
	source, err := providerServiceTemplate(features, parameters, definitions)
	if err != nil {
		return nil, Topology{}, err
	}
	result, err := resolveTOSCA(ctx, source, features)
	return source, result, err
}
