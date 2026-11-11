package topology

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/netip"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strings"

	"golang.org/x/crypto/ssh"
)

type Solution struct {
	Requirements Requirements
	Topology     Topology
	TOSCA        []byte
	Parameters   Parameters
	Optional     *Parameters
}

func match(pattern, value string) bool { return regexp.MustCompile(pattern).MatchString(value) }
func nonempty(value any) bool          { text, ok := value.(string); return ok && strings.TrimSpace(text) != "" }

func numeric(value any) (float64, bool) {
	switch number := value.(type) {
	case int:
		return float64(number), true
	case int64:
		return float64(number), true
	case uint64:
		return float64(number), number <= 1<<53
	case float64:
		return number, !math.IsNaN(number) && !math.IsInf(number, 0)
	default:
		return 0, false
	}
}

func list(value any) ([]string, bool) {
	switch values := value.(type) {
	case []string:
		return values, true
	case []any:
		result := make([]string, len(values))
		for i, value := range values {
			text, ok := value.(string)
			if !ok || strings.TrimSpace(text) == "" {
				return nil, false
			}
			result[i] = text
		}
		return result, true
	default:
		return nil, false
	}
}

func validateParameter(name string, value any, definition ParameterDefinition) error {
	valid, length := false, 0
	switch strings.ToLower(definition.Type) {
	case "string":
		text, ok := value.(string)
		valid = ok
		length = len([]rune(text))
	case "array":
		valid = value != nil && reflect.TypeOf(value).Kind() == reflect.Slice
		if valid {
			length = reflect.ValueOf(value).Len()
		}
	case "bool":
		_, valid = value.(bool)
	case "int":
		n, ok := numeric(value)
		valid = ok && n == math.Trunc(n) && math.Abs(n) <= 1<<53
	case "object":
		_, valid = value.(map[string]any)
	}
	if !valid {
		return fmt.Errorf("invalid type for Azure parameter %s: expected %s", name, definition.Type)
	}
	if len(definition.AllowedValues) > 0 && !slices.ContainsFunc(definition.AllowedValues, func(allowed any) bool { return reflect.DeepEqual(allowed, value) }) {
		return fmt.Errorf("invalid allowed value for %s", name)
	}
	if definition.MinLength != nil && length < *definition.MinLength {
		return fmt.Errorf("%s violates minLength", name)
	}
	if definition.MaxLength != nil && length > *definition.MaxLength {
		return fmt.Errorf("%s violates maxLength", name)
	}
	if n, ok := numeric(value); ok {
		if definition.MinValue != nil && n < *definition.MinValue {
			return fmt.Errorf("%s violates minValue", name)
		}
		if definition.MaxValue != nil && n > *definition.MaxValue {
			return fmt.Errorf("%s violates maxValue", name)
		}
	}
	return nil
}

func Solve(ctx context.Context, input Requirements) (Solution, error) {
	result := Solution{Requirements: input}
	if input.Version != 1 {
		return result, fmt.Errorf("requirements.version must be 1")
	}
	if input.Backend != "azure" {
		return result, fmt.Errorf("unsupported backend; only azure is implemented")
	}
	if err := input.Features.validate(); err != nil {
		return result, err
	}
	if input.Features.Deployment != "azure-functions" {
		return solveCompact(ctx, input)
	}
	sources, err := baselineFiles()
	if err != nil {
		return result, err
	}
	var template struct {
		Parameters map[string]ParameterDefinition `json:"parameters"`
	}
	if err := json.Unmarshal(sources["main.json"], &template); err != nil {
		return result, err
	}
	defaults := map[string]any{}
	for name, definition := range template.Parameters {
		if definition.DefaultValue != nil {
			var value any
			if err := json.Unmarshal(definition.DefaultValue, &value); err != nil {
				return result, fmt.Errorf("invalid embedded Azure default for %s: %w", name, err)
			}
			defaults[name] = value
		}
	}
	azure, features := input.Azure, input.Features
	if !isVaultResourceID(azure.SecretVaultID) {
		return result, fmt.Errorf("SEC-VAULT-001: azure.secretVaultId must identify an existing Azure Key Vault")
	}
	parameters := map[string]Parameter{}
	for name, value := range azure.Parameters {
		definition, exists := template.Parameters[name]
		if exists && strings.HasPrefix(strings.ToLower(definition.Type), "secure") {
			return result, fmt.Errorf("SEC-VAULT-002: %s must use a vault reference, not a literal parameter", name)
		}
		if !exists {
			return result, fmt.Errorf("unknown or secret Azure parameter: %s", name)
		}
		if err := validateParameter(name, value, definition); err != nil {
			return result, err
		}
		parameters[name] = Parameter{Value: value}
	}
	value := func(name string) any {
		if parameter, exists := parameters[name]; exists {
			return parameter.Value
		}
		return defaults[name]
	}
	set := func(name string, wanted any) error {
		if supplied, exists := azure.Parameters[name]; exists && !reflect.DeepEqual(supplied, wanted) {
			// YAML and JSON array decoding can have different concrete slice types.
			a, aErr := json.Marshal(supplied)
			b, bErr := json.Marshal(wanted)
			if aErr != nil || bErr != nil || string(a) != string(b) {
				return fmt.Errorf("%s conflicts with selected features", name)
			}
		}
		parameters[name] = Parameter{Value: wanted}
		return nil
	}
	for name, wanted := range map[string]any{
		"adxEnabled":                  features.Analytics == "adx",
		"functionPublicNetworkAccess": map[bool]string{true: "Disabled", false: "Enabled"}[features.Ingress == "private"],
	} {
		if err := set(name, wanted); err != nil {
			return result, err
		}
	}
	if features.Ingress == "public" {
		if err := set("functionAccessRestrictionsDefaultAction", "Allow"); err != nil {
			return result, err
		}
		if err := set("functionAccessRestrictions", []any{}); err != nil {
			return result, err
		}
	}
	collectionSecrets := []string{"collectorPrivateKey", "githubWebhookSecret"}
	if features.Acquisition == "actions" {
		if err := set("collectorImage", ""); err != nil {
			return result, err
		}
		for _, name := range []string{"collectorGithubAppId", "collectorControlRepository", "collectorNativeInventoryDigest"} {
			if nonempty(value(name)) {
				return result, fmt.Errorf("%s requires collector acquisition", name)
			}
		}
		for _, name := range collectionSecrets {
			if _, exists := azure.Secrets[name]; exists {
				return result, fmt.Errorf("%s requires collector acquisition", name)
			}
		}
	} else {
		for _, name := range []string{"collectorImage", "collectorGithubAppId", "collectorControlRepository", "collectorNativeInventoryDigest"} {
			if !nonempty(value(name)) {
				return result, fmt.Errorf("collector dependency missing: %s", name)
			}
		}
		for name, pattern := range map[string]string{
			"collectorGithubAppId": `^\d+$`, "collectorControlRepository": `^[\w.-]+/[\w.-]+$`, "collectorNativeInventoryDigest": `^[a-f0-9]{64}$`,
		} {
			text, _ := value(name).(string)
			if !match(pattern, text) {
				return result, fmt.Errorf("invalid collector dependency: %s", name)
			}
		}
		if users, ok := list(value("githubAdminUsers")); !ok || len(users) == 0 {
			return result, fmt.Errorf("collector requires githubAdminUsers")
		}
	}
	if features.Telemetry == "none" {
		if err := set("otlpEndpoint", ""); err != nil {
			return result, err
		}
		if err := set("otlpLogsEnabled", false); err != nil {
			return result, err
		}
		if _, exists := azure.Secrets["otlpHeaders"]; exists {
			return result, fmt.Errorf("otlpHeaders requires OTLP telemetry")
		}
	} else {
		text, _ := value("otlpEndpoint").(string)
		endpoint, err := url.Parse(text)
		if err != nil || endpoint.Scheme != "https" || endpoint.Hostname() == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" {
			return result, fmt.Errorf("OTLP dependency requires HTTPS otlpEndpoint without credentials, query or fragment")
		}
	}
	for name, pattern := range map[string]string{
		"functionAppName": `^[a-z][a-z0-9-]{0,17}[a-z0-9]$`, "storageAccountName": `^[a-z0-9]{3,24}$`,
		"containerRegistryName": `^[a-zA-Z0-9]{5,50}$`, "keyVaultName": `^[a-zA-Z][a-zA-Z0-9-]{1,22}[a-zA-Z0-9]$`,
	} {
		text, _ := value(name).(string)
		if !match(pattern, text) || (name == "functionAppName" || name == "keyVaultName") && strings.Contains(text, "--") {
			return result, fmt.Errorf("invalid Azure resource name: %s", name)
		}
	}
	if !nonempty(value("githubClientId")) {
		return result, fmt.Errorf("sign-in dependency missing: githubClientId")
	}
	appName, _ := value("functionAppName").(string)
	host := appName + ".azurewebsites.net"
	if _, exists := azure.Parameters["allowedHosts"]; !exists {
		if err := set("allowedHosts", []string{host}); err != nil {
			return result, err
		}
	}
	if _, exists := azure.Parameters["githubAllowedOrganizations"]; !exists {
		if err := set("githubAllowedOrganizations", []string{}); err != nil {
			return result, err
		}
	}
	authorized := false
	for _, field := range []string{"allowedHosts", "githubAllowedOrganizations", "githubAllowedTeams", "githubAllowedUsers", "githubAdminUsers"} {
		items, ok := list(value(field))
		if !ok || slices.ContainsFunc(items, func(item string) bool { return strings.TrimSpace(item) == "" }) {
			return result, fmt.Errorf("%s must contain nonempty strings", field)
		}
		if field == "allowedHosts" && !slices.Contains(items, host) {
			return result, fmt.Errorf("allowedHosts must include the Function App OAuth callback host")
		}
		if slices.Contains([]string{"githubAllowedOrganizations", "githubAllowedTeams", "githubAllowedUsers"}, field) && len(items) > 0 {
			authorized = true
		}
	}
	if !authorized {
		return result, fmt.Errorf("sign-in requires at least one allowed GitHub organization, team or user")
	}
	if _, exists := azure.Parameters["postgresRunRetentionDays"]; !exists {
		return result, fmt.Errorf("explicit postgresRunRetentionDays is required; preserve existing and snapshot retention")
	}
	requiredSecrets := []string{"githubClientSecret", "sessionSecret", "postgresAdministratorPassword", "postgresOwnerPassword", "postgresRuntimePassword"}
	if features.Acquisition == "collector" {
		requiredSecrets = append(requiredSecrets, collectionSecrets...)
	}
	runtimeSecrets := additionalRuntimeSecrets(features)
	requiredSecrets = append(requiredSecrets, runtimeSecrets...)
	if err := validateVaultReferences(input, requiredSecrets); err != nil {
		return result, err
	}
	for _, name := range requiredSecrets {
		if strings.TrimSpace(azure.Secrets[name]) == "" {
			return result, fmt.Errorf("secret dependency missing: %s", name)
		}
	}
	for name, secretName := range azure.Secrets {
		runtimeOnly := slices.Contains(runtimeSecrets, name)
		if !strings.HasPrefix(strings.ToLower(template.Parameters[name].Type), "secure") && !runtimeOnly {
			return result, fmt.Errorf("unknown or non-secret Azure secret: %s", name)
		}
		if !match(`^[a-zA-Z0-9-]{1,127}$`, secretName) {
			return result, fmt.Errorf("invalid Key Vault secret name for %s", name)
		}
		if runtimeOnly {
			continue
		}
		reference := &SecretReference{SecretName: secretName}
		reference.KeyVault.ID = azure.SecretVaultID
		parameters[name] = Parameter{Reference: reference}
	}
	for name, definition := range template.Parameters {
		parameter, exists := parameters[name]
		if definition.DefaultValue == nil && !exists {
			return result, fmt.Errorf("Azure dependency missing: %s", name)
		}
		if exists && parameter.Reference == nil {
			if err := validateParameter(name, parameter.Value, definition); err != nil {
				return result, err
			}
		}
	}
	result.Parameters = parameterDocument(parameters)
	result.Optional, err = solveHosts(input, parameters, appName)
	if err != nil {
		return result, err
	}
	result.TOSCA, err = providerServiceTemplate(features, parameters, template.Parameters)
	if err != nil {
		return result, err
	}
	result.Topology, err = resolveTOSCA(ctx, result.TOSCA, features)
	return result, err
}

func solveHosts(input Requirements, parameters map[string]Parameter, appName string) (*Parameters, error) {
	features, hosts := input.Features, input.Azure.Hosts
	if !features.SessionDataLake && !features.WebhookGateway {
		if hosts != nil {
			return nil, fmt.Errorf("azure.hosts requires sessionDataLake or webhookGateway")
		}
		return nil, nil
	}
	if hosts == nil {
		return nil, fmt.Errorf("optional host dependency missing: azure.hosts")
	}
	if !nonempty(parameters["location"].Value) {
		return nil, fmt.Errorf("optional hosts require an explicit Azure location")
	}
	if _, _, _, _, err := ssh.ParseAuthorizedKey([]byte(hosts.SSHPublicKey)); err != nil {
		return nil, fmt.Errorf("optional hosts require a valid SSH public key")
	}
	administrator := hosts.Administrator
	if administrator == "" {
		administrator = "caoadmin"
	}
	if !match(`^[a-z_][a-z0-9_-]{0,31}$`, administrator) || slices.Contains([]string{"root", "admin", "administrator", "guest"}, administrator) {
		return nil, fmt.Errorf("invalid optional host administrator")
	}
	size := hosts.VMSize
	if size == "" {
		size = "Standard_B2s"
	}
	if !match(`^Standard_[A-Za-z0-9_]+$`, size) {
		return nil, fmt.Errorf("invalid optional host vmSize")
	}
	for _, requirement := range []struct {
		field   string
		enabled bool
		cidrs   []string
	}{
		{"archiveAllowedSourceCidrs", features.SessionDataLake, hosts.ArchiveAllowedSourceCIDRs},
		{"gatewayAllowedSourceCidrs", features.WebhookGateway, hosts.GatewayAllowedSourceCIDRs},
	} {
		if requirement.enabled != (len(requirement.cidrs) > 0) {
			return nil, fmt.Errorf("%s must be configured exactly when its optional feature is selected", requirement.field)
		}
		for _, text := range requirement.cidrs {
			prefix, err := netip.ParsePrefix(text)
			if err != nil || !prefix.Addr().Is4() {
				return nil, fmt.Errorf("%s must contain reviewed IPv4 CIDRs", requirement.field)
			}
		}
	}
	if features.SessionDataLake != (len(hosts.ArchiveRepositories) > 0) {
		return nil, fmt.Errorf("session data lake requires exact archiveRepositories; omit them when disabled")
	}
	for _, repository := range hosts.ArchiveRepositories {
		if !match(`^[a-z0-9][a-z0-9_.-]*/[a-z0-9][a-z0-9_.-]*$`, repository) {
			return nil, fmt.Errorf("invalid archive repository scope")
		}
	}
	pgName := appName + "-pg"
	if parameter, exists := parameters["postgresServerName"]; exists {
		pgName, _ = parameter.Value.(string)
	}
	cidrs := func(values []string) []string { return append([]string{}, values...) }
	values := map[string]any{
		"location": parameters["location"].Value, "namePrefix": appName, "baselineVirtualNetworkName": appName + "-vnet",
		"postgresServerName": pgName, "containerRegistryName": parameters["containerRegistryName"].Value,
		"sessionDataLakeEnabled": features.SessionDataLake, "webhookGatewayEnabled": features.WebhookGateway,
		"administrator": administrator, "sshPublicKey": hosts.SSHPublicKey, "vmSize": size,
		"archiveAllowedSourceCidrs": cidrs(hosts.ArchiveAllowedSourceCIDRs), "gatewayAllowedSourceCidrs": cidrs(hosts.GatewayAllowedSourceCIDRs),
	}
	result := parameterDocument(map[string]Parameter{})
	for name, value := range values {
		result.Parameters[name] = Parameter{Value: value}
	}
	return &result, nil
}
