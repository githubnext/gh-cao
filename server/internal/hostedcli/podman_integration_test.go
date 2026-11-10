package hostedcli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/operational"
)

// Requires a preloaded pinned image with git, /bin/sh and ordinary Unix tools.
// Never pulls images, configures the host, or substitutes Docker/rootful mode.
func TestPodmanRealBoundary(t *testing.T) {
	if os.Getenv("CAO_TEST_PODMAN") != "1" {
		t.Skip("set CAO_TEST_PODMAN=1 on a Linux executor with delegated rootless cgroup v2 controllers")
	}
	if runtime.GOOS != "linux" {
		t.Skip("real Podman boundary requires Linux")
	}
	binary := strings.TrimSpace(os.Getenv("CAO_HOSTED_CLI_PODMAN"))
	if binary == "" {
		binary = "podman"
	}
	if _, err := exec.LookPath(binary); err != nil {
		t.Skip("Podman is unavailable; real boundary is not validated")
	}
	t.Setenv("CAO_PODMAN_TEST_SECRET", "must-not-reach-workload")
	lifecycle, err := NewPodmanContainerLifecycle(binary, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	value, code, err := lifecycle.engine.capture(ctx, []string{"info", "--format=json"})
	if err != nil || code != 0 {
		t.Fatalf("local Podman discovery failed: code=%d err=%v", code, err)
	}
	var info struct {
		Host struct {
			Security          struct{ Rootless, SelinuxEnabled bool }
			ServiceIsRemote   bool
			CgroupVersion     string
			CgroupControllers []string
		}
	}
	if err := json.Unmarshal([]byte(value), &info); err != nil {
		t.Fatal(err)
	}
	t.Logf("real engine evidence: rootless=%t remote=%t cgroup=%s controllers=%v SELinux-enabled=%t",
		info.Host.Security.Rootless, info.Host.ServiceIsRemote, info.Host.CgroupVersion, info.Host.CgroupControllers,
		info.Host.Security.SelinuxEnabled)
	if !info.Host.Security.SelinuxEnabled {
		t.Log("SELinux is disabled on this host; enforcing-SELinux relabel behavior is NOT validated")
	}
	if !info.Host.Security.Rootless || info.Host.ServiceIsRemote || info.Host.CgroupVersion != "v2" ||
		!slices.Contains(info.Host.CgroupControllers, "memory") || !slices.Contains(info.Host.CgroupControllers, "pids") {
		t.Skip("required rootless delegated memory/pids controllers are unavailable; workload isolation is NOT validated")
	}
	runtimeID, err := lifecycle.RuntimeID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	image := strings.TrimSpace(os.Getenv("CAO_TEST_PODMAN_IMAGE"))
	runner, err := NewContainerRunner(image, lifecycle)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := lifecycle.executionEngine(operational.CLICommandExecution{RuntimeID: runtimeID})
	if err != nil {
		t.Fatal(err)
	}
	_, code, err = engine.capture(ctx, []string{"image", "exists", runner.Image})
	if err == nil && code == 1 {
		t.Skip("the pinned test image is absent; preload it or set CAO_TEST_PODMAN_IMAGE; workload isolation is NOT validated")
	}
	if err != nil || code != 0 {
		t.Fatalf("local pinned image lookup failed: code=%d err=%v", code, err)
	}
	restarted, err := NewPodmanContainerLifecycle(binary, nil)
	if err != nil {
		t.Fatal(err)
	}
	if id, err := restarted.RuntimeID(ctx); err != nil || id != runtimeID {
		t.Fatalf("real runtime identity changed across adapter restart: %q, %v", id, err)
	}
	recovery, err := NewContainerRunner(runner.Image, restarted)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"result", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			// Keep test files in the checkout, and retain them if termination
			// cannot be proven; t.TempDir would unconditionally remove them.
			workspace, err := os.MkdirTemp(".", ".cao-podman-boundary-")
			if err != nil {
				t.Fatal(err)
			}
			workspace, err = filepath.Abs(workspace)
			if err != nil {
				t.Fatal(err)
			}
			name, err := containerName()
			if err != nil {
				t.Fatal(err)
			}
			execution := operational.CLICommandExecution{CommandID: 1, Name: name, RuntimeID: runtimeID}
			createAttempted := false
			t.Cleanup(func() {
				if createAttempted {
					if err := recovery.Reconcile(context.WithoutCancel(ctx), execution); err != nil {
						t.Errorf("test workload termination unconfirmed; retained workspace %s: %v", workspace, err)
						return
					}
				}
				if err := os.RemoveAll(workspace); err != nil {
					t.Error(err)
				}
			})
			if err := os.WriteFile(filepath.Join(workspace, "probe.sh"), []byte(podmanBoundaryProbe), 0o600); err != nil {
				t.Fatal(err)
			}
			record, err := json.Marshal(execution)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(workspace, "execution.json"), record, 0o600); err != nil {
				t.Fatal(err)
			}
			createAttempted = true
			if err := lifecycle.Create(ctx, execution, ContainerSpec{
				Image: runner.Image, Workspace: workspace, Directory: workspace, Program: "/bin/sh",
				Args: []string{"/workspace/probe.sh", mode, strconv.Itoa(os.Getuid()), strconv.Itoa(os.Getgid())},
				Env: []string{"HOME=/workspace", "GH_TOKEN=not-a-real-credential",
					"GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_TERMINAL_PROMPT=0"},
			}); err != nil {
				t.Fatal(err)
			}
			mountsJSON, code, err := engine.capture(ctx, []string{"inspect", "--format={{json .Mounts}}", name})
			var mounts []struct {
				Type        string
				Source      string
				Destination string
				RW          bool
			}
			if err != nil || code != 0 || json.Unmarshal([]byte(mountsJSON), &mounts) != nil {
				t.Fatal("real workload mount evidence is unavailable")
			}
			writableWorkspace := false
			for _, mount := range mounts {
				if mount.Type == "bind" && mount.RW {
					if mount.Source != workspace || mount.Destination != "/workspace" {
						t.Fatalf("unexpected writable host bind mount: %+v", mount)
					}
					writableWorkspace = true
				}
			}
			if !writableWorkspace {
				t.Fatal("private workspace bind mount is absent")
			}
			matching, code, err := engine.capture(ctx, []string{
				"container", "ls", "--all", "--quiet", "--filter", "name=^" + execution.Name + "$",
			})
			if err != nil || code != 0 || strings.TrimSpace(matching) == "" {
				t.Fatal("Podman exact-name filter did not match the existing owned container")
			}
			foreign, code, err := engine.capture(ctx, []string{
				"container", "ls", "--all", "--quiet", "--filter", "name=^" + execution.Name + "-foreign$",
			})
			if err != nil || code != 0 || strings.TrimSpace(foreign) != "" {
				t.Fatal("Podman exact-name filter matched a different name")
			}
			if err := lifecycle.Start(ctx, execution); err != nil {
				evidence, _, _ := engine.capture(ctx, []string{"inspect", podmanIsolationFormat, name})
				var fields map[string]any
				if json.Unmarshal([]byte(evidence), &fields) == nil {
					if host, ok := fields["HostConfig"].(map[string]any); ok {
						t.Logf("effective security evidence: privileged=%v readonly=%v caps=%v/%v capadd=%v securityopt=%v",
							host["Privileged"], host["ReadonlyRootfs"], fields["EffectiveCaps"],
							fields["BoundingCaps"], host["CapAdd"], host["SecurityOpt"])
						t.Logf("effective namespace evidence: network=%v user=%v pid=%v ipc=%v uts=%v cgroup=%v/%v tmpfs=%v",
							host["NetworkMode"], host["UsernsMode"], host["PidMode"], host["IpcMode"], host["UTSMode"],
							host["CgroupMode"], host["CgroupnsMode"], host["Tmpfs"])
						t.Logf("effective user mapping evidence: %v", host["IDMappings"])
					}
				}
				t.Fatal(err)
			}
			var output strings.Builder
			if mode == "cancel" {
				canceled, stop := context.WithCancel(ctx)
				defer stop()
				ready := false
				err := lifecycle.CollectOutput(canceled, execution, func(_ string, data []byte) {
					if output.Len()+len(data) <= 8192 {
						_, _ = output.Write(data)
					}
					if strings.Contains(output.String(), "boundary-ready") {
						ready = true
						stop()
					}
				})
				if !ready || !errors.Is(err, context.Canceled) {
					t.Fatalf("running workload did not reach cancellation point: %v; output=%s", err, output.String())
				}
				state, err := lifecycle.Inspect(ctx, execution)
				if err != nil || state.Terminated {
					t.Fatalf("transport cancellation incorrectly terminated workload: %+v, %v", state, err)
				}
				if !errors.Is(canceled.Err(), context.Canceled) {
					t.Fatalf("canceled output collection = %v", err)
				}
				if err := recovery.Reconcile(canceled, execution); err != nil {
					t.Fatalf("detached original-runtime recovery failed: %v", err)
				}
			} else {
				if err := lifecycle.CollectOutput(ctx, execution, func(stream string, data []byte) {
					if output.Len()+len(data) <= 8192 {
						_, _ = output.WriteString(stream + ":" + string(data))
					}
				}); err != nil {
					t.Fatal(err)
				}
				state, err := lifecycle.Inspect(ctx, execution)
				if err != nil || !state.Terminated || state.ExitCode == nil || *state.ExitCode != 7 {
					t.Fatalf("real workload result = %+v, %v; output=%s", state, err, output.String())
				}
				if !strings.Contains(output.String(), "stdout:boundary-stdout") ||
					!strings.Contains(output.String(), "stderr:boundary-stderr") {
					t.Fatalf("real startup output streams missing: %s", output.String())
				}
				if err := recovery.Reconcile(ctx, execution); err != nil {
					t.Fatal(err)
				}
			}
			inventory, code, err := engine.capture(ctx, []string{
				"container", "ls", "--all", "--quiet", "--filter", "name=^" + execution.Name + "$",
			})
			if err != nil || code != 0 || strings.TrimSpace(inventory) != "" {
				t.Fatal("original-runtime inventory did not confirm exact-owned-name removal")
			}
		})
	}
}

const podmanBoundaryProbe = `set -eu
test "$(id -u)" = "$2"
test "$(id -g)" = "$3"
test "${CAO_PODMAN_TEST_SECRET-unset}" = unset
test "$GH_TOKEN" = not-a-real-credential
grep -Eq '^CapEff:[[:space:]]+0000000000000000$' /proc/self/status
grep -Eq '^NoNewPrivs:[[:space:]]+1$' /proc/self/status
test "$(cat /sys/fs/cgroup/memory.max)" = 1073741824
test "$(cat /sys/fs/cgroup/pids.max)" = 256
awk '$2 == "/tmp" && $3 == "tmpfs" && $4 ~ /nosuid/ && $4 ~ /nodev/ && $4 ~ /noexec/ && $4 ~ /size=65536k/ { found = 1 }
END { exit !found }' /proc/mounts
if touch /cao-root-write 2>/dev/null; then exit 90; fi
printf workspace-only > /workspace/result
if test "$1" = cancel; then printf 'boundary-ready\n'; exec sleep 60; fi
printf 'boundary-stdout\n'
printf 'boundary-stderr\n' >&2
exit 7
`
