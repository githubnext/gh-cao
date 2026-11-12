package topology

import (
	"context"
	"fmt"
	"net/netip"
	"slices"
	"strings"
)

var containerAppsSecretNames = []string{
	"githubClientSecret", "sessionSecret", "githubWebhookSecret",
	"postgresAdministratorPassword", "postgresOwnerPassword", "postgresRuntimePassword",
}

func solveContainerApps(ctx context.Context, input Requirements) (Solution, error) {
	result := Solution{Requirements: input}
	if input.Azure.Hosts != nil {
		return result, fmt.Errorf("azure.hosts is only supported by the azure-functions deployment")
	}
	definitions, parameters, defaults, err := deploymentParameters(input, "assets/container-apps/main.generated.json", containerAppsSecretNames)
	if err != nil {
		return result, err
	}
	value := func(name string) any {
		if parameter, exists := parameters[name]; exists {
			return parameter.Value
		}
		return defaults[name]
	}
	for name, pattern := range map[string]string{
		"deploymentName":        `^[a-z][a-z0-9-]{0,17}[a-z0-9]$`,
		"containerRegistryName": `^[a-zA-Z0-9]{5,50}$`,
		"keyVaultName":          `^[a-zA-Z][a-zA-Z0-9-]{1,22}[a-zA-Z0-9]$`,
	} {
		text, _ := value(name).(string)
		if !match(pattern, text) || strings.Contains(text, "--") {
			return result, fmt.Errorf("invalid Azure resource name: %s", name)
		}
	}
	registry, _ := value("containerRegistryName").(string)
	for _, name := range []string{"dashboardImage", "provisioningImage", "ingestionImage"} {
		image, _ := value(name).(string)
		prefix := strings.ToLower(registry) + ".azurecr.io/"
		if !strings.HasPrefix(image, prefix) || !match(`^[a-z0-9][a-z0-9._/-]*@sha256:[a-f0-9]{64}$`, strings.TrimPrefix(image, prefix)) {
			return result, fmt.Errorf("%s requires an immutable SHA-256 image reference in the selected ACR", name)
		}
	}
	cidrs, ok := list(value("trustedProxyCidrs"))
	if !ok || len(cidrs) == 0 {
		return result, fmt.Errorf("container apps requires reviewed trustedProxyCidrs")
	}
	for _, text := range cidrs {
		prefix, err := netip.ParsePrefix(text)
		private := false
		for _, allowed := range []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "127.0.0.0/8"} {
			network := netip.MustParsePrefix(allowed)
			if err == nil && prefix.Bits() >= network.Bits() && network.Contains(prefix.Masked().Addr()) {
				private = true
			}
		}
		if !private {
			return result, fmt.Errorf("trustedProxyCidrs must contain only private IPv4 or loopback CIDRs supported by the hosted runtime")
		}
	}
	if err := validateHostedParameters(input, value); err != nil {
		return result, err
	}
	if err := validateVaultReferences(input, containerAppsSecretNames); err != nil {
		return result, err
	}
	for name, secret := range input.Azure.Secrets {
		if !slices.Contains(containerAppsSecretNames, name) {
			return result, fmt.Errorf("unknown Container Apps secret reference: %s", name)
		}
		ref := &SecretReference{SecretName: secret}
		ref.KeyVault.ID = input.Azure.SecretVaultID
		parameters[name] = Parameter{Reference: ref}
	}
	for name, definition := range definitions {
		if definition.DefaultValue == nil {
			if _, exists := parameters[name]; !exists {
				return result, fmt.Errorf("container apps Azure dependency missing: %s", name)
			}
		}
	}
	result.Parameters = parameterDocument(parameters)
	result.TOSCA, err = providerServiceTemplate(input.Features, parameters, definitions)
	if err != nil {
		return result, err
	}
	result.Topology, err = resolveTopology(ctx, result.TOSCA, input.Features)
	return result, err
}
