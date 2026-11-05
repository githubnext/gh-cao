package postgresx

import (
	"context"
	"database/sql"
	"strings"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/query"
)

// Scope erasure is part of the ownership transaction, never a later snapshot
// replacement that could remove a newly enrolled owner's publication.
func (s *Store) eraseNativeRepository(ctx context.Context, tx *sql.Tx, repository string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM cao_acquisition_outcomes WHERE namespace=$1 AND repository=$2`,
		s.namespace, repository); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM cao_acquisition_value_hints WHERE namespace=$1 AND repository=$2`,
		s.namespace, repository); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM cao_acquisition_inventory WHERE namespace=$1 AND repository=$2`,
		s.namespace, repository); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM repositories
		WHERE namespace=$1 AND lower(owner || '/' || name)=$2 LIMIT 257`, s.namespace, repository)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	if len(ids) > 256 {
		return collectnative.ErrCapacity
	}
	for _, source := range tableNames() {
		table := entityTables[source]
		if source == "$repositories" || source == "$workflows" || source == "$runs" || !table.canonical {
			continue
		}
		var predicates []string
		for _, column := range table.columns {
			switch column.field {
			case "repositoryId":
				predicates = append(predicates, `repository_id=ANY($2::text[])`)
			case "workflowId":
				predicates = append(predicates, `workflow_id IN (SELECT id FROM workflows WHERE namespace=$1 AND repository_id=ANY($2::text[]))`)
			}
		}
		if table.partitioned {
			predicates = append(predicates, `run_id IN (SELECT id FROM runs WHERE namespace=$1 AND repository_id=ANY($2::text[]))
				OR left(run_id,length($3))=$3`)
		}
		if len(predicates) == 0 {
			continue
		}
		predicate := strings.Join(predicates, " OR ")
		args := []any{s.namespace, ids}
		if strings.Contains(predicate, "$3") {
			args = append(args, "github:run:"+repository+":")
		}
		//nolint:gosec // Identifiers use the generated registry; ownership predicates are fixed expressions.
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+query.SQLIdentifier(table.name)+
			" WHERE namespace=$1 AND ("+predicate+")", args...); err != nil {
			return err
		}
	}
	for _, statement := range []string{
		`DELETE FROM runs WHERE namespace=$1 AND repository_id=ANY($2::text[])`,
		`DELETE FROM workflows WHERE namespace=$1 AND repository_id=ANY($2::text[])`,
		`DELETE FROM repositories WHERE namespace=$1 AND id=ANY($2::text[])`,
		`DELETE FROM cao_repository_lifecycle WHERE namespace=$1 AND id=ANY($2::text[])`,
	} {
		if _, err := tx.ExecContext(ctx, statement, s.namespace, ids); err != nil {
			return err
		}
	}
	if supported, err := dailyCapability(ctx, tx); err != nil {
		return err
	} else if supported {
		for _, statement := range []string{
			`DELETE FROM run_count_contributions WHERE namespace=$1 AND repository_id=ANY($2::text[])`,
			`DELETE FROM daily_run_counts WHERE namespace=$1 AND repository_id=ANY($2::text[])`,
		} {
			if _, err := tx.ExecContext(ctx, statement, s.namespace, ids); err != nil {
				return err
			}
		}
	}
	// A frozen frame cannot be edited without invalidating its probabilities.
	// Withdraw its bundles atomically rather than retain removed evidence or
	// silently substitute live detail for the independently sampled snapshot.
	for _, table := range statisticalTables {
		//nolint:gosec // Identifiers come only from the private generated TypeSpec registry.
		if _, err := tx.ExecContext(ctx, "DELETE FROM "+query.SQLIdentifier(table.name)+" WHERE namespace=$1", s.namespace); err != nil {
			return err
		}
	}
	_, err = tx.ExecContext(ctx, `DELETE FROM cao_sample_cao_quality WHERE namespace=$1`, s.namespace)
	return err
}
