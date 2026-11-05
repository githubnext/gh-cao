package postgresx

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/query"
	"github.com/githubnext/gh-cao/server/internal/sqlbuilder"
)

// Activate atomically consumes one exact safe-evidence digest and publishes its
// canonical graph. A crash cannot acknowledge evidence without its rows/revision.
// Older attempts are consumed without replacing a newer attempt's facts.
func (a Acquisition) Activate(ctx context.Context, publication AcquisitionPublication) (bool, error) {
	identity, expected := publication.Identity, publication.Digest
	decoded, digestErr := hex.DecodeString(expected)
	if identity.Validate() != nil || publication.Epoch <= 0 || digestErr != nil || len(decoded) != sha256.Size {
		return false, collectnative.ErrIdentity
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	tx, err := a.store.beginInvocation(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	epoch, err := a.owned(ctx, tx, identity, true)
	if err != nil {
		return false, err
	}
	if epoch != publication.Epoch {
		return false, collectnative.ErrClaimLost
	}
	if a.inventoryDigest != "" {
		if err := a.inventoryReady(ctx, tx, collectnative.EnrollmentScope{
			Repository: identity.Repository, InstallationID: identity.InstallationID, Epoch: epoch,
		}, a.inventoryDigest); err != nil {
			return false, err
		}
	}
	var digest string
	var consumed sql.NullString
	var content []byte
	var published time.Time
	err = tx.QueryRowContext(ctx, `SELECT digest,consumed_digest,content,published_at FROM cao_acquisition_attempts
		WHERE namespace=$1 AND repository=$2 AND run_id=$3 AND attempt=$4 AND epoch=$5 AND digest IS NOT NULL FOR UPDATE`,
		a.store.namespace, identity.Repository, identity.RunID, identity.Attempt, epoch).Scan(&digest, &consumed, &content, &published)
	if errors.Is(err, sql.ErrNoRows) || err == nil && expected != digest {
		return false, collectnative.ErrClaimLost
	}
	if err != nil {
		return false, err
	}
	if consumed.Valid && consumed.String == digest {
		return false, tx.Commit()
	}
	sum := sha256.Sum256(content)
	if len(content) > acquisitionMaxBytes || hex.EncodeToString(sum[:]) != digest {
		return false, errors.New("native acquisition publication integrity failure")
	}
	var evidence collectnative.Evidence
	if err := decodeJSON(content, &evidence); err != nil || evidence.Identity != identity {
		return false, errors.New("invalid native acquisition publication identity")
	}
	projection, err := collectnative.ProjectCanonical(ctx, evidence, published, collectnative.Limits{})
	if err != nil {
		return false, err
	}
	var previous int
	err = tx.QueryRowContext(ctx, `SELECT coalesce(attempt,1) FROM runs WHERE namespace=$1 AND id=$2`,
		a.store.namespace, projection.RunID).Scan(&previous)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	changed := previous <= identity.Attempt
	if changed {
		if err := a.store.projectAcquisition(ctx, tx, &projection, published); err != nil {
			return false, err
		}
		if err := a.replayOutcomes(ctx, tx, publication, projection.RunID, published); err != nil {
			return false, err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE cao_acquisition_attempts SET consumed_digest=digest
		WHERE namespace=$1 AND repository=$2 AND run_id=$3 AND attempt=$4 AND digest=$5`,
		a.store.namespace, identity.Repository, identity.RunID, identity.Attempt, digest); err != nil {
		return false, err
	}
	return changed, tx.Commit()
}

func (s *Store) projectAcquisition(ctx context.Context, tx *sql.Tx, projection *collectnative.Projection, published time.Time) error {
	if err := s.nativeRouting(ctx, tx, projection); err != nil {
		return err
	}
	run := projection.Collections["runs"][0]
	if err := s.invocationParents(ctx, tx, run, published); err != nil {
		return err
	}
	rebindAcquisitionParents(projection, run["repositoryId"].(string), run["workflowId"].(string))
	for _, collection := range []string{"repositories", "workflows"} {
		parent := projection.Collections[collection][0]
		parent["id"] = run["repositoryId"]
		if collection == "workflows" {
			parent["id"] = run["workflowId"]
		}
		if err := s.fillAcquisitionParent(ctx, tx, "$"+collection, parent); err != nil {
			return err
		}
	}
	runAt := published
	for _, field := range []string{"createdAt", "startedAt", "completedAt", "updatedAt"} {
		if value := run[field]; value != nil {
			bound, err := (entityColumn{kind: "timestamp"}).bind(value)
			if err != nil {
				return err
			}
			runAt = bound.(time.Time)
			break
		}
	}
	retainDetail := !runAt.Before(dayStart(published.AddDate(0, 0, -s.linkedRetentionDays)))
	for _, source := range tableNames() {
		table := entityTables[source]
		if !table.partitioned || source == "$runs" {
			continue
		}
		statement, _, err := sqlbuilder.Build("DELETE FROM {} WHERE namespace=$1 AND run_id=$2", sqlbuilder.Identifier(table.name))
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, statement, s.namespace, projection.RunID); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM runs WHERE namespace=$1 AND id=$2`, s.namespace, projection.RunID); err != nil {
		return err
	}
	referenced := map[string]bool{}
	for _, collection := range []string{"graderObservations", "evalObservations", "experimentAssignments"} {
		for _, row := range projection.Collections[collection] {
			if id, ok := row["auditId"].(string); ok {
				referenced[id] = true
			}
		}
	}
	// Parent order mirrors the typed schema, not map iteration.
	for _, collection := range []string{"runs", "experiments", "graders", "evals", "experimentAssignments",
		"audits", "domains", "tools", "skills", "friction", "issues", "graderObservations", "evalObservations"} {
		for _, row := range projection.Collections[collection] {
			if collection != "runs" && entityTables["$"+collection].partitioned && !retainDetail {
				continue
			}
			if collection == "audits" {
				if (row["type"] == "model.inventory" || row["type"] == "firewall.event") ||
					!referenced[fmt.Sprint(row["id"])] && discardAuditCopy(row, run) {
					continue
				}
				if err := validateAuditProjection(row); err != nil {
					return err
				}
			}
			switch collection {
			case "experiments", "graders", "evals":
				if err := s.mergeAcquisitionDefinition(ctx, tx, "$"+collection, row, published); err != nil {
					return err
				}
			case "issues":
				if err := s.mergeAcquisitionIssue(ctx, tx, row, runAt); err != nil {
					return err
				}
			default:
				if err := validateAuditReferenceProjection("$"+collection, row); err != nil {
					return err
				}
				if err := insertInvocationEntity(ctx, tx, s.namespace, "$"+collection, row, runAt); err != nil {
					return err
				}
			}
		}
		if len(projection.Collections[collection]) > 0 {
			if err := partialInvocationQuality(ctx, tx, s.namespace, "$"+collection); err != nil {
				return err
			}
		}
	}
	for _, source := range []string{"$repositories", "$workflows"} {
		if err := partialInvocationQuality(ctx, tx, s.namespace, source); err != nil {
			return err
		}
	}
	if err := s.reconcileDailyInvocation(ctx, tx, projection.RunID); err != nil {
		return err
	}
	if err := s.capInvocationRuns(ctx, tx); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `UPDATE cao_state SET revision=revision+1,evaluated_at=greatest(evaluated_at,$2)
		WHERE namespace=$1`, s.namespace, published)
	return err
}

// Fill absent structural metadata without replacing inventory-owned facts,
// campaign membership, enablement or rollout settings.
func (s *Store) fillAcquisitionParent(ctx context.Context, tx *sql.Tx, source string, row model.Row) error {
	table := entityTables[source]
	args := []any{s.namespace, row["id"], stringPresence(table, row)}
	updates := []string{"present_fields=present_fields | $3::bit varying"}
	for index, column := range table.columns {
		if column.field == "id" {
			continue
		}
		value, present := entityFieldValue(row, column.field)
		if !present {
			continue
		}
		bound, err := column.bind(value)
		if err != nil {
			return err
		}
		args = append(args, bound)
		name := query.SQLIdentifier(column.name)
		updates = append(updates, fmt.Sprintf("%s=CASE WHEN get_bit(present_fields,%d)=0 THEN $%d ELSE %s END",
			name, index, len(args), name))
	}
	statement, _, err := sqlbuilder.Build("UPDATE {} SET {} WHERE namespace=$1 AND id=$2",
		sqlbuilder.Identifier(table.name), sqlbuilder.Fragment(strings.Join(updates, ",")))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, statement, args...)
	return err
}

func (s *Store) nativeRouting(ctx context.Context, tx *sql.Tx, projection *collectnative.Projection) error {
	audits := projection.Collections["audits"]
	if !slices.ContainsFunc(audits, func(row model.Row) bool { return row["type"] == "session.format" }) {
		return nil
	}
	contract, err := collectnative.CanonicalContract()
	if err != nil {
		return err
	}
	metadata := func(source string) model.Metadata {
		return model.Metadata{"source-id": source, "source-kind": "canonical", "availability": "available",
			"as-of": "1970-01-01T00:00:00.000Z", "retrieved-at": "1970-01-01T00:00:00.000Z",
			"completeness": "unknown", "freshness": "unknown"}
	}
	reader := &readTransaction{store: s, tx: tx, publicationSources: map[string]model.Source{
		"$runs":   {Rows: projection.Collections["runs"], Metadata: metadata("$runs")},
		"$audits": {Rows: audits, Metadata: metadata("$audits")},
	}}
	result, _, err := reader.ExecuteSQLPlan(ctx, contract.Queries, []string{"routing-ingestion-aggregate"})
	if err != nil {
		return fmt.Errorf("native declarative routing projection: %w", err)
	}
	rows := result["routing-ingestion-aggregate"].Rows
	if len(rows) > 1 || len(rows) == 1 && rows[0]["canonical-run-id"] != projection.RunID {
		return errors.New("routing aggregate has no unique owning native run")
	}
	run := projection.Collections["runs"][0]
	if len(rows) == 1 {
		run["routingAggregateVersion"] = 1
		for field, input := range contract.RoutingFields {
			run[field] = rows[0][input]
		}
	}
	for _, row := range audits {
		for _, field := range contract.Transient {
			delete(row, field)
		}
	}
	return nil
}

func (s *Store) mergeAcquisitionDefinition(ctx context.Context, tx *sql.Tx, source string, row model.Row, at time.Time) error {
	table := entityTables[source]
	// Definitions have stable workflow-owned identities. Scalar fields are
	// updated only when this observation is at least as recent.
	var previous time.Time
	err := tx.QueryRowContext(ctx, "SELECT coalesce(last_observed_at,'epoch') FROM "+query.SQLIdentifier(table.name)+
		" WHERE namespace=$1 AND id=$2", s.namespace, row["id"]).Scan(&previous)
	if errors.Is(err, sql.ErrNoRows) {
		return insertInvocationEntity(ctx, tx, s.namespace, source, row, at)
	}
	if err != nil {
		return err
	}
	updates := []string{"first_observed_at=least(first_observed_at,$3)", "last_observed_at=greatest(last_observed_at,$3)",
		"present_fields=present_fields | $4::bit varying"}
	args := []any{s.namespace, row["id"], row["timestamp"], stringPresence(table, row)}
	for index, column := range table.columns {
		if column.field == "id" || column.field == "firstObservedAt" || column.field == "lastObservedAt" {
			continue
		}
		value, present := entityFieldValue(row, column.field)
		if !present {
			continue
		}
		bound, err := column.bind(value)
		if err != nil {
			return err
		}
		args = append(args, bound)
		name := query.SQLIdentifier(column.name)
		updates = append(updates, fmt.Sprintf("%s=CASE WHEN $3>=last_observed_at OR get_bit(present_fields,%d)=0 THEN $%d ELSE %s END",
			name, index, len(args), name))
	}
	statement, _, err := sqlbuilder.Build("UPDATE {} SET {} WHERE namespace=$1 AND id=$2",
		sqlbuilder.Identifier(table.name), sqlbuilder.Fragment(strings.Join(updates, ",")))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, statement, args...)
	return err
}

func (s *Store) mergeAcquisitionIssue(ctx context.Context, tx *sql.Tx, row model.Row, runAt time.Time) error {
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM issues WHERE namespace=$1 AND id=$2`,
		s.namespace, row["id"]).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		return insertInvocationEntity(ctx, tx, s.namespace, "$issues", row, runAt)
	}
	if count != 1 {
		return errors.New("native acquisition found duplicate retained canonical issue identity")
	}
	table := entityTables["$issues"]
	observed, err := (entityColumn{kind: "timestamp"}).bind(row["observedAt"])
	if err != nil || observed == nil {
		return errors.New("native issue observation requires a recorded clock")
	}
	// Issue identities are shared across runs. Activity orders equal-clock
	// observations by source identity, whose prefix is the canonical run ID.
	wins := `($3>observed_at OR $3=observed_at AND $4::text COLLATE "C">=run_id COLLATE "C")`
	args := []any{s.namespace, row["id"], observed, row["runId"], stringPresence(table, row), runAt}
	updates := []string{"present_fields=present_fields | $5::bit varying",
		"run_at=CASE WHEN " + wins + " THEN $6 ELSE run_at END"}
	for index, column := range table.columns {
		if column.field == "id" {
			continue
		}
		value, present := entityFieldValue(row, column.field)
		if !present {
			continue
		}
		bound, err := column.bind(value)
		if err != nil {
			return err
		}
		args = append(args, bound)
		name := query.SQLIdentifier(column.name)
		updates = append(updates, fmt.Sprintf("%s=CASE WHEN %s OR get_bit(present_fields,%d)=0 THEN $%d ELSE %s END",
			name, wins, index, len(args), name))
	}
	statement, _, err := sqlbuilder.Build("UPDATE issues SET {} WHERE namespace=$1 AND id=$2",
		sqlbuilder.Fragment(strings.Join(updates, ",")))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, statement, args...)
	return err
}

func rebindAcquisitionParents(projection *collectnative.Projection, repository, workflow string) {
	ids := map[string]string{}
	for _, collection := range []string{"experiments", "graders", "evals"} {
		for _, row := range projection.Collections[collection] {
			old := fmt.Sprint(row["id"])
			kind := strings.TrimSuffix(collection, "s")
			ids[old] = kind + ":gh-aw-logs:" + model.EncodeCoordinate(workflow+":"+fmt.Sprint(row["name"]))
		}
	}
	for _, rows := range projection.Collections {
		for _, row := range rows {
			if _, present := row["repositoryId"]; present {
				row["repositoryId"] = repository
			}
			if _, present := row["workflowId"]; present {
				row["workflowId"] = workflow
			}
			for _, field := range []string{"id", "experimentId", "graderId", "evalId"} {
				if replacement, exists := ids[fmt.Sprint(row[field])]; exists {
					row[field] = replacement
				}
			}
		}
	}
}
