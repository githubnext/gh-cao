package topology

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func reviewedFacts(t *testing.T) ArchitectureFacts {
	t.Helper()
	input := exampleRequirements(t)
	ref := SecretReference{SecretName: "session-secret"}
	ref.KeyVault.ID = input.Azure.SecretVaultID
	return ArchitectureFacts{
		RequiredSecrets: []string{"sessionSecret"}, Secrets: map[string]SecretReference{"sessionSecret": ref},
		VirtualNetwork: true, DatabasePrivate: true, ManagedIdentity: true, OAuthConfigured: true,
		MinimumTLS: "1.2", DatabaseHA: true, DatabaseBackups: true, DestinationVault: true, VaultPurgeProtection: true,
	}
}

func TestArchitectureStrictRulesAndWarnings(t *testing.T) {
	for _, entry := range []struct {
		name, code, severity string
		change               func(*ArchitectureFacts)
	}{
		{"missing-vault", "SEC-VAULT-001", "error", func(f *ArchitectureFacts) { f.Secrets = nil }},
		{"invalid-vault", "SEC-VAULT-001", "error", func(f *ArchitectureFacts) {
			ref := f.Secrets["sessionSecret"]
			ref.KeyVault.ID = "not-a-vault"
			f.Secrets["sessionSecret"] = ref
		}},
		{"literal-secret", "SEC-VAULT-002", "error", func(f *ArchitectureFacts) { f.LiteralSecrets = []string{"sessionSecret"} }},
		{"weak-tls", "SEC-TLS-001", "error", func(f *ArchitectureFacts) { f.MinimumTLS = "1.0" }},
		{"no-oauth", "SEC-AUTH-001", "error", func(f *ArchitectureFacts) { f.OAuthConfigured = false }},
		{"public-database", "SEC-DATA-001", "error", func(f *ArchitectureFacts) { f.DatabasePrivate = false }},
		{"public-without-vnet", "NET-VNET-001", "warning", func(f *ArchitectureFacts) { f.PublicIngress = true; f.VirtualNetwork = false }},
		{"one-vm", "REL-HOST-001", "warning", func(f *ArchitectureFacts) { f.SingleHost = true }},
		{"no-ha", "REL-DATABASE-001", "warning", func(f *ArchitectureFacts) { f.DatabaseHA = false }},
		{"disk-not-backup", "REL-BACKUP-001", "warning", func(f *ArchitectureFacts) { f.DatabaseBackups = false }},
		{"purge-off", "SEC-VAULT-003", "warning", func(f *ArchitectureFacts) { f.VaultPurgeProtection = false }},
		{"two-adx", "COST-ADX-001", "warning", func(f *ArchitectureFacts) { f.ADXDestinations = 2 }},
	} {
		t.Run(entry.name, func(t *testing.T) {
			facts := reviewedFacts(t)
			entry.change(&facts)
			report := EvaluateRules(facts)
			index := slices.IndexFunc(report.Findings, func(f RuleFinding) bool { return f.Code == entry.code && f.Severity == entry.severity })
			if index < 0 {
				t.Fatalf("missed rule %s: %+v", entry.code, report)
			}

			if !strings.HasPrefix(report.Findings[index].Reference, "https://learn.microsoft.com/azure/well-architected/") {
				t.Fatal("missing proven architecture guidance")
			}
			if (report.Err() != nil) != (entry.severity == "error") {
				t.Fatal("advisory and strict rules have wrong enforcement")
			}
		})
	}
}

func TestMalformedRequirementsDoNotDiscloseValues(t *testing.T) {
	value := "sensitive-value-must-not-be-logged"
	_, err := Parse([]byte("features:\n  webhookGateway: " + value + "\n"))
	if err == nil || strings.Contains(err.Error(), value) {
		t.Fatalf("malformed input leaked its contents: %v", err)
	}
}

func TestCompactProfilesRequireVaultAndRejectIncompatibleFeatures(t *testing.T) {
	source, err := ExampleFor("single-vm")
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*Requirements){
		func(r *Requirements) { r.Azure.SecretVaultID = "" },
		func(r *Requirements) { delete(r.Azure.Secrets, "tlsPrivateKey") },
		func(r *Requirements) { r.Features.WebhookGateway = true },
		func(r *Requirements) { r.Features.Acquisition = "collector" },
		func(r *Requirements) { r.Azure.Parameters["managedPostgres"] = true },
	} {
		input, err := Parse(source)
		if err != nil {
			t.Fatal(err)
		}
		change(&input)
		content, err := yaml.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		output := filepath.Join(t.TempDir(), "rejected")
		if _, err := Generate(context.Background(), content, output, bundleTestValidator(t)); err == nil {
			t.Fatal("unsafe or incompatible compact plan was accepted")
		}
	}
}
