package hostedcli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/operational"
)

func podmanOperation(process Process) string {
	for _, arg := range process.Args {
		if !strings.HasPrefix(arg, "--") {
			return arg
		}
	}
	return ""
}

func clearPodmanConfiguration(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"CONTAINER_HOST", "CONTAINER_CONNECTION", "CONTAINER_SSHKEY",
		"DOCKER_HOST", "DOCKER_CONTEXT", "PODMAN_CONNECTIONS_CONF",
		"CONTAINERS_CONF", "CONTAINERS_CONF_OVERRIDE", "CONTAINERS_CONF_MODULES",
		"CONTAINERS_STORAGE_CONF",
	} {
		t.Setenv(name, "")
	}
}

func podmanTest(t *testing.T) (*ContainerRunner, *containerCommandRunner, Process) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("Podman development adapter requires Linux")
	}
	clearPodmanConfiguration(t)
	root := t.TempDir()
	info, err := json.Marshal(map[string]any{
		"host": map[string]any{
			"serviceIsRemote": false, "security": map[string]any{"rootless": true},
			"cgroupVersion": "v2", "cgroupControllers": []string{"memory", "pids"},
		},
		"store": map[string]any{"graphRoot": root, "runRoot": root, "graphDriverName": "overlay"},
	})
	if err != nil {
		t.Fatal(err)
	}
	commands := &containerCommandRunner{}
	commands.run = func(_ context.Context, p Process, output OutputFunc) (int, error) {
		if p.Program != "podman-test" || !slices.Equal(p.Args[:2], []string{"--remote=false", "--db-backend=sqlite"}) {
			t.Fatalf("not an explicit local Podman invocation: %v", p)
		}
		switch podmanOperation(p) {
		case "info":
			if slices.Contains(p.Args, "--format={{.Host.DatabaseBackend}}") {
				var graphRoot string
				for _, arg := range p.Args {
					if strings.HasPrefix(arg, "--root=") {
						graphRoot = strings.TrimPrefix(arg, "--root=")
					}
				}
				// #nosec G304 G703 -- graphRoot is the test-owned fixture storage directory.
				file, err := os.OpenFile(filepath.Join(graphRoot, "cao-hosted-cli", "libpod", "db.sql"), os.O_CREATE|os.O_RDWR, 0o600)
				if err != nil {
					t.Fatal(err)
				}
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}
				output("stdout", []byte("sqlite"))
			} else {
				output("stdout", info)
			}
		case "create":
			output("stdout", []byte(strings.Repeat("a", 64)))
		case "inspect":
			output("stdout", []byte(`{"Status":"exited","Running":false,"ExitCode":7}`))
		case "logs":
			if output != nil {
				output("stdout", []byte("out"))
				output("stderr", []byte("err"))
			}
		case "start", "rm", "container":
		default:
			t.Fatalf("unexpected Podman call: %v", p.Args)
		}
		return 0, nil
	}
	lifecycle, err := NewPodmanContainerLifecycle("podman-test", commands)
	if err != nil {
		t.Fatal(err)
	}
	runner, err := NewContainerRunner("", lifecycle)
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	return runner, commands, Process{
		CommandID: 1, Workspace: workspace, Dir: workspace, Program: "gh-aw",
		Args: []string{"update", "--repo", "octo/cao"},
		Env:  []string{"GH_TOKEN=scoped-value", "HOME=/workspace/home"},
		RegisterContainer: func(_ context.Context, execution operational.CLICommandExecution) error {
			return execution.Validate()
		},
	}
}

func TestPodmanFullLifecycle(t *testing.T) {
	runner, commands, process := podmanTest(t)
	lifecycle := runner.Lifecycle.(*PodmanContainerLifecycle)
	lifecycle.engine.HostEnv = podmanHostEnvironment([]string{
		"PATH=/bin", "HOME=/operator", "XDG_RUNTIME_DIR=/run/user/1000",
		"GH_TOKEN=host-token", "CAO_PRIVATE_KEY=secret", "DOCKER_CONFIG=/credentials",
		"CONTAINER_HOST=ssh://remote", "CONTAINERS_CONF=/untrusted",
	})
	var execution operational.CLICommandExecution
	process.RegisterContainer = func(_ context.Context, e operational.CLICommandExecution) error {
		execution = e
		if !strings.HasPrefix(e.RuntimeID, "podman:") {
			t.Fatalf("backend identity = %q", e.RuntimeID)
		}
		return e.Validate()
	}
	base := commands.run
	var environmentFile string
	commands.run = func(ctx context.Context, p Process, output OutputFunc) (int, error) {
		expectedEnvironment := 3
		if slices.ContainsFunc(p.Env, func(entry string) bool { return strings.HasPrefix(entry, "CONTAINERS_CONF=") }) {
			expectedEnvironment++
			if !strings.HasPrefix(p.Env[len(p.Env)-1], "CONTAINERS_CONF=") {
				t.Fatal("execution did not select its owned engine configuration")
			}
			// #nosec G304 -- configuration path is created and owned by the adapter.
			config, err := os.ReadFile(strings.TrimPrefix(p.Env[len(p.Env)-1], "CONTAINERS_CONF="))
			if err != nil || !strings.Contains(string(config), "hooks_dir = []") ||
				!strings.Contains(string(config), "mounts = []") || !strings.Contains(string(config), "volumes = []") ||
				!strings.Contains(string(config), "/cao-hosted-cli/libpod") {
				t.Fatal("execution inherited host hooks, mounts or database configuration")
			}
			for _, prefix := range []string{"--root=", "--runroot=", "--storage-driver="} {
				if !slices.ContainsFunc(p.Args, func(arg string) bool { return strings.HasPrefix(arg, prefix) }) {
					t.Fatalf("execution did not pin storage selector %s", prefix)
				}
			}
		}
		if len(p.Env) != expectedEnvironment || strings.Contains(strings.Join(p.Env, "\n"), "secret") {
			t.Fatalf("host environment is not allowlisted: %v", p.Env)
		}
		switch podmanOperation(p) {
		case "create":
			if execution.Name == "" || p.Args[slices.Index(p.Args, "--name")+1] != execution.Name {
				t.Fatal("creation preceded durable registration or changed identity")
			}
			for _, flag := range []string{
				"--pull=never", "--userns=keep-id", "--image-volume=ignore",
				"--http-proxy=false", "--env-host=false", "--cgroups=enabled", "--read-only-tmpfs=false",
				"--default-mounts-file=/dev/null", "--read-only", "--cap-drop",
				"ALL", "--security-opt", "no-new-privileges", "--pids-limit", "256",
				"--memory", "1g", "--tmpfs", "/tmp:rw,nosuid,nodev,noexec,size=64m",
				"--network", "bridge", DefaultContainerImage, "--user",
			} {
				if !slices.Contains(p.Args, flag) {
					t.Fatalf("missing isolation option %s: %v", flag, p.Args)
				}
			}
			if slices.Contains(p.Args, "--rm") || strings.Contains(strings.Join(p.Args, " "), "scoped-value") {
				t.Fatal("auto-removal or token in arguments")
			}
			if count := slices.Index(p.Args, "--mount"); count < 0 ||
				p.Args[count+1] != "type=bind,src="+process.Workspace+",dst=/workspace,rw,relabel=private" ||
				slices.Contains(p.Args[count+1:], "--mount") {
				t.Fatal("unexpected writable host mount")
			}
			if slices.Contains(p.Args, "label=disable") {
				t.Fatal("SELinux separation must not be disabled")
			}
			tail := append([]string{"--entrypoint", "/bin/sh", DefaultContainerImage, "-c", containerBootstrap, "cao-hosted-cli", process.Program}, process.Args...)
			if !slices.Equal(p.Args[len(p.Args)-len(tail):], tail) {
				t.Fatalf("command was not passed positionally: %v", p.Args)
			}
			environmentFile = p.Args[slices.Index(p.Args, "--env-file")+1]
			stat, err := os.Stat(environmentFile)
			if err != nil || stat.Mode().Perm() != 0o600 || filepath.Dir(environmentFile) == process.Workspace {
				t.Fatal("environment file is not private and outside the mounted workspace")
			}
			// #nosec G304 -- path created by writeContainerEnvironment for this invocation.
			data, err := os.ReadFile(environmentFile)
			if err != nil || !strings.Contains(string(data), "GH_TOKEN=scoped-value\n") {
				t.Fatal("scoped environment missing")
			}
		case "start":
			if slices.Contains(p.Args, "--attach") {
				t.Fatal("start must not wait for workload output")
			}
			if _, err := os.Stat(environmentFile); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("environment file was retained after create")
			}
		case "logs":
			if !slices.Equal(p.Args[len(p.Args)-3:], []string{"logs", "--follow", execution.Name}) {
				t.Fatal("output must replay and follow the registered workload")
			}
		}
		return base(ctx, p, output)
	}
	var streams []string
	code, err := runner.Run(t.Context(), process, func(stream string, _ []byte) { streams = append(streams, stream) })
	if err != nil || code != 7 || !slices.Equal(streams, []string{"stdout", "stderr"}) {
		t.Fatalf("workload result = %d, %v, streams=%v", code, err, streams)
	}
	operations := make([]string, 0, len(commands.processes))
	for _, p := range commands.processes {
		operations = append(operations, podmanOperation(p))
	}
	if !slices.Equal(operations, []string{"info", "info", "create", "start", "logs", "info", "info", "inspect", "rm"}) {
		t.Fatalf("lifecycle ordering = %v", operations)
	}
}

func TestPodmanRuntimeIdentityAndPrerequisites(t *testing.T) {
	runner, commands, _ := podmanTest(t)
	lifecycle := runner.Lifecycle.(*PodmanContainerLifecycle)
	first, err := lifecycle.RuntimeID(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	second, err := lifecycle.RuntimeID(t.Context())
	if err != nil || first != second {
		t.Fatalf("identity changed across discovery: %q, %q, %v", first, second, err)
	}
	restarted, err := NewPodmanContainerLifecycle("podman-test", commands)
	if err != nil {
		t.Fatal(err)
	}
	if id, err := restarted.RuntimeID(t.Context()); err != nil || id != first {
		t.Fatalf("identity changed across executor restarts: %q, %v", id, err)
	}
	base := commands.run
	for _, test := range []struct{ name, old, replacement string }{
		{"remote", `"serviceIsRemote":false`, `"serviceIsRemote":true`},
		{"missing remote evidence", `"serviceIsRemote":false`, `"unknown":false`},
		{"rootful", `"rootless":true`, `"rootless":false`},
		{"missing rootless evidence", `"rootless":true`, `"unknown":true`},
		{"cgroup v1", `"v2"`, `"v1"`},
		{"missing memory", `"memory"`, `"cpu"`},
		{"missing pids", `"pids"`, `"cpu"`},
		{"missing root", `"graphRoot"`, `"unknown"`},
		{"unsupported driver", `"overlay"`, `"unknown"`},
	} {
		t.Run(test.name, func(t *testing.T) {
			commands.run = func(ctx context.Context, p Process, out OutputFunc) (int, error) {
				return base(ctx, p, func(stream string, data []byte) {
					out(stream, []byte(strings.ReplaceAll(string(data), test.old, test.replacement)))
				})
			}
			if _, err := lifecycle.RuntimeID(t.Context()); err == nil {
				t.Fatal("unsafe or unavailable local engine accepted")
			}
		})
	}
	commands.run = base
	var root string
	commands.run = func(ctx context.Context, p Process, out OutputFunc) (int, error) {
		return base(ctx, p, func(stream string, data []byte) {
			var info struct{ Store struct{ GraphRoot string } }
			if slices.Contains(p.Args, "--format=json") {
				if err := json.Unmarshal(data, &info); err != nil {
					t.Fatal(err)
				}
				root = info.Store.GraphRoot
			}
			out(stream, data)
		})
	}
	if _, err := lifecycle.RuntimeID(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(root, "cao-hosted-cli", "identity")); err != nil {
		t.Fatal(err)
	}
	third, err := lifecycle.RuntimeID(t.Context())
	if err != nil || third == first {
		t.Fatalf("lost storage identity did not change runtime: %q %v", third, err)
	}
	execution := operational.CLICommandExecution{CommandID: 1, Name: "cao-hosted-cli-" + strings.Repeat("a", 32), RuntimeID: first}
	commands.processes = nil
	if err := runner.Reconcile(t.Context(), execution); !errors.Is(err, ErrContainerTerminationUnconfirmed) {
		t.Fatalf("old namespace recovery = %v", err)
	}
	for _, p := range commands.processes {
		if podmanOperation(p) != "info" {
			t.Fatal("recovery touched workload after runtime identity changed")
		}
	}
}

func TestPodmanRejectsRemoteConfiguration(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux-only adapter")
	}
	clearPodmanConfiguration(t)
	for _, name := range []string{
		"CONTAINER_HOST", "CONTAINER_CONNECTION", "CONTAINER_SSHKEY", "DOCKER_HOST",
		"DOCKER_CONTEXT", "PODMAN_CONNECTIONS_CONF", "CONTAINERS_CONF",
		"CONTAINERS_CONF_OVERRIDE", "CONTAINERS_CONF_MODULES", "CONTAINERS_STORAGE_CONF",
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(name, "untrusted")
			if _, err := NewPodmanContainerLifecycle("", nil); err == nil {
				t.Fatal("remote/custom configuration accepted")
			}
		})
	}
}

func TestPodmanLifecycleFailuresAndCancellation(t *testing.T) {
	for _, operation := range []string{"info", "create", "start", "logs", "inspect", "rm"} {
		for _, canceled := range []bool{false, true} {
			t.Run(operation+strconvBool(canceled), func(t *testing.T) {
				runner, commands, process := podmanTest(t)
				base := commands.run
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				var cleanupDetached bool
				commands.run = func(callCtx context.Context, p Process, out OutputFunc) (int, error) {
					if podmanOperation(p) == operation {
						if canceled {
							cancel()
							return -1, context.Canceled
						}
						return 1, errors.New("engine unavailable")
					}
					if podmanOperation(p) == "container" {
						// Failed removal plus failed inventory is never absence.
						return 1, errors.New("engine unavailable")
					}
					if podmanOperation(p) == "rm" && ctx.Err() != nil {
						cleanupDetached = callCtx.Err() == nil
						if _, ok := callCtx.Deadline(); !ok {
							t.Fatal("cleanup context is not bounded")
						}
					}
					return base(callCtx, p, out)
				}
				code, err := runner.Run(ctx, process, nil)
				if err == nil || ((operation == "info" || operation == "create") && code >= 0) {
					t.Fatalf("failed %s yielded %d, %v", operation, code, err)
				}
				if operation == "create" && !canceled && !strings.Contains(err.Error(), "private workspace SELinux relabel permissions") {
					t.Fatal("creation failure omitted private-label prerequisite guidance")
				}
				if canceled && (operation == "create" || operation == "start" || operation == "logs") && !cleanupDetached {
					t.Fatal("cancellation prevented detached cleanup")
				}
			})
		}
	}
}

func strconvBool(value bool) string {
	if value {
		return "/canceled"
	}
	return "/failed"
}

func TestPodmanStopRequiresExactAbsence(t *testing.T) {
	for _, inventory := range []string{"", strings.Repeat("b", 64)} {
		runner, commands, _ := podmanTest(t)
		id, err := runner.Lifecycle.RuntimeID(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		execution := operational.CLICommandExecution{Name: "cao-hosted-cli-" + strings.Repeat("a", 32), RuntimeID: id}
		commands.run = func(_ context.Context, p Process, out OutputFunc) (int, error) {
			switch podmanOperation(p) {
			case "rm":
				return 1, errors.New("not removed")
			case "container":
				if p.Args[len(p.Args)-1] != "name=^"+execution.Name+"$" {
					t.Fatal("inventory is not exact-name scoped")
				}
				out("stdout", []byte(inventory))
				return 0, nil
			}
			t.Fatalf("unexpected command: %v", p.Args)
			return -1, nil
		}
		err = runner.Lifecycle.Stop(t.Context(), execution)
		if (err == nil) != (inventory == "") {
			t.Fatalf("inventory %q => %v", inventory, err)
		}
	}
}

func TestPodmanInspectRequiresConfirmedWorkloadResult(t *testing.T) {
	for _, value := range []string{
		`{}`, `not json`, `{"Status":"running","Running":true,"ExitCode":0}`,
		`{"Status":"exited","Running":true,"ExitCode":0}`,
		`{"Status":"exited","Running":false}`, `{"Status":"exited","Running":false,"ExitCode":256}`,
		`{"Status":"exited","Running":false,"ExitCode":-1}`,
	} {
		runner, commands, _ := podmanTest(t)
		id, err := runner.Lifecycle.RuntimeID(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		commands.run = func(_ context.Context, _ Process, out OutputFunc) (int, error) {
			out("stdout", []byte(value))
			return 0, nil
		}
		state, _ := runner.Lifecycle.Inspect(t.Context(), operational.CLICommandExecution{RuntimeID: id})
		if state.Terminated || state.ExitCode != nil {
			t.Fatalf("invalid result became termination: %s", value)
		}
	}
}

func TestPodmanStorageNamespaceChangesBlockRecovery(t *testing.T) {
	for _, field := range []string{"graphRoot", "runRoot"} {
		t.Run(field, func(t *testing.T) {
			runner, commands, _ := podmanTest(t)
			id, err := runner.Lifecycle.RuntimeID(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			otherRoot := t.TempDir()
			base := commands.run
			commands.run = func(ctx context.Context, p Process, out OutputFunc) (int, error) {
				if podmanOperation(p) != "info" {
					t.Fatal("foreign runtime recovery touched a workload")
				}
				return base(ctx, p, func(stream string, data []byte) {
					if !slices.Contains(p.Args, "--format=json") {
						out(stream, data)
						return
					}
					var value map[string]any
					if err := json.Unmarshal(data, &value); err != nil {
						t.Fatal(err)
					}
					value["store"].(map[string]any)[field] = otherRoot
					encoded, err := json.Marshal(value)
					if err != nil {
						t.Fatal(err)
					}
					out(stream, encoded)
				})
			}
			execution := operational.CLICommandExecution{
				CommandID: 1, Name: "cao-hosted-cli-" + strings.Repeat("a", 32), RuntimeID: id,
			}
			if err := runner.Reconcile(t.Context(), execution); !errors.Is(err, ErrContainerTerminationUnconfirmed) {
				t.Fatalf("changed %s did not block recovery: %v", field, err)
			}
		})
	}
}

func TestPodmanRejectsMalformedAndUnboundedDiscovery(t *testing.T) {
	for _, value := range []string{"{}", "malformed", strings.Repeat("a", 32769)} {
		runner, commands, _ := podmanTest(t)
		commands.run = func(_ context.Context, _ Process, out OutputFunc) (int, error) {
			out("stdout", []byte(value))
			return 0, nil
		}
		if _, err := runner.Lifecycle.RuntimeID(t.Context()); err == nil {
			t.Fatal("invalid or unbounded engine discovery accepted")
		}
	}
}

func TestPodmanReplacedNamespaceBlocksRecovery(t *testing.T) {
	runner, commands, _ := podmanTest(t)
	id, err := runner.Lifecycle.RuntimeID(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	lifecycle := runner.Lifecycle.(*PodmanContainerLifecycle)
	var graphRoot string
	for _, arg := range lifecycle.currentEngine.commandPrefix {
		if strings.HasPrefix(arg, "--root=") {
			graphRoot = strings.TrimPrefix(arg, "--root=")
		}
	}
	namespace := filepath.Join(graphRoot, "cao-hosted-cli", "libpod")
	oldNamespace := namespace + "-replaced"
	if err := os.Rename(namespace, oldNamespace); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(namespace, "cao-hosted-cli"), 0o700); err != nil {
		t.Fatal(err)
	}
	// Even copying the durable marker must not make a replacement database
	// directory masquerade as the original execution namespace.
	// #nosec G304 -- the path is the adapter's test-owned identity marker.
	marker, err := os.ReadFile(filepath.Join(oldNamespace, "cao-hosted-cli", "identity"))
	if err != nil {
		t.Fatal(err)
	}
	// #nosec G703 -- namespace derives only from podmanTest's private test directory.
	if err := os.WriteFile(filepath.Join(namespace, "cao-hosted-cli", "identity"), marker, 0o600); err != nil {
		t.Fatal(err)
	}
	newID, err := runner.Lifecycle.RuntimeID(t.Context())
	if err != nil || newID == id {
		t.Fatalf("replaced namespace retained original runtime identity: %q, %v", newID, err)
	}
	commands.processes = nil
	execution := operational.CLICommandExecution{
		CommandID: 1, Name: "cao-hosted-cli-" + strings.Repeat("a", 32), RuntimeID: id,
	}
	if err := runner.Reconcile(t.Context(), execution); !errors.Is(err, ErrContainerTerminationUnconfirmed) {
		t.Fatalf("replaced execution store did not retain the old claim: %v", err)
	}
	for _, p := range commands.processes {
		if podmanOperation(p) != "info" {
			t.Fatal("replacement namespace was used as absence proof for the old workload")
		}
	}
}

func TestPodmanReplacedDatabaseBlocksRecovery(t *testing.T) {
	runner, commands, _ := podmanTest(t)
	id, err := runner.Lifecycle.RuntimeID(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	lifecycle := runner.Lifecycle.(*PodmanContainerLifecycle)
	configFile := strings.TrimPrefix(lifecycle.currentEngine.HostEnv[len(lifecycle.currentEngine.HostEnv)-1], "CONTAINERS_CONF=")
	database := filepath.Join(filepath.Dir(configFile), "libpod", "db.sql")
	// #nosec G703 -- this is only the adapter's test-owned fixture database.
	if err := os.Rename(database, database+"-replaced"); err != nil {
		t.Fatal(err)
	}
	newID, err := lifecycle.RuntimeID(t.Context())
	if err != nil || newID == id {
		t.Fatalf("replaced database retained original runtime identity: %q, %v", newID, err)
	}
	commands.processes = nil
	execution := operational.CLICommandExecution{
		CommandID: 1, Name: "cao-hosted-cli-" + strings.Repeat("a", 32), RuntimeID: id,
	}
	if err := runner.Reconcile(t.Context(), execution); !errors.Is(err, ErrContainerTerminationUnconfirmed) {
		t.Fatalf("replacement database did not retain the old claim: %v", err)
	}
	for _, p := range commands.processes {
		if podmanOperation(p) != "info" {
			t.Fatal("replacement database was used as absence proof for the old workload")
		}
	}
}

func TestPodmanRejectsForeignExecutionNamespace(t *testing.T) {
	runner, commands, _ := podmanTest(t)
	if _, err := runner.Lifecycle.RuntimeID(t.Context()); err != nil {
		t.Fatal(err)
	}
	commands.processes = nil
	execution := operational.CLICommandExecution{
		CommandID: 1, Name: "cao-hosted-cli-" + strings.Repeat("a", 32), RuntimeID: "podman:foreign",
	}
	spec := ContainerSpec{Image: DefaultContainerImage}
	if err := runner.Lifecycle.Create(t.Context(), execution, spec); err == nil {
		t.Fatal("foreign runtime creation accepted")
	}
	if err := runner.Lifecycle.Start(t.Context(), execution); err == nil {
		t.Fatal("foreign runtime start accepted")
	}
	if _, err := runner.Lifecycle.Inspect(t.Context(), execution); err == nil {
		t.Fatal("foreign runtime inspection accepted")
	}
	if err := runner.Lifecycle.Stop(t.Context(), execution); err == nil {
		t.Fatal("foreign runtime removal accepted")
	}
	if err := runner.Lifecycle.CollectOutput(t.Context(), execution, nil); err == nil {
		t.Fatal("foreign runtime output accepted")
	}
	if len(commands.processes) != 0 {
		t.Fatal("foreign runtime request reached the engine")
	}
}

func TestPodmanRequiresQualifiedPinnedImage(t *testing.T) {
	runner, _, _ := podmanTest(t)
	for _, image := range []string{
		"gh-aw:latest", "gh-aw@sha256:" + strings.Repeat("a", 64),
		"library/gh-aw@sha256:" + strings.Repeat("a", 64),
	} {
		if _, err := NewContainerRunner(image, runner.Lifecycle); err == nil {
			t.Fatalf("ambiguous or unpinned Podman image accepted: %s", image)
		}
		if err := runner.Lifecycle.Create(t.Context(), operational.CLICommandExecution{}, ContainerSpec{Image: image}); err == nil {
			t.Fatal("direct adapter creation bypassed image validation")
		}
	}
}
