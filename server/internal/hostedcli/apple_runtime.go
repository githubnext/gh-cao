package hostedcli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/githubnext/gh-cao/server/internal/operational"
)

func (r *AppleContainerLifecycle) RuntimeID(ctx context.Context) (string, error) {
	value, code, err := r.capture(ctx, r.Binary, []string{"system", "status", "--format", "json"})
	var status struct {
		Status  string `json:"status"`
		AppRoot string `json:"appRoot"`
	}
	if err != nil || code != 0 || json.Unmarshal([]byte(value), &status) != nil || status.Status != "running" || !filepath.IsAbs(status.AppRoot) {
		return "", errors.New("hosted CLI requires running Apple container services; start them with container system start")
	}
	appRoot, err := filepath.EvalSymlinks(status.AppRoot)
	if err != nil {
		return "", errors.New("hosted CLI native runtime data root is unavailable")
	}
	domain, code, err := r.capture(ctx, "/bin/launchctl", []string{"managername"})
	domain = strings.TrimSpace(domain)
	if err != nil || code != 0 || (domain != "Aqua" && domain != "Background") {
		return "", errors.New("hosted CLI requires a local user launchd session")
	}
	seed, err := containerRuntimeSeed(appRoot)
	if err != nil {
		return "", err
	}
	host, err := os.Hostname()
	if err != nil {
		return "", errors.New("hosted CLI native host identity is unavailable")
	}
	digest := sha256.Sum256([]byte(host + "\x00" + strconv.Itoa(os.Getuid()) + "\x00" + domain + "\x00" + appRoot + "\x00" + seed))
	return "apple:" + hex.EncodeToString(digest[:]), nil
}

func containerRuntimeSeed(appRoot string) (string, error) {
	directory := filepath.Join(appRoot, "cao-hosted-cli")
	if err := os.Mkdir(directory, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
		return "", errors.New("hosted CLI native runtime identity directory could not be created")
	}
	info, err := os.Lstat(directory)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return "", errors.New("hosted CLI native runtime identity directory is not private")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return "", errors.New("hosted CLI native runtime identity directory is unavailable")
	}
	defer func() { _ = root.Close() }()
	file, err := root.OpenFile("identity", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err == nil {
		name, randomErr := containerName()
		if randomErr == nil {
			_, randomErr = file.WriteString(strings.TrimPrefix(name, "cao-hosted-cli-"))
		}
		syncErr := file.Sync()
		closeErr := file.Close()
		if randomErr != nil || syncErr != nil || closeErr != nil {
			return "", errors.New("hosted CLI native runtime identity could not be persisted")
		}
	} else if !errors.Is(err, os.ErrExist) {
		return "", errors.New("hosted CLI native runtime identity is unavailable")
	}
	value, err := appleReadPrivateFile(directory, "identity", 32)
	if err != nil || len(value) != 32 {
		return "", errors.New("hosted CLI native runtime identity is invalid")
	}
	if _, err := hex.DecodeString(string(value)); err != nil {
		return "", errors.New("hosted CLI native runtime identity is invalid")
	}
	directoryFile, err := root.Open(".")
	if err != nil {
		return "", errors.New("hosted CLI native runtime identity directory is unavailable")
	}
	syncErr := directoryFile.Sync()
	closeErr := directoryFile.Close()
	if syncErr != nil || closeErr != nil {
		return "", errors.New("hosted CLI native runtime identity directory could not be persisted")
	}
	return string(value), nil
}

func (r *AppleContainerLifecycle) servicePresent(ctx context.Context, name string) (bool, error) {
	value, code, err := r.capture(ctx, "/bin/launchctl", []string{"list"})
	if err != nil || code != 0 {
		return false, errors.New("hosted CLI native VM service inventory is unavailable")
	}
	lines := strings.Split(strings.TrimSpace(value), "\n")
	if len(lines) == 0 || strings.Join(strings.Fields(lines[0]), " ") != "PID Status Label" {
		return false, errors.New("hosted CLI native VM service inventory is invalid")
	}
	label := "com.apple.container.container-runtime-linux." + name
	for _, line := range lines[1:] {
		fields := strings.Fields(line)
		if len(fields) != 3 {
			return false, errors.New("hosted CLI native VM service inventory is invalid")
		}
		if fields[2] == label {
			return true, nil
		}
	}
	return false, nil
}

func (r *AppleContainerLifecycle) stopped(ctx context.Context, execution operational.CLICommandExecution) (bool, error) {
	value, code, err := r.capture(ctx, r.Binary, []string{"inspect", execution.Name})
	var records []struct {
		ID     string `json:"id"`
		Status struct {
			State string `json:"state"`
		} `json:"status"`
	}
	if err != nil || code != 0 || json.Unmarshal([]byte(value), &records) != nil || len(records) != 1 || records[0].ID != execution.Name {
		return false, errors.New("hosted CLI native workload state is unavailable")
	}
	switch records[0].Status.State {
	case "running", "stopping", "unknown":
		return false, nil
	case "stopped":
		present, err := r.servicePresent(ctx, execution.Name)
		return !present && err == nil, err
	default:
		return false, errors.New("hosted CLI native workload state is invalid")
	}
}
