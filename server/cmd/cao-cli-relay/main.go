// cao-cli-relay runs only inside the network-none Docker workload. It holds
// neither the policy nor host credentials and cannot select another socket.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
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
	ready, err := (&net.Dialer{Timeout: 5 * time.Second}).DialContext(ctx, "unix", cliegress.SocketPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "hosted CLI egress socket is unavailable")
		return 125
	}
	_ = ready.Close()
	listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp4", cliegress.RelayAddress)
	if err != nil {
		fmt.Fprintln(os.Stderr, "hosted CLI egress relay is unavailable")
		return 125
	}
	defer func() { _ = listener.Close() }()
	go func() {
		if err := cliegress.Relay(ctx, listener, cliegress.SocketPath); err != nil {
			fmt.Fprintln(os.Stderr, "hosted CLI egress relay stopped unexpectedly")
			cancel()
		}
	}()
	return runWorkload(ctx, os.Args[1:], os.Environ(), os.Stdin, os.Stdout, os.Stderr)
}

// runWorkload runs the workload with every proxy variable pointed at the relay
// and returns the relay exit status.
func runWorkload(ctx context.Context, args, environ []string, stdin io.Reader, stdout, stderr io.Writer) int {
	// #nosec G204 G702 -- fixed bootstrap and validated CLI arguments supplied by the Docker lifecycle, never host execution.
	command := exec.CommandContext(ctx, args[0], args[1:]...)
	command.Stdin, command.Stdout, command.Stderr = stdin, stdout, stderr
	command.Env = workloadEnv(environ)
	relayLog.Printf("starting workload: args=%d", len(args)-1)
	return workloadExitCode(command.Run())
}

// workloadExitCode maps a workload run result to the relay exit status: the
// workload's own code when it exited, 125 when it could not be run or was killed.
func workloadExitCode(err error) int {
	if err == nil {
		relayLog.Printf("workload exited: code=0")
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
