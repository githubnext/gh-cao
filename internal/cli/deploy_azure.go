package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/githubnext/gh-cao/internal/azcli"
	"github.com/githubnext/gh-cao/internal/topology"
)

const (
	deployAzurePlanKind        = "cao.deploy.azure.plan"
	deployAzurePlanVersion     = 2
	defaultDeployAzurePlanPath = ".cao/deploy-azure-plan.json"
)

type deployAzureDeps struct {
	az          azcli.Executor
	getenv      func(string) (string, bool)
	now         func() time.Time
	template    func(path string) deployAzureTemplateSource
	http        *http.Client
	pause       func(context.Context, time.Duration) error
	source      func(string) deployAzureBuildSource
	diagnostics io.Writer
}

func defaultDeployAzureDeps() deployAzureDeps {
	return deployAzureDeps{
		az:          azcli.Process{},
		getenv:      os.LookupEnv,
		now:         time.Now,
		template:    func(path string) deployAzureTemplateSource { return deployAzureFileTemplate{path: path} },
		diagnostics: os.Stderr,
	}
}

func deployCommand(deps deployAzureDeps) *cobra.Command {
	deploy := &cobra.Command{
		Use:   "deploy",
		Short: "Generate, check, plan and apply CAO deployments",
		Long: `Resolve deployment dependencies from generated TOSCA requirements.

init, generate and check prepare offline, review-only deployment bundles from
YAML feature choices and emit a TOSCA 1.3 deployment template. Templates,
examples and runbooks are embedded in Go;
generation requires Microsoft's official Bicep CLI on PATH.

azure plans or explicitly applies a fresh development Functions deployment from
a reviewed JSON config using the same TOSCA requirement resolver and embedded Azure template.
Offline generation never deploys resources or changes GitHub Apps.`,
		Args: cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return fmt.Errorf("deploy requires init, generate, check, or azure")
		},
	}
	deploy.AddCommand(deployAzureCommand(deps))
	addDeployBundleCommands(deploy)
	return deploy
}

func deployAzureCommand(deps deployAzureDeps) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "azure",
		Short: "Plan a fresh CAO dashboard deployment to Azure",
		Long: `Plan a fresh CAO dashboard deployment to Azure from a reviewed config.

The config (default ` + defaultDeployAzureConfigPath + `) names the subscription,
resource group, resources, and access policy. It never contains secret values;
secure parameters reference environment variables or Key Vault secrets.
The embedded Azure template and TOSCA requirement-graph resolver require no gh-cao
checkout for preflight or planning. --template selects an explicit ARM override.

--plan (default) runs read-only preflight checks and what-if, then writes a
secret-free plan file. --preflight runs only the preflight checks (tools,
login, permissions, resource providers, name availability, location, existing
instance, ingress drift, and retention read-back), prints every result, and
fails if any check failed; it writes no plan file. Only fresh deployments are
supported: existing resources fail closed. --apply requires environment: development
in the reviewed config; production source-build deployment is not supported.
It rechecks a reviewed plan, builds immutable images,
deploys, ingests the explicitly selected evidence, and verifies public OAuth ingress.
Updates to existing instances are not supported.`,
		Args: cobra.NoArgs,
	}
	flags := cmd.Flags()
	flags.String("config", defaultDeployAzureConfigPath, "Reviewed deployment config")
	flags.Bool("plan", false, "Preview with read-only checks and what-if (default mode)")
	flags.Bool("preflight", false, "Run read-only preflight checks only; writes no plan file")
	flags.Bool("json", false, "Print preflight results as JSON (with --preflight)")
	flags.Bool("apply", false, "Apply a reviewed plan to a fresh development deployment (not production-assured)")
	flags.String("plan-file", "", "Reviewed plan file required by --apply")
	flags.String("template", "", "Explicit compiled ARM override (default: embedded Azure template)")
	flags.String("out", defaultDeployAzurePlanPath, "Plan file to write")
	flags.String("source-dir", "", "Development gh-cao checkout used as the allowlisted build source")
	flags.String("repo-dir", ".", "Operator repository containing reviewed cao.json and cao.azure.json")
	flags.String("snapshot-dir", "", "Verified dashboard snapshot directory to ingest")
	flags.Bool("empty-evidence", false, "Explicitly initialize without evidence")
	flags.String("receipt", ".cao/deploy-azure-receipt.json", "Secret-free deployment receipt")
	flags.Duration("timeout", 45*time.Minute, "Overall apply timeout")
	cmd.MarkFlagsMutuallyExclusive("snapshot-dir", "empty-evidence")
	cmd.MarkFlagsMutuallyExclusive("plan", "apply", "preflight")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		apply, _ := flags.GetBool("apply")
		planFile, _ := flags.GetString("plan-file")
		configPath, _ := flags.GetString("config")
		templatePath, _ := flags.GetString("template")
		if apply {
			if planFile == "" {
				return errors.New("--apply requires --plan-file from a reviewed --plan run")
			}
			options := deployAzureApplyOptions{}
			options.SourceDir, _ = flags.GetString("source-dir")
			options.RepoDir, _ = flags.GetString("repo-dir")
			options.SnapshotDir, _ = flags.GetString("snapshot-dir")
			options.EmptyEvidence, _ = flags.GetBool("empty-evidence")
			options.ReceiptPath, _ = flags.GetString("receipt")
			timeout, _ := flags.GetDuration("timeout")
			if timeout <= 0 {
				return errors.New("--timeout must be positive")
			}
			ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
			defer cancel()
			applyDeps := deps
			applyDeps.diagnostics = cmd.ErrOrStderr()
			if err := deployAzureApply(ctx, applyDeps, configPath, templatePath, planFile, options); err != nil {
				return err
			}
			_, err := fmt.Fprintf(cmd.OutOrStdout(), "Azure development deployment verified (images not production-assured); receipt written to %s\n", options.ReceiptPath)
			return err
		}
		if flags.Changed("plan-file") {
			return errors.New("--plan-file is only valid with --apply")
		}
		asJSON, _ := flags.GetBool("json")
		if preflight, _ := flags.GetBool("preflight"); preflight {
			if flags.Changed("out") {
				return errors.New("--out is not valid with --preflight, which writes no plan file")
			}
			return deployAzurePreflightRun(cmd.Context(), cmd.OutOrStdout(), deps, configPath, templatePath, asJSON)
		}
		if asJSON {
			return errors.New("--json is only valid with --preflight")
		}
		out, _ := flags.GetString("out")
		return deployAzurePlanRun(cmd.Context(), cmd.OutOrStdout(), deps, configPath, templatePath, out)
	}
	return cmd
}

type deployAzurePlan struct {
	Kind                string                   `json:"kind"`
	SchemaVersion       int                      `json:"schemaVersion"`
	CreatedAt           string                   `json:"createdAt"`
	Config              deployAzurePlanDigest    `json:"config"`
	Template            deployAzurePlanDigest    `json:"template"`
	Subscription        string                   `json:"subscription"`
	TenantID            string                   `json:"tenantId"`
	ResourceGroup       string                   `json:"resourceGroup"`
	Location            string                   `json:"location"`
	Environment         string                   `json:"environment"`
	ResourceGroupExists bool                     `json:"resourceGroupExists"`
	Scope               string                   `json:"scope"`
	Parameters          map[string]any           `json:"parameters"`
	SecureParameters    map[string]string        `json:"secureParameters"`
	WhatIf              deployAzureWhatIfSummary `json:"whatIf"`
	Topology            topology.Topology        `json:"topology"`
}

type deployAzurePlanDigest struct {
	Path   string `json:"path,omitempty"`
	Origin string `json:"origin,omitempty"`
	SHA256 string `json:"sha256"`
}

type deployAzureWhatIfSummary struct {
	Status  string              `json:"status"`
	Counts  map[string]int      `json:"counts"`
	Changes []deployAzureChange `json:"changes"`
}

type deployAzureChange struct {
	ResourceID string `json:"resourceId"`
	ChangeType string `json:"changeType"`
}

func deployAzureLoadTemplate(ctx context.Context, deps deployAzureDeps, path string) (deployAzureTemplate, error) {
	if path == "" {
		return deployAzureEmbeddedTemplate{}.Load(ctx)
	}
	return deps.template(path).Load(ctx)
}

func resolveDeployAzureTopology(ctx context.Context, config deployAzureConfig) (topology.Topology, error) {
	features := topology.Features{
		Deployment: "azure-functions", Acquisition: "actions", Ingress: "public", Analytics: "none", Telemetry: "none",
	}
	if config.Telemetry.OTLPEndpoint != "" {
		features.Telemetry = "otlp"
	}
	var secrets []string
	for _, secure := range config.secureParameters() {
		secrets = append(secrets, secure.name)
	}
	return topology.ResolveAzure(ctx, features, config.parameters(), secrets)
}

func deployAzurePlanRun(ctx context.Context, stdout io.Writer, deps deployAzureDeps, configPath, templatePath, out string) error {
	loaded, err := loadDeployAzureConfig(configPath)
	if err != nil {
		return err
	}
	config := loaded.config
	template, err := deployAzureLoadTemplate(ctx, deps, templatePath)
	if err != nil {
		return err
	}
	parameters := config.parameters()
	var secureNames []string
	for _, secure := range config.secureParameters() {
		secureNames = append(secureNames, secure.name)
	}
	document, err := checkDeployAzureTemplate(template, parameters, secureNames)
	if err != nil {
		return err
	}
	resolved, err := resolveDeployAzureTopology(ctx, config)
	if err != nil {
		return fmt.Errorf("resolve Azure deployment topology: %w", err)
	}
	secrets, err := resolveDeployAzureSecrets(config, deps.getenv)
	if err != nil {
		return err
	}
	az := deployAzureRedacting(azcli.ReadOnly(deps.az), secrets)

	preflight := runDeployAzurePreflight(ctx, az, config, template)
	if err := writeDeployAzurePreflightTable(stdout, preflight.checks); err != nil {
		return err
	}
	if err := preflight.err(); err != nil {
		return err
	}
	account, groupExists := preflight.account, preflight.groupExists

	workspace, err := newDeployAzureWorkspace()
	if err != nil {
		return err
	}
	defer func() { _ = workspace.Close() }()
	parameterPath, err := workspace.write("parameters.json", deployAzureParameterFile(parameters, secrets))
	if err != nil {
		return err
	}
	scope := "resourceGroup"
	var args []string
	if groupExists {
		templateFile, err := workspace.write("template.json", json.RawMessage(template.Content))
		if err != nil {
			return err
		}
		args = []string{"deployment", "group", "what-if", "--subscription", config.Subscription, "--resource-group", config.ResourceGroup,
			"--template-file", templateFile}
	} else {
		scope = "subscription"
		names := append(sortedKeys(parameters), secureNames...)
		wrapper, err := deployAzureSubscriptionWrapper(template, document, config, names)
		if err != nil {
			return err
		}
		templateFile, err := workspace.write("template.json", wrapper)
		if err != nil {
			return err
		}
		args = []string{"deployment", "sub", "what-if", "--subscription", config.Subscription, "--location", config.Location,
			"--template-file", templateFile}
	}
	args = append(args, "--parameters", "@"+parameterPath, "--result-format", "ResourceIdOnly", "--no-pretty-print", "--output", "json")
	result, err := az.Run(ctx, args...)
	if err != nil {
		return fmt.Errorf("what-if failed: %w", err)
	}
	summary, err := parseDeployAzureWhatIf(result.Stdout)
	if err != nil {
		return err
	}

	plan := deployAzurePlan{
		Kind:                deployAzurePlanKind,
		SchemaVersion:       deployAzurePlanVersion,
		CreatedAt:           deps.now().UTC().Format(time.RFC3339),
		Config:              deployAzurePlanDigest{Path: filepath.ToSlash(configPath), SHA256: loaded.digest},
		Template:            deployAzurePlanDigest{Origin: template.Origin, SHA256: template.Digest},
		Subscription:        account.ID,
		TenantID:            account.TenantID,
		ResourceGroup:       config.ResourceGroup,
		Location:            config.Location,
		Environment:         config.Environment,
		ResourceGroupExists: groupExists,
		Scope:               scope,
		Parameters:          parameters,
		SecureParameters:    map[string]string{},
		WhatIf:              summary,
		Topology:            resolved,
	}
	for _, secure := range config.secureParameters() {
		plan.SecureParameters[secure.name] = secure.ref.describe()
	}
	if err := writeDeployAzurePlan(out, plan); err != nil {
		return err
	}
	var counts []string
	for _, kind := range sortedKeys(summary.Counts) {
		counts = append(counts, fmt.Sprintf("%s=%d", kind, summary.Counts[kind]))
	}
	_, err = fmt.Fprintf(stdout, "\nAzure deployment plan written to %s\nsubscription: %s\nresource group: %s (exists: %t)\nwhat-if: %s\nApply supports only reviewed environment: development configs, without production image assurance.\nReview the plan, then run gh cao deploy azure --apply --plan-file %s\n",
		out, plan.Subscription, plan.ResourceGroup, groupExists, strings.Join(counts, ", "), out)
	return err
}

func deployAzurePreflightRun(ctx context.Context, stdout io.Writer, deps deployAzureDeps, configPath, templatePath string, asJSON bool) error {
	loaded, err := loadDeployAzureConfig(configPath)
	if err != nil {
		return err
	}
	config := loaded.config
	template, err := deployAzureLoadTemplate(ctx, deps, templatePath)
	if err != nil {
		return err
	}
	var secureNames []string
	for _, secure := range config.secureParameters() {
		secureNames = append(secureNames, secure.name)
	}
	if _, err := checkDeployAzureTemplate(template, config.parameters(), secureNames); err != nil {
		return err
	}
	if _, err := resolveDeployAzureTopology(ctx, config); err != nil {
		return fmt.Errorf("resolve Azure deployment topology: %w", err)
	}
	// Preflight needs no secret values, but redacts any that are present.
	az := deployAzureRedacting(azcli.ReadOnly(deps.az), deployAzureAvailableSecrets(config, deps.getenv))
	preflight := runDeployAzurePreflight(ctx, az, config, template)
	if asJSON {
		content, err := json.MarshalIndent(preflight.report(), "", "  ")
		if err != nil {
			return err
		}
		if _, err := fmt.Fprintf(stdout, "%s\n", content); err != nil {
			return err
		}
	} else if err := writeDeployAzurePreflightTable(stdout, preflight.checks); err != nil {
		return err
	}
	return preflight.err()
}

func deployAzureRedacting(next azcli.Executor, secrets deployAzureSecrets) azcli.Executor {
	return azcli.ExecutorFunc(func(ctx context.Context, args ...string) (azcli.Result, error) {
		result, err := next.Run(ctx, args...)
		result.Stdout = secrets.redact(result.Stdout)
		result.Stderr = secrets.redact(result.Stderr)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return result, fmt.Errorf("az %s: %w", azcli.CommandName(args), ctxErr)
			}
			if errors.Is(err, context.DeadlineExceeded) {
				return result, fmt.Errorf("az %s: %w", azcli.CommandName(args), context.DeadlineExceeded)
			}
			return result, fmt.Errorf("az %s: %s", azcli.CommandName(args), secrets.redact(err.Error()))
		}
		return result, nil
	})
}

type deployAzureAccountInfo struct {
	ID       string `json:"id"`
	State    string `json:"state"`
	TenantID string `json:"tenantId"`
	User     struct {
		Name string `json:"name"`
		Type string `json:"type"`
	} `json:"user"`
}

func deployAzureAccount(ctx context.Context, az azcli.Executor, subscription string) (deployAzureAccountInfo, error) {
	result, err := az.Run(ctx, "account", "show", "--subscription", subscription, "--output", "json")
	if err != nil {
		return deployAzureAccountInfo{}, fmt.Errorf("check Azure subscription (run az login): %w", err)
	}
	var account deployAzureAccountInfo
	if err := json.Unmarshal([]byte(result.Stdout), &account); err != nil {
		return account, fmt.Errorf("parse az account show: %w", err)
	}
	if !strings.EqualFold(account.ID, subscription) {
		return account, fmt.Errorf("signed-in Azure account resolved subscription %q, want %q", account.ID, subscription)
	}
	if account.State != "Enabled" {
		return account, fmt.Errorf("subscription %s is %q, want Enabled", subscription, account.State)
	}
	return account, nil
}

func deployAzureGroupExists(ctx context.Context, az azcli.Executor, config deployAzureConfig) (bool, error) {
	result, err := az.Run(ctx, "group", "exists", "--subscription", config.Subscription, "--name", config.ResourceGroup, "--output", "json")
	if err != nil {
		return false, fmt.Errorf("check resource group: %w", err)
	}
	switch strings.TrimSpace(result.Stdout) {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("unexpected az group exists output %q", strings.TrimSpace(result.Stdout))
	}
}

func deployAzureResourceList(ctx context.Context, az azcli.Executor, filters ...string) ([]deployAzureResource, error) {
	args := append([]string{"resource", "list"}, filters...)
	result, err := az.Run(ctx, append(args, "--output", "json")...)
	if err != nil {
		return nil, fmt.Errorf("detect existing resources: %w", err)
	}
	var resources []deployAzureResource
	if err := json.Unmarshal([]byte(result.Stdout), &resources); err != nil {
		return nil, fmt.Errorf("parse az resource list: %w", err)
	}
	return resources, nil
}

// Fresh deployments may only create resources; any change to an existing
// resource means an instance already exists.
var deployAzureFreshChangeTypes = map[string]bool{"Create": true, "Deploy": true, "Ignore": true, "Unsupported": true}

func parseDeployAzureWhatIf(output string) (deployAzureWhatIfSummary, error) {
	var raw struct {
		Status  string          `json:"status"`
		Error   json.RawMessage `json:"error"`
		Changes []struct {
			ResourceID string `json:"resourceId"`
			ChangeType string `json:"changeType"`
		} `json:"changes"`
	}
	if err := json.Unmarshal([]byte(output), &raw); err != nil {
		return deployAzureWhatIfSummary{}, fmt.Errorf("parse what-if result: %w", err)
	}
	if raw.Status != "Succeeded" {
		return deployAzureWhatIfSummary{}, fmt.Errorf("what-if status %q, want Succeeded", raw.Status)
	}
	summary := deployAzureWhatIfSummary{Status: raw.Status, Counts: map[string]int{}, Changes: []deployAzureChange{}}
	for _, change := range raw.Changes {
		if !deployAzureFreshChangeTypes[change.ChangeType] {
			return deployAzureWhatIfSummary{}, fmt.Errorf("what-if reports %s for %s; only fresh deployments are supported", change.ChangeType, change.ResourceID)
		}
		summary.Counts[change.ChangeType]++
		summary.Changes = append(summary.Changes, deployAzureChange{ResourceID: change.ResourceID, ChangeType: change.ChangeType})
	}
	sort.Slice(summary.Changes, func(i, j int) bool { return summary.Changes[i].ResourceID < summary.Changes[j].ResourceID })
	return summary, nil
}

func writeDeployAzurePlan(path string, plan deployAzurePlan) error {
	content, err := json.MarshalIndent(plan, "", "  ")
	if err != nil {
		return err
	}
	content = append(content, '\n')
	directory := filepath.Dir(path)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return fmt.Errorf("create plan directory: %w", err)
	}
	temporary, err := os.CreateTemp(directory, ".deploy-azure-plan-*")
	if err != nil {
		return fmt.Errorf("write plan: %w", err)
	}
	defer func() { _ = os.Remove(temporary.Name()) }()
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write plan: %w", err)
	}
	if err := temporary.Chmod(0o644); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write plan: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("write plan: %w", err)
	}
	if err := os.Rename(temporary.Name(), path); err != nil {
		return fmt.Errorf("write plan: %w", err)
	}
	return nil
}

func readDeployAzurePlan(path string) (deployAzurePlan, error) {
	var plan deployAzurePlan
	content, err := os.ReadFile(path)
	if err != nil {
		return plan, fmt.Errorf("read plan file: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&plan); err != nil {
		return plan, fmt.Errorf("invalid plan file %s: %w", path, err)
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return plan, fmt.Errorf("invalid plan file %s: unexpected trailing content", path)
	}
	switch {
	case plan.Kind != deployAzurePlanKind || plan.SchemaVersion != deployAzurePlanVersion:
		return plan, fmt.Errorf("invalid plan file %s: expected %s version %d; rerun --plan", path, deployAzurePlanKind, deployAzurePlanVersion)
	case plan.WhatIf.Status != "Succeeded":
		return plan, fmt.Errorf("invalid plan file %s: what-if did not succeed", path)
	case plan.Config.SHA256 == "" || plan.Template.SHA256 == "":
		return plan, fmt.Errorf("invalid plan file %s: missing config or template digest", path)
	}
	if _, err := time.Parse(time.RFC3339, plan.CreatedAt); err != nil {
		return plan, fmt.Errorf("invalid plan file %s: createdAt: %w", path, err)
	}
	if err := topology.CheckTopology(plan.Topology); err != nil {
		return plan, fmt.Errorf("invalid plan file %s: %w; rerun --plan", path, err)
	}
	return plan, nil
}

func deployAzureApply(ctx context.Context, deps deployAzureDeps, configPath, templatePath, planFile string, options ...deployAzureApplyOptions) error {
	plan, err := readDeployAzurePlan(planFile)
	if err != nil {
		return err
	}
	loaded, err := loadDeployAzureConfig(configPath)
	if err != nil {
		return err
	}
	if loaded.digest != plan.Config.SHA256 {
		return fmt.Errorf("plan file %s was created from a different config; rerun --plan", planFile)
	}
	if !strings.EqualFold(loaded.config.Subscription, plan.Subscription) || loaded.config.ResourceGroup != plan.ResourceGroup {
		return fmt.Errorf("plan file %s targets a different subscription or resource group; rerun --plan", planFile)
	}
	template, err := deployAzureLoadTemplate(ctx, deps, templatePath)
	if err != nil {
		return err
	}
	if template.Digest != plan.Template.SHA256 {
		return fmt.Errorf("plan file %s was created from a different template; rerun --plan", planFile)
	}
	resolved, err := resolveDeployAzureTopology(ctx, loaded.config)
	if err != nil {
		return fmt.Errorf("resolve Azure deployment topology: %w", err)
	}
	if !reflect.DeepEqual(resolved, plan.Topology) {
		return fmt.Errorf("plan file %s was created from a different topology; rerun --plan", planFile)
	}
	if len(options) != 1 {
		return errors.New("--apply requires --source-dir and exactly one of --snapshot-dir or --empty-evidence")
	}
	return deployAzureExecute(ctx, deps, loaded, template, plan, planFile, options[0])
}

func sortedKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
