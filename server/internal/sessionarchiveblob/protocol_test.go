package sessionarchiveblob

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/sessionarchive"
)

// blobProtocol models documented HTTP preconditions, ETags and lease expiry.
// It is not evidence of live Azure durability, RBAC or ADX partition pruning.
type blobProtocol struct {
	mu         sync.Mutex
	objects    map[string][]byte
	etags      map[string]string
	counter    int
	lease      string
	expires    time.Time
	now        time.Time
	public     bool
	loseKey    string
	corruptKey string
	failKey    string
	uploads    map[string]int
	query      func(http.ResponseWriter, *http.Request, string)
}

const protocolProperties = `{"StorageConnectionStrings":["https://privatearchive.blob.core.windows.net/container/archive;managed_identity=system"],"reviewed":"synthetic"}`

func (s *blobProtocol) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var body struct {
			CSL string `json:"csl"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil {
			http.Error(w, "invalid query request", http.StatusBadRequest)
			return
		}
		switch {
		case strings.HasPrefix(body.CSL, ".show external table Events schema"):
			respond(w, []string{"TableName", "Schema", "DatabaseName", "Folder", "DocString"},
				[][]any{{"Events", EventSchema(), "Archive", "", ""}}, false)
		case strings.HasPrefix(body.CSL, ".show external table Events"):
			respond(w, []string{"TableName", "TableType", "Properties"}, [][]any{{"Events", "Blob", protocolProperties}}, false)
		case s.query != nil:
			s.query(w, r, body.CSL)
		default:
			http.Error(w, "no synthetic query response configured", http.StatusInternalServerError)
		}
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if r.URL.Query().Get("restype") == "container" {
		if s.public {
			w.Header().Set("x-ms-blob-public-access", "blob")
		}
		w.WriteHeader(http.StatusOK)
		return
	}
	key := strings.TrimPrefix(r.URL.Path, "/container/archive/")
	if r.Header.Get("x-ms-version") != "2023-11-03" || r.Header.Get("x-ms-date") == "" {
		http.Error(w, "missing Azure protocol version/date", http.StatusBadRequest)
		return
	}
	if s.failKey == key {
		http.Error(w, "synthetic unavailable", http.StatusServiceUnavailable)
		return
	}
	activeLease := s.lease != "" && s.now.Before(s.expires)
	if r.URL.Query().Get("comp") == "lease" {
		if key != "active.json" || s.objects[key] == nil || r.Method != http.MethodPut {
			http.Error(w, "unsupported lease", http.StatusBadRequest)
			return
		}
		id := r.Header.Get("x-ms-lease-id")
		switch r.Header.Get("x-ms-lease-action") {
		case "acquire":
			if activeLease {
				http.Error(w, "lease held", http.StatusConflict)
				return
			}
			if r.Header.Get("x-ms-lease-duration") != "60" || !uuidPattern.MatchString(r.Header.Get("x-ms-proposed-lease-id")) {
				http.Error(w, "invalid lease duration/id", http.StatusBadRequest)
				return
			}
			s.lease, s.expires = r.Header.Get("x-ms-proposed-lease-id"), s.now.Add(time.Minute)
			w.Header().Set("x-ms-lease-id", s.lease)
			w.WriteHeader(http.StatusCreated)
		case "renew":
			if id != s.lease || !activeLease {
				http.Error(w, "lease lost", http.StatusPreconditionFailed)
				return
			}
			s.expires = s.now.Add(time.Minute)
			w.Header().Set("x-ms-lease-id", s.lease)
		case "release":
			if id != s.lease {
				http.Error(w, "lease mismatch", http.StatusPreconditionFailed)
				return
			}
			s.lease = ""
		default:
			http.Error(w, "unsupported lease operation", http.StatusBadRequest)
		}
		return
	}
	switch r.Method {
	case http.MethodGet:
		body, exists := s.objects[key]
		if !exists {
			http.Error(w, "missing", http.StatusNotFound)
			return
		}
		w.Header().Set("ETag", s.etags[key])
		if s.corruptKey == key {
			body = append(bytes.Clone(body), 'x')
		}
		_, _ = w.Write(body)
	case http.MethodPut:
		if r.Header.Get("x-ms-blob-type") != "BlockBlob" {
			http.Error(w, "missing Blob type", http.StatusBadRequest)
			return
		}
		if r.Header.Get("If-None-Match") == "*" && s.objects[key] != nil {
			http.Error(w, "exists", http.StatusPreconditionFailed)
			return
		}
		if key == "active.json" && s.objects[key] != nil {
			if !activeLease || r.Header.Get("x-ms-lease-id") != s.lease {
				http.Error(w, "lease mismatch", http.StatusPreconditionFailed)
				return
			}
			if r.Header.Get("If-Match") != s.etags[key] {
				http.Error(w, "stale ETag", http.StatusPreconditionFailed)
				return
			}
		} else if r.Header.Get("If-None-Match") != "*" {
			http.Error(w, "immutable object requires create-only", http.StatusBadRequest)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 128<<20+1))
		if err != nil || int64(len(body)) != r.ContentLength {
			http.Error(w, "bad body", http.StatusBadRequest)
			return
		}
		s.counter++
		s.objects[key], s.etags[key] = body, fmt.Sprintf(`"%d"`, s.counter)
		s.uploads[key]++
		w.Header().Set("ETag", s.etags[key])
		if s.loseKey == key {
			s.loseKey = ""
			http.Error(w, "synthetic lost response after commit", http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusCreated)
	case http.MethodDelete:
		if s.objects[key] == nil {
			http.Error(w, "missing", http.StatusNotFound)
			return
		}
		delete(s.objects, key)
		delete(s.etags, key)
		w.WriteHeader(http.StatusAccepted)
	default:
		http.Error(w, "unsupported method", http.StatusMethodNotAllowed)
	}
}

func respond(w http.ResponseWriter, columns []string, rows [][]any, complete bool) {
	type column struct{ ColumnName string }
	type table struct {
		TableName string
		Columns   []column
		Rows      [][]any
	}
	primary := table{TableName: "PrimaryResult", Rows: rows}
	for _, name := range columns {
		primary.Columns = append(primary.Columns, column{name})
	}
	tables := []table{primary}
	if complete {
		tables = append(tables, table{TableName: "QueryStatus",
			Columns: []column{{"Severity"}, {"StatusCode"}}, Rows: [][]any{{4, 0}}})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"Tables": tables})
}

type protocolVerifier struct{}

func (protocolVerifier) Verify(_ context.Context, path string, file sessionarchive.File) error {
	content, err := os.ReadFile(path) // #nosec G304 -- Test verifier reads generated private staging fixtures.
	if err != nil || digest(content) != file.Digest || int64(len(content)) != file.Bytes {
		return ErrIntegrity
	}
	return nil
}

func validConfig() Config {
	c := Config{Enabled: true, Account: "privatearchive", Container: "container", Prefix: "archive",
		Endpoint: "https://archive.westus.kusto.windows.net", Database: "Archive", Table: "Events",
		Namespace: "private", RepositoryIDs: []string{"octo/api"},
		TenantID: "11111111-1111-1111-1111-111111111111", ClientID: "22222222-2222-2222-2222-222222222222",
		ClientSecret: "synthetic-credential-not-logged", DataApproved: true, SingleReplica: true,
		ExternalPropertiesDigest: digest([]byte(protocolProperties))}
	_ = c.Validate()
	c.Acceptance = c.AcceptanceBinding()
	return c
}

func newProtocol(t *testing.T) (*Archive, *blobProtocol, string) {
	t.Helper()
	protocol := &blobProtocol{objects: map[string][]byte{}, etags: map[string]string{},
		uploads: map[string]int{}, now: time.Now().UTC()}
	server := httptest.NewServer(protocol)
	t.Cleanup(server.Close)
	a := protocolArchive(t, protocol, server.URL)
	return a, protocol, server.URL
}

func protocolArchive(t *testing.T, s *blobProtocol, endpoint string) *Archive {
	t.Helper()
	c := validConfig()
	c.Endpoint = endpoint // Only test construction permits numeric-loopback HTTP.
	a := &Archive{config: c, converter: protocolVerifier{}, blob: &blobClient{root: endpoint + "/container/archive",
		http: &http.Client{CheckRedirect: rejectRedirect}},
		queryHTTP: &http.Client{CheckRedirect: rejectRedirect}, readers: map[string]reader{},
		now: func() time.Time { s.mu.Lock(); defer s.mu.Unlock(); return s.now }}
	if err := a.open(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = a.Close() })
	return a
}

func fixture(t *testing.T, generation uint64, version string) sessionarchive.Manifest {
	t.Helper()
	path := filepath.Join(t.TempDir(), "packed.parquet")
	content := []byte("synthetic Blob protocol bytes, not real Parquet: " + version)
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	m := sessionarchive.Member{Namespace: "private", Repository: "octo/api", RunID: "123", Attempt: 1,
		ID: digest([]byte("stable trace")), Version: digest([]byte(version)), Events: 2}
	f := sessionarchive.File{Path: path, Digest: digest(content), Bytes: int64(len(content)), Rows: 2, Members: []sessionarchive.Member{m}}
	return sessionarchive.Manifest{Version: 1, Generation: generation, CreatedAt: time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC),
		Files: []sessionarchive.File{f}, Members: []sessionarchive.Member{m}}
}

func TestBlobImmutablePublicationRestartAndOverlap(t *testing.T) {
	a, s, endpoint := newProtocol(t)
	m := fixture(t, 1, "first")
	key := objectKey(m.Files[0])
	s.loseKey = key
	if err := a.Publish(t.Context(), m); err == nil {
		t.Fatal("ambiguous object upload must retain durable staging")
	}
	if a.active.Generation != 0 || len(a.state.Pending) != 1 {
		t.Fatal("failed upload was published or orphan inventory was lost")
	}
	if err := a.Publish(t.Context(), m); err != nil {
		t.Fatal(err)
	}
	if err := a.Publish(t.Context(), m); err != nil || s.uploads[key] != 1 {
		t.Fatal("retry was not idempotent")
	}
	if bytes.Contains(s.objects["manifests/"+a.active.State+".json"], []byte(m.Files[0].Path)) {
		t.Fatal("private staging path leaked into remote manifest")
	}
	competing := &Archive{config: a.config, blob: a.blob, queryHTTP: a.queryHTTP, readers: map[string]reader{}, now: a.now}
	if err := competing.open(t.Context()); !errors.Is(err, ErrLease) {
		t.Fatalf("restart overlap was not rejected: %v", err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	restarted := protocolArchive(t, s, endpoint)
	if restarted.active.Generation != 1 || len(restarted.state.Manifest.Members) != 1 {
		t.Fatal("durable Blob generation not recovered")
	}
	if err := restarted.Publish(t.Context(), m); err != nil {
		t.Fatal(err)
	}
}

func TestBlobPointerLostAcknowledgementAndStaleWriter(t *testing.T) {
	a, s, _ := newProtocol(t)
	s.loseKey = "active.json"
	m := fixture(t, 1, "first")
	if err := a.Publish(t.Context(), m); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.etags["active.json"] = `"outside-writer"`
	s.mu.Unlock()
	if err := a.Publish(t.Context(), fixture(t, 2, "second")); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale ETag was accepted: %v", err)
	}
}

func TestBlobIntegrityFailuresNeverPublish(t *testing.T) {
	a, s, _ := newProtocol(t)
	m := fixture(t, 1, "first")
	s.corruptKey = objectKey(m.Files[0])
	if err := a.Publish(t.Context(), m); !errors.Is(err, ErrIntegrity) || a.active.Generation != 0 {
		t.Fatal("corrupt destination became ready")
	}
	s.corruptKey = ""
	m.Files[0].Members[0].Repository = "forbidden/repo"
	if err := a.Publish(t.Context(), m); !errors.Is(err, ErrScope) {
		t.Fatal("cross-repository physical file admitted")
	}
}

func TestBlobCorrectionWithdrawalAndRetirement(t *testing.T) {
	a, s, endpoint := newProtocol(t)
	first := fixture(t, 1, "first")
	if err := a.Publish(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	scope := sessionarchive.Scope{Namespace: "private", Repositories: []string{"octo/api"}}
	queryCtx, _, _, _, finish, err := a.pin(t.Context(), scope, "octo/api", "123", 1)
	if err != nil {
		t.Fatal(err)
	}
	second := fixture(t, 2, "second")
	if err := a.Publish(t.Context(), second); err != nil {
		t.Fatal(err)
	}
	if queryCtx.Err() != nil {
		t.Fatal("ordinary correction cancelled a pinned reader")
	}
	if err := a.Maintain(t.Context()); err != nil || s.objects[objectKey(first.Files[0])] == nil {
		t.Fatal("retirement deleted pinned data")
	}
	if err := finish(); err != nil {
		t.Fatal(err)
	}
	stale := first
	stale.Generation = 3
	if err := a.Publish(t.Context(), stale); !errors.Is(err, ErrWithdrawn) {
		t.Fatalf("superseded projection resurrected: %v", err)
	}
	withdraw := sessionarchive.Manifest{Version: 1, Generation: 3, CreatedAt: second.CreatedAt.Add(time.Second)}
	if err := a.Publish(t.Context(), withdraw); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	restarted := protocolArchive(t, s, endpoint)
	second.Generation = 4
	if err := restarted.Publish(t.Context(), second); !errors.Is(err, ErrWithdrawn) {
		t.Fatal("withdrawal fence did not survive restart")
	}
	s.mu.Lock()
	s.now = s.now.Add(25 * time.Hour)
	// Represents time passing with a still-owned renewed lease.
	s.expires = s.now.Add(time.Minute)
	s.mu.Unlock()
	if err := restarted.Maintain(t.Context()); err != nil {
		t.Fatal(err)
	}
	if s.objects[objectKey(first.Files[0])] != nil || s.objects[objectKey(second.Files[0])] != nil {
		t.Fatal("eligible unreferenced objects were not retired")
	}
}

func TestBlobWithdrawalCancelsAndDeniesInflightResults(t *testing.T) {
	a, s, _ := newProtocol(t)
	m := fixture(t, 1, "first")
	if err := a.Publish(t.Context(), m); err != nil {
		t.Fatal(err)
	}
	scope := sessionarchive.Scope{Namespace: "private", Repositories: []string{"octo/api"}}
	ctx, _, _, _, finish, err := a.pin(t.Context(), scope, "octo/api", "123", 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.WithdrawRepository(t.Context(), "octo/api"); err != nil {
		t.Fatal(err)
	}
	if ctx.Err() == nil || !errors.Is(finish(), ErrWithdrawn) {
		t.Fatal("withdrawal did not cancel and deny pinned result")
	}
	// Failed withdrawal persistence cannot permit subsequent reads.
	a, _, _ = newProtocol(t)
	if err := a.Publish(t.Context(), m); err != nil {
		t.Fatal(err)
	}
	_ = s
	a.etag = `"stale"`
	if err := a.WithdrawRepository(t.Context(), "octo/api"); err == nil || !a.lost.Load() {
		t.Fatal("unacknowledged withdrawal remained readable")
	}
}

func TestBlobLeaseExpiryAndControlBudgetFailClosed(t *testing.T) {
	a, s, _ := newProtocol(t)
	s.mu.Lock()
	s.now = s.now.Add(61 * time.Second)
	s.mu.Unlock()
	if err := a.Publish(t.Context(), fixture(t, 1, "first")); !errors.Is(err, ErrLease) {
		t.Fatal("expired owner still published")
	}
	a, _, _ = newProtocol(t)
	a.active.Revisions = 1024
	if err := a.Publish(t.Context(), fixture(t, 1, "first")); !errors.Is(err, ErrCapacity) {
		t.Fatal("immutable state budget unbounded")
	}
}

func TestBlobDefaultOffAndDestinationValidation(t *testing.T) {
	config, err := FromEnv(func(string) string { return "" })
	if err != nil || config.Enabled {
		t.Fatal("archive not default-off")
	}
	a, err := New(t.Context(), config, nil)
	if err != nil || !errors.Is(a.Publish(t.Context(), sessionarchive.Manifest{}), ErrDisabled) {
		t.Fatal("disabled archive did I/O")
	}
	for _, modify := range []func(*Config){
		func(c *Config) { c.Account = "account?sig=secret" },
		func(c *Config) { c.Prefix = "../outside" },
		func(c *Config) { c.Endpoint = "https://valid.kusto.windows.net@evil.test" },
		func(c *Config) { c.Endpoint += "?sig=secret" },
		func(c *Config) { c.Endpoint = "http://127.0.0.1:8080" },
		func(c *Config) { c.Endpoint = "https://ingest-archive.kusto.windows.net" },
		func(c *Config) { c.RepositoryIDs = []string{""} },
		func(c *Config) { c.SingleReplica = false },
		func(c *Config) { c.DataApproved = false },
	} {
		c := validConfig()
		modify(&c)
		if c.Validate() == nil {
			t.Fatal("unsafe destination accepted")
		}
	}
	c := validConfig()
	c.Acceptance = ""
	if _, err := New(t.Context(), c, nil); !errors.Is(err, ErrConformance) {
		t.Fatal("missing live evidence gate accepted")
	}
}

func TestBlobPublicContainerAndRedirectRejected(t *testing.T) {
	a, s, _ := newProtocol(t)
	s.public = true
	if err := a.blob.private(t.Context()); err == nil {
		t.Fatal("public container accepted")
	}
	redirected := false
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { redirected = true }))
	defer destination.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	b := blobClient{root: source.URL, http: &http.Client{CheckRedirect: rejectRedirect}}
	if _, _, err := b.get(t.Context(), "object", 4096); err == nil || redirected {
		t.Fatal("credential-bearing redirect followed")
	}
}

func TestBlobRepositoryWithdrawalFencesUnpublishedAndFutureAttempts(t *testing.T) {
	a, s, endpoint := newProtocol(t)
	m := fixture(t, 1, "pending")
	s.loseKey = objectKey(m.Files[0])
	if err := a.Publish(t.Context(), m); err == nil || len(a.state.Pending) != 1 {
		t.Fatal("test did not establish durable unpublished inventory")
	}
	if err := a.WithdrawRepository(t.Context(), "octo/api"); err != nil {
		t.Fatal(err)
	}
	if err := a.Publish(t.Context(), m); !errors.Is(err, ErrWithdrawn) {
		t.Fatal("repository withdrawal permitted an unpublished retry")
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	restarted := protocolArchive(t, s, endpoint)
	m.Files[0].Members[0].RunID = "999"
	m.Files[0].Members[0].Attempt = 2
	m.Members = slices.Clone(m.Files[0].Members)
	if err := restarted.Publish(t.Context(), m); !errors.Is(err, ErrWithdrawn) {
		t.Fatal("repository withdrawal did not survive for unseen future attempts")
	}
	empty, emptyProtocol, emptyEndpoint := newProtocol(t)
	if err := empty.WithdrawRepository(t.Context(), "octo/api"); err != nil {
		t.Fatal(err)
	}
	if err := empty.Close(); err != nil {
		t.Fatal(err)
	}
	empty = protocolArchive(t, emptyProtocol, emptyEndpoint)
	if err := empty.Publish(t.Context(), fixture(t, 1, "never-admitted")); !errors.Is(err, ErrWithdrawn) {
		t.Fatal("empty-repository withdrawal did not persist")
	}
}
