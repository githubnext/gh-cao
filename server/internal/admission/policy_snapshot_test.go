package admission

import (
	"errors"
	"strings"
	"testing"
)

func validSnapshot() Snapshot {
	used := int64(1)
	return Snapshot{Version: 1, Repositories: []Enrollment{{
		Repository: "octo/repo", RepositoryID: "1001", OwnerID: "2002", Enabled: true,
		Subjects: []string{"repo:octo/repo"},
		Workflows: []Policy{{
			WorkflowRef: "octo/repo/.github/workflows/a.yml@refs/heads/main",
			WorkflowSHA: strings.Repeat("a", 40), Events: []string{"push"}, ActorIDs: []string{"3003"},
			Allowances: Allowances{Primary: 5},
		}},
		Usage: Usage{Unit: "ai-credits", Used: &used, Limit: 10},
	}}}
}

func TestValidateSnapshot(t *testing.T) {
	zero := int32(0)
	tests := []struct {
		name   string
		mutate func(*Snapshot)
		ok     bool
	}{
		{"valid", func(*Snapshot) {}, true},
		{"empty repositories", func(s *Snapshot) { s.Repositories = nil }, true},
		{"wrong version", func(s *Snapshot) { s.Version = 2 }, false},
		{"too many repositories", func(s *Snapshot) { s.Repositories = make([]Enrollment, 1001) }, false},
		{"bad repository name", func(s *Snapshot) { s.Repositories[0].Repository = "nope" }, false},
		{"non decimal id", func(s *Snapshot) { s.Repositories[0].RepositoryID = "0" }, false},
		{"no subjects", func(s *Snapshot) { s.Repositories[0].Subjects = nil }, false},
		{"wrong unit", func(s *Snapshot) { s.Repositories[0].Usage.Unit = "tokens" }, false},
		{"missing used", func(s *Snapshot) { s.Repositories[0].Usage.Used = nil }, false},
		{"zero limit", func(s *Snapshot) { s.Repositories[0].Usage.Limit = 0 }, false},
		{"duplicate repository id", func(s *Snapshot) {
			s.Repositories = append(s.Repositories, s.Repositories[0])
		}, false},
		{"bad workflow sha", func(s *Snapshot) { s.Repositories[0].Workflows[0].WorkflowSHA = "abc" }, false},
		{"no events", func(s *Snapshot) { s.Repositories[0].Workflows[0].Events = nil }, false},
		{"invalid allowance", func(s *Snapshot) { s.Repositories[0].Workflows[0].Allowances.Evals = &zero }, false},
		{"duplicate workflow", func(s *Snapshot) {
			w := s.Repositories[0].Workflows
			s.Repositories[0].Workflows = append(w, w[0])
		}, false},
		{"bad actor", func(s *Snapshot) { s.Repositories[0].Workflows[0].ActorIDs = []string{"x"} }, false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			snapshot := validSnapshot()
			test.mutate(&snapshot)
			err := validateSnapshot(snapshot)
			if test.ok && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !test.ok && !errors.Is(err, ErrUnavailable) {
				t.Fatalf("error = %v, want ErrUnavailable", err)
			}
		})
	}
}
