package main

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
	"path/filepath"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/sqlitex"
)

const defaultSQLitePath = "../.cao/gh-aw-logs.sqlite"
const maxSQLiteManifestBytes int64 = 8 << 20
const maxSQLiteDownloadBytes int64 = 2 << 30

type sqliteDownloadError struct {
	message string
	retry   bool
}

func (e *sqliteDownloadError) Error() string { return e.message }

func sqliteManifestURL(raw string) (*url.URL, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, errors.New("an explicit Activity snapshot manifest URL is required: set DASHBOARD_DATA_URL or pass --download-sqlite=URL identifying payload-hashes.json; alternatively, download the cao-activity-index artifact from a selected CAO Activity run with gh run download RUN_ID --repo OWNER/REPO --name cao-activity-index --dir .cao at the checkout root, or select an existing snapshot with --sqlite FILE")
	}
	location, err := url.Parse(raw)
	if err != nil || location.Hostname() == "" || (location.Scheme != "https" && location.Scheme != "http") {
		return nil, errors.New("--download-sqlite URL must use HTTP or HTTPS")
	}
	if location.User != nil || location.Fragment != "" {
		return nil, errors.New("--download-sqlite URL must not contain credentials or a fragment")
	}
	if !strings.HasSuffix(location.Path, "/payload-hashes.json") {
		return nil, errors.New("--download-sqlite URL must identify payload-hashes.json; site and directory discovery is not supported")
	}
	return location, nil
}

func downloadSQLite(ctx context.Context, rawURL, destination string) error {
	manifestURL, err := sqliteManifestURL(rawURL)
	if err != nil {
		return err
	}
	destination, err = filepath.Abs(destination)
	if err != nil {
		return err
	}
	if err := validateSQLiteDestination(destination); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return fmt.Errorf("create SQLite cache directory: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	client := &http.Client{
		Timeout:       5 * time.Minute,
		CheckRedirect: checkSQLiteRedirect,
	}
	for attempt := 1; ; attempt++ {
		err := downloadSQLiteAttempt(ctx, client, manifestURL, destination)
		if err == nil {
			return nil
		}
		var failure *sqliteDownloadError
		if attempt == 3 || !errors.As(err, &failure) || !failure.retry {
			return err
		}
		commandLog.Printf("retrying SQLite snapshot download attempt=%d", attempt+1)
		timer := time.NewTimer(time.Duration(attempt) * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// checkSQLiteRedirect limits redirects to a bounded chain of credential-free
// HTTP(S) locations and never permits an HTTPS to HTTP downgrade.
func checkSQLiteRedirect(request *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		commandLog.Printf("SQLite download redirect rejected: reason=too_many hops=%d", len(via))
		return errors.New("stopped after 10 redirects")
	}
	if request.URL.User != nil ||
		(request.URL.Scheme != "http" && request.URL.Scheme != "https") ||
		(via[len(via)-1].URL.Scheme == "https" && request.URL.Scheme != "https") {
		commandLog.Printf("SQLite download redirect rejected: reason=unsafe_target")
		return errors.New("unsafe SQLite download redirect")
	}
	return nil
}

func validateSQLiteDestination(destination string) error {
	info, err := os.Lstat(destination)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect SQLite cache destination: %w", err)
	}
	if err == nil && !info.Mode().IsRegular() {
		return errors.New("SQLite cache destination must be a regular file, not a directory or symlink")
	}
	// An old WAL must never be replayed against a newly downloaded database.
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if _, err := os.Lstat(destination + suffix); err == nil {
			return errors.New("SQLite cache has journal sidecars; stop its users and checkpoint it, or choose a new --sqlite path")
		} else if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("inspect SQLite cache journal: %w", err)
		}
	}
	return nil
}

func downloadSQLiteAttempt(ctx context.Context, client *http.Client, sourceURL *url.URL, destination string) (err error) {
	manifest := &strings.Builder{}
	manifestURL, err := fetchSQLitePayload(ctx, client, sourceURL, "manifest", manifest, maxSQLiteManifestBytes)
	if err != nil {
		return err
	}
	expected, err := parseSQLiteChecksum([]byte(manifest.String()))
	if err != nil {
		return err
	}
	staging, err := os.MkdirTemp(filepath.Dir(destination), ".cao-snapshot-")
	if err != nil {
		return err
	}
	defer func() {
		err = errors.Join(err, os.RemoveAll(staging))
	}()
	temporary, err := os.CreateTemp(staging, "snapshot-*.sqlite")
	if err != nil {
		return err
	}
	if err := func() (err error) {
		defer func() { err = errors.Join(err, temporary.Close()) }()
		hash := sha256.New()
		location := manifestURL.ResolveReference(&url.URL{Path: "gh-aw-logs.sqlite"})
		if _, err := fetchSQLitePayload(ctx, client, location, "image", io.MultiWriter(temporary, hash), maxSQLiteDownloadBytes); err != nil {
			return err
		}
		if hex.EncodeToString(hash.Sum(nil)) != expected {
			commandLog.Printf("SQLite snapshot checksum mismatch")
			return &sqliteDownloadError{message: "SQLite snapshot checksum mismatch; the published snapshot may be changing", retry: true}
		}
		return temporary.Sync()
	}(); err != nil {
		return err
	}
	snapshot, err := sqlitex.Open(ctx, temporary.Name())
	if err != nil {
		return fmt.Errorf("validate downloaded SQLite snapshot: %w", err)
	}
	if err := snapshot.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateSQLiteDestination(destination); err != nil {
		return err
	}
	if err := os.Rename(temporary.Name(), destination); err != nil {
		return fmt.Errorf("publish downloaded SQLite snapshot: %w", err)
	}
	commandLog.Printf("published SQLite snapshot")
	return nil
}

// parseSQLiteChecksum extracts the lowercase hex SHA-256 recorded for the
// SQLite image in a payload-hashes manifest.
func parseSQLiteChecksum(manifest []byte) (string, error) {
	var hashes map[string]json.RawMessage
	if err := json.Unmarshal(manifest, &hashes); err != nil {
		return "", errors.New("SQLite snapshot manifest must contain a JSON object")
	}
	if string(hashes["synthetic"]) == "true" {
		return "", errors.New("synthetic Pages demo data cannot be used as an operational Activity snapshot")
	}
	var expected string
	if err := json.Unmarshal(hashes["gh-aw-logs.sqlite"], &expected); err != nil {
		return "", errors.New("SQLite snapshot manifest contains no valid SQLite SHA-256 checksum")
	}
	digest, err := hex.DecodeString(expected)
	if err != nil || len(digest) != sha256.Size {
		return "", errors.New("SQLite snapshot manifest contains no valid SQLite SHA-256 checksum")
	}
	return hex.EncodeToString(digest), nil
}

// retryableSQLiteStatus reports whether an HTTP status is a transient failure.
func retryableSQLiteStatus(status int) bool {
	return status == http.StatusRequestTimeout || status == http.StatusTooEarly ||
		status == http.StatusTooManyRequests || status >= 500
}

func fetchSQLitePayload(ctx context.Context, client *http.Client, location *url.URL, resource string, destination io.Writer, limit int64) (_ *url.URL, err error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, location.String(), nil) // #nosec G704 -- Operator-selected HTTP(S) snapshot endpoint, never a remote request input.
	if err != nil {
		return nil, fmt.Errorf("create SQLite %s request", resource)
	}
	request.Header.Set("Accept", "application/octet-stream, application/json")
	request.Header.Set("Cache-Control", "no-cache")
	response, err := client.Do(request) // #nosec G704 -- CLI acquisition with credential-free URLs and checked redirects; local endpoints are intentionally supported.
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		// Transport errors can include the full URL, including secret query values.
		return nil, &sqliteDownloadError{message: fmt.Sprintf("download SQLite %s: transport failure or rejected redirect", resource), retry: true}
	}
	defer func() { err = errors.Join(err, response.Body.Close()) }()
	if response.StatusCode != http.StatusOK {
		retry := retryableSQLiteStatus(response.StatusCode)
		return nil, &sqliteDownloadError{message: fmt.Sprintf("download SQLite %s: HTTP %d", resource, response.StatusCode), retry: retry}
	}
	if response.ContentLength > limit {
		return nil, fmt.Errorf("SQLite %s exceeds the %d-byte download limit", resource, limit)
	}
	size, err := io.Copy(destination, io.LimitReader(response.Body, limit+1))
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, &sqliteDownloadError{message: fmt.Sprintf("download SQLite %s: interrupted response", resource), retry: true}
		}
		return nil, fmt.Errorf("write downloaded SQLite %s: %w", resource, err)
	}
	if size > limit {
		return nil, fmt.Errorf("SQLite %s exceeds the %d-byte download limit", resource, limit)
	}
	if size == 0 {
		return nil, fmt.Errorf("download SQLite %s: empty response", resource)
	}
	return response.Request.URL, nil
}
