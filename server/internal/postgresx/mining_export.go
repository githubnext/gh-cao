package postgresx

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/sessionexport"
)

func (s *Store) ReadMining(ctx context.Context, repository, runID string, attempt int64) (snapshot sessionexport.MiningSnapshot, err error) {
	number, parseErr := strconv.ParseInt(runID, 10, 64)
	if parseErr != nil || number <= 0 || strconv.FormatInt(number, 10) != runID || attempt < 1 {
		return snapshot, errors.New("mining export requires an exact run attempt")
	}
	err = s.WithReadTransaction(ctx, func(ctx context.Context, reader NativeReader) error {
		r := reader.(*readTransaction)
		snapshot.Namespace = s.namespace
		err := r.tx.QueryRowContext(ctx, "SELECT revision FROM cao_state WHERE namespace=$1", s.namespace).Scan(&snapshot.Revision)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		canonical := "github:run:" + repository + ":" + runID
		runs, err := readExportRows(ctx, r.tx, s.namespace, "$runs", []string{"id", "attempt", "repositoryId"},
			"id=$2 AND repository_id=$3 AND attempt=$4 LIMIT 1",
			canonical, "repository:"+model.EncodeCoordinate(repository), attempt)
		if err != nil || len(runs) == 0 {
			return err
		}
		snapshot.Run = runs[0]
		snapshot.Audits, err = readExportRows(ctx, r.tx, s.namespace, "$audits",
			[]string{"id", "runId", "source", "type", "correlationId", "claimRunAttempt", "claimRunId", "name", "code",
				"targetRepository", "targetWorkflowPath", "status", "decision", "scope", "evidenceState", "observedAt", "timestamp", "summary", "payloadRef"},
			fmt.Sprintf("run_id=$2 AND source='server-mining' AND type IN ('episode.membership','episode.root','outcome.observed') ORDER BY id LIMIT %d", sessionexport.MaxRows+1),
			canonical)
		if err != nil {
			return err
		}
		snapshot.Outputs, err = readExportRows(ctx, r.tx, s.namespace, "$issues",
			[]string{"id", "runId", "safeOutputType", "safeOutputUrl", "correlationId", "isPullRequest", "githubEntityType"},
			fmt.Sprintf("run_id=$2 AND safe_output_type IN ('create_issue','create_pull_request') ORDER BY id LIMIT %d", sessionexport.MaxRows+1), canonical)
		if err != nil {
			return err
		}
		if len(snapshot.Audits)+len(snapshot.Outputs) > sessionexport.MaxRows {
			return sessionexport.ErrTooLarge
		}
		content, err := json.Marshal(snapshot)
		if err != nil {
			return err
		}
		if len(content) > sessionexport.MaxBytes {
			return sessionexport.ErrTooLarge
		}
		return nil
	})
	return snapshot, err
}
