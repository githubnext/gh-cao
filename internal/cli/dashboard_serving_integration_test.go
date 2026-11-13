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
	dockerExplicit := os.Getenv("CAO_DASHBOARD_TEST_DOCKER_EXPLICIT") == "1"
	if admin == "" && !managed && !dockerExplicit {
		t.Fatal("selected integration requires CAO_DASHBOARD_TEST_POSTGRES_URL (disposable native PostgreSQL administrator URL), CAO_DASHBOARD_TEST_DOCKER=1, or CAO_DASHBOARD_TEST_DOCKER_EXPLICIT=1")
	}
	site := os.Getenv("CAO_DASHBOARD_TEST_SITE")
	if !filepath.IsAbs(site) {
		t.Fatal("selected integration requires CAO_DASHBOARD_TEST_SITE naming an absolute, real built site directory")
	}
	repository, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	temporary := filepath.Join(repository, ".tmp")
	if err := os.MkdirAll(temporary, 0o700); err != nil {
		t.Fatal(err)
	}
	directory, err := os.MkdirTemp(temporary, ".dashboard serving integration-")
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
			t.Run("synthetic-collection", func(t *testing.T) {
				runDashboardServingIntegration(t, ctx, binary, builtSite, filepath.Join(directory, "explicit backend"), runtimeURL, nil)
			})
			if cli := os.Getenv("CAO_DASHBOARD_TEST_CLI"); cli != "" {
				t.Run("cli-process", func(t *testing.T) {
					runDashboardServingCLIProcess(t, ctx, cli, binary, builtSite, filepath.Join(directory, "native CLI process"), runtimeURL)
				})
			} else {
				t.Log("CAO_DASHBOARD_TEST_CLI not supplied: native gh-cao process smoke unavailable; synthetic collection uses dashboardCommand")
			}
		})
	}
	if managed {
		t.Run("managed", func(t *testing.T) {
			if runtime.GOOS == "windows" {
				t.Fatal("managed Windows PostgreSQL is unsupported: production private-file checks require a Windows ACL implementation and Docker Desktop bind-mount identity is not normalized; use an explicitly provisioned disposable PostgreSQL URL instead")
			}
			engine, err := selectDashboardEngine(ctx, "docker")
			if err != nil {
				t.Fatalf("selected managed integration requires local Docker with Linux containers: %v", err)
			}
			runDashboardServingIntegration(t, ctx, binary, builtSite, filepath.Join(directory, "managed backend"), "", &engine)
		})
	}
	if dockerExplicit {
		t.Run("docker-explicit", func(t *testing.T) {
			engine, err := selectDashboardEngine(ctx, "docker")
			if err != nil {
				t.Fatalf("selected Docker explicit integration requires local Docker with Linux containers: %v", err)
			}
			admin := startDashboardServingDockerPostgres(t, ctx, engine)
			runtimeURL := provisionDashboardServingPostgres(t, ctx, binary, admin)
			t.Run("synthetic-collection", func(t *testing.T) {
				runDashboardServingIntegration(t, ctx, binary, builtSite, filepath.Join(directory, "Docker explicit backend"), runtimeURL, nil)
			})
			if cli := os.Getenv("CAO_DASHBOARD_TEST_CLI"); cli != "" {
				t.Run("cli-process", func(t *testing.T) {
					runDashboardServingCLIProcess(t, ctx, cli, binary, builtSite, filepath.Join(directory, "Docker native CLI process"), runtimeURL)
				})
			} else {
				t.Log("CAO_DASHBOARD_TEST_CLI not supplied: native gh-cao process smoke unavailable")
			}
			t.Log("Docker-backed explicit PostgreSQL exercised; automatic managed-cache portability is not established")
		})
	}
}

func startDashboardServingDockerPostgres(t *testing.T, ctx context.Context, engine dashboardEngine) string {
	t.Helper()
	owner, err := dashboardPostgresSecret()
	if err != nil {
		t.Fatal(err)
	}
	password, err := dashboardPostgresSecret()
	if err != nil {
		t.Fatal(err)
	}
	name := "gh-cao-serving-explicit-" + owner[:24]
	t.Cleanup(func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		found, err := engine.run(cleanup, "container", "ls", "--all", "--filter", "name=^"+name+"$", "--format", "{{.ID}}")
		if err != nil {
			t.Errorf("locate uniquely named Docker explicit fixture: %v", err)
			return
		}
		if strings.TrimSpace(string(found)) == "" {
			return
		}
		container, err := engine.inspectPostgres(cleanup, name)
		if err != nil || container.Config.Labels[dashboardPostgresLabel] != owner ||
			container.Config.Image != dashboardPostgresImage {
			t.Error("refusing Docker explicit cleanup: fixture owner or image mismatch")
			return
		}
		if _, err := engine.run(cleanup, "container", "rm", "--force", "--volumes", container.ID); err != nil {
			t.Errorf("remove uniquely owned Docker explicit fixture and anonymous volumes: %v", err)
		}
	})
	if _, err := engine.run(ctx, "image", "inspect", dashboardPostgresImage); err != nil {
		if _, err := engine.run(ctx, "pull", "--quiet", dashboardPostgresImage); err != nil {
			t.Fatalf("pull Docker explicit PostgreSQL fixture image: %v", err)
		}
	}
	// No host bind mounts or production managed-cache state: Docker owns this
	// fixture's disposable anonymous data volume on every host platform.
	if _, err := engine.run(ctx, "run", "--detach", "--name", name,
		"--label", dashboardPostgresLabel+"="+owner, "--publish", "127.0.0.1::5432",
		"--env", "POSTGRES_DB=cao_dashboard", "--env", "POSTGRES_USER=cao_bootstrap",
		"--env", "POSTGRES_PASSWORD="+password,
		dashboardPostgresImage, "postgres", "-c", "max_locks_per_transaction=512"); err != nil {
		t.Fatalf("start Docker explicit PostgreSQL fixture: %v", err)
	}
	container, err := engine.inspectPostgres(ctx, name)
	if err != nil || container.Config.Labels[dashboardPostgresLabel] != owner ||
		container.Config.Image != dashboardPostgresImage {
		t.Fatalf("Docker explicit PostgreSQL identity unavailable: %v", err)
	}
	ports := container.NetworkSettings.Ports["5432/tcp"]
	if len(ports) != 1 || ports[0].HostIP != "127.0.0.1" {
		t.Fatal("Docker explicit PostgreSQL must publish exactly one numeric-loopback port")
	}
	port := ports[0].HostPort
	if err := validateDashboardListen(net.JoinHostPort("127.0.0.1", port)); err != nil || port == "0" {
		t.Fatal("Docker explicit PostgreSQL published an invalid port")
	}
	ready, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	for {
		if _, err := engine.run(ready, "exec", container.ID, "pg_isready", "-h", "127.0.0.1",
			"-U", "cao_bootstrap", "-d", "cao_dashboard"); err == nil {
			break
		}
		select {
		case <-ready.Done():
			t.Fatal("Docker explicit PostgreSQL did not become ready")
		case <-time.After(250 * time.Millisecond):
		}
	}
	address := &url.URL{Scheme: "postgres", Host: net.JoinHostPort("127.0.0.1", port),
		Path: "/cao_dashboard", User: url.UserPassword("cao_bootstrap", password), RawQuery: "sslmode=disable"}
	return address.String()
}

func runDashboardServingCLIProcess(t *testing.T, parent context.Context, cli, binary, builtSite, directory, postgresURL string) {
	t.Helper()
	if !filepath.IsAbs(cli) {
		t.Fatal("CAO_DASHBOARD_TEST_CLI must name an absolute native gh-cao executable")
	}
	if err := os.Mkdir(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(parent, 45*time.Second)
	defer cancel()
	output := &dashboardServingOutput{ready: make(chan string, 1)}
	command := exec.CommandContext(ctx, cli, "dashboard", "--repo", "octo/api",
		"--cache-dir", filepath.Join(directory, "offline canonical cache"),
		"--server-executable", binary, "--site", builtSite,
		"--listen", "127.0.0.1:0", "--duration", "10s", "--count", "2")
	command.Dir = directory
	// No gh executable, inherited credentials, or operator configuration: this
	// checks offline process serving, not successful GitHub acquisition.
	command.Env = append(dashboardServerEnvironment(), "PATH="+directory, "HOME="+directory,
		"USERPROFILE="+directory, "CAO_POSTGRES_URL="+postgresURL)
	command.Stdout, command.Stderr = output, output
	command.WaitDelay = 10 * time.Second
	if err := command.Start(); err != nil {
		t.Fatalf("start explicitly selected native gh-cao: %v", err)
	}
	done := make(chan error, 1)
	exited := make(chan struct{})
	go func() {
		done <- command.Wait()
		close(exited)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-exited:
		case <-time.After(15 * time.Second):
			t.Error("native gh-cao process did not exit")
		}
	})
	var address string
	select {
	case address = <-output.ready:
	case err := <-done:
		t.Fatalf("native gh-cao exited before serving: %v\n%s", err, output.diagnostics())
	case <-ctx.Done():
		t.Fatalf("native gh-cao never announced readiness: %v\n%s", ctx.Err(), output.diagnostics())
	}
	capability, err := url.Parse(address)
	if err != nil || capability.Scheme != "http" || capability.User != nil ||
		net.ParseIP(capability.Hostname()) == nil || !net.ParseIP(capability.Hostname()).IsLoopback() ||
		capability.Query().Get("access_token") == "" {
		t.Fatal("native gh-cao did not announce a numeric-loopback dashboard capability")
	}
	origin := capability.Scheme + "://" + capability.Host
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, origin+"/", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+capability.Query().Get("access_token"))
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("native gh-cao child did not serve its real dashboard: %v", err)
	}
	shell, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	_ = response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK ||
		!bytes.Contains(shell, []byte(`<meta name="dashboard-data-backend" content="server-http">`)) {
		t.Fatalf("native gh-cao shell bootstrap failed: status=%d error=%v", response.StatusCode, err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("native gh-cao did not exit cleanly at its bounded duration: %v\n%s", err, output.diagnostics())
		}
	case <-ctx.Done():
		t.Fatalf("native gh-cao exceeded its bounded duration: %v\n%s", ctx.Err(), output.diagnostics())
	}
	client.CloseIdleConnections()
	if response, err := client.Get(origin + "/api/readiness"); err == nil {
		_ = response.Body.Close()
		t.Fatal("native gh-cao left its dashboard child listening after exit")
	}
	t.Log("native gh-cao served real server-http assets and reaped its child; GitHub collection deliberately unavailable")
}

func provisionDashboardServingPostgres(t *testing.T, ctx context.Context, binary, admin string) string {
	t.Helper()
	address, err := url.Parse(admin)
	if err != nil || (address.Scheme != "postgres" && address.Scheme != "postgresql") || address.User == nil ||
		net.ParseIP(address.Hostname()) == nil || !net.ParseIP(address.Hostname()).IsLoopback() {
		t.Fatal("CAO_DASHBOARD_TEST_POSTGRES_URL must select an administrator of a disposable numeric-loopback PostgreSQL cluster")
	}
	if address.User.Username() != "cao_bootstrap" || address.Path != "/cao_dashboard" {
		t.Fatal("selected disposable PostgreSQL requires administrator cao_bootstrap and database cao_dashboard")
	}
	address.Scheme = "postgres"
	admin = address.String()
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
		baseGH := dashboardTestGH(t, now)
		earlyAcquisition := make(chan struct{}, 1)
		gh := func(ctx context.Context, args ...string) (string, string, error) {
			if !output.hasAnnouncedReadiness() {
				select {
				case earlyAcquisition <- struct{}{}:
				default:
				}
				return "", "", fmt.Errorf("acquisition raced the native server's initial snapshot ingestion")
			}
			return baseGH(ctx, args...)
		}
		command := dashboardCommand("integration", gh, dashboardTestLogs(t))
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
		select {
		case <-earlyAcquisition:
			t.Fatal("collection began before the native server finished initial ingestion and announced readiness")
		default:
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
	mutex     sync.Mutex
	buffer    bytes.Buffer
	ready     chan string
	announced bool
}

func (output *dashboardServingOutput) Write(value []byte) (int, error) {
	output.mutex.Lock()
	defer output.mutex.Unlock()
	n, err := output.buffer.Write(value)
	if _, rest, found := strings.Cut(output.buffer.String(), "Dashboard: "); found {
		if line, _, complete := strings.Cut(rest, "\n"); complete {
			if fields := strings.Fields(line); len(fields) > 0 {
				output.announced = true
				select {
				case output.ready <- fields[0]:
				default:
				}
			}
		}
	}
	return n, err
}

func (output *dashboardServingOutput) hasAnnouncedReadiness() bool {
	output.mutex.Lock()
	defer output.mutex.Unlock()
	return output.announced
}

func TestDashboardServingOutputCapturesProcessReadiness(t *testing.T) {
	output := &dashboardServingOutput{ready: make(chan string, 1)}
	address := "http://127.0.0.1:12345/?access_token=synthetic-test-capability#page-overview"
	// os/exec copies pipe output with io.Copy; do not promote Buffer.ReadFrom,
	// which would bypass Write's readiness notification and synchronization.
	reader := io.LimitReader(strings.NewReader("Dashboard: "+address), 1024)
	if _, err := io.Copy(output, reader); err != nil {
		t.Fatal(err)
	}
	if output.hasAnnouncedReadiness() {
		t.Fatal("incomplete pipe line marked the server ready for collection")
	}
	select {
	case <-output.ready:
		t.Fatal("incomplete pipe line announced a truncated dashboard URL")
	default:
	}
	if _, err := io.Copy(output, io.LimitReader(strings.NewReader("\n"), 1)); err != nil {
		t.Fatal(err)
	}
	if !output.hasAnnouncedReadiness() {
		t.Fatal("complete readiness line did not allow collection")
	}
	select {
	case actual := <-output.ready:
		if actual != address {
			t.Fatal("pipe copy announced the wrong dashboard URL")
		}
	default:
		t.Fatal("pipe copy bypassed dashboard readiness notification")
	}
	if strings.Contains(output.diagnostics(), "synthetic-test-capability") {
		t.Fatal("diagnostics exposed a dashboard access capability")
	}
}

func (output *dashboardServingOutput) diagnostics() string {
	output.mutex.Lock()
	defer output.mutex.Unlock()
	var redacted []string
	for _, line := range strings.Split(output.buffer.String(), "\n") {
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
