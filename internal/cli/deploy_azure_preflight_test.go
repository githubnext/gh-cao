package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/githubnext/gh-cao/internal/azcli"
)

const testDeploySiteID = "/subscriptions/" + testDeploySubscription + "/resourceGroups/rg-synthetic-test/providers/Microsoft.Web/sites/fa-synthetic-test"

// testDeployProviders builds az provider list output covering the required
// providers and every template resource type in one location.
func testDeployProviders(t *testing.T, state, location string) string {
	t.Helper()
	content, err := os.ReadFile(testDeployTemplatePath(t))
	if err != nil {
		t.Fatal(err)
	}
	facts := deployAzureTemplateFactsFrom(content, nil)
	type resourceType struct {
		ResourceType string   `json:"resourceType"`
		Locations    []string `json:"locations"`
	}
	type provider struct {
		Namespace         string         `json:"namespace"`
		RegistrationState string         `json:"registrationState"`
		ResourceTypes     []resourceType `json:"resourceTypes"`
	}
	byNamespace := map[string]*provider{}
	var order []string
	add := func(namespace string) *provider {
		if existing, ok := byNamespace[strings.ToLower(namespace)]; ok {
			return existing
		}
		created := &provider{Namespace: namespace, RegistrationState: state}
		byNamespace[strings.ToLower(namespace)] = created
		order = append(order, strings.ToLower(namespace))
		return created
	}
	for _, namespace := range deployAzureRequiredProviders {
		add(namespace)
	}
	for _, full := range facts.types {
		namespace, typeName, _ := strings.Cut(full, "/")
		entry := add(namespace)
		entry.ResourceTypes = append(entry.ResourceTypes, resourceType{ResourceType: typeName, Locations: []string{location}})
	}
	var providers []provider
	for _, key := range order {
		providers = append(providers, *byNamespace[key])
	}
	return string(mustJSON(t, providers))
}

func runDeployPreflight(t *testing.T, fake *fakeAz, config any, extra ...string) (deployAzurePreflightReport, string, error) {
	t.Helper()
	t.Chdir(t.TempDir())
	configPath := writeDeployConfig(t, config)
	root, stdout, stderr := testDeployRoot(fake)
	root.SetArgs(append([]string{"deploy", "azure", "--preflight", "--json", "--config", configPath, "--template", testDeployTemplatePath(t)}, extra...))
	err := root.ExecuteContext(context.Background())
	var report deployAzurePreflightReport
	if decodeErr := json.Unmarshal(stdout.Bytes(), &report); decodeErr != nil {
		t.Fatalf("preflight JSON: %v\nstdout:\n%s\nstderr:\n%s\nerr: %v", decodeErr, stdout, stderr, err)
	}
	if entries, readErr := os.ReadDir("."); readErr != nil || len(entries) != 0 {
		t.Fatalf("--preflight wrote files to the working directory: %v %v", entries, readErr)
	}
	return report, stdout.String() + stderr.String(), err
}

func findDeployCheck(t *testing.T, report deployAzurePreflightReport, id string) deployAzureCheck {
	t.Helper()
	for _, check := range report.Checks {
		if check.ID == id {
			return check
		}
	}
	t.Fatalf("check %s missing from %+v", id, report.Checks)
	return deployAzureCheck{}
}

var testDeployCheckIDs = []string{"tools", "login", "existing-instance", "permissions", "resource-providers", "location",
	"name-storage-account", "name-container-registry", "name-key-vault", "name-function-app", "name-postgres-server",
	"ingress-drift", "retention-readback"}

var testDeployRestPath = regexp.MustCompile(`^/subscriptions/` + testDeploySubscription + `/providers/(Microsoft\.KeyVault|Microsoft\.Web|Microsoft\.DBforPostgreSQL)(/locations/[a-z0-9]+)?/checkNameAvailability\?api-version=\d{4}-\d{2}-\d{2}(-preview)?$`)

func assertDeployCallsReadOnly(t *testing.T, calls [][]string) {
	t.Helper()
	for _, call := range calls {
		if !azcli.IsReadOnly(call) {
			t.Fatalf("non-read-only call %q", call)
		}
		if azcli.CommandName(call) != "rest" {
			continue
		}
		method, url := call[slices.Index(call, "--method")+1], call[slices.Index(call, "--url")+1]
		if strings.EqualFold(method, "get") && strings.HasSuffix(url, "/providers/Microsoft.Authorization/permissions?api-version=2022-04-01") {
			continue
		}
		if strings.EqualFold(method, "get") && strings.Contains(url, "/providers/Microsoft.Web/geoRegions?") && azcli.IsReadOnly(call) {
			continue
		}
		if !strings.EqualFold(method, "post") || !testDeployRestPath.MatchString(url) {
			t.Fatalf("az rest outside the checkNameAvailability allowlist: %q", call)
		}
	}
}

func TestDeployAzurePreflightHealthy(t *testing.T) {
	fake := &fakeAz{t: t}
	report, _, err := runDeployPreflight(t, fake, testDeployConfig())
	if err != nil {
		t.Fatal(err)
	}
	if report.Kind != deployAzurePreflightKind || report.SchemaVersion != 1 || report.Status != deployAzureCheckPass {
		t.Fatalf("report header = %+v", report)
	}
	var ids []string
	for _, check := range report.Checks {
		ids = append(ids, check.ID)
		want := deployAzureCheckPass
		if check.ID == "ingress-drift" || check.ID == "retention-readback" {
			want = deployAzureCheckInfo
		}
		if check.Status != want || check.Message == "" {
			t.Fatalf("%s = %+v, want %s", check.ID, check, want)
		}
	}
	if !slices.Equal(ids, testDeployCheckIDs) {
		t.Fatalf("check ids = %q", ids)
	}
	if tools := findDeployCheck(t, report, "tools"); !strings.Contains(tools.Message, "Bicep is not required") {
		t.Fatalf("tools message = %q", tools.Message)
	}
	if login := findDeployCheck(t, report, "login"); !strings.Contains(login.Message, testDeployTenant) {
		t.Fatalf("login does not report tenant: %q", login.Message)
	}
	providers := findDeployCheck(t, report, "resource-providers")
	for _, namespace := range []string{"Microsoft.Web", "Microsoft.App", "Microsoft.DBforPostgreSQL", "Microsoft.KeyVault", "Microsoft.ContainerRegistry",
		"Microsoft.Storage", "Microsoft.Network", "Microsoft.OperationalInsights", "Microsoft.Insights", "Microsoft.ManagedIdentity"} {
		if !strings.Contains(providers.Message, namespace) {
			t.Fatalf("providers message missing %s: %q", namespace, providers.Message)
		}
	}
	assertDeployCallsReadOnly(t, fake.calls)
	var namespaces []string
	for _, call := range fake.calls {
		if azcli.CommandName(call) == "rest" {
			namespaces = append(namespaces, strings.TrimPrefix(fakeAzKey(call), "rest "))
		}
	}
	if !slices.Equal(namespaces, []string{"Microsoft.Authorization", "Microsoft.Web geoRegions", "Microsoft.KeyVault", "Microsoft.Web", "Microsoft.DBforPostgreSQL"}) {
		t.Fatalf("checkNameAvailability namespaces = %q", namespaces)
	}
}

func TestDeployAzurePreflightTable(t *testing.T) {
	t.Chdir(t.TempDir())
	fake := &fakeAz{t: t, overrides: map[string]fakeAzResponse{"acr check-name": {stdout: `{"nameAvailable":false,"reason":"AlreadyExists","message":"taken"}`}}}
	root, stdout, _ := testDeployRoot(fake)
	root.SetArgs([]string{"deploy", "azure", "--preflight", "--config", writeDeployConfig(t, testDeployConfig())})
	err := root.Execute()
	if err == nil || !strings.Contains(err.Error(), "name-container-registry") {
		t.Fatalf("expected failing preflight error naming the check, got %v", err)
	}
	for _, want := range []string{"STATUS", "PASS", "FAIL", "INFO", "name-container-registry", "Remediation", "resources.containerRegistry"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("table missing %q:\n%s", want, stdout)
		}
	}
	if entries, _ := os.ReadDir("."); len(entries) != 0 {
		t.Fatalf("--preflight wrote files: %v", entries)
	}
}

func TestDeployAzurePreflightChecks(t *testing.T) {
	failed := errors.New("ERROR: synthetic az failure")
	err := func(stderr string) fakeAzResponse {
		return fakeAzResponse{stderr: stderr, err: &azcli.ExitError{Code: 1, Stderr: stderr}}
	}
	ok := func(stdout string) fakeAzResponse { return fakeAzResponse{stdout: stdout} }
	unavailable := `{"nameAvailable":false,"reason":"AlreadyExists","message":"The name is already in use."}`
	for _, tt := range []struct {
		name      string
		overrides map[string]fakeAzResponse
		fake      func(*fakeAz)
		config    func(map[string]any)
		id        string
		status    string
		message   string
	}{
		{name: "tools old az", overrides: map[string]fakeAzResponse{"version": ok(`{"azure-cli":"2.50.0"}`)}, id: "tools", status: "fail", message: "older than the required"},
		{name: "tools missing az", overrides: map[string]fakeAzResponse{"version": {err: failed}}, id: "tools", status: "fail", message: "Azure CLI is unavailable"},
		{name: "tools unreadable version", overrides: map[string]fakeAzResponse{"version": ok(`{}`)}, id: "tools", status: "unknown", message: "could not read the Azure CLI version"},
		{name: "tools failure skips later checks", overrides: map[string]fakeAzResponse{"version": {err: failed}}, id: "permissions", status: "unknown", message: "Azure CLI is unavailable"},

		{name: "login not signed in", overrides: map[string]fakeAzResponse{"account show": err("Please run 'az login' to setup account.")}, id: "login", status: "fail", message: "az login"},
		{name: "login wrong subscription", overrides: map[string]fakeAzResponse{"account show": ok(`{"id":"00000000-0000-4000-8000-0000000000ff","state":"Enabled","tenantId":"` + testDeployTenant + `"}`)}, id: "login", status: "fail", message: "00000000-0000-4000-8000-0000000000ff"},
		{name: "login disabled subscription", overrides: map[string]fakeAzResponse{"account show": ok(`{"id":"` + testDeploySubscription + `","state":"Disabled","tenantId":"` + testDeployTenant + `"}`)}, id: "login", status: "fail", message: "Disabled"},
		{name: "login failure skips names", overrides: map[string]fakeAzResponse{"account show": {err: failed}}, id: "name-key-vault", status: "unknown", message: "login check did not pass"},

		{name: "existing unknown fails closed", overrides: map[string]fakeAzResponse{"account show": {err: failed}}, id: "existing-instance", status: "fail", message: "login check did not pass"},
		{name: "existing list error fails closed", overrides: map[string]fakeAzResponse{"resource list": {err: failed}}, id: "existing-instance", status: "fail", message: "could not determine whether an instance exists"},
		{name: "existing named resource", fake: func(f *fakeAz) {
			f.namedResources = map[string]string{"stsynthetictest": `[{"id":"/subscriptions/x/resourceGroups/rg-other/providers/Microsoft.Storage/storageAccounts/stsynthetictest","type":"Microsoft.Storage/storageAccounts"}]`}
		}, id: "existing-instance", status: "fail", message: "only fresh deployments are supported"},

		{name: "permissions reader only", overrides: map[string]fakeAzResponse{"rest Microsoft.Authorization": ok(`{"value":[{"actions":["*/read"],"notActions":[]}]}`)}, id: "permissions", status: "fail", message: "do not grant"},
		{name: "permissions contributor lacks assign", overrides: map[string]fakeAzResponse{"rest Microsoft.Authorization": ok(`{"value":[{"actions":["*"],"notActions":["Microsoft.Authorization/*/write"]}]}`)}, id: "permissions", status: "fail", message: "Microsoft.Authorization/roleAssignments/write"},
		{name: "permissions union restores assign", overrides: map[string]fakeAzResponse{"rest Microsoft.Authorization": ok(`{"value":[{"actions":["*"],"notActions":["Microsoft.Authorization/*/write"]},{"actions":["Microsoft.Authorization/roleAssignments/write"],"notActions":[]}]}`)}, id: "permissions", status: "pass", message: "can create resources and role assignments"},
		{name: "permissions paginated unknown", overrides: map[string]fakeAzResponse{"rest Microsoft.Authorization": ok(`{"value":[{"actions":["*"]}],"nextLink":"https://management.azure.com/next"}`)}, id: "permissions", status: "unknown", message: "missing or incomplete"},
		{name: "permissions malformed unknown", overrides: map[string]fakeAzResponse{"rest Microsoft.Authorization": ok(`{}`)}, id: "permissions", status: "unknown", message: "missing or incomplete"},
		{name: "permissions unavailable", overrides: map[string]fakeAzResponse{"rest Microsoft.Authorization": err("AuthorizationFailed")}, id: "permissions", status: "unknown", message: "could not read ARM effective permissions"},
		{name: "permissions timeout", overrides: map[string]fakeAzResponse{"rest Microsoft.Authorization": {err: context.DeadlineExceeded}}, id: "permissions", status: "unknown", message: "timed out after at most one minute"},
		{name: "permissions service principal", overrides: map[string]fakeAzResponse{"account show": ok(`{"id":"` + testDeploySubscription + `","state":"Enabled","tenantId":"` + testDeployTenant + `","user":{"name":"00000000-0000-4000-8000-000000000005","type":"servicePrincipal"}}`)}, id: "permissions", status: "pass", message: "can create resources"},
		{name: "permissions managed identity", overrides: map[string]fakeAzResponse{"account show": ok(`{"id":"` + testDeploySubscription + `","state":"Enabled","tenantId":"` + testDeployTenant + `","user":{"name":"x","type":"managedIdentity"}}`)}, id: "permissions", status: "pass", message: "no Microsoft Graph lookup"},

		{name: "providers unregistered", fake: func(f *fakeAz) {
			f.overrides = map[string]fakeAzResponse{"provider list": ok(testDeployProviders(f.t, "NotRegistered", "West US 3"))}
		}, id: "resource-providers", status: "fail", message: "Microsoft.App (NotRegistered)"},
		{name: "providers missing namespace", overrides: map[string]fakeAzResponse{"provider list": ok(`[]`)}, id: "resource-providers", status: "fail", message: "Microsoft.Web (NotFound)"},
		{name: "providers list error", overrides: map[string]fakeAzResponse{"provider list": {err: failed}}, id: "resource-providers", status: "unknown", message: "could not list resource providers"},

		{name: "location unsupported type", fake: func(f *fakeAz) {
			f.overrides = map[string]fakeAzResponse{"provider list": ok(testDeployProviders(f.t, "Registered", "East US"))}
		}, id: "location", status: "fail", message: "does not offer"},
		{name: "location plan sku unavailable", overrides: map[string]fakeAzResponse{"rest Microsoft.Web geoRegions": ok(`{"value":[{"name":"East US"}]}`)}, id: "location", status: "fail", message: "App Service plan EP1 on Linux"},
		{name: "location postgres sku missing", overrides: map[string]fakeAzResponse{"postgres flexible-server list-skus": ok(`[{"supportedServerEditions":[{"name":"Burstable","supportedServerSkus":[{"name":"Standard_B1ms"}]}]}]`)}, id: "location", status: "fail", message: "PostgreSQL Burstable Standard_B2s"},
		{name: "location postgres unrecognized", overrides: map[string]fakeAzResponse{"postgres flexible-server list-skus": ok(`[{"unexpected":true}]`)}, id: "location", status: "unknown", message: "unrecognized capability output"},
		{name: "location list error", overrides: map[string]fakeAzResponse{"rest Microsoft.Web geoRegions": {err: failed}}, id: "location", status: "unknown", message: "could not confirm location"},

		{name: "storage taken", overrides: map[string]fakeAzResponse{"storage account check-name": ok(unavailable)}, id: "name-storage-account", status: "fail", message: "not available (AlreadyExists"},
		{name: "storage error", overrides: map[string]fakeAzResponse{"storage account check-name": {err: failed}}, id: "name-storage-account", status: "unknown", message: "could not check storage account name"},
		{name: "registry taken", overrides: map[string]fakeAzResponse{"acr check-name": ok(unavailable)}, id: "name-container-registry", status: "fail", message: "container registry name acrsynthetictest is not available"},
		{name: "registry malformed", overrides: map[string]fakeAzResponse{"acr check-name": ok(`{}`)}, id: "name-container-registry", status: "unknown", message: "did not include nameAvailable"},
		{name: "key vault taken", overrides: map[string]fakeAzResponse{"rest Microsoft.KeyVault": ok(unavailable)}, id: "name-key-vault", status: "fail", message: "Key Vault name kv-synthetic-test is not available"},
		{name: "key vault soft-deleted", overrides: map[string]fakeAzResponse{"keyvault list-deleted": ok(`[{"name":"kv-synthetic-test","properties":{"location":"westus3","scheduledPurgeDate":"2026-02-01T00:00:00Z"}}]`)}, id: "name-key-vault", status: "fail", message: "soft-deleted"},
		{name: "key vault deleted list error", overrides: map[string]fakeAzResponse{"keyvault list-deleted": {err: failed}}, id: "name-key-vault", status: "unknown", message: "could not list soft-deleted Key Vaults"},
		{name: "key vault check error", overrides: map[string]fakeAzResponse{"rest Microsoft.KeyVault": {err: failed}}, id: "name-key-vault", status: "unknown", message: "could not check Key Vault name"},
		{name: "function app taken", overrides: map[string]fakeAzResponse{"rest Microsoft.Web": ok(unavailable)}, id: "name-function-app", status: "fail", message: "Function App name fa-synthetic-test is not available"},
		{name: "function app error", overrides: map[string]fakeAzResponse{"rest Microsoft.Web": {err: failed}}, id: "name-function-app", status: "unknown", message: "could not check Function App name"},
		{name: "postgres taken", overrides: map[string]fakeAzResponse{"rest Microsoft.DBforPostgreSQL": ok(unavailable)}, id: "name-postgres-server", status: "fail", message: "PostgreSQL server name fa-synthetic-test-pg is not available"},
		{name: "postgres configured name", config: func(c map[string]any) { c["resources"].(map[string]any)["postgresServer"] = "pg-synthetic-test" },
			overrides: map[string]fakeAzResponse{"rest Microsoft.DBforPostgreSQL": ok(unavailable)}, id: "name-postgres-server", status: "fail", message: "pg-synthetic-test is not available"},
		{name: "postgres error", overrides: map[string]fakeAzResponse{"rest Microsoft.DBforPostgreSQL": {err: failed}}, id: "name-postgres-server", status: "unknown", message: "could not check PostgreSQL server name"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeAz{t: t, overrides: tt.overrides}
			if tt.fake != nil {
				tt.fake(fake)
			}
			config := testDeployConfig()
			if tt.config != nil {
				tt.config(config)
			}
			report, _, runErr := runDeployPreflight(t, fake, config)
			check := findDeployCheck(t, report, tt.id)
			if check.Status != tt.status || !strings.Contains(check.Message, tt.message) {
				t.Fatalf("%s = %+v, want %s containing %q", tt.id, check, tt.status, tt.message)
			}
			if check.Status != deployAzureCheckPass && strings.TrimSpace(check.Remediation) == "" {
				t.Fatalf("%s %s has no remediation", tt.id, check.Status)
			}
			anyFailed := slices.ContainsFunc(report.Checks, func(c deployAzureCheck) bool {
				return c.Status == deployAzureCheckFail || c.Status == deployAzureCheckUnknown
			})
			if anyFailed != (runErr != nil) || anyFailed != (report.Status == deployAzureCheckFail) {
				t.Fatalf("failed=%t but err=%v status=%s", anyFailed, runErr, report.Status)
			}
			if len(report.Checks) != len(testDeployCheckIDs) {
				t.Fatalf("preflight stopped early: %d checks", len(report.Checks))
			}
			assertDeployCallsReadOnly(t, fake.calls)
		})
	}
}

func TestDeployAzurePreflightExistingInstanceReports(t *testing.T) {
	site := `[{"id":"` + testDeploySiteID + `","type":"Microsoft.Web/sites"}]`
	job := "/subscriptions/" + testDeploySubscription + "/resourceGroups/rg-synthetic-test/providers/Microsoft.App/jobs/fa-synthetic-test-ingest"
	for _, tt := range []struct {
		name      string
		overrides map[string]fakeAzResponse
		ingress   []string
		retention []string
	}{
		{name: "matches defaults", ingress: []string{"matches template defaults", "publicNetworkAccess=Enabled (template Enabled)", "scmIpSecurityRestrictionsUseMain=false (template false)"},
			retention: []string{"function app: LINKED=45 RUN=45", "fa-synthetic-test-ingest: LINKED=60 RUN=60", "retentionDays=45 is lower than the greatest existing value 60"}},
		{name: "drifted", overrides: map[string]fakeAzResponse{
			"resource show Microsoft.Web/sites": {stdout: `{"publicNetworkAccess":"Disabled"}`},
			"resource show config":              {stdout: `{"ipSecurityRestrictionsDefaultAction":"Deny","ipSecurityRestrictions":[{"name":"office","ipAddress":"203.0.113.0/24","action":"Allow"}],"scmIpSecurityRestrictionsDefaultAction":"Allow","scmIpSecurityRestrictionsUseMain":true}`},
		}, ingress: []string{"differs from template defaults in publicNetworkAccess, main defaultAction, main rules, scm defaultAction, scmIpSecurityRestrictionsUseMain", "main rules=1 (template 0)"}},
		{name: "unreadable", overrides: map[string]fakeAzResponse{
			"resource show Microsoft.Web/sites":   {err: errors.New("ERROR: AuthorizationFailed")},
			"functionapp config appsettings list": {err: errors.New("ERROR: AuthorizationFailed")},
			"resource show Microsoft.App/jobs":    {stdout: `[]`},
		}, ingress: []string{"could not read existing Function App ingress"}, retention: []string{"function app: unreadable", "fa-synthetic-test-ingest: not set"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeAz{t: t, groupExists: true, overrides: tt.overrides,
				groupResources: `[{"id":"` + testDeploySiteID + `","type":"Microsoft.Web/sites"},{"id":"` + job + `","type":"Microsoft.App/jobs"}]`,
				namedResources: map[string]string{"fa-synthetic-test": site}}
			report, _, err := runDeployPreflight(t, fake, testDeployConfig())
			if err == nil || !strings.Contains(err.Error(), "only fresh deployments") {
				t.Fatalf("existing instance must still fail closed, got %v", err)
			}
			for id, wants := range map[string][]string{"ingress-drift": tt.ingress, "retention-readback": tt.retention} {
				check := findDeployCheck(t, report, id)
				if check.Status != deployAzureCheckInfo || check.Remediation == "" {
					t.Fatalf("%s must be report-only info with guidance: %+v", id, check)
				}
				for _, want := range wants {
					if !strings.Contains(check.Message, want) {
						t.Fatalf("%s message %q missing %q", id, check.Message, want)
					}
				}
			}
			assertDeployCallsReadOnly(t, fake.calls)
			for _, call := range fake.calls {
				if azcli.CommandName(call) == "functionapp config appsettings list" && !slices.Contains(call, "--query") {
					t.Fatalf("app settings read without a retention-only query: %q", call)
				}
			}
		})
	}
}

func TestDeployAzurePreflightNeverExposesSecrets(t *testing.T) {
	leak := "echo " + strings.Join(testDeploySecrets(), " ")
	fake := &fakeAz{t: t, overrides: map[string]fakeAzResponse{
		"acr check-name":            {stderr: leak, err: &azcli.ExitError{Code: 1, Stderr: leak}},
		"rest Microsoft.KeyVault":   {stdout: `{"nameAvailable":false,"reason":"AlreadyExists","message":"` + testDeployPassword + `"}`},
		"keyvault list-deleted":     {err: errors.New(leak)},
		"appservice list-locations": {err: errors.New(leak)},
	}}
	report, output, err := runDeployPreflight(t, fake, testDeployConfig())
	observed := output + string(mustJSON(t, report))
	if err != nil {
		observed += err.Error()
	}
	for _, call := range fake.calls {
		observed += strings.Join(call, " ")
	}
	for _, secret := range testDeploySecrets() {
		if strings.Contains(observed, secret) {
			t.Fatalf("secret value exposed in preflight argv, output, or error:\n%s", observed)
		}
	}
	if check := findDeployCheck(t, report, "name-container-registry"); !strings.Contains(check.Message, "***") {
		t.Fatalf("expected redacted az error, got %q", check.Message)
	}
}

func TestDeployAzurePlanRunsPreflightFirst(t *testing.T) {
	fake := &fakeAz{t: t, overrides: map[string]fakeAzResponse{"rest Microsoft.Web": {stdout: `{"nameAvailable":false,"reason":"AlreadyExists"}`}}}
	stdout, _, out, err := runDeployPlan(t, fake, testDeployConfig())
	if err == nil || !strings.Contains(err.Error(), "name-function-app") {
		t.Fatalf("expected preflight failure, got %v", err)
	}
	if !strings.Contains(stdout, "FAIL") || !strings.Contains(stdout, "name-function-app") {
		t.Fatalf("plan did not print preflight table:\n%s", stdout)
	}
	for _, call := range fake.calls {
		if slices.Contains(call, "what-if") {
			t.Fatalf("what-if ran after preflight failed: %q", call)
		}
	}
	if _, statErr := os.Stat(out); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatal("failed preflight wrote a plan file")
	}
	if !slices.Equal(fake.calls[0], []string{"version", "--output", "json"}) {
		t.Fatalf("first call = %q, want az version", fake.calls[0])
	}
}

func TestDeployAzurePreflightWorksWithoutSecrets(t *testing.T) {
	t.Chdir(t.TempDir())
	fake := &fakeAz{t: t}
	root, stdout, _ := testDeployRootWith(fake, func(string) (string, bool) { return "", false })
	root.SetArgs([]string{"deploy", "azure", "--preflight", "--config", writeDeployConfig(t, testDeployConfig())})
	if err := root.Execute(); err != nil {
		t.Fatalf("preflight should not require secrets: %v\n%s", err, stdout)
	}
	if entries, _ := os.ReadDir("."); len(entries) != 0 {
		t.Fatalf("--preflight wrote files: %v", entries)
	}
}

func TestDeployAzurePreflightModeFlags(t *testing.T) {
	configPath := writeDeployConfig(t, testDeployConfig())
	for _, tt := range []struct {
		args []string
		want string
	}{
		{[]string{"--preflight", "--plan"}, "none of the others can be"},
		{[]string{"--preflight", "--apply"}, "none of the others can be"},
		{[]string{"--preflight", "--out", filepath.Join(t.TempDir(), "p.json")}, "--out is not valid with --preflight"},
		{[]string{"--preflight", "--plan-file", "p.json"}, "--plan-file is only valid with --apply"},
		{[]string{"--json"}, "--json is only valid with --preflight"},
	} {
		fake := &fakeAz{t: t}
		root, _, _ := testDeployRoot(fake)
		root.SetArgs(append([]string{"deploy", "azure", "--config", configPath}, tt.args...))
		if err := root.Execute(); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Fatalf("%q: expected %q, got %v", tt.args, tt.want, err)
		}
		if len(fake.calls) != 0 {
			t.Fatalf("%q: unexpected calls %q", tt.args, fake.calls)
		}
	}
}
