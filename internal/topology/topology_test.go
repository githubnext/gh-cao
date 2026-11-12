package topology

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
	"gopkg.in/yaml.v3"
)

func exampleRequirements(t *testing.T) Requirements {
	t.Helper()
	content, err := Example()
	if err != nil {
		t.Fatal(err)
	}
	input, err := Parse(content)
	if err != nil {
		t.Fatal(err)
	}
	return input
}

func withHosts(t *testing.T) Requirements {
	t.Helper()
	input := exampleRequirements(t)
	key, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	public, err := ssh.NewPublicKey(key)
	if err != nil {
		t.Fatal(err)
	}
	input.Features.SessionDataLake, input.Features.WebhookGateway = true, true
	input.Azure.Hosts = &Hosts{
		SSHPublicKey:              strings.TrimSpace(string(ssh.MarshalAuthorizedKey(public))),
		ArchiveAllowedSourceCIDRs: []string{"203.0.113.0/24"}, GatewayAllowedSourceCIDRs: []string{"192.0.2.0/24"},
		ArchiveRepositories: []string{"octo/api"},
	}
	for _, name := range additionalRuntimeSecrets(input.Features) {
		input.Azure.Secrets[name] = "test-" + strings.ToLower(name)
	}
	return input
}

func TestTopologyResolvedFromFeatureGraph(t *testing.T) {
	features := exampleRequirements(t).Features
	topology, err := resolveTopology(context.Background(), features)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckTopology(topology); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(topology.Order, []string{"monitoring", "network", "job-environment", "postgres", "registry", "runtime-storage", "sign-in-app", "secrets", "database-provisioning", "dashboard", "snapshot-ingestion"}) {
		t.Fatalf("unexpected topology order: %v", topology.Order)
	}
}

func TestFeatureCombinationsAndBundleDrift(t *testing.T) {
	for _, lake := range []bool{false, true} {
		for _, gateway := range []bool{false, true} {
			input := withHosts(t)
			input.Features.SessionDataLake, input.Features.WebhookGateway = lake, gateway
			if !lake {
				input.Azure.Hosts.ArchiveRepositories = nil
				input.Azure.Hosts.ArchiveAllowedSourceCIDRs = nil
			}
			if !gateway {
				input.Azure.Hosts.GatewayAllowedSourceCIDRs = nil
			}
			if !lake && !gateway {
				input.Azure.Hosts = nil
			}
			for _, name := range additionalRuntimeSecrets(Features{SessionDataLake: true, WebhookGateway: true}) {
				if !slices.Contains(additionalRuntimeSecrets(input.Features), name) {
					delete(input.Azure.Secrets, name)
				}
			}
			content, err := yaml.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			directory := filepath.Join(t.TempDir(), "bundle")
			graph, err := Generate(context.Background(), content, directory, bundleTestValidator(t))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Check(context.Background(), directory); err != nil {
				t.Fatal(err)
			}
			if _, err := Generate(context.Background(), content, directory, bundleTestValidator(t)); err == nil {
				t.Fatal("overwrote output")
			}
			manifest, err := os.ReadFile(filepath.Join(directory, "MANIFEST.md"))
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Count(manifest, []byte("```mermaid")) != 2 {
				t.Fatal("missing architecture/dependency diagrams")
			}
			if strings.Contains(string(manifest), "Gateway receive VM") != gateway || strings.Contains(string(manifest), "Dedicated private Blob") != lake {
				t.Fatal("diagram does not reflect selected features")
			}
			if strings.Contains(string(manifest), input.Azure.SecretVaultID) {
				t.Fatal("diagram leaked a secret reference")
			}
			if gateway && slices.Index(graph.Order, "gateway-dispatch") >= slices.Index(graph.Order, "gateway-receive") {
				t.Fatal("receive started before source authorization primer")
			}
			for _, name := range []string{"main.bicep", "parameters.json", "manifest.json", "MANIFEST.md", "topology.yaml", "RULES.md", "rules.json"} {
				target := filepath.Join(directory, name)
				original, err := os.ReadFile(target)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(target, append(original, '\n'), 0o600); err != nil {
					t.Fatal(err)
				}
				if _, err := Check(context.Background(), directory); err == nil {
					t.Fatalf("missed drift in %s", name)
				}
				if err := os.WriteFile(target, original, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(directory, "orphan.bicep"), []byte("stale"), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Check(context.Background(), directory); err == nil {
				t.Fatal("accepted orphaned infrastructure")
			}
		}
	}
}

func TestDependencyChecker(t *testing.T) {
	solution, err := Solve(context.Background(), exampleRequirements(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"missing-node", "missing-edge", "duplicate-edge", "cycle", "order", "duplicate-node"} {
		t.Run(name, func(t *testing.T) {
			content, err := json.Marshal(solution.Topology)
			if err != nil {
				t.Fatal(err)
			}
			var graph Topology
			if err := json.Unmarshal(content, &graph); err != nil {
				t.Fatal(err)
			}
			index := slices.IndexFunc(graph.Nodes, func(node Node) bool { return node.ID == "dashboard" })
			switch name {
			case "missing-node":
				graph.Nodes = slices.DeleteFunc(graph.Nodes, func(node Node) bool { return node.ID == "postgres" })
			case "missing-edge":
				graph.Nodes[index].DependsOn = graph.Nodes[index].DependsOn[1:]
			case "duplicate-edge":
				graph.Nodes[index].DependsOn = append(graph.Nodes[index].DependsOn, graph.Nodes[index].DependsOn[0])
			case "cycle":
				for i := range graph.Nodes {
					if graph.Nodes[i].ID == "network" {
						graph.Nodes[i].DependsOn = []string{"dashboard"}
					}
				}
			case "order":
				slices.Reverse(graph.Order)
			case "duplicate-node":
				graph.Nodes = append(graph.Nodes, graph.Nodes[0])
			}
			if err := CheckTopology(graph); err == nil {
				t.Fatalf("accepted %s", name)
			}
		})
	}
}

func TestRequirementsFailClosed(t *testing.T) {
	changes := []struct {
		name   string
		change func(*Requirements)
	}{
		{"backend", func(r *Requirements) { r.Backend = "aws" }},
		{"secret-literal", func(r *Requirements) { r.Azure.Parameters["sessionSecret"] = "not-a-reference" }},
		{"unknown-parameter", func(r *Requirements) { r.Azure.Parameters["unknown"] = true }},
		{"missing-secret", func(r *Requirements) { delete(r.Azure.Secrets, "sessionSecret") }},
		{"reused-password", func(r *Requirements) {
			r.Azure.Secrets["postgresOwnerPassword"] = r.Azure.Secrets["postgresRuntimePassword"]
		}},
		{"missing-retention", func(r *Requirements) { delete(r.Azure.Parameters, "postgresRunRetentionDays") }},
		{"invalid-retention", func(r *Requirements) { r.Azure.Parameters["postgresRunRetentionDays"] = 6 }},
		{"invalid-name", func(r *Requirements) { r.Azure.Parameters["functionAppName"] = "cao--bad" }},
		{"missing-allowlist", func(r *Requirements) { r.Azure.Parameters["githubAllowedOrganizations"] = []string{} }},
		{"feature-conflict", func(r *Requirements) { r.Azure.Parameters["adxEnabled"] = true }},
		{"missing-collector", func(r *Requirements) { r.Features.Acquisition = "collector" }},
		{"missing-otlp", func(r *Requirements) { r.Features.Telemetry = "otlp" }},
		{"invalid-otlp", func(r *Requirements) {
			r.Features.Telemetry = "otlp"
			r.Azure.Parameters["otlpEndpoint"] = "https://user:secret@example.com"
		}},
		{"missing-hosts", func(r *Requirements) { r.Features.WebhookGateway = true }},
	}
	for _, entry := range changes {
		t.Run(entry.name, func(t *testing.T) {
			input := exampleRequirements(t)
			entry.change(&input)
			if _, err := Solve(context.Background(), input); err == nil {
				t.Fatalf("accepted %s", entry.name)
			}
		})
	}
	for _, source := range []string{"version: 1\nversion: 2", "version: 1\nunknown: true", "features: null", "one: &value [1]\ntwo: *value", "features: ["} {
		if _, err := Parse([]byte(source)); err == nil {
			t.Fatalf("accepted invalid YAML: %s", source)
		}
	}
}
func TestTOSCABaseline(t *testing.T) {
	solution, err := Solve(context.Background(), exampleRequirements(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(solution.Topology.Nodes) != 11 {
		t.Fatalf("unexpected TOSCA closure: %+v", solution.Topology)
	}
	if err := CheckTopology(solution.Topology); err != nil {
		t.Fatal(err)
	}
}

func TestEmbeddedAzureParity(t *testing.T) {
	files, err := baselineFiles()
	if err != nil {
		t.Fatal(err)
	}
	names, err := filepath.Glob("../../server/azure/*.bicep")
	if err != nil {
		t.Fatal(err)
	}
	names = append(names, "../../server/azure/main.json")
	if len(files) != len(names) {
		t.Fatal("embedded Azure file set drift; run go generate ./internal/topology")
	}
	for _, name := range names {
		canonical, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if string(canonical) != string(files[filepath.Base(name)]) {
			t.Fatalf("embedded Azure source drift: %s; run go generate ./internal/topology", name)
		}
	}
}

func TestBundleDeterminismAndSymlinks(t *testing.T) {
	content, err := Example()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	first, second := filepath.Join(root, "first"), filepath.Join(root, "second")
	for _, directory := range []string{first, second} {
		if _, err := Generate(context.Background(), content, directory, bundleTestValidator(t)); err != nil {
			t.Fatal(err)
		}
	}
	a, err := os.ReadFile(filepath.Join(first, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(second, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Engine string `json:"engine"`
	}
	if err := json.Unmarshal(a, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Engine != "cao-feature-dependency-graph-v1" {
		t.Fatalf("unexpected topology engine: %s", manifest.Engine)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("resolved topology or manifest is nondeterministic")
	}
	target := filepath.Join(first, "parameters.json")
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(second, "parameters.json"), target); err != nil {
		t.Fatal(err)
	}
	if _, err := Check(context.Background(), first); err == nil {
		t.Fatal("accepted symlinked generated file")
	}
}

func TestGatewayStartupCredentialIsolation(t *testing.T) {
	root := t.TempDir()
	credentials, bin := filepath.Join(root, "credentials"), filepath.Join(root, "bin")
	for _, directory := range []string{credentials, bin} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	script, err := assets.ReadFile("assets/run-gateway.sh")
	if err != nil {
		t.Fatal(err)
	}
	run := filepath.Join(root, "run-gateway.sh")
	if err := os.WriteFile(run, script, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "docker"), []byte("#!/bin/sh\nprintf '%s\\n' \"$@\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"tls.crt", "tls.key", "receive.pem", "dispatch.pem"} {
		if err := os.WriteFile(filepath.Join(credentials, name), []byte("test fixture, not a credential"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	policy, environment := filepath.Join(root, "policy.json"), filepath.Join(root, "environment")
	for _, name := range []string{policy, environment} {
		if err := os.WriteFile(name, []byte{}, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, role := range []string{"receive", "dispatch"} {
		cmd := exec.Command("sh", run, role, "example.azurecr.io/gateway@sha256:"+strings.Repeat("a", 64), policy, environment, credentials)
		cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("%s: %s: %v", role, output, err)
		}
		if role == "receive" {
			if bytes.Contains(output, []byte(".pem")) || bytes.Contains(output, []byte("KEY_FILE")) {
				t.Fatal("receiver received App private keys")
			}
			if !bytes.Contains(output, []byte("--publish\n443:8443")) {
				t.Fatal("receiver has no TLS listener mapping")
			}
		} else {
			if bytes.Contains(output, []byte("--publish")) || bytes.Contains(output, []byte("tls.key")) {
				t.Fatal("dispatcher received ingress material")
			}
			if !bytes.Contains(output, []byte("CAO_GATEWAY_DISPATCH_KEY_FILE")) {
				t.Fatal("dispatcher is missing its own key reference")
			}
		}
	}
	if err := os.Chmod(environment, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", run, "receive", "example.azurecr.io/gateway@sha256:"+strings.Repeat("a", 64), policy, environment, credentials)
	cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	if output, err := cmd.CombinedOutput(); err == nil || !bytes.Contains(output, []byte("must be private")) {
		t.Fatalf("accepted public credential environment: %s: %v", output, err)
	}
}
