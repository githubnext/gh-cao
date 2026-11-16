package postgresx

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/githubnext/gh-cao/server/internal/mining"
	"github.com/githubnext/gh-cao/server/internal/query"
)

var outputLifecycleParentSources = []string{"$campaigns", "$repositories", "$workflows"}

func (w *Writer) preserveOutputLifecycles(ctx context.Context) error {
	cutoff := w.ingestedAt.AddDate(0, 0, -mining.OutputLifecycleRetentionDays)
	if _, err := w.tx.Exec(ctx, `CREATE TEMP TABLE output_lifecycles_preserved ON COMMIT DROP AS
		SELECT * FROM output_lifecycles WHERE namespace=$1 AND observed_at >= $2`,
		w.store.namespace, cutoff); err != nil {
		return err
	}
	conditions := map[string]string{
		"$repositories": `EXISTS(SELECT 1 FROM output_lifecycles_preserved l
			WHERE l.namespace=t.namespace AND l.repository_id=t.id)`,
		"$workflows": `EXISTS(SELECT 1 FROM output_lifecycles_preserved l
			WHERE l.namespace=t.namespace AND l.workflow_id=t.id)`,
		"$campaigns": `EXISTS(SELECT 1 FROM workflows f JOIN output_lifecycles_preserved l
			ON l.namespace=f.namespace AND l.workflow_id=f.id
			WHERE f.namespace=t.namespace AND f.campaign_id=t.id)`,
	}
	for _, source := range outputLifecycleParentSources {
		table := entityTables[source]
		statement := "CREATE TEMP TABLE " + query.SQLIdentifier(table.name+"_output_lifecycle_preserved") +
			" ON COMMIT DROP AS SELECT t.* FROM " + query.SQLIdentifier(table.name) +
			" t WHERE t.namespace=$1 AND " + conditions[source]
		if _, err := w.tx.Exec(ctx, statement, w.store.namespace); err != nil {
			return err
		}
	}
	return nil
}

func adoptionAuthoritySQL(alias string) string {
	return `(CASE ` + alias + `.adoption_source
		WHEN 'github-api' THEN 3
		WHEN 'github-webhook' THEN 2
		WHEN 'delivery-rule' THEN 1
		WHEN 'adoption-registry' THEN 1
		ELSE 0 END)`
}

func (w *Writer) restoreOutputLifecycles(ctx context.Context) error {
	for _, source := range outputLifecycleParentSources {
		table := entityTables[source]
		columns := []string{"namespace", "ordinal", "present_fields"}
		projection := []string{
			"p.namespace",
			"n.next_ordinal + row_number() OVER(ORDER BY p.ordinal) - 1",
			"p.present_fields",
		}
		for _, column := range table.columns {
			columns = append(columns, query.SQLIdentifier(column.name))
			projection = append(projection, "p."+query.SQLIdentifier(column.name))
		}
		name := query.SQLIdentifier(table.name)
		statement := fmt.Sprintf(`INSERT INTO %s(%s) SELECT %s FROM %s p
			CROSS JOIN (SELECT coalesce(max(ordinal)+1,0) AS next_ordinal FROM %s WHERE namespace=$1) n
			WHERE NOT EXISTS(SELECT 1 FROM %s t WHERE t.namespace=p.namespace AND t.id=p.id)`,
			name, strings.Join(columns, ","), strings.Join(projection, ","),
			query.SQLIdentifier(table.name+"_output_lifecycle_preserved"), name, name)
		if _, err := w.tx.Exec(ctx, statement, w.store.namespace); err != nil {
			return fmt.Errorf("restore output lifecycle parent %s: %w", source, err)
		}
	}

	previousAuthority := adoptionAuthoritySQL("p")
	incomingAuthority := adoptionAuthoritySQL("t")
	var conflict int
	err := w.tx.QueryRow(ctx, `SELECT 1 FROM output_lifecycles t
		JOIN output_lifecycles_preserved p ON p.namespace=t.namespace AND p.id=t.id
		WHERE t.namespace=$1 AND (
			t.repository_id<>p.repository_id OR t.workflow_id<>p.workflow_id OR t.run_id<>p.run_id
			OR t.safe_output_type<>p.safe_output_type OR t.delivery_state<>p.delivery_state
			OR t.delivery_signal<>p.delivery_signal
			OR t.delivery_source<>p.delivery_source OR t.delivery_evidence_id<>p.delivery_evidence_id
			OR t.rule_version<>p.rule_version
			OR (t.target_repository IS NOT NULL AND p.target_repository IS NOT NULL AND t.target_repository<>p.target_repository)
			OR (t.target_url IS NOT NULL AND p.target_url IS NOT NULL AND t.target_url<>p.target_url)
			OR (`+previousAuthority+`=`+incomingAuthority+`
				AND p.adoption_observed_at=t.adoption_observed_at
				AND (p.adoption_state,p.adoption_signal,p.adoption_at,p.adoption_source)
					IS DISTINCT FROM (t.adoption_state,t.adoption_signal,t.adoption_at,t.adoption_source))
			OR (coalesce(p.survival_observed_at,'epoch')=coalesce(t.survival_observed_at,'epoch')
				AND (p.survival_state,p.survival_signal,p.survival_observed_at)
					IS DISTINCT FROM (t.survival_state,t.survival_signal,t.survival_observed_at))
			OR (coalesce(p.reversed_at,'epoch')=coalesce(t.reversed_at,'epoch')
				AND (p.reversal_signal,p.reversed_at) IS DISTINCT FROM (t.reversal_signal,t.reversed_at))
		) LIMIT 1`, w.store.namespace).Scan(&conflict)
	if err == nil {
		return errors.New("conflicting output lifecycle replacement evidence")
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	previousAdoptionWins := `(` + previousAuthority + `>` + incomingAuthority +
		` OR (` + previousAuthority + `=` + incomingAuthority + ` AND p.adoption_observed_at>t.adoption_observed_at))`
	previousSurvivalWins := `coalesce(p.survival_observed_at,'epoch')>coalesce(t.survival_observed_at,'epoch')`
	previousReversalWins := `coalesce(p.reversed_at,'epoch')>coalesce(t.reversed_at,'epoch')`
	if _, err := w.tx.Exec(ctx, `UPDATE output_lifecycles t SET
		present_fields=t.present_fields | p.present_fields,
		target_repository=coalesce(t.target_repository,p.target_repository),
		target_url=coalesce(t.target_url,p.target_url),
		title=coalesce(t.title,p.title),
		delivered_at=least(t.delivered_at,p.delivered_at),
		delivery_observed_at=least(t.delivery_observed_at,p.delivery_observed_at),
		adoption_at=CASE WHEN `+previousAdoptionWins+` THEN p.adoption_at ELSE t.adoption_at END,
		adoption_observed_at=CASE WHEN `+previousAdoptionWins+` THEN p.adoption_observed_at ELSE t.adoption_observed_at END,
		adoption_signal=CASE WHEN `+previousAdoptionWins+` THEN p.adoption_signal ELSE t.adoption_signal END,
		adoption_source=CASE WHEN `+previousAdoptionWins+` THEN p.adoption_source ELSE t.adoption_source END,
		adoption_state=CASE WHEN `+previousAdoptionWins+` THEN p.adoption_state ELSE t.adoption_state END,
		reversal_signal=CASE WHEN `+previousReversalWins+` THEN p.reversal_signal ELSE t.reversal_signal END,
		reversed_at=CASE WHEN `+previousReversalWins+` THEN p.reversed_at ELSE t.reversed_at END,
		survival_observed_at=CASE WHEN `+previousSurvivalWins+` THEN p.survival_observed_at ELSE t.survival_observed_at END,
		survival_signal=CASE WHEN `+previousSurvivalWins+` THEN p.survival_signal ELSE t.survival_signal END,
		survival_state=CASE WHEN `+previousSurvivalWins+` THEN p.survival_state ELSE t.survival_state END,
		observed_at=greatest(t.observed_at,p.observed_at)
		FROM output_lifecycles_preserved p
		WHERE t.namespace=$1 AND p.namespace=t.namespace AND p.id=t.id`, w.store.namespace); err != nil {
		return err
	}
	table := entityTables["$outputLifecycles"]
	columns := []string{"namespace", "ordinal", "present_fields"}
	projection := []string{
		"p.namespace",
		"n.next_ordinal + row_number() OVER(ORDER BY p.ordinal) - 1",
		"p.present_fields",
	}
	for _, column := range table.columns {
		columns = append(columns, query.SQLIdentifier(column.name))
		projection = append(projection, "p."+query.SQLIdentifier(column.name))
	}
	name := query.SQLIdentifier(table.name)
	statement := fmt.Sprintf(`INSERT INTO %s(%s) SELECT %s FROM output_lifecycles_preserved p
		CROSS JOIN (SELECT coalesce(max(ordinal)+1,0) AS next_ordinal FROM %s WHERE namespace=$1) n
		WHERE NOT EXISTS(SELECT 1 FROM %s t WHERE t.namespace=p.namespace AND t.id=p.id)`,
		name, strings.Join(columns, ","), strings.Join(projection, ","), name, name)
	if _, err := w.tx.Exec(ctx, statement, w.store.namespace); err != nil {
		return err
	}
	cutoff := w.ingestedAt.AddDate(0, 0, -mining.OutputLifecycleRetentionDays)
	_, err = w.tx.Exec(ctx, `DELETE FROM output_lifecycles WHERE namespace=$1 AND observed_at<$2`,
		w.store.namespace, cutoff)
	return err
}
