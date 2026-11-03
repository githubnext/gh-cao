package cli

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var regexpStableVersion = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+$`)

var nativeRuntimeResources = []string{
	"activity", "dashboard", ".github/actions/setup-cao-runtime", ".github/actions/setup-gh-aw",
	".github/cao/instructions.md", ".github/workflows/shared/activity-cache.md",
	".github/workflows/shared/control.md", ".github/workflows/shared/review-bundle.md",
	".github/workflows/shared/operation.md",
}

var nativeRuntimeRequiredFiles = []string{
	"activity/workflow-values.mjs", "activity/workflow-value-evidence.mjs", "activity/github-api-budget.mjs",
	"dashboard/site/src/data/model/operational-value-evidence.js",
	"activity/cao.mjs", "activity/cli/activity-stats.mjs",
	"activity/cli/cache.mjs", "activity/cli/dashboard.mjs",
	"activity/cli/download.mjs", "activity/cli/files.mjs",
	"activity/cli/ingestion.mjs", "activity/cli/issue-status.mjs", "activity/cli/jsonl.mjs",
	"activity/cli/options.mjs", "activity/cli/payloads.mjs",
	"activity/cli/queries.mjs",
	"activity/cli/run.mjs", "activity/cli-usage.mjs", "activity/normalized-phase.mjs",
	"activity/commands/index.mjs",
	"activity/control-settings.mjs", "activity/repository-visibility.mjs", "activity/recycle-snapshot.mjs",
	"activity/collect-logs.sh", "activity/collect-workflow-logs.mjs", "activity/marketplace.mjs",
	"activity/problem-clustering.mjs", "activity/repository-memory.mjs",
	"dashboard/site/package.json", "dashboard/report/aic-usage.mjs",
}

func nativeMaterializeRuntime(ctx context.Context, gh ghExecutor, campaign string) (err error) {
	records, err := nativeInstalledPackages()
	if err != nil {
		return err
	}
	type plan struct {
		record    nativePackageRecord
		resources []string
	}
	find := func(name string) *nativePackageRecord {
		for _, record := range records {
			if record.name == name {
				return &record
			}
		}
		return nil
	}
	root := find(catalogRepository + "/control-plane")
	if root == nil {
		root = find(catalogRepository)
	}
	plans := []plan{}
	if campaign == "root" {
		if root == nil {
			return nativeErrorf("No installed CAO root package record found")
		}
		resources := []string{}
		for _, resource := range nativeRuntimeResources {
			if (resource == "activity" || resource == "dashboard") && find(catalogRepository+"/"+resource) != nil {
				continue
			}
			resources = append(resources, resource)
		}
		plans = append(plans, plan{*root, resources})
		for _, focused := range []string{"activity", "dashboard"} {
			if record := find(catalogRepository + "/" + focused); record != nil {
				plans = append(plans, plan{*record, []string{focused}})
			}
		}
	} else {
		record := find(catalogRepository + "/" + campaign)
		if record == nil {
			record = root
		}
		if record == nil {
			return nativeErrorf("No installed CAO package record found for %s", campaign)
		}
		plans = append(plans, plan{*record, []string{campaign}})
	}
	staging, err := os.MkdirTemp(".", ".cao-runtime-")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(staging)) }()
	roots := map[string]string{}
	for _, plan := range plans {
		revision := strings.ToLower(plan.record.revision)
		if !commitPattern.MatchString(revision) {
			return nativeErrorf("CAO runtime requires a full revision SHA")
		}
		if roots[revision] != "" {
			continue
		}
		directory := filepath.Join(staging, revision)
		if err := os.Mkdir(directory, 0o755); err != nil {
			return err
		}
		root, err := nativeStageRuntime(ctx, gh, plan.record, directory)
		if err != nil {
			return err
		}
		roots[revision] = root
	}
	// Validate every selected source and destination before replacing resources.
	for _, plan := range plans {
		for _, resource := range plan.resources {
			if _, err := os.Stat(filepath.Join(roots[strings.ToLower(plan.record.revision)], filepath.FromSlash(resource))); err != nil {
				return nativeErrorf("CAO source revision is missing required resource %s: %v", resource, err)
			}
			if _, err := repositoryPath(resource); err != nil {
				return err
			}
		}
	}
	for _, plan := range plans {
		for _, resource := range plan.resources {
			destination, _ := repositoryPath(resource)
			if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
				return err
			}
			if err := os.RemoveAll(destination); err != nil {
				return err
			}
			if err := nativeCopyTree(filepath.Join(roots[strings.ToLower(plan.record.revision)], filepath.FromSlash(resource)), destination); err != nil {
				return err
			}
		}
	}
	if campaign == "root" {
		return nativeRemoveRetiredLauncher()
	}
	if campaign != "root" {
		metadata, err := repositoryPath(filepath.Join(".github", "cao", "intelligence", campaign+".json"))
		if err != nil {
			return err
		}
		if err := os.Remove(metadata); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		content, err := os.ReadFile(filepath.Join(campaign, "intelligence.json"))
		if err == nil {
			if err := os.MkdirAll(filepath.Dir(metadata), 0o755); err != nil {
				return err
			}
			return os.WriteFile(metadata, content, 0o644)
		}
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func nativeStageRuntime(ctx context.Context, gh ghExecutor, record nativePackageRecord, directory string) (string, error) {
	archive, err := nativeAcquireRuntime(ctx, gh, record, directory)
	if err != nil {
		return "", err
	}
	extracted := filepath.Join(directory, "extracted")
	if err := os.Mkdir(extracted, 0o755); err != nil {
		return "", err
	}
	root, err := nativeExtractRuntime(ctx, archive, extracted)
	if err != nil {
		return "", err
	}
	if err := nativeVerifyRuntime(root, strings.ToLower(record.revision)); err != nil {
		return "", err
	}
	return root, nil
}

func nativeAcquireRuntime(ctx context.Context, gh ghExecutor, record nativePackageRecord, directory string) (string, error) {
	revision := strings.ToLower(record.revision)
	name := "cao-runtime-" + revision
	run := func(arguments ...string) (string, error) {
		stdout, stderr, err := gh(ctx, arguments...)
		if err != nil {
			return "", commandError("Unable to acquire or verify prebuilt CAO runtime", stderr, err)
		}
		return stdout, nil
	}
	ref := record.source[strings.LastIndex(record.source, "@")+1:]
	if regexpStableVersion.MatchString(ref) {
		if _, err := run("release", "download", ref, "--repo", "github.com/"+catalogRepository, "--pattern", name+".tgz", "--dir", directory); err != nil {
			return "", err
		}
	} else {
		runID := int64(0)
		for attempt := 0; attempt <= 60; attempt++ {
			stdout, err := run("api", "--hostname", "github.com", "--paginate", "--slurp", "repos/"+catalogRepository+"/actions/workflows/cao-runtime.yml/runs?head_sha="+revision+"&per_page=100")
			if err != nil {
				return "", err
			}
			var pages []struct {
				Runs []struct {
					ID                        int64  `json:"id"`
					SHA                       string `json:"head_sha"`
					Event, Status, Conclusion string
				} `json:"workflow_runs"`
			}
			if err := json.Unmarshal([]byte(stdout), &pages); err != nil {
				return "", err
			}
			active, failed := false, ""
			for _, page := range pages {
				for _, candidate := range page.Runs {
					if candidate.SHA != revision || (candidate.Event != "push" && candidate.Event != "release") || candidate.ID < 1 {
						continue
					}
					if candidate.Status == "queued" || candidate.Status == "in_progress" || candidate.Status == "pending" || candidate.Status == "requested" || candidate.Status == "waiting" {
						active = true
					}
					if candidate.Status == "completed" && candidate.Conclusion != "" && candidate.Conclusion != "success" {
						failed = candidate.Conclusion
					}
					if candidate.Conclusion != "success" {
						continue
					}
					artifacts, err := run("api", "--hostname", "github.com", "--paginate", "--slurp", "repos/"+catalogRepository+"/actions/runs/"+strconv.FormatInt(candidate.ID, 10)+"/artifacts?per_page=100")
					if err != nil {
						return "", err
					}
					var artifactPages []struct {
						Artifacts []struct {
							Name    string
							Expired *bool
						}
					}
					if err := json.Unmarshal([]byte(artifacts), &artifactPages); err != nil {
						return "", err
					}
					for _, page := range artifactPages {
						for _, artifact := range page.Artifacts {
							if artifact.Name == name && artifact.Expired != nil && !*artifact.Expired {
								runID = candidate.ID
							}
						}
					}
					if runID != 0 {
						break
					}
				}
			}
			if runID != 0 {
				break
			}
			if !active {
				if failed != "" {
					return "", nativeErrorf("The CAO runtime build for %s completed with %s. Use a revision with a successful runtime build.", revision, failed)
				}
				return "", nativeErrorf("No successful, unexpired prebuilt CAO runtime is available for %s; source/npm fallback is disabled.", revision)
			}
			if attempt == 60 {
				return "", nativeErrorf("The CAO runtime build for %s is still running after 5 minutes.", revision)
			}
			select {
			case <-ctx.Done():
				return "", ctx.Err()
			case <-time.After(5 * time.Second):
			}
		}
		if _, err := run("run", "download", strconv.FormatInt(runID, 10), "--repo", "github.com/"+catalogRepository, "--name", name, "--dir", directory); err != nil {
			return "", err
		}
	}
	archive := filepath.Join(directory, name+".tgz")
	if _, err := os.Stat(archive); err != nil {
		return "", err
	}
	if _, err := run("attestation", "verify", archive, "--hostname", "github.com", "--repo", catalogRepository,
		"--signer-workflow", catalogRepository+"/.github/workflows/cao-runtime.yml",
		"--source-digest", revision, "--signer-digest", revision, "--deny-self-hosted-runners"); err != nil {
		return "", err
	}
	return archive, nil
}

func nativeExtractRuntime(ctx context.Context, filename, destination string) (rootPath string, err error) {
	file, err := os.Open(filename)
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	compressed, err := gzip.NewReader(file)
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, compressed.Close()) }()
	reader := tar.NewReader(compressed)
	root := ""
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			if _, err := io.Copy(io.Discard, compressed); err != nil {
				return "", err
			}
			if root == "" {
				return "", nativeErrorf("CAO runtime archive is empty")
			}
			return filepath.Join(destination, root), nil
		}
		if err != nil {
			return "", err
		}
		if header.Typeflag == tar.TypeXGlobalHeader {
			continue
		}
		name := strings.TrimRight(header.Name, "/")
		if name == "" || path.Clean(name) != name || !filepath.IsLocal(filepath.FromSlash(name)) || strings.ContainsAny(name, "\\:\x00") {
			return "", fmt.Errorf("unsafe CAO runtime archive path: %q", header.Name)
		}
		currentRoot, relative, _ := strings.Cut(name, "/")
		if root == "" {
			root = currentRoot
		}
		if currentRoot != root {
			return "", nativeErrorf("CAO archive must contain exactly one repository root")
		}
		if header.Typeflag == tar.TypeDir {
			continue
		}
		if header.Typeflag != tar.TypeReg || relative == "" {
			return "", fmt.Errorf("unsupported CAO runtime archive entry: %s", name)
		}
		allowed := relative == "package.json" || relative == "cao-runtime.json" || relative == "cao.sh"
		for _, resource := range nativeRuntimeResources {
			allowed = allowed || relative == resource || strings.HasPrefix(relative, resource+"/")
		}
		if !allowed {
			return "", nativeErrorf("CAO runtime artifact contains an unowned path: %s", relative)
		}
		if err := stageCampaignFile(destination, name, os.FileMode(header.Mode), reader); err != nil {
			return "", err
		}
	}
}

func nativeRemoveRetiredLauncher() error {
	info, err := os.Lstat("cao.sh")
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return nil
	}
	digest, err := nativeHashFile("cao.sh")
	if err != nil {
		return err
	}
	switch digest {
	case "c55be5e923b46a247eeee6d8c2f0926883f469e9577b4ac4b99d7171972e4c4d",
		"f186ab9735fb4d2a44333a81eac9ed18638ae2c4304af37ab351cc39ecf983c3":
		return os.Remove("cao.sh")
	}
	return nil
}

func nativeVerifyRuntime(root, revision string) error {
	content, err := os.ReadFile(filepath.Join(root, "cao-runtime.json"))
	if err != nil {
		return err
	}
	value, err := nativeDecodeJSON(content)
	if err != nil {
		return err
	}
	metadata, ok := value.(map[string]any)
	if !ok || nativeNumber(metadata["schemaVersion"]) != 1 || metadata["repository"] != catalogRepository || metadata["revision"] != revision {
		return nativeErrorf("CAO runtime artifact provenance does not match %s@%s", catalogRepository, revision)
	}
	files, ok := metadata["files"].(map[string]any)
	if !ok {
		return nativeErrorf("CAO runtime artifact has no valid file inventory")
	}
	found := 0
	err = filepath.WalkDir(root, func(filename string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return nativeErrorf("CAO runtime contains a non-regular file: %s", filename)
		}
		relative, err := filepath.Rel(root, filename)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if relative == "package.json" || relative == "cao-runtime.json" {
			return nil
		}
		hash, err := nativeHashFile(filename)
		if err != nil {
			return err
		}
		if files[relative] != hash {
			return nativeErrorf("CAO runtime artifact checksum mismatch: %s", relative)
		}
		found++
		return nil
	})
	if err != nil {
		return err
	}
	if found != len(files) {
		return nativeErrorf("CAO runtime artifact file inventory does not match its contents")
	}
	for _, resource := range nativeRuntimeResources {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(resource))); err != nil {
			return nativeErrorf("CAO source revision is missing required resource: %s", resource)
		}
	}
	for _, resource := range nativeRuntimeRequiredFiles {
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(resource)))
		if err != nil || !info.Mode().IsRegular() {
			return nativeErrorf("CAO runtime is incomplete at its canonical source paths: %s", resource)
		}
	}
	return nil
}

func nativeCopyTree(source, destination string) (err error) {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if info.IsDir() {
		if err := os.MkdirAll(destination, 0o755); err != nil {
			return err
		}
		entries, err := os.ReadDir(source)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			if err := nativeCopyTree(filepath.Join(source, entry.Name()), filepath.Join(destination, entry.Name())); err != nil {
				return err
			}
		}
		return nil
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("refusing to copy non-regular file: %s", source)
	}
	file, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	return stageNativeCopy(destination, info.Mode(), file)
}

func stageNativeCopy(destination string, mode os.FileMode, input io.Reader) error {
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644|mode&0o111)
	if err != nil {
		return err
	}
	_, err = io.Copy(file, input)
	return errors.Join(err, file.Close())
}
