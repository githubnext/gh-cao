package adxtrace

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeRunFixture(t *testing.T, logs, run, info string, session []byte) {
	t.Helper()
	usage := filepath.Join(logs, run, "usage")
	if err := os.MkdirAll(usage, 0o700); err != nil {
		t.Fatal(err)
	}
	if info != "" {
		if err := os.WriteFile(filepath.Join(logs, run, "aw_info.json"), []byte(info), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if session != nil {
		if err := os.WriteFile(filepath.Join(usage, "aw_session.jsonl"), session, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCaptureCandidateClassifiesEvidence(t *testing.T) {
	c := configFor(t, "full")
	logs := t.TempDir()
	writeRunFixture(t, logs, "run-1", `{"run_id":1,"run_attempt":1,"repository":"octo/api"}`, source(t, "ok"))
	writeRunFixture(t, logs, "run-2", `{"run_id":2,"run_attempt":1,"repository":"octo/api"}`, source(t, "ok"))
	writeRunFixture(t, logs, "run-3", `{"run_id":3,"run_attempt":1,"repository":"octo/api"}`, []byte("{\"type\":\"session.format\",\"data\":{\"version\":99}}"))
	writeRunFixture(t, logs, "run-4", `{"run_id":4,"run_attempt":1,"repository":"octo/api"}`, make([]byte, c.MaxTraceBytes+1))
	root, err := os.OpenRoot(logs)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()

	tests := []struct {
		name   string
		cand   candidate
		reason string
	}{
		{"projected", candidate{run: "1", directory: "run-1", day: "2026-01-01", attempt: 1}, ""},
		{"identity mismatch", candidate{run: "2", directory: "run-2", day: "2026-01-01", attempt: 2}, "artifact-identity-gap"},
		{"missing directory name", candidate{run: "5", day: "2026-01-01", attempt: 1}, "artifact-directory-gap"},
		{"absent artifact", candidate{run: "6", directory: "run-6", day: "2026-01-01", attempt: 1}, "artifact-unavailable"},
		{"invalid source", candidate{run: "3", directory: "run-3", day: "2026-01-01", attempt: 1}, "invalid-source"},
		{"oversized source", candidate{run: "4", directory: "run-4", day: "2026-01-01", attempt: 1}, "size-exclusion"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, reason, err := captureCandidate(c, root, "octo/api", tt.cand, clock)
			if err != nil {
				t.Fatal(err)
			}
			if reason != tt.reason {
				t.Fatalf("reason = %q, want %q", reason, tt.reason)
			}
			if e.RunID != tt.cand.run || e.Attempt != tt.cand.attempt || e.Repository != "octo/api" || e.ID == "" {
				t.Fatalf("evidence lost candidate identity: %+v", e)
			}
			if (tt.reason == "") != (len(e.Tables) > 0) {
				t.Fatalf("tables projected = %d for reason %q", len(e.Tables), tt.reason)
			}
		})
	}
}

func TestCaptureEvidenceSkipsRepositoriesOutsideScope(t *testing.T) {
	c := configFor(t, "full")
	called := false
	err := CaptureEvidence(t.Context(), c, "other/repo", t.TempDir(), filepath.Join(t.TempDir(), "repo-"), func() time.Time { return clock }, func(Evidence, string) error {
		called = true
		return nil
	})
	if err != nil || called {
		t.Fatalf("out-of-scope repository captured: err=%v called=%v", err, called)
	}
}
