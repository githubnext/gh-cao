package cli

import (
	"io/fs"
	"os"
	"strings"

	"github.com/githubnext/gh-cao/internal/dashboardquery/workflowmetadata"
)

// Local engine evidence comes only from lockfiles in the matching checkout.
func dashboardCheckoutEngines(checkout string, workflows []dashboardWorkflow) map[string]string {
	root, err := os.OpenRoot(checkout)
	if err != nil {
		return nil
	}
	defer func() { _ = root.Close() }()
	engines := map[string]string{}
	for _, workflow := range workflows {
		path := workflow.Path
		name, valid := strings.CutPrefix(path, ".github/workflows/")
		if !valid || strings.ContainsAny(name, `/\`) || !fs.ValidPath(path) || !strings.HasSuffix(name, ".lock.yml") {
			continue
		}
		stat, err := root.Lstat(path)
		if err != nil || !stat.Mode().IsRegular() {
			continue
		}
		file, err := root.Open(path)
		if err != nil {
			continue
		}
		metadata := workflowmetadata.Parse(file)
		_ = file.Close()
		if metadata.Engine != "" {
			engines[path] = metadata.Engine
		}
	}
	return engines
}
