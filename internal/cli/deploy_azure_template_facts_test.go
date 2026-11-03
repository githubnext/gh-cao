package cli

import (
	"os"
	"slices"
	"strings"
	"testing"
)

func TestDeployAzurePreflightDoesNotRequireKusto(t *testing.T) {
	for _, kusto := range []string{
		"",
		`,{"namespace":"Microsoft.Kusto","registrationState":"NotRegistered","resourceTypes":[{"resourceType":"clusters","locations":["East US"]}]}`,
	} {
		name := "missing"
		if kusto != "" {
			name = "unregistered and unavailable in region"
		}
		t.Run(name, func(t *testing.T) {
			providers := testDeployProviders(t, "Registered", "West US 3")
			providers = strings.TrimSuffix(providers, "]") + kusto + "]"
			fake := &fakeAz{t: t, overrides: map[string]fakeAzResponse{
				"provider list":                {stdout: providers},
				"rest Microsoft.Authorization": {stdout: `{"value":[{"actions":["*"],"notActions":["Microsoft.Kusto/*"]}]}`},
			}}
			report, output, err := runDeployPreflight(t, fake, testDeployConfig())
			if err != nil {
				t.Fatalf("disabled ADX must not block deployment: %v\n%s", err, output)
			}
			for _, id := range []string{"resource-providers", "permissions", "location"} {
				check := findDeployCheck(t, report, id)
				if check.Status != deployAzureCheckPass || strings.Contains(check.Message, "Microsoft.Kusto") {
					t.Fatalf("%s includes disabled ADX prerequisites: %+v", id, check)
				}
			}
			assertDeployCallsReadOnly(t, fake.calls)
		})
	}
}

func TestDeployAzureTemplateFactsADXOptIn(t *testing.T) {
	content, err := os.ReadFile(testDeployTemplatePath(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, enabled := range []bool{false, true} {
		facts := deployAzureTemplateFactsFrom(content, map[string]any{"adxEnabled": enabled})
		for _, resourceType := range []string{"Microsoft.Kusto/clusters", "Microsoft.Kusto/clusters/databases", "Microsoft.Network/privateEndpoints"} {
			if slices.Contains(facts.types, resourceType) != enabled {
				t.Fatalf("adxEnabled=%t: resource type %s in %q", enabled, resourceType, facts.types)
			}
		}
		if got := slices.Contains((&deployAzurePreflight{template: facts}).requiredProviders(), "Microsoft.Kusto"); got != enabled {
			t.Fatalf("adxEnabled=%t: Kusto provider required=%t", enabled, got)
		}
	}
}

func TestDeployAzureTemplateFactsConditions(t *testing.T) {
	for _, tt := range []struct {
		name      string
		condition any
		supplied  map[string]any
		want      bool
	}{
		{name: "unconditional", want: true},
		{name: "literal false", condition: false},
		{name: "literal true", condition: true, want: true},
		{name: "default false", condition: "[parameters('enabled')]"},
		{name: "supplied true", condition: "[parameters('enabled')]", supplied: map[string]any{"enabled": true}, want: true},
		{name: "unresolved parameter", condition: "[parameters('unknown')]", want: true},
		{name: "unsupported expression", condition: "[not(parameters('enabled'))]", want: true},
		{name: "string is not boolean", condition: "false", want: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			resource := map[string]any{"type": "Microsoft.Kusto/clusters", "condition": tt.condition}
			document := map[string]any{
				"parameters": map[string]any{"enabled": map[string]any{"type": "bool", "defaultValue": false}},
				"resources":  []any{resource},
			}
			facts := deployAzureTemplateFactsFrom(mustJSON(t, document), tt.supplied)
			if got := slices.Contains(facts.types, "Microsoft.Kusto/clusters"); got != tt.want {
				t.Fatalf("Kusto included=%t, want %t", got, tt.want)
			}
		})
	}
}

func TestDeployAzureTemplateFactsNestedParameters(t *testing.T) {
	for _, tt := range []struct {
		name   string
		passed map[string]any
		want   bool
	}{
		{name: "inner default differs from outer"},
		{name: "outer value passed to inner", passed: map[string]any{"enabled": map[string]any{"value": "[parameters('enabled')]"}}, want: true},
		{name: "explicit false overrides outer true", passed: map[string]any{"enabled": map[string]any{"value": false}}},
		{name: "unresolved argument does not use inner default", passed: map[string]any{"enabled": map[string]any{"value": "[variables('unknown')]"}}, want: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			content := mustJSON(t, map[string]any{
				"parameters": map[string]any{"enabled": map[string]any{"defaultValue": true}},
				"resources": map[string]any{"nested": map[string]any{
					"type": "Microsoft.Resources/deployments",
					"properties": map[string]any{
						"parameters": tt.passed,
						"template": map[string]any{
							"parameters": map[string]any{"enabled": map[string]any{"defaultValue": false}},
							"resources": map[string]any{"cluster": map[string]any{
								"type": "Microsoft.Kusto/clusters", "condition": "[parameters('enabled')]",
							}},
						},
					},
				}},
			})
			facts := deployAzureTemplateFactsFrom(content, nil)
			if got := slices.Contains(facts.types, "Microsoft.Kusto/clusters"); got != tt.want {
				t.Fatalf("Kusto included=%t, want %t", got, tt.want)
			}
		})
	}
}
