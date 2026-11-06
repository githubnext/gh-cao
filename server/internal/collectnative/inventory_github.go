package collectnative

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/model"
)

// InventorySnapshotRef is reviewed deployment configuration. SHA pins the
// repository object containing the snapshot; Provenance pins its source build
// and reviewed policy independently (there is no self-referential commit hash).
type InventorySnapshotRef struct {
	Repository     string
	InstallationID int64
	SHA            string
	Path           string
	Digest         string
	Provenance     InventoryProvenance
}

func (g GitHub) LoadInventorySnapshot(ctx context.Context, ref InventorySnapshotRef) (InventorySnapshot, error) {
	if !coordinate.MatchString(ref.Repository) || ref.InstallationID <= 0 || !inventorySHA.MatchString(ref.SHA) ||
		!inventoryDigest.MatchString(ref.Digest) || !inventoryObjectPath(ref.Path) || ref.Provenance.Validate() != nil {
		return InventorySnapshot{}, ErrIdentity
	}
	content, err := g.inventoryContent(ctx, ref.InstallationID, ref.Repository, ref.Path, ref.SHA)
	if err != nil {
		return InventorySnapshot{}, err
	}
	return NewInventorySnapshot(content, ref.Digest, ref.Provenance)
}

func inventoryObjectPath(value string) bool {
	if value == "" || len(value) > 1024 || strings.HasPrefix(value, "/") || strings.ContainsAny(value, "\\?#") {
		return false
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func (g GitHub) inventoryContent(ctx context.Context, installation int64, repository, objectPath, sha string) ([]byte, error) {
	parts := strings.Split(objectPath, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	content, _, _, err := g.get(ctx, installation, "repos/"+repository+"/contents/"+strings.Join(parts, "/")+"?ref="+url.QueryEscape(sha))
	if err != nil {
		return nil, err
	}
	var object struct {
		Type     string `json:"type"`
		Encoding string `json:"encoding"`
		Content  string `json:"content"`
	}
	if err := decode(content, &object); err != nil || object.Type != "file" || object.Encoding != "base64" {
		return nil, errors.New("native inventory object requires inline base64 file content")
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(object.Content, "\n", ""))
	if err != nil || len(decoded) > InventoryMaxBytes {
		return nil, ErrCapacity
	}
	return decoded, nil
}

type inventoryWorkflow struct {
	ID        json.Number `json:"id"`
	Name      string      `json:"name"`
	Path      string      `json:"path"`
	State     string      `json:"state"`
	URL       string      `json:"html_url"`
	CreatedAt string      `json:"created_at"`
	UpdatedAt string      `json:"updated_at"`
}

// BuildInventory reads only one explicitly enrolled repository. Reviewed source
// and policy data is immutable; live repository metadata cannot grant campaign
// membership or change role/readiness/admission/configuration-policy.
func (g GitHub) BuildInventory(ctx context.Context, scope EnrollmentScope, snapshot InventorySnapshot) (InventoryEvidence, error) {
	evidence := InventoryEvidence{Provenance: snapshot.Provenance, Digest: snapshot.Digest, Scope: scope,
		SourceSnapshot: bytes.Clone(snapshot.content), ObservedAt: time.Now().UTC()}
	g.Limits.Bytes = min(g.Limits.defaults().Bytes, InventoryMaxBytes)
	if err := snapshot.Validate(); err != nil {
		return evidence, err
	}
	if (Identity{Repository: scope.Repository, InstallationID: scope.InstallationID, RunID: 1, Attempt: 1}).Validate() != nil || scope.Epoch <= 0 {
		return evidence, ErrIdentity
	}
	document, err := snapshot.document()
	if err != nil {
		return evidence, err
	}
	evidence.Sources = document.Sources
	// Scope is replaced, never extended using the source snapshot's old fleet.
	owner, name, _ := strings.Cut(scope.Repository, "/")
	observed := evidence.ObservedAt.Format(time.RFC3339Nano)
	repository := model.Row{"organization": owner, "repository": name, "repository-name": name, "observed-at": observed}
	failures := []any{}
	totalBytes := 0
	get := func(endpoint string, target any) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		content, _, _, err := g.get(ctx, scope.InstallationID, endpoint)
		totalBytes += len(content)
		if totalBytes > InventoryMaxBytes {
			return ErrCapacity
		}
		if err != nil {
			return err
		}
		return decode(content, target)
	}
	var remote struct {
		FullName      string `json:"full_name"`
		Visibility    string `json:"visibility"`
		Private       bool   `json:"private"`
		DefaultBranch string `json:"default_branch"`
	}
	if err := get("repos/"+scope.Repository, &remote); err != nil {
		return evidence, fmt.Errorf("native inventory repository metadata: %w", err)
	}
	if !strings.EqualFold(remote.FullName, scope.Repository) {
		return evidence, ErrIdentity
	}
	visibility := remote.Visibility
	if visibility == "" {
		visibility = "public"
		if remote.Private {
			visibility = "private"
		}
	}
	repository["visibility"] = visibility
	evidence.Sources["repositories"] = model.Source{Source: "repositories", Rows: []model.Row{repository},
		Metadata: model.Metadata{"availability": "available", "completeness": "complete", "freshness": "current", "as-of": observed}}
	var commit struct {
		SHA string `json:"sha"`
	}
	if remote.DefaultBranch == "" {
		failures = append(failures, model.Row{"operation": "workflow-version-discovery", "reason": "default-branch-unavailable"})
	} else if err := get("repos/"+scope.Repository+"/commits/"+url.PathEscape(remote.DefaultBranch), &commit); err != nil || !inventorySHA.MatchString(commit.SHA) {
		if ctx.Err() != nil {
			return evidence, ctx.Err()
		}
		if errors.Is(err, ErrCapacity) {
			return evidence, err
		}
		commit.SHA = ""
		failures = append(failures, model.Row{"operation": "workflow-version-discovery", "reason": "repository-revision-unavailable"})
	}
	latest, err := g.inventoryLatestCompiler(ctx, scope.InstallationID)
	if err != nil {
		if ctx.Err() != nil {
			return evidence, ctx.Err()
		}
		failures = append(failures, model.Row{"operation": "gh-aw-version-discovery", "reason": "latest-release-unavailable"})
	}
	rows := map[string]model.Row{}
	pages, observedEntries, expected := 0, 0, -1
	complete := false
	for page := 1; page <= 10; page++ {
		var listing struct {
			Count     *int                `json:"total_count"`
			Workflows []inventoryWorkflow `json:"workflows"`
		}
		err := get(fmt.Sprintf("repos/%s/actions/workflows?per_page=100&page=%d", scope.Repository, page), &listing)
		if err != nil || listing.Workflows == nil || listing.Count == nil || *listing.Count < 0 || len(listing.Workflows) > 100 {
			if ctx.Err() != nil {
				return evidence, ctx.Err()
			}
			if errors.Is(err, ErrCapacity) {
				return evidence, err
			}
			failures = append(failures, model.Row{"operation": "workflow-registry-discovery", "reason": "registry-unavailable"})
			break
		}
		pages++
		expected = *listing.Count
		observedEntries += len(listing.Workflows)
		for _, workflow := range listing.Workflows {
			key := inventoryPath(workflow.Path)
			if workflow.State == "deleted" || key == "" || strings.TrimSpace(workflow.Name) == "" {
				continue
			}
			if _, duplicate := rows[key]; duplicate {
				return evidence, errors.New("native inventory registry contains conflicting workflow identities")
			}
			active := "unknown"
			if workflow.State == "active" {
				active = "true"
			} else if strings.HasPrefix(workflow.State, "disabled_") {
				active = "false"
			}
			version := ""
			if strings.HasSuffix(strings.ToLower(workflow.Path), ".lock.yml") {
				var source []byte
				var err error
				if commit.SHA != "" {
					source, err = g.inventoryContent(ctx, scope.InstallationID, scope.Repository, workflow.Path, commit.SHA)
					totalBytes += len(source)
				}
				if totalBytes > InventoryMaxBytes || errors.Is(err, ErrCapacity) {
					return evidence, ErrCapacity
				}
				if ctx.Err() != nil {
					return evidence, ctx.Err()
				}
				version = inventoryCompilerVersion(source)
				if err != nil || version == "" {
					failures = append(failures, model.Row{"operation": "workflow-version-discovery", "workflow": key, "reason": "compiler-metadata-unavailable"})
				}
			}
			row := model.Row{"organization": owner, "repository": name, "workflow": key, "workflow-name": workflow.Name,
				"workflow-role": "standalone", "workflow-source-kind": "registry", "workflow-active": active,
				"workflow-registry-state": workflow.State, "gh-aw-version": inventoryUnknown(version),
				"gh-aw-current-version": inventoryUnknown(latest), "gh-aw-update-state": inventoryVersionState(version, latest),
				"rollout-mode": "unknown", "observed-at": observed}
			if workflow.ID != "" {
				row["workflow-id"] = string(workflow.ID)
			}
			if workflow.URL != "" {
				if strings.HasPrefix(workflow.URL, "https://github.com/") {
					row["workflow-link"] = model.Row{"relation": "workflow", "href": workflow.URL, "label": "View " + workflow.Name}
				}
			}
			if workflow.CreatedAt != "" {
				row["created-at"] = workflow.CreatedAt
			}
			if workflow.UpdatedAt != "" {
				row["updated-at"] = workflow.UpdatedAt
			}
			rows[key] = row
		}
		if len(listing.Workflows) < 100 || observedEntries >= expected {
			complete = observedEntries >= expected
			break
		}
	}
	if !complete && pages > 0 {
		failures = append(failures, model.Row{"operation": "workflow-registry-discovery", "reason": "registry-listing-incomplete"})
	}
	// Overlay only reviewed source declarations belonging to this exact scope,
	// preserving all campaign/source/role/readiness/admission fields.
	for _, declared := range document.Sources["workflows"].Rows {
		coordinate := strings.ToLower(inventoryText(declared, "organization") + "/" + inventoryText(declared, "repository"))
		if coordinate != scope.Repository {
			continue
		}
		key := inventoryPath(inventoryText(declared, "workflow"))
		if key == "" {
			return evidence, errors.New("native inventory source workflow path is invalid")
		}
		registry := rows[key]
		merged := model.Row{}
		for field, value := range declared {
			merged[field] = value
		}
		merged["workflow"] = key
		merged["workflow-active"] = "unknown"
		if registry != nil {
			for _, field := range []string{"workflow-name", "workflow-active", "workflow-registry-state", "workflow-id", "workflow-link", "created-at", "updated-at"} {
				if value, ok := registry[field]; ok {
					merged[field] = value
				}
			}
		}
		version := inventoryText(declared, "gh-aw-version")
		if version == "" || version == "unknown" {
			version = inventoryText(registry, "gh-aw-version")
		}
		merged["gh-aw-version"], merged["gh-aw-current-version"] = inventoryUnknown(version), inventoryUnknown(latest)
		merged["gh-aw-update-state"] = inventoryVersionState(version, latest)
		rows[key] = merged
	}
	keys := make([]string, 0, len(rows))
	for key := range rows {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	workflows := make([]model.Row, 0, len(keys))
	for _, key := range keys {
		workflows = append(workflows, rows[key])
	}
	completeness := "complete"
	if !complete || len(failures) > 0 {
		completeness = "partial"
	}
	availability := "available"
	if len(workflows) == 0 && !complete {
		availability = "unavailable"
	}
	// Source-maintenance comparisons remain explicitly snapshot-timed. A fresh
	// registry listing is not proof that pinned catalog/version evidence is fresh.
	evidence.Sources["workflows"] = model.Source{Source: "workflows", Rows: workflows,
		Metadata: model.Metadata{"availability": availability, "completeness": completeness, "freshness": "unknown",
			"as-of": observed, "pages": pages, "expected": expected, "observed": observedEntries, "failures": failures,
			"source-sha": snapshot.Provenance.SourceSHA, "registry-revision": commit.SHA, "registry-complete": complete}}
	for _, name := range []string{"campaigns", "configuration-policy"} {
		source := evidence.Sources[name]
		if source.Metadata == nil {
			source.Metadata = model.Metadata{}
		}
		source.Metadata["freshness"] = "unknown"
		source.Metadata["source-sha"] = snapshot.Provenance.SourceSHA
		source.Metadata["policy-sha"] = snapshot.Provenance.PolicySHA
		evidence.Sources[name] = source
	}
	return evidence, ValidateInventoryEvidence(evidence)
}

func inventoryUnknown(value string) string {
	if value == "" {
		return "unknown"
	}
	return value
}

var inventoryVersionPattern = regexp.MustCompile(`^v?([0-9]+)\.([0-9]+)\.([0-9]+)(?:-([0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?$`)

func inventoryVersion(value string) string {
	match := inventoryVersionPattern.FindStringSubmatch(strings.TrimSpace(value))
	if match == nil {
		return ""
	}
	for _, part := range strings.Split(match[4], ".") {
		if len(part) > 1 && part[0] == '0' {
			if _, err := strconv.ParseUint(part, 10, 64); err == nil {
				return ""
			}
		}
	}
	normalized := "v" + strings.Join(match[1:4], ".")
	if match[4] != "" {
		normalized += "-" + match[4]
	}
	return normalized
}

func inventoryCompilerVersion(content []byte) string {
	for _, line := range strings.Split(string(content), "\n") {
		if metadata, ok := strings.CutPrefix(line, "# gh-aw-metadata: "); ok {
			var fields struct {
				Version string `json:"compiler_version"`
			}
			if json.Unmarshal([]byte(metadata), &fields) == nil {
				return inventoryVersion(fields.Version)
			}
			return ""
		}
	}
	return ""
}

func inventoryVersionState(installed, latest string) string {
	left, right := inventoryVersion(installed), inventoryVersion(latest)
	if left == "" || right == "" {
		return "unknown"
	}
	// Compare semver numerically, including prerelease precedence.
	l, r := inventoryVersionPattern.FindStringSubmatch(left), inventoryVersionPattern.FindStringSubmatch(right)
	for i := 1; i <= 3; i++ {
		a, b := strings.TrimLeft(l[i], "0"), strings.TrimLeft(r[i], "0")
		if len(a) < len(b) || len(a) == len(b) && a < b {
			return "update-available"
		}
		if len(a) > len(b) || a > b {
			return "up-to-date"
		}
	}
	if l[4] == r[4] || l[4] == "" {
		return "up-to-date"
	}
	if r[4] == "" {
		return "update-available"
	}
	lp, rp := strings.Split(l[4], "."), strings.Split(r[4], ".")
	for i := 0; i < max(len(lp), len(rp)); i++ {
		if i >= len(lp) {
			return "update-available"
		}
		if i >= len(rp) {
			return "up-to-date"
		}
		if lp[i] == rp[i] {
			continue
		}
		ln, le := strconv.ParseUint(lp[i], 10, 64)
		rn, re := strconv.ParseUint(rp[i], 10, 64)
		if le == nil && re == nil && ln < rn || le == nil && re != nil ||
			le != nil && re != nil && lp[i] < rp[i] {
			return "update-available"
		}
		return "up-to-date"
	}
	return "up-to-date"
}

func (g GitHub) inventoryLatestCompiler(ctx context.Context, installation int64) (string, error) {
	for page := 1; page <= 3; page++ {
		content, _, _, err := g.get(ctx, installation, fmt.Sprintf("repos/github/gh-aw/releases?per_page=100&page=%d", page))
		if err != nil {
			return "", err
		}
		var releases []struct {
			Tag        string `json:"tag_name"`
			Draft      bool   `json:"draft"`
			Prerelease bool   `json:"prerelease"`
		}
		if err := decode(content, &releases); err != nil || len(releases) > 100 {
			return "", errors.New("invalid native inventory release listing")
		}
		for _, release := range releases {
			version := inventoryVersion(release.Tag)
			if !release.Draft && !release.Prerelease && version != "" && !strings.Contains(version, "-") {
				return version, nil
			}
		}
		if len(releases) < 100 {
			break
		}
	}
	return "", errors.New("native inventory stable compiler release unavailable")
}
