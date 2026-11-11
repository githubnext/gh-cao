// Package dashboardassets owns the built dashboard resources used by the server.
package dashboardassets

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	prefix           = "embedded:"
	DashboardQueries = "src/agent/queries.generated.json"
	DatabaseQueries  = "src/data/queries/database.json"
	AgentCatalog     = "src/agent/catalog.generated.json"
	MCPContract      = "src/agent/mcp-contract.json"
)

//go:embed all:resources
var resources embed.FS

// Site returns the immutable dashboard built into this executable.
func Site() (fs.FS, error) {
	site, err := fs.Sub(resources, "resources/site")
	if err != nil {
		return nil, err
	}
	if _, err := fs.Stat(site, "index.html"); err != nil {
		return nil, fmt.Errorf("dashboard resources are not embedded; run npm run dashboard:server:build before go build, or select a built dashboard with --site: %w", err)
	}
	return site, nil
}

// Path selects embedded resources unless an explicit disk directory is supplied.
func Path(directory, name string) string {
	if directory == "" {
		return prefix + name
	}
	return filepath.Join(directory, filepath.FromSlash(name))
}

// ReadFile reads an explicit embedded resource or an operator-configured file.
// Disk errors never fall back to embedded resources.
func ReadFile(path string) ([]byte, error) {
	if name, ok := strings.CutPrefix(path, prefix); ok {
		site, err := Site()
		if err != nil {
			return nil, err
		}
		return fs.ReadFile(site, name)
	}
	// #nosec G304 -- disk paths are explicit operator configuration.
	return os.ReadFile(path)
}
