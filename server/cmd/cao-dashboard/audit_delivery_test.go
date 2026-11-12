package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/auditdelivery"
	"github.com/githubnext/gh-cao/server/internal/operational"
	"github.com/githubnext/gh-cao/server/internal/server"
)

func TestAuditDeliverReportsConfigurationFailure(t *testing.T) {
	t.Setenv("CAO_AUDIT_DELIVERY_URL", "https://user:secret@example.test")
	t.Setenv("CAO_AUDIT_DELIVERY_TOKEN", "secret-token")
	cmd := newRootCommand()
	var stdout, stderr bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	cmd.SetArgs([]string{"audit", "deliver", "--stream", "deployment-namespace"})
	err := cmd.Execute()
	if err == nil || stdout.Len() != 0 || strings.Contains(stderr.String(), "secret") || strings.Contains(err.Error(), "user:") {
		t.Fatalf("unsafe failure: out=%q err=%q error=%v", stdout.String(), stderr.String(), err)
	}
	var result auditdelivery.Result
	if err := json.NewDecoder(&stderr).Decode(&result); err != nil || result.Status != "failure" || result.Stage != "configuration" {
		t.Fatalf("missing observable failure: %+v %v", result, err)
	}
}

func TestAuditDeliverDoesNotBootstrap(t *testing.T) {
	t.Setenv("CAO_AUDIT_DELIVERY_URL", "https://127.0.0.1:1")
	t.Setenv("CAO_AUDIT_DELIVERY_TOKEN", "local-test-token")
	_, dsn, _ := operationalCommandFixture(t)
	t.Setenv("CAO_OPERATIONAL_NAMESPACE", "not-provisioned")
	for range 2 {
		cmd := newRootCommand()
		var stdout, stderr bytes.Buffer
		cmd.SetOut(&stdout)
		cmd.SetErr(&stderr)
		cmd.SetArgs([]string{"audit", "deliver", "--stream", "local"})
		err := cmd.Execute()
		var result auditdelivery.Result
		if err == nil || stdout.Len() != 0 {
			t.Fatalf("missing schema accepted: %q, %v", stdout.String(), err)
		}
		if err := json.NewDecoder(&stderr).Decode(&result); err != nil || result.Stage != "source_open" || result.Status != "failure" {
			t.Fatalf("missing source failure result: %+v, %v", result, err)
		}
		db, err := sql.Open("pgx", dsn)
		if err != nil {
			t.Fatal(err)
		}
		var count int
		err = db.QueryRowContext(t.Context(), `SELECT count(*) FROM cao_operational_namespaces WHERE namespace='not-provisioned'`).Scan(&count)
		_ = db.Close()
		if err != nil || count != 0 {
			t.Fatalf("delivery initialized its source: count=%d err=%v", count, err)
		}
	}
}

func TestAuditDeliverPostgresReadOnlyEndToEnd(t *testing.T) {
	_, dsn, namespace := operationalCommandFixture(t)
	seedPostgresAuditHistory(t, dsn, namespace, time.Now().Add(-2*operational.AuditRetention), 3)
	t.Setenv("CAO_POSTGRES_URL", dsn+"&default_transaction_read_only=on")
	reader, closeReader, err := server.OpenAuditReader(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = closeReader() }()
	head, err := reader.VerifyAudit(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := reader.AppendAudit(t.Context(), operational.AuditEvent{Event: "test.denied", Outcome: "success"}); err == nil {
		t.Fatal("delivery reader can write")
	}
	anchor := auditdelivery.Anchor{Stream: "local", ObservedAt: time.Now().UTC(),
		Head: operational.AuditAnchor{Sequence: head.LastSequence, Hash: head.LastHash}}
	body, err := json.Marshal(anchor)
	if err != nil {
		t.Fatal(err)
	}
	anchorPath := filepath.Join(t.TempDir(), "anchor.json")
	if err := os.WriteFile(anchorPath, body, 0o600); err != nil {
		t.Fatal(err)
	}
	destination, checkpoint := localAuditDeliveryDestination(t)
	t.Setenv("CAO_AUDIT_DELIVERY_URL", destination.URL)
	t.Setenv("CAO_AUDIT_DELIVERY_TOKEN", "local-test-token")
	// Test-only trust configuration; production still verifies the platform
	// trust store and exposes no insecure-TLS switch.
	transport := http.DefaultTransport
	http.DefaultTransport = destination.Client().Transport
	t.Cleanup(func() { http.DefaultTransport = transport })

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(t.Context(), `SELECT namespace FROM cao_operational_namespaces WHERE namespace=$1 FOR UPDATE`, namespace); err != nil {
		t.Fatal(err)
	}
	// The complete command must succeed while another process could be
	// writing. An accidental read-write verifier would require this lock.
	for index, want := range []int64{2, 3, 3} {
		cmd := newRootCommand()
		var stdout, stderr bytes.Buffer
		cmd.SetOut(&stdout)
		cmd.SetErr(&stderr)
		args := []string{"--stream", "local", "--max-events", "2", "--timeout", "5s"}
		if index == 0 {
			args = append(args, "--bootstrap-anchor", anchorPath)
		}
		cmd.SetArgs(append([]string{"audit", "deliver"}, args...))
		if err := cmd.Execute(); err != nil {
			t.Fatalf("local delivery: %v; %s", err, stderr.String())
		}
		var result auditdelivery.Result
		if err := json.Unmarshal(stdout.Bytes(), &result); err != nil || result.Status != "success" ||
			result.LastSequence != want || result.VerifiedHead != 3 || result.Remaining != 3-want {
			t.Fatalf("local delivery result: %+v, %v", result, err)
		}
	}
	saved := checkpoint()
	if saved.Delivered.Sequence != 3 || saved.Verification.LastHash != head.LastHash {
		t.Fatalf("independent checkpoint did not preserve source head: %+v", saved)
	}
	after, err := reader.VerifyAudit(t.Context())
	if err != nil || after != head {
		t.Fatalf("delivery appended events or retained expired history: %+v, %v", after, err)
	}
}

func localAuditDeliveryDestination(t *testing.T) (*httptest.Server, func() auditdelivery.Checkpoint) {
	t.Helper()
	var mu sync.Mutex
	objects := map[string][]byte{}
	versions := map[string]string{}
	var revision int
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer local-test-token" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		body, exists := objects[r.URL.Path]
		switch r.Method {
		case http.MethodGet:
			if !exists {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			w.Header().Set("ETag", versions[r.URL.Path])
			_, _ = w.Write(body)
		case http.MethodPut:
			if r.Header.Get("If-None-Match") == "*" {
				if exists {
					w.WriteHeader(http.StatusPreconditionFailed)
					return
				}
			} else if !exists || r.Header.Get("If-Match") != versions[r.URL.Path] {
				w.WriteHeader(http.StatusPreconditionFailed)
				return
			}
			value, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 128<<10))
			if err != nil || !json.Valid(value) {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			revision++
			objects[r.URL.Path] = value
			versions[r.URL.Path] = fmt.Sprintf(`"%d"`, revision)
			w.WriteHeader(http.StatusCreated)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, func() auditdelivery.Checkpoint {
		mu.Lock()
		defer mu.Unlock()
		var checkpoint auditdelivery.Checkpoint
		if err := json.Unmarshal(objects["/local/checkpoint.json"], &checkpoint); err != nil {
			t.Fatal(err)
		}
		return checkpoint
	}
}

func TestBootstrapAnchorReadFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "anchor.json")
	if _, err := readBootstrapAnchor(filepath.Dir(path)); err == nil {
		t.Fatal("non-regular bootstrap input accepted")
	}
	if _, err := readBootstrapAnchor(path); err == nil {
		t.Fatal("missing bootstrap anchor accepted")
	}
	for _, body := range []string{"{}", strings.Repeat("x", 17<<10), `{"head":`} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		anchor, err := readBootstrapAnchor(path)
		if body != "{}" && err == nil {
			t.Fatal("invalid bootstrap accepted")
		}
		if body == "{}" && anchor.Head.Sequence != 0 {
			t.Fatal("empty object became trusted anchor")
		}
	}
}

func TestBuildDeliveryOptionsBounds(t *testing.T) {
	dir := t.TempDir()
	anchorPath := filepath.Join(dir, "anchor.json")
	if err := os.WriteFile(anchorPath, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	badPath := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(badPath, []byte(`not json`), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name      string
		stream    string
		maxEvents int
		maxAge    time.Duration
		timeout   time.Duration
		bootstrap string
		wantErr   string
		wantBoot  bool
	}{
		{name: "valid", stream: "prod", maxEvents: 100, maxAge: time.Hour, timeout: time.Minute},
		{name: "valid with bootstrap", stream: "prod", maxEvents: 1, maxAge: time.Minute, timeout: time.Second, bootstrap: anchorPath, wantBoot: true},
		{name: "timeout too short", stream: "prod", maxEvents: 100, maxAge: time.Hour, timeout: time.Millisecond, wantErr: "timeout"},
		{name: "timeout too long", stream: "prod", maxEvents: 100, maxAge: time.Hour, timeout: 31 * time.Minute, wantErr: "timeout"},
		{name: "bad stream", stream: "bad stream!", maxEvents: 100, maxAge: time.Hour, timeout: time.Minute, wantErr: "stream"},
		{name: "bad max events", stream: "prod", maxEvents: 0, maxAge: time.Hour, timeout: time.Minute, wantErr: "max-events"},
		{name: "bad anchor age", stream: "prod", maxEvents: 5, maxAge: time.Second, timeout: time.Minute, wantErr: "max-anchor-age"},
		{name: "invalid bootstrap", stream: "prod", maxEvents: 5, maxAge: time.Hour, timeout: time.Minute, bootstrap: badPath, wantErr: "bootstrap"},
		{name: "missing bootstrap", stream: "prod", maxEvents: 5, maxAge: time.Hour, timeout: time.Minute, bootstrap: filepath.Join(dir, "none.json"), wantErr: "bootstrap"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			options, err := buildDeliveryOptions(tc.stream, tc.maxEvents, tc.maxAge, tc.timeout, tc.bootstrap)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error = %v, want containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if options.Stream != tc.stream || options.MaxEvents != tc.maxEvents || options.MaxAnchorAge != tc.maxAge {
				t.Fatalf("options = %+v", options)
			}
			if (options.Bootstrap != nil) != tc.wantBoot {
				t.Fatalf("bootstrap set = %v, want %v", options.Bootstrap != nil, tc.wantBoot)
			}
		})
	}
}
