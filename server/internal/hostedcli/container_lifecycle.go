package hostedcli

import (
	"context"
	"errors"

	"github.com/githubnext/gh-cao/server/internal/operational"
)

// ErrContainerTerminationUnconfirmed means the durable claim must not be
// settled and the workspace must remain intact for operator recovery.
var ErrContainerTerminationUnconfirmed = errors.New("hosted CLI container termination is unconfirmed")

// ContainerSpec is the validated command and its private workspace. Adapters
// must isolate this workspace, use only Env, and initialize the Git root inside
// the pinned image before executing Program and Args without shell expansion.
type ContainerSpec struct {
	Image     string
	Workspace string
	Directory string
	Program   string
	Args      []string
	Env       []string
}

// ContainerState separates workload termination from output transport exit.
// The zero value is unconfirmed; ExitCode is optional even after termination.
type ContainerState struct {
	Terminated bool
	ExitCode   *int
}

// ContainerLifecycle manages an execution whose identity the runner persists
// before Create. Every operation must honor context cancellation/deadlines and
// address exactly that identity; it must not automatically retry workloads.
type ContainerLifecycle interface {
	// RuntimeID is stable across restarts and identifies the backend and its
	// execution namespace. Each adapter must use distinct backend-scoped
	// IDs so recovery never confuses namespaces.
	RuntimeID(context.Context) (string, error)
	Create(context.Context, operational.CLICommandExecution, ContainerSpec) error
	// Start launches without waiting for workload termination.
	Start(context.Context, operational.CLICommandExecution) error
	Inspect(context.Context, operational.CLICommandExecution) (ContainerState, error)
	// Stop succeeds only after confirming termination/absence, not merely
	// accepting a stop request, and removes execution resources when possible.
	Stop(context.Context, operational.CLICommandExecution) error
	// CollectOutput replays output from startup then follows until transport
	// closure. Neither closure nor success proves that the workload exited.
	CollectOutput(context.Context, operational.CLICommandExecution, OutputFunc) error
}
