package operational

import (
	"context"
	"regexp"
)

// CLICommandExecutionQueue persists container identity before any runtime
// side effect. Hosted executors require this capability in addition to the
// command queue. Identities are immutable and retained with command history.
type CLICommandExecutionQueue interface {
	RegisterExecution(context.Context, CLICommandLease, CLICommandExecution) error
	UnsettledExecutions(context.Context, CLICommandLease) ([]CLICommandExecution, error)
}

// CLICommandExecution identifies a container in its originating runtime namespace.
// A missing container in a different namespace is not proof of termination.
type CLICommandExecution struct {
	CommandID int64
	Name      string
	RuntimeID string
}

var cliContainerName = regexp.MustCompile(`^cao-hosted-cli-[a-f0-9]{32}$`)
var cliRuntimeID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9:._-]{0,127}$`)

func (e CLICommandExecution) Validate() error {
	if e.CommandID < 1 || !cliContainerName.MatchString(e.Name) || !cliRuntimeID.MatchString(e.RuntimeID) {
		return ErrCLICommandInput
	}
	return nil
}
