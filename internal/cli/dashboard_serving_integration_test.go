package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// This exercises the real native child, built site and PostgreSQL adapters;
// only GitHub acquisition is replaced with bounded synthetic evidence.
func TestDashboardServingIntegration(t *testing.T) {
	if os.Getenv("CAO_DASHBOARD_SERVING_INTEGRATION") != "1" {
		t.Skip("set CAO_DASHBOARD_SERVING_INTEGRATION=1 and CAO_DASHBOARD_TEST_SITE to an absolute built site directory")
	}
	admin := os.Getenv("CAO_DASHBOARD_TEST_POSTGRES_URL")
	managed := os.Getenv("CAO_DASHBOARD_TEST_DOCKER") == "1"
	if admin == "" && !managed {
		t.Fatal("selected integration requires CAO_DASHBOARD_TEST_POSTGRES_URL (disposable native PostgreSQL administrator URL) or CAO_DASHBOARD_TEST_DOCKER=1")
	}
	site := os.Getenv("CAO_DASHBOARD_TEST_SITE")
	if !filepath.IsAbs(site) {
		t.Fatal("selected integration requires CAO_DASHBOARD_TEST_SITE naming an absolute, real built site directory")
	}
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	directory, err := os.MkdirTemp(".", ".dashboard serving integration-")
	if err != nil {
		t.Fatal(err)
	}
	directory, err = filepath.Abs(directory)
	if err != nil {
		t.Fatal(err)
	}
	// Match the physical host path Docker inspects, including macOS symlinks.
	directory, err = filepath.EvalSymlinks(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.RemoveAll(directory); err != nil {
			t.Errorf("remove test-owned dashboard cache: %v", err)
		}
	})
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Minute)
	defer cancel()
	t.Setenv("CAO_POSTGRES_URL", "")
	binary := filepath.Join(directory, "native dashboard server")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	if prebuilt := os.Getenv("CAO_DASHBOARD_TEST_SERVER"); prebuilt != "" {
		content, err := os.ReadFile(prebuilt)
		if err != nil {
			t.Fatalf("read explicitly selected native server: %v", err)
		}
		if err := os.WriteFile(binary, content, 0o700); err != nil {
			t.Fatal(err)
		}
	} else {
		build := exec.CommandContext(ctx, "go", "build", "-mod=readonly", "-o", binary, "./cmd/cao-dashboard")
		build.Dir = filepath.Join(repository, "server")
		build.Env = append(os.Environ(), "GOOS="+runtime.GOOS, "GOARCH="+runtime.GOARCH)
		if output, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build native cao-dashboard (Go and populated module caches required): %v\n%s", err, output)
		}
	}
	builtSite := filepath.Join(directory, "built dashboard site")
	if err := os.CopyFS(builtSite, os.DirFS(site)); err != nil {
		t.Fatalf("copy selected built site: %v", err)
	}
	if _, _, cleanup, err := dashboardServerAssets(dashboardOptions{server: binary, site: builtSite}); err != nil {
		t.Fatalf("selected integration requires complete built dashboard assets: %v", err)
	} else {
		cleanup()
	}
	if admin != "" {
		t.Run("explicit", func(t *testing.T) {
			runtimeURL := provisionDashboardServingPostgres(t, ctx, binary, admin)
			runDashboardServingIntegration(t, ctx, binary, builtSite, filepath.Join(directory, "explicit backend"), runtimeURL, nil)
		})
	}
	if managed {
		t.Run("managed", func(t *testing.T) {
			engine, err := selectDashboardEngine(ctx, "docker")
			if err != nil {
				t.Fatalf("selected managed integration requires local Docker with Linux containers: %v", err)
			}
			runDashboardServingIntegration(t, ctx, binary, builtSite, filepath.Join(directory, "managed backend"), "", &engine)
		})
	}
}

func provisionDashboardServingPostgres(t *testing.T, ctx context.Context, binary, admin string) string {
	t.Helper()
	address, err := url.Parse(admin)
	if err != nil || (address.Scheme != "postgres" && address.Scheme != "postgresql") ||
		net.ParseIP(address.Hostname()) == nil || !net.ParseIP(address.Hostname()).IsLoopback() {
		t.Fatal("CAO_DASHBOARD_TEST_POSTGRES_URL must explicitly select disposable numeric-loopback PostgreSQL")
	}
	password, err := dashboardPostgresSecret()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, binary, "database", "provision-local",
		"--namespace", dashboardPostgresNamespace([]string{"octo/api"}))
	command.Env = append(dashboardServerEnvironment(), "CAO_DATABASE_ADMIN_URL="+admin,
		"CAO_LOCAL_POSTGRES_RUNTIME_PASSWORD="+password)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("provision explicitly selected disposable PostgreSQL: %v\n%s", err, output)
	}
	address.User = url.UserPassword("cao_dashboard_runtime", password)
	parameters := address.Query()
	parameters.Set("search_path", "cao")
	address.RawQuery = parameters.Encode()
	return address.String()
}

func runDashboardServingIntegration(t *testing.T, ctx context.Context, binary, builtSite, directory, postgresURL string, engine *dashboardEngine) {
	t.Helper()
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	// Keep the command's .gitignore update inside the uniquely owned workspace.
	t.Chdir(directory)
	cache := filepath.Join(directory, "private canonical cache")
	postgres := filepath.Join(cache, "postgres")
	if engine != nil {
		t.Cleanup(func() { cleanupDashboardServingPostgres(t, *engine, postgres) })
	}
	transport := &http.Transport{Proxy: nil}
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	t.Cleanup(transport.CloseIdleConnections)
	now := time.Now().UTC().Add(-time.Second)
	var previous dashboardPostgresState
	for phase := range 2 {
		output := &dashboardServingOutput{ready: make(chan string, 1)}
		session, stop := context.WithCancel(ctx)
		command := dashboardCommand("integration", dashboardTestGH(t, now), dashboardTestLogs(t))
		command.SilenceUsage, command.SilenceErrors = true, true
		command.SetOut(output)
		command.SetErr(output)
		args := []string{
			"--repo", "octo/api", "--cache-dir", cache, "--server-executable", binary,
			"--site", builtSite, "--container-engine", "docker", "--listen", "127.0.0.1:0",
			"--duration", "10m", "--refresh", "1m", "--count", "2",
		}
		if postgresURL != "" {
			args = append(args, "--postgres-url", postgresURL)
		}
		command.SetArgs(args)
		done := make(chan error, 1)
		exited := make(chan struct{})
		go func() {
			done <- command.ExecuteContext(session)
			close(exited)
		}()
		t.Cleanup(func() {
			stop()
			select {
			case <-exited:
			case <-time.After(20 * time.Second):
				t.Error("dashboard command did not reap its native child after cancellation")
			}
		})
		var address string
		select {
		case address = <-output.ready:
		case err := <-done:
			stop()
			t.Fatalf("dashboard phase %d exited before serving: %v\n%s", phase, err, output.diagnostics())
		case <-ctx.Done():
			stop()
			t.Fatalf("dashboard startup deadline: %v\n%s", ctx.Err(), output.diagnostics())
		}
		capability, err := url.Parse(address)
		if err != nil || capability.Scheme != "http" || capability.User != nil ||
			net.ParseIP(capability.Hostname()) == nil || !net.ParseIP(capability.Hostname()).IsLoopback() {
			t.Fatal("native dashboard did not announce a numeric loopback URL")
		}
		token := capability.Query().Get("access_token")
		if token == "" {
			t.Fatal("native dashboard did not announce its local access capability")
		}
		origin := capability.Scheme + "://" + capability.Host
		request := func(method, path, body string) (int, []byte, error) {
			request, err := http.NewRequestWithContext(session, method, origin+path, strings.NewReader(body))
			if err != nil {
				return 0, nil, err
			}
			request.Header.Set("Authorization", "Bearer "+token)
			request.Header.Set("Content-Type", "application/json")
			response, err := client.Do(request)
			if err != nil {
				return 0, nil, err
			}
			defer func() { _ = response.Body.Close() }()
			content, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
			return response.StatusCode, content, err
		}
		status, shell, err := request(http.MethodGet, "/", "")
		if err != nil || status != http.StatusOK ||
			!bytes.Contains(shell, []byte(`<meta name="dashboard-data-backend" content="server-http">`)) {
			t.Fatalf("real dashboard shell did not select server-http: status=%d error=%v", status, err)
		}
		deadline := time.Now().Add(90 * time.Second)
		for {
			lastStatus, content, err := request(http.MethodPost, "/api/v1/query",
				`{"sourceNames":["runs"],"pagination":{"runs":{"limit":5}}}`)
			var result struct {
				Revision int64 `json:"revision"`
				Sources  map[string]struct {
					Rows []map[string]any `json:"rows"`
				} `json:"sources"`
			}
			if err == nil && lastStatus == http.StatusOK && json.Unmarshal(content, &result) == nil {
				found := false
				for _, row := range result.Sources["runs"].Rows {
					found = found || (row["run"] == "123" && row["organization"] == "octo" &&
						row["repository"] == "api" && row["run-status"] == "in_progress")
				}
				if found && result.Revision > 0 {
					break
				}
			}
			select {
			case err := <-done:
				t.Fatalf("native dashboard exited during canonical query: %v\n%s", err, output.diagnostics())
			case <-session.Done():
				t.Fatalf("canonical query canceled: %v", session.Err())
			case <-time.After(250 * time.Millisecond):
			}
			if time.Now().After(deadline) {
				t.Fatalf("real declarative runs query never returned synthetic run 123: status=%d error=%v\n%s",
					lastStatus, err, output.diagnostics())
			}
		}
		status, readiness, err := request(http.MethodGet, "/api/readiness", "")
		var health struct {
			Ready bool `json:"ready"`
			Data  struct {
				Available bool `json:"available"`
			} `json:"data"`
		}
		if err != nil || status != http.StatusOK || json.Unmarshal(readiness, &health) != nil ||
			!health.Ready || !health.Data.Available {
			t.Fatalf("real PostgreSQL readiness failed: status=%d error=%v body=%s", status, err, readiness)
		}
		var state dashboardPostgresState
		if engine != nil {
			state, err = readDashboardPostgresState(filepath.Join(postgres, "state.json"))
			if err != nil || state.ContainerID == "" {
				t.Fatalf("managed PostgreSQL state was not persisted: %v", err)
			}
			container, err := engine.inspectPostgres(ctx, state.ContainerID)
			if err != nil || !container.State.Running {
				t.Fatalf("managed PostgreSQL is not a real running container: %v", err)
			}
			if _, err := validateDashboardPostgresContainer(container, state, filepath.Join(postgres, "data")); err != nil {
				t.Fatal(err)
			}
			if phase == 1 && (state.ContainerID != previous.ContainerID || state.Owner != previous.Owner) {
				t.Fatal("restart replaced the persistent test-owned PostgreSQL container")
			}
		} else if _, err := os.Lstat(postgres); !os.IsNotExist(err) {
			t.Fatal("explicit PostgreSQL path unexpectedly created managed container state")
		}
		stop()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("cancellation did not gracefully stop dashboard: %v\n%s", err, output.diagnostics())
			}
		case <-time.After(20 * time.Second):
			t.Fatal("cancellation did not reap the native dashboard child")
		}
		client.CloseIdleConnections()
		check, err := client.Get(origin + "/api/readiness")
		if err == nil {
			_ = check.Body.Close()
			t.Fatal("native dashboard listener survived command cancellation")
		}
		if phase == 0 && engine != nil {
			previous = state
			if _, err := engine.run(ctx, "stop", "--time", "10", state.ContainerID); err != nil {
				t.Fatalf("stop uniquely owned PostgreSQL before persistence/restart check: %v", err)
			}
		}
	}
}

type dashboardServingOutput struct {
	mutex sync.Mutex
	bytes.Buffer
	ready chan string
}

func (output *dashboardServingOutput) Write(value []byte) (int, error) {
	output.mutex.Lock()
	defer output.mutex.Unlock()
	n, err := output.Buffer.Write(value)
	if _, rest, found := strings.Cut(output.Buffer.String(), "Dashboard: "); found {
		if fields := strings.Fields(rest); len(fields) > 0 {
			select {
			case output.ready <- fields[0]:
			default:
			}
		}
	}
	return n, err
}

func (output *dashboardServingOutput) diagnostics() string {
	output.mutex.Lock()
	defer output.mutex.Unlock()
	var redacted []string
	for _, line := range strings.Split(output.Buffer.String(), "\n") {
		if strings.Contains(line, "access_token=") {
			line = "[local dashboard capability URL redacted]"
		}
		redacted = append(redacted, line)
	}
	return strings.Join(redacted, "\n")
}

func cleanupDashboardServingPostgres(t *testing.T, engine dashboardEngine, directory string) {
	t.Helper()
	state, err := readDashboardPostgresState(filepath.Join(directory, "state.json"))
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		t.Errorf("cannot safely identify test-owned PostgreSQL for cleanup: %v", err)
		return
	}
	// The deterministic name also finds a container created just before a setup
	// failure persisted its ID. Never enumerate or remove unrelated containers.
	digest := sha256.Sum256([]byte(directory))
	name := fmt.Sprintf("gh-cao-postgres-%x", digest[:12])
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	found, err := engine.run(ctx, "container", "ls", "--all", "--filter", "name=^"+name+"$", "--format", "{{.ID}}")
	if err != nil {
		t.Errorf("locate uniquely named test PostgreSQL: %v", err)
		return
	}
	if strings.TrimSpace(string(found)) == "" {
		return
	}
	container, err := engine.inspectPostgres(ctx, name)
	if err != nil || container.Config.Labels[dashboardPostgresLabel] != state.Owner ||
		container.Config.Image != dashboardPostgresImage ||
		(state.ContainerID != "" && container.ID != state.ContainerID) {
		t.Error("refusing cleanup: container does not match test cache owner, image and identity")
		return
	}
	if runtime.GOOS != "windows" {
		if err := validateDashboardPostgresOwnership(container, state, filepath.Join(directory, "data")); err != nil {
			t.Errorf("refusing to change permissions outside test-owned PostgreSQL data: %v", err)
			return
		}
		if !container.State.Running {
			if _, err := engine.run(ctx, "start", container.ID); err != nil {
				t.Errorf("start owned PostgreSQL to restore cache ownership: %v", err)
				return
			}
		}
		// PostgreSQL changes its bind mount to its container UID. Restore the
		// invoking user's ownership before removing this private test workspace.
		owner := fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())
		if _, err := engine.run(ctx, "exec", "--user", "0", container.ID, "chown", "-R", owner, "/var/lib/postgresql/data"); err != nil {
			t.Errorf("restore test-owned PostgreSQL cache ownership: %v", err)
		}
	}
	if _, err := engine.run(ctx, "container", "rm", "--force", container.ID); err != nil {
		t.Errorf("remove uniquely owned test PostgreSQL: %v", err)
	}
}
