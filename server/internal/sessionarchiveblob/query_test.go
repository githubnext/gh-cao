package sessionarchiveblob

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/sessionarchive"
)

func TestHostedQueryContentionDoesNotWaitForPublication(t *testing.T) {
	for _, kind := range []string{"timeline", "summary", "finish"} {
		t.Run(kind, func(t *testing.T) {
			a, _, _ := newProtocol(t)
			if err := a.Publish(t.Context(), fixture(t, 1, "first")); err != nil {
				t.Fatal(err)
			}
			scope := sessionarchive.Scope{Namespace: "private", Repositories: []string{"octo/api"}}
			finish := func() error { return nil }
			if kind == "finish" {
				_, _, _, _, release, err := a.pin(t.Context(), scope, "octo/api", "123", 1)
				if err != nil {
					t.Fatal(err)
				}
				finish = func() error {
					defer func() { _ = release.Close() }()
					return release.Deliver(func() error { return nil })
				}
			}
			ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
			defer cancel()
			a.mu.Lock()
			done := make(chan error, 1)
			go func() {
				var err error
				switch kind {
				case "timeline":
					_, _, err = a.Timeline(ctx, scope, sessionarchive.TimelineRequest{Repository: "octo/api", RunID: "123", Attempt: 1, Limit: 10})
				case "summary":
					_, _, err = a.Summaries(ctx, scope, "octo/api", 10)
				case "finish":
					err = finish()
				}
				done <- err
			}()
			select {
			case err := <-done:
				if !errors.Is(err, ErrCapacity) && !errors.Is(err, context.DeadlineExceeded) {
					t.Errorf("contention did not return an explicit bounded failure: %v", err)
				}
				a.mu.Unlock()
			case <-time.After(time.Second):
				a.mu.Unlock()
				<-done
				t.Fatal("query waited behind publication beyond its deadline")
			}
			if kind == "finish" {
				_, _, _, _, release, err := a.pin(t.Context(), scope, "octo/api", "123", 1)
				if err != nil {
					t.Fatalf("failed finalization leaked its reader slot: %v", err)
				}
				t.Cleanup(func() { _ = release.Close() })
				_ = finish()
				if _, _, _, _, _, err := a.pin(t.Context(), scope, "octo/api", "123", 1); !errors.Is(err, ErrCapacity) {
					t.Fatal("repeated finalization released a newer reader")
				}
				if err := release.Deliver(func() error { return nil }); err != nil {
					t.Fatal(err)
				}
				if err := release.Close(); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestHostedCanceledQueryCannotFinalizeSuccessfully(t *testing.T) {
	a, _, _ := newProtocol(t)
	if err := a.Publish(t.Context(), fixture(t, 1, "first")); err != nil {
		t.Fatal(err)
	}
	scope := sessionarchive.Scope{Namespace: "private", Repositories: []string{"octo/api"}}
	ctx, cancel := context.WithCancel(t.Context())
	_, _, _, _, reader, err := a.pin(ctx, scope, "octo/api", "123", 1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reader.Close() })
	cancel()
	if err := reader.Deliver(func() error { return nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled query finalization claimed success: %v", err)
	}
	if _, _, _, _, _, err := a.pin(ctx, scope, "octo/api", "123", 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled query was admitted: %v", err)
	}
}

func fixtureEvents(member sessionarchive.Member) []sessionarchive.Event {
	var events []sessionarchive.Event
	for i := int64(0); i < member.Events; i++ {
		payload := `{"type":"user.message","data":{"content":"approved","null":null,"zero":0}}`
		events = append(events, sessionarchive.Event{
			Sequence: i, Type: "user.message", Namespace: member.Namespace, Repository: member.Repository,
			RunID: member.RunID, RunAttempt: member.Attempt, TraceID: member.ID, ProjectionID: member.Version,
			ContractVersion: sessionarchive.ContractVersion, PayloadJSON: payload, ContentDigest: digest([]byte(payload))})
	}
	return events
}

func TestHostedTimelineAndSummaryProtocol(t *testing.T) {
	a, s, _ := newProtocol(t)
	m := fixture(t, 1, "first")
	if err := a.Publish(t.Context(), m); err != nil {
		t.Fatal(err)
	}
	events := fixtureEvents(m.Members[0])
	s.query = func(w http.ResponseWriter, _ *http.Request, query string) {
		if strings.Contains(query, "summarize") {
			member := m.Members[0]
			respond(w, []string{"Repository", "RunId", "RunAttempt", "ProjectionId", "Events"},
				[][]any{{member.Repository, member.RunID, member.Attempt, member.Version, member.Events}}, true)
			return
		}
		var rows [][]any
		for _, event := range events {
			rows = append(rows, []any{event})
		}
		respond(w, []string{"Event"}, rows, true)
	}
	scope := sessionarchive.Scope{Namespace: "private", Repositories: []string{"octo/api"}}
	generation, actual, err := a.Timeline(t.Context(), scope, sessionarchive.TimelineRequest{Repository: "octo/api", RunID: "123", Attempt: 1, Limit: 10})
	if err != nil || generation != 1 || !reflect.DeepEqual(actual, events) {
		t.Fatalf("approved evidence not reconstructed: %v", err)
	}
	generation, summary, err := a.Summaries(t.Context(), scope, "octo/api", 10)
	if err != nil || generation != 1 || len(summary) != 1 || summary[0].ProjectionID != m.Members[0].Version {
		t.Fatalf("selected membership not summarized: %#v %v", summary, err)
	}
	if _, _, err := a.Timeline(t.Context(), scope, sessionarchive.TimelineRequest{Repository: "octo/api", RunID: "123", Attempt: 1, Limit: 1}); !errors.Is(err, sessionarchive.ErrLimit) {
		t.Fatal("oversized trace silently truncated")
	}
	if _, _, err := a.Timeline(t.Context(), sessionarchive.Scope{Namespace: "private", Repositories: []string{"forbidden/repo"}},
		sessionarchive.TimelineRequest{Repository: "octo/api", RunID: "123", Attempt: 1, Limit: 10}); !errors.Is(err, ErrScope) {
		t.Fatal("caller repository scope bypassed")
	}
	if _, _, err := a.Timeline(t.Context(), scope, sessionarchive.TimelineRequest{Repository: "octo/api", RunID: "999", Attempt: 1, Limit: 10}); !errors.Is(err, sessionarchive.ErrNotFound) {
		t.Fatal("missing trace not explicit")
	}
	a.config.MaxQueryBytes = 1
	if _, _, err := a.Summaries(t.Context(), scope, "octo/api", 10); !errors.Is(err, ErrCapacity) {
		t.Fatal("physical scan budget ignored")
	}
}

func TestHostedResultReaderLifetime(t *testing.T) {
	for _, kind := range []string{"timeline", "summary"} {
		for _, change := range []string{"none", "correction", "trace-withdrawal", "repository-withdrawal", "cancellation", "closed", "lease-loss"} {
			t.Run(kind+"/"+change, func(t *testing.T) {
				a, s, _ := newProtocol(t)
				first := fixture(t, 1, "first")
				if err := a.Publish(t.Context(), first); err != nil {
					t.Fatal(err)
				}
				s.query = func(w http.ResponseWriter, _ *http.Request, _ string) {
					if kind == "summary" {
						m := first.Members[0]
						respond(w, []string{"Repository", "RunId", "RunAttempt", "ProjectionId", "Events"},
							[][]any{{m.Repository, m.RunID, m.Attempt, m.Version, m.Events}}, true)
						return
					}
					var rows [][]any
					for _, event := range fixtureEvents(first.Members[0]) {
						rows = append(rows, []any{event})
					}
					respond(w, []string{"Event"}, rows, true)
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				scope := sessionarchive.Scope{Namespace: "private", Repositories: []string{"octo/api"}}
				var reader *Reader
				var err error
				if kind == "timeline" {
					_, _, reader, err = a.ReadTimeline(ctx, scope, sessionarchive.TimelineRequest{
						Repository: "octo/api", RunID: "123", Attempt: 1, Limit: 10})
				} else {
					_, _, reader, err = a.ReadSummaries(ctx, scope, "octo/api", 10)
				}
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = reader.Close() })
				if a.reader.Load() != reader.pinned {
					t.Fatal("completed query released its withdrawal-aware reader")
				}
				var expected error
				switch change {
				case "none":
				case "correction":
					err = a.Publish(t.Context(), fixture(t, 2, "correction"))
				case "trace-withdrawal":
					err = a.Publish(t.Context(), sessionarchive.Manifest{Version: 1, Generation: 2, CreatedAt: first.CreatedAt})
					expected = ErrWithdrawn
				case "repository-withdrawal":
					err = a.WithdrawRepository(t.Context(), "octo/api")
					expected = ErrWithdrawn
				case "cancellation":
					cancel()
					expected = context.Canceled
				case "closed":
					err = reader.Close()
					expected = context.Canceled
				case "lease-loss":
					a.lost.Store(true)
					expected = ErrLease
				default:
					t.Fatal("unknown reader-lifetime case")
				}
				if err != nil {
					t.Fatal(err)
				}
				delivered := false
				err = reader.Deliver(func() error {
					delivered = true
					if a.mu.TryLock() {
						a.mu.Unlock()
						t.Error("delivery was not serialized with withdrawal")
					}
					return nil
				})
				if !errors.Is(err, expected) || delivered != (expected == nil) {
					t.Fatalf("unexpected delivery: called=%t err=%v expected=%v", delivered, err, expected)
				}
				if err := reader.Close(); err != nil || a.reader.Load() != nil {
					t.Fatal("response cleanup leaked its reader")
				}
			})
		}
	}
}

func TestHostedIncompleteAndUnexpectedEvidenceRejected(t *testing.T) {
	for _, failure := range []string{"missing-event", "wrong-attempt", "wrong-version", "wrong-trace", "duplicate-sequence", "bad-payload", "partial", "missing-status", "wrong-count"} {
		t.Run(failure, func(t *testing.T) {
			a, s, _ := newProtocol(t)
			m := fixture(t, 1, "first")
			if err := a.Publish(t.Context(), m); err != nil {
				t.Fatal(err)
			}
			events := fixtureEvents(m.Members[0])
			switch failure {
			case "missing-event":
				events = events[:1]
			case "wrong-attempt":
				events[0].RunAttempt = 2
			case "wrong-version":
				events[0].ProjectionID = digest([]byte("unselected"))
			case "wrong-trace":
				events[0].TraceID = digest([]byte("unselected"))
			case "duplicate-sequence":
				events[1].Sequence = 0
			case "bad-payload":
				events[0].PayloadJSON = `{"changed":true}`
			}
			s.query = func(w http.ResponseWriter, _ *http.Request, _ string) {
				if failure == "wrong-count" {
					member := m.Members[0]
					respond(w, []string{"Repository", "RunId", "RunAttempt", "ProjectionId", "Events"},
						[][]any{{member.Repository, member.RunID, member.Attempt, member.Version, 99}}, true)
					return
				}
				if failure == "partial" {
					_, _ = w.Write([]byte(`{"Tables":[{"TableName":"PrimaryResult","Columns":[{"ColumnName":"Event"}],"Rows":[]},{"TableName":"QueryStatus","Columns":[{"ColumnName":"Severity"},{"ColumnName":"StatusCode"}],"Rows":[[2,1]]}]}`))
					return
				}
				var rows [][]any
				for _, event := range events {
					rows = append(rows, []any{event})
				}
				respond(w, []string{"Event"}, rows, failure != "missing-status")
			}
			scope := sessionarchive.Scope{Namespace: "private", Repositories: []string{"octo/api"}}
			var err error
			if failure == "wrong-count" {
				_, _, err = a.Summaries(t.Context(), scope, "octo/api", 10)
			} else {
				_, _, err = a.Timeline(t.Context(), scope, sessionarchive.TimelineRequest{Repository: "octo/api", RunID: "123", Attempt: 1, Limit: 10})
			}
			if err == nil {
				t.Fatal("incomplete or unselected evidence accepted")
			}
			if a.reader.Load() != nil {
				t.Fatal("failed query leaked its withdrawal-aware reader")
			}
		})
	}
}

func TestHostedTrustedPlanAndDefinitionBinding(t *testing.T) {
	a, _, _ := newProtocol(t)
	m := fixture(t, 1, "first")
	kql := membershipKQL("Events", m.Files, m.Members)
	if strings.Contains(kql, m.Files[0].Path) || !strings.Contains(kql, "ArchiveObject in ("+literal(m.Files[0].Digest)) ||
		!strings.Contains(kql, literal(m.Members[0].Version)) || !strings.Contains(kql, literal(m.Members[0].ID)) ||
		!strings.Contains(kql, "on Namespace,Repository,RunId,RunAttempt,ProjectionId,TraceId") {
		t.Fatal("plan not exact object and complete canonical membership bound")
	}

	a.config.ExternalPropertiesDigest = digest([]byte("changed table definition"))
	if err := a.verifyTable(t.Context()); !errors.Is(err, ErrConformance) {
		t.Fatal("mutated external table definition was trusted")
	}
	c := validConfig()
	ddl, err := ExternalTableDDL(c)
	if err != nil || strings.Contains(ddl, c.ClientSecret) || !strings.Contains(ddl, c.blobRoot()+";managed_identity=system") ||
		!strings.Contains(ddl, "ArchiveDay") || !strings.Contains(ddl, "PayloadJSON:string") {
		t.Fatal("setup schema or destination binding incorrect")
	}
	content, err := json.Marshal(c)
	if err != nil || strings.Contains(string(content), c.ClientSecret) {
		t.Fatal("config exposes query or Blob credentials")
	}
	for _, properties := range []string{
		strings.ReplaceAll(protocolProperties, "/container/archive", "/other/archive"),
		strings.ReplaceAll(protocolProperties, ";managed_identity=system", "?sig=broad"),
		strings.ReplaceAll(protocolProperties, "privatearchive.blob", "otheraccount.blob"),
		`{"StorageConnectionStrings":["https://privatearchive.blob.core.windows.net/container/archive;managed_identity=system","https://elsewhere.example/"]}`,
	} {
		if a.exactStorageProperties(properties) {
			t.Fatal("external table accepted another destination or SAS scope")
		}
	}
}

func TestHostedADXVisibilityPrecedesPublication(t *testing.T) {
	a, s, _ := newProtocol(t)
	a.verifyADX = true
	m := fixture(t, 1, "first")
	s.query = func(w http.ResponseWriter, _ *http.Request, query string) {
		if !strings.Contains(query, "hash_sha256(PayloadJSON)") || !strings.Contains(query, "Unique=count()") {
			t.Error("publication did not execute content and exact sequence verification")
		}
		respond(w, strings.Split("Namespace,Repository,RunId,RunAttempt,ProjectionId,TraceId,Events,Unique,First,Last,Invalid", ","), nil, true)
	}
	if err := a.Publish(t.Context(), m); !errors.Is(err, ErrIntegrity) || a.Status().Generation != 0 || len(a.state.Pending) != 1 {
		t.Fatal("successful Blob upload was mistaken for ADX analytical readiness")
	}
	s.query = func(w http.ResponseWriter, _ *http.Request, _ string) {
		member := m.Members[0]
		respond(w, strings.Split("Namespace,Repository,RunId,RunAttempt,ProjectionId,TraceId,Events,Unique,First,Last,Invalid", ","),
			[][]any{{member.Namespace, member.Repository, member.RunID, member.Attempt, member.Version, member.ID, 2, 2, 0, 1, 0}}, true)
	}
	if err := a.Publish(t.Context(), m); err != nil || a.Status().Generation != 1 {
		t.Fatalf("verified analytical publication did not acknowledge retry: %v", err)
	}
}
