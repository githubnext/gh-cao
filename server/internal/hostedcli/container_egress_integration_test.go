package hostedcli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/operational"
)

// Requires a real local rootless Podman engine and a preloaded pinned image.
// Never pulls images, configures the host, mutates host networks, or treats
// mock command output as kernel proof.
func TestContainerEgressRealBoundary(t *testing.T) {
	if os.Getenv("CAO_TEST_PODMAN_EGRESS") != "1" {
		t.Skip("set CAO_TEST_PODMAN_EGRESS=1 on a dedicated Linux rootless Podman executor")
	}
	if runtime.GOOS != "linux" {
		t.Fatal("real Podman egress boundary requires Linux")
	}
	binary := strings.TrimSpace(os.Getenv("CAO_HOSTED_CLI_PODMAN"))
	if binary == "" {
		binary = "podman"
	}
	if _, err := exec.LookPath(binary); err != nil {
		t.Fatal("Podman is unavailable; real network boundary is not validated")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	// The relay is attached after its build; the platform query needs none.
	lifecycle, err := NewPodmanContainerLifecycle(binary, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	runtimeID, err := lifecycle.RuntimeID(ctx)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := lifecycle.executionEngine(operational.CLICommandExecution{RuntimeID: runtimeID})
	if err != nil {
		t.Fatal(err)
	}
	platform, code, err := engine.capture(ctx, []string{"info", podmanPlatformFormat})
	arch := map[string]string{"linux/amd64": "amd64", "linux/arm64": "arm64"}[strings.TrimSpace(platform)]
	if err != nil || code != 0 || arch == "" {
		t.Fatal("integration test requires a supported local Linux Podman engine")
	}
	image := strings.TrimSpace(os.Getenv("CAO_TEST_PODMAN_IMAGE"))
	runner, err := NewContainerRunner(image, lifecycle)
	if err != nil {
		t.Fatal(err)
	}
	if _, code, err := engine.capture(ctx, []string{"image", "exists", runner.Image}); err != nil || code != 0 {
		t.Fatal("the pinned test image is absent; preload it or set CAO_TEST_PODMAN_IMAGE; the egress boundary is NOT validated")
	}
	workspace, err := os.MkdirTemp("", "cao-net-")
	if err != nil {
		t.Fatal(err)
	}
	name, err := containerName()
	if err != nil {
		t.Fatal(err)
	}
	execution := operational.CLICommandExecution{CommandID: 1, Name: name, RuntimeID: runtimeID}
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		if runner.Reconcile(cleanup, execution) != nil {
			t.Errorf("test workload termination unconfirmed; retained workspace %s", workspace)
			return
		}
		_ = os.RemoveAll(workspace)
	})
	for _, build := range [][2]string{
		{"../../cmd/cao-cli-relay", "relay"},
		{"./testdata/egress-probe", "probe"},
	} {
		// #nosec G204 -- fixed test-owned build targets and private output paths.
		command := exec.CommandContext(ctx, "go", "build", "-trimpath", "-o", filepath.Join(workspace, build[1]), build[0])
		command.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+arch)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("test helper build failed: %v: %s", err, output)
		}
	}
	policyPath := filepath.Join(workspace, "policy.json")
	if err := os.WriteFile(policyPath, []byte(`{"hosts":["github.com","api.github.com"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// #nosec G304 -- the test's freshly built relay artifact.
	data, err := os.ReadFile(filepath.Join(workspace, "relay"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	egress, err := LoadContainerEgress(policyPath, filepath.Join(workspace, "relay"), hex.EncodeToString(sum[:]))
	if err != nil {
		t.Fatal(err)
	}
	// Derived engines share the adapter's egress configuration and relays.
	lifecycle.engine.egress = egress
	if _, err := lifecycle.RuntimeID(ctx); err != nil {
		t.Fatal(err)
	}
	// Record the test identity before any create, like the production queue.
	if err := os.WriteFile(filepath.Join(workspace, "execution"), []byte(runtimeID+"\n"+name), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := lifecycle.Create(ctx, execution, ContainerSpec{
		Image: runner.Image, Workspace: workspace, Directory: workspace,
		Program: "/workspace/probe", Env: []string{"HOME=/workspace", "GH_PROMPT_DISABLED=1", "GIT_TERMINAL_PROMPT=0"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := lifecycle.Start(ctx, execution); err != nil {
		t.Fatal(err)
	}
	var output strings.Builder
	if err := lifecycle.CollectOutput(ctx, execution, func(_ string, data []byte) {
		if output.Len()+len(data) < 8192 {
			_, _ = output.Write(data)
		}
	}); err != nil {
		t.Fatal(err)
	}
	state, err := lifecycle.Inspect(ctx, execution)
	if err != nil || !state.Terminated || state.ExitCode == nil || *state.ExitCode != 0 {
		t.Fatalf("real boundary probe failed: %v: %s", err, output.String())
	}
	if !strings.Contains(output.String(), "network-none: direct TCP/UDP/DNS denied") {
		t.Fatal("real boundary probe did not produce its success evidence")
	}
	t.Log(output.String())
}
