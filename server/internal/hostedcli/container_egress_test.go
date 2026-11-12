package hostedcli

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/cliegress"
	"github.com/githubnext/gh-cao/server/internal/operational"
)

func egressFixture(t *testing.T) (string, string, string) {
	t.Helper()
	dir := t.TempDir()
	policy := filepath.Join(dir, "policy.json")
	relay := filepath.Join(dir, "relay")
	if err := os.WriteFile(policy, []byte(`{"hosts":["api.github.com"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	// Deterministic ELF header for digest/architecture validation, never run.
	header := make([]byte, 64)
	copy(header, []byte{0x7f, 'E', 'L', 'F', 2, 1, 1})
	binary.LittleEndian.PutUint16(header[16:], 2)
	binary.LittleEndian.PutUint16(header[18:], 62)
	binary.LittleEndian.PutUint32(header[20:], 1)
	binary.LittleEndian.PutUint16(header[52:], 64)
	if err := os.WriteFile(relay, header, 0o600); err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(header)
	return policy, relay, hex.EncodeToString(hash[:])
}

func TestContainerEgressRequiresReviewedDigestAndImmutablePolicySnapshot(t *testing.T) {
	policy, relay, digest := egressFixture(t)
	egress, err := LoadContainerEgress(policy, relay, digest)
	if err != nil || egress.architecture != "amd64" {
		t.Fatalf("valid reviewed artifact rejected: %v", err)
	}
	for _, tc := range [][3]string{
		{"", relay, digest}, {policy, "", digest}, {policy, relay, ""},
		{policy, relay, strings.Repeat("0", 64)}, {policy + ".missing", relay, digest},
		{policy, filepath.Dir(relay), digest},
	} {
		if _, err := LoadContainerEgress(tc[0], tc[1], tc[2]); err == nil {
			t.Fatal("unavailable or unverified configuration accepted")
		}
	}
	if err := os.WriteFile(relay, []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(policy, []byte(`{"hosts":["evil.example"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(egress.relay)
	if hex.EncodeToString(sum[:]) != digest || !slices.Equal(egress.policy.Hosts, []string{"api.github.com"}) {
		t.Fatal("mutable host files changed loaded configuration")
	}
	for _, data := range [][]byte{[]byte("not-an-executable"), make([]byte, 64)} {
		if err := os.WriteFile(relay, data, 0o600); err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		if _, err := LoadContainerEgress(policy, relay, hex.EncodeToString(sum[:])); err == nil {
			t.Fatal("verified digest alone accepted a non-Linux relay")
		}
	}
}

func TestContainerEgressAcceptsBuiltRelayForBothLinuxArchitectures(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64"} {
		t.Run(arch, func(t *testing.T) {
			policy, path, _ := egressFixture(t)
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
			// #nosec G204 -- fixed package and flags; output is this test's private temporary file.
			command := exec.CommandContext(t.Context(), "go", "build", "-trimpath", "-buildvcs=false", "-ldflags=-s -w", "-o", path, "../../cmd/cao-cli-relay")
			command.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+arch, "CGO_ENABLED=0")
			if output, err := command.CombinedOutput(); err != nil {
				t.Fatalf("relay build failed: %v: %s", err, output)
			}
			// #nosec G304 -- the test's freshly built relay artifact.
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(data)
			config, err := LoadContainerEgress(policy, path, hex.EncodeToString(sum[:]))
			if err != nil || config.architecture != arch {
				t.Fatalf("built artifact failed validation: %v", err)
			}
		})
	}
}

func egressContainerTest(t *testing.T) (*ContainerRunner, *containerCommandRunner, Process) {
	t.Helper()
	runner, commands, process := containerTest(t)
	// Unix socket paths have a platform length limit; do not depend on long
	// Go test names or the worktree's pathname fitting inside it.
	workspace, err := os.MkdirTemp("", "ce-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(workspace) })
	process.Workspace, process.Dir = workspace, workspace
	policy, relay, digest := egressFixture(t)
	lifecycle := runner.Lifecycle.(testOCILifecycle)
	lifecycle.egress, err = LoadContainerEgress(policy, relay, digest)
	if err != nil {
		t.Fatal(err)
	}
	base := commands.run
	commands.run = func(ctx context.Context, p Process, output OutputFunc) (int, error) {
		if slices.Contains(p.Args, podmanPlatformFormat) {
			output("stdout", []byte("linux/amd64\n"))
			return 0, nil
		}
		return base(ctx, p, output)
	}
	return runner, commands, process
}

func TestContainerEgressUsesNetworkNoneAndPrivateReadOnlyRelay(t *testing.T) {
	runner, commands, process := egressContainerTest(t)
	registered := false
	process.RegisterContainer = func(context.Context, operational.CLICommandExecution) error { registered = true; return nil }
	base := commands.run
	socket := filepath.Join(process.Workspace, ".cao-egress", "proxy.sock")
	commands.run = func(ctx context.Context, p Process, output OutputFunc) (int, error) {
		if p.Args[2] == "create" {
			if !registered || p.Args[slices.Index(p.Args, "--network")+1] != "none" {
				t.Fatal("egress did not follow registration with a network-none workload")
			}
			if slices.Contains(p.Args, "bridge") || slices.Contains(p.Args, "--privileged") ||
				!slices.Contains(p.Args, "/workspace/.cao-egress:ro,nosuid,nodev,noexec,size=4k,notmpcopyup") {
				t.Fatal("unrestricted networking or writable relay alias")
			}
			resolved, err := filepath.EvalSymlinks(filepath.Dir(socket))
			if err != nil || !slices.Contains(p.Args, "type=bind,src="+resolved+",dst=/cao-egress,readonly") {
				t.Fatal("relay directory is not mounted read-only")
			}
			if p.Args[slices.Index(p.Args, "--entrypoint")+1] != cliegress.RelayPath {
				t.Fatal("verified relay is not the entrypoint")
			}
			for path, mode := range map[string]os.FileMode{
				filepath.Dir(socket): 0o700, socket: 0o600, filepath.Join(filepath.Dir(socket), "relay"): 0o500,
			} {
				info, err := os.Stat(path)
				if err != nil || info.Mode().Perm() != mode {
					t.Fatalf("unprotected egress resource: %v", err)
				}
			}
			conn, err := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "unix", socket)
			if err != nil {
				t.Fatal("private proxy did not start")
			}
			_ = conn.Close()
			if strings.Contains(strings.Join(p.Args, " "), "scoped-value") {
				t.Fatal("token leaked into runtime arguments")
			}
		}
		return base(ctx, p, output)
	}
	if code, err := runner.Run(t.Context(), process, nil); err != nil || code != 0 {
		t.Fatalf("execution failed: %d %v", code, err)
	}
	if conn, err := (&net.Dialer{Timeout: time.Second}).DialContext(t.Context(), "unix", socket); err == nil {
		_ = conn.Close()
		t.Fatal("proxy outlived confirmed workload cleanup")
	}
}

func TestContainerEgressFailureNeverCreatesBridgeWorkload(t *testing.T) {
	for _, scenario := range []string{"daemon-unavailable", "architecture", "socket-unavailable", "canceled"} {
		t.Run(scenario, func(t *testing.T) {
			runner, commands, process := egressContainerTest(t)
			base := commands.run
			commands.run = func(ctx context.Context, p Process, output OutputFunc) (int, error) {
				if slices.Contains(p.Args, podmanPlatformFormat) {
					if scenario == "daemon-unavailable" {
						return 1, errors.New("unavailable secret-response")
					}
					if scenario == "architecture" {
						output("stdout", []byte("linux/arm64\n"))
						return 0, nil
					}
				}
				return base(ctx, p, output)
			}
			if scenario == "socket-unavailable" {
				if err := os.Mkdir(filepath.Join(process.Workspace, ".cao-egress"), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if scenario == "canceled" {
				cancel()
			}
			if _, err := runner.Run(ctx, process, nil); err == nil || strings.Contains(err.Error(), "secret-response") {
				t.Fatal("egress error was lost or exposed runtime output")
			}
			for _, p := range commands.processes {
				if p.Args[2] == "create" {
					t.Fatal("failed boundary created a workload")
				}
			}
		})
	}
}

func TestContainerEgressCancellationCutsProxyButRetainsUnconfirmedClaim(t *testing.T) {
	runner, commands, process := egressContainerTest(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	base := commands.run
	commands.run = func(callCtx context.Context, p Process, output OutputFunc) (int, error) {
		switch p.Args[2] {
		case "logs":
			cancel()
			return -1, context.Canceled
		case "inspect":
			output("stdout", []byte(`{"Status":"running","Running":true}`))
			return 0, nil
		case "rm":
			if callCtx.Err() != nil {
				t.Fatal("bounded cleanup inherited cancellation")
			}
			return 1, errors.New("daemon unavailable")
		}
		return base(callCtx, p, output)
	}
	if _, err := runner.Run(ctx, process, nil); !errors.Is(err, ErrContainerTerminationUnconfirmed) {
		t.Fatalf("unconfirmed removal lost protected claim semantics: %v", err)
	}
	socket := filepath.Join(process.Workspace, ".cao-egress", "proxy.sock")
	if conn, err := (&net.Dialer{Timeout: time.Second}).DialContext(t.Context(), "unix", socket); err == nil {
		_ = conn.Close()
		t.Fatal("canceled command retained egress")
	}
	if _, err := os.Stat(filepath.Join(process.Workspace, ".cao-egress", "relay")); err != nil {
		t.Fatal("unconfirmed command resources were removed")
	}
}

func TestContainerEgressStopWaitsForConcurrentProxyShutdown(t *testing.T) {
	closing := make(chan struct{})
	release := make(chan struct{})
	closed := make(chan struct{})
	lifecycle := &ociContainerLifecycle{relays: &egressRelays{
		closers: map[string]func(){
			"command": func() {
				close(closing)
				<-release
				close(closed)
			},
		},
	}}
	firstDone := make(chan struct{})
	go func() {
		lifecycle.stopEgress("command")
		close(firstDone)
	}()
	<-closing
	secondDone := make(chan struct{})
	go func() {
		lifecycle.stopEgress("command")
		close(secondDone)
	}()
	select {
	case <-secondDone:
		t.Error("concurrent stop returned before proxy shutdown completed")
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	<-firstDone
	<-secondDone
	select {
	case <-closed:
	default:
		t.Fatal("stop returned without closing the proxy")
	}
	if len(lifecycle.relays.closers) != 0 {
		t.Fatal("stopped proxy retained its cleanup callback")
	}
}
