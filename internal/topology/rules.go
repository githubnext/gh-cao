package topology

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const (
	secretsGuidance     = "https://learn.microsoft.com/azure/well-architected/security/application-secrets"
	securityGuidance    = "https://learn.microsoft.com/azure/well-architected/security/checklist"
	reliabilityGuidance = "https://learn.microsoft.com/azure/well-architected/reliability/checklist"
	costGuidance        = "https://learn.microsoft.com/azure/well-architected/cost-optimization/checklist"
)

// ArchitectureFacts describe declared capabilities, never observed deployment readiness.
type ArchitectureFacts struct {
	RequiredSecrets                                                 []string
	Secrets                                                         map[string]SecretReference
	LiteralSecrets                                                  []string
	PublicIngress, VirtualNetwork, DatabasePrivate, ManagedIdentity bool
	OAuthConfigured                                                 bool
	MinimumTLS                                                      string
	SingleHost, DatabaseHA, DatabaseBackups, VaultPurgeProtection   bool
	DestinationVault                                                bool
	ExistingVault                                                   bool
	ADXDestinations                                                 int
}

type RuleFinding struct {
	Code      string `json:"code"`
	Severity  string `json:"severity"`
	Message   string `json:"message"`
	Reference string `json:"reference"`
}

type RuleReport struct {
	Scope    string        `json:"scope"`
	Errors   int           `json:"errors"`
	Warnings int           `json:"warnings"`
	Findings []RuleFinding `json:"findings"`
}

func EvaluateRules(f ArchitectureFacts) RuleReport {
	report := RuleReport{Scope: "declared configuration only; not live verification or certification", Findings: []RuleFinding{}}
	add := func(code, severity, message, reference string) {
		report.Findings = append(report.Findings, RuleFinding{code, severity, message, reference})
		if severity == "error" {
			report.Errors++
		} else {
			report.Warnings++
		}
	}
	missing := []string{}
	for _, name := range f.RequiredSecrets {
		ref, exists := f.Secrets[name]
		if !exists || !isVaultResourceID(ref.KeyVault.ID) || !match(`^[a-zA-Z0-9-]{1,127}$`, ref.SecretName) {
			missing = append(missing, name)
		}
	}
	if len(f.RequiredSecrets) == 0 || len(missing) > 0 {
		add("SEC-VAULT-001", "error", "Required credentials must reference a Key Vault: "+strings.Join(missing, ", "), secretsGuidance)
	}
	if len(f.LiteralSecrets) > 0 {
		add("SEC-VAULT-002", "error", "Secret values are forbidden in configuration and deployment parameters: "+strings.Join(f.LiteralSecrets, ", "), secretsGuidance)
	}
	if !slices.Contains([]string{"1.2", "1.3"}, f.MinimumTLS) {
		add("SEC-TLS-001", "error", "Hosted transport must require TLS 1.2 or newer.", securityGuidance)
	}
	if !f.OAuthConfigured {
		add("SEC-AUTH-001", "error", "Hosted access requires GitHub OAuth and an explicit authorization allowlist.", securityGuidance)
	}
	if !f.DatabasePrivate {
		add("SEC-DATA-001", "error", "PostgreSQL must not expose a public database endpoint or port.", securityGuidance)
	}
	if f.PublicIngress && !f.VirtualNetwork {
		add("NET-VNET-001", "warning", "Public hosted service has no declared VNet isolation; review segmentation and private backend connectivity.", securityGuidance)
	}
	if f.PublicIngress {
		add("NET-EDGE-001", "warning", "Review public ingress, approved source networks, WAF/DDoS controls and certificate operations; no edge-security service is implied.", securityGuidance)
	}
	if !f.ManagedIdentity {
		add("SEC-IDENTITY-001", "warning", "Prefer managed identity and narrowly scoped secret/data grants over persistent infrastructure credentials.", securityGuidance)
	}
	if f.SingleHost {
		add("REL-HOST-001", "warning", "One or more service roles have a single VM failure domain; plan restart, recovery and acceptable downtime.", reliabilityGuidance)
	}
	if !f.DatabaseHA {
		add("REL-DATABASE-001", "warning", "No zone-redundant PostgreSQL HA is declared; review availability and recovery objectives.", reliabilityGuidance)
	}
	if !f.DatabaseBackups {
		add("REL-BACKUP-001", "warning", "Persistent disks are not backups. Configure off-host PostgreSQL backups and rehearse restore.", reliabilityGuidance)
	}
	if f.DestinationVault && !f.VaultPurgeProtection {
		add("SEC-VAULT-003", "warning", "Key Vault purge protection is disabled; restrict this choice to explicitly disposable environments.", secretsGuidance)
	}
	if f.ExistingVault {
		add("SEC-VAULT-004", "warning", "Verify the existing input vault's RBAC, secret-scoped grants, audit, rotation, soft delete and purge protection independently.", secretsGuidance)
	}
	if f.ADXDestinations > 1 {
		add("COST-ADX-001", "warning", "Separate analytics and archive ADX destinations are selected; review whether both paid clusters and their isolation are required.", costGuidance)
	}
	return report
}

func isVaultResourceID(value string) bool {
	return match(`(?i)^/subscriptions/[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}/resourceGroups/[\w.()-]+/providers/Microsoft\.KeyVault/vaults/[a-z0-9-]{3,24}$`, value)
}

func additionalRuntimeSecrets(features Features) []string {
	names := []string{}
	if features.SessionDataLake {
		names = append(names, "archiveOauthClientSecret", "archiveSessionSecret", "archiveEntraClientSecret", "archiveTlsCertificate", "archiveTlsPrivateKey")
	}
	if features.WebhookGateway {
		names = append(names, "gatewayWebhookSecret", "gatewayReceivePrivateKey", "gatewayDispatchPrivateKey", "gatewayTlsCertificate", "gatewayTlsPrivateKey")
	}
	return names
}

func ReviewRules(solution Solution) RuleReport {
	input := solution.Requirements
	required := []string{"githubClientSecret", "sessionSecret", "postgresAdministratorPassword", "postgresOwnerPassword", "postgresRuntimePassword"}
	compact := input.Features.Deployment == "single-vm" || input.Features.Deployment == "minimal-azure"
	if compact {
		required = slices.Clone(compactSecretNames)
	} else if input.Features.Deployment == "azure-container-apps" {
		required = slices.Clone(containerAppsSecretNames)
	} else if input.Features.Acquisition == "collector" {
		required = append(required, "collectorPrivateKey", "githubWebhookSecret")
	}
	required = append(required, additionalRuntimeSecrets(input.Features)...)
	nodes := map[string]bool{}
	for _, node := range solution.Topology.Nodes {
		nodes[node.ID] = true
	}
	known := slices.Contains([]string{"single-vm", "minimal-azure", "azure-functions", "azure-container-apps"}, input.Features.Deployment)
	authorized := false
	for _, field := range []string{"githubAllowedOrganizations", "githubAllowedTeams", "githubAllowedUsers"} {
		if users, ok := list(input.Azure.Parameters[field]); ok && len(users) > 0 {
			authorized = true
		}
	}
	facts := ArchitectureFacts{
		RequiredSecrets: required, Secrets: map[string]SecretReference{},
		PublicIngress:   input.Features.Ingress == "public",
		DatabasePrivate: known && nodes["postgres"], ManagedIdentity: known && nodes["dashboard"],
		OAuthConfigured:      nonempty(input.Azure.Parameters["githubClientId"]) && authorized,
		SingleHost:           compact || input.Features.SessionDataLake || input.Features.WebhookGateway,
		DatabaseBackups:      known && input.Features.Deployment != "single-vm",
		VaultPurgeProtection: true, DestinationVault: !compact, ExistingVault: true,
	}
	if known {
		facts.MinimumTLS = "1.2"
	}
	for _, node := range solution.Topology.Nodes {
		if node.ID == "network" {
			facts.VirtualNetwork = true
		}
		if node.ID == "adx" || node.ID == "archive-adx" {
			facts.ADXDestinations++
		}
	}
	if value, exists := input.Azure.Parameters["keyVaultPurgeProtection"]; exists {
		facts.VaultPurgeProtection, _ = value.(bool)
	}

	for name, secret := range input.Azure.Secrets {
		ref := SecretReference{SecretName: secret}
		ref.KeyVault.ID = input.Azure.SecretVaultID
		facts.Secrets[name] = ref
	}
	for _, name := range append(slices.Clone(required), "previousSessionSecret", "otlpHeaders") {
		if _, exists := input.Azure.Parameters[name]; exists {
			facts.LiteralSecrets = append(facts.LiteralSecrets, name)
		}
	}
	slices.Sort(facts.LiteralSecrets)
	return EvaluateRules(facts)
}

func (r RuleReport) Err() error {
	failures := []string{}
	for _, finding := range r.Findings {
		if finding.Severity == "error" {
			failures = append(failures, finding.Code+": "+finding.Message)
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("topology rule violations: %s", strings.Join(failures, "; "))
	}
	return nil
}

func (r RuleReport) Markdown() string {
	var out strings.Builder
	fmt.Fprintf(&out, "# Architecture rule review\n\n%d strict errors; %d advisory warnings.\n\nScope: %s.\n\n", r.Errors, r.Warnings, r.Scope)
	out.WriteString("Rules follow Microsoft Azure Well-Architected security and reliability guidance.\nWarnings record tradeoffs, not blanket production prohibitions. Zero errors is\nnot a compliance claim, approval, or evidence that external prerequisites exist.\n\n| Rule | Severity | Finding | Guidance |\n| --- | --- | --- | --- |\n")
	for _, finding := range r.Findings {
		fmt.Fprintf(&out, "| %s | %s | %s | [Microsoft guidance](%s) |\n", finding.Code, finding.Severity, finding.Message, finding.Reference)
	}
	return out.String()
}

func ReadRuleReport(directory string) (RuleReport, error) {
	var report RuleReport
	content, err := os.ReadFile(filepath.Join(directory, "rules.json"))
	if err != nil {
		return report, err
	}
	err = json.Unmarshal(content, &report)
	return report, err
}
