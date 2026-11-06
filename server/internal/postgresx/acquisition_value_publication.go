package postgresx

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/model"
)

// PublishValue atomically checks the original enrollment and starting packet
// before replacing accepted evidence and reusable proof. The caller must enforce
// its separate reviewed registration, frozen collector contract and quota grant;
// ingestion enrollment alone does not authorize Value acquisition.
func (a Acquisition) PublishValue(
	ctx context.Context, scope collectnative.EnrollmentScope, previous, packet model.Row,
	files []ValueProofFile, updatedAt time.Time,
) (ValuePublication, error) {
	if scope.Epoch <= 0 || scope.InstallationID <= 0 || !validEnrollmentRepository(scope.Repository) {
		return ValuePublication{}, collectnative.ErrIdentity
	}
	row, _, err := normalizeValuePublication(packet)
	if err != nil {
		return ValuePublication{}, err
	}
	if row["repository"] != scope.Repository || updatedAt.IsZero() {
		return ValuePublication{}, ErrValueInvalid
	}
	var previousDigest string
	if previous != nil {
		start, digest, err := normalizeValuePublication(previous)
		if err != nil {
			return ValuePublication{}, err
		}
		if start["id"] != row["id"] {
			return ValuePublication{}, ErrValueInvalid
		}
		previousDigest = digest
	}
	files, err = validateValueProofFiles(files)
	if err != nil {
		return ValuePublication{}, err
	}
	workflow := strings.TrimSuffix(strings.TrimPrefix(evidenceText(row["sourcePath"]), ".github/workflows/"), ".md")
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	return a.store.publishOperationalValue(ctx, row, &valueProofPublication{
		repository: scope.Repository, workflow: workflow, files: files, updatedAt: updatedAt,
		scope: &scope, previous: previousDigest,
	})
}

func (s *Store) fenceValuePublication(ctx context.Context, tx pgx.Tx, row model.Row, proof *valueProofPublication) error {
	if err := s.fenceValueScope(ctx, tx, *proof.scope); err != nil {
		return err
	}
	current, err := readValuePacket(ctx, tx, s.namespace, evidenceText(row["id"]))
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	var digest string
	if current != nil {
		_, digest, err = normalizeValuePublication(current)
		if err != nil {
			return err
		}
	}
	if digest != proof.previous {
		_, incoming, err := normalizeValuePublication(row)
		if err != nil {
			return err
		}
		if digest == incoming {
			return s.sameValueProof(ctx, tx, proof)
		}
		return ErrValueConflict
	}
	return nil
}

func (s *Store) fenceValueScope(ctx context.Context, tx pgx.Tx, scope collectnative.EnrollmentScope) error {
	var installation, epoch int64
	err := tx.QueryRow(ctx, `SELECT installation_id,epoch FROM cao_acquisition_scopes
		WHERE namespace=$1 AND repository=$2 FOR SHARE`, s.namespace, scope.Repository).Scan(&installation, &epoch)
	if errors.Is(err, pgx.ErrNoRows) || err == nil &&
		(installation != scope.InstallationID || epoch != scope.Epoch) {
		return collectnative.ErrClaimLost
	}
	return err
}

// ReadValue restores a single accepted packet and its bounded reusable proof
// from one source-pinned snapshot, never from another replica's local workspace.
func (a Acquisition) ReadValue(ctx context.Context, scope collectnative.EnrollmentScope, workflow string,
) (model.Row, []ValueProofFile, error) {
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
	packet, err := readLatestValuePacket(ctx, tx, a.store.namespace, scope.Repository, workflow)
	if err != nil {
		return nil, nil, err
	}
	rows, err := tx.Query(ctx, `SELECT path,digest,content FROM cao_value_proof_files
		WHERE namespace=$1 AND repository=$2 AND workflow=$3 ORDER BY path COLLATE "C" LIMIT $4`,
		a.store.namespace, scope.Repository, workflow, ValueProofMaxFiles+1)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	files := []ValueProofFile{}
	var total int
	for rows.Next() {
		var file ValueProofFile
		if err := rows.Scan(&file.Path, &file.Digest, &file.Content); err != nil {
			return nil, nil, err
		}
		total += len(file.Content)
		if len(file.Content) > ValueProofFileMax || total > ValueProofMaxBytes || len(files) >= ValueProofMaxFiles {
			return nil, nil, collectnative.ErrCapacity
		}
		files = append(files, file)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, err
	}
	rows.Close()
	files, err = validateValueProofFiles(files)
	if err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, nil, err
	}
	return packet, files, nil
}

func (s *Store) sameValueProof(ctx context.Context, tx pgx.Tx, proof *valueProofPublication) error {
	rows, err := tx.Query(ctx, `SELECT path,digest FROM cao_value_proof_files
		WHERE namespace=$1 AND repository=$2 AND workflow=$3 ORDER BY path COLLATE "C" LIMIT $4`,
		s.namespace, proof.repository, proof.workflow, ValueProofMaxFiles+1)
	if err != nil {
		return err
	}
	defer rows.Close()
	index := 0
	for rows.Next() {
		var path, digest string
		if err := rows.Scan(&path, &digest); err != nil {
			return err
		}
		if index >= len(proof.files) || path != proof.files[index].Path || digest != proof.files[index].Digest {
			return ErrValueConflict
		}
		index++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if index != len(proof.files) {
		return ErrValueConflict
	}
	return nil
}
