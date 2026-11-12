package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/githubnext/gh-cao/internal/dashboardquery"

	"github.com/githubnext/gh-cao/server/internal/dashboardstore"
	"github.com/githubnext/gh-cao/server/internal/logger"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
	"github.com/githubnext/gh-cao/server/internal/query"
)

var readLog = logger.New("cao:dashboard:read")

func newReadCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use: "read", Hidden: true, Args: cobra.NoArgs,
		Short: "read canonical PostgreSQL records for the native CLI",
	}
	endpoint := cmd.Flags().String("postgres-url", "", "PostgreSQL connection; defaults to CAO_POSTGRES_URL")
	namespace, namespaceErr := registerRedisNamespaceFlag(cmd, "canonical dataset namespace")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		if namespaceErr != nil {
			return namespaceErr
		}
		store, err := newPostgresStore(cmd.Context(), *endpoint, *namespace)
		if err != nil {
			return err
		}
		defer func() { _ = store.Close() }()
		scanner := bufio.NewScanner(cmd.InOrStdin())
		scanner.Buffer(make([]byte, 4096), 1<<20)
		encoder := json.NewEncoder(cmd.OutOrStdout())
		return store.WithReadTransaction(cmd.Context(), func(ctx context.Context, reader dashboardstore.Reader) error {
			for scanner.Scan() {
				var request dashboardquery.CanonicalReadRequest
				if err := json.Unmarshal(scanner.Bytes(), &request); err != nil {
					return errors.New("invalid canonical read request")
				}
				result, err := readCanonical(ctx, reader, request)
				if err != nil {
					result = dashboardquery.CanonicalReadResult{Error: err.Error()}
				}
				if err := encoder.Encode(result); err != nil {
					return err
				}
			}
			return scanner.Err()
		})
	}
	return cmd
}

// canonicalReadDefinition validates a native read request and builds the query
// definition that serves it, without touching storage.
func canonicalReadDefinition(request dashboardquery.CanonicalReadRequest) (query.Definition, bool, error) {
	source := "$" + request.Collection
	switch request.Kind {
	case "collection", "evidence", "measurements", "statistical-runs":
		if !postgresx.IsCanonicalCollection(request.Collection) && request.Collection != "transactions" {
			return query.Definition{}, false, fmt.Errorf("unknown canonical collection: %s", request.Collection)
		}
		if request.Collection == "operationalValueEvidence" {
			return query.Definition{}, false, errors.New("raw evidence packet reads are unsupported; use a named Value query")
		}
	case "evidence-facts":
		source = "$operationalValueEvidence"
	case "daily-run-counts", "daily-run-count-metadata":
		source = "daily-run-counts"
	case "daily-run-count-records":
		source = "$dailyRunCounts"
	case "local-metadata":
		return query.Definition{}, true, nil
	default:
		return query.Definition{}, false, errors.New("unsupported canonical read kind")
	}
	if request.MaxRows < 0 || request.MaxRows > 500_000 || request.MaxBytes < 0 || request.MaxBytes > 1<<30 {
		return query.Definition{}, false, errors.New("invalid canonical read budget")
	}
	definition := query.Definition{Name: "native-read", From: source}
	if request.Kind == "daily-run-count-metadata" {
		limit := 1
		definition.Limit = &limit
	}
	for _, field := range request.Fields {
		definition.Select = append(definition.Select, query.SelectedField{Field: field})
	}
	predicates := []query.Predicate{}
	if request.RecordIDs != nil {
		ids := make([]any, len(request.RecordIDs))
		for i, id := range request.RecordIDs {
			ids[i] = id
		}
		predicates = append(predicates, query.Predicate{Field: "id", In: ids})
	}
	for _, selection := range request.Selections {
		field, value := selection.Field, selection.Value
		supported := request.Kind == "evidence-facts" && (field == "report-id" || field == "workflow-route") ||
			request.Kind == "measurements" && (field == "id" || field == "workflow-route")
		if !supported {
			return query.Definition{}, false, errors.New("unsupported canonical selection")
		}
		predicates = append(predicates, query.Predicate{Field: field, Equals: value})
	}
	if len(predicates) > 0 {
		definition.Filter = &query.Filter{Predicates: predicates}
	}
	return definition, false, nil
}

func readCanonical(ctx context.Context, reader dashboardstore.Reader, request dashboardquery.CanonicalReadRequest) (dashboardquery.CanonicalReadResult, error) {
	definition, localOnly, err := canonicalReadDefinition(request)
	if err != nil {
		readLog.Printf("canonical read rejected")
		return dashboardquery.CanonicalReadResult{}, err
	}
	if localOnly {
		return dashboardquery.CanonicalReadResult{Available: false}, nil
	}
	sources, _, err := reader.ExecuteSQLPlan(ctx, []query.Definition{definition}, []string{definition.Name})
	if err != nil {
		return dashboardquery.CanonicalReadResult{}, err
	}
	value := sources[definition.Name]
	result := dashboardquery.CanonicalReadResult{Rows: make([]map[string]any, len(value.Rows)), Metadata: map[string]any(value.Metadata),
		Available: value.Metadata["availability"] != "unavailable"}
	if result.Available && (request.Kind == "daily-run-counts" || request.Kind == "daily-run-count-records") {
		excluded, err := json.Marshal(value.Metadata["undated-run-count"])
		if value.Metadata["undated-run-count"] == nil || err != nil || json.Unmarshal(excluded, &result.Undated) != nil {
			return result, errors.New("daily run-count exclusion evidence is unavailable")
		}
	}
	if request.MaxRows > 0 && len(value.Rows) > request.MaxRows {
		readLog.Printf("canonical read exceeded row budget: rows=%d max_rows=%d", len(value.Rows), request.MaxRows)
		return result, dashboardquery.ErrSnapshotInputBudget
	}
	var bytes int64
	for i, row := range value.Rows {
		result.Rows[i] = map[string]any(row)
		content, err := json.Marshal(row)
		if err != nil {
			return result, err
		}
		bytes += int64(len(content))
		if request.MaxBytes > 0 && bytes > request.MaxBytes {
			readLog.Printf("canonical read exceeded byte budget: rows=%d max_bytes=%d", len(value.Rows), request.MaxBytes)
			return dashboardquery.CanonicalReadResult{}, dashboardquery.ErrSnapshotInputBudget
		}
	}
	if request.Kind == "statistical-runs" {
		result.Watermark = dashboardquery.CanonicalStatisticalWatermark(result.Rows)
	} else if state, err := reader.State(ctx); err != nil {
		return result, err
	} else {
		result.Watermark = state.EvaluatedAt.UTC().Format("2006-01-02T15:04:05.999999Z07:00")
	}
	return result, nil
}
