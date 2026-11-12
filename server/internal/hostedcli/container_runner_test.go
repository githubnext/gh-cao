package hostedcli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/operational"
)

type containerCommandRunner struct {
	processes []Process
	run       func(context.Context, Process, OutputFunc) (int, error)
}

func (r *containerCommandRunner) Run(ctx context.Context, process Process, output OutputFunc) (int, error) {
	r.processes = append(r.processes, process)
	return r.run(ctx, process, output)
}

// testOCILifecycle supplies the runtime identity that a real adapter (Podman)
// discovers, so the shared OCI mechanics are testable on every platform.
type testOCILifecycle struct{ *ociContainerLifecycle }

var _ ContainerLifecycle = testOCILifecycle{}

func (l testOCILifecycle) RuntimeID(ctx context.Context) (string, error) {
	value, code, err := l.capture(ctx, []string{"info", "--format={{.ID}}"})
	id := strings.TrimSpace(value)
	if err != nil || code != 0 || id == "" {
		return "", errors.New("test runtime identity is unavailable")
	}
	return id, nil
}

func newTestOCILifecycle(commands Runner) testOCILifecycle {
	return testOCILifecycle{&ociContainerLifecycle{
		binary: "podman-test", commands: commands, relays: &egressRelays{},
		commandPrefix: []string{"--remote=false", "--db-backend=sqlite"},
	}}
}

func containerTest(t *testing.T) (*ContainerRunner, *containerCommandRunner, Process) {
	t.Helper()
	workspace := t.TempDir()
	work := filepath.Join(workspace, "work")
	if err := os.Mkdir(work, 0o700); err != nil {
		t.Fatal(err)
	}
	commands := &containerCommandRunner{}
	commands.run = func(_ context.Context, process Process, output OutputFunc) (int, error) {
		switch process.Args[2] {
		case "info":
			output("stdout", []byte("original-daemon\n"))
		case "create":
			output("stdout", []byte(strings.Repeat("a", 64)+"\n"))
		case "inspect":
			output("stdout", []byte(`{"Status":"exited","Running":false,"Restarting":false,"ExitCode":0}`))
		case "start", "logs", "rm":
		case "container":
			output("stdout", []byte(strings.Repeat("a", 64)+"\n"))
		default:
			t.Fatalf("unexpected container engine call %v", process.Args)
		}
		return 0, nil
	}
	runner, err := NewContainerRunner("", newTestOCILifecycle(commands))
	if err != nil {
		t.Fatal(err)
	}
	process := Process{
		CommandID: 1, Workspace: workspace, Dir: work, Program: "gh-aw",
		Args: []string{"update", "https://github.com/githubnext/gh-cao/security", "--repo", "octo/cao", "--create-pull-request"},
		Env:  []string{"GH_TOKEN=scoped-value", "HOME=/workspace/home"},
		RegisterContainer: func(_ context.Context, execution operational.CLICommandExecution) error {
			return execution.Validate()
		},
	}
	return runner, commands, process
}

func TestContainerRunnerUsesPinnedImageAndPrivateEnvironmentFile(t *testing.T) {
	runner, commands, process := containerTest(t)
	runner.Lifecycle.(testOCILifecycle).hostEnv = podmanHostEnvironment([]string{"PATH=/usr/bin", "HOME=/operator", "CAO_PRIVATE_KEY=excluded"})
	var environmentFile string
	registered := false
	process.RegisterContainer = func(_ context.Context, execution operational.CLICommandExecution) error {
		registered = true
		if execution.RuntimeID != "original-daemon" {
			t.Fatalf("runtime identity = %q", execution.RuntimeID)
		}
		return execution.Validate()
	}
	base := commands.run
	commands.run = func(ctx context.Context, p Process, output OutputFunc) (int, error) {
		switch p.Args[2] {
		case "start":
			if slices.Contains(p.Args, "--attach") {
				t.Fatal("start must not couple workload lifetime to output transport")
			}
			if _, err := os.Stat(environmentFile); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("environment file remained after creation: %v", err)
			}
		case "logs":
			if !slices.Equal(p.Args[:4], []string{"--remote=false", "--db-backend=sqlite", "logs", "--follow"}) {
				t.Fatalf("output must replay from startup and follow: %v", p.Args)
			}
		case "create":
			if !registered {
				t.Fatal("create must follow durable registration")
			}
			if slices.Contains(p.Env, "CAO_PRIVATE_KEY=excluded") || strings.Contains(strings.Join(p.Args, " "), "scoped-value") {
				t.Fatal("server credentials or scoped token leaked to container engine arguments/environment")
			}
			environmentFile = p.Args[slices.Index(p.Args, "--env-file")+1]
			info, err := os.Stat(environmentFile)
			if err != nil || info.Mode().Perm() != 0o600 {
				t.Fatalf("environment file is not private: %v", err)
			}
			// #nosec G304 -- path created by writeContainerEnvironment in this test.
			contents, err := os.ReadFile(environmentFile)
			if err != nil || !strings.Contains(string(contents), "GH_TOKEN=scoped-value\n") {
				t.Fatal("missing scoped token in private environment file")
			}
			for _, arg := range []string{"--remote=false", "--read-only", "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--network", "bridge"} {
				if !slices.Contains(p.Args, arg) {
					t.Fatalf("missing isolation flag %q", arg)
				}
			}
			if slices.Contains(p.Args, "--rm") {
				t.Fatal("auto-removal would lose workload exit evidence")
			}
			resolvedWorkspace, err := filepath.EvalSymlinks(process.Workspace)
			if err != nil {
				t.Fatal(err)
			}
			if p.Args[slices.Index(p.Args, "--mount")+1] != "type=bind,src="+resolvedWorkspace+",dst=/workspace,rw" {
				t.Fatal("unexpected host mount")
			}
			expected := append([]string{"--entrypoint", "/bin/sh", DefaultContainerImage, "-c", containerBootstrap, "cao-hosted-cli", process.Program}, process.Args...)
			if !slices.Equal(p.Args[len(p.Args)-len(expected):], expected) {
				t.Fatalf("unexpected fixed bootstrap %v", p.Args)
			}
		}
		return base(ctx, p, output)
	}
	if code, err := runner.Run(t.Context(), process, nil); err != nil || code != 0 {
		t.Fatalf("result = %d %v", code, err)
	}
	if _, err := os.Stat(environmentFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("environment file remained: %v", err)
	}
}

func TestContainerRunnerConfirmedRemovalDoesNotInventWorkloadResult(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "lost-result", true: "canceled"}[canceled], func(t *testing.T) {
			runner, commands, process := containerTest(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			base := commands.run
			commands.run = func(callCtx context.Context, p Process, output OutputFunc) (int, error) {
				if p.Args[2] == "logs" && canceled {
					cancel()
					return -1, context.Canceled
				}
				if p.Args[2] == "inspect" {
					output("stdout", []byte(`{"Status":"running","Running":true,"Restarting":false,"ExitCode":0}`))
					return 0, nil
				}
				return base(callCtx, p, output)
			}
			code, err := runner.Run(ctx, process, nil)
			if code != -1 || err == nil || errors.Is(err, ErrContainerTerminationUnconfirmed) {
				t.Fatalf("confirmed removal result = %d %v", code, err)
			}
			if canceled && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation was lost: %v", err)
			}
		})
	}
}

func TestContainerRunnerCreateFailureDoesNotInventWorkloadResult(t *testing.T) {
	for _, tc := range []struct {
		name string
		code int
		err  error
	}{
		{name: "engine-exit", code: 125},
		{name: "missing-container-id", code: 0},
		{name: "lost-response", code: 1, err: errors.New("create response lost")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			runner, commands, process := containerTest(t)
			base := commands.run
			commands.run = func(ctx context.Context, p Process, output OutputFunc) (int, error) {
				if p.Args[2] == "create" {
					return tc.code, tc.err
				}
				return base(ctx, p, output)
			}
			code, err := runner.Run(t.Context(), process, nil)
			if code != -1 || err == nil || errors.Is(err, ErrContainerTerminationUnconfirmed) {
				t.Fatalf("create failure with confirmed cleanup must have no workload exit code: %d %v", code, err)
			}
			for _, p := range commands.processes {
				if p.Args[2] == "start" {
					t.Fatal("failed creation must not start a workload")
				}
			}
		})
	}
}

func TestContainerRunnerRequiresDurableRegistration(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(map[bool]string{false: "store-failure", true: "missing-callback"}[missing], func(t *testing.T) {
			runner, commands, process := containerTest(t)
			process.RegisterContainer = nil
			if !missing {
				process.RegisterContainer = func(context.Context, operational.CLICommandExecution) error { return operational.ErrConflict }
			}
			if _, err := runner.Run(t.Context(), process, nil); err == nil {
				t.Fatal("unregistered container accepted")
			}
			for _, p := range commands.processes {
				if p.Args[2] == "create" {
					t.Fatal("container created without durable identity")
				}
			}
		})
	}
}

func TestContainerRunnerRequiresConfirmedTermination(t *testing.T) {
	for _, scenario := range []string{"create", "start", "output-error", "output-zero", "output-nonzero", "cancel", "timeout", "inspect-failure"} {
		t.Run(scenario, func(t *testing.T) {
			runner, commands, process := containerTest(t)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if scenario == "timeout" {
				var timeoutCancel context.CancelFunc
				ctx, timeoutCancel = context.WithTimeout(t.Context(), 20*time.Millisecond)
				defer timeoutCancel()
			}
			base := commands.run
			commands.run = func(callCtx context.Context, p Process, output OutputFunc) (int, error) {
				switch p.Args[2] {
				case "create":
					if scenario == "create" {
						return -1, errors.New("lost create response")
					}
				case "start":
					if scenario == "start" {
						return -1, errors.New("lost start response")
					}
				case "logs":
					switch scenario {
					case "cancel":
						cancel()
						return -1, context.Canceled
					case "timeout":
						<-ctx.Done()
						return -1, ctx.Err()
					case "output-error":
						return -1, errors.New("lost output transport")
					case "output-nonzero":
						return 125, nil
					}
				case "inspect":
					if scenario == "inspect-failure" {
						return 1, errors.New("daemon unavailable")
					}
					output("stdout", []byte(`{"Status":"running","Running":true,"Restarting":false,"ExitCode":0}`))
					return 0, nil
				case "rm":
					if callCtx.Err() != nil {
						t.Fatal("cleanup inherited cancellation")
					}
					deadline, ok := callCtx.Deadline()
					if !ok || time.Until(deadline) > 10*time.Second {
						t.Fatal("cleanup is not bounded")
					}
					return 1, errors.New("cleanup unavailable")
				}
				return base(callCtx, p, output)
			}
			code, err := runner.Run(ctx, process, nil)
			if code != -1 || !errors.Is(err, ErrContainerTerminationUnconfirmed) {
				t.Fatalf("uncertain termination became a settled result: %d %v", code, err)
			}
		})
	}
}

func TestContainerRunnerUsesWorkloadExitNotEngineExit(t *testing.T) {
	runner, commands, process := containerTest(t)
	base := commands.run
	commands.run = func(ctx context.Context, p Process, output OutputFunc) (int, error) {
		if p.Args[2] == "inspect" {
			output("stdout", []byte(`{"Status":"exited","Running":false,"Restarting":false,"ExitCode":17}`))
			return 0, nil
		}
		return base(ctx, p, output)
	}
	if code, err := runner.Run(t.Context(), process, nil); err != nil || code != 17 {
		t.Fatalf("workload result = %d %v", code, err)
	}
}

func TestContainerReconciliationRequiresOriginalDaemonAndConfirmedRemoval(t *testing.T) {
	for _, scenario := range []string{"removed", "absent", "running", "unavailable", "other-daemon"} {
		t.Run(scenario, func(t *testing.T) {
			runner, commands, _ := containerTest(t)
			base := commands.run
			commands.run = func(ctx context.Context, p Process, output OutputFunc) (int, error) {
				switch p.Args[2] {
				case "info":
					if scenario == "other-daemon" {
						output("stdout", []byte("other-daemon"))
						return 0, nil
					}
				case "rm":
					if scenario != "removed" {
						return 1, errors.New("remove failed")
					}
				case "container":
					if scenario == "absent" {
						return 0, nil
					}
					if scenario == "unavailable" {
						return 1, errors.New("daemon unavailable")
					}
				}
				return base(ctx, p, output)
			}
			err := runner.Reconcile(t.Context(), operational.CLICommandExecution{CommandID: 1, Name: "cao-hosted-cli-" + strings.Repeat("a", 32), RuntimeID: "original-daemon"})
			wantUnconfirmed := scenario != "removed" && scenario != "absent"
			if errors.Is(err, ErrContainerTerminationUnconfirmed) != wantUnconfirmed {
				t.Fatalf("reconciliation = %v", err)
			}
			if scenario == "other-daemon" {
				for _, p := range commands.processes {
					if p.Args[2] == "rm" {
						t.Fatal("removed container on a different daemon")
					}
				}
			}
		})
	}
}

func TestContainerRunnerRejectsMutableImagesAndOutsideWorkspaces(t *testing.T) {
	for _, image := range []string{"ghcr.io/github/gh-aw:latest", "ghcr.io/github/gh-aw@sha256:abc"} {
		if _, err := NewContainerRunner(image, newTestOCILifecycle(nil)); err == nil {
			t.Errorf("mutable or invalid image %q was accepted", image)
		}
	}
	runner, _, process := containerTest(t)
	process.Dir = t.TempDir()
	if _, err := runner.Run(t.Context(), process, nil); err == nil || !strings.Contains(err.Error(), "inside its workspace") {
		t.Fatalf("outside working directory was not rejected: %v", err)
	}
}
