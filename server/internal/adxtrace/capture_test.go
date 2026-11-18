package adxtrace

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
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

func TestCandidateDirectory(t *testing.T) {
	logs := filepath.Join(string(filepath.Separator), "data", "logs")
	tests := []struct {
		name    string
		run     map[string]any
		want    string
		wantErr bool
	}{
		{"default from run id", map[string]any{}, "run-7", false},
		{"relative path is cleaned", map[string]any{"logs_path": "a/../run-9"}, "run-9", false},
		{"absolute path becomes relative", map[string]any{"logs_path": filepath.Join(logs, "run-8")}, "run-8", false},
		{"relative escape becomes gap", map[string]any{"logs_path": "../outside"}, "", false},
		{"absolute escape becomes gap", map[string]any{"logs_path": filepath.Join(string(filepath.Separator), "data", "other")}, "", false},
		{"non-string rejected", map[string]any{"logs_path": 5}, "", true},
		{"blank rejected", map[string]any{"logs_path": "  "}, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := candidateDirectory(logs, tt.run, "7")
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Fatalf("candidateDirectory = %q, %v; want %q, err=%t", got, err, tt.want, tt.wantErr)
			}
		})
	}
}

func TestMergeCandidate(t *testing.T) {
	recorded := candidate{run: "1", directory: "run-1", day: "2026-01-01", attempt: 1}
	tests := []struct {
		name    string
		old     candidate
		present bool
		value   candidate
		keep    bool
		wantErr bool
	}{
		{"first frame", candidate{}, false, recorded, true, false},
		{"identical repeat", recorded, true, recorded, true, false},
		{"gap does not replace directory", recorded, true, candidate{run: "1", day: "2026-01-01", attempt: 1}, false, false},
		{"directory replaces gap", candidate{run: "1", day: "2026-01-01", attempt: 1}, true, recorded, true, false},
		{"same directory on another day is ambiguous", recorded, true, candidate{run: "1", directory: "run-1", day: "2026-01-02", attempt: 1}, false, true},
		{"conflicting directories are ambiguous", recorded, true, candidate{run: "1", directory: "run-other", day: "2026-01-01", attempt: 1}, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			keep, err := mergeCandidate(tt.old, tt.present, tt.value)
			if keep != tt.keep || (err != nil) != tt.wantErr {
				t.Fatalf("mergeCandidate = %t, %v; want %t, err=%t", keep, err, tt.keep, tt.wantErr)
			}
		})
	}
}

func TestReadCandidatesCollapsesRepeatedFramesAndRejectsAmbiguity(t *testing.T) {
	frame := func(id, path string) string {
		return `{"kind":"run","run":{"status":"completed","repository":"octo/api","run_id":` + id +
			`,"run_attempt":1,"completed_at":"2026-01-01T00:00:00Z","logs_path":"` + path + `"}}` + "\n"
	}
	out := map[string]candidate{}
	input := frame("1", "run-1") + frame("1", "run-1") + `{"kind":"other"}` + "\n"
	if err := readCandidates(t.Context(), strings.NewReader(input), "octo/api", t.TempDir(), out, 10); err != nil {
		t.Fatal(err)
	}
	want := candidate{run: "1", directory: "run-1", day: "2026-01-01", attempt: 1}
	if len(out) != 1 || out["1:1"] != want {
		t.Fatalf("candidates = %+v", out)
	}
	err := readCandidates(t.Context(), strings.NewReader(frame("1", "run-1")+frame("1", "run-2")), "octo/api", t.TempDir(), map[string]candidate{}, 10)
	if err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("conflicting directories err = %v", err)
	}
	err = readCandidates(t.Context(), strings.NewReader(frame("1", "run-1")+frame("2", "run-2")), "octo/api", t.TempDir(), map[string]candidate{}, 1)
	if !errors.Is(err, ErrCapacity) {
		t.Fatalf("capacity err = %v", err)
	}
}
