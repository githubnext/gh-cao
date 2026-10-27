package collect

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLocalValueRevisionTracksReviewedHostedInputs(t *testing.T) {
	directory := t.TempDir()
	policy := filepath.Join(directory, "cao.json")
	request := filepath.Join(directory, "workflow-value-local.json")
	if err := os.WriteFile(policy, []byte(`{"version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(request, []byte(`[{"schemaVersion":1}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := readLocalValueRevision(t.Context(), policy, request)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(request, []byte(`[{"schemaVersion":1,"eligible":true}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	afterRequest, err := readLocalValueRevision(t.Context(), policy, request)
	if err != nil {
		t.Fatal(err)
	}
	if before == afterRequest {
		t.Fatal("hosted local Value request changes must invalidate publication")
	}
	if err := os.WriteFile(policy, []byte(`{"version":2}`), 0o600); err != nil {
		t.Fatal(err)
	}
	afterPolicy, err := readLocalValueRevision(t.Context(), policy, request)
	if err != nil {
		t.Fatal(err)
	}
	if afterRequest == afterPolicy {
		t.Fatal("reviewed policy changes must invalidate publication")
	}
}

func TestLocalValueRevisionAllowsMissingOptionalRequest(t *testing.T) {
	policy := filepath.Join(t.TempDir(), "cao.json")
	if err := os.WriteFile(policy, []byte(`{"version":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readLocalValueRevision(t.Context(), policy, filepath.Join(t.TempDir(), "missing.json")); err != nil {
		t.Fatal(err)
	}
}

func TestLocalValuePublicationRejectsEverySupersededInput(t *testing.T) {
	generation := []byte("projection-1")
	revision := localValueRevision{policy: [32]byte{1}, request: [32]byte{2}}
	repositories := []string{"octo/api"}
	if !localValuePublicationCurrent(generation, generation, revision, revision, repositories, repositories) {
		t.Fatal("unchanged hosted inputs were rejected")
	}
	tests := []struct {
		name         string
		generation   []byte
		revision     localValueRevision
		repositories []string
	}{
		{"projection", []byte("projection-2"), revision, repositories},
		{"policy", generation, localValueRevision{policy: [32]byte{3}, request: revision.request}, repositories},
		{"request", generation, localValueRevision{policy: revision.policy, request: [32]byte{4}}, repositories},
		{"enrollment", generation, revision, []string{"octo/other"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if localValuePublicationCurrent(generation, test.generation, revision, test.revision,
				repositories, test.repositories) {
				t.Fatal("superseded hosted input was accepted")
			}
		})
	}
}
