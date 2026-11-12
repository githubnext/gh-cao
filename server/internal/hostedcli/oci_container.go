package hostedcli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/githubnext/gh-cao/server/internal/cliegress"
	"github.com/githubnext/gh-cao/server/internal/operational"
)

// The pinned gh-aw update/upgrade --repo entrypoints require a Git root
// before cloning their target. Only this fixed bootstrap is interpreted;
// the validated program and arguments remain positional parameters.
const containerBootstrap = `git -c init.defaultBranch=main init --quiet --template= . && exec "$@"`

var (
	containerIDPattern = regexp.MustCompile(`^[a-f0-9]{12,64}$`)
	containerEnvName   = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

// ociContainerLifecycle holds the shared OCI command mechanics used by the
// Podman adapter. Its host mounts contain only the command's workspace and,
// when egress is enabled, the private egress channel. It has no runtime
// discovery of its own; the owning adapter pins the engine and identity.
type ociContainerLifecycle struct {
	binary        string
	hostEnv       []string
	commands      Runner
	egress        *ContainerEgress
	relays        *egressRelays
	commandPrefix []string
	createOptions []string
	responseLimit int
	// privateWorkspaceLabel requests a private SELinux relabel of host mounts.
	privateWorkspaceLabel bool
}

// egressRelays is shared by every engine derived from one adapter so cleanup
// reaches proxies started under an earlier runtime discovery.
type egressRelays struct {
	mu      sync.Mutex
	closers map[string]func()
}

func (r *ociContainerLifecycle) args(args ...string) []string {
	return append(append([]string{}, r.commandPrefix...), args...)
}

func (r *ociContainerLifecycle) Create(ctx context.Context, execution operational.CLICommandExecution, spec ContainerSpec) error {
	workspace, workdir, err := containerPaths(spec.Workspace, spec.Directory)
	if err != nil {
		return err
	}
	if strings.ContainsAny(workspace, ",\r\n") {
		return errors.New("hosted CLI workspace path cannot be mounted by the container runtime")
	}
	environmentFile, err := writeContainerEnvironment(spec.Env)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(environmentFile) }()
	network := "bridge" // Explicit local development, selected by the server.
	var egressDirectory string
	if r.egress != nil {
		network = "none"
		egressDirectory, err = r.prepareEgress(ctx, execution, workspace)
		if err != nil {
			return err
		}
	}
	workspaceMount := "type=bind,src=" + workspace + ",dst=/workspace,rw"
	label := ""
	if r.privateWorkspaceLabel {
		label = ",relabel=private"
	}
	workspaceMount += label
	args := r.args(
		"create", "--init", "--name", execution.Name,
		"--network", network,
		"--read-only",
		"--cap-drop", "ALL",
		"--security-opt", "no-new-privileges",
		"--pids-limit", "256",
		"--memory", "1g",
		"--tmpfs", "/tmp:rw,nosuid,nodev,noexec,size=64m",
		"--mount", workspaceMount,
		"--workdir", workdir,
		"--env-file", environmentFile,
	)
	args = append(args, r.createOptions...)
	if user := containerUser(); user != "" {
		args = append(args, "--user", user)
	}
	if r.egress != nil {
		// Hide the writable workspace alias of the same host directory. The
		// hiding tmpfs must not copy up the relay or socket beneath it.
		args = append(args, "--tmpfs", egressTmpfsDestination+":"+egressTmpfsOptions,
			"--mount", "type=bind,src="+egressDirectory+",dst=/cao-egress,readonly"+label,
			"--entrypoint", cliegress.RelayPath, spec.Image, "/bin/sh")
	} else {
		args = append(args, "--entrypoint", "/bin/sh", spec.Image)
	}
	args = append(args, "-c", containerBootstrap, "cao-hosted-cli", spec.Program)
	args = append(args, spec.Args...)

	var containerID strings.Builder
	createCode, createErr := r.commands.Run(ctx, Process{
		Program: r.binary, Args: args, Env: r.hostEnv,
	}, func(stream string, data []byte) {
		if stream == "stdout" && containerID.Len()+len(data) <= 256 {
			_, _ = containerID.Write(data)
		}
	})
	if err := os.Remove(environmentFile); err != nil && !errors.Is(err, os.ErrNotExist) {
		return errors.New("hosted CLI container environment could not be removed")
	}
	if createErr != nil || createCode != 0 || !containerIDPattern.MatchString(strings.TrimSpace(containerID.String())) {
		return errors.New("hosted CLI container could not be created")
	}
	return nil
}

func containerPaths(workspace, directory string) (string, string, error) {
	if workspace == "" || directory == "" {
		return "", "", errors.New("hosted CLI container requires a workspace and working directory")
	}
	workspace, err := filepath.Abs(workspace)
	if err != nil {
		return "", "", errors.New("hosted CLI workspace path is invalid")
	}
	directory, err = filepath.Abs(directory)
	if err != nil {
		return "", "", errors.New("hosted CLI working directory is invalid")
	}
	workspace, err = filepath.EvalSymlinks(workspace)
	if err != nil {
		return "", "", errors.New("hosted CLI workspace path is unavailable")
	}
	directory, err = filepath.EvalSymlinks(directory)
	if err != nil {
		return "", "", errors.New("hosted CLI working directory is unavailable")
	}
	relative, err := filepath.Rel(workspace, directory)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", "", errors.New("hosted CLI working directory must remain inside its workspace")
	}
	return workspace, filepath.ToSlash(filepath.Join("/workspace", relative)), nil
}

func writeContainerEnvironment(environment []string) (string, error) {
	file, err := os.CreateTemp("", "cao-hosted-cli-env-")
	if err != nil {
		return "", errors.New("hosted CLI container environment could not be prepared")
	}
	defer func() { _ = file.Close() }()
	if err := file.Chmod(0o600); err != nil {
		_ = os.Remove(file.Name())
		return "", errors.New("hosted CLI container environment could not be protected")
	}
	seen := make(map[string]bool, len(environment))
	for _, entry := range environment {
		name, value, ok := strings.Cut(entry, "=")
		if !ok || !containerEnvName.MatchString(name) || seen[name] || strings.ContainsAny(value, "\x00\r\n") {
			_ = os.Remove(file.Name())
			return "", errors.New("hosted CLI container environment is invalid")
		}
		seen[name] = true
		if _, err := fmt.Fprintf(file, "%s=%s\n", name, value); err != nil {
			_ = os.Remove(file.Name())
			return "", errors.New("hosted CLI container environment could not be written")
		}
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(file.Name())
		return "", errors.New("hosted CLI container environment could not be closed")
	}
	return file.Name(), nil
}

func containerUser() string {
	return hostContainerUser()
}
