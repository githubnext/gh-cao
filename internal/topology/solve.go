package topology

import (
	"context"
	"fmt"
	"math"
	"net/netip"
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
	if input.Features.Deployment == "azure-container-apps" {
		return solveContainerApps(ctx, input)
	}
	return solveCompact(ctx, input)
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
