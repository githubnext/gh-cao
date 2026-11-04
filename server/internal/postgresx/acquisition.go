package postgresx

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/githubnext/gh-cao/server/internal/collectnative"
)

const (
	acquisitionMaxBytes    = 32 << 20
	acquisitionRepoBytes   = 64 << 20
	acquisitionTotalBytes  = 2 << 30
	acquisitionMaxAttempts = 10000
)

// Acquisition owns private safe evidence in the canonical database, independent
// of derived canonical replacement and operational cache eviction.
type Acquisition struct{ store *Store }

func (s *Store) Acquisition() Acquisition { return Acquisition{store: s} }

var _ collectnative.Store = Acquisition{}

type AcquisitionPublication struct {
	Identity collectnative.Identity
	Epoch    int64
	Digest   string
}

// Publications enumerates a bounded batch of durable pending intents, not
// evidence bodies. Reads do not claim or consume them; Activate fences each
// exact digest under the canonical writer transaction.
func (a Acquisition) Publications(ctx context.Context, limit int) ([]AcquisitionPublication, error) {
	if limit < 1 || limit > 256 {
		return nil, collectnative.ErrCapacity
	}
	rows, err := a.store.db.QueryContext(ctx, `SELECT a.repository,s.installation_id,a.run_id,a.attempt,a.epoch,a.digest
		FROM cao_acquisition_attempts a JOIN cao_acquisition_scopes s
		ON s.namespace=a.namespace AND s.repository=a.repository AND s.epoch=a.epoch
		WHERE a.namespace=$1 AND s.installation_id>0 AND a.digest IS NOT NULL
		AND a.digest IS DISTINCT FROM a.consumed_digest
		ORDER BY a.published_at,a.repository,a.run_id,a.attempt LIMIT $2`, a.store.namespace, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := make([]AcquisitionPublication, 0, limit)
	for rows.Next() {
		var publication AcquisitionPublication
		if err := rows.Scan(&publication.Identity.Repository, &publication.Identity.InstallationID,
			&publication.Identity.RunID, &publication.Identity.Attempt, &publication.Epoch, &publication.Digest); err != nil {
			return nil, err
		}
		result = append(result, publication)
	}
	return result, rows.Err()
}

// Replay activates a bounded pending batch. Another instance can resume after
// any interruption without reacquiring GitHub artifacts or credentials.
func (a Acquisition) Replay(ctx context.Context, limit int) (int, error) {
	publications, err := a.Publications(ctx, limit)
	if err != nil {
		return 0, err
	}
	changed := 0
	for _, publication := range publications {
		published, err := a.Activate(ctx, publication)
		if err != nil {
			return changed, err
		}
		if published {
			changed++
		}
	}
	return changed, nil
}

// SetEnrollment must be called only by the enrollment composition root after
// authenticated scope reconciliation. Zero withdraws scope and erases evidence.
// Every ownership change fences all previous claims, including transfer back to
// the same installation. It is not a new enrollment discovery authority.
func (a Acquisition) SetEnrollment(ctx context.Context, repository string, installation int64) error {
	if installation < 0 || (collectnative.Identity{Repository: repository, InstallationID: 1, RunID: 1, Attempt: 1}).Validate() != nil {
		return collectnative.ErrIdentity
	}
	tx, err := a.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `INSERT INTO cao_acquisition_scopes(namespace,repository,installation_id,epoch)
		VALUES($1,$2,$3,1) ON CONFLICT(namespace,repository) DO NOTHING`, a.store.namespace, repository, installation); err != nil {
		return err
	}
	var previous int64
	if err := tx.QueryRowContext(ctx, `SELECT installation_id FROM cao_acquisition_scopes
		WHERE namespace=$1 AND repository=$2 FOR UPDATE`, a.store.namespace, repository).Scan(&previous); err != nil {
		return err
	}
	if previous != installation || installation == 0 {
		if _, err := tx.ExecContext(ctx, `UPDATE cao_acquisition_scopes SET installation_id=$3,epoch=epoch+1
			WHERE namespace=$1 AND repository=$2`, a.store.namespace, repository, installation); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM cao_acquisition_attempts WHERE namespace=$1 AND repository=$2`,
			a.store.namespace, repository); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (a Acquisition) owned(ctx context.Context, tx *sql.Tx, identity collectnative.Identity, exclusive bool) (int64, error) {
	lock := " FOR SHARE"
	if exclusive {
		lock = " FOR UPDATE"
	}
	var installation, epoch int64
	err := tx.QueryRowContext(ctx, `SELECT installation_id,epoch FROM cao_acquisition_scopes
		WHERE namespace=$1 AND repository=$2`+lock, a.store.namespace, identity.Repository).Scan(&installation, &epoch)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && (installation != identity.InstallationID || installation == 0)) {
		return 0, collectnative.ErrIdentity
	}
	return epoch, err
}

func (a Acquisition) Claim(ctx context.Context, identity collectnative.Identity, ttl time.Duration) (collectnative.Claim, error) {
	if identity.Validate() != nil || ttl < time.Millisecond || ttl > time.Hour {
		return collectnative.Claim{}, collectnative.ErrIdentity
	}
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return collectnative.Claim{}, err
	}
	claim := collectnative.Claim{Identity: identity, Token: hex.EncodeToString(random)}
	tx, err := a.store.db.BeginTx(ctx, nil)
	if err != nil {
		return claim, err
	}
	defer func() { _ = tx.Rollback() }()
	// Scope locking orders claims, retention and withdrawal consistently.
	claim.Epoch, err = a.owned(ctx, tx, identity, true)
	if err != nil {
		return claim, err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM cao_acquisition_attempts WHERE namespace=$1 AND repository=$2
		AND coalesce(published_at,created_at) < clock_timestamp()-interval '30 days'
		AND digest IS NOT DISTINCT FROM consumed_digest
		AND (lease_until IS NULL OR lease_until<=clock_timestamp())`, a.store.namespace, identity.Repository); err != nil {
		return claim, err
	}
	var count int
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT count(*),coalesce(bool_or(run_id=$3 AND attempt=$4),false)
		FROM cao_acquisition_attempts WHERE namespace=$1 AND repository=$2`,
		a.store.namespace, identity.Repository, identity.RunID, identity.Attempt).Scan(&count, &exists); err != nil {
		return claim, err
	}
	if count >= acquisitionMaxAttempts && !exists {
		return claim, collectnative.ErrCapacity
	}
	err = tx.QueryRowContext(ctx, `INSERT INTO cao_acquisition_attempts
		(namespace,repository,run_id,attempt,epoch,generation,created_at,token,lease_until)
		VALUES($1,$2,$3,$4,$5,1,clock_timestamp(),$6,clock_timestamp()+$7::bigint*interval '1 millisecond')
		ON CONFLICT(namespace,repository,run_id,attempt) DO UPDATE SET
		epoch=excluded.epoch,generation=cao_acquisition_attempts.generation+1,token=excluded.token,lease_until=excluded.lease_until
		WHERE cao_acquisition_attempts.lease_until IS NULL OR cao_acquisition_attempts.lease_until<=clock_timestamp()
		RETURNING generation`, a.store.namespace, identity.Repository, identity.RunID, identity.Attempt, claim.Epoch, claim.Token, ttl.Milliseconds()).
		Scan(&claim.Generation)
	if errors.Is(err, sql.ErrNoRows) {
		return claim, collectnative.ErrBusy
	}
	if err != nil {
		return claim, err
	}
	return claim, tx.Commit()
}

func (a Acquisition) Publish(ctx context.Context, claim collectnative.Claim, evidence collectnative.Evidence) error {
	if claim.Identity != evidence.Identity || claim.Validate() != nil || claim.Token == "" || claim.Epoch <= 0 || claim.Generation <= 0 {
		return collectnative.ErrIdentity
	}
	content, digest, err := evidence.Bytes()
	if err != nil {
		return err
	}
	if len(content) > acquisitionMaxBytes {
		return collectnative.ErrCapacity
	}
	tx, err := a.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	epoch, err := a.owned(ctx, tx, claim.Identity, true)
	if err != nil {
		return err
	}
	if epoch != claim.Epoch {
		return collectnative.ErrClaimLost
	}
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		"native-acquisition:"+a.store.namespace); err != nil {
		return err
	}
	var retained int64
	if err := tx.QueryRowContext(ctx, `SELECT coalesce(sum(octet_length(content)),0) FROM cao_acquisition_attempts
		WHERE namespace=$1 AND repository=$2 AND NOT(run_id=$3 AND attempt=$4)`,
		a.store.namespace, claim.Repository, claim.RunID, claim.Attempt).Scan(&retained); err != nil {
		return err
	}
	if retained+int64(len(content)) > acquisitionRepoBytes {
		return collectnative.ErrCapacity
	}
	if err := tx.QueryRowContext(ctx, `SELECT coalesce(sum(octet_length(content)),0) FROM cao_acquisition_attempts
		WHERE namespace=$1 AND NOT(repository=$2 AND run_id=$3 AND attempt=$4)`,
		a.store.namespace, claim.Repository, claim.RunID, claim.Attempt).Scan(&retained); err != nil {
		return err
	}
	if retained+int64(len(content)) > acquisitionTotalBytes {
		return collectnative.ErrCapacity
	}
	result, err := tx.ExecContext(ctx, `UPDATE cao_acquisition_attempts SET
		digest=$9,content=$10,published_at=CASE WHEN digest IS DISTINCT FROM $9 THEN clock_timestamp() ELSE published_at END,
		token=NULL,lease_until=NULL
		WHERE namespace=$1 AND repository=$2 AND run_id=$3 AND attempt=$4 AND epoch=$5
		AND generation=$6 AND token=$7 AND lease_until>clock_timestamp() AND epoch=$8`,
		a.store.namespace, claim.Repository, claim.RunID, claim.Attempt, claim.Epoch,
		claim.Generation, claim.Token, epoch, digest, content)
	if err != nil {
		return err
	}
	if count, err := result.RowsAffected(); err != nil {
		return err
	} else if count != 1 {
		return collectnative.ErrClaimLost
	}
	return tx.Commit()
}

func (a Acquisition) Release(ctx context.Context, claim collectnative.Claim) error {
	if claim.Validate() != nil || claim.Token == "" {
		return collectnative.ErrIdentity
	}
	_, err := a.store.db.ExecContext(ctx, `UPDATE cao_acquisition_attempts SET token=NULL,lease_until=NULL
		WHERE namespace=$1 AND repository=$2 AND run_id=$3 AND attempt=$4 AND epoch=$5 AND generation=$6 AND token=$7`,
		a.store.namespace, claim.Repository, claim.RunID, claim.Attempt, claim.Epoch, claim.Generation, claim.Token)
	return err
}

// Pending reads one bounded durable handoff. A different process can replay it
// without credentials, scripts, artifacts, an Activity checkout or local files.
// Reading does not mark evidence consumed or imply canonical activation.
func (a Acquisition) Pending(ctx context.Context, identity collectnative.Identity) (collectnative.Evidence, string, error) {
	if identity.Validate() != nil {
		return collectnative.Evidence{}, "", collectnative.ErrIdentity
	}
	tx, err := a.store.db.BeginTx(ctx, nil)
	if err != nil {
		return collectnative.Evidence{}, "", err
	}
	defer func() { _ = tx.Rollback() }()
	epoch, err := a.owned(ctx, tx, identity, false)
	if err != nil {
		return collectnative.Evidence{}, "", err
	}
	var content []byte
	var digest string
	if err := tx.QueryRowContext(ctx, `SELECT content,digest FROM cao_acquisition_attempts
		WHERE namespace=$1 AND repository=$2 AND run_id=$3 AND attempt=$4 AND epoch=$5
		AND digest IS DISTINCT FROM consumed_digest AND octet_length(content)<=$6`,
		a.store.namespace, identity.Repository, identity.RunID, identity.Attempt, epoch, acquisitionMaxBytes).
		Scan(&content, &digest); err != nil {
		return collectnative.Evidence{}, "", err
	}
	sum := sha256.Sum256(content)
	if hex.EncodeToString(sum[:]) != digest {
		return collectnative.Evidence{}, "", errors.New("native acquisition publication digest mismatch")
	}
	var evidence collectnative.Evidence
	if err := json.Unmarshal(content, &evidence); err != nil || evidence.Identity != identity || evidence.Version != 1 {
		return evidence, "", errors.New("invalid native acquisition publication")
	}
	return evidence, digest, tx.Commit()
}
