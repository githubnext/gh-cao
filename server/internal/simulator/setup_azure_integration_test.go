package simulator

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestCAOSetupAzureIntegration(t *testing.T) {
	testCAOSetupAzureIntegration(t, "successful deployment", "plan drift", "provisioning failure")
}

func testCAOSetupAzureIntegration(t *testing.T, scenarios ...string) {
	t.Helper()
	if os.Getenv("CAO_SETUP_AZURE_INTEGRATION") != "1" {
		t.Skip("run npm run test:integration:setup-azure to exercise setup with GitHub and Azure simulators")
	}
	tools := []string{"go", "gh", "git", "sh"}
	if os.Getenv("CAO_SETUP_AZURITE_BINARY") == "" {
		tools = append(tools, "docker")
	}
	for _, tool := range tools {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatal(err)
		}
	}
	catalog, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Minute)
	defer cancel()
	root := t.TempDir()
	binary := filepath.Join(root, "gh-cao")
	buildEnvironment := setupAzureEnvironment(setupAzureToolEnvironment("PATH", "HOME", "TMPDIR", "GOPATH", "GOMODCACHE", "GOCACHE", "CGO_ENABLED", "SDKROOT", "DEVELOPER_DIR"), map[string]string{
		"GOPROXY": "off", "GOSUMDB": "off", "GOVCS": "*:off", "GOTOOLCHAIN": "local", "GOENV": "off", "GOFLAGS": "-mod=readonly",
	})
	bootstrapRun(t, ctx, catalog, buildEnvironment, "go", "build", "-mod=readonly", "-ldflags=-X main.version=v0.0.1", "-o", binary, "./cmd/gh-cao")
	runtimeFiles := setupRuntimeFixtureFiles(t, catalog)
	storage := startSetupAzurite(t, ctx)
	for _, scenario := range scenarios {
		t.Run(scenario, func(t *testing.T) {
			journey := scenario == "private farm journey"
			fixtureFiles := runtimeFiles
			if journey {
				fixtureFiles = nil
			}
			f := newSetupFixture(t, ctx, root, catalog, binary, fixtureFiles)
			api := newAzureSetupAPI(t, catalog, storage)
			oauth := newSetupAzureOAuth()
			adapter, endpoint := installAzureCLIAdapter(t, api)
			proxy, ca := bootstrapProxy(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Host == setupAzureHost || r.Host == setupAzureSCM {
					api.serveIngress(w, r)
					return
				}
				if oauth.serveHTTP(w, r) {
					return
				}
				f.api.ServeHTTP(w, r)
			}), setupAzureHost, setupAzureSCM)
			testBinary, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			overrides := map[string]string{
				"PATH":                  adapter,
				"CAO_SETUP_TEST_BINARY": testBinary, "CAO_SETUP_AZURE_ENDPOINT": endpoint,
				"GORACE":      "atexit_sleep_ms=0",
				"HTTPS_PROXY": proxy, "HTTP_PROXY": proxy, "ALL_PROXY": proxy,
				"https_proxy": proxy, "http_proxy": proxy, "all_proxy": proxy,
				"SSL_CERT_FILE": ca, "CURL_CA_BUNDLE": ca, "GIT_SSL_CAINFO": ca, "NODE_EXTRA_CA_CERTS": ca,
				"AZURE_CONFIG_DIR":             filepath.Join(f.root, "azure-config"),
				"GIT_ALLOW_PROTOCOL":           "file",
				"AZURE_CORE_COLLECT_TELEMETRY": "false",
			}
			for name, value := range setupAzureSecrets() {
				overrides[name] = value
			}
			f.env = setupAzureEnvironment(f.env, overrides)
			policy, err := json.Marshal(f.policy)
			if err != nil {
				t.Fatal(err)
			}
			setupWrite(t, f.policyPath(), policy)
			if journey {
				setupAzurePrivateFarm(t, ctx, f, catalog, binary)
			} else {
				f.assertSuccess(t, f.run(t, ctx, ""), []string{"platform/control"}, "workflow-token")
				f.assertRequests(t, ctx, false)
			}
			configuredPolicy, githubState := setupRead(t, f.policyPath()), f.state(t)
			setupWrite(t, filepath.Join(f.checkout, ".github/workflows/cao.azure.json"),
				[]byte(`{"extends":"cao.json","control-plane":{"web":{"host":{"target":{"module":"azure-functions"},"operational-store":{"backend":"postgres"}}}}}`))
			if scenario == "successful deployment" || journey {
				api.functions = newSetupAzureFunctions(t, ctx)
				if journey {
					api.functions.proxy, api.functions.ca = proxy, ca
				}
			}
			config := filepath.Join(f.checkout, ".github/cao/deploy-azure.json")
			setupWrite(t, config, setupAzureConfig(t))
			tree := setupCheckoutState(t, f.checkout)
			planPath, receiptPath := filepath.Join(f.root, "plan.json"), filepath.Join(f.root, "receipt.json")
			common := []string{"--config", config, "--template", filepath.Join(catalog, "server/azure/main.json")}
			preflight := runSetupAzure(t, ctx, f, "", slices.Concat(common, []string{"--preflight", "--json"})...)
			var report struct {
				Kind, Status string
				Checks       []struct{ Status string }
			}
			if err := json.Unmarshal(preflight, &report); err != nil ||
				report.Kind != "cao.deploy.azure.preflight" || report.Status != "pass" || len(report.Checks) != 13 {
				t.Fatalf("unexpected preflight report: %s (%v)", preflight, err)
			}
			for _, check := range report.Checks {
				if check.Status != "pass" && check.Status != "info" {
					t.Fatalf("preflight did not pass every required check: %s", preflight)
				}
			}
			runSetupAzure(t, ctx, f, "", slices.Concat(common, []string{"--plan", "--out", planPath})...)
			plan := setupRead(t, planPath)
			assertSetupAzureNoSecrets(t, plan)
			api.mu.Lock()
			mutated := api.group || api.registry || api.deployed || len(api.images) != 0
			api.mu.Unlock()
			if mutated {
				t.Fatal("preflight or plan mutated Azure state")
			}
			if scenario == "plan drift" {
				api.mu.Lock()
				api.drift = true
				api.mu.Unlock()
			}
			if scenario == "provisioning failure" {
				api.mu.Lock()
				api.failProvision = true
				api.mu.Unlock()
			}
			failure := map[string]string{"plan drift": "azure plan drift detected", "provisioning failure": "database provision failed before ingestion"}[scenario]
			apply := slices.Concat(common, []string{"--apply", "--plan-file", planPath, "--source-dir", catalog,
				"--repo-dir", f.checkout, "--empty-evidence", "--receipt", receiptPath, "--timeout", "2m"})
			if journey {
				api.snapshot = true
				snapshot := setupAzureFarmSnapshot(t, 1)
				apply = slices.Concat(common, []string{"--apply", "--plan-file", planPath, "--source-dir", catalog,
					"--repo-dir", f.checkout, "--snapshot-dir", snapshot, "--receipt", receiptPath, "--timeout", "2m"})
			}
			runSetupAzure(t, ctx, f, failure, apply...)
			if failure == "" {
				assertSetupAzureReceipt(t, ctx, api, receiptPath, plan)
				if journey {
					assertSetupAzureFarmJourney(t, ctx, api.functions, oauth, proxy, ca)
				}
				api.functions.assertShutdown(t, ctx)
				before := setupRead(t, receiptPath)
				runSetupAzure(t, ctx, f, "already exists", apply...)
				if !bytes.Equal(before, setupRead(t, receiptPath)) {
					t.Fatal("repeated apply overwrote the deployment receipt")
				}
				// A successful deployment is not an upgrade or repeat-apply authority.
				runSetupAzure(t, ctx, f, "only fresh deployments are supported", slices.Concat(common, []string{"--plan", "--out", planPath})...)
			} else if _, err := os.Stat(receiptPath); !os.IsNotExist(err) {
				t.Fatal("failed apply created a success-shaped receipt")
			}
			if !bytes.Equal(plan, setupRead(t, planPath)) {
				t.Fatal("apply or rejected replanning changed the reviewed plan")
			}
			api.mu.Lock()
			defer api.mu.Unlock()
			if len(api.unhandled) != 0 {
				t.Fatalf("unsupported Azure simulator requests: %v", api.unhandled)
			}
			if scenario == "plan drift" && (api.group || api.registry || api.deployed || len(api.images) != 0) {
				t.Fatal("plan drift caused an Azure mutation")
			}
			if scenario == "provisioning failure" && (api.executions[setupAzureApp+"-ingest"] != "" || api.restarted || api.references) {
				t.Fatal("failed provisioning proceeded to ingestion, restart, or verification")
			}
			if !bytes.Equal(configuredPolicy, setupRead(t, f.policyPath())) || !bytes.Equal(githubState, f.state(t)) ||
				!reflect.DeepEqual(tree, setupCheckoutState(t, f.checkout)) {
				t.Fatal("Azure deployment changed reviewed GitHub authority or consumer files")
			}
			t.Logf("real setup and Azure plan/apply completed scenario %q through %d simulated Azure commands/HTTPS requests", scenario, len(api.calls))
		})
	}
}

func setupAzureEnvironment(environment []string, overrides map[string]string) []string {
	result := make([]string, 0, len(environment)+len(overrides))
	for _, item := range environment {
		name, _, _ := strings.Cut(item, "=")
		if _, replaced := overrides[name]; !replaced {
			result = append(result, item)
		}
	}
	for name, value := range overrides {
		result = append(result, name+"="+value)
	}
	return result
}

func setupAzureToolEnvironment(names ...string) []string {
	environment := make([]string, 0, len(names))
	for _, name := range names {
		if value, ok := os.LookupEnv(name); ok {
			environment = append(environment, name+"="+value)
		}
	}
	return environment
}

func setupAzureSecrets() map[string]string {
	return map[string]string{ // #nosec G101 -- synthetic test-only values without any external authority.
		"CAO_SIM_POSTGRES_PASSWORD": "synthetic-admin-password-material-0001",
		"CAO_SIM_OWNER_PASSWORD":    "synthetic-owner-password-material-0002",
		"CAO_SIM_RUNTIME_PASSWORD":  "synthetic-runtime-password-material-0003",
		"CAO_SIM_CLIENT_SECRET":     "synthetic-oauth-client-secret-0004",
		"CAO_SIM_SESSION_SECRET":    "synthetic-session-secret-material-0005",
	}
}

func setupAzureConfig(t *testing.T) []byte {
	t.Helper()
	secrets := map[string]any{}
	for field, variable := range map[string]string{ // #nosec G101 -- environment variable names, never credential values.
		"postgresAdministratorPassword": "CAO_SIM_POSTGRES_PASSWORD", "postgresOwnerPassword": "CAO_SIM_OWNER_PASSWORD",
		"postgresRuntimePassword": "CAO_SIM_RUNTIME_PASSWORD", "githubClientSecret": "CAO_SIM_CLIENT_SECRET", "sessionSecret": "CAO_SIM_SESSION_SECRET",
	} {
		secrets[field] = map[string]string{"env": variable}
	}
	content, err := json.Marshal(map[string]any{
		"schemaVersion": 1, "environment": "development", "subscription": setupAzureSubscription, "resourceGroup": setupAzureGroup, "location": "westus3",
		"resources": map[string]string{"functionApp": setupAzureApp, "storageAccount": "stcaosetupsim", "containerRegistry": setupAzureRegistry, "keyVault": "kv-cao-setup-sim"},
		"image":     map[string]string{"tag": "v0.0.0-test"}, "allowedHosts": []string{setupAzureHost},
		"access":        map[string]any{"organizations": []string{"platform"}, "users": []string{}, "teams": []string{}},
		"retentionDays": 45, "oauth": map[string]string{"clientId": "Iv1.caosetupsim"}, "secrets": secrets,
	})
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func runSetupAzure(t *testing.T, ctx context.Context, f *setupFixture, failure string, args ...string) []byte {
	t.Helper()
	command := exec.CommandContext(ctx, "gh", append([]string{"cao", "deploy", "azure"}, args...)...) // #nosec G204 -- real CLI and test-owned arguments in an isolated environment.
	command.Dir, command.Env = f.checkout, f.env
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	assertSetupAzureNoSecrets(t, stdout.Bytes())
	assertSetupAzureNoSecrets(t, stderr.Bytes())
	if failure != "" {
		if err == nil || !strings.Contains(stderr.String(), failure) {
			t.Fatalf("want Azure failure %q, got %v\nstdout: %s\nstderr: %s", failure, err, &stdout, &stderr)
		}
	} else if err != nil {
		t.Fatalf("Azure command failed: %v\nstdout: %s\nstderr: %s", err, &stdout, &stderr)
	}
	return stdout.Bytes()
}

func assertSetupAzureNoSecrets(t *testing.T, content []byte) {
	t.Helper()
	for _, value := range setupAzureSecrets() {
		if bytes.Contains(content, []byte(value)) {
			t.Fatal("Azure setup exposed a secure parameter value")
		}
	}
}

func assertSetupAzureReceipt(t *testing.T, ctx context.Context, api *azureSetupAPI, path string, plan []byte) {
	t.Helper()
	content := setupRead(t, path)
	assertSetupAzureNoSecrets(t, content)
	var receipt struct {
		Kind, PlanSHA256, Environment, ImageAssurance, SourceRevision, ContextSHA256 string
		JobExecution, ProvisionExecution                                             string
		Images                                                                       []struct{ Target, Repository, Reference, ConfiguredReference, Digest, Scan, SBOM, Provenance string }
		Verification                                                                 []struct {
			Check  string
			Passed bool
		}
	}
	if err := json.Unmarshal(content, &receipt); err != nil {
		t.Fatal(err)
	}
	if receipt.Kind != "cao.deploy.azure.receipt" || receipt.PlanSHA256 != fmt.Sprintf("%x", sha256.Sum256(plan)) ||
		receipt.Environment != "development" || receipt.ImageAssurance != "not-evaluated-development-only" ||
		len(receipt.SourceRevision) != 40 || len(receipt.ContextSHA256) != 64 ||
		receipt.JobExecution != setupAzureApp+"-ingest-execution" || receipt.ProvisionExecution != setupAzureApp+"-db-provision-execution" ||
		len(receipt.Images) != 3 || len(receipt.Verification) != 6 {
		t.Fatalf("incomplete or incorrectly bound Azure receipt: %s", content)
	}
	for _, image := range receipt.Images {
		if image.Reference != image.ConfiguredReference || !strings.HasSuffix(image.Reference, "@"+image.Digest) ||
			image.Scan != "not-performed" || image.SBOM != "not-generated" || image.Provenance != "local-context-not-attested" {
			t.Fatalf("incorrect development image binding: %+v", image)
		}
		manifest, err := api.storage.request(ctx, http.MethodGet, "/setup/"+image.Repository+".json", nil)
		if err != nil {
			t.Fatal(err)
		}
		var build struct{ Target, ContextSHA256 string }
		if err := json.Unmarshal(manifest, &build); err != nil || build.Target != image.Target ||
			len(build.ContextSHA256) != 64 || image.Digest != fmt.Sprintf("sha256:%x", sha256.Sum256(manifest)) {
			t.Fatal("receipt image does not match the Azurite-backed simulated build")
		}
	}
	checks := make([]string, 0, len(receipt.Verification))
	for _, verification := range receipt.Verification {
		if !verification.Passed {
			t.Fatalf("verification did not pass: %s", verification.Check)
		}
		checks = append(checks, verification.Check)
	}
	if !slices.Equal(checks, []string{"health", "readiness", "signed-out-campaigns", "root-login", "github-oauth", "scm-denied"}) {
		t.Fatalf("incorrect Azure verification checks: %v", checks)
	}
	state, err := api.storage.request(ctx, http.MethodGet, "/setup/deployment.json", nil)
	if err != nil {
		t.Fatal(err)
	}
	assertSetupAzureNoSecrets(t, state)
	var stored map[string]string
	if err := json.Unmarshal(state, &stored); err != nil {
		t.Fatal(err)
	}
	api.mu.Lock()
	defer api.mu.Unlock()
	if !reflect.DeepEqual(stored, api.configured) || len(api.locked) != 3 || api.referenceReads != 2 || !api.references {
		t.Fatal("deployment receipt was not backed by persisted Azurite state, locked images, and resolved references")
	}
}
