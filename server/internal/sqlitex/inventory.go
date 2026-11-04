package sqlitex

import (
	"context"
	"errors"

	"github.com/githubnext/gh-cao/internal/dashboardquery"

	"github.com/githubnext/gh-cao/server/internal/dashboardstore"
)

// SnapshotRepositories bounds optional archive access to the existing inventory.
func (s *Store) SnapshotRepositories(ctx context.Context) ([]string, error) {
	var repositories []string
	err := s.WithReadTransaction(ctx, func(ctx context.Context, r dashboardstore.Reader) error {
		rows, err := r.(*reader).tx.QueryContext(ctx, `
			SELECT json_extract(repo.value,'$.owner') || '/' || json_extract(repo.value,'$.name')
			FROM __idb_records AS repo
			WHERE repo.database_name=? AND repo.store_name='repositories'
			ORDER BY repo.record_key LIMIT 10001`, dashboardquery.DatabaseName)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var repository string
			if err := rows.Scan(&repository); err != nil {
				return err
			}
			repositories = append(repositories, repository)
		}
		if len(repositories) > 10000 {
			return errors.New("local snapshot inventory exceeds the 10000-repository scope limit")
		}
		return rows.Err()
	})
	return repositories, err
}
