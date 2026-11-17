// cao-cli-relay runs only inside the network-none Docker workload. It holds
// neither the policy nor host credentials and cannot select another socket.
package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/githubnext/gh-cao/server/internal/cliegress"
	"github.com/githubnext/gh-cao/server/internal/logger"
)

var relayLog = logger.New("cao:cli-relay")

const egressProbeTimeout = 5 * time.Second

var proxyVariables = []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy"}

// workloadEnv drops inherited proxy settings and points every proxy variable at
// the relay so the workload can only egress through it.
func workloadEnv(environ []string) []string {
	var env []string
	for _, entry := range environ {
		name, _, _ := strings.Cut(entry, "=")
		switch strings.ToUpper(name) {
		case "HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY":
		default:
			env = append(env, entry)
		}
	}
	for _, name := range proxyVariables {
		env = append(env, name+"=http://"+cliegress.RelayAddress)
	}
	return append(env, "NO_PROXY=", "no_proxy=")
}

func main() { os.Exit(run()) }

func run() int {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "hosted CLI relay requires a workload")
		return 125
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := probeEgressSocket(ctx, cliegress.SocketPath); err != nil {
		fmt.Fprintln(os.Stderr, "hosted CLI egress socket is unavailable")
		return 125
	}
	listener, err := startRelay(ctx, cancel, cliegress.RelayAddress, cliegress.SocketPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "hosted CLI egress relay is unavailable")
		return 125
	}
	defer func() { _ = listener.Close() }()
	// #nosec G204 G702 -- fixed bootstrap and validated CLI arguments supplied by the Docker lifecycle, never host execution.
	command := exec.CommandContext(ctx, os.Args[1], os.Args[2:]...)
	command.Stdin, command.Stdout, command.Stderr = os.Stdin, os.Stdout, os.Stderr
	command.Env = workloadEnv(os.Environ())
	relayLog.Printf("starting workload: args=%d", len(os.Args)-2)
	return workloadExitCode(command.Run())
}

// startRelay listens on address and relays accepted connections to socket in
// the background. An unexpected relay failure cancels the workload context so
// the workload never keeps running without a working proxy.
func startRelay(ctx context.Context, cancel context.CancelFunc, address, socket string) (net.Listener, error) {
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp4", address)
	if err != nil {
		relayLog.Printf("egress relay listen failed: ctx_done=%t", ctx.Err() != nil)
		return nil, err
	}
	go func() {
		if err := cliegress.Relay(ctx, listener, socket); err != nil {
			relayLog.Printf("egress relay stopped unexpectedly")
			fmt.Fprintln(os.Stderr, "hosted CLI egress relay stopped unexpectedly")
			cancel()
		}
	}()
	relayLog.Printf("egress relay listening")
	return listener, nil
}

// probeEgressSocket confirms the host egress socket accepts a connection before
// the relay starts, so the workload never runs without a reachable proxy.
func probeEgressSocket(ctx context.Context, socket string) error {
	ready, err := (&net.Dialer{Timeout: egressProbeTimeout}).DialContext(ctx, "unix", socket)
	if err != nil {
		relayLog.Printf("egress socket probe failed: ctx_done=%t", ctx.Err() != nil)
		return err
	}
	relayLog.Printf("egress socket probe succeeded")
	_ = ready.Close()
	return nil
}

// workloadExitCode maps a workload run result to the relay exit status: the
// workload's own code when it exited, 125 when it could not be run or was killed.
func workloadExitCode(err error) int {
	if err == nil {
		return 0
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() >= 0 {
		relayLog.Printf("workload exited: code=%d", exit.ExitCode())
		return exit.ExitCode()
	}
	relayLog.Printf("workload failed to run")
	fmt.Fprintln(os.Stderr, "hosted CLI relay workload failed")
	return 125
}
