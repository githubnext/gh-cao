package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/githubnext/gh-cao/internal/azcli"
)

const (
	deployAzurePreflightKind    = "cao.deploy.azure.preflight"
	deployAzurePreflightVersion = 1
	// deployAzureMinimumAzVersion is the oldest Azure CLI exercised with the
	// read-only checks and what-if options used here.
	deployAzureMinimumAzVersion = "2.60.0"

	deployAzureCheckPass    = "pass"
	deployAzureCheckFail    = "fail"
	deployAzureCheckUnknown = "unknown"
	deployAzureCheckInfo    = "info"
)

// deployAzureRequiredProviders are always checked; namespaces discovered in
// the template are added to this set.
var deployAzureRequiredProviders = []string{
	"Microsoft.App", "Microsoft.ContainerRegistry", "Microsoft.DBforPostgreSQL", "Microsoft.Insights", "Microsoft.KeyVault",
	"Microsoft.Network", "Microsoft.OperationalInsights", "Microsoft.Storage", "Microsoft.Web",
}

// Namespaces that are registered in every subscription and cannot be unregistered.
var deployAzureImplicitProviders = map[string]bool{"microsoft.resources": true, "microsoft.authorization": true}

// deployAzureCheck is one structured preflight result.
type deployAzureCheck struct {
	ID          string `json:"id"`
	Status      string `json:"status"`
	Message     string `json:"message"`
	Remediation string `json:"remediation,omitempty"`
}

type deployAzurePreflightReport struct {
	Kind          string             `json:"kind"`
	SchemaVersion int                `json:"schemaVersion"`
	Status        string             `json:"status"`
	Checks        []deployAzureCheck `json:"checks"`
}

type deployAzureResource struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

type deployAzureProvider struct {
	Namespace         string `json:"namespace"`
	RegistrationState string `json:"registrationState"`
	ResourceTypes     []struct {
		ResourceType string   `json:"resourceType"`
		Locations    []string `json:"locations"`
	} `json:"resourceTypes"`
}

// deployAzurePreflight runs read-only checks and retains facts that plan mode reuses.
type deployAzurePreflight struct {
	az       azcli.Executor
	config   deployAzureConfig
	template deployAzureTemplateFacts

	checks      []deployAzureCheck
	azAvailable bool
	loggedIn    bool
	account     deployAzureAccountInfo
	groupKnown  bool
	groupExists bool
	groupItems  []deployAzureResource
	existing    []deployAzureResource
	siteID      string
	providers   []deployAzureProvider
	providerErr error
	timedOut    bool
}

func runDeployAzurePreflight(ctx context.Context, az azcli.Executor, config deployAzureConfig, template deployAzureTemplate) *deployAzurePreflight {
	p := &deployAzurePreflight{az: az, config: config, template: deployAzureTemplateFactsFrom(template.Content, config.parameters())}
	p.az = azcli.ExecutorFunc(func(ctx context.Context, args ...string) (azcli.Result, error) {
		bounded, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		result, err := az.Run(bounded, args...)
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(bounded.Err(), context.DeadlineExceeded) {
			p.timedOut = true
			return result, fmt.Errorf("preflight subprocess az %s timed out after at most one minute: %w", azcli.CommandName(args), context.DeadlineExceeded)
		}
		return result, err
	})
	for _, check := range []func(context.Context) deployAzureCheck{
		p.checkTools,
		p.checkLogin,
		p.checkExisting,
		p.checkPermissions,
		p.checkProviders,
		p.checkLocation,
		p.checkStorageName,
		p.checkRegistryName,
		p.checkKeyVaultName,
		p.checkFunctionAppName,
		p.checkPostgresName,
		p.checkIngress,
		p.checkRetention,
	} {
		p.timedOut = false
		result := check(ctx)
		if p.timedOut {
			result.Status = deployAzureCheckUnknown
		}
		p.checks = append(p.checks, result)
	}
	return p
}

func (p *deployAzurePreflight) report() deployAzurePreflightReport {
	status := deployAzureCheckPass
	if p.failed() {
		status = deployAzureCheckFail
	}
	return deployAzurePreflightReport{Kind: deployAzurePreflightKind, SchemaVersion: deployAzurePreflightVersion, Status: status, Checks: p.checks}
}

func (p *deployAzurePreflight) failed() bool {
	for _, check := range p.checks {
		if check.Status == deployAzureCheckFail || check.Status == deployAzureCheckUnknown {
			return true
		}
	}
	return false
}

func (p *deployAzurePreflight) err() error {
	var failures []string
	for _, check := range p.checks {
		if check.Status == deployAzureCheckFail || check.Status == deployAzureCheckUnknown {
			failures = append(failures, check.ID+": "+check.Message)
		}
	}
	if len(failures) == 0 {
		return nil
	}
	return fmt.Errorf("preflight failed (%d check(s)): %s", len(failures), strings.Join(failures, "; "))
}

func writeDeployAzurePreflightTable(w io.Writer, checks []deployAzureCheck) error {
	table := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	_, _ = fmt.Fprintln(table, "STATUS\tCHECK\tMESSAGE")
	counts := map[string]int{}
	for _, check := range checks {
		counts[check.Status]++
		_, _ = fmt.Fprintf(table, "%s\t%s\t%s\n", strings.ToUpper(check.Status), check.ID, check.Message)
	}
	if err := table.Flush(); err != nil {
		return err
	}
	var remediation []string
	for _, check := range checks {
		if check.Remediation != "" && check.Status != deployAzureCheckPass {
			remediation = append(remediation, fmt.Sprintf("  %s: %s", check.ID, check.Remediation))
		}
	}
	if len(remediation) > 0 {
		if _, err := fmt.Fprintf(w, "\nRemediation:\n%s\n", strings.Join(remediation, "\n")); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(w, "\nPreflight: %d pass, %d fail, %d unknown, %d info\n",
		counts[deployAzureCheckPass], counts[deployAzureCheckFail], counts[deployAzureCheckUnknown], counts[deployAzureCheckInfo])
	return err
}

func deployAzureCheckResult(id, status, message, remediation string) deployAzureCheck {
	return deployAzureCheck{ID: id, Status: status, Message: message, Remediation: remediation}
}

// skipped returns a non-nil unknown result when a prerequisite check failed.
func (p *deployAzurePreflight) skipped(id string) *deployAzureCheck {
	switch {
	case !p.azAvailable:
		check := deployAzureCheckResult(id, deployAzureCheckUnknown, "not checked: the Azure CLI is unavailable", "Resolve the tools check, then rerun preflight.")
		return &check
	case !p.loggedIn:
		check := deployAzureCheckResult(id, deployAzureCheckUnknown, "not checked: the login check did not pass", "Resolve the login check, then rerun preflight.")
		return &check
	}
	return nil
}

// azError summarizes an Azure CLI failure on one line.
func azError(err error) string {
	for _, line := range strings.Split(err.Error(), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			if len(line) > 300 {
				line = line[:300] + "…"
			}
			return line
		}
	}
	return "unknown error"
}

func (p *deployAzurePreflight) run(ctx context.Context, args ...string) (string, error) {
	result, err := p.az.Run(ctx, args...)
	return result.Stdout, err
}

func (p *deployAzurePreflight) runJSON(ctx context.Context, target any, args ...string) error {
	stdout, err := p.run(ctx, args...)
	if err != nil {
		return err
	}
	if err := json.Unmarshal([]byte(stdout), target); err != nil {
		return fmt.Errorf("parse az %s output: %w", azcli.CommandName(args), err)
	}
	return nil
}

func (p *deployAzurePreflight) checkTools(ctx context.Context) deployAzureCheck {
	const id = "tools"
	var version map[string]any
	if err := p.runJSON(ctx, &version, "version", "--output", "json"); err != nil {
		return deployAzureCheckResult(id, deployAzureCheckFail, "Azure CLI is unavailable: "+azError(err),
			"Install the Azure CLI "+deployAzureMinimumAzVersion+" or later (https://aka.ms/azure-cli) and ensure az is on PATH.")
	}
	p.azAvailable = true
	installed, _ := version["azure-cli"].(string)
	bicep := "Bicep is not required: the compiled ARM template is used directly"
	newer, ok := deployAzureVersionAtLeast(installed, deployAzureMinimumAzVersion)
	switch {
	case !ok:
		return deployAzureCheckResult(id, deployAzureCheckUnknown, fmt.Sprintf("could not read the Azure CLI version from az version (got %q); %s", installed, bicep),
			"Run az version and confirm azure-cli is "+deployAzureMinimumAzVersion+" or later.")
	case !newer:
		return deployAzureCheckResult(id, deployAzureCheckFail, fmt.Sprintf("Azure CLI %s is older than the required %s; %s", installed, deployAzureMinimumAzVersion, bicep),
			"Run az upgrade (or reinstall the Azure CLI) to "+deployAzureMinimumAzVersion+" or later.")
	}
	return deployAzureCheckResult(id, deployAzureCheckPass, fmt.Sprintf("Azure CLI %s (minimum %s); %s", installed, deployAzureMinimumAzVersion, bicep), "")
}

var deployAzureVersionPattern = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)`)

func deployAzureVersionAtLeast(installed, minimum string) (bool, bool) {
	have := deployAzureVersionPattern.FindStringSubmatch(installed)
	want := deployAzureVersionPattern.FindStringSubmatch(minimum)
	if have == nil || want == nil {
		return false, false
	}
	for index := 1; index <= 3; index++ {
		h, _ := strconv.Atoi(have[index])
		w, _ := strconv.Atoi(want[index])
		if h != w {
			return h > w, true
		}
	}
	return true, true
}

func (p *deployAzurePreflight) checkLogin(ctx context.Context) deployAzureCheck {
	const id = "login"
	if !p.azAvailable {
		return *p.skipped(id)
	}
	account, err := deployAzureAccount(ctx, p.az, p.config.Subscription)
	p.account = account
	if err != nil {
		return deployAzureCheckResult(id, deployAzureCheckFail, azError(err),
			fmt.Sprintf("Run az login with an identity that can access subscription %s, and confirm the subscription is Enabled and matches config.subscription.", p.config.Subscription))
	}
	p.loggedIn = true
	principal := account.User.Type
	if principal == "" {
		principal = "unknown principal type"
	}
	return deployAzureCheckResult(id, deployAzureCheckPass,
		fmt.Sprintf("subscription %s is Enabled; tenant %s; signed in as %s", account.ID, account.TenantID, principal), "")
}

func (p *deployAzurePreflight) checkExisting(ctx context.Context) deployAzureCheck {
	const id = "existing-instance"
	const freshOnly = "only fresh deployments are supported; upgrading an existing instance is not yet available"
	const remediation = "Choose unused names and an empty or new resource group in the config; upgrades of existing instances are not yet supported."
	if check := p.skipped(id); check != nil {
		// Existing-instance detection is fail-closed: unknown is never a pass.
		check.Status = deployAzureCheckFail
		return *check
	}
	exists, err := deployAzureGroupExists(ctx, p.az, p.config)
	if err != nil {
		return deployAzureCheckResult(id, deployAzureCheckFail, "could not determine whether an instance exists: "+azError(err), remediation)
	}
	p.groupKnown, p.groupExists = true, exists
	var findings []string
	if exists {
		resources, err := deployAzureResourceList(ctx, p.az, "--subscription", p.config.Subscription, "--resource-group", p.config.ResourceGroup)
		if err != nil {
			return deployAzureCheckResult(id, deployAzureCheckFail, "could not determine whether an instance exists: "+azError(err), remediation)
		}
		p.groupItems = resources
		p.existing = append(p.existing, resources...)
		if len(resources) > 0 {
			findings = append(findings, fmt.Sprintf("resource group %s already contains %d resource(s) (for example %s)", p.config.ResourceGroup, len(resources), resources[0].ID))
		}
	}
	for _, name := range p.config.globalResourceNames() {
		resources, err := deployAzureResourceList(ctx, p.az, "--subscription", p.config.Subscription, "--name", name)
		if err != nil {
			return deployAzureCheckResult(id, deployAzureCheckFail, "could not determine whether an instance exists: "+azError(err), remediation)
		}
		if len(resources) > 0 {
			p.existing = append(p.existing, resources...)
			findings = append(findings, fmt.Sprintf("resource %s already exists in subscription %s (%s)", name, p.config.Subscription, resources[0].ID))
		}
	}
	for _, resource := range p.existing {
		if strings.EqualFold(resource.Type, "Microsoft.Web/sites") && strings.EqualFold(deployAzureResourceName(resource.ID), p.config.Resources.FunctionApp) {
			p.siteID = resource.ID
			break
		}
	}
	if len(findings) > 0 {
		return deployAzureCheckResult(id, deployAzureCheckFail, strings.Join(findings, "; ")+"; "+freshOnly, remediation)
	}
	return deployAzureCheckResult(id, deployAzureCheckPass,
		fmt.Sprintf("no existing instance: resource group %s exists=%t and configured names are unused in the subscription", p.config.ResourceGroup, exists), "")
}

func deployAzureResourceName(id string) string {
	return id[strings.LastIndex(id, "/")+1:]
}

func (p *deployAzurePreflight) targetScope() string {
	scope := "/subscriptions/" + p.config.Subscription
	if p.groupExists {
		scope += "/resourceGroups/" + p.config.ResourceGroup
	}
	return scope
}

func (p *deployAzurePreflight) loadProviders(ctx context.Context) error {
	if p.providers != nil || p.providerErr != nil {
		return p.providerErr
	}
	var providers []deployAzureProvider
	p.providerErr = p.runJSON(ctx, &providers, "provider", "list", "--subscription", p.config.Subscription,
		"--query", "[].{namespace:namespace,registrationState:registrationState,resourceTypes:resourceTypes[].{resourceType:resourceType,locations:locations}}",
		"--output", "json")
	p.providers = providers
	return p.providerErr
}

func (p *deployAzurePreflight) requiredProviders() []string {
	set := map[string]string{}
	for _, namespace := range deployAzureRequiredProviders {
		set[strings.ToLower(namespace)] = namespace
	}
	for _, resourceType := range p.template.types {
		namespace, _, _ := strings.Cut(resourceType, "/")
		if !deployAzureImplicitProviders[strings.ToLower(namespace)] {
			if _, ok := set[strings.ToLower(namespace)]; !ok {
				set[strings.ToLower(namespace)] = namespace
			}
		}
	}
	namespaces := make([]string, 0, len(set))
	for _, namespace := range set {
		namespaces = append(namespaces, namespace)
	}
	sort.Strings(namespaces)
	return namespaces
}

func (p *deployAzurePreflight) provider(namespace string) *deployAzureProvider {
	for index := range p.providers {
		if strings.EqualFold(p.providers[index].Namespace, namespace) {
			return &p.providers[index]
		}
	}
	return nil
}

func (p *deployAzurePreflight) checkProviders(ctx context.Context) deployAzureCheck {
	const id = "resource-providers"
	if check := p.skipped(id); check != nil {
		return *check
	}
	required := p.requiredProviders()
	if err := p.loadProviders(ctx); err != nil {
		return deployAzureCheckResult(id, deployAzureCheckUnknown, "could not list resource providers: "+azError(err),
			"Confirm these providers are Registered in the subscription: "+strings.Join(required, ", ")+".")
	}
	var unregistered, commands []string
	for _, namespace := range required {
		state := "NotFound"
		if provider := p.provider(namespace); provider != nil {
			state = provider.RegistrationState
		}
		if state != "Registered" {
			unregistered = append(unregistered, fmt.Sprintf("%s (%s)", namespace, state))
			commands = append(commands, fmt.Sprintf("az provider register --subscription %s --namespace %s", p.config.Subscription, namespace))
		}
	}
	if len(unregistered) > 0 {
		return deployAzureCheckResult(id, deployAzureCheckFail, "resource providers are not registered: "+strings.Join(unregistered, ", "),
			"A subscription owner must register them, then wait for Registered: "+strings.Join(commands, "; "))
	}
	return deployAzureCheckResult(id, deployAzureCheckPass, fmt.Sprintf("%d required providers registered: %s", len(required), strings.Join(required, ", ")), "")
}

func deployAzureNormalizeLocation(location string) string {
	return strings.ToLower(strings.ReplaceAll(location, " ", ""))
}

func (p *deployAzurePreflight) checkLocation(ctx context.Context) deployAzureCheck {
	const id = "location"
	if check := p.skipped(id); check != nil {
		return *check
	}
	location := deployAzureNormalizeLocation(p.config.Location)
	var unsupported, undetermined []string
	if err := p.loadProviders(ctx); err != nil {
		undetermined = append(undetermined, "resource type locations ("+azError(err)+")")
	} else {
		for _, resourceType := range p.template.types {
			namespace, typeName, _ := strings.Cut(resourceType, "/")
			if strings.Contains(typeName, "/") || deployAzureImplicitProviders[strings.ToLower(namespace)] {
				continue
			}
			provider := p.provider(namespace)
			if provider == nil {
				undetermined = append(undetermined, resourceType)
				continue
			}
			found := false
			for _, candidate := range provider.ResourceTypes {
				if !strings.EqualFold(candidate.ResourceType, typeName) {
					continue
				}
				found = true
				supported := len(candidate.Locations) == 0
				for _, available := range candidate.Locations {
					normalized := deployAzureNormalizeLocation(available)
					if normalized == location || normalized == "global" {
						supported = true
					}
				}
				if !supported {
					unsupported = append(unsupported, resourceType)
				}
			}
			if !found {
				undetermined = append(undetermined, resourceType)
			}
		}
	}
	if sku := p.template.planSKU; sku == "" {
		undetermined = append(undetermined, "App Service plan SKU (not found in template)")
	} else {
		var locations []struct {
			Name string `json:"name"`
		}
		var locationErr error
		if sku == "EP1" || sku == "EP2" || sku == "EP3" {
			var regions struct {
				Value []struct {
					Name string `json:"name"`
				} `json:"value"`
			}
			locationErr = p.runJSON(ctx, &regions, "rest", "--method", "get", "--url",
				"/subscriptions/"+p.config.Subscription+"/providers/Microsoft.Web/geoRegions?api-version=2024-11-01&sku="+sku+"&linuxWorkersEnabled=true", "--output", "json")
			locations = regions.Value
		} else {
			locationErr = p.runJSON(ctx, &locations, "appservice", "list-locations", "--subscription", p.config.Subscription, "--sku", sku, "--linux-workers-enabled", "--output", "json")
		}
		if locationErr != nil {
			undetermined = append(undetermined, "App Service plan "+sku+" ("+azError(locationErr)+")")
		} else {
			offered := false
			for _, candidate := range locations {
				offered = offered || deployAzureNormalizeLocation(candidate.Name) == location
			}
			if !offered {
				unsupported = append(unsupported, "App Service plan "+sku+" on Linux")
			}
		}
	}
	if p.template.postgresSKU == "" || p.template.postgresTier == "" {
		undetermined = append(undetermined, "PostgreSQL SKU (not found in template)")
	} else {
		var capabilities any
		label := fmt.Sprintf("PostgreSQL %s %s", p.template.postgresTier, p.template.postgresSKU)
		if err := p.runJSON(ctx, &capabilities, "postgres", "flexible-server", "list-skus", "--subscription", p.config.Subscription, "--location", p.config.Location, "--output", "json"); err != nil {
			undetermined = append(undetermined, label+" ("+azError(err)+")")
		} else {
			switch deployAzureFindSKU(capabilities, p.template.postgresTier, p.template.postgresSKU) {
			case deployAzureCheckPass:
			case deployAzureCheckFail:
				unsupported = append(unsupported, label)
			default:
				undetermined = append(undetermined, label+" (unrecognized capability output)")
			}
		}
	}
	switch {
	case len(unsupported) > 0:
		return deployAzureCheckResult(id, deployAzureCheckFail, fmt.Sprintf("location %s does not offer: %s", p.config.Location, strings.Join(unsupported, ", ")),
			"Choose a location that offers every required resource type and SKU, or request quota/access for this region.")
	case len(undetermined) > 0:
		return deployAzureCheckResult(id, deployAzureCheckUnknown, fmt.Sprintf("could not confirm location %s supports: %s", p.config.Location, strings.Join(undetermined, ", ")),
			"Confirm availability in the Azure portal or with az provider show / az postgres flexible-server list-skus before applying.")
	}
	return deployAzureCheckResult(id, deployAzureCheckPass, fmt.Sprintf("location %s offers the required resource types, App Service plan %s, and PostgreSQL %s %s",
		p.config.Location, p.template.planSKU, p.template.postgresTier, p.template.postgresSKU), "")
}

// deployAzureFindSKU searches capability output for an object named tier that
// contains an object named sku. It returns pass, fail (tier found without the
// SKU), or unknown (tier not found, so the output shape is unrecognized).
func deployAzureFindSKU(value any, tier, sku string) string {
	result := deployAzureCheckUnknown
	var visit func(any, bool)
	visit = func(node any, inTier bool) {
		switch typed := node.(type) {
		case map[string]any:
			name, _ := typed["name"].(string)
			if inTier && strings.EqualFold(name, sku) {
				result = deployAzureCheckPass
				return
			}
			nowInTier := inTier
			if !inTier && strings.EqualFold(name, tier) {
				nowInTier = true
				if result == deployAzureCheckUnknown {
					result = deployAzureCheckFail
				}
			}
			for _, child := range typed {
				visit(child, nowInTier)
				if result == deployAzureCheckPass {
					return
				}
			}
		case []any:
			for _, child := range typed {
				visit(child, inTier)
				if result == deployAzureCheckPass {
					return
				}
			}
		}
	}
	visit(value, false)
	return result
}

type deployAzureNameResult struct {
	NameAvailable *bool  `json:"nameAvailable"`
	Reason        string `json:"reason"`
	Message       string `json:"message"`
}

func (p *deployAzurePreflight) nameCheck(ctx context.Context, id, label, field, name string, args ...string) (deployAzureCheck, bool) {
	var result deployAzureNameResult
	if err := p.runJSON(ctx, &result, args...); err != nil {
		return deployAzureCheckResult(id, deployAzureCheckUnknown, fmt.Sprintf("could not check %s name %s: %s", label, name, azError(err)),
			fmt.Sprintf("Confirm %s %s is globally available before applying.", label, name)), false
	}
	if result.NameAvailable == nil {
		return deployAzureCheckResult(id, deployAzureCheckUnknown, fmt.Sprintf("name availability response for %s %s did not include nameAvailable", label, name),
			fmt.Sprintf("Confirm %s %s is globally available before applying.", label, name)), false
	}
	if !*result.NameAvailable {
		detail := strings.TrimSpace(strings.Trim(result.Reason+": "+result.Message, ": "))
		if detail == "" {
			detail = "no reason given"
		}
		return deployAzureCheckResult(id, deployAzureCheckFail, fmt.Sprintf("%s name %s is not available (%s)", label, name, detail),
			fmt.Sprintf("Choose a different %s in the config; %s names are globally unique.", field, label)), false
	}
	return deployAzureCheckResult(id, deployAzureCheckPass, fmt.Sprintf("%s name %s is available", label, name), ""), true
}

func (p *deployAzurePreflight) restNameArgs(provider, resourceType, name string) []string {
	apiVersion := p.template.apiVersions[strings.ToLower(resourceType)]
	if apiVersion == "" {
		apiVersion = "2023-01-01"
	}
	body, _ := json.Marshal(map[string]string{"name": name, "type": resourceType})
	path := fmt.Sprintf("/subscriptions/%s/providers/%s", p.config.Subscription, provider)
	if provider == "Microsoft.DBforPostgreSQL" {
		path += "/locations/" + p.config.Location
	}
	return []string{"rest", "--method", "post",
		"--url", fmt.Sprintf("%s/checkNameAvailability?api-version=%s", path, apiVersion),
		"--body", string(body), "--output", "json"}
}

func (p *deployAzurePreflight) checkStorageName(ctx context.Context) deployAzureCheck {
	const id = "name-storage-account"
	if check := p.skipped(id); check != nil {
		return *check
	}
	name := p.config.Resources.StorageAccount
	check, _ := p.nameCheck(ctx, id, "storage account", "resources.storageAccount", name,
		"storage", "account", "check-name", "--subscription", p.config.Subscription, "--name", name, "--output", "json")
	return check
}

func (p *deployAzurePreflight) checkRegistryName(ctx context.Context) deployAzureCheck {
	const id = "name-container-registry"
	if check := p.skipped(id); check != nil {
		return *check
	}
	name := p.config.Resources.ContainerRegistry
	check, _ := p.nameCheck(ctx, id, "container registry", "resources.containerRegistry", name,
		"acr", "check-name", "--subscription", p.config.Subscription, "--name", name, "--output", "json")
	return check
}

func (p *deployAzurePreflight) checkKeyVaultName(ctx context.Context) deployAzureCheck {
	const id = "name-key-vault"
	if check := p.skipped(id); check != nil {
		return *check
	}
	name := p.config.Resources.KeyVault
	check, available := p.nameCheck(ctx, id, "Key Vault", "resources.keyVault", name, p.restNameArgs("Microsoft.KeyVault", "Microsoft.KeyVault/vaults", name)...)
	if check.Status == deployAzureCheckFail {
		return check
	}
	var deleted []struct {
		Name       string `json:"name"`
		Properties struct {
			Location           string `json:"location"`
			ScheduledPurgeDate string `json:"scheduledPurgeDate"`
		} `json:"properties"`
	}
	if err := p.runJSON(ctx, &deleted, "keyvault", "list-deleted", "--subscription", p.config.Subscription, "--resource-type", "vault", "--output", "json"); err != nil {
		return deployAzureCheckResult(id, deployAzureCheckUnknown, fmt.Sprintf("could not list soft-deleted Key Vaults: %s", azError(err)),
			fmt.Sprintf("Run az keyvault list-deleted and confirm %s is not soft-deleted.", name))
	}
	for _, vault := range deleted {
		if strings.EqualFold(vault.Name, name) {
			return deployAzureCheckResult(id, deployAzureCheckFail,
				fmt.Sprintf("Key Vault %s is soft-deleted in this subscription (location %s, scheduled purge %s)", name, vault.Properties.Location, vault.Properties.ScheduledPurgeDate),
				fmt.Sprintf("Choose a different resources.keyVault, or have an authorized operator recover (az keyvault recover --name %s) or purge (az keyvault purge --name %s) it after review.", name, name))
		}
	}
	if !available {
		return check
	}
	check.Message += " and not soft-deleted in this subscription"
	return check
}

func (p *deployAzurePreflight) checkFunctionAppName(ctx context.Context) deployAzureCheck {
	const id = "name-function-app"
	if check := p.skipped(id); check != nil {
		return *check
	}
	name := p.config.Resources.FunctionApp
	check, _ := p.nameCheck(ctx, id, "Function App", "resources.functionApp", name, p.restNameArgs("Microsoft.Web", "Microsoft.Web/sites", name)...)
	return check
}

func (p *deployAzurePreflight) checkPostgresName(ctx context.Context) deployAzureCheck {
	const id = "name-postgres-server"
	if check := p.skipped(id); check != nil {
		return *check
	}
	name := p.config.postgresServerName()
	check, _ := p.nameCheck(ctx, id, "PostgreSQL server", "resources.postgresServer", name,
		p.restNameArgs("Microsoft.DBforPostgreSQL", "Microsoft.DBforPostgreSQL/flexibleServers", name)...)
	return check
}

type deployAzureIngress struct {
	PublicNetworkAccess                    string            `json:"publicNetworkAccess"`
	IPSecurityRestrictionsDefaultAction    string            `json:"ipSecurityRestrictionsDefaultAction"`
	IPSecurityRestrictions                 []json.RawMessage `json:"ipSecurityRestrictions"`
	SCMIPSecurityRestrictionsDefaultAction string            `json:"scmIpSecurityRestrictionsDefaultAction"`
	SCMIPSecurityRestrictionsUseMain       *bool             `json:"scmIpSecurityRestrictionsUseMain"`
}

func (p *deployAzurePreflight) checkIngress(ctx context.Context) deployAzureCheck {
	const id = "ingress-drift"
	const remediation = "Report only. Before any future upgrade, carry approved ingress forward with functionPublicNetworkAccess, functionAccessRestrictionsDefaultAction, functionAccessRestrictions, functionScmAccessRestrictions, and functionScmAccessRestrictionsUseMain."
	if p.siteID == "" {
		return deployAzureCheckResult(id, deployAzureCheckInfo, "no existing Function App found; nothing to compare", "")
	}
	apiVersion := p.template.apiVersions["microsoft.web/sites"]
	if apiVersion == "" {
		apiVersion = "2023-12-01"
	}
	var site, config deployAzureIngress
	if err := p.runJSON(ctx, &site, "resource", "show", "--ids", p.siteID, "--api-version", apiVersion,
		"--query", "{publicNetworkAccess:properties.publicNetworkAccess}", "--output", "json"); err != nil {
		return deployAzureCheckResult(id, deployAzureCheckInfo, "could not read existing Function App ingress: "+azError(err), remediation)
	}
	if err := p.runJSON(ctx, &config, "resource", "show", "--ids", p.siteID+"/config/web", "--api-version", apiVersion,
		"--query", "{ipSecurityRestrictionsDefaultAction:properties.ipSecurityRestrictionsDefaultAction,ipSecurityRestrictions:properties.ipSecurityRestrictions,scmIpSecurityRestrictionsDefaultAction:properties.scmIpSecurityRestrictionsDefaultAction,scmIpSecurityRestrictionsUseMain:properties.scmIpSecurityRestrictionsUseMain}",
		"--output", "json"); err != nil {
		return deployAzureCheckResult(id, deployAzureCheckInfo, "could not read existing Function App access restrictions: "+azError(err), remediation)
	}
	defaults := p.template.ingress
	useMain := "unset"
	if config.SCMIPSecurityRestrictionsUseMain != nil {
		useMain = strconv.FormatBool(*config.SCMIPSecurityRestrictionsUseMain)
	}
	rules := deployAzureCountExplicitRules(config.IPSecurityRestrictions)
	fields := []struct{ name, live, template string }{
		{"publicNetworkAccess", site.PublicNetworkAccess, defaults.publicNetworkAccess},
		{"main defaultAction", config.IPSecurityRestrictionsDefaultAction, defaults.mainDefaultAction},
		{"main rules", strconv.Itoa(rules), strconv.Itoa(defaults.mainRules)},
		{"scm defaultAction", config.SCMIPSecurityRestrictionsDefaultAction, defaults.scmDefaultAction},
		{"scmIpSecurityRestrictionsUseMain", useMain, defaults.scmUseMain},
	}
	var parts, drift []string
	for _, field := range fields {
		parts = append(parts, fmt.Sprintf("%s=%s (template %s)", field.name, valueOr(field.live, "unset"), valueOr(field.template, "unknown")))
		if !strings.EqualFold(field.live, field.template) {
			drift = append(drift, field.name)
		}
	}
	summary := "matches template defaults"
	if len(drift) > 0 {
		summary = "differs from template defaults in " + strings.Join(drift, ", ")
	}
	return deployAzureCheckResult(id, deployAzureCheckInfo, fmt.Sprintf("existing Function App %s %s: %s", p.config.Resources.FunctionApp, summary, strings.Join(parts, "; ")), remediation)
}

// deployAzureCountExplicitRules ignores the implicit "Allow all" rule that
// App Service reports when no explicit rules exist.
func deployAzureCountExplicitRules(rules []json.RawMessage) int {
	count := 0
	for _, raw := range rules {
		var rule struct {
			Name      string `json:"name"`
			IPAddress string `json:"ipAddress"`
		}
		if json.Unmarshal(raw, &rule) == nil && strings.EqualFold(rule.IPAddress, "Any") && strings.HasSuffix(strings.ToLower(rule.Name), "all") {
			continue
		}
		count++
	}
	return count
}

func valueOr(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

var deployAzureRetentionSettings = []string{"CAO_POSTGRES_RUN_RETENTION_DAYS", "CAO_POSTGRES_LINKED_RETENTION_DAYS"}

func (p *deployAzurePreflight) checkRetention(ctx context.Context) deployAzureCheck {
	const id = "retention-readback"
	const remediation = "Report only. Before any future upgrade, set retentionDays to at least the greatest existing effective retention across all roles."
	type setting struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	var apps []deployAzureResource
	for _, resource := range p.existing {
		if strings.EqualFold(resource.Type, "Microsoft.App/containerApps") || strings.EqualFold(resource.Type, "Microsoft.App/jobs") {
			apps = append(apps, resource)
		}
	}
	if p.siteID == "" && len(apps) == 0 {
		return deployAzureCheckResult(id, deployAzureCheckInfo, "no existing instance found; nothing to read back", "")
	}
	filter := fmt.Sprintf("[?name=='%s' || name=='%s'].{name:name,value:value}", deployAzureRetentionSettings[0], deployAzureRetentionSettings[1])
	greatest := 0
	var roles []string
	record := func(role string, values []setting) {
		if len(values) == 0 {
			roles = append(roles, role+": not set")
			return
		}
		var parts []string
		for _, value := range values {
			parts = append(parts, fmt.Sprintf("%s=%s", strings.TrimPrefix(strings.TrimSuffix(value.Name, "_RETENTION_DAYS"), "CAO_POSTGRES_"), value.Value))
			if days, err := strconv.Atoi(value.Value); err == nil && days > greatest {
				greatest = days
			}
		}
		sort.Strings(parts)
		roles = append(roles, role+": "+strings.Join(parts, " "))
	}
	if p.siteID != "" {
		var values []setting
		group := deployAzureResourceGroupOf(p.siteID)
		if err := p.runJSON(ctx, &values, "functionapp", "config", "appsettings", "list", "--subscription", p.config.Subscription,
			"--resource-group", group, "--name", p.config.Resources.FunctionApp, "--query", filter, "--output", "json"); err != nil {
			roles = append(roles, "function app: unreadable ("+azError(err)+")")
		} else {
			record("function app", values)
		}
	}
	for _, resource := range apps {
		var values []setting
		name := deployAzureResourceName(resource.ID)
		if err := p.runJSON(ctx, &values, "resource", "show", "--ids", resource.ID,
			"--query", "properties.template.containers[].env[] | "+filter, "--output", "json"); err != nil {
			roles = append(roles, name+": unreadable ("+azError(err)+")")
		} else {
			record(name, values)
		}
	}
	message := fmt.Sprintf("existing retention %s; config retentionDays=%d", strings.Join(roles, "; "), p.config.RetentionDays)
	if greatest > p.config.RetentionDays {
		message += fmt.Sprintf(" is lower than the greatest existing value %d", greatest)
	}
	return deployAzureCheckResult(id, deployAzureCheckInfo, message, remediation)
}

func deployAzureResourceGroupOf(id string) string {
	parts := strings.Split(id, "/")
	for index := 0; index+1 < len(parts); index++ {
		if strings.EqualFold(parts[index], "resourceGroups") {
			return parts[index+1]
		}
	}
	return ""
}

// deployAzureTemplateFacts are values preflight derives from the compiled template.
type deployAzureTemplateFacts struct {
	types        []string
	apiVersions  map[string]string
	planSKU      string
	postgresSKU  string
	postgresTier string
	ingress      struct {
		publicNetworkAccess string
		mainDefaultAction   string
		mainRules           int
		scmDefaultAction    string
		scmUseMain          string
	}
}

var deployAzureParameterExpression = regexp.MustCompile(`^\[parameters\('([^']+)'\)\]$`)

func deployAzureTemplateFactsFrom(content []byte, supplied map[string]any) deployAzureTemplateFacts {
	facts := deployAzureTemplateFacts{apiVersions: map[string]string{}}
	var root map[string]any
	if json.Unmarshal(content, &root) != nil {
		return facts
	}
	types := map[string]bool{}
	var walk func(map[string]any, map[string]any)
	walk = func(document map[string]any, supplied map[string]any) {
		parameters, _ := document["parameters"].(map[string]any)
		resolve := func(value any) any {
			if text, ok := value.(string); ok {
				if match := deployAzureParameterExpression.FindStringSubmatch(text); match != nil {
					if value, ok := supplied[match[1]]; ok {
						return value
					}
					if parameter, ok := parameters[match[1]].(map[string]any); ok {
						return parameter["defaultValue"]
					}
					return nil
				}
			}
			return value
		}
		text := func(value any) string {
			switch typed := resolve(value).(type) {
			case string:
				if strings.HasPrefix(typed, "[") {
					return ""
				}
				return typed
			case bool:
				return strconv.FormatBool(typed)
			}
			return ""
		}
		if facts.postgresSKU == "" {
			facts.postgresSKU = text("[parameters('postgresSkuName')]")
			facts.postgresTier = text("[parameters('postgresSkuTier')]")
		}
		var list []any
		switch typed := document["resources"].(type) {
		case []any:
			list = typed
		case map[string]any:
			for _, value := range typed {
				list = append(list, value)
			}
		}
		for _, item := range list {
			resource, ok := item.(map[string]any)
			if !ok {
				continue
			}
			// Only omit resources proven disabled; unknown ARM expressions
			// retain their prerequisites rather than weakening preflight.
			if enabled, ok := resolve(resource["condition"]).(bool); ok && !enabled {
				continue
			}
			resourceType, _ := resource["type"].(string)
			if resourceType == "" {
				continue
			}
			types[resourceType] = true
			if apiVersion, ok := resource["apiVersion"].(string); ok && facts.apiVersions[strings.ToLower(resourceType)] == "" {
				facts.apiVersions[strings.ToLower(resourceType)] = apiVersion
			}
			properties, _ := resource["properties"].(map[string]any)
			switch strings.ToLower(resourceType) {
			case "microsoft.web/serverfarms":
				if sku, ok := resource["sku"].(map[string]any); ok && facts.planSKU == "" {
					facts.planSKU = text(sku["name"])
				}
			case "microsoft.web/sites":
				facts.ingress.publicNetworkAccess = text(properties["publicNetworkAccess"])
				if siteConfig, ok := properties["siteConfig"].(map[string]any); ok {
					facts.ingress.mainDefaultAction = text(siteConfig["ipSecurityRestrictionsDefaultAction"])
					if rules, ok := resolve(siteConfig["ipSecurityRestrictions"]).([]any); ok {
						facts.ingress.mainRules = len(rules)
					}
					facts.ingress.scmDefaultAction = text(siteConfig["scmIpSecurityRestrictionsDefaultAction"])
					facts.ingress.scmUseMain = text(siteConfig["scmIpSecurityRestrictionsUseMain"])
				}
			case "microsoft.resources/deployments":
				if nested, ok := properties["template"].(map[string]any); ok {
					values := map[string]any{}
					if parameters, ok := properties["parameters"].(map[string]any); ok {
						for name, entry := range parameters {
							if parameter, ok := entry.(map[string]any); ok {
								values[name] = resolve(parameter["value"])
							}
						}
					}
					walk(nested, values)
				}
			}
		}
	}
	walk(root, supplied)
	for resourceType := range types {
		facts.types = append(facts.types, resourceType)
	}
	sort.Strings(facts.types)
	return facts
}
