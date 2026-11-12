// Package dashboardstore defines the canonical PostgreSQL read boundary for
// local and hosted servers.
package dashboardstore

import (
	"context"
	"time"

	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/query"
)

type State struct {
	Ready                   bool
	Revision                int64
	WorkflowSubjectRevision int64
	WorkflowSubjectDigest   string
	DataRevision            string
	EvaluatedAt             time.Time
	Counts                  map[string]int
}

type KeysetPage struct {
	Boundary   string
	PlanDigest string
	Revision   string
	Comparator string
	Keys       []string
	After      []string
}

type Page struct {
	Offset int
	Limit  int
	Keyset *KeysetPage
}

type ResourceLimits struct {
	MaxInputRows    int
	MaxOperations   int
	MaxWorkingBytes int64
}

type ExecutionOptions struct {
	Runtime        func(context.Context, string) (model.Source, error)
	Pages          map[string]Page
	ResourceLimits *ResourceLimits
	Repositories   []string
}

// Reader is scoped to the callback's consistent read transaction.
type Reader interface {
	State(context.Context) (State, error)
	ExecuteSQLPlan(context.Context, []query.Definition, []string) (map[string]model.Source, model.Metrics, error)
	ExecuteSQLPlanWithOptions(context.Context, []query.Definition, []string, ExecutionOptions) (map[string]model.Source, model.Metrics, error)
	Diagnostics(context.Context) (model.Diagnostics, error)
}

type Store interface {
	Open() bool
	Close() error
	State(context.Context) (State, error)
	// WithReadTransaction bounds the callback's context and keeps state, queries,
	// and diagnostics on one snapshot. Callback errors are returned unchanged.
	WithReadTransaction(context.Context, func(context.Context, Reader) error) error
	Diagnostics(context.Context) (model.Diagnostics, error)
}
