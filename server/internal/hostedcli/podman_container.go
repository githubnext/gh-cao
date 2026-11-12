package hostedcli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/githubnext/gh-cao/server/internal/operational"
)

const podmanNamespace = "cao-hosted-cli"

// PodmanContainerLifecycle is the local Linux rootless container adapter. With
// egress it runs network-none workloads whose only route is the verified relay;
// without egress it uses bridge networking for explicit local development only.
type PodmanContainerLifecycle struct {
	engine        *ociContainerLifecycle
	mu            sync.RWMutex
	currentID     string
	currentEngine *ociContainerLifecycle
	created       map[string]podmanCreatedWorkspace
}

var _ ContainerLifecycle = (*PodmanContainerLifecycle)(nil)

// NewPodmanContainerLifecycle selects the local rootless engine. A nil egress
// leaves workloads on bridge networking; the server permits that only for
// explicit local development.
func NewPodmanContainerLifecycle(binary string, commands Runner, egress *ContainerEgress) (*PodmanContainerLifecycle, error) {
	if runtime.GOOS != "linux" {
		return nil, errors.New("hosted CLI Podman requires a local Linux rootless engine")
	}
	for _, name := range []string{
		"CONTAINER_HOST", "CONTAINER_CONNECTION", "CONTAINER_SSHKEY", "PODMAN_CONNECTIONS_CONF",
		"CONTAINERS_CONF", "CONTAINERS_CONF_OVERRIDE", "CONTAINERS_CONF_MODULES",
		"CONTAINERS_STORAGE_CONF",
	} {
		if strings.TrimSpace(os.Getenv(name)) != "" {
			return nil, errors.New("hosted CLI Podman rejects remote or overridden engine configuration")
		}
	}
	if binary == "" {
		binary = "podman"
	}
	if commands == nil {
		commands = ExecRunner{}
	}
	return &PodmanContainerLifecycle{engine: &ociContainerLifecycle{
		binary: binary, commands: commands,
		hostEnv: podmanHostEnvironment(os.Environ()),
		egress:  egress, relays: &egressRelays{},
		commandPrefix: []string{"--remote=false", "--db-backend=sqlite",
			"--default-mounts-file=/dev/null"},
		createOptions: []string{"--pull=never", "--userns=keep-id", "--image-volume=ignore",
			"--http-proxy=false", "--env-host=false", "--privileged=false", "--cgroups=enabled", "--read-only-tmpfs=false"},
		responseLimit:         32768,
		privateWorkspaceLabel: true,
	}}, nil
}

func podmanHostEnvironment(environment []string) []string {
	allowed := map[string]bool{
		"PATH": true, "HOME": true, "XDG_RUNTIME_DIR": true,
		"XDG_CONFIG_HOME": true, "XDG_DATA_HOME": true,
	}
	var result []string
	for _, entry := range environment {
		name, _, ok := strings.Cut(entry, "=")
		if ok && allowed[name] {
			result = append(result, entry)
		}
	}
	return result
}

func (r *PodmanContainerLifecycle) RuntimeID(ctx context.Context) (string, error) {
	value, code, err := r.engine.capture(ctx, []string{"info", "--format=json"})
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	var info struct {
		Host struct {
			ServiceIsRemote   *bool
			Security          struct{ Rootless *bool }
			CgroupVersion     string
			CgroupControllers []string
		}
		Store struct {
			GraphRoot       string
			RunRoot         string
			GraphDriverName string
		}
	}
	if err != nil || code != 0 || json.Unmarshal([]byte(value), &info) != nil ||
		info.Host.ServiceIsRemote == nil || *info.Host.ServiceIsRemote ||
		info.Host.Security.Rootless == nil || !*info.Host.Security.Rootless ||
		info.Host.CgroupVersion != "v2" {
		return "", errors.New("hosted CLI Podman requires local rootless Linux with cgroup v2")
	}
	controllers := make(map[string]bool)
	for _, controller := range info.Host.CgroupControllers {
		controllers[controller] = true
	}
	if !controllers["memory"] || !controllers["pids"] {
		return "", errors.New("hosted CLI Podman requires delegated memory and pids controllers")
	}
	if info.Store.GraphDriverName != "overlay" && info.Store.GraphDriverName != "vfs" {
		return "", errors.New("hosted CLI Podman requires a supported rootless storage driver")
	}
	roots := []string{info.Store.GraphRoot, info.Store.RunRoot}
	for i, path := range roots {
		if !filepath.IsAbs(path) {
			return "", errors.New("hosted CLI Podman storage identity is unavailable")
		}
		roots[i], err = filepath.EvalSymlinks(path)
		if err != nil {
			return "", errors.New("hosted CLI Podman storage identity is unavailable")
		}
	}
	seed, err := containerRuntimeSeed(roots[0])
	if err != nil {
		return "", err
	}
	host, err := os.Hostname()
	if err != nil || host == "" {
		return "", errors.New("hosted CLI Podman host identity is unavailable")
	}
	staticDirectory := filepath.Join(roots[0], "cao-hosted-cli", "libpod")
	if err := os.Mkdir(staticDirectory, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return "", errors.New("hosted CLI Podman execution namespace could not be created")
	}
	stateInfo, err := os.Lstat(staticDirectory)
	if err != nil || !stateInfo.IsDir() || stateInfo.Mode().Perm()&0o077 != 0 {
		return "", errors.New("hosted CLI Podman execution namespace is not private")
	}
	namespaceSeed, err := containerRuntimeSeed(staticDirectory)
	if err != nil {
		return "", err
	}
	configFile, err := podmanEngineConfiguration(roots[0], roots[1])
	if err != nil {
		return "", err
	}
	// Pin the execution database as well as storage. A containers.conf
	// static_dir change must not turn a foreign empty inventory into absence.
	engine := &ociContainerLifecycle{
		binary: r.engine.binary, commands: r.engine.commands,
		hostEnv: append(append([]string{}, r.engine.hostEnv...), "CONTAINERS_CONF="+configFile),
		egress:  r.engine.egress, relays: r.engine.relays,
		commandPrefix: append(r.engine.args(), "--root="+roots[0], "--runroot="+roots[1],
			"--storage-driver="+info.Store.GraphDriverName),
		createOptions: r.engine.createOptions, responseLimit: r.engine.responseLimit,
		privateWorkspaceLabel: r.engine.privateWorkspaceLabel,
	}
	backend, code, err := engine.capture(ctx, []string{"info", "--format={{.Host.DatabaseBackend}}"})
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil || code != 0 || strings.TrimSpace(backend) != "sqlite" {
		return "", errors.New("hosted CLI Podman owned execution database is unavailable")
	}
	storageIdentity, err := podmanStorageIdentity(roots[0], roots[1], staticDirectory, filepath.Join(staticDirectory, "db.sql"))
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(strings.Join([]string{
		host, strconv.Itoa(os.Getuid()), roots[0], roots[1], podmanNamespace, seed,
		staticDirectory, namespaceSeed, storageIdentity, "sqlite", info.Store.GraphDriverName,
	}, "\x00")))
	id := "podman:" + hex.EncodeToString(digest[:])
	r.mu.Lock()
	r.currentID, r.currentEngine = id, engine
	r.mu.Unlock()
	return id, nil
}

func podmanEngineConfiguration(graphRoot, runRoot string) (string, error) {
	directory := filepath.Join(graphRoot, "cao-hosted-cli")
	staticDirectory, err := json.Marshal(filepath.Join(directory, "libpod"))
	if err != nil {
		return "", err
	}
	temporaryDirectory, err := json.Marshal(filepath.Join(runRoot, "cao-hosted-cli"))
	if err != nil {
		return "", err
	}
	// JSON string quoting is also valid for these TOML basic strings.
	config := "[containers]\nenv = []\nenv_host = false\nhttp_proxy = false\nmounts = []\nvolumes = []\n" +
		"[engine]\nenv = []\nhooks_dir = []\nstatic_dir = " + string(staticDirectory) +
		"\ntmp_dir = " + string(temporaryDirectory) + "\n"
	root, err := os.OpenRoot(directory)
	if err != nil {
		return "", errors.New("hosted CLI Podman private engine configuration is unavailable")
	}
	defer func() { _ = root.Close() }()
	file, err := root.OpenFile("engine.conf", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err == nil {
		_, writeErr := file.WriteString(config)
		syncErr := file.Sync()
		closeErr := file.Close()
		if writeErr != nil || syncErr != nil || closeErr != nil {
			return "", errors.New("hosted CLI Podman private engine configuration could not be persisted")
		}
	} else if !errors.Is(err, os.ErrExist) {
		return "", errors.New("hosted CLI Podman private engine configuration is unavailable")
	}
	existing, err := appleReadPrivateFile(directory, "engine.conf", 4096)
	if err != nil || string(existing) != config {
		return "", errors.New("hosted CLI Podman private engine configuration changed")
	}
	return filepath.Join(directory, "engine.conf"), nil
}

func (r *PodmanContainerLifecycle) executionEngine(execution operational.CLICommandExecution) (*ociContainerLifecycle, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if execution.RuntimeID == "" || execution.RuntimeID != r.currentID || r.currentEngine == nil {
		return nil, errors.New("hosted CLI Podman original runtime namespace is unavailable")
	}
	return r.currentEngine, nil
}

func (r *PodmanContainerLifecycle) Create(ctx context.Context, execution operational.CLICommandExecution, spec ContainerSpec) error {
	if err := r.validateImage(spec.Image); err != nil {
		return err
	}
	workspace, workdir, err := containerPaths(spec.Workspace, spec.Directory)
	if err != nil {
		return err
	}
	engine, err := r.executionEngine(execution)
	if err != nil {
		return err
	}
	err = engine.Create(ctx, execution, spec)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return errors.Join(errors.New("hosted CLI Podman creation failed; verify the local image, rootless limits and private workspace SELinux relabel permissions"), err)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.currentID != execution.RuntimeID {
		return errors.New("hosted CLI Podman runtime changed during creation")
	}
	if r.created == nil {
		r.created = make(map[string]podmanCreatedWorkspace)
	}
	r.created[execution.Name] = podmanCreatedWorkspace{
		runtimeID: execution.RuntimeID, path: workspace, workdir: workdir, user: containerUser(),
		egress: engine.egress != nil,
	}
	return nil
}

func (r *PodmanContainerLifecycle) validateImage(image string) error {
	registry, _, _ := strings.Cut(image, "/")
	if !containerImagePattern.MatchString(image) ||
		(registry != "localhost" && !strings.ContainsAny(registry, ".:")) {
		return errors.New("hosted CLI Podman requires a fully qualified digest-pinned image already in local storage")
	}
	return nil
}

func (r *PodmanContainerLifecycle) Start(ctx context.Context, execution operational.CLICommandExecution) error {
	engine, err := r.executionEngine(execution)
	if err != nil {
		return err
	}
	r.mu.Lock()
	expected, ok := r.created[execution.Name]
	delete(r.created, execution.Name)
	r.mu.Unlock()
	if !ok || expected.runtimeID != execution.RuntimeID {
		return errors.New("hosted CLI Podman start requires this adapter's newly created workspace")
	}
	if err := validatePodmanIsolation(ctx, engine, execution, expected); err != nil {
		return err
	}
	return engine.Start(ctx, execution)
}

func (r *PodmanContainerLifecycle) Inspect(ctx context.Context, execution operational.CLICommandExecution) (ContainerState, error) {
	engine, err := r.executionEngine(execution)
	if err != nil {
		return ContainerState{}, err
	}
	return engine.Inspect(ctx, execution)
}

func (r *PodmanContainerLifecycle) Stop(ctx context.Context, execution operational.CLICommandExecution) error {
	// The relay set is shared by every derived engine, so egress is cut even
	// when the original runtime namespace can no longer be resolved.
	r.engine.stopEgress(execution.Name)
	engine, err := r.executionEngine(execution)
	if err != nil {
		return err
	}
	if err := engine.Stop(ctx, execution); err != nil {
		return err
	}
	r.forgetCreation(execution)
	return nil
}

func (r *PodmanContainerLifecycle) forgetCreation(execution operational.CLICommandExecution) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if expected, ok := r.created[execution.Name]; ok && expected.runtimeID == execution.RuntimeID {
		delete(r.created, execution.Name)
	}
}

func (r *PodmanContainerLifecycle) CollectOutput(ctx context.Context, execution operational.CLICommandExecution, output OutputFunc) error {
	engine, err := r.executionEngine(execution)
	if err != nil {
		return err
	}
	return engine.CollectOutput(ctx, execution, output)
}
