package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/githubnext/gh-cao/internal/topology"
)

// deployAzureTemplate is a compiled ARM template for the CAO Azure profile.
type deployAzureTemplate struct {
	Content []byte
	Digest  string
	Origin  string
}

// deployAzureTemplateSource supplies an embedded baseline or explicit ARM override.
type deployAzureTemplateSource interface {
	Load(context.Context) (deployAzureTemplate, error)
}

type deployAzureFileTemplate struct {
	path string
}

type deployAzureEmbeddedTemplate struct{}

func (deployAzureEmbeddedTemplate) Load(ctx context.Context) (deployAzureTemplate, error) {
	if err := ctx.Err(); err != nil {
		return deployAzureTemplate{}, err
	}
	content, err := topology.AzureTemplate()
	if err != nil {
		return deployAzureTemplate{}, fmt.Errorf("read embedded Azure template: %w", err)
	}
	sum := sha256.Sum256(content)
	return deployAzureTemplate{Content: content, Digest: hex.EncodeToString(sum[:]), Origin: "embedded"}, nil
}

func (s deployAzureFileTemplate) Load(ctx context.Context) (deployAzureTemplate, error) {
	if err := ctx.Err(); err != nil {
		return deployAzureTemplate{}, err
	}
	content, err := os.ReadFile(s.path)
	if err != nil {
		return deployAzureTemplate{}, fmt.Errorf("read template %s: %w", s.path, err)
	}
	sum := sha256.Sum256(content)
	return deployAzureTemplate{Content: content, Digest: hex.EncodeToString(sum[:]), Origin: "file"}, nil
}

type deployAzureTemplateParameter struct {
	Type         string `json:"type"`
	DefaultValue any    `json:"defaultValue"`
}

type deployAzureTemplateDocument struct {
	Schema     string                                  `json:"$schema"`
	Parameters map[string]deployAzureTemplateParameter `json:"parameters"`
	Resources  json.RawMessage                         `json:"resources"`
}

// checkDeployAzureTemplate fails closed unless every supplied parameter is
// declared, secure values bind only to securestring parameters, and every
// required template parameter is supplied.
func checkDeployAzureTemplate(template deployAzureTemplate, parameters map[string]any, secure []string) (deployAzureTemplateDocument, error) {
	var document deployAzureTemplateDocument
	if err := json.Unmarshal(template.Content, &document); err != nil {
		return document, fmt.Errorf("template is not compiled ARM JSON: %w", err)
	}
	if !strings.Contains(document.Schema, "deploymentTemplate.json") || len(document.Parameters) == 0 || len(document.Resources) == 0 {
		return document, errors.New("template is not a resource group ARM deployment template")
	}
	supplied := map[string]bool{}
	for name := range parameters {
		declared, ok := document.Parameters[name]
		if !ok {
			return document, fmt.Errorf("template does not declare parameter %s", name)
		}
		if strings.EqualFold(declared.Type, "securestring") || strings.EqualFold(declared.Type, "secureobject") {
			return document, fmt.Errorf("template parameter %s is secure but the config supplies it as plain text", name)
		}
		supplied[name] = true
	}
	for _, name := range secure {
		declared, ok := document.Parameters[name]
		if !ok {
			return document, fmt.Errorf("template does not declare secure parameter %s", name)
		}
		if !strings.EqualFold(declared.Type, "securestring") {
			return document, fmt.Errorf("template parameter %s must be securestring", name)
		}
		supplied[name] = true
	}
	var missing []string
	for name, declared := range document.Parameters {
		if declared.DefaultValue == nil && !supplied[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return document, fmt.Errorf("template requires parameters the config does not supply: %s", strings.Join(missing, ", "))
	}
	return document, nil
}

// deployAzureWorkspace holds owner-only deployment inputs and removes them on close.
type deployAzureWorkspace struct {
	directory string
}

func newDeployAzureWorkspace() (*deployAzureWorkspace, error) {
	directory, err := os.MkdirTemp("", "gh-cao-deploy-azure-")
	if err != nil {
		return nil, fmt.Errorf("create private deployment workspace: %w", err)
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		_ = os.RemoveAll(directory)
		return nil, fmt.Errorf("restrict deployment workspace: %w", err)
	}
	return &deployAzureWorkspace{directory: directory}, nil
}

func (w *deployAzureWorkspace) write(name string, value any) (string, error) {
	content, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "", err
	}
	path := filepath.Join(w.directory, name)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("create %s: %w", name, err)
	}
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return "", fmt.Errorf("write %s: %w", name, err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("write %s: %w", name, err)
	}
	return path, nil
}

func (w *deployAzureWorkspace) Close() error {
	return os.RemoveAll(w.directory)
}

// deployAzureSecrets resolves secret references into parameter-file entries
// and remembers resolved values so diagnostics can be redacted.
type deployAzureSecrets struct {
	entries map[string]any
	values  []string
}

func resolveDeployAzureSecrets(config deployAzureConfig, getenv func(string) (string, bool)) (deployAzureSecrets, error) {
	secrets := deployAzureSecrets{entries: map[string]any{}}
	passwords := map[string]string{}
	passwordRefs := map[string]string{}
	for _, secure := range config.secureParameters() {
		if strings.HasPrefix(secure.name, "postgres") {
			ref := secure.ref.describe()
			if previous := passwordRefs[ref]; previous != "" {
				return deployAzureSecrets{}, fmt.Errorf("%s and %s must reference different database passwords", previous, secure.field)
			}
			passwordRefs[ref] = secure.field
		}
		if secure.ref.KeyVault != nil {
			secrets.entries[secure.name] = map[string]any{"reference": map[string]any{
				"keyVault":   map[string]string{"id": secure.ref.KeyVault.ID},
				"secretName": secure.ref.KeyVault.SecretName,
			}}
			continue
		}
		value, ok := getenv(secure.ref.Env)
		if !ok || value == "" {
			return deployAzureSecrets{}, fmt.Errorf("%s references environment variable %s, which is not set", secure.field, secure.ref.Env)
		}
		if len(value) < secure.minLength {
			return deployAzureSecrets{}, fmt.Errorf("%s from %s must be at least %d characters", secure.field, secure.ref.Env, secure.minLength)
		}
		if strings.HasPrefix(secure.name, "postgres") {
			if previous := passwords[value]; previous != "" {
				return deployAzureSecrets{}, fmt.Errorf("%s and %s must have different database passwords", previous, secure.field)
			}
			passwords[value] = secure.field
		}
		secrets.entries[secure.name] = map[string]any{"value": value}
		secrets.values = append(secrets.values, value)
	}
	return secrets, nil
}

// deployAzureAvailableSecrets collects env-referenced secret values that are
// set, only so preflight diagnostics can redact them.
func deployAzureAvailableSecrets(config deployAzureConfig, getenv func(string) (string, bool)) deployAzureSecrets {
	secrets := deployAzureSecrets{entries: map[string]any{}}
	for _, secure := range config.secureParameters() {
		if secure.ref.Env == "" {
			continue
		}
		if value, ok := getenv(secure.ref.Env); ok && value != "" {
			secrets.values = append(secrets.values, value)
		}
	}
	return secrets
}

func (s deployAzureSecrets) redact(text string) string {
	for _, value := range s.values {
		text = strings.ReplaceAll(text, value, "***")
	}
	return text
}

func deployAzureParameterFile(parameters map[string]any, secrets deployAzureSecrets) map[string]any {
	entries := map[string]any{}
	for name, value := range parameters {
		entries[name] = map[string]any{"value": value}
	}
	for name, entry := range secrets.entries {
		entries[name] = entry
	}
	return map[string]any{
		"$schema":        "https://schema.management.azure.com/schemas/2019-04-01/deploymentParameters.json#",
		"contentVersion": "1.0.0.0",
		"parameters":     entries,
	}
}

// deployAzureSubscriptionWrapper lets a subscription-scope what-if preview the
// resource group creation together with the resource-group-scoped template.
func deployAzureSubscriptionWrapper(template deployAzureTemplate, document deployAzureTemplateDocument, config deployAzureConfig, names []string) (map[string]any, error) {
	var inner any
	if err := json.Unmarshal(template.Content, &inner); err != nil {
		return nil, err
	}
	declared := map[string]any{}
	passed := map[string]any{}
	for _, name := range names {
		declared[name] = map[string]string{"type": document.Parameters[name].Type}
		passed[name] = map[string]string{"value": "[parameters('" + name + "')]"}
	}
	return map[string]any{
		"$schema":        "https://schema.management.azure.com/schemas/2018-05-01/subscriptionDeploymentTemplate.json#",
		"contentVersion": "1.0.0.0",
		"parameters":     declared,
		"resources": []any{
			map[string]any{
				"type":       "Microsoft.Resources/resourceGroups",
				"apiVersion": "2022-09-01",
				"name":       config.ResourceGroup,
				"location":   config.Location,
			},
			map[string]any{
				"type":          "Microsoft.Resources/deployments",
				"apiVersion":    "2022-09-01",
				"name":          "cao-dashboard",
				"resourceGroup": config.ResourceGroup,
				"dependsOn":     []string{"[subscriptionResourceId('Microsoft.Resources/resourceGroups', '" + config.ResourceGroup + "')]"},
				"properties": map[string]any{
					"mode":                        "Incremental",
					"expressionEvaluationOptions": map[string]string{"scope": "inner"},
					"template":                    inner,
					"parameters":                  passed,
				},
			},
		},
	}, nil
}
