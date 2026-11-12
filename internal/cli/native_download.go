package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

var nativeDigestPattern = regexp.MustCompile(`^[a-fA-F0-9]{64}$`)

var errNativeDownloadDowngrade = errors.New("dashboard download redirect must not downgrade HTTPS")

type nativeDownloadFailure struct {
	message string
	retry   bool
}

func (e *nativeDownloadFailure) Error() string { return e.message }

func nativeDownload(cmd *cobra.Command, _ ghExecutor) error {
	manifestURL := nativeString(cmd, "url")
	if manifestURL == "" {
		manifestURL = os.Getenv("DASHBOARD_DATA_URL")
	}
	output := nativeString(cmd, "output")
	if output == "" {
		output = ".cao"
	}
	digest := nativeString(cmd, "manifest-sha256")
	if digest == "" {
		digest = os.Getenv("DASHBOARD_MANIFEST_SHA256")
	}
	result, err := nativeDownloadSnapshot(cmd.Context(), manifestURL, output, digest, nil)
	if err != nil {
		return err
	}
	return printJSON(cmd, result)
}

func nativeDownloadURL(raw string) (*url.URL, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nativeErrorf("An explicit Activity snapshot manifest URL is required: pass --url or set DASHBOARD_DATA_URL to a payload-hashes.json URL. Alternatively, download the cao-activity-index artifact from a selected CAO Activity run with gh run download RUN_ID --repo OWNER/REPO --name cao-activity-index --dir .cao, then query the local cache with --database FILE.")
	}
	value, err := url.Parse(raw)
	if err != nil || value.Host == "" || (value.Scheme != "https" && value.Scheme != "http") {
		return nil, nativeErrorf("Dashboard data URL must use HTTP or HTTPS.")
	}
	if value.User != nil {
		return nil, nativeErrorf("Dashboard data URL must not contain credentials.")
	}
	return value, nil
}

func nativeDownloadSnapshot(ctx context.Context, rawURL, output, digest string, client *http.Client) (map[string]any, error) {
	manifestURL, err := nativeDownloadURL(rawURL)
	if err != nil {
		return nil, err
	}
	if !strings.HasSuffix(manifestURL.Path, "/payload-hashes.json") {
		return nil, nativeErrorf("Dashboard data URL must identify payload-hashes.json.")
	}
	if digest != "" && !nativeDigestPattern.MatchString(digest) {
		return nil, nativeErrorf("Dashboard manifest SHA-256 must contain exactly 64 hexadecimal characters.")
	}
	directory, err := filepath.Abs(output)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return nil, err
	}
	if client == nil {
		client = &http.Client{Timeout: 120 * time.Second}
	}
	// Redirects must not smuggle credentials or downgrade HTTPS, even when a
	// caller supplies its own redirect policy.
	copyClient := *client
	previousRedirect := copyClient.CheckRedirect
	copyClient.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if _, err := nativeDownloadURL(request.URL.String()); err != nil {
			return err
		}
		if len(via) > 0 && via[len(via)-1].URL.Scheme == "https" && request.URL.Scheme != "https" {
			return errNativeDownloadDowngrade
		}
		if previousRedirect != nil {
			return previousRedirect(request, via)
		}
		if len(via) >= 10 {
			return errors.New("stopped after 10 redirects")
		}
		return nil
	}
	for attempt := 1; attempt <= 3; attempt++ {
		result, err := nativeDownloadAttempt(ctx, manifestURL, directory, strings.ToLower(digest), &copyClient)
		if err == nil {
			return result, nil
		}
		var failure *nativeDownloadFailure
		if !errors.As(err, &failure) || !failure.retry || attempt == 3 {
			return nil, err
		}
		if !strings.HasPrefix(failure.message, "Activity ") {
			timer := time.NewTimer(time.Duration(attempt) * time.Second)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
	}
	return nil, errors.New("download retry exhausted")
}

func nativeFetchFile(ctx context.Context, client *http.Client, source *url.URL, destination string, allowEmpty bool) (err error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source.String(), nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/x-ndjson, application/json, text/plain")
	response, err := client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if errors.Is(err, errNativeDownloadDowngrade) {
			return errNativeDownloadDowngrade
		}
		return &nativeDownloadFailure{fmt.Sprintf("Unable to download %s: transport failure", source), true}
	}
	defer func() { err = errors.Join(err, response.Body.Close()) }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		retry := response.StatusCode == 408 || response.StatusCode == 425 || response.StatusCode == 429 || (response.StatusCode >= 500 && response.StatusCode <= 599)
		return &nativeDownloadFailure{fmt.Sprintf("Unable to download %s: HTTP %d", source, response.StatusCode), retry}
	}
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	size, err := io.Copy(file, response.Body)
	closeErr := file.Close()
	if err != nil {
		return &nativeDownloadFailure{fmt.Sprintf("Unable to download %s: interrupted response", source), ctx.Err() == nil}
	}
	if closeErr != nil {
		return closeErr
	}
	if !allowEmpty && size == 0 {
		return nativeErrorf("Unable to download %s: response body is empty", source)
	}
	return nil
}

func nativeHashFile(name string) (digest string, err error) {
	file, err := os.Open(name)
	if err != nil {
		return "", err
	}
	defer func() { err = errors.Join(err, file.Close()) }()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func nativeShardName(name string) (string, bool, error) {
	if name == "." || path.Clean(name) != name || !filepath.IsLocal(filepath.FromSlash(name)) || strings.ContainsAny(name, "\\:\x00\r\n") {
		return "", false, nativeErrorf("Activity shard manifest contains an unsafe path: %s", name)
	}
	directory, leaf, found := strings.Cut(name, "/")
	if directory != "gh-aw-logs-runs" && directory != "gh-aw-logs-records" && directory != "gh-aw-logs-shards" {
		return "", false, nil
	}
	if !found || leaf == "" || !strings.HasSuffix(leaf, ".jsonl") || strings.ContainsAny(leaf, "/\\:%?#\x00\r\n") || path.Clean(name) != name || !filepath.IsLocal(filepath.FromSlash(name)) {
		return "", false, nativeErrorf("Activity shard manifest contains an unsafe path: %s", name)
	}
	return directory, true, nil
}

func nativeDownloadAttempt(ctx context.Context, manifestURL *url.URL, directory, digest string, client *http.Client) (result map[string]any, err error) {
	staging, err := os.MkdirTemp(directory, ".deployed-dashboard-")
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, os.RemoveAll(staging)) }()
	baseNames := []string{"payload-hashes.json", "gh-aw-logs.sqlite", "inventory-sources.json"}
	manifestPath := filepath.Join(staging, baseNames[0])
	if err := nativeFetchFile(ctx, client, manifestURL, manifestPath, false); err != nil {
		return nil, err
	}
	manifestHash, err := nativeHashFile(manifestPath)
	if err != nil {
		return nil, err
	}
	if digest != "" && manifestHash != digest {
		return nil, nativeErrorf("Activity snapshot manifest checksum mismatch: payload-hashes.json")
	}
	content, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, err
	}
	value, err := nativeDecodeJSON(content)
	if err != nil {
		return nil, err
	}
	hashes, ok := value.(map[string]any)
	if !ok {
		return nil, nativeErrorf("Activity snapshot manifest must contain a JSON object.")
	}
	if hashes["synthetic"] == true {
		return nil, nativeErrorf("Synthetic Pages demo data cannot be used as an operational Activity snapshot.")
	}
	expected, _ := hashes["gh-aw-logs.sqlite"].(string)
	if !nativeDigestPattern.MatchString(expected) {
		return nil, nativeErrorf("Activity snapshot manifest contains no valid SQLite checksum.")
	}
	for _, name := range baseNames[1:] {
		source := manifestURL.ResolveReference(&url.URL{Path: name})
		if err := nativeFetchFile(ctx, client, source, filepath.Join(staging, name), false); err != nil {
			return nil, err
		}
	}
	inventory, err := os.ReadFile(filepath.Join(staging, "inventory-sources.json"))
	if err != nil {
		return nil, err
	}
	value, err = nativeDecodeJSON(inventory)
	if err != nil {
		return nil, err
	}
	if _, ok := value.(map[string]any); !ok {
		return nil, nativeErrorf("Deployed inventory sources must contain a JSON object.")
	}
	actual, err := nativeHashFile(filepath.Join(staging, "gh-aw-logs.sqlite"))
	if err != nil {
		return nil, err
	}
	if actual != strings.ToLower(expected) {
		return nil, &nativeDownloadFailure{"Activity SQLite checksum mismatch: gh-aw-logs.sqlite", true}
	}
	groups := map[string][]string{}
	for name, value := range hashes {
		group, shard, err := nativeShardName(name)
		if err != nil {
			return nil, err
		}
		expected, _ := value.(string)
		if shard && nativeDigestPattern.MatchString(expected) {
			groups[group] = append(groups[group], name)
		}
	}
	for _, names := range groups {
		sort.Strings(names)
	}
	payloads := append([]string{}, groups["gh-aw-logs-shards"]...)
	if len(groups["gh-aw-logs-runs"]) > 0 {
		payloads = append(append([]string{}, groups["gh-aw-logs-runs"]...), groups["gh-aw-logs-records"]...)
	}
	if len(payloads) == 0 {
		return nil, nativeErrorf("Activity shard manifest contains no valid JSONL shards.")
	}
	directories := []string{}
	seen := map[string]bool{}
	for _, name := range payloads {
		group, _, _ := strings.Cut(name, "/")
		if !seen[group] {
			seen[group] = true
			directories = append(directories, group)
		}
		destination := filepath.Join(staging, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			return nil, err
		}
		if err := nativeFetchFile(ctx, client, manifestURL.ResolveReference(&url.URL{Path: name}), destination, true); err != nil {
			return nil, err
		}
		actual, err := nativeHashFile(destination)
		if err != nil {
			return nil, err
		}
		if actual != strings.ToLower(hashes[name].(string)) {
			return nil, &nativeDownloadFailure{"Activity shard checksum mismatch: " + name, true}
		}
		info, err := os.Stat(destination)
		if err != nil {
			return nil, err
		}
		if info.Size() == 0 {
			delete(hashes, name)
			if err := os.Remove(destination); err != nil {
				return nil, err
			}
		}
	}
	content, err = json.MarshalIndent(hashes, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(manifestPath, append(content, '\n'), 0o644); err != nil {
		return nil, err
	}
	if err := nativePublishDownload(staging, directory, append(append([]string{}, directories...), baseNames...)); err != nil {
		return nil, err
	}
	paths := []string{}
	for _, name := range directories {
		paths = append(paths, filepath.Join(directory, name))
	}
	return map[string]any{
		"manifestUrl": manifestURL.String(), "databaseUrl": manifestURL.ResolveReference(&url.URL{Path: "gh-aw-logs.sqlite"}).String(),
		"inventoryUrl": manifestURL.ResolveReference(&url.URL{Path: "inventory-sources.json"}).String(),
		"manifest":     filepath.Join(directory, "payload-hashes.json"), "database": filepath.Join(directory, "gh-aw-logs.sqlite"),
		"inventory": filepath.Join(directory, "inventory-sources.json"), "payloadDirectories": paths,
	}, nil
}

// Keep the previous snapshot until all verified payloads are ready and roll
// back a failed publication rather than leaving a mixed old/new snapshot.
func nativePublishDownload(staging, destination string, names []string) (err error) {
	root, err := os.OpenRoot(destination)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, root.Close()) }()
	staged, err := filepath.Rel(destination, staging)
	if err != nil || !filepath.IsLocal(staged) || filepath.Base(staged) != staged || !strings.HasPrefix(staged, ".deployed-dashboard-") {
		return errors.New("download staging directory must remain within the output root")
	}
	backup := filepath.Join(staged, "previous")
	if err := root.Mkdir(backup, 0o700); err != nil {
		return err
	}
	published, moved := []string{}, []string{}
	rollback := func(cause error) error {
		for i := len(published) - 1; i >= 0; i-- {
			name := published[i]
			cause = errors.Join(cause, root.RemoveAll(name))
		}
		for i := len(moved) - 1; i >= 0; i-- {
			name := moved[i]
			cause = errors.Join(cause, root.Rename(filepath.Join(backup, name), name))
		}
		return cause
	}
	for _, name := range names {
		switch name {
		case "payload-hashes.json", "gh-aw-logs.sqlite", "inventory-sources.json", "gh-aw-logs-runs", "gh-aw-logs-records", "gh-aw-logs-shards":
		default:
			return rollback(nativeErrorf("Activity snapshot contains an unsafe publication path: %s", name))
		}
		// A leaf symlink is replaced, never followed. Reject one that points
		// beyond the user-selected root before moving any previous snapshot.
		if _, err := root.Stat(name); err != nil && !errors.Is(err, os.ErrNotExist) {
			return rollback(err)
		}
		if _, err := root.Lstat(name); err == nil {
			if err := root.Rename(name, filepath.Join(backup, name)); err != nil {
				return rollback(err)
			}
			moved = append(moved, name)
		} else if !errors.Is(err, os.ErrNotExist) {
			return rollback(err)
		}
		if err := root.Rename(filepath.Join(staged, name), name); err != nil {
			return rollback(err)
		}
		published = append(published, name)
	}
	return nil
}
