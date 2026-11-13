package namespaces

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/githubnext/gh-cao/server/internal/logger"
)

var namespaceLog = logger.New("cao:namespace")

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
		return "", errors.New("namespace must contain 1-64 lowercase letters, digits, underscores, or hyphens")
	}
	return namespacePrefix + name, nil
}

func DefaultNamespace(workingDirectory string) (string, error) {
	directory, err := filepath.Abs(workingDirectory)
	if err != nil {
		return "", errors.New("resolve namespace working directory")
	}
	if resolved, resolveErr := filepath.EvalSymlinks(directory); resolveErr == nil {
		directory = resolved
	}
	root, source := checkoutRoot(directory)
	namespaceLog.Printf("resolved checkout root source=%s", source)
	sum := sha256.Sum256([]byte(root))
	return NormalizeNamespace("checkout-" + hex.EncodeToString(sum[:12]))
}

// Environment rejects removed selectors rather than silently opening a
// different dataset under the default namespace.
func Environment(lookup func(string) (string, bool)) (string, error) {
	for _, name := range []string{"REDIS_NAMESPACE", "CAO_REDIS_NAMESPACE"} {
		if value, _ := lookup(name); strings.TrimSpace(value) != "" {
			return "", fmt.Errorf("%s is no longer supported; use CAO_DATABASE_NAMESPACE and CAO_OPERATIONAL_NAMESPACE", name)
		}
	}
	value, _ := lookup("CAO_DATABASE_NAMESPACE")
	return strings.TrimSpace(value), nil
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
