package postgresx

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/query"
)

// PublishNativeValuePreparation merges reviewed imports and collection decisions.
// The revision and all exact source epochs are checked under the namespace
// writer lock. It never writes acquisition proof or grants collector authority.
func (s *Store) PublishNativeValuePreparation(
	ctx context.Context, expectedRevision int64, scopes []collectnative.EnrollmentScope,
	collections, packets []model.Row,
) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if len(scopes) == 0 || len(scopes) > 10000 || len(collections)+len(packets) > 10000 {
		return 0, ErrValueInvalid
	}
	index := make(map[string]collectnative.EnrollmentScope, len(scopes))
	for _, scope := range scopes {
		if !validEnrollmentRepository(scope.Repository) || scope.InstallationID <= 0 || scope.Epoch <= 0 {
			return 0, ErrValueInvalid
		}
		if _, exists := index[scope.Repository]; exists {
			return 0, ErrValueInvalid
		}
		index[scope.Repository] = scope
	}
	bytes := 0
	for _, rows := range [][]model.Row{collections, packets} {
		identities := make(map[string]bool, len(rows))
		for _, row := range rows {
			if _, exists := index[strings.ToLower(evidenceText(row["repository"]))]; !exists {
				return 0, ErrValueInvalid
			}
			identity := evidenceText(row["id"])
			if identity == "" || identities[identity] {
				return 0, ErrValueInvalid
			}
			identities[identity] = true
			content, err := json.Marshal(row)
			if err != nil {
				return 0, ErrValueInvalid
			}
			bytes += len(content)
		}
	}
	if bytes > nativeValueInputBytes {
		return 0, ErrValueInvalid
	}
	connection, err := pgx.ConnectConfig(ctx, s.config.Copy())
	if err != nil {
		return 0, err
	}
	defer func() { _ = connection.Close(context.WithoutCancel(ctx)) }()
	tx, err := connection.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock_shared(712083241,17484)`); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || ':' || $1,0))`, s.namespace); err != nil {
		return 0, err
	}
	var revision int64
	if err := tx.QueryRow(ctx, `SELECT revision FROM cao_state WHERE namespace=$1 FOR UPDATE`, s.namespace).Scan(&revision); err != nil {
		return 0, err
	}
	if revision != expectedRevision {
		return 0, ErrOperationalValueRevisionStale
	}
	for _, scope := range scopes {
		var installation, epoch int64
		err := tx.QueryRow(ctx, `SELECT installation_id,epoch FROM cao_acquisition_scopes
			WHERE namespace=$1 AND repository=$2 FOR SHARE`, s.namespace, scope.Repository).Scan(&installation, &epoch)
		if errors.Is(err, pgx.ErrNoRows) || err == nil && (installation != scope.InstallationID || epoch != scope.Epoch) {
			return 0, collectnative.ErrClaimLost
		}
		if err != nil {
			return 0, err
		}
	}
	writer := &Writer{store: s, tx: tx, ordinals: map[string]int64{}, batches: map[string][][]any{}}
	for _, source := range []string{"$workflows", "$workflowValueCollections", "$operationalValueEvidence", "$operationalValueEvidence.facts"} {
		var ordinal int64
		if err := tx.QueryRow(ctx, "SELECT coalesce(max(ordinal)+1,0) FROM "+query.SQLIdentifier(entityTables[source].name)+
			" WHERE namespace=$1", s.namespace).Scan(&ordinal); err != nil {
			return 0, err
		}
		writer.ordinals[source] = ordinal
	}
	if err := writer.preserveCollectionDecisions(ctx); err != nil {
		return 0, err
	}
	for _, row := range collections {
		if err := validateWorkflowValueCollection(row); err != nil {
			return 0, err
		}
		if err := writer.nativeValueHealthParent(ctx, row); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM workflow_value_collections WHERE namespace=$1 AND id=$2`, s.namespace, row["id"]); err != nil {
			return 0, err
		}
		if err := writer.Append(ctx, "$workflowValueCollections", row); err != nil {
			return 0, err
		}
	}
	for _, packet := range packets {
		row, digest, err := normalizeValuePublication(packet)
		if err != nil {
			return 0, err
		}
		if known, err := valueParentKnown(ctx, tx, s.namespace, row); err != nil || !known {
			return 0, errors.Join(err, ErrValueParentMissing)
		}
		previous, err := readValuePacket(ctx, tx, s.namespace, evidenceText(row["id"]))
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return 0, err
		}
		if previous != nil {
			_, previousDigest, err := normalizeValuePublication(previous)
			if err != nil {
				return 0, err
			}
			if digest == previousDigest {
				continue
			}
			if err := validateValueReplacement(previous, row); err != nil {
				return 0, err
			}
		}
		for _, statement := range []string{
			`DELETE FROM operational_value_evidence_facts WHERE namespace=$1 AND packet_id=$2`,
			`DELETE FROM operational_value_evidence WHERE namespace=$1 AND id=$2`,
		} {
			if _, err := tx.Exec(ctx, statement, s.namespace, row["id"]); err != nil {
				return 0, err
			}
		}
		if err := writer.Append(ctx, "$operationalValueEvidence", row); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO cao_value_publications(namespace,packet_id,digest,published_at)
			VALUES($1,$2,$3,now()) ON CONFLICT(namespace,packet_id)
			DO UPDATE SET digest=excluded.digest,published_at=excluded.published_at`, s.namespace, row["id"], digest); err != nil {
			return 0, err
		}
	}
	if err := writer.Flush(ctx); err != nil {
		return 0, err
	}
	if err := writer.reconcileCollectionDecisions(ctx); err != nil {
		return 0, err
	}
	if len(collections) > 0 {
		if _, err := tx.Exec(ctx, `INSERT INTO cao_quality(namespace,collection,availability,completeness,freshness,as_of,retrieved_at)
			SELECT $1,'$workflowValueCollections','available','unknown','unknown',min(observed_at),max(observed_at)
			FROM workflow_value_collections WHERE namespace=$1 HAVING count(*)>0
			ON CONFLICT(namespace,collection) DO UPDATE SET availability=excluded.availability,
			completeness='unknown',freshness='unknown',as_of=excluded.as_of,retrieved_at=excluded.retrieved_at`, s.namespace); err != nil {
			return 0, err
		}
	}
	if len(packets) > 0 {
		if err := writer.valueQuality(ctx); err != nil {
			return 0, err
		}
	}
	if len(collections)+len(packets) > 0 {
		if err := tx.QueryRow(ctx, `UPDATE cao_state SET revision=revision+1,evaluated_at=greatest(evaluated_at,$2)
			WHERE namespace=$1 RETURNING revision`, s.namespace, time.Now().UTC()).Scan(&revision); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, err
	}
	return revision, nil
}

func (w *Writer) nativeValueHealthParent(ctx context.Context, row model.Row) error {
	known, err := valueParentKnown(ctx, w.tx, w.store.namespace, row)
	if err != nil || known {
		return err
	}
	// A reviewed registration may precede the first observed workflow/run.
	// Preserve only its structural filename parent, never invent an observed
	// unconfigured workflow from ingestion enrollment alone.
	if row["registration"] == "unconfigured" {
		return ErrValueParentMissing
	}
	var repositoryKnown bool
	if err := w.tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM repositories WHERE namespace=$1
		AND id=$2 AND lower(owner || '/' || name)=$3)`, w.store.namespace,
		row["repositoryId"], row["repository"]).Scan(&repositoryKnown); err != nil {
		return err
	}
	if !repositoryKnown {
		return ErrValueParentMissing
	}
	if err := w.Append(ctx, "$workflows", model.Row{"id": row["workflowId"], "repositoryId": row["repositoryId"],
		"path": row["sourcePath"], "state": "unknown"}); err != nil {
		return err
	}
	return w.Flush(ctx)
}
