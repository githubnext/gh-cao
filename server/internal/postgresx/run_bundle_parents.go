package postgresx

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/githubnext/gh-cao/server/internal/query"
)

func (w *Writer) stageBundleParentVersions(ctx context.Context, input RunBundlePublication) error {
	var records [][]any
	for name, rows := range input.Parents {
		for _, row := range rows {
			content, err := json.Marshal(row)
			if err != nil {
				return err
			}
			digest := sha256.Sum256(content)
			records = append(records, []any{"$" + name, row["id"], input.ObservedAt, hex.EncodeToString(digest[:])})
		}
	}
	if len(records) == 0 {
		return nil
	}
	if _, err := w.tx.Exec(ctx, `CREATE TEMP TABLE run_bundle_parents(
		collection text,id text,observed_at timestamptz NOT NULL,digest text NOT NULL,
		PRIMARY KEY(collection,id)) ON COMMIT DROP`); err != nil {
		return err
	}
	_, err := w.tx.CopyFrom(ctx, pgx.Identifier{"run_bundle_parents"},
		[]string{"collection", "id", "observed_at", "digest"}, pgx.CopyFromRows(records))
	return err
}

func (w *Writer) fenceBundleParent(ctx context.Context, source string) error {
	stage := query.SQLIdentifier(entityTables[source].name + "_stage")
	var conflict bool
	if err := w.tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM `+stage+` s
		JOIN run_bundle_parents p ON p.collection=$2 AND p.id=s.id
		JOIN cao_bundle_versions v ON v.namespace=s.namespace AND v.collection=p.collection AND v.id=p.id
		WHERE s.namespace=$1 AND p.observed_at=v.observed_at AND p.digest<>v.digest)`,
		w.store.namespace, source).Scan(&conflict); err != nil {
		return err
	}
	if conflict {
		return errors.New("conflicting shared-parent content at the same accepted source version")
	}
	if _, err := w.tx.Exec(ctx, `DELETE FROM `+stage+` s USING run_bundle_parents p,cao_bundle_versions v
		WHERE s.namespace=$1 AND p.collection=$2 AND p.id=s.id
		AND v.namespace=s.namespace AND v.collection=p.collection AND v.id=p.id
		AND p.observed_at<=v.observed_at`, w.store.namespace, source); err != nil {
		return err
	}
	_, err := w.tx.Exec(ctx, `INSERT INTO cao_bundle_versions(namespace,collection,id,observed_at,digest)
		SELECT s.namespace,p.collection,p.id,p.observed_at,p.digest FROM `+stage+` s
		JOIN run_bundle_parents p ON p.id=s.id AND p.collection=$2 WHERE s.namespace=$1
		ON CONFLICT(namespace,collection,id) DO UPDATE SET observed_at=excluded.observed_at,digest=excluded.digest`,
		w.store.namespace, source)
	return err
}
