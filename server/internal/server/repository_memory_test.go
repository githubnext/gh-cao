package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/repositorymemory"
)

func TestRepositoryMemoryHandlers(t *testing.T) {
	database := integrationDatabase(t)
	content := []byte("# Context\n")
	sum := sha256.Sum256(content)
	manifest, err := json.Marshal(repositorymemory.Manifest{
		Version: 1,
		Campaigns: []repositorymemory.Campaign{{
			Campaign: "security-review",
			Branch:   "memory/security-review",
			Commit:   strings.Repeat("a", 40),
			Files: []repositorymemory.File{{
				Path:   "notes/context.md",
				OID:    strings.Repeat("b", 40),
				SHA256: hex.EncodeToString(sum[:]),
				Size:   int64(len(content)),
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	directory := repositoryMemoryArtifact(t, manifest, "security-review/notes/context.md", content)
	app := &App{database: database, config: Config{SourceDirectory: directory}}

	list := httptest.NewRecorder()
	listRequest := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/memory/security-review", nil)
	listRequest.SetPathValue("campaign", "security-review")
	app.repositoryMemoryCampaign(list, listRequest)
	if list.Code != http.StatusOK || !strings.Contains(list.Body.String(), `"notes/context.md"`) {
		t.Fatalf("list response = %d %s", list.Code, list.Body.String())
	}

	file := httptest.NewRecorder()
	fileRequest := httptest.NewRequestWithContext(
		t.Context(), http.MethodGet,
		"/api/v1/memory/security-review/content?path=notes%2Fcontext.md", nil,
	)
	fileRequest.SetPathValue("campaign", "security-review")
	app.repositoryMemoryContent(file, fileRequest)
	if file.Code != http.StatusOK || !strings.Contains(file.Body.String(), `"# Context\n"`) {
		t.Fatalf("content response = %d %s", file.Code, file.Body.String())
	}
}

func TestRepositoryMemoryHandlersRejectInvalidAndMissingRequests(t *testing.T) {
	database := integrationDatabase(t)
	manifest := []byte(`{"version":1,"campaigns":[]}`)
	directory := repositoryMemoryArtifact(t, manifest, "", nil)
	app := &App{database: database, config: Config{SourceDirectory: directory}}

	invalid := httptest.NewRecorder()
	invalidRequest := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/memory/INVALID", nil)
	invalidRequest.SetPathValue("campaign", "INVALID")
	app.repositoryMemoryCampaign(invalid, invalidRequest)
	if invalid.Code != http.StatusBadRequest {
		t.Fatalf("invalid campaign response = %d", invalid.Code)
	}

	missing := httptest.NewRecorder()
	missingRequest := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/memory/missing", nil)
	missingRequest.SetPathValue("campaign", "missing")
	app.repositoryMemoryCampaign(missing, missingRequest)
	if missing.Code != http.StatusOK || strings.TrimSpace(missing.Body.String()) != "null" {
		t.Fatalf("missing campaign response = %d", missing.Code)
	}
}

type contendedRepositoryMemoryCache struct {
	repositorymemory.RemoteCache
}

func (contendedRepositoryMemoryCache) TryLock(context.Context, string, string, time.Duration) (bool, error) {
	return false, nil
}

func TestRepositoryMemoryHandlerReportsConcurrentRefresh(t *testing.T) {
	database := integrationDatabase(t)
	seedDatabase(t, database, nil)
	store := newTestOperationalStore(t)
	app := &App{services: store.OperationalServices(), database: database,
		memory: &repositorymemory.RemoteResolver{Cache: contendedRepositoryMemoryCache{store}},
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/api/v1/memory/example", nil)
	request.SetPathValue("campaign", "example")

	app.repositoryMemoryCampaign(response, request)

	if response.Code != http.StatusTooManyRequests || response.Header().Get("Retry-After") != "1" {
		t.Fatalf("throttled response = %d Retry-After=%q", response.Code, response.Header().Get("Retry-After"))
	}
}

func TestRepositoryMemoryIntegrityFailureAcceptsMatchingContent(t *testing.T) {
	content := []byte("# Context\n")
	sum := sha256.Sum256(content)
	expected := repositorymemory.File{Size: int64(len(content)), SHA256: hex.EncodeToString(sum[:])}
	if reason, ok := repositoryMemoryIntegrityFailure(content, expected); !ok {
		t.Fatalf("expected matching content to pass integrity validation, got reason %q", reason)
	}
}

func TestRepositoryMemoryIntegrityFailureAcceptsContentWithoutRecordedChecksum(t *testing.T) {
	content := []byte("# Context\n")
	expected := repositorymemory.File{Size: int64(len(content))}
	if reason, ok := repositoryMemoryIntegrityFailure(content, expected); !ok {
		t.Fatalf("expected content without a recorded checksum to pass, got reason %q", reason)
	}
}

func TestRepositoryMemoryIntegrityFailureRejectsSizeMismatch(t *testing.T) {
	content := []byte("# Context\n")
	expected := repositorymemory.File{Size: int64(len(content)) + 1}
	reason, ok := repositoryMemoryIntegrityFailure(content, expected)
	if ok || reason != repositoryMemoryIntegrityReasonSizeMismatch {
		t.Fatalf("expected size-mismatch rejection, got ok=%t reason=%q", ok, reason)
	}
}

func TestRepositoryMemoryIntegrityFailureRejectsSHA256Mismatch(t *testing.T) {
	content := []byte("# Context\n")
	expected := repositorymemory.File{Size: int64(len(content)), SHA256: strings.Repeat("0", 64)}
	reason, ok := repositoryMemoryIntegrityFailure(content, expected)
	if ok || reason != repositoryMemoryIntegrityReasonSHA256Mismatch {
		t.Fatalf("expected sha256-mismatch rejection, got ok=%t reason=%q", ok, reason)
	}
}

func TestRepositoryMemoryIntegrityFailureIsCaseInsensitiveForSHA256(t *testing.T) {
	content := []byte("# Context\n")
	sum := sha256.Sum256(content)
	expected := repositorymemory.File{
		Size:   int64(len(content)),
		SHA256: strings.ToUpper(hex.EncodeToString(sum[:])),
	}
	if reason, ok := repositoryMemoryIntegrityFailure(content, expected); !ok {
		t.Fatalf("expected case-insensitive checksum comparison to pass, got reason %q", reason)
	}
}

func TestRepositoryMemoryContentRejectsIntegrityFailure(t *testing.T) {
	database := integrationDatabase(t)
	content := []byte("# Context\n")
	manifest, err := json.Marshal(repositorymemory.Manifest{
		Version: 1,
		Campaigns: []repositorymemory.Campaign{{
			Campaign: "security-review",
			Branch:   "memory/security-review",
			Commit:   strings.Repeat("a", 40),
			Files: []repositorymemory.File{{
				Path:   "notes/context.md",
				OID:    strings.Repeat("b", 40),
				SHA256: strings.Repeat("0", 64),
				Size:   int64(len(content)),
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	directory := repositoryMemoryArtifact(t, manifest, "security-review/notes/context.md", content)
	app := &App{database: database, config: Config{SourceDirectory: directory}}

	response := httptest.NewRecorder()
	request := httptest.NewRequestWithContext(
		t.Context(), http.MethodGet,
		"/api/v1/memory/security-review/content?path=notes%2Fcontext.md", nil,
	)
	request.SetPathValue("campaign", "security-review")
	app.repositoryMemoryContent(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("integrity-failure response = %d %s", response.Code, response.Body.String())
	}
}

func repositoryMemoryArtifact(t *testing.T, manifest []byte, fileID string, content []byte) string {
	t.Helper()
	directory, err := os.MkdirTemp(".", "memory-artifact-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	root := filepath.Join(directory, "memory")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), manifest, 0600); err != nil {
		t.Fatal(err)
	}
	if fileID != "" {
		path := filepath.Join(root, filepath.FromSlash(fileID))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, content, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return directory
}
