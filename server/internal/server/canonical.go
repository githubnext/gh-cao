package server

import (
	"context"
	"errors"
	"net/http"

	"github.com/githubnext/gh-cao/server/internal/dashboardstore"
	"github.com/githubnext/gh-cao/server/internal/logger"
	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/query"
)

var canonicalLog = logger.New("cao:server:canonical")

type canonicalService struct {
	store       dashboardstore.Store
	definitions []query.Definition
}

var errCanonicalEntityNotFound = errors.New("canonical entity was not found")

func (service canonicalService) rows(ctx context.Context, source string) ([]model.Row, error) {
	var rows []model.Row
	err := service.store.WithReadTransaction(ctx, func(ctx context.Context, reader dashboardstore.Reader) error {
		active, err := reader.State(ctx)
		if err != nil {
			return err
		}
		if !active.Ready {
			return errors.New("dashboard data is unavailable")
		}
		sources, _, err := reader.ExecuteSQLPlan(ctx, service.definitions, []string{source})
		if err == nil {
			if sources[source].Metadata["availability"] == "unavailable" {
				return errors.New("canonical data is unavailable")
			}
			rows = sources[source].Rows
		}
		return err
	})
	return rows, err
}

func (service canonicalService) filteredRows(ctx context.Context, source string, filters map[string]any) ([]model.Row, error) {
	predicates := make([]query.Predicate, 0, len(filters))
	for field, value := range filters {
		predicates = append(predicates, query.Predicate{Field: field, Equals: value})
	}
	definition := query.Definition{
		Name: "canonical-api-result",
		From: source,
		Filter: &query.Filter{
			Predicates: predicates,
		},
	}
	definitions := append(append([]query.Definition{}, service.definitions...), definition)
	var rows []model.Row
	err := service.store.WithReadTransaction(ctx, func(ctx context.Context, reader dashboardstore.Reader) error {
		active, err := reader.State(ctx)
		if err != nil {
			return err
		}
		if !active.Ready {
			return errors.New("dashboard data is unavailable")
		}
		sources, _, err := reader.ExecuteSQLPlan(ctx, definitions, []string{definition.Name})
		if err == nil {
			if sources[definition.Name].Metadata["availability"] == "unavailable" {
				return errors.New("canonical data is unavailable")
			}
			rows = sources[definition.Name].Rows
		}
		return err
	})
	return rows, err
}

func (service canonicalService) entity(ctx context.Context, source, id string) (model.Row, error) {
	rows, err := service.filteredRows(ctx, source, map[string]any{"id": id})
	if err != nil {
		return nil, err
	}
	if len(rows) > 0 {
		return rows[0], nil
	}
	return nil, errCanonicalEntityNotFound
}

func (service canonicalService) repositoryRuns(ctx context.Context, id string) ([]model.Row, error) {
	return service.parentRuns(ctx, "repositories", "repositoryId", id)
}

func (service canonicalService) workflowRuns(ctx context.Context, id string) ([]model.Row, error) {
	return service.parentRuns(ctx, "workflows", "workflowId", id)
}

func (service canonicalService) parentRuns(ctx context.Context, parent, field, id string) ([]model.Row, error) {
	definitions := append([]query.Definition{}, service.definitions...)
	definitions = append(definitions, query.Definition{Name: "canonical-parent", From: parent, Filter: &query.Filter{Predicates: []query.Predicate{{Field: "id", Equals: id}}}},
		query.Definition{Name: "canonical-run-input", From: "$runs", Filter: &query.Filter{Predicates: []query.Predicate{{Field: field, Equals: id}}}})
	var presentation query.Definition
	for _, definition := range service.definitions {
		if definition.Name == "runs" {
			presentation = definition
			break
		}
	}
	if presentation.Name == "" {
		return nil, errors.New("canonical run projection is not declared")
	}
	presentation.Name, presentation.From = "canonical-parent-runs", "canonical-run-input"
	definitions = append(definitions, presentation)
	var rows []model.Row
	err := service.store.WithReadTransaction(ctx, func(ctx context.Context, reader dashboardstore.Reader) error {
		active, err := reader.State(ctx)
		if err != nil {
			return err
		}
		if !active.Ready {
			return errors.New("dashboard data is unavailable")
		}
		sources, _, err := reader.ExecuteSQLPlan(ctx, definitions, []string{"canonical-parent", "canonical-parent-runs"})
		if err != nil {
			return err
		}
		if sources["canonical-parent"].Metadata["availability"] == "unavailable" || sources["canonical-parent-runs"].Metadata["availability"] == "unavailable" {
			return errors.New("canonical data is unavailable")
		}
		if len(sources["canonical-parent"].Rows) == 0 {
			return errCanonicalEntityNotFound
		}
		rows = sources["canonical-parent-runs"].Rows
		return nil
	})
	return rows, err
}

// canonicalOutcome classifies a canonical query's result for the HTTP layer,
// so a query failure is diagnosable without logging the query error text
// (which may embed a source name or filter value).
type canonicalOutcome string

const (
	canonicalOutcomeOK          canonicalOutcome = "ok"
	canonicalOutcomeNotFound    canonicalOutcome = "not-found"
	canonicalOutcomeUnavailable canonicalOutcome = "unavailable"
)

// classifyCanonicalError applies the standard priority for turning a
// canonicalService error into an HTTP response: a nil error succeeds, a
// missing entity is a 404, and any other error means canonical data itself
// could not be read, which is a 503. It is a pure function extracted from
// the two call sites that previously duplicated this classification inline,
// so the mapping from error to outcome, status, and message is testable
// without an httptest server.
func classifyCanonicalError(err error) (outcome canonicalOutcome, status int, message string) {
	switch {
	case err == nil:
		return canonicalOutcomeOK, http.StatusOK, ""
	case errors.Is(err, errCanonicalEntityNotFound):
		return canonicalOutcomeNotFound, http.StatusNotFound, "canonical entity was not found"
	default:
		return canonicalOutcomeUnavailable, http.StatusServiceUnavailable, "canonical data is unavailable"
	}
}

func (a *App) repositories(response http.ResponseWriter, request *http.Request) {
	rows, err := a.canonical.rows(request.Context(), "repositories")
	a.writeCanonicalRows(request.Context(), response, rows, err)
}

func (a *App) repository(response http.ResponseWriter, request *http.Request) {
	row, err := a.canonical.entity(request.Context(), "repositories", request.PathValue("id"))
	if outcome, status, message := classifyCanonicalError(err); outcome != canonicalOutcomeOK {
		canonicalLog.PrintfContext(request.Context(), "repository query failed outcome=%s", outcome)
		if outcome == canonicalOutcomeNotFound {
			// This endpoint predates the shared classifier and names the
			// missing entity in its message, unlike the generic message the
			// other canonical endpoints share; preserve that wording.
			message = "repository was not found"
		}
		writeError(request.Context(), response, status, message)
		return
	}
	writeJSON(response, http.StatusOK, row)
}

func (a *App) repositoryRuns(response http.ResponseWriter, request *http.Request) {
	rows, err := a.canonical.repositoryRuns(request.Context(), request.PathValue("id"))
	a.writeCanonicalRows(request.Context(), response, rows, err)
}

func (a *App) workflowRuns(response http.ResponseWriter, request *http.Request) {
	rows, err := a.canonical.workflowRuns(request.Context(), request.PathValue("id"))
	a.writeCanonicalRows(request.Context(), response, rows, err)
}

func (a *App) writeCanonicalRows(ctx context.Context, response http.ResponseWriter, rows []model.Row, err error) {
	if outcome, status, message := classifyCanonicalError(err); outcome != canonicalOutcomeOK {
		canonicalLog.PrintfContext(ctx, "canonical rows query failed outcome=%s", outcome)
		writeError(ctx, response, status, message)
		return
	}
	writeJSON(response, http.StatusOK, rows)
}
