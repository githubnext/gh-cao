package postgresx

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/query"
)

// ReadValueBatch restores all metric packets and their shared proof from one
// enrollment-pinned snapshot. A workflow with no packets is not an error.
func (a Acquisition) ReadValueBatch(
	ctx context.Context, scope collectnative.EnrollmentScope, workflow string,
) ([]model.Row, []ValueProofFile, error) {
	if scope.Epoch <= 0 || scope.InstallationID <= 0 || !validEnrollmentRepository(scope.Repository) ||
		!evidenceSlug.MatchString(workflow) {
		return nil, nil, collectnative.ErrIdentity
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	connection, err := pgx.ConnectConfig(ctx, a.store.config.Copy())
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = connection.Close(context.WithoutCancel(ctx)) }()
	tx, err := connection.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := a.store.fenceValueScope(ctx, tx, scope); err != nil {
		return nil, nil, err
	}
	identity := model.Row{
		"repository": scope.Repository, "sourcePath": ".github/workflows/" + workflow + ".md",
		"repositoryId": "repository:" + evidenceComponent(scope.Repository),
		"workflowId":   "workflow:" + evidenceComponent(scope.Repository+":.github/workflows/"+workflow+".md"),
	}
	known, err := valueParentKnown(ctx, tx, a.store.namespace, identity)
	if err != nil {
		return nil, nil, err
	}
	if !known {
		return nil, nil, ErrValueParentMissing
	}
	rows, err := tx.Query(ctx, `SELECT id FROM operational_value_evidence WHERE namespace=$1
		AND repository_id=$2 AND workflow_id=$3 ORDER BY id LIMIT 4`,
		a.store.namespace, identity["repositoryId"], identity["workflowId"])
	if err != nil {
		return nil, nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, nil, err
	}
	if len(ids) > 3 {
		return nil, nil, ErrValueInvalid
	}
	packets := make([]model.Row, 0, len(ids))
	for _, id := range ids {
		packet, err := readValuePacket(ctx, tx, a.store.namespace, id)
		if err != nil {
			return nil, nil, err
		}
		packets = append(packets, packet)
	}
	proofRows, err := tx.Query(ctx, `SELECT path,digest,content FROM cao_value_proof_files
		WHERE namespace=$1 AND repository=$2 AND workflow=$3 ORDER BY path COLLATE "C" LIMIT $4`,
		a.store.namespace, scope.Repository, workflow, ValueProofMaxFiles+1)
	if err != nil {
		return nil, nil, err
	}
	files := []ValueProofFile{}
	var total int
	for proofRows.Next() {
		var file ValueProofFile
		if err := proofRows.Scan(&file.Path, &file.Digest, &file.Content); err != nil {
			proofRows.Close()
			return nil, nil, err
		}
		total += len(file.Content)
		if len(file.Content) > ValueProofFileMax || total > ValueProofMaxBytes || len(files) >= ValueProofMaxFiles {
			proofRows.Close()
			return nil, nil, collectnative.ErrCapacity
		}
		files = append(files, file)
	}
	err = proofRows.Err()
	proofRows.Close()
	if err != nil {
		return nil, nil, err
	}
	files, err = validateValueProofFiles(files)
	if err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, err
	}
	return packets, files, nil
}

// PublishValueBatch replaces all metric packets and their proof in one
// transaction, checking the enrollment epoch and every starting packet first.
func (a Acquisition) PublishValueBatch(
	ctx context.Context, scope collectnative.EnrollmentScope, previous, packets []model.Row,
	files []ValueProofFile, updatedAt time.Time,
) (ValuePublication, error) {
	if scope.Epoch <= 0 || scope.InstallationID <= 0 || !validEnrollmentRepository(scope.Repository) ||
		len(packets) != 3 || updatedAt.IsZero() {
		return ValuePublication{}, collectnative.ErrIdentity
	}
	files, err := validateValueProofFiles(files)
	if err != nil {
		return ValuePublication{}, err
	}
	type item struct {
		row, old   model.Row
		id, digest string
		oldDigest  string
	}
	items := make([]item, 0, len(packets))
	seen := map[string]bool{}
	workflow, capture, cutoff := "", "", ""
	for _, packet := range packets {
		row, digest, err := normalizeValuePublication(packet)
		if err != nil {
			return ValuePublication{}, err
		}
		path := evidenceText(row["sourcePath"])
		name := strings.TrimSuffix(strings.TrimPrefix(path, ".github/workflows/"), ".md")
		id := evidenceText(row["id"])
		if row["repository"] != scope.Repository || !evidenceSlug.MatchString(name) ||
			seen[id] || workflow != "" && (workflow != name ||
			capture != row["observedAt"] || cutoff != row["windowEnd"]) {
			return ValuePublication{}, ErrValueInvalid
		}
		seen[id] = true
		workflow, capture, cutoff = name, evidenceText(row["observedAt"]), evidenceText(row["windowEnd"])
		items = append(items, item{row: row, id: id, digest: digest})
	}
	if len(previous) != 0 && len(previous) != len(items) {
		return ValuePublication{}, ErrValueConflict
	}
	for _, packet := range previous {
		old, digest, err := normalizeValuePublication(packet)
		if err != nil {
			return ValuePublication{}, err
		}
		found := false
		for i := range items {
			if items[i].id == old["id"] {
				items[i].old, items[i].oldDigest = old, digest
				found = true
				break
			}
		}
		if !found {
			return ValuePublication{}, ErrValueConflict
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].id < items[j].id })
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	connection, err := pgx.ConnectConfig(ctx, a.store.config.Copy())
	if err != nil {
		return ValuePublication{}, err
	}
	defer func() { _ = connection.Close(context.WithoutCancel(ctx)) }()
	tx, err := connection.Begin(ctx)
	if err != nil {
		return ValuePublication{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	for _, lock := range []string{
		`SELECT pg_advisory_xact_lock_shared(712083241, 17484)`,
		`SELECT pg_advisory_xact_lock(hashtextextended(current_schema() || ':' || $1, 0))`,
	} {
		args := []any{}
		if strings.Contains(lock, "$1") {
			args = append(args, a.store.namespace)
		}
		if _, err := tx.Exec(ctx, lock, args...); err != nil {
			return ValuePublication{}, err
		}
	}
	if err := a.store.fenceValueScope(ctx, tx, scope); err != nil {
		return ValuePublication{}, err
	}
	result := ValuePublication{ID: items[0].id, ObservedAt: capture}
	if err := tx.QueryRow(ctx, `SELECT revision FROM cao_state WHERE namespace=$1 FOR UPDATE`,
		a.store.namespace).Scan(&result.Revision); err != nil {
		return ValuePublication{}, err
	}
	allSame := true
	for _, entry := range items {
		known, err := valueParentKnown(ctx, tx, a.store.namespace, entry.row)
		if err != nil {
			return ValuePublication{}, err
		}
		if !known {
			return ValuePublication{}, ErrValueParentMissing
		}
		current, err := readValuePacket(ctx, tx, a.store.namespace, entry.id)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return ValuePublication{}, err
		}
		currentDigest := ""
		if current != nil {
			_, currentDigest, err = normalizeValuePublication(current)
			if err != nil {
				return ValuePublication{}, err
			}
		}
		if currentDigest != entry.oldDigest && currentDigest != entry.digest {
			return ValuePublication{}, ErrValueConflict
		}
		if currentDigest != entry.digest {
			allSame = false
			if current != nil {
				if err := validateValueReplacement(current, entry.row); err != nil {
					return ValuePublication{}, err
				}
			}
		}
	}
	proof := &valueProofPublication{repository: scope.Repository, workflow: workflow, files: files}
	if allSame {
		if err := a.store.sameValueProof(ctx, tx, proof); err != nil {
			return ValuePublication{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return ValuePublication{}, err
		}
		return result, nil
	}
	writer := &Writer{store: a.store, tx: tx, ordinals: map[string]int64{}, batches: map[string][][]any{}}
	for _, source := range []string{"$operationalValueEvidence", "$operationalValueEvidence.facts"} {
		var ordinal int64
		if err := tx.QueryRow(ctx, "SELECT coalesce(max(ordinal)+1,0) FROM "+
			query.SQLIdentifier(entityTables[source].name)+" WHERE namespace=$1",
			a.store.namespace).Scan(&ordinal); err != nil {
			return ValuePublication{}, err
		}
		writer.ordinals[source] = ordinal
	}
	for _, entry := range items {
		for _, statement := range []string{
			`DELETE FROM operational_value_evidence_facts WHERE namespace=$1 AND packet_id=$2`,
			`DELETE FROM operational_value_evidence WHERE namespace=$1 AND id=$2`,
		} {
			if _, err := tx.Exec(ctx, statement, a.store.namespace, entry.id); err != nil {
				return ValuePublication{}, err
			}
		}
		if err := writer.Append(ctx, "$operationalValueEvidence", entry.row); err != nil {
			return ValuePublication{}, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO cao_value_publications(namespace,packet_id,digest,published_at)
			VALUES($1,$2,$3,now()) ON CONFLICT(namespace,packet_id) DO UPDATE SET digest=excluded.digest,published_at=excluded.published_at`,
			a.store.namespace, entry.id, entry.digest); err != nil {
			return ValuePublication{}, err
		}
	}
	if err := writer.Flush(ctx); err != nil {
		return ValuePublication{}, err
	}
	if err := writer.valueQuality(ctx); err != nil {
		return ValuePublication{}, err
	}
	if err := a.store.replaceValueProof(ctx, tx, scope.Repository, workflow, files, updatedAt); err != nil {
		return ValuePublication{}, err
	}
	at, _ := evidenceTime(capture)
	if err := tx.QueryRow(ctx, `UPDATE cao_state SET revision=revision+1,evaluated_at=greatest(evaluated_at,$2)
		WHERE namespace=$1 RETURNING revision`, a.store.namespace, at).Scan(&result.Revision); err != nil {
		return ValuePublication{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ValuePublication{}, err
	}
	result.Updated = true
	return result, nil
}
