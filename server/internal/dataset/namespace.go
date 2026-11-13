// Package dataset owns provider-independent canonical dataset identity.
package dataset

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/githubnext/gh-cao/server/internal/logger"
)

var namespaceLog = logger.New("cao:dataset:namespace")

const namespacePrefix = "cao:"

var namespaceName = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

// checkoutRootSource identifies how checkoutRoot resolved its result. It is
// useful for diagnosing an unexpected default namespace without logging the
// checkout path itself.
type checkoutRootSource string

const (
	checkoutRootSourceGitMarker checkoutRootSource = "git-marker"
	checkoutRootSourceFallback  checkoutRootSource = "fallback"
)

func NormalizeNamespace(value string) (string, error) {
	name := strings.ToLower(strings.TrimSpace(value))
	name = strings.TrimPrefix(name, namespacePrefix)
	if !namespaceName.MatchString(name) {
		return "", errors.New("dataset namespace must contain 1-64 lowercase letters, digits, underscores, or hyphens")
	}
	return namespacePrefix + name, nil
}

func DefaultNamespace(workingDirectory string) (string, error) {
	directory, err := filepath.Abs(workingDirectory)
	if err != nil {
		return "", errors.New("resolve dataset namespace working directory")
	}
	if resolved, resolveErr := filepath.EvalSymlinks(directory); resolveErr == nil {
		directory = resolved
	}
	root, source := checkoutRoot(directory)
	namespaceLog.Printf("resolved checkout root source=%s", source)
	sum := sha256.Sum256([]byte(root))
	return NormalizeNamespace("checkout-" + hex.EncodeToString(sum[:12]))
}

// checkoutRoot walks up from directory looking for a ".git" marker. It
// returns the marker's directory and checkoutRootSourceGitMarker when found,
// or the original directory and checkoutRootSourceFallback when no marker is
// found before reaching the filesystem root.
func checkoutRoot(directory string) (string, checkoutRootSource) {
	for candidate := directory; ; candidate = filepath.Dir(candidate) {
		if _, err := os.Stat(filepath.Join(candidate, ".git")); err == nil {
			return candidate, checkoutRootSourceGitMarker
		}
		parent := filepath.Dir(candidate)
		if parent == candidate {
			return directory, checkoutRootSourceFallback
		}
	}
}
