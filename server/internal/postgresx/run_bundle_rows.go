package postgresx

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/githubnext/gh-cao/server/internal/query"
)

func (w *Writer) applyRunBundleRows(ctx context.Context) (map[string]bool, error) {
	changed := map[string]bool{}
	if err := w.prepareBundleStages(ctx); err != nil {
		return nil, err
	}
	for _, source := range []string{"$campaigns", "$repositories", "$workflows", "$experiments", "$graders", "$evals"} {
		if !w.staged[source] {
			continue
		}
		if err := w.fenceBundleParent(ctx, source); err != nil {
			return nil, err
		}
		if err := w.mergeBundleStage(ctx, source, "true"); err != nil {
			return nil, err
		}
		if source == "$repositories" {
			if _, err := w.tx.Exec(ctx, `UPDATE repositories_stage r SET lifecycle=l.lifecycle,
				present_fields=set_bit(r.present_fields,$2,1) FROM cao_repository_lifecycle l
				WHERE r.namespace=$1 AND l.namespace=r.namespace AND l.id=r.id`,
				w.store.namespace, repositoryLifecycleIndex()); err != nil {
				return nil, err
			}
		}
		count, err := w.upsertBundleStage(ctx, source)
		if err != nil {
			return nil, err
		}
		changed[source] = count > 0
	}
	cutoff := dayStart(w.ingestedAt.AddDate(0, 0, -w.store.linkedRetentionDays))
	var newAttempt, moved, expired, fullEvidence bool
	var complete []string
	if err := w.tx.QueryRow(ctx, `SELECT coalesce(bool_or(new_attempt),false),
		coalesce(bool_or(run_at<>old_run_at),false),coalesce(bool_or(run_at<$1),false),
		coalesce(bool_or(full_evidence),false),
		ARRAY(SELECT DISTINCT c.collection FROM run_bundle_coverage c JOIN affected_runs k ON k.id=c.id
			WHERE c.coverage='complete') FROM affected_runs`, cutoff).
		Scan(&newAttempt, &moved, &expired, &fullEvidence, &complete); err != nil {
		return nil, err
	}
	for _, source := range tableNames() {
		table := entityTables[source]
		if !table.partitioned || source == "$runs" {
			continue
		}
		missing := "true"
		if w.staged[source] {
			missing = "NOT EXISTS(SELECT 1 FROM " + query.SQLIdentifier(table.name+"_stage") + " s WHERE s.id=t.id AND s.run_id=t.run_id)"
		}
		protected := "false"
		switch source {
		case "$audits":
			protected = "coalesce(t.source='conclusion-admission',false)"
		case "$graderObservations":
			protected = "t.id LIKE 'grader-observation:conclusion-admission:%'"
		}
		statement := "DELETE FROM " + query.SQLIdentifier(table.name) + ` t USING affected_runs k
			WHERE t.namespace=$1 AND t.run_id=k.id AND t.run_at=k.old_run_at
			AND (k.new_attempt OR k.run_at<$2 OR (k.full_evidence AND (` + protected + `))
				OR (NOT (` + protected + `) AND ` + missing + ` AND EXISTS(
					SELECT 1 FROM run_bundle_coverage c WHERE c.id=k.id AND c.collection=$3 AND c.coverage='complete')))`
		if newAttempt || expired || slices.Contains(complete, source) || fullEvidence && protected != "false" {
			tag, err := w.tx.Exec(ctx, statement, w.store.namespace, cutoff, source)
			if err != nil {
				return nil, err
			}
			w.accounting[source].records -= tag.RowsAffected()
			changed[source] = tag.RowsAffected() > 0
		}
		if moved {
			tag, err := w.tx.Exec(ctx, "UPDATE "+query.SQLIdentifier(table.name)+` t SET run_at=k.run_at
				FROM affected_runs k WHERE t.namespace=$1 AND t.run_id=k.id AND t.run_at=k.old_run_at
					AND k.run_at<>k.old_run_at AND k.run_at>=$2`, w.store.namespace, cutoff)
			if err != nil {
				return nil, err
			}
			changed[source] = changed[source] || tag.RowsAffected() > 0
		}
	}
	if moved {
		if _, err := w.tx.Exec(ctx, `UPDATE runs r SET run_at=k.run_at FROM affected_runs k
			WHERE r.namespace=$1 AND r.id=k.id AND r.run_at=k.old_run_at AND k.run_at<>k.old_run_at`, w.store.namespace); err != nil {
			return nil, err
		}
	}
	count, err := w.upsertBundleStage(ctx, "$runs")
	if err != nil {
		return nil, err
	}
	changed["$runs"] = count > 0
	for _, source := range tableNames() {
		table := entityTables[source]
		if !table.partitioned || source == "$runs" || !w.staged[source] {
			continue
		}
		if _, err := w.tx.Exec(ctx, "UPDATE "+query.SQLIdentifier(table.name+"_stage")+`
			s SET run_at=k.run_at FROM affected_runs k WHERE s.run_id=k.id`); err != nil {
			return nil, err
		}
		if _, err := w.tx.Exec(ctx, "DELETE FROM "+query.SQLIdentifier(table.name+"_stage")+" WHERE run_at<$1", cutoff); err != nil {
			return nil, err
		}
		if err := w.mergeBundleStage(ctx, source, `EXISTS(SELECT 1 FROM run_bundle_coverage c
			WHERE c.id=s.run_id AND c.collection='`+source+`' AND c.coverage='partial')`); err != nil {
			return nil, err
		}
		count, err := w.upsertBundleStage(ctx, source)
		if err != nil {
			return nil, err
		}
		changed[source] = changed[source] || count > 0
	}
	if w.staged["$transactions"] {
		count, err := w.upsertBundleStage(ctx, "$transactions")
		if err != nil {
			return nil, err
		}
		changed["$transactions"] = count > 0
	}
	return changed, nil
}

func (w *Writer) prepareBundleStages(ctx context.Context) error {
	full := "false"
	if w.staged["$audits"] {
		full += " OR EXISTS(SELECT 1 FROM audits_stage a WHERE a.run_id=k.id AND a.source='session')"
	}
	if w.staged["$graderObservations"] {
		full += " OR EXISTS(SELECT 1 FROM grader_observations_stage o WHERE o.run_id=k.id)"
	}
	for _, statement := range []string{
		"ALTER TABLE affected_runs ADD COLUMN full_evidence boolean",
		"UPDATE affected_runs k SET full_evidence=(" + full + ")",
		"ALTER TABLE affected_runs ADD COLUMN enrich_conclusion boolean",
		`UPDATE affected_runs k SET enrich_conclusion=NOT k.new_attempt AND NOT k.full_evidence AND EXISTS(
			SELECT 1 FROM audits a WHERE a.namespace=$1 AND a.run_id=k.id AND a.run_at=k.old_run_at
				AND a.source='conclusion-admission' AND a.source_sequence=0
				AND CASE WHEN a.name ~ '^[1-9][0-9]*$' THEN a.name::numeric=k.attempt ELSE false END)`,
		`DELETE FROM runs_stage s WHERE NOT EXISTS(SELECT 1 FROM affected_runs k WHERE k.id=s.id)`,
	} {
		var args []any
		if strings.Contains(statement, "$1") {
			args = []any{w.store.namespace}
		}
		if _, err := w.tx.Exec(ctx, statement, args...); err != nil {
			return err
		}
	}
	for _, source := range tableNames() {
		table := entityTables[source]
		if table.partitioned && source != "$runs" && w.staged[source] {
			if _, err := w.tx.Exec(ctx, "DELETE FROM "+query.SQLIdentifier(table.name+"_stage")+
				" s WHERE NOT EXISTS(SELECT 1 FROM affected_runs k WHERE k.id=s.run_id)"); err != nil {
				return err
			}
		}
	}
	for _, entry := range []struct{ source, child, field string }{
		{"$graders", "$graderObservations", "grader_id"},
		{"$evals", "$evalObservations", "eval_id"},
		{"$experiments", "$experimentAssignments", "experiment_id"},
		{"$workflows", "$runs", "workflow_id"},
		{"$repositories", "$runs", "repository_id"},
		{"$campaigns", "$workflows", "campaign_id"},
	} {
		if !w.staged[entry.source] {
			continue
		}
		keep := "false"
		if w.staged[entry.child] {
			keep = "EXISTS(SELECT 1 FROM " + query.SQLIdentifier(entityTables[entry.child].name+"_stage") +
				" c WHERE c." + query.SQLIdentifier(entry.field) + "=s.id)"
		}
		if _, err := w.tx.Exec(ctx, "DELETE FROM "+query.SQLIdentifier(entityTables[entry.source].name+"_stage")+
			" s WHERE NOT ("+keep+")"); err != nil {
			return err
		}
	}
	if err := w.mergeBundleStage(ctx, "$runs", "NOT k.new_attempt"); err != nil {
		return err
	}
	if _, err := w.tx.Exec(ctx, `UPDATE runs_stage s SET run_at=coalesce(s.created_at,s.started_at,s.completed_at,s.updated_at,k.old_run_at,s.run_at)
		FROM affected_runs k WHERE s.id=k.id`); err != nil {
		return err
	}
	_, err := w.tx.Exec(ctx, `UPDATE affected_runs k SET run_at=s.run_at FROM runs_stage s WHERE s.id=k.id`)
	return err
}

func (w *Writer) mergeBundleStage(ctx context.Context, source, condition string) error {
	table := entityTables[source]
	var updates []string
	updates = append(updates, "present_fields=s.present_fields | t.present_fields")
	for index, column := range table.columns {
		if column.field == "id" {
			continue
		}
		name := query.SQLIdentifier(column.name)
		present := fmt.Sprintf("get_bit(s.present_fields,%d)=1", index)
		if source == "$runs" {
			present += " AND (NOT k.enrich_conclusion OR s." + name + " IS NOT NULL)"
		}
		updates = append(updates, fmt.Sprintf("%s=CASE WHEN %s THEN s.%s ELSE t.%s END",
			name, present, name, name))
	}
	from, key := query.SQLIdentifier(table.name)+" t", "t.namespace=s.namespace AND t.id=s.id"
	if source == "$runs" {
		from += ",affected_runs k"
		key += " AND k.id=s.id AND t.run_at=k.old_run_at"
	} else if table.partitioned {
		key += " AND t.run_at=s.run_at AND t.run_id=s.run_id"
	}
	_, err := w.tx.Exec(ctx, "UPDATE "+query.SQLIdentifier(table.name+"_stage")+" s SET "+
		strings.Join(updates, ",")+" FROM "+from+" WHERE "+key+" AND ("+condition+")")
	return err
}

func (w *Writer) upsertBundleStage(ctx context.Context, source string) (int64, error) {
	table := entityTables[source]
	stage, target := query.SQLIdentifier(table.name+"_stage"), query.SQLIdentifier(table.name)
	columns := []string{"namespace", "ordinal", "present_fields"}
	selects := []string{"s.namespace", "coalesce(t.ordinal,$2+s.ordinal)", "s.present_fields"}
	updates, previous, incoming := []string{"present_fields=excluded.present_fields"}, []string{"t.present_fields"}, []string{"excluded.present_fields"}
	for _, column := range table.columns {
		name := query.SQLIdentifier(column.name)
		columns = append(columns, name)
		selects = append(selects, "s."+name)
		if column.field != "id" {
			updates = append(updates, name+"=excluded."+name)
			previous, incoming = append(previous, "t."+name), append(incoming, "excluded."+name)
		}
	}
	join, conflict := "t.namespace=s.namespace AND t.id=s.id", "(namespace,id)"
	if table.partitioned {
		columns, selects = append(columns, "run_at"), append(selects, "s.run_at")
		join += " AND t.run_at=s.run_at"
		conflict = "(namespace,id,run_at)"
	}
	if err := w.validateBundleIdentity(ctx, source); err != nil {
		return 0, err
	}
	var inserted, changed int64
	err := w.tx.QueryRow(ctx, `WITH new_rows AS MATERIALIZED (
		SELECT count(*) AS inserted FROM `+stage+` s LEFT JOIN `+target+` t ON `+join+`
			WHERE s.namespace=$1 AND t.id IS NULL),
		applied AS (INSERT INTO `+target+` AS t(`+strings.Join(columns, ",")+`)
		SELECT `+strings.Join(selects, ",")+` FROM `+stage+` s LEFT JOIN `+target+` t ON `+join+`
		WHERE s.namespace=$1 ON CONFLICT`+conflict+` DO UPDATE SET `+strings.Join(updates, ",")+`
		WHERE ROW(`+strings.Join(previous, ",")+`) IS DISTINCT FROM ROW(`+strings.Join(incoming, ",")+`)
		RETURNING id)
		SELECT (SELECT inserted FROM new_rows),count(*) FROM applied`,
		w.store.namespace, w.accounting[source].nextOrdinal).Scan(&inserted, &changed)
	if err != nil {
		return 0, fmt.Errorf("publish run bundle %s: %w", source, err)
	}
	w.accounting[source].records += inserted
	w.accounting[source].nextOrdinal += w.ordinals[source]
	return changed, nil
}

func (w *Writer) validateBundleIdentity(ctx context.Context, source string) error {
	table := entityTables[source]
	predicate := ""
	switch source {
	case "$workflows":
		predicate = "t.repository_id IS DISTINCT FROM s.repository_id"
	case "$experiments", "$graders", "$evals":
		predicate = "t.workflow_id IS DISTINCT FROM s.workflow_id"
	default:
		if table.partitioned && source != "$runs" {
			predicate = "t.run_id<>s.run_id"
		}
	}
	if predicate == "" {
		return nil
	}
	var conflict bool
	err := w.tx.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM "+query.SQLIdentifier(table.name)+" t JOIN "+
		query.SQLIdentifier(table.name+"_stage")+` s ON s.namespace=t.namespace AND s.id=t.id
		WHERE t.namespace=$1 AND (`+predicate+`))`, w.store.namespace).Scan(&conflict)
	if err != nil {
		return err
	}
	if conflict {
		return errors.New("run bundle changes a shared parent or evidence owner; authoritative reconciliation is required")
	}
	return nil
}
