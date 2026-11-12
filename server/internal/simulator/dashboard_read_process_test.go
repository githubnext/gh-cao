package simulator

import (
	"context"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func TestDashboardReadSyntheticProcessScenario(t *testing.T) {
	if os.Getenv("CAO_DASHBOARD_CONTRACT_PROCESS") != "1" {
		t.Skip("explicit synthetic selection: npm run test:integration:dashboard-contract")
	}
	fixture := newDashboardReadFixture(t)
	root := t.TempDir()
	binary := filepath.Join(root, "cao-dashboard")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	// #nosec G204 -- fixed Go package and arguments; output is in a test-owned temporary directory.
	build := exec.CommandContext(ctx, "go", "build", "-mod=readonly", "-o", binary, "./cmd/cao-dashboard")
	build.Dir = "../.."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build real dashboard server: %v\n%s", err, output)
	}
	var network net.ListenConfig
	listener, err := network.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	// Reserve an ephemeral address rather than claiming a fixed developer port.
	// If another process wins the bind race, readiness/exit checks fail explicitly.
	// #nosec G204 -- execute only the binary just built above with test-owned local fixture paths.
	command := exec.CommandContext(ctx, binary, "serve",
		"--listen="+address, "--postgres-url="+fixture.endpoint, "--redis-namespace=default", "--site="+fixture.config.SiteDirectory,
		"--access-token="+dashboardScenarioToken,
		"--database-queries="+fixture.config.DatabaseQueriesPath,
		"--dashboard-queries="+fixture.config.DashboardQueriesPath,
		"--agent-catalog="+fixture.config.AgentCatalogPath, "--mcp-contract="+fixture.config.MCPContractPath)
	command.Env = append(dashboardScenarioEnvironment(t, root), "CAO_OPERATIONAL_NAMESPACE=synthetic-contract")
	output, err := os.CreateTemp(root, "synthetic-server-*.log")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = output.Close() })
	command.Stdout, command.Stderr = output, output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			t.Error("synthetic server process did not stop")
		}
		if t.Failed() {
			content, err := os.ReadFile(output.Name())
			if err != nil {
				t.Error(err)
			} else {
				t.Logf("SYNTHETIC server log:\n%s", content)
			}
		}
	})
	endpoint := "http://" + address
	client := newDashboardScenarioClient(t, endpoint)
	startup, stop := context.WithTimeout(ctx, 15*time.Second)
	defer stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		request, err := http.NewRequestWithContext(startup, http.MethodGet, endpoint+"/api/readiness", nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+dashboardScenarioToken)
		response, err := client.client.Do(request)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				break
			}
		}
		select {
		case err := <-done:
			done <- err
			t.Fatalf("synthetic server exited before readiness: %v", err)
		case <-startup.Done():
			t.Fatal("synthetic server did not become ready")
		case <-ticker.C:
		}
	}
	runDashboardReadScenario(t, fixture, endpoint)
	select {
	case err := <-done:
		done <- err
		t.Fatalf("synthetic server exited during transport scenario: %v", err)
	default:
	}
}

func dashboardScenarioEnvironment(t *testing.T, root string) []string {
	t.Helper()
	// Do not inherit credentials, policy, database URLs, exporters or proxy
	// settings. Serving the existing snapshot requires no acquisition tools.
	environment := []string{
		"HOME=" + root, "USERPROFILE=" + root,
		"PATH=" + root,
		"OTEL_SDK_DISABLED=true", "DO_NOT_TRACK=1", "GH_TELEMETRY=0",
		"DASHBOARD_DATA_URL=disabled-synthetic-scenario",
	}
	for _, name := range []string{"SystemRoot", "WINDIR", "TMPDIR", "TMP", "TEMP"} {
		if value, ok := os.LookupEnv(name); ok {
			environment = append(environment, name+"="+value)
		}
	}
	return environment
}
