package postgresx

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/githubnext/gh-cao/server/internal/query"
)

type collectionAccounting struct {
	records, bytes, nextOrdinal int64
	evidenceAt                  *time.Time
}

func newCollectionAccounting() map[string]*collectionAccounting {
	result := map[string]*collectionAccounting{}
	for _, source := range tableNames() {
		if !entityTables[source].internal {
			result[source] = &collectionAccounting{}
		}
	}
	return result
}

func entityClockSQL(table entityTable, alias string) string {
	var columns []string
	for _, column := range table.columns {
		if column.kind != "timestamp" {
			continue
		}
		switch column.field {
		case "observedAt", "timestamp", "createdAt", "startedAt", "completedAt", "updatedAt",
			"observed-at", "created-at", "started-at", "ended-at", "provenance.observedAt":
			columns = append(columns, alias+query.SQLIdentifier(column.name))
		}
	}
	if len(columns) == 0 {
		return "NULL::timestamptz"
	}
	return "greatest(" + strings.Join(columns, ",") + ")"
}

func (w *Writer) captureAuditClocks(ctx context.Context, scope string) error {
	filter := ""
	if scope != "" {
		filter = " AND EXISTS(SELECT 1 FROM " + query.SQLIdentifier(scope) +
			" k WHERE k.id=t.run_id AND k.run_at=t.run_at)"
	}
	_, err := w.tx.Exec(ctx, `CREATE TEMP TABLE ingestion_audit_clocks ON COMMIT DROP AS
		SELECT t.run_id,t.run_at,max(`+entityClockSQL(entityTables["$audits"], "t.")+`) AS evidence_at
		FROM audits t WHERE t.namespace=$1`+filter+` GROUP BY t.run_id,t.run_at`, w.store.namespace)
	return err
}

// A scope is an owned temporary relation of exact (id, run_at) keys, not user SQL.
func measureRunAccounting(ctx context.Context, namespace, scope string, db capacitySQL) error {
	if db.preserveClocks {
		if err := db.exec(ctx, `CREATE TEMP TABLE previous_input_clocks ON COMMIT DROP AS
			SELECT a.run_id,a.collection,a.run_at,a.evidence_at FROM cao_run_accounting a
			JOIN cao_run_accounting p ON p.namespace=a.namespace AND p.run_id=a.run_id AND p.collection='$runs'
			JOIN runs r ON r.namespace=p.namespace AND r.id=p.run_id AND r.run_at=p.run_at
			WHERE a.namespace=$1 AND coalesce(r.attempt,1)=coalesce(p.attempt,1)
				AND (a.collection='$runs' OR a.run_at>=$2)`, namespace, db.linkedCutoff); err != nil {
			return err
		}
		defer func() { _ = db.exec(ctx, "DROP TABLE previous_input_clocks") }()
	}
	filter := ""
	args := []any{namespace}
	if scope != "" {
		filter = " AND run_id IN (SELECT id FROM " + query.SQLIdentifier(scope) + ")"
	}
	if db.sources != nil {
		var sources []string
		for source, changed := range db.sources {
			if changed && entityTables[source].partitioned {
				sources = append(sources, source)
			}
		}
		filter += " AND collection=ANY($2::text[])"
		args = append(args, sources)
	}
	if err := db.exec(ctx, "DELETE FROM cao_run_accounting WHERE namespace=$1"+filter, args...); err != nil {
		return err
	}
	for _, source := range tableNames() {
		table := entityTables[source]
		if !table.partitioned || db.sources != nil && !db.sources[source] {
			continue
		}
		id := "t.run_id"
		attempt := "NULL::bigint"
		if source == "$runs" {
			id = "t.id"
			attempt = "max(t.attempt)"
		}
		where := "t.namespace=$1"
		if scope != "" {
			where += " AND EXISTS(SELECT 1 FROM " + query.SQLIdentifier(scope) +
				" k WHERE k.id=" + id + " AND k.run_at=t.run_at)"
		}
		statement := `INSERT INTO cao_run_accounting(namespace,collection,run_id,run_at,records,bytes,next_ordinal,evidence_at,attempt)
			SELECT $1,$2,` + id + `,t.run_at,count(*),sum(` + entityBytesSQL(table) + `)::bigint,
				max(t.ordinal)+1,max(` + entityClockSQL(table, "t.") + `),` + attempt + `
			FROM ` + query.SQLIdentifier(table.name) + ` t WHERE ` + where + ` GROUP BY ` + id + `,t.run_at`
		if err := db.exec(ctx, statement, namespace, source); err != nil {
			return fmt.Errorf("measure run accounting %s: %w", source, err)
		}
	}
	if db.preserveClocks {
		if err := db.exec(ctx, `INSERT INTO cao_run_accounting(namespace,collection,run_id,run_at,records,bytes,next_ordinal,evidence_at)
			SELECT $1,collection,run_id,run_at,0,0,0,evidence_at FROM previous_input_clocks
			ON CONFLICT(namespace,collection,run_id) DO UPDATE SET
				evidence_at=greatest(cao_run_accounting.evidence_at,excluded.evidence_at)`, namespace); err != nil {
			return err
		}
	}
	if db.auditClocks && (db.sources == nil || db.sources["$audits"]) {
		return db.exec(ctx, `INSERT INTO cao_run_accounting(namespace,collection,run_id,run_at,records,bytes,next_ordinal,evidence_at)
			SELECT $1,'$audits',run_id,run_at,0,0,0,evidence_at FROM ingestion_audit_clocks
			ON CONFLICT(namespace,collection,run_id) DO UPDATE SET evidence_at=excluded.evidence_at`, namespace)
	}
	return nil
}

func (w *Writer) saveAccounting(ctx context.Context, revision int64) error {
	var sources []string
	var counts, sizes, ordinals []int64
	var clocks []*time.Time
	for _, source := range tableNames() {
		state := w.accounting[source]
		if state == nil {
			continue
		}
		sources = append(sources, source)
		counts = append(counts, state.records)
		sizes = append(sizes, state.bytes)
		ordinals = append(ordinals, state.nextOrdinal)
		clocks = append(clocks, state.evidenceAt)
	}
	_, err := w.tx.Exec(ctx, `INSERT INTO cao_ingestion_accounting(namespace,collection,revision,records,bytes,next_ordinal,evidence_at)
		SELECT $1,s.collection,$2,s.records,s.bytes,s.next_ordinal,s.evidence_at
		FROM unnest($3::text[],$4::bigint[],$5::bigint[],$6::timestamptz[],$7::bigint[]) s(collection,records,next_ordinal,evidence_at,bytes)
		ON CONFLICT(namespace,collection) DO UPDATE SET revision=excluded.revision,records=excluded.records,
			bytes=excluded.bytes,next_ordinal=excluded.next_ordinal,evidence_at=excluded.evidence_at`,
		w.store.namespace, revision, sources, counts, ordinals, clocks, sizes)
	return err
}

func (w *Writer) loadAccounting(ctx context.Context) error {
	rows, err := w.tx.Query(ctx, `SELECT collection,records,bytes,next_ordinal,evidence_at FROM cao_ingestion_accounting
		WHERE namespace=$1 AND revision=$2`, w.store.namespace, w.revision)
	if err != nil {
		return err
	}
	found := 0
	for rows.Next() {
		var source string
		value := &collectionAccounting{}
		if err := rows.Scan(&source, &value.records, &value.bytes, &value.nextOrdinal, &value.evidenceAt); err != nil {
			rows.Close()
			return err
		}
		if w.accounting[source] == nil {
			rows.Close()
			return errors.New("unregistered collection in publication accounting")
		}
		w.accounting[source] = value
		found++
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	if found == len(w.accounting) {
		return nil
	}
	// Other canonical writers invalidate this revision-qualified acceleration.
	// Reconstruct from authoritative typed rows before using it, never from a
	// process-local cache or an assumed absence of concurrent writers.
	postgresxLog.Printf("reconstructing ingestion accounting revision=%d", w.revision)
	var capacityCurrent bool
	if err := w.tx.QueryRow(ctx, "SELECT coalesce(capacity_revision=revision,false) FROM cao_state WHERE namespace=$1",
		w.store.namespace).Scan(&capacityCurrent); err != nil {
		return err
	}
	if !capacityCurrent {
		db := w.capacitySQL(false)
		db.preserveClocks = true
		if err := measureRunAccounting(ctx, w.store.namespace, "", db); err != nil {
			return err
		}
	}
	batch := &pgx.Batch{}
	var sources []string
	for _, source := range tableNames() {
		if w.accounting[source] == nil {
			continue
		}
		table := entityTables[source]
		sources = append(sources, source)
		if table.partitioned {
			batch.Queue(`SELECT coalesce(sum(records),0)::bigint,coalesce(sum(bytes),0)::bigint,coalesce(max(next_ordinal),0),max(evidence_at)
				FROM cao_run_accounting WHERE namespace=$1 AND collection=$2`, w.store.namespace, source)
		} else {
			batch.Queue("SELECT count(*),0::bigint,coalesce(max(ordinal)+1,0),max("+entityClockSQL(table, "")+
				") FROM "+query.SQLIdentifier(table.name)+" WHERE namespace=$1", w.store.namespace)
		}
	}
	results := w.tx.SendBatch(ctx, batch)
	for _, source := range sources {
		value := w.accounting[source]
		if err := results.QueryRow().Scan(&value.records, &value.bytes, &value.nextOrdinal, &value.evidenceAt); err != nil {
			_ = results.Close()
			return err
		}
	}
	if err := results.Close(); err != nil {
		return err
	}
	return w.saveAccounting(ctx, w.revision)
}

func (w *Writer) capacitySQL(auditClocks bool) capacitySQL {
	return capacitySQL{
		auditClocks:  auditClocks,
		linkedCutoff: dayStart(w.ingestedAt.AddDate(0, 0, -w.store.linkedRetentionDays)),
		exec: func(ctx context.Context, sql string, args ...any) error {
			_, err := w.tx.Exec(ctx, sql, args...)
			return err
		},
		row: func(ctx context.Context, sql string, args ...any) capacityRow {
			return w.tx.QueryRow(ctx, sql, args...)
		},
	}
}
