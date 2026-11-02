// Package dashboardstore defines the canonical read boundary shared by hosted
// PostgreSQL and explicitly selected local SQLite snapshots.
package dashboardstore

import (
	"context"
	"time"

	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/query"
)

type State struct {
	Ready        bool
	Revision     int64
	DataRevision string
	EvaluatedAt  time.Time
	Counts       map[string]int
}

type Page struct{ Offset, Limit int }

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

// Reader is scoped to the callback's consistent read transaction. The historical
// ExecuteSQLPlan names do not require SQL execution: PostgreSQL compiles SQL,
// while local SQLite uses the bounded Go evaluator and rejects resource-limit
// overrides.
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
