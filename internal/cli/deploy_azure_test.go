package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/githubnext/gh-cao/internal/azcli"
)

// Synthetic values only; none identify a real deployment.
const (
	testDeploySubscription    = "00000000-0000-4000-8000-000000000001"
	testDeployTenant          = "00000000-0000-4000-8000-000000000002"
	testDeployPassword        = "synthetic-postgres-password-0001"
	testDeployOwnerPassword   = "synthetic-owner-password-material-0004"
	testDeployRuntimePassword = "synthetic-runtime-password-material-0005"
	testDeployClientSecret    = "synthetic-client-secret-value-0002"
	testDeploySession         = "synthetic-session-secret-material-0003-abcdef"
	testDeployPrincipal       = "00000000-0000-4000-8000-000000000004"
)

func testDeployConfig() map[string]any {
	return map[string]any{
		"schemaVersion": 1,
		"environment":   "development",
		"subscription":  testDeploySubscription,
		"resourceGroup": "rg-synthetic-test",
		"location":      "westus3",
		"resources": map[string]any{
			"functionApp":       "fa-synthetic-test",
			"storageAccount":    "stsynthetictest",
			"containerRegistry": "acrsynthetictest",
			"keyVault":          "kv-synthetic-test",
		},
		"image":         map[string]any{"tag": "v0.0.0-test"},
		"allowedHosts":  []string{"dashboard.example.test"},
		"access":        map[string]any{"organizations": []string{"example-org"}, "teams": []string{"example-org/example-team"}, "users": []string{}},
		"retentionDays": 45,
		"oauth":         map[string]any{"clientId": "Iv1.synthetic0001"},
		"secrets": map[string]any{
			"postgresAdministratorPassword": map[string]any{"env": "CAO_TEST_POSTGRES_PASSWORD"},
			"postgresOwnerPassword":         map[string]any{"env": "CAO_TEST_OWNER_PASSWORD"},
			"postgresRuntimePassword":       map[string]any{"env": "CAO_TEST_RUNTIME_PASSWORD"},
			"githubClientSecret":            map[string]any{"env": "CAO_TEST_GITHUB_CLIENT_SECRET"},
			"sessionSecret":                 map[string]any{"env": "CAO_TEST_SESSION_SECRET"},
		},
	}
}

func testDeployEnv(name string) (string, bool) {
	value, ok := map[string]string{
		"CAO_TEST_POSTGRES_PASSWORD":    testDeployPassword,
		"CAO_TEST_OWNER_PASSWORD":       testDeployOwnerPassword,
		"CAO_TEST_RUNTIME_PASSWORD":     testDeployRuntimePassword,
		"CAO_TEST_GITHUB_CLIENT_SECRET": testDeployClientSecret,
		"CAO_TEST_SESSION_SECRET":       testDeploySession,
	}[name]
	return value, ok
}

func testDeploySecrets() []string {
	return []string{testDeployPassword, testDeployOwnerPassword, testDeployRuntimePassword, testDeployClientSecret, testDeploySession}
}

func writeDeployConfig(t *testing.T, config any) string {
	t.Helper()
	content, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "deploy-azure.json")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// testDeployTemplateAbs is resolved at init so tests that t.Chdir still find it.
var testDeployTemplateAbs, testDeployTemplateErr = filepath.Abs(filepath.Join("..", "..", "server", "azure", "main.json"))

func testDeployTemplatePath(t *testing.T) string {
	t.Helper()
	if testDeployTemplateErr != nil {
		t.Fatal(testDeployTemplateErr)
	}
	return testDeployTemplateAbs
}

// fakeAz records argv and answers the read-only plan commands.
type fakeAz struct {
	t              *testing.T
	calls          [][]string
	groupExists    bool
	groupResources string
	namedResources map[string]string
	whatIf         string
	whatIfStderr   string
	whatIfErr      error
	parameterFiles []string
	// overrides replace the default healthy response for a fakeAzKey.
	overrides map[string]fakeAzResponse
}

type fakeAzResponse struct {
	stdout string
	stderr string
	err    error
}

var deployAzureAllowedCommands = [][]string{
	{"version"}, {"account", "show"}, {"group", "exists"}, {"resource", "list"}, {"resource", "show"},
	{"provider", "list"}, {"role", "assignment", "list"}, {"ad", "signed-in-user", "show"}, {"ad", "sp", "show"},
	{"storage", "account", "check-name"}, {"acr", "check-name"}, {"keyvault", "list-deleted"},
	{"appservice", "list-locations"}, {"postgres", "flexible-server", "list-skus"},
	{"functionapp", "config", "appsettings", "list"}, {"rest"},
	{"deployment", "group", "what-if"}, {"deployment", "sub", "what-if"},
}

// fakeAzKey names a call for overrides: the command path, plus the provider
// namespace for az rest checkNameAvailability and the target for resource show.
func fakeAzKey(args []string) string {
	key := azcli.CommandName(args)
	flag := func(name string) string {
		index := slices.Index(args, name)
		if index < 0 || index+1 >= len(args) {
			return ""
		}
		return args[index+1]
	}
	switch key {
	case "rest":
		if strings.Contains(flag("--url"), "/providers/Microsoft.DBforPostgreSQL/locations/") {
			return "rest Microsoft.DBforPostgreSQL"
		}
		parts := strings.Split(strings.SplitN(flag("--url"), "?", 2)[0], "/")
		if parts[len(parts)-1] == "geoRegions" {
			return "rest Microsoft.Web geoRegions"
		}
		return "rest " + parts[len(parts)-2]
	case "resource show":
		ids := flag("--ids")
		if strings.HasSuffix(ids, "/config/web") {
			return "resource show config"
		}
		return "resource show " + ids[strings.LastIndex(ids, "/providers/")+len("/providers/"):strings.LastIndex(ids, "/")]
	}
	return key
}

var deployAzureForbiddenWords = []string{"create", "update", "delete", "set", "start", "stop", "restart", "import", "build", "push", "deploy", "assign", "purge", "login"}

func (f *fakeAz) Run(ctx context.Context, args ...string) (azcli.Result, error) {
	f.t.Helper()
	if err := ctx.Err(); err != nil {
		return azcli.Result{}, err
	}
	f.calls = append(f.calls, slices.Clone(args))
	path := azcli.CommandPath(args)
	if !slices.ContainsFunc(deployAzureAllowedCommands, func(allowed []string) bool { return slices.Equal(allowed, path) }) || !azcli.IsReadOnly(args) {
		f.t.Fatalf("plan executed non-allowlisted az command %q", strings.Join(args, " "))
	}
	if response, ok := f.overrides[fakeAzKey(args)]; ok {
		return azcli.Result{Stdout: response.stdout, Stderr: response.stderr}, response.err
	}
	for _, word := range path {
		if slices.Contains(deployAzureForbiddenWords, word) && word != "deployment" {
			f.t.Fatalf("plan executed mutating az verb %q", strings.Join(args, " "))
		}
	}
	flag := func(name string) string {
		index := slices.Index(args, name)
		if index < 0 || index+1 >= len(args) {
			return ""
		}
		return args[index+1]
	}
	switch fakeAzKey(args) {
	case "version":
		return azcli.Result{Stdout: `{"azure-cli":"2.67.0","azure-cli-core":"2.67.0"}`}, nil
	case "account show":
		return azcli.Result{Stdout: fmt.Sprintf(`{"id":%q,"state":"Enabled","tenantId":%q,"user":{"name":"operator@example.test","type":"user"}}`, flag("--subscription"), testDeployTenant)}, nil
	case "ad signed-in-user show":
		return azcli.Result{Stdout: `"` + testDeployPrincipal + `"`}, nil
	case "ad sp show":
		return azcli.Result{Stdout: `"` + testDeployPrincipal + `"`}, nil
	case "role assignment list":
		return azcli.Result{Stdout: `[{"roleDefinitionName":"Owner","scope":"/subscriptions/` + testDeploySubscription + `"}]`}, nil
	case "rest Microsoft.Authorization":
		return azcli.Result{Stdout: `{"value":[{"actions":["*"],"notActions":[]}]}`}, nil
	case "rest Microsoft.Web geoRegions":
		return azcli.Result{Stdout: `{"value":[{"name":"West US 3"},{"name":"East US"}]}`}, nil
	case "provider list":
		return azcli.Result{Stdout: testDeployProviders(f.t, "Registered", "West US 3")}, nil
	case "storage account check-name", "acr check-name", "rest Microsoft.KeyVault", "rest Microsoft.Web", "rest Microsoft.DBforPostgreSQL":
		return azcli.Result{Stdout: `{"nameAvailable":true,"reason":null,"message":null}`}, nil
	case "keyvault list-deleted":
		return azcli.Result{Stdout: `[]`}, nil
	case "appservice list-locations":
		return azcli.Result{Stdout: `[{"name":"West US 3"},{"name":"East US"}]`}, nil
	case "postgres flexible-server list-skus":
		return azcli.Result{Stdout: `[{"supportedServerEditions":[{"name":"Burstable","supportedServerSkus":[{"name":"Standard_B1ms"},{"name":"Standard_B2s"}]}]}]`}, nil
	case "resource show Microsoft.Web/sites":
		return azcli.Result{Stdout: `{"publicNetworkAccess":"Enabled"}`}, nil
	case "resource show config":
		return azcli.Result{Stdout: `{"ipSecurityRestrictionsDefaultAction":"Allow","ipSecurityRestrictions":[{"name":"Allow all","ipAddress":"Any","action":"Allow"}],"scmIpSecurityRestrictionsDefaultAction":"Deny","scmIpSecurityRestrictionsUseMain":false}`}, nil
	case "resource show Microsoft.App/containerApps", "resource show Microsoft.App/jobs":
		return azcli.Result{Stdout: `[{"name":"CAO_POSTGRES_RUN_RETENTION_DAYS","value":"60"},{"name":"CAO_POSTGRES_LINKED_RETENTION_DAYS","value":"60"}]`}, nil
	case "functionapp config appsettings list":
		return azcli.Result{Stdout: `[{"name":"CAO_POSTGRES_RUN_RETENTION_DAYS","value":"45"},{"name":"CAO_POSTGRES_LINKED_RETENTION_DAYS","value":"45"}]`}, nil
	case "group exists":
		return azcli.Result{Stdout: fmt.Sprintf("%t\n", f.groupExists)}, nil
	case "resource list":
		if flag("--resource-group") != "" {
			if f.groupResources != "" {
				return azcli.Result{Stdout: f.groupResources}, nil
			}
			return azcli.Result{Stdout: "[]"}, nil
		}
		if found, ok := f.namedResources[flag("--name")]; ok {
			return azcli.Result{Stdout: found}, nil
		}
		return azcli.Result{Stdout: "[]"}, nil
	default:
		parameters := strings.TrimPrefix(flag("--parameters"), "@")
		info, err := os.Stat(parameters)
		if err != nil {
			f.t.Fatalf("what-if parameter file missing: %v", err)
		}
		if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
			f.t.Fatalf("parameter file mode = %v, want 0600", info.Mode().Perm())
		}
		content, err := os.ReadFile(parameters)
		if err != nil {
			f.t.Fatal(err)
		}
		for _, secret := range testDeploySecrets() {
			if !bytes.Contains(content, []byte(secret)) {
				f.t.Fatalf("secure value was not delivered through the parameter file")
			}
		}
		if _, err := os.Stat(flag("--template-file")); err != nil {
			f.t.Fatalf("what-if template missing: %v", err)
		}
		f.parameterFiles = append(f.parameterFiles, parameters)
		output := f.whatIf
		if output == "" {
			output = fmt.Sprintf(`{"status":"Succeeded","error":null,"changes":[{"resourceId":"/subscriptions/%s/resourceGroups/rg-synthetic-test/providers/Microsoft.Web/sites/fa-synthetic-test","changeType":"Create"},{"resourceId":"/subscriptions/%s/resourceGroups/rg-synthetic-test/providers/Microsoft.KeyVault/vaults/kv-synthetic-test","changeType":"Create"}]}`, testDeploySubscription, testDeploySubscription)
		}
		return azcli.Result{Stdout: output, Stderr: f.whatIfStderr}, f.whatIfErr
	}
}

func testDeployRoot(az azcli.Executor) (*cobra.Command, *bytes.Buffer, *bytes.Buffer) {
	return testDeployRootWith(az, testDeployEnv)
}

func testDeployRootWith(az azcli.Executor, getenv func(string) (string, bool)) (*cobra.Command, *bytes.Buffer, *bytes.Buffer) {
	root := &cobra.Command{Use: "cao", SilenceErrors: true, SilenceUsage: true,
		Annotations: map[string]string{cobra.CommandDisplayNameAnnotation: "gh cao"}}
	root.AddCommand(deployCommand(deployAzureDeps{
		az:       az,
		getenv:   getenv,
		now:      func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) },
		template: func(path string) deployAzureTemplateSource { return deployAzureFileTemplate{path: path} },
	}))
	stdout, stderr := &bytes.Buffer{}, &bytes.Buffer{}
	root.SetOut(stdout)
	root.SetErr(stderr)
	return root, stdout, stderr
}

func runDeployPlan(t *testing.T, fake *fakeAz, config any) (string, string, string, error) {
	t.Helper()
	configPath := writeDeployConfig(t, config)
	out := filepath.Join(t.TempDir(), "plans", "plan.json")
	root, stdout, stderr := testDeployRoot(fake)
	root.SetArgs([]string{"deploy", "azure", "--config", configPath, "--template", testDeployTemplatePath(t), "--out", out})
	err := root.ExecuteContext(context.Background())
	return stdout.String(), stderr.String(), out, err
}

func TestDeployAzurePlanRunsOnlyReadOnlyCommands(t *testing.T) {
	for _, tt := range []struct {
		name        string
		groupExists bool
		whatIf      []string
		scope       string
	}{
		{"new resource group", false, []string{"deployment", "sub", "what-if"}, "subscription"},
		{"empty resource group", true, []string{"deployment", "group", "what-if"}, "resourceGroup"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			fake := &fakeAz{t: t, groupExists: tt.groupExists}
			stdout, _, out, err := runDeployPlan(t, fake, testDeployConfig())
			if err != nil {
				t.Fatal(err)
			}
			if len(fake.calls) == 0 || !slices.Equal(azcli.CommandPath(fake.calls[len(fake.calls)-1]), tt.whatIf) {
				t.Fatalf("calls = %q, want final %q", fake.calls, tt.whatIf)
			}
			for _, call := range fake.calls {
				if !azcli.IsReadOnly(call) {
					t.Fatalf("non-read-only call %q", call)
				}
			}
			for _, file := range fake.parameterFiles {
				if _, err := os.Stat(file); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("parameter file %s was not removed: %v", file, err)
				}
			}
			var plan deployAzurePlan
			content, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(content, &plan); err != nil {
				t.Fatal(err)
			}
			if plan.Kind != deployAzurePlanKind || plan.Scope != tt.scope || plan.CreatedAt != "2026-01-02T03:04:05Z" ||
				plan.TenantID != testDeployTenant || plan.WhatIf.Counts["Create"] != 2 || len(plan.Config.SHA256) != 64 || len(plan.Template.SHA256) != 64 {
				t.Fatalf("unexpected plan: %s", content)
			}
			if plan.Parameters["functionAppName"] != "fa-synthetic-test" || plan.Parameters["postgresRunRetentionDays"] != float64(45) {
				t.Fatalf("plan parameters do not map config: %v", plan.Parameters)
			}
			if plan.SecureParameters["sessionSecret"] != "env:CAO_TEST_SESSION_SECRET" {
				t.Fatalf("secure parameter reference missing: %v", plan.SecureParameters)
			}
			if !strings.Contains(stdout, "Create=2") {
				t.Fatalf("missing summary: %s", stdout)
			}
		})
	}
}

func TestDeployAzurePlanNeverExposesSecrets(t *testing.T) {
	leak := "echo " + strings.Join(testDeploySecrets(), " ")
	for _, tt := range []struct {
		name string
		fake *fakeAz
	}{
		{"success with noisy stderr", &fakeAz{whatIfStderr: leak}},
		{"what-if failure echoing secrets", &fakeAz{whatIfStderr: leak, whatIfErr: &azcli.ExitError{Code: 1, Stderr: leak}, whatIf: `{}`}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tt.fake.t = t
			stdout, stderr, out, err := runDeployPlan(t, tt.fake, testDeployConfig())
			observed := stdout + stderr
			if err != nil {
				observed += err.Error()
			}
			if plan, readErr := os.ReadFile(out); readErr == nil {
				observed += string(plan)
			}
			for _, call := range tt.fake.calls {
				observed += strings.Join(call, " ")
			}
			for _, secret := range testDeploySecrets() {
				if strings.Contains(observed, secret) {
					t.Fatalf("secret value exposed in argv, output, error, or plan:\n%s", observed)
				}
			}
			if tt.fake.whatIfErr != nil && (err == nil || !strings.Contains(err.Error(), "***")) {
				t.Fatalf("expected redacted what-if error, got %v", err)
			}
		})
	}
}

func TestDeployAzurePlanFailsClosedOnExistingInstance(t *testing.T) {
	id := "/subscriptions/" + testDeploySubscription + "/resourceGroups/rg-other/providers/Microsoft.KeyVault/vaults/kv-synthetic-test"
	for _, tt := range []struct {
		name string
		fake *fakeAz
		want string
	}{
		{"populated resource group", &fakeAz{groupExists: true, groupResources: `[{"id":"/subscriptions/x/resourceGroups/rg-synthetic-test/providers/Microsoft.Web/sites/other"}]`}, "already contains 1 resource"},
		{"named resource elsewhere", &fakeAz{namedResources: map[string]string{"kv-synthetic-test": `[{"id":"` + id + `"}]`}}, "kv-synthetic-test already exists"},
		{"what-if modifies", &fakeAz{whatIf: `{"status":"Succeeded","changes":[{"resourceId":"/x","changeType":"Modify"}]}`}, "reports Modify"},
		{"what-if failed", &fakeAz{whatIf: `{"status":"Failed","changes":[]}`}, "what-if status"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			tt.fake.t = t
			_, _, out, err := runDeployPlan(t, tt.fake, testDeployConfig())
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected %q, got %v", tt.want, err)
			}
			if tt.want != "what-if status" && tt.want != "reports Modify" && !strings.Contains(err.Error(), "only fresh deployments") {
				t.Fatalf("missing fresh-only guidance: %v", err)
			}
			if _, statErr := os.Stat(out); !errors.Is(statErr, os.ErrNotExist) {
				t.Fatal("failed plan wrote a plan file")
			}
		})
	}
}

func TestDeployAzurePlanRequiresSecretsBeforeAzure(t *testing.T) {
	fake := &fakeAz{t: t}
	configPath := writeDeployConfig(t, testDeployConfig())
	root, _, _ := testDeployRootWith(fake, func(string) (string, bool) { return "", false })
	root.SetArgs([]string{"deploy", "azure", "--config", configPath, "--template", testDeployTemplatePath(t), "--out", filepath.Join(t.TempDir(), "p.json")})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "CAO_TEST_POSTGRES_PASSWORD") {
		t.Fatalf("expected missing env error, got %v", err)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("Azure CLI called before secrets resolved: %q", fake.calls)
	}
}

func TestDeployAzurePlanUsesEmbeddedTopologyResources(t *testing.T) {
	t.Chdir(t.TempDir())
	t.Setenv("PATH", "")
	fake := &fakeAz{t: t}
	root, _, _ := testDeployRoot(fake)
	config := writeDeployConfig(t, testDeployConfig())
	out := filepath.Join(t.TempDir(), "plan.json")
	root.SetArgs([]string{"deploy", "azure", "--config", config, "--out", out})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	plan, err := readDeployAzurePlan(out)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Template.Origin != "embedded" || len(plan.Topology.Order) == 0 {
		t.Fatalf("missing embedded template or resolved topology: %+v", plan)
	}
	before := len(fake.calls)
	changed, err := loadDeployAzureConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	changed.config.Telemetry.OTLPEndpoint = "https://collector.example.test"
	plan.Topology, err = resolveDeployAzureTopology(context.Background(), changed.config)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeDeployAzurePlan(out, plan); err != nil {
		t.Fatal(err)
	}
	root, _, _ = testDeployRoot(fake)
	root.SetArgs([]string{"deploy", "azure", "--apply", "--config", config, "--plan-file", out})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "topology") {
		t.Fatalf("apply accepted changed topology: %v", err)
	}
	if len(fake.calls) != before {
		t.Fatal("Azure was called before topology drift was rejected")
	}
}

func TestDeployAzureTemplateMustMatchConfig(t *testing.T) {
	fake := &fakeAz{t: t}
	template := filepath.Join(t.TempDir(), "main.json")
	writeTestFile(t, template, `{"$schema":"https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#","parameters":{"location":{"type":"string"},"unexpectedRequired":{"type":"string"}},"resources":[]}`)
	root, _, _ := testDeployRoot(fake)
	root.SetArgs([]string{"deploy", "azure", "--config", writeDeployConfig(t, testDeployConfig()), "--template", template})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "template") {
		t.Fatalf("expected template mismatch, got %v", err)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("unexpected calls: %q", fake.calls)
	}
}

func TestDeployAzureHelpIsOffline(t *testing.T) {
	for _, args := range [][]string{{"deploy", "--help"}, {"deploy", "azure", "--help"}, {"deploy", "init", "--help"}, {"deploy", "generate", "--help"}, {"deploy", "check", "--help"}} {
		failing := azcli.ExecutorFunc(func(context.Context, ...string) (azcli.Result, error) {
			t.Fatal("help invoked the Azure CLI")
			return azcli.Result{}, nil
		})
		root, stdout, stderr := testDeployRoot(failing)
		root.SetArgs(args)
		if err := root.Execute(); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(stdout.String(), "gh cao "+strings.Join(args[:len(args)-1], " ")) || stderr.Len() != 0 {
			t.Fatalf("unexpected help output: %s %s", stdout, stderr)
		}
	}
}

func TestDeployAzureStandaloneHelpWithEmptyPath(t *testing.T) {
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "gh-cao")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-o", binary, "./cmd/gh-cao")
	build.Dir = repository
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, output)
	}
	working := t.TempDir()
	for _, args := range [][]string{{"deploy", "--help"}, {"deploy", "azure", "--help"}, {"deploy", "generate", "--help"}} {
		command := exec.Command(binary, args...)
		command.Dir = working
		command.Env = append(os.Environ(), "PATH=")
		output, err := command.CombinedOutput()
		if err != nil || !strings.Contains(string(output), "gh cao deploy") {
			t.Fatalf("%q: %v\n%s", args, err, output)
		}
	}
	command := exec.Command(binary, "deploy", "azure", "--config", "missing.json", "--template", "missing.json")
	command.Dir = working
	command.Env = append(os.Environ(), "PATH=")
	if output, err := command.CombinedOutput(); err == nil || !strings.Contains(string(output), "missing.json") {
		t.Fatalf("expected config error without Azure CLI: %v\n%s", err, output)
	}
	if files, _ := os.ReadDir(working); len(files) != 0 {
		t.Fatal("deploy help or failed plan wrote to the working directory")
	}
}

func TestDeployAzureModeFlags(t *testing.T) {
	for _, tt := range []struct {
		args []string
		want string
	}{
		{[]string{"--apply"}, "--apply requires --plan-file"},
		{[]string{"--plan", "--apply", "--plan-file", "p.json"}, "none of the others can be"},
		{[]string{"--plan-file", "p.json"}, "--plan-file is only valid with --apply"},
		{[]string{"extra"}, "unknown command"},
	} {
		fake := &fakeAz{t: t}
		root, _, _ := testDeployRoot(fake)
		root.SetArgs(append([]string{"deploy", "azure"}, tt.args...))
		if err := root.Execute(); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Fatalf("%q: expected %q, got %v", tt.args, tt.want, err)
		}
		if len(fake.calls) != 0 {
			t.Fatalf("%q: unexpected calls %q", tt.args, fake.calls)
		}
	}
}

func TestDeployAzureApplyValidatesPlanBeforeInputs(t *testing.T) {
	fake := &fakeAz{t: t}
	configPath := writeDeployConfig(t, testDeployConfig())
	planPath := filepath.Join(t.TempDir(), "plan.json")
	root, _, _ := testDeployRoot(fake)
	root.SetArgs([]string{"deploy", "azure", "--plan", "--config", configPath, "--template", testDeployTemplatePath(t), "--out", planPath})
	if err := root.Execute(); err != nil {
		t.Fatal(err)
	}
	planCalls := len(fake.calls)

	apply := func(config, template, plan string) error {
		root, stdout, _ := testDeployRoot(fake)
		root.SetArgs([]string{"deploy", "azure", "--apply", "--config", config, "--template", template, "--plan-file", plan})
		err := root.Execute()
		if stdout.Len() != 0 {
			t.Fatalf("apply wrote output: %s", stdout)
		}
		return err
	}
	if err := apply(configPath, testDeployTemplatePath(t), planPath); err == nil || !strings.Contains(err.Error(), "--source-dir") {
		t.Fatalf("expected explicit build-source failure, got %v", err)
	}

	changed := testDeployConfig()
	changed["retentionDays"] = 90
	if err := apply(writeDeployConfig(t, changed), testDeployTemplatePath(t), planPath); err == nil || !strings.Contains(err.Error(), "different config") {
		t.Fatalf("expected config mismatch, got %v", err)
	}
	otherTemplate := filepath.Join(t.TempDir(), "main.json")
	content, _ := os.ReadFile(testDeployTemplatePath(t))
	writeTestFile(t, otherTemplate, string(content)+"\n")
	if err := apply(configPath, otherTemplate, planPath); err == nil || !strings.Contains(err.Error(), "different template") {
		t.Fatalf("expected template mismatch, got %v", err)
	}
	tampered := filepath.Join(t.TempDir(), "tampered.json")
	planContent, _ := os.ReadFile(planPath)
	writeTestFile(t, tampered, strings.Replace(string(planContent), `"kind"`, `"unexpected": true, "kind"`, 1))
	if err := apply(configPath, testDeployTemplatePath(t), tampered); err == nil || !strings.Contains(err.Error(), "invalid plan file") {
		t.Fatalf("expected invalid plan, got %v", err)
	}
	oldPlan, err := readDeployAzurePlan(planPath)
	if err != nil {
		t.Fatal(err)
	}
	oldPlan.SchemaVersion = 1
	if err := writeDeployAzurePlan(tampered, oldPlan); err != nil {
		t.Fatal(err)
	}
	if err := apply(configPath, "", tampered); err == nil || !strings.Contains(err.Error(), "rerun --plan") {
		t.Fatalf("expected old plan to require regeneration, got %v", err)
	}
	if len(fake.calls) != planCalls {
		t.Fatalf("apply invoked the Azure CLI: %q", fake.calls[planCalls:])
	}
}

func TestDeployAzureConfigValidation(t *testing.T) {
	valid, err := json.Marshal(testDeployConfig())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseDeployAzureConfig(valid); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}
	mutate := func(change func(map[string]any)) []byte {
		config := testDeployConfig()
		change(config)
		content, err := json.Marshal(config)
		if err != nil {
			t.Fatal(err)
		}
		return content
	}
	resources := func(config map[string]any) map[string]any { return config["resources"].(map[string]any) }
	secrets := func(config map[string]any) map[string]any { return config["secrets"].(map[string]any) }
	for _, tt := range []struct {
		name    string
		content []byte
		want    string
	}{
		{"unknown top-level field", mutate(func(c map[string]any) { c["extra"] = true }), `unknown field "extra"`},
		{"unknown nested field", mutate(func(c map[string]any) { resources(c)["frontDoor"] = "fd-synthetic" }), `unknown field "frontDoor"`},
		{"missing subscription", mutate(func(c map[string]any) { delete(c, "subscription") }), "subscription is required"},
		{"missing key vault", mutate(func(c map[string]any) { delete(resources(c), "keyVault") }), "resources.keyVault is required"},
		{"missing retention", mutate(func(c map[string]any) { delete(c, "retentionDays") }), "retentionDays is required"},
		{"missing secret reference", mutate(func(c map[string]any) { delete(secrets(c), "sessionSecret") }), "secrets.sessionSecret is required"},
		{"angle placeholder", mutate(func(c map[string]any) { c["resourceGroup"] = "<resource-group>" }), "resourceGroup contains an unresolved placeholder"},
		{"template placeholder", mutate(func(c map[string]any) { c["oauth"] = map[string]any{"clientId": "${CLIENT_ID}"} }), "oauth.clientId contains an unresolved placeholder"},
		{"todo placeholder", mutate(func(c map[string]any) { c["allowedHosts"] = []string{"TODO.example.test"} }), "allowedHosts[0] contains an unresolved placeholder"},
		{"embedded secret string", mutate(func(c map[string]any) { secrets(c)["githubClientSecret"] = testDeployClientSecret }), "secret values must not be embedded"},
		{"embedded secret value field", mutate(func(c map[string]any) {
			secrets(c)["githubClientSecret"] = map[string]any{"value": testDeployClientSecret}
		}), `unknown field "value"`},
		{"embedded token", mutate(func(c map[string]any) {
			c["oauth"] = map[string]any{"clientId": "ghp_" + strings.Repeat("A", 36)}
		}), "appears to embed a secret value"},
		{"embedded connection string", mutate(func(c map[string]any) {
			resources(c)["postgresDatabase"] = "postgres://admin:hunter2@db.example.test/cao"
		}), "appears to embed a secret value"},
		{"ambiguous secret source", mutate(func(c map[string]any) {
			secrets(c)["sessionSecret"] = map[string]any{"env": "CAO_TEST_SESSION_SECRET", "keyVault": map[string]any{"id": "/x", "secretName": "y"}}
		}), "exactly one of env or keyVault"},
		{"retention out of range", mutate(func(c map[string]any) { c["retentionDays"] = 3 }), "between 7 and 3650"},
		{"mutable image tag", mutate(func(c map[string]any) { c["image"] = map[string]any{"tag": "latest"} }), "not latest"},
		{"no principals", mutate(func(c map[string]any) { c["access"] = map[string]any{} }), "at least one organization"},
		{"trailing content", append(append([]byte(nil), valid...), []byte(` {}`)...), "unexpected content"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseDeployAzureConfig(tt.content)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected %q, got %v", tt.want, err)
			}
			if strings.Contains(err.Error(), testDeployClientSecret) || strings.Contains(err.Error(), "hunter2") {
				t.Fatalf("validation error echoed secret material: %v", err)
			}
		})
	}
}

func TestDeployAzureKeyVaultReferenceStaysUnresolved(t *testing.T) {
	config := testDeployConfig()
	vault := "/subscriptions/" + testDeploySubscription + "/resourceGroups/rg-bootstrap-test/providers/Microsoft.KeyVault/vaults/kv-bootstrap-test"
	config["secrets"].(map[string]any)["sessionSecret"] = map[string]any{"keyVault": map[string]any{"id": vault, "secretName": "session-secret"}}
	parsed, err := parseDeployAzureConfig(mustJSON(t, config))
	if err != nil {
		t.Fatal(err)
	}
	secrets, err := resolveDeployAzureSecrets(parsed, testDeployEnv)
	if err != nil {
		t.Fatal(err)
	}
	entry := mustJSON(t, secrets.entries["sessionSecret"])
	if !strings.Contains(string(entry), `"reference"`) || strings.Contains(string(entry), testDeploySession) {
		t.Fatalf("Key Vault secret was resolved locally: %s", entry)
	}
}

func TestRootModuleDoesNotImportServer(t *testing.T) {
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	list := exec.Command("go", "list", "-deps", "./...")
	list.Dir = repository
	output, err := list.Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}
	for _, pkg := range strings.Fields(string(output)) {
		if strings.HasPrefix(pkg, "github.com/githubnext/gh-cao/server") {
			t.Fatalf("root module depends on server package %s", pkg)
		}
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	content, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return content
}
