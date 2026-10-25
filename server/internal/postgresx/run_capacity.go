package postgresx

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/githubnext/gh-cao/server/internal/query"
)

type runCapacity struct {
	rows  int
	bytes int64
}

type capacityRow interface{ Scan(...any) error }

type capacitySQL struct {
	exec func(context.Context, string, ...any) error
	row  func(context.Context, string, ...any) capacityRow
}

func (s *Store) capacity() runCapacity {
	if s.runCapacity.rows > 0 && s.runCapacity.bytes > 0 {
		return s.runCapacity
	}
	return runCapacity{rows: query.MaxInputRows, bytes: query.MaxWorkingBytes}
}

func entityBytesSQL(table entityTable, alias string) string {
	parts := make([]string, 1, 1+len(table.columns))
	parts[0] = strconv.Itoa(64+len(table.columns)) + "::bigint"
	for _, column := range table.columns {
		parts = append(parts, "coalesce(octet_length("+alias+"."+query.SQLIdentifier(column.name)+"::text),0)")
	}
	return "(" + strings.Join(parts, "+") + ")::bigint"
}

func (w *Writer) capRuns(ctx context.Context) error {
	return capRunSubtrees(ctx, w.store.namespace, w.store.capacity(), capacitySQL{
		exec: func(ctx context.Context, statement string, args ...any) error {
			_, err := w.tx.Exec(ctx, statement, args...)
			return err
		},
		row: func(ctx context.Context, statement string, args ...any) capacityRow {
			return w.tx.QueryRow(ctx, statement, args...)
		},
	})
}

func (s *Store) capInvocationRuns(ctx context.Context, tx *sql.Tx) error {
	return capRunSubtrees(ctx, s.namespace, s.capacity(), capacitySQL{
		exec: func(ctx context.Context, statement string, args ...any) error {
			_, err := tx.ExecContext(ctx, statement, args...)
			return err
		},
		row: func(ctx context.Context, statement string, args ...any) capacityRow {
			return tx.QueryRowContext(ctx, statement, args...)
		},
	})
}

// Reconciliation precedes capping: contribution receipts and daily observations
// survive hot-detail eviction. Everything commits with the owning write.
func capRunSubtrees(ctx context.Context, namespace string, limits runCapacity, db capacitySQL) error {
	var sources []string
	for _, source := range tableNames() {
		if entityTables[source].partitioned {
			sources = append(sources, source)
		}
	}
	columns := make([]string, 0, len(sources))
	for index := range sources {
		columns = append(columns, "n"+strconv.Itoa(index)+" bigint NOT NULL DEFAULT 0")
	}
	if err := db.exec(ctx, `CREATE TEMP TABLE cao_capacity_weights
		(id text PRIMARY KEY,bytes bigint NOT NULL,`+strings.Join(columns, ",")+`) ON COMMIT DROP`); err != nil {
		return err
	}
	defer func() { _ = db.exec(ctx, "DROP TABLE IF EXISTS cao_capacity_weights") }()
	// Aggregate one typed source at a time. A single wide UNION across every
	// daily partition multiplies planner/JIT memory before eviction can run.
	for index, source := range sources {
		table := entityTables[source]
		identity := "t.run_id"
		if source == "$runs" {
			identity = "t.id"
		}
		count := "n" + strconv.Itoa(index)
		statement := `INSERT INTO cao_capacity_weights AS w(id,bytes,` + count + `)
			SELECT ` + identity + `,sum(` + entityBytesSQL(table, "t") + `)::bigint,count(*)
			FROM ` + query.SQLIdentifier(table.name) + ` t WHERE t.namespace=$1 GROUP BY ` + identity + `
			ON CONFLICT(id) DO UPDATE SET bytes=w.bytes+excluded.bytes,` + count + `=excluded.` + count
		if err := db.exec(ctx, statement, namespace); err != nil {
			return fmt.Errorf("measure canonical capacity for %s: %w", source, err)
		}
	}
	if err := db.exec(ctx, "ANALYZE cao_capacity_weights"); err != nil {
		return err
	}
	checks := make([]string, 0, len(sources)+1)
	for index := range sources {
		checks = append(checks, "sum(w.n"+strconv.Itoa(index)+") OVER newest <= $2")
	}
	checks = append(checks, "sum(w.bytes) OVER newest <= $3")
	statement := `CREATE TEMP TABLE cao_capacity_runs ON COMMIT DROP AS
		SELECT r.id,r.run_at,row_number() OVER newest AS position,(` + strings.Join(checks, " AND ") + `) AS keep
		FROM runs r JOIN cao_capacity_weights w ON w.id=r.id WHERE r.namespace=$1
		WINDOW newest AS (ORDER BY r.run_at DESC,r.id COLLATE "C" DESC ROWS UNBOUNDED PRECEDING)`
	if err := db.exec(ctx, statement, namespace, limits.rows, limits.bytes); err != nil {
		return fmt.Errorf("measure canonical run capacity: %w", err)
	}
	defer func() { _ = db.exec(ctx, "DROP TABLE IF EXISTS cao_capacity_runs") }()
	var invalid, evicted int
	if err := db.row(ctx, `SELECT count(*) FILTER(WHERE NOT keep AND position=1),
		count(*) FILTER(WHERE NOT keep) FROM cao_capacity_runs`).Scan(&invalid, &evicted); err != nil {
		return err
	}
	if invalid != 0 {
		return errors.New("canonical run capacity cannot fit the newest complete run subtree")
	}
	if evicted == 0 {
		return nil
	}
	if err := db.exec(ctx, "CREATE INDEX ON cao_capacity_runs(id) WHERE NOT keep"); err != nil {
		return err
	}
	// Delete every owned collection before its Run; inventory, operational
	// value evidence, and daily contribution receipts are deliberately untouched.
	for _, source := range sources {
		if source == "$runs" {
			continue
		}
		table := entityTables[source]
		if err := db.exec(ctx, "DELETE FROM "+query.SQLIdentifier(table.name)+
			" t USING cao_capacity_runs e WHERE t.namespace=$1 AND t.run_id=e.id AND NOT e.keep", namespace); err != nil {
			return err
		}
	}
	if err := db.exec(ctx, `DELETE FROM runs r USING cao_capacity_runs e
		WHERE r.namespace=$1 AND r.id=e.id AND NOT e.keep`, namespace); err != nil {
		return err
	}
	for _, source := range sources {
		if err := db.exec(ctx, `INSERT INTO cao_quality(namespace,collection,availability,completeness,freshness)
			VALUES($1,$2,'available','partial','unknown') ON CONFLICT(namespace,collection)
			DO UPDATE SET completeness='partial'`, namespace, source); err != nil {
			return err
		}
	}
	storeLog.Printf("canonical capacity selected_evictions=%d row_limit=%d byte_limit=%d", evicted, limits.rows, limits.bytes)
	return nil
}
