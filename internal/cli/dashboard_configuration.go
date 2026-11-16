package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type dashboardConfigurationKey struct{}

type dashboardConfigurationSnapshot struct {
	digest       string
	repositories []map[string]any
}

type dashboardConfiguration struct {
	path        string
	builtins    []string
	publication sync.Mutex
	current     atomic.Pointer[dashboardConfigurationSnapshot]
}

func dashboardCheckoutRepository(ctx context.Context) string {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	content, err := exec.CommandContext(ctx, "git", "remote", "get-url", "origin").Output()
	if err != nil {
		log.Print("Local GitHub checkout unavailable; explicitly selected repositories remain built in")
		return ""
	}
	remote := strings.TrimSpace(string(content))
	for _, prefix := range []string{"https://github.com/", "git@github.com:", "ssh://git@github.com/"} {
		if len(remote) < len(prefix) || !strings.EqualFold(remote[:len(prefix)], prefix) {
			continue
		}
		repository := strings.TrimSuffix(strings.TrimSuffix(remote[len(prefix):], "/"), ".git")
		if repositoryPattern.MatchString(repository) && !strings.Contains(repository, "..") {
			return strings.ToLower(repository)
		}
	}
	log.Print("Checkout origin is not a supported GitHub repository; explicitly selected repositories remain built in")
	return ""
}

func (c *dashboardConfiguration) repositoryRows() []map[string]any {
	repositories := map[string]bool{}
	for _, row := range c.current.Load().repositories {
		repositories[nativeText(row["organization"])+"/"+nativeText(row["repository"])] = true
	}
	for _, repository := range c.builtins {
		repositories[strings.ToLower(repository)] = true
	}
	rows := make([]map[string]any, 0, len(repositories))
	for _, repository := range slices.Sorted(maps.Keys(repositories)) {
		owner, name, _ := strings.Cut(repository, "/")
		rows = append(rows, map[string]any{"organization": owner, "repository": name})
	}
	return rows
}

func readDashboardConfiguration(path string) (*dashboardConfigurationSnapshot, error) {
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return &dashboardConfigurationSnapshot{digest: "not-configured"}, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	content, err := io.ReadAll(io.LimitReader(file, (2<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(content) > 2<<20 {
		return nil, errors.New("local CAO configuration exceeds 2 MiB")
	}
	value, err := nativeDecodeJSON(content)
	if err != nil {
		return nil, errors.New("local CAO configuration is not valid JSON")
	}
	document, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("local CAO configuration must be an object")
	}
	if err := nativeValidatePolicy(document); err != nil {
		return nil, errors.New("local CAO configuration does not satisfy the policy schema")
	}
	canonical, err := json.Marshal(document)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(canonical)
	snapshot := &dashboardConfigurationSnapshot{digest: hex.EncodeToString(digest[:])}
	control, _ := document["control-plane"].(map[string]any)
	scope, _ := control["scope"].(map[string]any)
	repositories := map[string]bool{}
	for _, value := range nativeArray(scope["allowed-repositories"]) {
		repository := strings.ToLower(nativeText(value))
		if !repositoryPattern.MatchString(repository) || strings.Contains(repository, "..") {
			continue // Policy patterns are bounds, not repository discovery.
		}
		repositories[repository] = true
	}
	for _, repository := range slices.Sorted(maps.Keys(repositories)) {
		owner, name, _ := strings.Cut(repository, "/")
		snapshot.repositories = append(snapshot.repositories, map[string]any{
			"organization": owner, "repository": name,
		})
	}
	return snapshot, nil
}

func dashboardSnapshotGuard(ctx context.Context) func() {
	configuration, _ := ctx.Value(dashboardConfigurationKey{}).(*dashboardConfiguration)
	if configuration == nil {
		return func() {}
	}
	configuration.publication.Lock()
	return configuration.publication.Unlock
}

func (c *dashboardConfiguration) watch(ctx context.Context, changed func() error, diagnostics io.Writer) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var previousError string
	appliedDigest := c.current.Load().digest
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			snapshot, err := readDashboardConfiguration(c.path)
			if err == nil && snapshot.digest != appliedDigest {
				c.current.Store(snapshot)
				err = changed()
				if err == nil {
					appliedDigest = snapshot.digest
				}
			}
			if err != nil {
				if text := err.Error(); text != previousError {
					_, _ = fmt.Fprintf(diagnostics, "Dashboard configuration reload failed; previous published configuration retained: %v\n", err)
					previousError = text
				}
			} else {
				previousError = ""
			}
		}
	}
}

func refreshDashboardConfigurationSnapshot(ctx context.Context, cache string) error {
	unlock := dashboardSnapshotGuard(ctx)
	defer unlock()
	manifest, err := readDashboardSnapshotManifest(ctx, cache)
	if err != nil {
		return err
	}
	content, err := readDashboardCheckpointPayload(ctx, cache, "inventory-sources.json", manifest["inventory-sources.json"])
	if err != nil {
		return err
	}
	var inventory map[string]struct {
		Metadata map[string]any `json:"metadata"`
	}
	if err := json.Unmarshal(content, &inventory); err != nil {
		return err
	}
	var transactions []map[string]any
	for _, key := range []string{"local-dashboard", "local-dashboard-ingestion"} {
		if record, ok := inventory["repositories"].Metadata[key].(map[string]any); ok {
			transactions = append(transactions, record)
		}
	}
	return publishDashboardSnapshotCheckpoint(ctx, cache, maps.Clone(manifest), manifest,
		map[string][]map[string]any{"transactions": transactions}, nil)
}
