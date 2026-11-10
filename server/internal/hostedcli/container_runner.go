package hostedcli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/githubnext/gh-cao/server/internal/operational"
)

const DefaultContainerImage = "ghcr.io/github/gh-aw@sha256:7ff77dc03182d4edcebc902fa94b0239eb9cde65ea212509aef700bcab354090"

var containerImagePattern = regexp.MustCompile(`^[a-z0-9.-]+(?::[0-9]+)?/[a-z0-9._/-]+@sha256:[a-f0-9]{64}$`)

// ContainerRunner owns registration, execution, and bounded reconciliation.
// Runtime-specific process management belongs to its lifecycle adapter.
type ContainerRunner struct {
	Image     string
	Lifecycle ContainerLifecycle
}

var _ RecoverableRunner = (*ContainerRunner)(nil)

func NewContainerRunner(image string, lifecycle ContainerLifecycle) (*ContainerRunner, error) {
	if image == "" {
		image = DefaultContainerImage
	}
	if !containerImagePattern.MatchString(image) {
		return nil, errors.New("hosted CLI container image must be a digest-pinned OCI image reference")
	}
	if lifecycle == nil {
		return nil, errors.New("hosted CLI requires a container lifecycle")
	}
	if validator, ok := lifecycle.(interface{ validateImage(string) error }); ok {
		if err := validator.validateImage(image); err != nil {
			return nil, err
		}
	}
	return &ContainerRunner{Image: image, Lifecycle: lifecycle}, nil
}

func (r *ContainerRunner) Run(ctx context.Context, process Process, output OutputFunc) (int, error) {
	if process.Program != "gh" && process.Program != "gh-aw" {
		return -1, errors.New("hosted CLI container rejected an unsupported executable")
	}
	if process.Stdin != "" {
		return -1, errors.New("hosted CLI containers do not support interactive input")
	}
	if process.RegisterContainer == nil {
		return -1, errors.New("hosted CLI requires durable container identity registration")
	}
	runtimeID, err := r.Lifecycle.RuntimeID(ctx)
	if err != nil {
		return -1, err
	}
	name, err := containerName()
	if err != nil {
		return -1, err
	}
	execution := operational.CLICommandExecution{CommandID: process.CommandID, Name: name, RuntimeID: runtimeID}
	if err := execution.Validate(); err != nil {
		return -1, errors.New("hosted CLI container identity is invalid")
	}
	if err := process.RegisterContainer(ctx, execution); err != nil {
		return -1, errors.New("hosted CLI container identity could not be persisted")
	}
	if err := ctx.Err(); err != nil {
		return -1, err
	}
	spec := ContainerSpec{
		Image: r.Image, Workspace: process.Workspace, Directory: process.Dir,
		Program: process.Program, Args: process.Args, Env: process.Env,
	}
	if err := r.Lifecycle.Create(ctx, execution, spec); err != nil {
		return -1, errors.Join(err, r.Reconcile(ctx, execution))
	}
	runErr := r.Lifecycle.Start(ctx, execution)
	if runErr == nil {
		runErr = r.Lifecycle.CollectOutput(ctx, execution, output)
	}
	return r.finishContainer(ctx, execution, runErr)
}

// Reconcile stops a recorded container on its original runtime. The caller must
// first ensure the old executor and outstanding runtime requests have stopped.
func (r *ContainerRunner) Reconcile(ctx context.Context, execution operational.CLICommandExecution) error {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := execution.Validate(); err != nil {
		return fmt.Errorf("%w: invalid recovery identity", ErrContainerTerminationUnconfirmed)
	}
	if err := r.sameRuntime(cleanupCtx, execution); err != nil {
		return err
	}
	if err := r.Lifecycle.Stop(cleanupCtx, execution); err != nil {
		return fmt.Errorf("%w: stop could not be confirmed", ErrContainerTerminationUnconfirmed)
	}
	return nil
}

func (r *ContainerRunner) sameRuntime(ctx context.Context, execution operational.CLICommandExecution) error {
	id, err := r.Lifecycle.RuntimeID(ctx)
	if err != nil || id != execution.RuntimeID {
		return fmt.Errorf("%w: original container runtime is unavailable", ErrContainerTerminationUnconfirmed)
	}
	return nil
}

func (r *ContainerRunner) finishContainer(ctx context.Context, execution operational.CLICommandExecution, runErr error) (int, error) {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := r.sameRuntime(cleanupCtx, execution); err != nil {
		return -1, err
	}
	state, inspectErr := r.Lifecycle.Inspect(cleanupCtx, execution)
	valid := inspectErr == nil && (state.ExitCode == nil || (state.Terminated && *state.ExitCode >= 0 && *state.ExitCode <= 255))
	stopErr := r.Lifecycle.Stop(cleanupCtx, execution)
	if !valid || !state.Terminated {
		if stopErr != nil {
			return -1, ErrContainerTerminationUnconfirmed
		}
		return -1, errors.Join(runErr, errors.New("hosted CLI container ended without a confirmed workload result"))
	}
	if stopErr != nil {
		runErr = errors.Join(runErr, errors.New("hosted CLI container exited but could not be removed"))
	}
	if state.ExitCode == nil {
		return -1, errors.Join(runErr, errors.New("hosted CLI container exited without a workload exit code"))
	}
	return *state.ExitCode, runErr
}

func containerName() (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", errors.New("hosted CLI container name could not be generated")
	}
	return "cao-hosted-cli-" + hex.EncodeToString(random), nil
}
