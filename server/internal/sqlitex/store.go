// Package sqlitex serves existing canonical SQLite snapshots locally. It never
// provisions, repairs, or writes a snapshot and is not a hosted storage adapter.
package sqlitex

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/githubnext/gh-cao/internal/dashboardquery"

	"github.com/githubnext/gh-cao/server/internal/dashboardstore"
	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
)

type Store struct {
	db               *sql.DB
	dataVersion      int64
	state            dashboardstore.State
	metadata         map[string]model.Metadata
	conn             *sql.Conn
	readGate         chan struct{}
	snapshotRevision string
	overlayPath      string
}

type reader struct {
	store *Store
	tx    *sql.Tx
}

var _ dashboardstore.Store = (*Store)(nil)
var _ dashboardstore.Reader = (*reader)(nil)

func Open(ctx context.Context, path string) (*Store, error) {
	return OpenWithMetadata(ctx, path, nil)
}

// OpenWithMetadata retains explicitly supplied published inventory quality.
// Snapshots without independent quality evidence keep unknown quality.
func OpenWithMetadata(ctx context.Context, path string, metadata map[string]model.Metadata) (*Store, error) {
	if strings.TrimSpace(path) == "" || strings.ContainsRune(path, 0) {
		return nil, errors.New("SQLite requires an existing canonical snapshot file")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(absolute) // #nosec G703 -- operator-selected local snapshot, intentionally not confined to a directory.
	if err != nil {
		return nil, fmt.Errorf("open canonical SQLite snapshot: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("canonical SQLite snapshot must be a regular file")
	}
	db, err := sql.Open("sqlite", snapshotDSN(absolute))
	if err != nil {
		return nil, err
	}
	// State's data_version cache belongs to this one SQLite connection.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	store := &Store{db: db, metadata: metadata}
	if err := store.WithReadTransaction(ctx, func(ctx context.Context, r dashboardstore.Reader) error {
		_, err := r.State(ctx)
		return err
	}); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("read canonical SQLite snapshot (download or rebuild it before serving): %w", err)
	}
	return store, nil
}

func snapshotDSN(absolute string) string {
	// A drive letter must be in the path (file:///C:/...), not the URI authority.
	location := &url.URL{Scheme: "file", Path: "/" + strings.TrimPrefix(filepath.ToSlash(absolute), "/")}
	parameters := url.Values{"mode": {"ro"}, "_pragma": {
		"busy_timeout(5000)", "query_only(1)", "trusted_schema(0)", "mmap_size(1073741824)",
	}}
	location.RawQuery = parameters.Encode()
	return location.String()
}

func storeVersion(ctx context.Context, tx *sql.Tx) error {
	var version int
	if err := tx.QueryRowContext(ctx, `SELECT version FROM __idb_databases WHERE name=?`, dashboardquery.DatabaseName).Scan(&version); err != nil {
		return err
	}
	if version < 44 || version > dashboardquery.DatabaseVersion {
		return fmt.Errorf("unsupported canonical SQLite database version %d; expected %d", version, dashboardquery.DatabaseVersion)
	}
	return nil
}

func (s *Store) Open() bool { return s != nil && s.db != nil }
func (s *Store) Close() error {
	var err error
	if s.conn != nil {
		err = s.conn.Close()
	}
	return errors.Join(err, s.db.Close())
}

func (s *Store) WithReadTransaction(ctx context.Context, fn func(context.Context, dashboardstore.Reader) error) error {
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if s.readGate != nil {
		select {
		case s.readGate <- struct{}{}:
			defer func() { <-s.readGate }()
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	begin := s.db.BeginTx
	if s.conn != nil {
		begin = s.conn.BeginTx
	}
	tx, err := begin(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return fmt.Errorf("begin SQLite snapshot read: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := fn(ctx, &reader{store: s, tx: tx}); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) State(ctx context.Context) (state dashboardstore.State, err error) {
	err = s.WithReadTransaction(ctx, func(ctx context.Context, r dashboardstore.Reader) error {
		state, err = r.State(ctx)
		return err
	})
	return state, err
}

func (r *reader) State(ctx context.Context) (dashboardstore.State, error) {
	var version int64
	if err := r.tx.QueryRowContext(ctx, "PRAGMA data_version").Scan(&version); err != nil {
		return dashboardstore.State{}, err
	}
	if r.store.dataVersion == version {
		return cloneState(r.store.state), nil
	}
	if err := storeVersion(ctx, r.tx); err != nil {
		return dashboardstore.State{}, err
	}
	state := dashboardstore.State{Counts: map[string]int{}, EvaluatedAt: time.Unix(0, 0).UTC()}
	dailyStores := map[string]bool{}
	hash := sha256.New()
	_, _ = fmt.Fprintf(hash, "schema:%d:", dashboardquery.DatabaseVersion)
	rows, err := r.tx.QueryContext(ctx, `SELECT name FROM __idb_stores WHERE database_name=? ORDER BY name`, dashboardquery.DatabaseName)
	if err != nil {
		return state, err
	}
	if err := func() error {
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				return err
			}
			if canonicalCollection(name) {
				state.Counts["$"+name] = 0
				_, _ = fmt.Fprintf(hash, "%d:%s", len(name), name)
			} else if dailyCollection(name) {
				dailyStores[name] = true
				_, _ = fmt.Fprintf(hash, "%d:%s", len(name), name)
				if name == "dailyRunCounts" {
					state.Counts["daily-run-counts"] = 0
				}
			}
		}
		return rows.Err()
	}(); err != nil {
		return state, err
	}
	if len(state.Counts) == 0 {
		return state, errors.New("SQLite snapshot has no canonical object stores")
	}
	rows, err = r.tx.QueryContext(ctx, `SELECT store_name,record_key,value,
		coalesce(json_extract(value,'$.createdAt'),json_extract(value,'$.completedAt'),
			json_extract(value,'$.observedAt'),json_extract(value,'$.startedAt'),'')
		FROM __idb_records WHERE database_name=? ORDER BY store_name,record_key`, dashboardquery.DatabaseName)
	if err != nil {
		return state, err
	}
	if err := func() error {
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var name, key, value, observed string
			if err := rows.Scan(&name, &key, &value, &observed); err != nil {
				return err
			}
			if dailyCollection(name) {
				if !dailyStores[name] {
					return errors.New("SQLite daily records refer to an undeclared object store")
				}
				for _, part := range []string{name, key, value} {
					_, _ = fmt.Fprintf(hash, "%d:%s", len(part), part)
				}
				if name == "dailyRunCounts" {
					state.Counts["daily-run-counts"]++
					state.Ready = true
				}
				continue
			}
			if !canonicalCollection(name) {
				continue
			}
			if _, declared := state.Counts["$"+name]; !declared {
				return errors.New("SQLite canonical records refer to an undeclared object store")
			}
			for _, part := range []string{name, key, value} {
				_, _ = fmt.Fprintf(hash, "%d:%s", len(part), part)
			}
			state.Counts["$"+name]++
			state.Ready = state.Ready || name != "transactions"
			if at, err := time.Parse(time.RFC3339Nano, observed); err == nil && at.After(state.EvaluatedAt) {
				state.EvaluatedAt = at.UTC()
			}
		}
		return rows.Err()
	}(); err != nil {
		return state, err
	}
	digest := hash.Sum(nil)
	state.DataRevision = "sha256:" + hex.EncodeToString(digest)
	// Revision crosses the JSON boundary, so keep it exactly representable.
	state.Revision = int64(binary.BigEndian.Uint64(digest[:8]) & ((1 << 53) - 1))
	if state.Revision == 0 {
		state.Revision = 1
	}
	r.store.dataVersion, r.store.state = version, state
	return cloneState(state), nil
}

func canonicalCollection(name string) bool {
	return name == "transactions" || name == "marketplacePackages" || postgresx.IsCanonicalCollection(name)
}

func dailyCollection(name string) bool {
	return name == "dailyRunCounts" || name == "runCountContributions" || name == "dailyRunCountState"
}

func cloneState(state dashboardstore.State) dashboardstore.State {
	counts := make(map[string]int, len(state.Counts))
	for name, count := range state.Counts {
		counts[name] = count
	}
	state.Counts = counts
	return state
}

func (s *Store) Diagnostics(ctx context.Context) (diagnostics model.Diagnostics, err error) {
	err = s.WithReadTransaction(ctx, func(ctx context.Context, r dashboardstore.Reader) error {
		diagnostics, err = r.Diagnostics(ctx)
		return err
	})
	return diagnostics, err
}
