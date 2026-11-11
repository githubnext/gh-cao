package topology

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func TestResolveAzureUsesBundleDependencyEngine(t *testing.T) {
	input := exampleRequirements(t)
	for _, telemetry := range []string{"none", "otlp"} {
		t.Run(telemetry, func(t *testing.T) {
			input.Features.Telemetry = telemetry
			if telemetry == "otlp" {
				input.Azure.Parameters["otlpEndpoint"] = "https://collector.example.test"
			}
			bundle, err := Solve(context.Background(), input)
			if err != nil {
				t.Fatal(err)
			}
			values := map[string]any{}
			var secrets []string
			for name, parameter := range bundle.Parameters.Parameters {
				if parameter.Reference != nil {
					secrets = append(secrets, name)
				} else {
					values[name] = parameter.Value
				}
			}
			resolved, err := ResolveAzure(context.Background(), input.Features, values, secrets)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(resolved, bundle.Topology) {
				t.Fatalf("Azure deploy and bundle dependencies differ: %+v != %+v", resolved, bundle.Topology)
			}
			values["postgresRunRetentionDays"] = -1
			if _, err := ResolveAzure(context.Background(), input.Features, values, secrets); err == nil {
				t.Fatal("accepted a provider parameter constraint violation")
			}
		})
	}
}

func TestResolveAzureRejectsInvalidInputs(t *testing.T) {
	input := exampleRequirements(t)
	for _, test := range []struct {
		name   string
		values map[string]any
		secure []string
		want   string
	}{
		{"literal secret", map[string]any{"sessionSecret": "not-a-reference"}, nil, "secret Azure parameter"},
		{"unknown parameter", map[string]any{"notDeclared": true}, nil, "unknown"},
		{"non-secret reference", nil, []string{"functionAppName"}, "non-secret"},
		{"unknown reference", nil, []string{"notDeclared"}, "unknown"},
		{"missing required input", nil, nil, "dependency missing"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := ResolveAzure(context.Background(), input.Features, test.values, test.secure)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %s: %v", test.want, err)
			}
		})
	}
	for _, profile := range []string{"single-vm", "minimal-azure", "full-azure"} {
		content, err := ExampleFor(profile)
		if err != nil {
			t.Fatal(err)
		}
		other, err := Parse(content)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := ResolveAzure(context.Background(), other.Features, nil, nil); err == nil {
			t.Fatalf("native deployment accepted a bundle-only profile: %s", profile)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Solve(ctx, input); !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}
}
