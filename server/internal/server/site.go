package server

import (
	"errors"
	"io/fs"
	"os"

	"github.com/githubnext/gh-cao/server/internal/dashboardassets"
)

func openSite(directory string) (fs.FS, func(), error) {
	if directory == "" {
		site, err := dashboardassets.Site()
		return site, func() {}, err
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, nil, err
	}
	return root.FS(), func() { _ = root.Close() }, nil
}

// ValidateSite checks the selected resources before startup or snapshot download.
func ValidateSite(directory string) error {
	site, closeSite, err := openSite(directory)
	if err != nil {
		return err
	}
	defer closeSite()
	content, err := fs.ReadFile(site, "index.html")
	if err != nil || len(content) == 0 {
		return errors.New("hosted site directory must contain a readable, non-empty built dashboard index.html")
	}
	return nil
}
