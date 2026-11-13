package postgresx

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

func checkProvisionedContract(ctx context.Context, db *sql.DB) error {
	var digest string
	if err := db.QueryRowContext(ctx, `SELECT digest FROM cao_contract`).Scan(&digest); err != nil {
		return fmt.Errorf("canonical storage is not provisioned; run database bootstrap as owner: %w", err)
	}
	if digest != fmt.Sprintf("%x", sha256.Sum256([]byte(nativeSchemaSQL))) {
		return errors.New("native storage contract changed; provision a new canonical database without removing operational audit history")
	}
	var statistics bool
	if err := db.QueryRowContext(ctx, `SELECT coalesce(has_function_privilege(to_regprocedure('cao_analyze_native()'), 'EXECUTE'), false)
		AND coalesce(has_function_privilege(to_regprocedure('cao_analyze_native(text[])'), 'EXECUTE'), false)`).Scan(&statistics); err != nil {
		return err
	}
	if !statistics {
		return errors.New("native statistics capability is not provisioned; run database bootstrap and canonical grants as owner")
	}
	return nil
}

// PostgreSQL 16 silently skips ANALYZE for non-owners. Grant only this fixed
// operation, not table ownership, arbitrary SQL, or caller-chosen object names.
func initializeStatistics(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(712083241, 17483)`); err != nil {
		return err
	}
	var schema string
	if err := tx.QueryRowContext(ctx, `SELECT current_schema()`).Scan(&schema); err != nil {
		return err
	}
	name := pgx.Identifier{schema, "cao_analyze_native"}.Sanitize()
	var body strings.Builder
	body.WriteString("CREATE OR REPLACE FUNCTION " + name + "() RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $statistics$ BEGIN\n")
	for _, source := range tableNames() {
		body.WriteString("ANALYZE " + pgx.Identifier{schema, entityTables[source].name}.Sanitize() + ";\n")
	}
	body.WriteString("END; $statistics$; REVOKE ALL ON FUNCTION " + name + "() FROM PUBLIC;")
	if _, err := tx.ExecContext(ctx, body.String()); err != nil {
		return err
	}
	var sources []string
	for _, source := range tableNames() {
		sources = append(sources, "'"+source+"'")
	}
	body.Reset()
	body.WriteString("CREATE OR REPLACE FUNCTION " + name + "(sources text[]) RETURNS void LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $statistics$ BEGIN\n")
	body.WriteString("IF sources IS NULL OR cardinality(sources)>" + fmt.Sprint(len(sources)) +
		" OR EXISTS(SELECT 1 FROM unnest(sources) s WHERE s IS NULL OR s NOT IN (" +
		strings.Join(sources, ",") + ")) THEN RAISE EXCEPTION 'invalid canonical statistics scope'; END IF;\n")
	for _, source := range tableNames() {
		body.WriteString("IF '" + source + "'=ANY(sources) THEN ANALYZE " +
			pgx.Identifier{schema, entityTables[source].name}.Sanitize() + "; END IF;\n")
	}
	body.WriteString("END; $statistics$; REVOKE ALL ON FUNCTION " + name + "(text[]) FROM PUBLIC;")
	if _, err := tx.ExecContext(ctx, body.String()); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) checkProvisionedPartitions(ctx context.Context, now time.Time, retention, linkedRetention int) error {
	now = now.UTC()
	if linkedRetention > retention {
		return errors.New("linked retention days must not exceed run retention days")
	}
	existing, _, err := s.existingPartitions(ctx, now.AddDate(0, 0, -retention), now.AddDate(0, 0, -linkedRetention))
	if err != nil {
		return err
	}
	start := dayStart(now.AddDate(0, 0, -retention))
	linkedStart := dayStart(now.AddDate(0, 0, -linkedRetention))
	end := dayStart(now).AddDate(0, 0, futureRunDays+1)
	for at := start; at.Before(end); at = at.AddDate(0, 0, 1) {
		for _, table := range partitionTables {
			if table != "runs" && at.Before(linkedStart) {
				continue
			}
			if !existing[table+"_d"+at.Format("20060102")] {
				return errors.New("canonical partitions are not provisioned through the required window; run database maintain as owner")
			}
		}
	}
	return nil
}
