package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

func nativeDashboardExecutable() (string, func(), error) {
	if filename := os.Getenv("CAO_DASHBOARD_EXECUTABLE"); filename != "" {
		absolute, err := filepath.Abs(filename)
		return absolute, func() {}, err
	}
	if filename, err := exec.LookPath("cao-dashboard"); err == nil {
		return filename, func() {}, nil
	}
	server, _, cleanup, err := dashboardServerAssets(dashboardOptions{})
	if err != nil {
		return "", nil, fmt.Errorf("canonical PostgreSQL access requires the bundled cao-dashboard executable or CAO_DASHBOARD_EXECUTABLE: %w", err)
	}
	return server, cleanup, nil
}
