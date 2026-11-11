package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/githubnext/gh-cao/internal/azcli"
)

type deployAzureApplyOptions struct {
	SourceDir, RepoDir, SnapshotDir, ReceiptPath string
	EmptyEvidence                                bool
}

type deployAzureReceipt struct {
	Kind               string                    `json:"kind"`
	SchemaVersion      int                       `json:"schemaVersion"`
	PlanSHA256         string                    `json:"planSha256"`
	StartedAt          string                    `json:"startedAt"`
	CompletedAt        string                    `json:"completedAt"`
	DeploymentName     string                    `json:"deploymentName"`
	DeploymentID       string                    `json:"deploymentId"`
	SourceRevision     string                    `json:"sourceRevision"`
	SourceWorktree     string                    `json:"sourceWorktree"`
	Environment        string                    `json:"environment"`
	ImageAssurance     string                    `json:"imageAssurance"`
	ContextSHA256      string                    `json:"contextSha256"`
	Images             []deployAzureBuiltImage   `json:"images"`
	JobExecution       string                    `json:"jobExecution"`
	ProvisionExecution string                    `json:"provisionExecution,omitempty"`
	Verification       []deployAzureVerification `json:"verification"`
}

func deployAzureExecute(ctx context.Context, deps deployAzureDeps, loaded deployAzureLoadedConfig, template deployAzureTemplate, plan deployAzurePlan, planFile string, options deployAzureApplyOptions) error {
	if loaded.config.Environment != "development" || plan.Environment != "development" {
		return errors.New("source-build apply requires environment: development in the reviewed config and plan; production promotion is unsupported without an independent per-target scan, SBOM and provenance gate")
	}
	var document deployAzureTemplateDocument
	if err := json.Unmarshal(template.Content, &document); err != nil {
		return fmt.Errorf("parse digest-capable template: %w", err)
	}
	for _, name := range []string{"functionImageDigest", "ingestionImageDigest", "provisioningImageDigest"} {
		if document.Parameters[name].Type != "string" {
			return fmt.Errorf("source-build apply requires a digest-capable template with string parameter %s; regenerate the reviewed template and plan", name)
		}
	}
	if options.SourceDir == "" {
		return errors.New("--apply requires --source-dir (a development gh-cao checkout)")
	}
	if (options.SnapshotDir == "") == !options.EmptyEvidence {
		return errors.New("--apply requires exactly one of --snapshot-dir or --empty-evidence")
	}
	if options.ReceiptPath == "" {
		return errors.New("--receipt must not be empty")
	}
	if _, err := os.Lstat(options.ReceiptPath); err == nil {
		return fmt.Errorf("receipt %s already exists; choose a new --receipt path", options.ReceiptPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect receipt: %w", err)
	}
	secrets, err := resolveDeployAzureSecrets(loaded.config, deps.getenv)
	if err != nil {
		return err
	}
	az := deployAzureRedacting(deps.az, secrets)
	workspace, err := newDeployAzureWorkspace()
	if err != nil {
		return err
	}
	defer func() { _ = workspace.Close() }()
	var source deployAzureBuildSource = deployAzureDevSource{directory: options.SourceDir}
	if deps.source != nil {
		source = deps.source(options.SourceDir)
	}
	build, err := prepareDeployAzureBuild(ctx, source, workspace, options)
	if err != nil {
		return err
	}
	registryTemplate, err := deployAzureRegistryTemplate(template)
	if err != nil {
		return err
	}
	// Reuse the complete read-only planner, including freshness and authority
	// checks, immediately before the first Azure write.
	freshPath := filepath.Join(workspace.directory, "rechecked-plan.json")
	recheckTemplate, err := workspace.write("recheck-template.json", json.RawMessage(template.Content))
	if err != nil {
		return err
	}
	if err := deployAzurePlanRun(ctx, io.Discard, deps, loaded.path, recheckTemplate, freshPath); err != nil {
		return err
	}
	fresh, err := readDeployAzurePlan(freshPath)
	if err != nil {
		return err
	}
	if fresh.TenantID != plan.TenantID || fresh.Scope != plan.Scope || fresh.ResourceGroupExists != plan.ResourceGroupExists ||
		fresh.Location != plan.Location || !reflect.DeepEqual(fresh.Parameters, plan.Parameters) ||
		!reflect.DeepEqual(fresh.SecureParameters, plan.SecureParameters) || !reflect.DeepEqual(fresh.WhatIf, plan.WhatIf) ||
		!reflect.DeepEqual(fresh.Topology, plan.Topology) {
		return errors.New("azure plan drift detected; no Azure changes were made; rerun --plan and review the new change set")
	}
	config := loaded.config
	planBytes, err := os.ReadFile(planFile)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(planBytes)
	receipt := deployAzureReceipt{
		Kind: "cao.deploy.azure.receipt", SchemaVersion: 1, PlanSHA256: hex.EncodeToString(sum[:]),
		StartedAt: deps.now().UTC().Format(time.RFC3339), SourceRevision: build.Revision, ContextSHA256: build.Digest,
		SourceWorktree: build.Worktree, Environment: "development", ImageAssurance: "not-evaluated-development-only",
		DeploymentName: "cao-" + hex.EncodeToString(sum[:8]),
	}
	scope := []string{"--subscription", config.Subscription, "--resource-group", config.ResourceGroup}
	run := func(verb []string, extra ...string) (azcli.Result, error) {
		args := append(append(append([]string{}, verb...), scope...), extra...)
		return az.Run(ctx, append(args, "--output", "json")...)
	}
	if !plan.ResourceGroupExists {
		if _, err := az.Run(ctx, "group", "create", "--subscription", config.Subscription, "--name", config.ResourceGroup, "--location", config.Location, "--output", "none"); err != nil {
			return fmt.Errorf("create fresh resource group: %w", err)
		}
	}
	registryPath, err := workspace.write("registry.json", registryTemplate)
	if err != nil {
		return err
	}
	registryParameters, err := workspace.write("registry-parameters.json", deployAzureParameterFile(map[string]any{
		"containerRegistryName": config.Resources.ContainerRegistry, "location": config.Location,
	}, deployAzureSecrets{}))
	if err != nil {
		return err
	}
	if _, err := run([]string{"deployment", "group", "create"}, "--name", receipt.DeploymentName+"-registry", "--template-file", registryPath, "--parameters", "@"+registryParameters); err != nil {
		return fmt.Errorf("create exact template registry: %w", err)
	}
	var outputs struct {
		Outputs map[string]json.RawMessage `json:"outputs"`
	}
	if err := json.Unmarshal(template.Content, &outputs); err != nil {
		return err
	}
	_, provision := outputs.Outputs["provisioningJobName"]
	receipt.Images, err = deployAzureBuildImages(ctx, az, config, build, options, provision)
	if err != nil {
		return err
	}
	templatePath, err := workspace.write("full-template.json", json.RawMessage(template.Content))
	if err != nil {
		return err
	}
	parameters, err := deployAzureImageParameters(config, receipt.Images, options.EmptyEvidence, provision)
	if err != nil {
		return err
	}
	parametersPath, err := workspace.write("apply-parameters.json", deployAzureParameterFile(parameters, secrets))
	if err != nil {
		return err
	}
	result, err := deployAzureCreateTemplate(ctx, deps, az, config, receipt.DeploymentName, templatePath, parametersPath)
	if err != nil {
		return fmt.Errorf("deploy full template (resource group retained for diagnosis): %w", err)
	}
	var deployment struct {
		ID         string `json:"id"`
		Properties struct {
			ProvisioningState string `json:"provisioningState"`
			Outputs           struct {
				FunctionHostName struct {
					Value string `json:"value"`
				} `json:"functionHostName"`
				IngestionJobName struct {
					Value string `json:"value"`
				} `json:"ingestionJobName"`
				ProvisioningJobName struct {
					Value string `json:"value"`
				} `json:"provisioningJobName"`
			} `json:"outputs"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(result.Stdout), &deployment); err != nil {
		return fmt.Errorf("parse deployment result: %w", err)
	}
	if deployment.ID == "" || deployment.Properties.ProvisioningState != "Succeeded" {
		return errors.New("full deployment did not return an ID and Succeeded state")
	}
	receipt.DeploymentID = deployment.ID
	host := deployment.Properties.Outputs.FunctionHostName.Value
	job := deployment.Properties.Outputs.IngestionJobName.Value
	if host != config.Resources.FunctionApp+".azurewebsites.net" || job != config.Resources.FunctionApp+"-ingest" {
		return errors.New("deployment returned unexpected Function App hostname or ingestion job; refusing to contact it")
	}
	if provision {
		provisionJob := deployment.Properties.Outputs.ProvisioningJobName.Value
		if provisionJob != config.Resources.FunctionApp+"-db-provision" {
			return errors.New("deployment returned an unexpected database provision job; refusing to execute it")
		}
	}
	if err := deployAzureReadBackImages(ctx, az, config, receipt.Images); err != nil {
		return err
	}
	if provision {
		provisionJob := deployment.Properties.Outputs.ProvisioningJobName.Value
		receipt.ProvisionExecution, err = deployAzureRunJob(ctx, deps, az, config, provisionJob)
		if err != nil {
			return fmt.Errorf("database provision failed before ingestion: %w", err)
		}
	}
	receipt.JobExecution, err = deployAzureRunJob(ctx, deps, az, config, job)
	if err != nil {
		return err
	}
	if err := deployAzureRestartAndAwaitReferences(ctx, deps, az, config); err != nil {
		return err
	}
	receipt.Verification, err = deployAzureVerify(ctx, deps, az, config, host)
	if err != nil {
		return err
	}
	receipt.CompletedAt = deps.now().UTC().Format(time.RFC3339)
	return writeDeployAzureReceipt(options.ReceiptPath, receipt, secrets)
}

func deployAzureRegistryTemplate(template deployAzureTemplate) (map[string]any, error) {
	var document struct {
		Resources json.RawMessage `json:"resources"`
		Variables map[string]any  `json:"variables"`
	}
	if err := json.Unmarshal(template.Content, &document); err != nil {
		return nil, err
	}
	var resources []map[string]any
	if err := json.Unmarshal(document.Resources, &resources); err != nil {
		var symbolic map[string]map[string]any
		if err := json.Unmarshal(document.Resources, &symbolic); err != nil {
			return nil, fmt.Errorf("parse template resources: %w", err)
		}
		for _, resource := range symbolic {
			resources = append(resources, resource)
		}
	}
	for _, resource := range resources {
		if resource["type"] != "Microsoft.ContainerRegistry/registries" {
			continue
		}
		// Copy the exact resource declaration rather than approximating its SKU,
		// tags or network properties in an imperative `az acr create`.
		delete(resource, "dependsOn")
		return map[string]any{
			"$schema":        "https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#",
			"contentVersion": "1.0.0.0",
			"parameters": map[string]any{
				"containerRegistryName": map[string]string{"type": "string"},
				"location":              map[string]string{"type": "string"},
			},
			"variables": map[string]any{"tags": document.Variables["tags"]},
			"resources": []any{resource},
		}, nil
	}
	return nil, errors.New("template has no supported Container Registry resource")
}

func writeDeployAzureReceipt(path string, receipt deployAzureReceipt, secrets deployAzureSecrets) error {
	content, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	if secrets.redact(string(content)) != string(content) {
		return errors.New("refusing to write a receipt containing a secret")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create receipt: %w", err)
	}
	if _, err := file.Write(append(content, '\n')); err != nil {
		_ = file.Close()
		return fmt.Errorf("write receipt: %w", err)
	}
	return file.Close()
}
