package auditdelivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/operational"
	"github.com/githubnext/gh-cao/server/internal/operational/postgres"
	"github.com/githubnext/gh-cao/server/internal/testutil"
)

type objectFixture struct {
	mu      sync.Mutex
	objects map[string][]byte
	etags   map[string]string
	writes  map[string]int
	reject  func(*http.Request) bool
	counter int
}

func newFixture(t *testing.T) (*objectFixture, *HTTPDestination) {
	t.Helper()
	fixture := &objectFixture{objects: map[string][]byte{}, etags: map[string]string{}, writes: map[string]int{}}
	srv := httptest.NewTLSServer(http.HandlerFunc(fixture.serve))
	t.Cleanup(srv.Close)
	destination, err := NewHTTPDestination(srv.URL, "test-only-token")
	if err != nil {
		t.Fatal(err)
	}
	destination.client.Transport = srv.Client().Transport
	return fixture, destination
}

func (f *objectFixture) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer test-only-token" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if f.reject != nil && f.reject(r) {
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	body, exists := f.objects[r.URL.Path]
	switch r.Method {
	case http.MethodGet:
		if !exists {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("ETag", f.etags[r.URL.Path])
		_, _ = w.Write(body)
	case http.MethodPut:
		if r.Header.Get("If-None-Match") == "*" {
			if exists {
				w.WriteHeader(http.StatusPreconditionFailed)
				return
			}
		} else if r.Header.Get("If-Match") == "" || r.Header.Get("If-Match") != f.etags[r.URL.Path] {
			w.WriteHeader(http.StatusPreconditionFailed)
			return
		}
		var value json.RawMessage
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxObjectBytes)).Decode(&value); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		f.counter++
		f.objects[r.URL.Path] = value
		f.etags[r.URL.Path] = fmt.Sprintf(`"%d"`, f.counter)
		f.writes[r.URL.Path]++
		w.WriteHeader(http.StatusCreated)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func seededSource(t *testing.T, count int) (*postgres.Store, Options) {
	t.Helper()
	store, _, _ := testutil.OperationalPostgres(t)
	for range count {
		if _, err := store.AppendAudit(t.Context(), operational.AuditEvent{Event: "cli_command.admitted", Outcome: "success"}); err != nil {
			t.Fatal(err)
		}
	}
	verified, err := store.VerifyAudit(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	options := Options{Stream: "deployment-namespace", MaxEvents: 2, MaxAnchorAge: time.Hour,
		Bootstrap: &Anchor{Stream: "deployment-namespace", ObservedAt: time.Now().UTC(),
			Head: operational.AuditAnchor{Sequence: verified.LastSequence, Hash: verified.LastHash}}}
	return store, options
}

func TestRestartAfterDeliveryBeforeCheckpoint(t *testing.T) {
	for _, stage := range []string{"event", "verification", "checkpoint", "checkpoint_readback"} {
		t.Run(stage, func(t *testing.T) {
			store, options := seededSource(t, 3)
			fixture, destination := newFixture(t)
			fixture.reject = func(r *http.Request) bool {
				switch stage {
				case "event":
					return r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "00000000000000000002.json")
				case "verification":
					return r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/verifications/")
				case "checkpoint":
					return r.Method == http.MethodPut && strings.HasSuffix(r.URL.Path, "/checkpoint.json")
				default:
					return r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/checkpoint.json") && fixture.writes[r.URL.Path] > 0
				}
			}
			result, err := Run(t.Context(), store, destination, options)
			if err == nil || result.Status != "failure" {
				t.Fatalf("fault was successful: %+v %v", result, err)
			}
			fixture.mu.Lock()
			fixture.reject = nil
			fixture.mu.Unlock()
			// No exporter process state is retained across these invocations.
			result, err = Run(t.Context(), store, destination, options)
			if err != nil || result.Status != "success" {
				t.Fatalf("restart failed: %+v %v", result, err)
			}
			result, err = Run(t.Context(), store, destination, options)
			if err != nil || result.LastSequence != 3 || result.Remaining != 0 {
				t.Fatalf("catch-up failed: %+v %v", result, err)
			}
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			for sequence := 1; sequence <= 3; sequence++ {
				key := fmt.Sprintf("/%s/events/%020d.json", options.Stream, sequence)
				if fixture.writes[key] != 1 {
					t.Fatalf("sequence %d stored %d times", sequence, fixture.writes[key])
				}
			}
		})
	}
}

func TestBoundedPageAndIdleVerification(t *testing.T) {
	store, options := seededSource(t, 3)
	fixture, destination := newFixture(t)
	result, err := Run(t.Context(), store, destination, options)
	if err != nil || result.Delivered != 2 || result.LastSequence != 2 || result.VerifiedHead != 3 || result.Remaining != 1 {
		t.Fatalf("page result: %+v %v", result, err)
	}
	options.Bootstrap = nil
	if _, err := Run(t.Context(), store, destination, options); err != nil {
		t.Fatal(err)
	}
	result, err = Run(t.Context(), store, destination, options)
	if err != nil || result.Delivered != 0 || result.LastSequence != 3 {
		t.Fatalf("idle verification: %+v %v", result, err)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	evidence := 0
	for key := range fixture.objects {
		if strings.Contains(key, "/verifications/") {
			evidence++
		}
	}
	if evidence != 3 {
		t.Fatalf("wanted independent evidence for every run; got %d", evidence)
	}
}

type sourceOverride struct {
	Reader
	list   func(context.Context, operational.AuditFilter) ([]operational.AuditEvent, error)
	verify func(context.Context, ...operational.AuditAnchor) (operational.AuditVerification, error)
}

func (s sourceOverride) ListAudit(ctx context.Context, filter operational.AuditFilter) ([]operational.AuditEvent, error) {
	if s.list != nil {
		return s.list(ctx, filter)
	}
	return s.Reader.ListAudit(ctx, filter)
}

func (s sourceOverride) VerifyAudit(ctx context.Context, anchors ...operational.AuditAnchor) (operational.AuditVerification, error) {
	if s.verify != nil {
		return s.verify(ctx, anchors...)
	}
	return s.Reader.VerifyAudit(ctx, anchors...)
}

func TestAnchorAndRetentionFailuresWriteNothing(t *testing.T) {
	for _, fault := range []string{"missing", "stale", "future", "namespace", "rewrite", "gap", "expired", "interrupted", "retention_race", "source_error", "empty"} {
		t.Run(fault, func(t *testing.T) {
			store, options := seededSource(t, 3)
			fixture, destination := newFixture(t)
			source := sourceOverride{Reader: store}
			switch fault {
			case "missing":
				options.Bootstrap = nil
			case "stale":
				options.Bootstrap.ObservedAt = time.Now().Add(-2 * time.Hour)
			case "future":
				options.Bootstrap.ObservedAt = time.Now().Add(time.Hour)
			case "namespace":
				options.Bootstrap.Stream = "other-namespace"
			case "rewrite":
				options.Bootstrap.Head.Hash = strings.Repeat("f", 64)
			case "gap":
				source.list = func(ctx context.Context, filter operational.AuditFilter) ([]operational.AuditEvent, error) {
					filter.AfterSequence++
					return store.ListAudit(ctx, filter)
				}
			case "source_error":
				source.list = func(context.Context, operational.AuditFilter) ([]operational.AuditEvent, error) {
					return nil, errors.New("secret-bearing source error")
				}
			case "empty":
				source.list = func(context.Context, operational.AuditFilter) ([]operational.AuditEvent, error) {
					return []operational.AuditEvent{}, nil
				}
			default:
				source.verify = func(ctx context.Context, anchors ...operational.AuditAnchor) (operational.AuditVerification, error) {
					if fault == "interrupted" {
						return operational.AuditVerification{LastSequence: 2}, context.Canceled
					}
					value, err := store.VerifyAudit(ctx, anchors...)
					if fault == "expired" {
						value.AnchorsExpired = 1
					} else {
						value.FirstSequence = 2
					}
					return value, err
				}
			}
			result, err := Run(t.Context(), source, destination, options)
			if err == nil || result.Status != "failure" {
				t.Fatalf("fault accepted: %+v %v", result, err)
			}
			if strings.Contains(err.Error(), "secret-bearing") {
				t.Fatal("source error leaked")
			}
			fixture.mu.Lock()
			defer fixture.mu.Unlock()
			if len(fixture.objects) != 0 {
				t.Fatal("wrote evidence after failed verification")
			}
		})
	}
}

func TestChangedRowsAndConcurrentAppend(t *testing.T) {
	for _, changed := range []bool{true, false} {
		t.Run(fmt.Sprint(changed), func(t *testing.T) {
			store, options := seededSource(t, 3)
			_, destination := newFixture(t)
			source := sourceOverride{Reader: store, list: func(ctx context.Context, filter operational.AuditFilter) ([]operational.AuditEvent, error) {
				rows, err := store.ListAudit(ctx, filter)
				if err != nil {
					return nil, err
				}
				if changed {
					rows[0].Actor = "tampered"
				} else if _, err := store.AppendAudit(ctx, operational.AuditEvent{Event: "cli_command.admitted", Outcome: "success"}); err != nil {
					return nil, err
				}
				return rows, nil
			}}
			result, err := Run(t.Context(), source, destination, options)
			if changed && err == nil {
				t.Fatal("accepted export page that did not match verified chain")
			}
			if !changed && (err != nil || result.LastSequence != 2 || result.VerifiedHead != 4 || result.Remaining != 2) {
				t.Fatalf("append high-water mark: %+v %v", result, err)
			}
		})
	}
}

func TestExpiredCheckpointDoesNotRebootstrap(t *testing.T) {
	store, options := seededSource(t, 3)
	fixture, destination := newFixture(t)
	if _, err := Run(t.Context(), store, destination, options); err != nil {
		t.Fatal(err)
	}
	checkpoint, _, _, err := destination.Load(t.Context(), options.Stream)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint.VerifiedAt = time.Now().Add(-2 * time.Hour)
	body, err := json.Marshal(checkpoint)
	if err != nil {
		t.Fatal(err)
	}
	fixture.mu.Lock()
	fixture.objects["/"+options.Stream+"/checkpoint.json"] = body
	writes := fixture.counter
	fixture.mu.Unlock()
	if _, err := Run(t.Context(), store, destination, options); err == nil {
		t.Fatal("stale checkpoint silently rebaselined with bootstrap anchor")
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if fixture.counter != writes {
		t.Fatal("stale checkpoint changed destination")
	}
}

func TestInterruptionDuringVerification(t *testing.T) {
	store, options := seededSource(t, 3)
	fixture, destination := newFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	source := sourceOverride{Reader: store, verify: func(ctx context.Context, _ ...operational.AuditAnchor) (operational.AuditVerification, error) {
		timer := time.AfterFunc(time.Millisecond, cancel)
		defer timer.Stop()
		<-ctx.Done()
		return operational.AuditVerification{LastSequence: 1}, ctx.Err()
	}}
	result, err := Run(ctx, source, destination, options)
	if err == nil || result.Stage != "verification" {
		t.Fatalf("interrupted verification succeeded: %+v %v", result, err)
	}
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if len(fixture.objects) != 0 {
		t.Fatal("interrupted verification advanced evidence")
	}
}

func TestOptionsBounds(t *testing.T) {
	base := Options{Stream: "deployment-namespace", MaxEvents: 1, MaxAnchorAge: time.Hour}
	for _, events := range []int{0, -1, 1001} {
		options := base
		options.MaxEvents = events
		if err := options.Validate(); err == nil {
			t.Fatal("unbounded event count accepted")
		}
	}
	for _, age := range []time.Duration{0, time.Second, 8 * 24 * time.Hour} {
		options := base
		options.MaxAnchorAge = age
		if err := options.Validate(); err == nil {
			t.Fatal("unbounded anchor age accepted")
		}
	}
	for _, stream := range []string{"", "../namespace", "x/y", "x?token=y", strings.Repeat("x", 129)} {
		options := base
		options.Stream = stream
		if err := options.Validate(); err == nil {
			t.Fatal("unsafe namespace key accepted")
		}
	}
}
