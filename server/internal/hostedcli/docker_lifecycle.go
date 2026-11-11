package hostedcli

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/githubnext/gh-cao/server/internal/operational"
)

func (r *DockerContainerLifecycle) RuntimeID(ctx context.Context) (string, error) {
	if err := r.requireLocalDockerContext(ctx); err != nil {
		return "", err
	}
	value, code, err := r.capture(ctx, []string{"info", "--format={{.ID}}"})
	id := strings.TrimSpace(value)
	if err != nil || code != 0 || id == "" || len(id) > 128 || strings.ContainsAny(id, "\x00\r\n") {
		return "", errors.New("hosted CLI Docker daemon identity is unavailable")
	}
	return id, nil
}

func (r *DockerContainerLifecycle) capture(ctx context.Context, args []string) (string, int, error) {
	var value strings.Builder
	overflow := false
	limit := r.responseLimit
	if limit == 0 {
		limit = 4096
	}
	code, err := r.Commands.Run(ctx, Process{
		Program: r.DockerBinary, Args: r.args(args...), Env: r.HostEnv,
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

func (r *DockerContainerLifecycle) Stop(ctx context.Context, execution operational.CLICommandExecution) error {
	// Cutting egress is safe even when workload removal is unconfirmed. It
	// does not prove termination or permit releasing the durable claim.
	r.stopEgress(execution.Name)
	code, err := r.Commands.Run(ctx, Process{
		Program: r.DockerBinary, Args: r.args("rm", "--force", execution.Name), Env: r.HostEnv,
	}, nil)
	if err == nil && code == 0 {
		return nil
	}
	// A successful, empty exact-name inventory proves absence. An inspect/rm
	// error alone cannot distinguish absence from a disconnected daemon.
	value, code, err := r.capture(ctx, []string{"container", "ls", "--all", "--quiet", "--no-trunc", "--filter", "name=^/" + execution.Name + "$"})
	if err == nil && code == 0 && strings.TrimSpace(value) == "" {
		return nil
	}
	return errors.New("hosted CLI container removal failed")
}

func (r *DockerContainerLifecycle) Inspect(ctx context.Context, execution operational.CLICommandExecution) (ContainerState, error) {
	value, inspectCode, inspectErr := r.capture(ctx, []string{"inspect", "--format={{json .State}}", execution.Name})
	var state struct {
		Status     string
		Running    *bool
		Restarting *bool
		ExitCode   *int
	}
	if inspectErr != nil || inspectCode != 0 || json.Unmarshal([]byte(value), &state) != nil {
		return ContainerState{}, errors.New("hosted CLI Docker workload state is unavailable")
	}
	if state.Status == "exited" && state.Running != nil && !*state.Running &&
		state.Restarting != nil && !*state.Restarting && state.ExitCode != nil &&
		*state.ExitCode >= 0 && *state.ExitCode <= 255 {
		return ContainerState{Terminated: true, ExitCode: state.ExitCode}, nil
	}
	return ContainerState{}, nil
}

func (r *DockerContainerLifecycle) Start(ctx context.Context, execution operational.CLICommandExecution) error {
	_, code, err := r.capture(ctx, []string{"start", execution.Name})
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil || code != 0 {
		return errors.New("hosted CLI container could not be started")
	}
	return nil
}

func (r *DockerContainerLifecycle) CollectOutput(ctx context.Context, execution operational.CLICommandExecution, output OutputFunc) error {
	code, err := r.Commands.Run(ctx, Process{
		Program: r.DockerBinary, Args: r.args("logs", "--follow", execution.Name), Env: r.HostEnv,
	}, output)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil || code != 0 {
		return errors.New("hosted CLI container output collection failed")
	}
	return nil
}
