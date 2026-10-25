package postgresx

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/query"
	"github.com/githubnext/gh-cao/server/internal/sessionexport"
	"github.com/githubnext/gh-cao/server/internal/sqlbuilder"
)

// ReadSession reads one retained completed run and its canonical observations
// in one bounded snapshot. No network I/O runs inside the transaction.
func (s *Store) ReadSession(ctx context.Context, request sessionexport.Request) (session sessionexport.Session, err error) {
	if len(request.RepositoryIDs) == 0 || len(request.RepositoryIDs) > 1000 {
		return session, errors.New("session export requires 1-1000 exact repository IDs")
	}
	err = s.WithReadTransaction(ctx, func(ctx context.Context, reader NativeReader) error {
		r := reader.(*readTransaction)
		session.Namespace = s.namespace
		session.Facts = map[string][]model.Row{}
		session.Quality = map[string]model.Metadata{}
		err := r.tx.QueryRowContext(ctx, "SELECT revision FROM cao_state WHERE namespace=$1", s.namespace).Scan(&session.Revision)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if request.After == "" && request.CompletedRevision == session.Revision {
			return nil
		}
		run, err := readExportRows(ctx, r.tx, s.namespace, "$runs", request.Fields["$runs"],
			"id>$2 AND repository_id=ANY($3) AND status='completed' ORDER BY id LIMIT 1", request.After, request.RepositoryIDs)
		if len(run) > 0 {
			session.Run = run[0]
		}
		if err != nil || len(run) == 0 {
			return err
		}
		remaining := sessionexport.MaxRows - 1
		bytes := 0
		for _, source := range []string{"$runs", "$tools", "$domains", "$graderObservations", "$evalObservations"} {
			rows := run
			if source != "$runs" {
				rows, err = readExportRows(ctx, r.tx, s.namespace, source, request.Fields[source],
					fmt.Sprintf("run_id=$2 ORDER BY id LIMIT %d", remaining+1), session.Run["id"])
				if err != nil {
					return err
				}
				if len(rows) > remaining {
					return sessionexport.ErrTooLarge
				}
				remaining -= len(rows)
				session.Facts[source] = rows
			}
			for _, row := range rows {
				content, err := json.Marshal(row)
				if err != nil {
					return err
				}
				bytes += len(content)
				if bytes > sessionexport.MaxBytes {
					return sessionexport.ErrTooLarge
				}
			}
			var availability, completeness, freshness string
			err := r.tx.QueryRowContext(ctx, `SELECT availability,completeness,freshness
				FROM cao_quality WHERE namespace=$1 AND collection=$2`, s.namespace, source).
				Scan(&availability, &completeness, &freshness)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if errors.Is(err, sql.ErrNoRows) {
				availability, completeness, freshness = "unavailable", "unknown", "unknown"
			}
			session.Quality[source] = model.Metadata{"availability": availability, "completeness": completeness, "freshness": freshness}
		}
		return nil
	})
	return session, err
}

func readExportRows(ctx context.Context, tx *sql.Tx, namespace, source string, fields []string, predicate string, args ...any) ([]model.Row, error) {
	table := entityTables[source]
	columns := []string{"present_fields::text"}
	var selected []entityColumn
	var indexes []int
	for i, column := range table.columns {
		if column.kind == "object" || strings.Contains(column.field, ".") {
			continue
		}
		if column.field != "id" && column.field != "runId" && column.field != "attempt" && !slices.Contains(fields, column.field) {
			continue
		}
		selected = append(selected, column)
		indexes = append(indexes, i)
		columns = append(columns, query.SQLIdentifier(column.name))
	}
	statement, arguments, err := sqlbuilder.Build("SELECT {} FROM {} WHERE namespace={} AND {}",
		sqlbuilder.Fragment(strings.Join(columns, ",")), sqlbuilder.Identifier(table.name), namespace, sqlbuilder.Fragment(predicate))
	if err != nil {
		return nil, err
	}
	arguments = append(arguments, args...)
	rows, err := tx.QueryContext(ctx, statement, arguments...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var result []model.Row
	bytes := 0
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(values))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return nil, err
		}
		presence, ok := values[0].(string)
		if !ok || len(presence) != len(table.columns) {
			return nil, errors.New("invalid canonical export presence bits")
		}
		row := model.Row{}
		for i, column := range selected {
			if presence[indexes[i]] != '1' {
				continue
			}
			value, err := decodeSQLValue(values[i+1], sqlKind(column))
			if err != nil {
				return nil, err
			}
			row[column.field] = value
		}
		result = append(result, row)
		content, err := json.Marshal(row)
		if err != nil {
			return nil, err
		}
		bytes += len(content)
		if bytes > sessionexport.MaxBytes {
			return result, sessionexport.ErrTooLarge
		}
	}
	return result, rows.Err()
}
