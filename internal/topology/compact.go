package topology

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"golang.org/x/crypto/ssh"
)

var compactSecretNames = []string{
	"githubClientSecret", "sessionSecret", "githubWebhookSecret",
	"postgresAdministratorPassword", "postgresOwnerPassword", "postgresRuntimePassword",
	"postgresRuntimeUrl", "tlsCertificate", "tlsPrivateKey",
}

func solveCompact(ctx context.Context, input Requirements) (Solution, error) {
	result := Solution{Requirements: input}
	if input.Azure.Hosts != nil {
		return result, fmt.Errorf("azure.hosts is only supported by the azure-functions deployment")
	}
	definitions, parameters, defaults, err := deploymentParameters(input, "assets/compact/main.generated.json", compactSecretNames)
	if err != nil {
		return result, err
	}
	value := func(name string) any {
		if parameter, exists := parameters[name]; exists {
			return parameter.Value
		}
		return defaults[name]
	}
	managed := input.Features.Deployment == "minimal-azure"
	if supplied, exists := input.Azure.Parameters["managedPostgres"]; exists && supplied != managed {
		return result, fmt.Errorf("managedPostgres conflicts with deployment %s", input.Features.Deployment)
	}
	parameters["managedPostgres"] = Parameter{Value: managed}
	name, _ := value("deploymentName").(string)
	if !match(`^[a-z][a-z0-9-]{0,17}[a-z0-9]$`, name) || strings.Contains(name, "--") {
		return result, fmt.Errorf("deploymentName must be 2-19 lowercase letters/digits/hyphens, starting with a letter")
	}
	key, _ := value("sshPublicKey").(string)
	if _, _, _, _, err := ssh.ParseAuthorizedKey([]byte(key)); err != nil {
		return result, fmt.Errorf("compact deployment requires a valid SSH public key")
	}
	admin, _ := value("administrator").(string)
	if !match(`^[a-z_][a-z0-9_-]{0,31}$`, admin) || slices.Contains([]string{"root", "admin", "administrator", "guest"}, admin) {
		return result, fmt.Errorf("invalid compact host administrator")
	}
	size, _ := value("vmSize").(string)
	if !match(`^Standard_[A-Za-z0-9_]+$`, size) {
		return result, fmt.Errorf("invalid compact host vmSize")
	}
	cidrs, ok := list(value("httpsSourceCidrs"))
	if !ok || len(cidrs) == 0 {
		return result, fmt.Errorf("compact deployment requires reviewed httpsSourceCidrs")
	}
	for _, text := range cidrs {
		prefix, err := netip.ParsePrefix(text)
		if err != nil || !prefix.Addr().Is4() {
			return result, fmt.Errorf("httpsSourceCidrs must contain IPv4 CIDRs")
		}
	}
	if err := validateHostedParameters(input, value); err != nil {
		return result, err
	}
	if err := validateVaultReferences(input, compactSecretNames); err != nil {
		return result, err
	}
	for secret := range input.Azure.Secrets {
		if !slices.Contains(compactSecretNames, secret) {
			return result, fmt.Errorf("unknown compact secret reference: %s", secret)
		}
	}
	if managed {
		ref := &SecretReference{SecretName: input.Azure.Secrets["postgresAdministratorPassword"]}
		ref.KeyVault.ID = input.Azure.SecretVaultID
		parameters["postgresAdministratorPassword"] = Parameter{Reference: ref}
	}
	for name, definition := range definitions {
		if definition.DefaultValue == nil {
			if _, exists := parameters[name]; !exists {
				return result, fmt.Errorf("compact Azure dependency missing: %s", name)
			}
		}
	}
	result.Parameters = parameterDocument(parameters)
	result.TOSCA, err = providerServiceTemplate(input.Features, parameters, definitions)
	if err != nil {
		return result, err
	}
	result.Topology, err = resolveTopology(ctx, input.Features)
	return result, err
}

func deploymentParameters(input Requirements, source string, secretNames []string) (map[string]ParameterDefinition, map[string]Parameter, map[string]any, error) {
	content, err := assets.ReadFile(source)
	if err != nil {
		return nil, nil, nil, err
	}
	var template struct {
		Parameters map[string]ParameterDefinition `json:"parameters"`
	}
	if err := json.Unmarshal(content, &template); err != nil {
		return nil, nil, nil, err
	}
	parameters := map[string]Parameter{}
	defaults := map[string]any{}
	for name, definition := range template.Parameters {
		if definition.DefaultValue != nil {
			var value any
			if err := json.Unmarshal(definition.DefaultValue, &value); err != nil {
				return nil, nil, nil, err
			}
			defaults[name] = value
		}
	}
	for name, value := range input.Azure.Parameters {
		definition, exists := template.Parameters[name]
		if slices.Contains(secretNames, name) || exists && strings.HasPrefix(strings.ToLower(definition.Type), "secure") {
			return nil, nil, nil, fmt.Errorf("SEC-VAULT-002: %s must use a vault reference, not a literal parameter", name)
		}
		if !exists {
			return nil, nil, nil, fmt.Errorf("unknown or secret Azure parameter: %s", name)
		}
		if err := validateParameter(name, value, definition); err != nil {
			return nil, nil, nil, err
		}
		parameters[name] = Parameter{Value: value}
	}
	return template.Parameters, parameters, defaults, nil
}

func validateHostedParameters(input Requirements, value func(string) any) error {
	if !nonempty(value("githubClientId")) {
		return fmt.Errorf("hosted OAuth requires githubClientId")
	}
	authorized := false
	for _, field := range []string{"githubAllowedOrganizations", "githubAllowedTeams", "githubAllowedUsers", "githubAdminUsers"} {
		users, ok := list(value(field))
		if !ok || slices.ContainsFunc(users, func(user string) bool { return strings.TrimSpace(user) == "" }) {
			return fmt.Errorf("%s must contain nonempty strings", field)
		}
		if field == "githubAdminUsers" && len(users) == 0 {
			return fmt.Errorf("hosted service requires githubAdminUsers")
		}
		if field != "githubAdminUsers" && len(users) > 0 {
			authorized = true
		}
	}
	if !authorized {
		return fmt.Errorf("hosted OAuth requires at least one allowed organization, team or user")
	}
	if _, exists := input.Azure.Parameters["postgresRunRetentionDays"]; !exists {
		return fmt.Errorf("explicit postgresRunRetentionDays is required")
	}
	return nil
}

func validateVaultReferences(input Requirements, required []string) error {
	if !isVaultResourceID(input.Azure.SecretVaultID) {
		return fmt.Errorf("SEC-VAULT-001: secrets require an explicit Azure Key Vault resource ID")
	}
	for _, name := range required {
		if !match(`^[a-zA-Z0-9-]{1,127}$`, input.Azure.Secrets[name]) {
			return fmt.Errorf("SEC-VAULT-001: missing or invalid Key Vault reference for %s", name)
		}
	}
	passwords := map[string]bool{}
	for _, role := range []string{"postgresAdministratorPassword", "postgresOwnerPassword", "postgresRuntimePassword"} {
		name := input.Azure.Secrets[role]
		if passwords[name] {
			return fmt.Errorf("SEC-VAULT-001: PostgreSQL roles require distinct vault secret references")
		}
		passwords[name] = true
	}
	return nil
}
