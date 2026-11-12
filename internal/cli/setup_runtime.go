package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"

	"github.com/githubnext/gh-cao/internal/ghcli"
)

var setupRuntimeFiles = []string{
	".github/workflows/cao-validate.yml",
	".github/workflows/shared/control.mjs",
	".github/workflows/shared/inventory-credentials.mjs",
	".github/workflows/shared/policy.mjs",
	".github/workflows/shared/authority.mjs",
	".github/workflows/shared/operations.mjs",
	".github/actions/setup-cao-runtime/action.yml",
	".github/actions/setup-gh-aw/action.yml",
}

func setupRuntimeComplete() (bool, error) {
	for _, name := range append(append([]string{}, setupRuntimeFiles...), nativeRuntimeRequiredFiles...) {
		filename, err := setupDestination(name)
		if err != nil {
			return false, err
		}
		info, err := os.Lstat(filename)
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if !info.Mode().IsRegular() {
			return false, fmt.Errorf("setup requires a regular runtime file: %s", name)
		}
	}
	return true, nil
}

func setupCheckoutRoot(ctx context.Context) error {
	child := exec.CommandContext(ctx, "git", "rev-parse", "--show-prefix")
	output, err := child.Output()
	if err != nil || strings.TrimSpace(string(output)) != "" {
		return errors.New("run gh cao setup from the root of an existing GitHub repository checkout; use gh repo create or gh repo clone explicitly")
	}
	return nil
}

func setupRuntime(cmd *cobra.Command, gh ghExecutor) (files []string, err error) {
	if err := nativeSetupNote(cmd, "Checking CAO runtime...\n"); err != nil {
		return nil, err
	}
	complete, err := setupRuntimeComplete()
	if err != nil || complete {
		return nil, err
	}
	if err := nativeSetupNote(cmd, "Fetching CAO release...\n"); err != nil {
		return nil, err
	}
	tag, err := nativeSetupGH(cmd, gh, "release", "view", "--repo", "github.com/"+catalogRepository, "--json", "tagName", "--jq", ".tagName")
	if err != nil {
		return nil, err
	}
	if !regexpStableVersion.MatchString(tag) {
		return nil, fmt.Errorf("latest CAO release must have a stable v-prefixed version: %s", tag)
	}
	revision, err := nativeSetupGH(cmd, gh, "api", "--hostname", "github.com", "repos/"+catalogRepository+"/commits/"+tag, "--jq", ".sha")
	if err != nil {
		return nil, err
	}
	if !commitPattern.MatchString(revision) {
		return nil, errors.New("CAO release did not resolve to a full commit SHA")
	}
	manifest, err := nativeSetupGH(cmd, gh, "api", "--hostname", "github.com", "repos/"+catalogRepository+"/contents/aw.yml?ref="+revision, "-H", "Accept: application/vnd.github.raw+json")
	if err != nil {
		return nil, err
	}
	match := regexp.MustCompile(`(?m)^min-version: (v[0-9]+\.[0-9]+\.[0-9]+)\s*$`).FindStringSubmatch(manifest)
	if len(match) != 2 {
		return nil, errors.New("CAO release manifest has no valid minimum gh-aw version")
	}
	required := match[1]
	if err := nativeSetupNote(cmd, "Checking gh-aw...\n"); err != nil {
		return nil, err
	}
	current, versionErr := nativeVersion(cmd.Context(), gh)
	if versionErr != nil {
		extensions, err := nativeSetupGH(cmd, gh, "extension", "list")
		if err != nil {
			return nil, err
		}
		if strings.Contains(extensions, "gh-aw") {
			return nil, versionErr
		}
		if err := nativeSetupNote(cmd, "Installing gh-aw %s...\n", required); err != nil {
			return nil, err
		}
		if _, err := nativeSetupGH(cmd, gh, "extension", "install", "github/gh-aw", "--pin", required); err != nil {
			return nil, err
		}
		current, err = nativeVersion(cmd.Context(), gh)
		if err != nil {
			return nil, err
		}
	}
	if nativeCompareVersions(current, required) < 0 {
		return nil, fmt.Errorf("installed gh-aw %s is older than required %s; upgrade gh-aw explicitly, then rerun gh cao setup", current, required)
	}
	staging, err := os.MkdirTemp(".", ".cao-setup-")
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(staging)) }()
	staging, err = filepath.Abs(staging)
	if err != nil {
		return nil, err
	}
	packages := filepath.Join(staging, "checkout")
	if output, err := exec.CommandContext(cmd.Context(), "git", "init", "--quiet", packages).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("initialize isolated CAO installation: %s: %w", output, err)
	}
	// gh-aw owns package records and project settings, but operates only in staging.
	if err := nativeSetupNote(cmd, "Preparing CAO workflows...\n"); err != nil {
		return nil, err
	}
	if _, stderr, err := gh(ghcli.WithDirectory(cmd.Context(), packages), "aw", "add", catalogRepository+"@"+tag, "--no-security-scanner", "--no-gitattributes"); err != nil {
		return nil, commandError("unable to prepare CAO root package", stderr, err)
	}
	records, err := nativeInstalledPackagesIn(packages)
	if err != nil {
		return nil, err
	}
	var record *nativePackageRecord
	for i := range records {
		if records[i].name == catalogRepository {
			record = &records[i]
		}
	}
	if record == nil || !strings.EqualFold(record.revision, revision) {
		return nil, errors.New("CAO root package record does not match the selected release revision")
	}
	if err := nativeSetupNote(cmd, "Downloading and verifying CAO runtime...\n"); err != nil {
		return nil, err
	}
	runtimeRoot, err := nativeStageRuntime(cmd.Context(), gh, *record, staging)
	if err != nil {
		return nil, err
	}
	for _, resource := range nativeRuntimeResources {
		if _, err := setupCopyMissing(filepath.Join(runtimeRoot, resource), filepath.Join(packages, resource), false); err != nil {
			return nil, err
		}
	}
	for _, name := range append(append([]string{}, setupRuntimeFiles...), nativeRuntimeRequiredFiles...) {
		info, err := os.Stat(filepath.Join(packages, name))
		if err != nil || !info.Mode().IsRegular() {
			return nil, fmt.Errorf("prepared CAO root package is incomplete: %s", name)
		}
	}
	// Preflight every destination before creating any local runtime files.
	if err := nativeSetupNote(cmd, "Adding CAO runtime files...\n"); err != nil {
		return nil, err
	}
	for _, apply := range []bool{false, true} {
		for _, resource := range []string{
			"activity", "dashboard", ".github/actions", ".github/workflows",
			".github/aw", ".github/skills", ".github/agents", ".github/cao/instructions.md",
		} {
			source := filepath.Join(packages, resource)
			if _, err := os.Lstat(source); errors.Is(err, os.ErrNotExist) {
				continue
			} else if err != nil {
				return nil, err
			}
			prepared, err := setupCopyMissing(source, resource, !apply)
			if err != nil {
				return nil, err
			}
			if !apply {
				files = append(files, prepared...)
			}
		}
	}
	return files, nil
}

func setupCopyMissing(source, destination string, dryRun bool) ([]string, error) {
	var files []string
	err := filepath.WalkDir(source, func(filename string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, filename)
		if err != nil {
			return err
		}
		target, err := setupDestination(filepath.Join(destination, relative))
		if err != nil {
			return err
		}
		info, err := os.Lstat(target)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if info != nil && (info.Mode()&os.ModeSymlink != 0 || info.IsDir() != entry.IsDir()) {
			return fmt.Errorf("setup refuses to replace existing path: %s", target)
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return fmt.Errorf("setup refuses non-regular runtime source: %s", filename)
		}
		files = append(files, filepath.ToSlash(filepath.Join(destination, relative)))
		if info != nil {
			existing, err := os.ReadFile(target)
			if err != nil {
				return err
			}
			incoming, err := os.ReadFile(filename)
			if err != nil {
				return err
			}
			if !bytes.Equal(existing, incoming) {
				return fmt.Errorf("setup refuses to overwrite existing file: %s; review the conflict before retrying (use gh cao update for installed runtime upgrades)", target)
			}
			return nil
		}
		if dryRun {
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		return nativeCopyTree(filename, target)
	})
	return files, err
}

func setupDestination(name string) (string, error) {
	filename, err := repositoryPath(name)
	if err != nil {
		return "", err
	}
	root, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for current := filename; current != root; current = filepath.Dir(current) {
		info, err := os.Lstat(current)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		if info != nil && info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("setup refuses a symlink destination: %s", current)
		}
	}
	return filename, nil
}
