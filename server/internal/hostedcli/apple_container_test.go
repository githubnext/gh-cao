//go:build !windows

package hostedcli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/operational"
)

func appleTestExecution() operational.CLICommandExecution {
	return operational.CLICommandExecution{CommandID: 1, Name: "cao-hosted-cli-" + strings.Repeat("a", 32), RuntimeID: "apple:test"}
}

func appleContainerTest(t *testing.T) (*ContainerRunner, *AppleContainerLifecycle, *containerCommandRunner, Process) {
	t.Helper()
	_, _, process := containerTest(t)
	appRoot := t.TempDir()
	commands := &containerCommandRunner{}
	lifecycle := &AppleContainerLifecycle{Binary: "container", Commands: commands, HostEnv: []string{"PATH=/usr/bin", "HOME=/private/developer"}}
	commands.run = func(_ context.Context, p Process, output OutputFunc) (int, error) {
		if p.Program == "/bin/launchctl" {
			if p.Args[0] == "managername" {
				output("stdout", []byte("Aqua\n"))
			} else {
				output("stdout", []byte("PID\tStatus\tLabel\n"))
			}
			return 0, nil
		}
		switch p.Args[0] {
		case "system":
			output("stdout", fmt.Appendf(nil, `{"status":"running","appRoot":%q}`, appRoot))
		case "create":
			output("stdout", []byte(p.Args[slices.Index(p.Args, "--name")+1]+"\n"))
		case "start":
			name := p.Args[len(p.Args)-1]
			run, err := lifecycle.execution(name)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(run.workspace, ".cao-apple/result"), []byte(name+"\n0\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			output("stdout", []byte("native stdout\n"))
			output("stderr", []byte("native stderr\n"))
		case "inspect":
			output("stdout", fmt.Appendf(nil, `[{"id":%q,"status":{"state":"stopped"}}]`, p.Args[1]))
		case "delete", "list":
		default:
			t.Fatalf("unexpected native runtime call %v", p.Args)
		}
		return 0, nil
	}
	runner, err := NewContainerRunner("", lifecycle)
	if err != nil {
		t.Fatal(err)
	}
	return runner, lifecycle, commands, process
}

func TestAppleContainerUsesIsolatedPinnedGuest(t *testing.T) {
	runner, _, commands, process := appleContainerTest(t)
	var streams []string
	code, err := runner.Run(t.Context(), process, func(stream string, data []byte) {
		streams = append(streams, stream+":"+string(data))
	})
	if code != 0 || err != nil || !slices.Equal(streams, []string{"stdout:native stdout\n", "stderr:native stderr\n"}) {
		t.Fatalf("native result = %d %v %v", code, err, streams)
	}
	for _, p := range commands.processes {
		if p.Args[0] != "create" {
			continue
		}
		for _, arg := range []string{"--read-only", "--cap-drop", "ALL", "--ulimit", "nproc=256:256", "--memory", "1g", "--no-new-privs", "/bin/setpriv", DefaultContainerImage} {
			if !slices.Contains(p.Args, arg) {
				t.Fatalf("missing native isolation argument %q", arg)
			}
		}
		for _, arg := range []string{"--ssh", "--publish-socket", "--virtualization", "--rm", "--debug"} {
			if slices.Contains(p.Args, arg) {
				t.Fatalf("unexpected native authority %q", arg)
			}
		}
		if strings.Contains(strings.Join(p.Args, " "), "scoped-value") {
			t.Fatal("scoped token was placed in native CLI arguments")
		}
		envFile := p.Args[slices.Index(p.Args, "--env-file")+1]
		if _, err := os.Stat(envFile); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("native environment file was retained")
		}
	}
}

func TestAppleContainerPreservesArgumentBoundaries(t *testing.T) {
	for _, count := range []int{0, 1, 256} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			_, lifecycle, commands, _ := appleContainerTest(t)
			args := make([]string, count)
			for i := range args {
				args[i] = fmt.Sprintf("argument %d; literal 'value'", i)
			}
			workspace := t.TempDir()
			spec := ContainerSpec{
				Workspace: workspace, Directory: workspace, Image: DefaultContainerImage,
				Program: "gh-aw", Args: args,
			}
			if err := lifecycle.Create(t.Context(), appleTestExecution(), spec); err != nil {
				t.Fatal(err)
			}
			created := commands.processes[len(commands.processes)-1].Args
			start := slices.Index(created, "cao-hosted-cli")
			want := append([]string{"cao-hosted-cli", appleTestExecution().Name, spec.Program}, args...)
			if start < 0 || !slices.Equal(created[start:], want) {
				t.Fatal("native command changed argument order or boundaries")
			}
		})
	}
}

func TestAppleContainerExitRequiresReceiptAndServiceAbsence(t *testing.T) {
	for _, scenario := range []string{"receipt", "receipt-client-exit", "missing", "wrong-identity", "invalid-code", "oversized", "symlink", "vm-still-present", "service-inventory-failed"} {
		t.Run(scenario, func(t *testing.T) {
			runner, lifecycle, commands, process := appleContainerTest(t)
			base := commands.run
			commands.run = func(ctx context.Context, p Process, output OutputFunc) (int, error) {
				if p.Args[0] == "start" {
					name := p.Args[len(p.Args)-1]
					run, _ := lifecycle.execution(name)
					result := filepath.Join(run.workspace, ".cao-apple/result")
					switch scenario {
					case "receipt", "receipt-client-exit":
						if err := os.WriteFile(result, []byte(name+"\n17\n"), 0o600); err != nil {
							t.Fatal(err)
						}
						if scenario == "receipt-client-exit" {
							return 17, nil
						}
					case "wrong-identity", "invalid-code", "oversized":
						content := map[string]string{"wrong-identity": "other\n0\n", "invalid-code": name + "\n256\n", "oversized": strings.Repeat("0", 128)}[scenario]
						if err := os.WriteFile(result, []byte(content), 0o600); err != nil {
							t.Fatal(err)
						}
					case "symlink":
						if err := os.Symlink("/dev/zero", result); err != nil {
							t.Fatal(err)
						}
					}
					return 0, nil
				}
				return base(ctx, p, output)
			}
			if scenario == "vm-still-present" || scenario == "service-inventory-failed" {
				// Inspect directly: a stopped API snapshot does not prove that
				// the separate VM service was successfully unregistered.
				execution := appleTestExecution()
				commands.run = func(ctx context.Context, p Process, output OutputFunc) (int, error) {
					if p.Program == "/bin/launchctl" && p.Args[0] == "list" {
						if scenario == "service-inventory-failed" {
							return 1, errors.New("unavailable")
						}
						output("stdout", []byte("PID Status Label\n123 0 com.apple.container.container-runtime-linux."+execution.Name+"\n"))
						return 0, nil
					}
					return base(ctx, p, output)
				}
				state, _ := lifecycle.Inspect(t.Context(), execution)
				if state.Terminated {
					t.Fatal("unconfirmed VM service was treated as terminated")
				}
				stopCtx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
				defer cancel()
				if err := lifecycle.Stop(stopCtx, execution); err == nil {
					t.Fatal("successful native delete was mistaken for VM termination")
				}

				return
			}
			code, err := runner.Run(t.Context(), process, nil)
			if scenario == "receipt" || scenario == "receipt-client-exit" {
				if code != 17 || err != nil {
					t.Fatalf("workload exit = %d %v", code, err)
				}
			} else if code != -1 || err == nil {
				t.Fatalf("missing or invalid receipt became workload success: %d %v", code, err)
			}
		})
	}
}

func TestAppleRuntimeIdentityAndRecovery(t *testing.T) {
	runner, lifecycle, commands, _ := appleContainerTest(t)
	id, err := lifecycle.RuntimeID(t.Context())
	if err != nil || !strings.HasPrefix(id, "apple:") {
		t.Fatalf("runtime identity = %q %v", id, err)
	}
	replacement := &AppleContainerLifecycle{Binary: lifecycle.Binary, Commands: commands, HostEnv: lifecycle.HostEnv}
	second, err := replacement.RuntimeID(t.Context())
	if err != nil || second != id {
		t.Fatal("runtime identity did not survive adapter restart")
	}
	execution := appleTestExecution()
	execution.RuntimeID = id
	if err := runner.Reconcile(t.Context(), execution); err != nil {
		t.Fatal(err)
	}
	execution.RuntimeID = "apple:other-runtime"
	before := len(commands.processes)
	if err := runner.Reconcile(t.Context(), execution); !errors.Is(err, ErrContainerTerminationUnconfirmed) {
		t.Fatalf("recovery accepted another runtime: %v", err)
	}
	for _, p := range commands.processes[before:] {
		if p.Args[0] == "delete" {
			t.Fatal("recovery removed a container from another runtime")
		}
	}
}

func TestAppleRuntimeIdentityCorruptionFailsClosed(t *testing.T) {
	root := t.TempDir()
	seed, err := containerRuntimeSeed(root)
	if err != nil || len(seed) != 32 {
		t.Fatalf("native identity initialization: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "cao-hosted-cli", "identity"), []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := containerRuntimeSeed(root); err == nil {
		t.Fatal("corrupt native runtime identity was silently replaced")
	}
}

func TestAppleContainerClientFailureIsNotWorkloadSuccess(t *testing.T) {
	runner, _, commands, process := appleContainerTest(t)
	base := commands.run
	commands.run = func(ctx context.Context, p Process, output OutputFunc) (int, error) {
		code, err := base(ctx, p, output)
		if p.Args[0] == "start" {
			return 1, nil
		}
		return code, err
	}
	code, err := runner.Run(t.Context(), process, nil)
	if code != 0 || err == nil {
		t.Fatalf("native client failure was lost behind the guest receipt: %d %v", code, err)
	}
}
