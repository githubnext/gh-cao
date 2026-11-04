package postgresx

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"regexp"

	"github.com/githubnext/gh-cao/server/internal/adxtrace"
	"github.com/githubnext/gh-cao/server/internal/collectnative"
	"github.com/githubnext/gh-cao/server/internal/model"
)

type traceStorage struct {
	store       *Store
	destination string
	maxBytes    int64
	maxEntries  int
}

type traceObjects struct {
	traceStorage
	ctx context.Context
	tx  *sql.Tx
}

var traceObjectKey = regexp.MustCompile(`^(ledger[.]json|[a-f0-9]{64}[.](trace|dataset|mining))$`)

// TraceStorage stores only policy-approved projections and their delivery ledger.
// The destination identity separates incompatible policies/sampling contracts.
// It is not a cache, an Activity lake, or a canonical dashboard data source.
func (s *Store) TraceStorage(destination string, maxBytes int64, maxEntries int) (adxtrace.Storage, error) {
	if destination == "" || len(destination) > 512 || maxBytes < 2048 || maxBytes > 1<<40 ||
		maxEntries < 1 || maxEntries > 100000 {
		return nil, errors.New("invalid shared trace storage identity or budget")
	}
	return traceStorage{s, destination, maxBytes, maxEntries}, nil
}

func (s traceStorage) Transaction(ctx context.Context, action func(adxtrace.Objects) error) error {
	if action == nil {
		return errors.New("trace transaction requires an action")
	}
	if mutation, ok := ctx.Value(enrollmentTraceTransactionKey{}).(*enrollmentMutation); ok {
		if mutation.store != s.store {
			return collectnative.ErrUnsupported
		}
		return s.transact(ctx, mutation.tx, action)
	}
	tx, err := s.store.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err := s.transact(ctx, tx, action); err != nil {
		return err
	}
	return tx.Commit()
}

func (s traceStorage) transact(ctx context.Context, tx *sql.Tx, action func(adxtrace.Objects) error) error {
	var locked bool
	if err := tx.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock(
		hashtextextended(current_schema() || ':trace:' || $1 || ':' || $2,0))`,
		s.store.namespace, s.destination).Scan(&locked); err != nil {
		return err
	}
	if !locked {
		return adxtrace.ErrBusy
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO cao_state(namespace,revision,data_revision,evaluated_at,schema_version)
		VALUES($1,0,'','epoch',$2) ON CONFLICT(namespace) DO NOTHING`, s.store.namespace, model.SchemaVersion); err != nil {
		return err
	}
	return action(traceObjects{s, ctx, tx})
}

func (o traceObjects) Read(key string, limit int64) ([]byte, error) {
	if !traceObjectKey.MatchString(key) || limit < 0 || limit > 128<<20 {
		return nil, errors.New("invalid trace object read")
	}
	var size int64
	err := o.tx.QueryRowContext(o.ctx, `SELECT octet_length(content) FROM cao_trace_objects
		WHERE namespace=$1 AND destination=$2 AND key=$3`,
		o.store.namespace, o.destination, key).Scan(&size)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fs.ErrNotExist
	}
	if err != nil {
		return nil, err
	}
	if size > limit {
		return nil, adxtrace.ErrCapacity
	}
	var content []byte
	err = o.tx.QueryRowContext(o.ctx, `SELECT content FROM cao_trace_objects
		WHERE namespace=$1 AND destination=$2 AND key=$3`,
		o.store.namespace, o.destination, key).Scan(&content)
	return content, err
}

func (o traceObjects) Write(key string, content []byte) error {
	if !traceObjectKey.MatchString(key) {
		return errors.New("invalid trace object key")
	}
	if len(content) > 128<<20 {
		return adxtrace.ErrCapacity
	}
	var bytes int64
	var count int
	if err := o.tx.QueryRowContext(o.ctx, `SELECT coalesce(sum(octet_length(content)),0),count(*)
		FROM cao_trace_objects WHERE namespace=$1 AND destination=$2 AND key<>$3`,
		o.store.namespace, o.destination, key).Scan(&bytes, &count); err != nil {
		return err
	}
	// Each admitted record may own trace, mining and dataset objects in addition
	// to the ledger. The ledger independently enforces the logical entry bound.
	if bytes+int64(len(content)) > o.maxBytes || count >= 3*o.maxEntries+1 {
		return adxtrace.ErrCapacity
	}
	_, err := o.tx.ExecContext(o.ctx, `INSERT INTO cao_trace_objects(namespace,destination,key,content)
		VALUES($1,$2,$3,$4) ON CONFLICT(namespace,destination,key) DO UPDATE SET content=excluded.content`,
		o.store.namespace, o.destination, key, content)
	return err
}

func (o traceObjects) Remove(key string) error {
	if !traceObjectKey.MatchString(key) {
		return errors.New("invalid trace object key")
	}
	result, err := o.tx.ExecContext(o.ctx, `DELETE FROM cao_trace_objects WHERE namespace=$1 AND destination=$2 AND key=$3`,
		o.store.namespace, o.destination, key)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err == nil && n == 0 {
		return fs.ErrNotExist
	}
	return err
}

func (o traceObjects) List() ([]adxtrace.ObjectInfo, error) {
	rows, err := o.tx.QueryContext(o.ctx, `SELECT key,octet_length(content) FROM cao_trace_objects
		WHERE namespace=$1 AND destination=$2 ORDER BY key LIMIT $3`,
		o.store.namespace, o.destination, 3*o.maxEntries+2)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var result []adxtrace.ObjectInfo
	var bytes int64
	for rows.Next() {
		var info adxtrace.ObjectInfo
		if err := rows.Scan(&info.Key, &info.Bytes); err != nil {
			return nil, err
		}
		if !traceObjectKey.MatchString(info.Key) {
			return nil, errors.New("invalid stored trace object")
		}
		result = append(result, info)
		bytes += info.Bytes
		if len(result) > 3*o.maxEntries+1 || bytes > o.maxBytes {
			return nil, adxtrace.ErrCapacity
		}
	}
	return result, rows.Err()
}
