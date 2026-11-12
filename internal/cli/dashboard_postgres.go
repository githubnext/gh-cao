package cli

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const dashboardPostgresImage = "docker.io/library/postgres:16-alpine"
const dashboardPostgresLabel = "io.githubnext.cao.dashboard.postgres"

type dashboardEngine struct {
	name string
	run  func(context.Context, ...string) ([]byte, error)
}

type dashboardPostgresState struct {
	Version         int    `json:"version"`
	Engine          string `json:"engine"`
	Owner           string `json:"owner"`
	ContainerID     string `json:"containerId"`
	RuntimePassword string `json:"runtimePassword"`
}

type dashboardPostgresContainer struct {
	ID     string `json:"Id"`
	Config struct {
		Image  string
		Labels map[string]string
	}
	State           struct{ Running bool }
	Mounts          []struct{ Type, Source, Destination string }
	NetworkSettings struct {
		Ports map[string][]struct {
			HostIP   string `json:"HostIp"`
			HostPort string
		}
	}
}

type dashboardEngineOutput struct{ bytes.Buffer }

func (output *dashboardEngineOutput) Write(value []byte) (int, error) {
	if output.Len()+len(value) > 1<<20 {
		return 0, errors.New("container engine output exceeded its 1 MiB budget")
	}
	return output.Buffer.Write(value)
}

func newDashboardEngine(name string) (dashboardEngine, error) {
	binary, err := exec.LookPath(name)
	if err != nil {
		return dashboardEngine{}, fmt.Errorf("%s is not installed", name)
	}
	return dashboardEngine{name: name, run: func(ctx context.Context, args ...string) ([]byte, error) {
		var output, diagnostic dashboardEngineOutput
		command := exec.CommandContext(ctx, binary, args...)
		command.Stdout, command.Stderr = &output, &diagnostic
		if err := command.Run(); err != nil {
			return nil, fmt.Errorf("%s %s failed: %w", name, args[0], err)
		}
		return output.Bytes(), nil
	}}, nil
}

func dashboardLocalEngineEndpoint(endpoint string) bool {
	if strings.HasPrefix(endpoint, "unix://") || strings.HasPrefix(endpoint, "npipe://") {
		return true
	}
	address, err := url.Parse(endpoint)
	return err == nil && net.ParseIP(address.Hostname()) != nil && net.ParseIP(address.Hostname()).IsLoopback()
}

func (engine dashboardEngine) verifyLocal(ctx context.Context) error {
	if engine.name == "docker" {
		endpoint := os.Getenv("DOCKER_HOST")
		if endpoint == "" || os.Getenv("DOCKER_CONTEXT") != "" {
			content, err := engine.run(ctx, "context", "inspect", "--format", "{{json .Endpoints.docker.Host}}")
			if err != nil {
				return err
			}
			if err := json.Unmarshal(bytes.TrimSpace(content), &endpoint); err != nil {
				return errors.New("docker did not identify its local endpoint")
			}
		}
		if !dashboardLocalEngineEndpoint(endpoint) {
			return errors.New("managed PostgreSQL requires a local Docker endpoint")
		}
	} else {
		if endpoint := os.Getenv("CONTAINER_HOST"); endpoint != "" && !dashboardLocalEngineEndpoint(endpoint) {
			return errors.New("managed PostgreSQL requires a local Podman endpoint")
		}
		content, err := engine.run(ctx, "system", "connection", "list", "--format", "json")
		if err != nil {
			return err
		}
		var connections []struct {
			Name    string
			Default bool
			URI     string
		}
		if err := json.Unmarshal(content, &connections); err != nil {
			return errors.New("podman did not identify its local connections")
		}
		selected := os.Getenv("CONTAINER_CONNECTION")
		if selected != "" && os.Getenv("CONTAINER_HOST") != "" {
			return errors.New("managed PostgreSQL requires an unambiguous local Podman connection")
		}
		found := selected == ""
		for _, connection := range connections {
			if (selected == "" && connection.Default) || (selected != "" && connection.Name == selected) {
				found = true
				if dashboardLocalEngineEndpoint(connection.URI) {
					continue
				}
				return errors.New("managed PostgreSQL cannot use a remote Podman connection")
			}
		}
		if !found {
			return errors.New("the selected Podman connection is unavailable")
		}
	}
	format := "{{.OSType}}"
	if engine.name == "podman" {
		format = "{{.Host.OS}}"
	}
	content, err := engine.run(ctx, "info", "--format", format)
	if err != nil {
		return err
	}
	if strings.TrimSpace(string(content)) != "linux" {
		return errors.New("managed PostgreSQL requires a local Linux container engine")
	}
	return nil
}

func validDashboardContainerEngine(name string) bool {
	return name == "docker" || name == "podman"
}

// resolveDashboardContainerEngine applies flag > CAO_CONTAINER_ENGINE > automatic selection.
func resolveDashboardContainerEngine(flag string, changed bool) (string, error) {
	if changed {
		if !validDashboardContainerEngine(flag) {
			return "", errors.New("--container-engine must be docker or podman")
		}
		return flag, nil
	}
	if selected, present := os.LookupEnv("CAO_CONTAINER_ENGINE"); present {
		if !validDashboardContainerEngine(selected) {
			return "", errors.New("CAO_CONTAINER_ENGINE must be docker or podman")
		}
		return selected, nil
	}
	return "", nil
}

func selectDashboardEngine(ctx context.Context, name string) (dashboardEngine, error) {
	if name != "" && !validDashboardContainerEngine(name) {
		return dashboardEngine{}, errors.New("container engine must be docker or podman")
	}
	names := []string{"docker", "podman"}
	if name != "" {
		names = []string{name}
	}
	var failures []error
	for _, candidate := range names {
		engine, err := newDashboardEngine(candidate)
		if err == nil {
			probe, cancel := context.WithTimeout(ctx, 10*time.Second)
			err = engine.verifyLocal(probe)
			cancel()
		}
		if err == nil {
			return engine, nil
		}
		failures = append(failures, err)
	}
	if name != "" {
		alternative := "podman"
		if name == "podman" {
			alternative = "docker"
		}
		return dashboardEngine{}, fmt.Errorf("%s is not usable for managed PostgreSQL; start it, pass --container-engine %s, or supply --postgres-url: %w", name, alternative, errors.Join(failures...))
	}
	return dashboardEngine{}, fmt.Errorf("no usable local container engine; start Docker/Podman or supply --postgres-url: %w", errors.Join(failures...))
}

func dashboardPostgresSecret() (string, error) {
	var value [32]byte
	_, err := rand.Read(value[:])
	return hex.EncodeToString(value[:]), err
}

func readDashboardPostgresState(filename string) (dashboardPostgresState, error) {
	var state dashboardPostgresState
	info, err := os.Lstat(filename)
	if err != nil {
		return state, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Size() > 4096 {
		return state, errors.New("managed PostgreSQL state must be a private regular file")
	}
	content, err := os.ReadFile(filename)
	if err != nil {
		return state, err
	}
	if json.Unmarshal(content, &state) != nil || state.Version != 1 ||
		len(state.Owner) != 64 || len(state.RuntimePassword) != 64 ||
		state.Engine != "docker" && state.Engine != "podman" {
		return state, errors.New("managed PostgreSQL state is invalid")
	}
	return state, nil
}

func writeDashboardPostgresState(filename string, state dashboardPostgresState) error {
	content, err := json.Marshal(state)
	if err != nil {
		return err
	}
	temporary := filename + ".tmp"
	file, err := os.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(temporary) }()
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(temporary, filename)
}

func (engine dashboardEngine) inspectPostgres(ctx context.Context, id string) (dashboardPostgresContainer, error) {
	var value dashboardPostgresContainer
	content, err := engine.run(ctx, "container", "inspect", id)
	if err != nil {
		return value, err
	}
	var containers []dashboardPostgresContainer
	if json.Unmarshal(content, &containers) != nil || len(containers) != 1 || containers[0].ID == "" {
		return value, errors.New("managed PostgreSQL container identity is unavailable")
	}
	return containers[0], nil
}

func validateDashboardPostgresOwnership(container dashboardPostgresContainer, state dashboardPostgresState, data string) error {
	if container.Config.Labels[dashboardPostgresLabel] != state.Owner || container.Config.Image != dashboardPostgresImage ||
		state.ContainerID != "" && container.ID != state.ContainerID {
		return errors.New("the PostgreSQL container is not owned by this dashboard cache")
	}
	found := false
	for _, mount := range container.Mounts {
		if mount.Destination == "/var/lib/postgresql/data" {
			found = mount.Type == "bind" && filepath.Clean(mount.Source) == filepath.Clean(data)
		}
	}
	if !found {
		return errors.New("managed PostgreSQL requires its private cache bind mount")
	}
	return nil
}

func validateDashboardPostgresContainer(container dashboardPostgresContainer, state dashboardPostgresState, data string) (string, error) {
	if err := validateDashboardPostgresOwnership(container, state, data); err != nil {
		return "", err
	}
	ports := container.NetworkSettings.Ports["5432/tcp"]
	if len(ports) != 1 || ports[0].HostIP != "127.0.0.1" {
		return "", errors.New("managed PostgreSQL requires a single loopback port")
	}
	port, err := strconv.ParseUint(ports[0].HostPort, 10, 16)
	if err != nil || port == 0 {
		return "", errors.New("managed PostgreSQL did not publish a valid local port")
	}
	return ports[0].HostPort, nil
}

func startDashboardPostgres(ctx context.Context, engine dashboardEngine, directory string) (string, string, error) {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", "", err
	}
	if info, err := os.Lstat(directory); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", "", errors.New("managed PostgreSQL requires a real private cache directory")
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return "", "", err
	}
	lock := filepath.Join(directory, "setup.lock")
	if err := os.Mkdir(lock, 0o700); err != nil {
		return "", "", errors.New("managed PostgreSQL setup is already locked; do not remove another process's lock")
	}
	defer func() { _ = os.Remove(lock) }()
	filename := filepath.Join(directory, "state.json")
	state, err := readDashboardPostgresState(filename)
	if errors.Is(err, os.ErrNotExist) {
		state = dashboardPostgresState{Version: 1, Engine: engine.name}
		state.Owner, err = dashboardPostgresSecret()
		if err == nil {
			state.RuntimePassword, err = dashboardPostgresSecret()
		}
		if err == nil {
			err = writeDashboardPostgresState(filename, state)
		}
	}
	if err != nil {
		return "", "", err
	}
	if state.Engine != engine.name {
		return "", "", errors.New("this dashboard cache belongs to another container engine; select it or use a different --cache-dir")
	}
	passwordFile := filepath.Join(directory, "password")
	if info, err := os.Lstat(passwordFile); err == nil && (!info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0) {
		return "", "", errors.New("managed PostgreSQL credentials must be a private regular file")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", "", err
	}
	adminPassword, err := os.ReadFile(passwordFile)
	if errors.Is(err, os.ErrNotExist) && state.ContainerID == "" {
		secret, secretErr := dashboardPostgresSecret()
		if secretErr != nil {
			return "", "", secretErr
		}
		adminPassword = []byte(secret)
		err = os.WriteFile(passwordFile, adminPassword, 0o600)
	}
	if err != nil || len(adminPassword) != 64 {
		return "", "", errors.New("the managed PostgreSQL administrator credential is unavailable")
	}
	data := filepath.Join(directory, "data")
	if err := os.MkdirAll(data, 0o700); err != nil {
		return "", "", err
	}
	if info, err := os.Lstat(data); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", "", errors.New("managed PostgreSQL data must be a real cache directory")
	}
	digest := sha256.Sum256([]byte(directory))
	name := fmt.Sprintf("gh-cao-postgres-%x", digest[:12])
	if state.ContainerID == "" {
		// Listing by exact name distinguishes absence from a daemon/permission failure.
		content, err := engine.run(ctx, "container", "ls", "--all", "--filter", "name=^"+name+"$", "--format", "{{.ID}}")
		if err != nil {
			return "", "", err
		}
		if strings.TrimSpace(string(content)) == "" {
			if _, err := engine.run(ctx, "image", "inspect", dashboardPostgresImage); err != nil {
				if _, err := engine.run(ctx, "pull", "--quiet", dashboardPostgresImage); err != nil {
					return "", "", err
				}
			}
			_, err = engine.run(ctx, "run", "--detach", "--name", name,
				"--label", dashboardPostgresLabel+"="+state.Owner,
				"--publish", "127.0.0.1::5432",
				"--volume", data+":/var/lib/postgresql/data:Z",
				"--volume", passwordFile+":/run/secrets/postgres:ro,Z",
				"--env", "POSTGRES_DB=cao_dashboard", "--env", "POSTGRES_USER=cao_bootstrap",
				"--env", "POSTGRES_PASSWORD_FILE=/run/secrets/postgres",
				dashboardPostgresImage, "postgres", "-c", "max_locks_per_transaction=512")
			if err != nil {
				return "", "", err
			}
		}
	}
	container, err := engine.inspectPostgres(ctx, name)
	if err != nil {
		return "", "", err
	}
	if err := validateDashboardPostgresOwnership(container, state, data); err != nil {
		return "", "", err
	}
	if !container.State.Running {
		if _, err := engine.run(ctx, "start", container.ID); err != nil {
			return "", "", err
		}
	}
	state.ContainerID = container.ID
	if err := writeDashboardPostgresState(filename, state); err != nil {
		return "", "", err
	}
	ready, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	var port string
	for {
		container, err = engine.inspectPostgres(ready, state.ContainerID)
		if err == nil {
			port, err = validateDashboardPostgresContainer(container, state, data)
		}
		if err != nil {
			return "", "", err
		}
		if _, err := engine.run(ready, "exec", state.ContainerID, "pg_isready", "-h", "127.0.0.1", "-U", "cao_bootstrap", "-d", "cao_dashboard"); err == nil {
			break
		}
		select {
		case <-ready.Done():
			return "", "", errors.New("managed PostgreSQL did not become ready before its deadline")
		case <-time.After(250 * time.Millisecond):
		}
	}
	address := &url.URL{Scheme: "postgres", Host: net.JoinHostPort("127.0.0.1", port), Path: "/cao_dashboard"}
	address.User = url.UserPassword("cao_bootstrap", string(adminPassword))
	address.RawQuery = "sslmode=disable"
	return address.String(), state.RuntimePassword, nil
}

func configureDashboardPostgres(ctx context.Context, server, cache, namespace, supplied, engineName string) (string, error) {
	if strings.TrimSpace(supplied) != "" {
		return supplied, nil
	}
	setup, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	engine, err := selectDashboardEngine(setup, engineName)
	if err != nil {
		return "", err
	}
	directory := filepath.Join(cache, "postgres")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", err
	}
	if info, err := os.Lstat(directory); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("managed PostgreSQL provisioning requires its own cache directory")
	}
	lock := filepath.Join(directory, "provision.lock")
	if err := os.Mkdir(lock, 0o700); err != nil {
		return "", errors.New("managed PostgreSQL provisioning is already in progress")
	}
	defer func() { _ = os.Remove(lock) }()
	admin, password, err := startDashboardPostgres(setup, engine, directory)
	if err != nil {
		return "", err
	}
	command := exec.CommandContext(setup, server, "database", "provision-local", "--namespace", namespace)
	command.Env = append(dashboardServerEnvironment(), "CAO_DATABASE_ADMIN_URL="+admin,
		"CAO_LOCAL_POSTGRES_RUNTIME_PASSWORD="+password)
	var diagnostic dashboardEngineOutput
	command.Stderr = &diagnostic
	if err := command.Run(); err != nil {
		return "", fmt.Errorf("cannot provision the managed PostgreSQL runtime: %w: %s", err, strings.TrimSpace(diagnostic.String()))
	}
	address, err := url.Parse(admin)
	if err != nil {
		return "", err
	}
	address.User = url.UserPassword("cao_dashboard_runtime", password)
	parameters := address.Query()
	parameters.Set("search_path", "cao")
	address.RawQuery = parameters.Encode()
	return address.String(), nil
}

func ensureDashboardCacheIgnored(cache string) error {
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(root, cache)
	if err != nil || relative == ".." || strings.HasPrefix(filepath.ToSlash(relative), "../") {
		return nil
	}
	if relative == "." {
		return errors.New("--cache-dir must be a dedicated directory, not the working directory")
	}
	pattern := "/" + strings.NewReplacer(`\`, `\\`, " ", `\ `, "[", `\[`, "]", `\]`,
		"*", `\*`, "?", `\?`, "#", `\#`, "!", `\!`).Replace(filepath.ToSlash(relative)) + "/"
	if strings.HasPrefix(filepath.ToSlash(relative), ".github/cao/dashboard/") {
		pattern = "/.github/cao/dashboard/"
	}
	filename := filepath.Join(root, ".gitignore")
	if info, err := os.Lstat(filename); err == nil && !info.Mode().IsRegular() {
		return errors.New("dashboard cache requires a regular .gitignore file")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	content, err := os.ReadFile(filename)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	for _, line := range strings.Split(string(content), "\n") {
		if line == pattern || line == strings.TrimPrefix(pattern, "/") {
			return nil
		}
	}
	file, err := os.OpenFile(filename, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	_, writeErr := file.WriteString("\n" + pattern + "\n")
	return errors.Join(writeErr, file.Close())
}
