package hostedcli

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/githubnext/gh-cao/server/internal/operational"
)

func (r *ociContainerLifecycle) capture(ctx context.Context, args []string) (string, int, error) {
	var value strings.Builder
	overflow := false
	limit := r.responseLimit
	if limit == 0 {
		limit = 4096
	}
	code, err := r.commands.Run(ctx, Process{
		Program: r.binary, Args: r.args(args...), Env: r.hostEnv,
	}, func(stream string, data []byte) {
		if stream != "stdout" {
			return
		}
		if value.Len()+len(data) > limit {
			overflow = true
			return
		}
		_, _ = value.Write(data)
	})
	if overflow {
		return "", code, errors.New("hosted CLI container runtime response exceeded its limit")
	}
	return value.String(), code, err
}

func (r *ociContainerLifecycle) Stop(ctx context.Context, execution operational.CLICommandExecution) error {
	// Cutting egress is safe even when workload removal is unconfirmed. It
	// does not prove termination or permit releasing the durable claim.
	r.stopEgress(execution.Name)
	_, code, err := r.capture(ctx, []string{"rm", "--force", execution.Name})
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err == nil && code == 0 {
		return nil
	}
	// A successful, empty exact-name inventory proves absence. An inspect/rm
	// error alone cannot distinguish absence from an unavailable engine.
	// Podman names do not have a leading slash.
	value, code, err := r.capture(ctx, []string{
		"container", "ls", "--all", "--quiet", "--no-trunc", "--filter", "name=^" + execution.Name + "$",
	})
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err == nil && code == 0 && strings.TrimSpace(value) == "" {
		return nil
	}
	return errors.New("hosted CLI container removal failed")
}

func (r *ociContainerLifecycle) Inspect(ctx context.Context, execution operational.CLICommandExecution) (ContainerState, error) {
	value, code, err := r.capture(ctx, []string{"inspect", "--format={{json .State}}", execution.Name})
	if ctx.Err() != nil {
		return ContainerState{}, ctx.Err()
	}
	var state struct {
		Status   string
		Running  *bool
		ExitCode *int
	}
	if err != nil || code != 0 || json.Unmarshal([]byte(value), &state) != nil {
		return ContainerState{}, errors.New("hosted CLI container workload state is unavailable")
	}
	if state.Status == "exited" && state.Running != nil && !*state.Running &&
		state.ExitCode != nil && *state.ExitCode >= 0 && *state.ExitCode <= 255 {
		return ContainerState{Terminated: true, ExitCode: state.ExitCode}, nil
	}
	return ContainerState{}, nil
}

func (r *ociContainerLifecycle) Start(ctx context.Context, execution operational.CLICommandExecution) error {
	_, code, err := r.capture(ctx, []string{"start", execution.Name})
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil || code != 0 {
		return errors.New("hosted CLI container could not be started")
	}
	return nil
}

func (r *ociContainerLifecycle) CollectOutput(ctx context.Context, execution operational.CLICommandExecution, output OutputFunc) error {
	code, err := r.commands.Run(ctx, Process{
		Program: r.binary, Args: r.args("logs", "--follow", execution.Name), Env: r.hostEnv,
	}, output)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil || code != 0 {
		return errors.New("hosted CLI container output collection failed")
	}
	return nil
}
