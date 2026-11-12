package cli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestDashboardPostgresExplicitConnectionDoesNotManageContainers(t *testing.T) {
	connection := "postgres://explicit.example/database"
	got, err := configureDashboardPostgres(t.Context(), "must-not-run", t.TempDir(), "scope", connection, "must-not-run")
	if err != nil || got != connection {
		t.Fatalf("explicit connection was not preserved: %q %v", got, err)
	}
}

func TestDashboardRejectsAnExplicitEmptyConnectionBeforeCollection(t *testing.T) {
	command := dashboardCommand("test", func(context.Context, ...string) (string, string, error) {
		t.Fatal("invalid explicit connection reached GitHub acquisition")
		return "", "", nil
	}, nil)
	command.SetArgs([]string{"--postgres-url", ""})
	if err := command.ExecuteContext(t.Context()); err == nil || !strings.Contains(err.Error(), "--postgres-url must name") {
		t.Fatalf("explicit empty connection was accepted: %v", err)
	}
}

func TestDashboardEngineRejectsRemoteConnectionsBeforeWork(t *testing.T) {
	for _, provider := range []string{"docker", "podman"} {
		t.Run(provider, func(t *testing.T) {
			for _, name := range []string{"DOCKER_HOST", "DOCKER_CONTEXT", "CONTAINER_HOST", "CONTAINER_CONNECTION"} {
				t.Setenv(name, "")
			}
			engine := dashboardEngine{name: provider, run: func(_ context.Context, args ...string) ([]byte, error) {
				if args[0] == "context" {
					return []byte(`"ssh://remote.example"`), nil
				}
				if args[0] == "system" {
					return []byte(`[{"Name":"remote","Default":true,"URI":"ssh://remote.example"}]`), nil
				}
				t.Fatal("remote engine reached runtime or resource operations")
				return nil, nil
			}}
			if err := engine.verifyLocal(t.Context()); err == nil {
				t.Fatal("remote container endpoint was accepted")
			}
		})
	}
}

func TestDashboardEngineSelection(t *testing.T) {
	for _, test := range []struct {
		flag    string
		changed bool
		env     *string
		want    string
		err     string
	}{
		{want: ""},
		{env: ptr("docker"), want: "docker"},
		{env: ptr("podman"), want: "podman"},
		{flag: "podman", changed: true, env: ptr("docker"), want: "podman"},
		{flag: "docker", changed: true, env: ptr("unsupported"), want: "docker"},
		{flag: "auto", changed: true, err: "--container-engine must be docker or podman"},
		{flag: "", changed: true, err: "--container-engine must be docker or podman"},
		{env: ptr(""), err: "CAO_CONTAINER_ENGINE must be docker or podman"},
		{env: ptr("unsupported"), err: "CAO_CONTAINER_ENGINE must be docker or podman"},
	} {
		t.Setenv("CAO_CONTAINER_ENGINE", "")
		if test.env == nil {
			_ = os.Unsetenv("CAO_CONTAINER_ENGINE")
		} else {
			t.Setenv("CAO_CONTAINER_ENGINE", *test.env)
		}
		got, err := resolveDashboardContainerEngine(test.flag, test.changed)
		if test.err != "" {
			if err == nil || !strings.Contains(err.Error(), test.err) {
				t.Fatalf("%+v: invalid engine was accepted: %q %v", test, got, err)
			}
		} else if err != nil || got != test.want {
			t.Fatalf("%+v: got %q %v, want %q", test, got, err, test.want)
		}
	}
}

func TestDashboardRejectsInvalidContainerEngineBeforeCollection(t *testing.T) {
	command := dashboardCommand("test", func(context.Context, ...string) (string, string, error) {
		t.Fatal("invalid container engine reached GitHub acquisition")
		return "", "", nil
	}, nil)
	command.SetArgs([]string{"--container-engine", "auto"})
	if err := command.ExecuteContext(t.Context()); err == nil || !strings.Contains(err.Error(), "--container-engine must be docker or podman") {
		t.Fatalf("invalid container engine was accepted: %v", err)
	}
}

func ptr(value string) *string { return &value }

func TestDashboardEngineAutomaticSelection(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("container engine fixtures require a POSIX shell")
	}
	for _, test := range []struct {
		name      string
		selected  string
		docker    string
		podman    bool
		want      string
		wantError string
	}{
		{name: "prefers docker", docker: "usable", podman: true, want: "docker"},
		{name: "docker missing", podman: true, want: "podman"},
		{name: "docker unusable", docker: "unusable", podman: true, want: "podman"},
		{name: "neither installed", wantError: "no usable local container engine"},
		{name: "explicit docker missing", selected: "docker", podman: true, wantError: "docker is not usable"},
		{name: "explicit docker unusable", selected: "docker", docker: "unusable", podman: true, wantError: "docker is not usable"},
		{name: "explicit podman", selected: "podman", docker: "usable", podman: true, want: "podman"},
		{name: "explicit podman missing", selected: "podman", docker: "usable", wantError: "podman is not usable"},
	} {
		t.Run(test.name, func(t *testing.T) {
			directory := t.TempDir()
			t.Setenv("PATH", directory)
			for _, name := range []string{"DOCKER_HOST", "DOCKER_CONTEXT", "CONTAINER_HOST", "CONTAINER_CONNECTION"} {
				t.Setenv(name, "")
			}
			calls := filepath.Join(directory, "calls")
			t.Setenv("CAO_TEST_ENGINE_CALLS", calls)
			for _, name := range []string{"docker", "podman"} {
				if (name == "docker" && test.docker == "") || (name == "podman" && !test.podman) {
					continue
				}
				script := "#!/bin/sh\nprintf '%s\\n' \"" + name + "\" >> \"$CAO_TEST_ENGINE_CALLS\"\n"
				if name == "docker" && test.docker == "unusable" {
					script += "exit 1\n"
				} else {
					script += "case \"$1\" in\ncontext) printf '\"unix:///var/run/docker.sock\"';;\nsystem) printf '[]';;\ninfo) printf 'linux';;\n*) exit 1;;\nesac\n"
				}
				if err := os.WriteFile(filepath.Join(directory, name), []byte(script), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			engine, err := selectDashboardEngine(t.Context(), test.selected)
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("got %v, want %q", err, test.wantError)
				}
			} else if err != nil || engine.name != test.want {
				t.Fatalf("got %q %v, want %q", engine.name, err, test.want)
			}
			content, err := os.ReadFile(calls)
			if err != nil && !errors.Is(err, os.ErrNotExist) {
				t.Fatal(err)
			}
			for _, called := range strings.Fields(string(content)) {
				if (test.selected != "" && called != test.selected) || (test.want == "docker" && called != "docker") {
					t.Fatalf("unexpected engine %q was probed", called)
				}
			}
		})
	}
}

func TestDashboardPostgresOwnedContainerLifecycle(t *testing.T) {
	for _, provider := range []string{"docker", "podman"} {
		t.Run(provider, func(t *testing.T) {
			directory := t.TempDir()
			id := strings.Repeat("a", 64)
			var calls []string
			running := true
			foreign := false
			engine := dashboardEngine{name: provider}
			engine.run = func(_ context.Context, args ...string) ([]byte, error) {
				calls = append(calls, strings.Join(args, " "))
				switch args[0] {
				case "image":
					return nil, errors.New("image is not cached")
				case "pull", "run", "exec":
					if slices.Contains(args, "POSTGRES_PASSWORD=") {
						t.Fatal("administrator credential reached command arguments")
					}
					return nil, nil
				case "start":
					running = true
					return nil, nil
				case "container":
					if args[1] == "ls" {
						return nil, nil
					}
					state, err := readDashboardPostgresState(filepath.Join(directory, "state.json"))
					if err != nil {
						return nil, err
					}
					container := dashboardPostgresContainer{ID: id}
					container.Config.Image = dashboardPostgresImage
					container.Config.Labels = map[string]string{dashboardPostgresLabel: state.Owner}
					if foreign {
						container.Config.Labels[dashboardPostgresLabel] = "foreign"
					}
					container.State.Running = running
					container.Mounts = append(container.Mounts, struct{ Type, Source, Destination string }{
						"bind", filepath.Join(directory, "data"), "/var/lib/postgresql/data",
					})
					container.NetworkSettings.Ports = map[string][]struct {
						HostIP   string `json:"HostIp"`
						HostPort string
					}{"5432/tcp": {{"127.0.0.1", "55439"}}}
					return json.Marshal([]dashboardPostgresContainer{container})
				}
				return nil, errors.New("unexpected engine operation")
			}
			admin, password, err := startDashboardPostgres(t.Context(), engine, directory)
			if err != nil || !strings.Contains(admin, "127.0.0.1:55439") || len(password) != 64 {
				t.Fatalf("managed container failed: %v", err)
			}
			initial := slices.Clone(calls)
			if !slices.ContainsFunc(initial, func(call string) bool { return strings.HasPrefix(call, "pull ") }) ||
				!slices.ContainsFunc(initial, func(call string) bool {
					return strings.Contains(call, "--volume "+filepath.Join(directory, "data")+":/var/lib/postgresql/data:Z")
				}) {
				t.Fatal("container did not pull PostgreSQL and bind-mount its cache")
			}
			running, calls = false, nil
			reused, samePassword, err := startDashboardPostgres(t.Context(), engine, directory)
			if err != nil || reused != admin || password != samePassword ||
				!slices.Contains(calls, "start "+id) ||
				slices.ContainsFunc(calls, func(call string) bool { return strings.HasPrefix(call, "run ") }) {
				t.Fatalf("owned PostgreSQL was not reused: %v %v", calls, err)
			}
			foreign, calls = true, nil
			if _, _, err := startDashboardPostgres(t.Context(), engine, directory); err == nil ||
				slices.ContainsFunc(calls, func(call string) bool { return strings.HasPrefix(call, "start ") }) {
				t.Fatal("foreign container ownership did not fail before start")
			}
		})
	}
}

func TestDashboardCacheIgnorePreservesExistingRules(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := exec.CommandContext(t.Context(), "git", "init", "--quiet").Run(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(".gitignore", []byte("existing-rule\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	cache := filepath.Join(root, ".github", "cao", "dashboard", "scope")
	for range 2 {
		if err := ensureDashboardCacheIgnored(cache); err != nil {
			t.Fatal(err)
		}
	}
	content, err := os.ReadFile(".gitignore")
	if err != nil || string(content) != "existing-rule\n\n/.github/cao/dashboard/\n" {
		t.Fatalf("cache ignore damaged or duplicated existing rules: %q %v", content, err)
	}
	if err := exec.CommandContext(t.Context(), "git", "check-ignore", "--quiet",
		filepath.Join(".github", "cao", "dashboard", "scope", "postgres", "state.json")).Run(); err != nil {
		t.Fatal("private managed database state is not ignored by Git")
	}
}

func TestDashboardManagedPostgresContainer(t *testing.T) {
	if os.Getenv("CAO_TEST_MANAGED_POSTGRES") != "1" {
		t.Skip("set CAO_TEST_MANAGED_POSTGRES=1 to exercise a real local container engine")
	}
	server, err := filepath.Abs("../../.tmp/cao-dashboard")
	if err != nil {
		t.Fatal(err)
	}
	cache, err := os.MkdirTemp(".", ".native-managed-postgres-")
	if err != nil {
		t.Fatal(err)
	}
	cache, err = filepath.Abs(cache)
	if err != nil {
		t.Fatal(err)
	}
	engineName, err := resolveDashboardContainerEngine("", false)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := selectDashboardEngine(t.Context(), engineName)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		if state, err := readDashboardPostgresState(filepath.Join(cache, "postgres", "state.json")); err == nil && state.ContainerID != "" {
			if _, err := engine.run(ctx, "rm", "--force", state.ContainerID); err != nil {
				t.Error(err)
				return
			}
			if _, err := engine.run(ctx, "run", "--rm", "--network", "none",
				"--volume", filepath.Join(cache, "postgres", "data")+":/data:Z",
				"--entrypoint", "sh", dashboardPostgresImage, "-c", "chmod -R ugo+rwX /data"); err != nil {
				t.Error(err)
				return
			}
		}
		if err := os.RemoveAll(cache); err != nil {
			t.Error(err)
		}
	})
	connection, err := configureDashboardPostgres(t.Context(), server, cache, "managed-test", "", engineName)
	if err != nil {
		t.Fatal(err)
	}
	reused, err := configureDashboardPostgres(t.Context(), server, cache, "managed-test", "", engineName)
	if err != nil || reused != connection {
		t.Fatalf("real managed PostgreSQL did not reuse its runtime connection: %v", err)
	}
	if err := publishDashboardSnapshot(t.Context(), cache, map[string][]map[string]any{
		"repositories": {{"id": "repository:test", "owner": "acme", "name": "test"}},
	}); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), server, "ingest", "--source", cache, "--redis-namespace", "managed-test",
		"--database-queries", "../../dashboard/site/src/data/queries/database.json")
	command.Env = append(dashboardServerEnvironment(), "CAO_POSTGRES_URL="+connection)
	if content, err := command.CombinedOutput(); err != nil {
		t.Fatalf("runtime role cannot ingest canonical data: %v\n%s", err, content)
	}
	command = exec.CommandContext(t.Context(), server, "database", "bootstrap", "--store", "canonical", "--namespace", "forbidden")
	command.Env = append(dashboardServerEnvironment(), "CAO_DATABASE_ADMIN_URL="+connection)
	if err := command.Run(); err == nil {
		t.Fatal("runtime role unexpectedly gained bootstrap authority")
	}
}
