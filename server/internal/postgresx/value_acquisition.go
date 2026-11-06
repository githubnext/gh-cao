package postgresx

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/githubnext/gh-cao/server/internal/model"
)

const (
	ValueProofMaxFiles = 2048
	ValueProofMaxBytes = 64 << 20
	ValueProofFileMax  = 16 << 20
)

var valueProofDigest = regexp.MustCompile(`^[a-f0-9]{64}$`)

type ValueProofFile struct {
	Path    string
	Digest  string
	Content []byte
}

type ValueCollectionCost struct {
	At                        time.Time
	Installation              int64
	Repository                string
	Workflow                  string
	Collector                 string
	APIRequests               int
	CoreRequests              int
	SearchRequests            int
	GraphQLRequests           int
	CacheHits                 int
	PreflightRequests         int
	GraphQLPointsObserved     int
	GraphQLRequestsUnverified int
	WallMS                    int64
	OutputBytes               int64
	TransportBytes            int64
	Succeeded                 bool
	FailureReason             string
}

func validateValueProofFiles(files []ValueProofFile) ([]ValueProofFile, error) {
	if len(files) > ValueProofMaxFiles {
		return nil, errors.New("value proof file count exceeds its budget")
	}
	result := slices.Clone(files)
	var total int
	seen := map[string]bool{}
	for index := range result {
		file := &result[index]
		if file.Path == "" || len(file.Path) > 512 || file.Path != path.Clean(file.Path) ||
			path.IsAbs(file.Path) || file.Path == "." || strings.HasPrefix(file.Path, "../") ||
			strings.Contains(file.Path, `\`) || seen[file.Path] {
			return nil, errors.New("value proof contains an unsafe or duplicate path")
		}
		if len(file.Content) > ValueProofFileMax {
			return nil, errors.New("value proof file exceeds its byte budget")
		}
		total += len(file.Content)
		if total > ValueProofMaxBytes {
			return nil, errors.New("value proof exceeds its byte budget")
		}
		digest := fmt.Sprintf("%x", sha256.Sum256(file.Content))
		if file.Digest == "" {
			file.Digest = digest
		}
		if !valueProofDigest.MatchString(file.Digest) || file.Digest != digest {
			return nil, errors.New("value proof file digest is invalid")
		}
		file.Content = slices.Clone(file.Content)
		seen[file.Path] = true
	}
	slices.SortFunc(result, func(left, right ValueProofFile) int {
		return strings.Compare(left.Path, right.Path)
	})
	return result, nil
}

func (s *Store) ReadValueProof(ctx context.Context, repository, workflow string) ([]ValueProofFile, error) {
	if len(repository) > 256 || len(workflow) > 256 ||
		!targetCoordinate.MatchString(repository) || !evidenceSlug.MatchString(workflow) {
		return nil, ErrValueInvalid
	}
	rows, err := s.db.QueryContext(ctx, `SELECT path,digest,content FROM cao_value_proof_files
		WHERE namespace=$1 AND repository=$2 AND workflow=$3 ORDER BY path LIMIT $4`,
		s.namespace, strings.ToLower(repository), workflow, ValueProofMaxFiles+1)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	files := []ValueProofFile{}
	for rows.Next() {
		var file ValueProofFile
		if err := rows.Scan(&file.Path, &file.Digest, &file.Content); err != nil {
			return nil, err
		}
		files = append(files, file)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return validateValueProofFiles(files)
}

func (s *Store) replaceValueProof(
	ctx context.Context, tx pgx.Tx, repository, workflow string, files []ValueProofFile, updatedAt time.Time,
) error {
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`,
		"value-proof:"+s.namespace+":"+repository+":"+workflow); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM cao_value_proof_files
		WHERE namespace=$1 AND repository=$2 AND workflow=$3`, s.namespace, repository, workflow); err != nil {
		return err
	}
	for _, file := range files {
		if _, err := tx.Exec(ctx, `INSERT INTO cao_value_proof_files
			(namespace,repository,workflow,path,digest,content,updated_at)
			VALUES($1,$2,$3,$4,$5,$6,$7)`,
			s.namespace, repository, workflow, file.Path, file.Digest, file.Content, updatedAt.UTC()); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) DeleteValueProofs(ctx context.Context, repository string) error {
	if len(repository) > 256 || !targetCoordinate.MatchString(repository) {
		return ErrValueInvalid
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM cao_value_proof_files WHERE namespace=$1 AND repository=$2`,
		s.namespace, strings.ToLower(repository))
	return err
}

func validateValueCollectionCost(record ValueCollectionCost) error {
	if record.At.IsZero() || record.Installation <= 0 ||
		len(record.Repository) > 256 || len(record.Workflow) > 256 ||
		!targetCoordinate.MatchString(record.Repository) || !evidenceSlug.MatchString(record.Workflow) ||
		record.Collector == "" || len(record.Collector) > 512 ||
		record.APIRequests < 0 || record.CoreRequests < 0 || record.SearchRequests < 0 ||
		record.GraphQLRequests < 0 ||
		record.APIRequests != record.CoreRequests+record.SearchRequests+record.GraphQLRequests ||
		record.CacheHits < 0 || record.PreflightRequests < 0 || record.PreflightRequests > record.CoreRequests ||
		record.GraphQLPointsObserved < 0 || record.GraphQLRequestsUnverified < 0 ||
		record.GraphQLRequestsUnverified > record.GraphQLRequests ||
		record.WallMS < 0 || record.OutputBytes < 0 || record.TransportBytes < 0 {
		return errors.New("invalid Value collection cost")
	}
	if record.Succeeded == (record.FailureReason != "") {
		return errors.New("invalid Value collection cost outcome")
	}
	switch record.FailureReason {
	case "", "api-budget", "budget-unavailable", "timeout", "output-budget", "collector-failed":
		return nil
	default:
		return errors.New("invalid Value collection cost failure reason")
	}
}

func (s *Store) AppendValueCollectionCost(ctx context.Context, record ValueCollectionCost) error {
	if err := validateValueCollectionCost(record); err != nil {
		return err
	}
	day := time.Date(record.At.UTC().Year(), record.At.UTC().Month(), record.At.UTC().Day(), 0, 0, 0, 0, time.UTC)
	successes, failures := 0, 1
	if record.Succeeded {
		successes, failures = 1, 0
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	_, err = tx.ExecContext(ctx, `INSERT INTO cao_value_collection_costs
		(namespace,installation_id,repository,workflow,collector,day,attempts,successes,failures,
		api_requests,core_requests,search_requests,graphql_requests,cache_hits,preflight_requests,
		graphql_points_observed,graphql_requests_unverified,
		wall_ms,max_wall_ms,output_bytes,transport_bytes,last_failure_reason)
		VALUES($1,$2,$3,$4,$5,$6,1,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$17,$18,$19,$20)
		ON CONFLICT(namespace,installation_id,repository,workflow,day) DO UPDATE SET
		collector=excluded.collector,attempts=cao_value_collection_costs.attempts+1,
		successes=cao_value_collection_costs.successes+excluded.successes,
		failures=cao_value_collection_costs.failures+excluded.failures,
		api_requests=cao_value_collection_costs.api_requests+excluded.api_requests,
		core_requests=cao_value_collection_costs.core_requests+excluded.core_requests,
		search_requests=cao_value_collection_costs.search_requests+excluded.search_requests,
		graphql_requests=cao_value_collection_costs.graphql_requests+excluded.graphql_requests,
		cache_hits=cao_value_collection_costs.cache_hits+excluded.cache_hits,
		preflight_requests=cao_value_collection_costs.preflight_requests+excluded.preflight_requests,
		graphql_points_observed=cao_value_collection_costs.graphql_points_observed+excluded.graphql_points_observed,
		graphql_requests_unverified=cao_value_collection_costs.graphql_requests_unverified+excluded.graphql_requests_unverified,
		wall_ms=cao_value_collection_costs.wall_ms+excluded.wall_ms,
		max_wall_ms=greatest(cao_value_collection_costs.max_wall_ms,excluded.max_wall_ms),
		output_bytes=cao_value_collection_costs.output_bytes+excluded.output_bytes,
		transport_bytes=cao_value_collection_costs.transport_bytes+excluded.transport_bytes,
		last_failure_reason=coalesce(excluded.last_failure_reason,cao_value_collection_costs.last_failure_reason)`,
		s.namespace, record.Installation, strings.ToLower(record.Repository), record.Workflow, record.Collector, day,
		successes, failures, record.APIRequests, record.CoreRequests, record.SearchRequests, record.GraphQLRequests,
		record.CacheHits, record.PreflightRequests,
		record.GraphQLPointsObserved, record.GraphQLRequestsUnverified, record.WallMS,
		record.OutputBytes, record.TransportBytes, nullableText(record.FailureReason))
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM cao_value_collection_costs
		WHERE namespace=$1 AND day < $2`, s.namespace, day.AddDate(0, 0, -90)); err != nil {
		return err
	}
	return tx.Commit()
}

func nullableText(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func (s *Store) ValueCollectionCostSource(ctx context.Context, since time.Time, limit int) (model.Source, error) {
	if since.IsZero() {
		since = time.Now().UTC().AddDate(0, 0, -30)
	}
	if limit <= 0 || limit > 100000 {
		limit = 100000
	}
	rows, err := s.db.QueryContext(ctx, `SELECT day,workflow,
		COUNT(DISTINCT collector),COUNT(DISTINCT installation_id),COUNT(DISTINCT repository),
		SUM(attempts),SUM(successes),SUM(failures),SUM(api_requests),SUM(core_requests),
		SUM(search_requests),SUM(graphql_requests),SUM(cache_hits),SUM(preflight_requests),
		SUM(graphql_points_observed),SUM(graphql_requests_unverified),SUM(wall_ms),MAX(max_wall_ms),
		SUM(output_bytes),SUM(transport_bytes),last_failure_reason
		FROM cao_value_collection_costs WHERE namespace=$1 AND day >= $2
		GROUP BY day,workflow,last_failure_reason
		ORDER BY day DESC,workflow,last_failure_reason NULLS FIRST LIMIT $3`,
		s.namespace, dayStart(since), limit+1)
	if err != nil {
		return model.Source{}, err
	}
	defer func() { _ = rows.Close() }()
	result := model.Source{Source: "value-collection-costs", Rows: []model.Row{}, Metadata: model.Metadata{
		"source-id": "value-collection-costs", "availability": "available",
		"completeness": "complete", "freshness": "current",
	}}
	for rows.Next() {
		var day time.Time
		var row struct {
			Collectors, Installations, Repositories, Attempts, Successes, Failures, APIRequests int64
			CoreRequests, SearchRequests, GraphQLRequests, CacheHits, Preflights                int64
			GraphPoints, GraphUnknown, WallMS, MaxWallMS, OutputBytes, TransportBytes           int64
			Workflow                                                                            string
			FailureReason                                                                       *string
		}
		if err := rows.Scan(&day, &row.Workflow, &row.Collectors, &row.Installations, &row.Repositories,
			&row.Attempts, &row.Successes, &row.Failures, &row.APIRequests, &row.CoreRequests,
			&row.SearchRequests, &row.GraphQLRequests, &row.CacheHits, &row.Preflights,
			&row.GraphPoints, &row.GraphUnknown, &row.WallMS, &row.MaxWallMS, &row.OutputBytes,
			&row.TransportBytes, &row.FailureReason); err != nil {
			return model.Source{}, err
		}
		result.Rows = append(result.Rows, model.Row{
			"day": day.UTC().Format(time.RFC3339), "workflow": row.Workflow,
			"collectors": row.Collectors, "installations": row.Installations, "repositories": row.Repositories,
			"attempts": row.Attempts, "successes": row.Successes, "failures": row.Failures,
			"api-requests": row.APIRequests, "core-requests": row.CoreRequests,
			"search-requests": row.SearchRequests, "graphql-requests": row.GraphQLRequests,
			"cache-hits":         row.CacheHits,
			"preflight-requests": row.Preflights, "graphql-points-observed": row.GraphPoints,
			"graphql-requests-unverified": row.GraphUnknown, "wall-ms": row.WallMS,
			"max-wall-ms": row.MaxWallMS, "output-bytes": row.OutputBytes,
			"transport-bytes": row.TransportBytes, "last-failure-reason": optionalText(row.FailureReason),
		})
		if len(result.Rows) > limit {
			return model.Source{}, errors.New("value collection cost source exceeds its row budget")
		}
	}
	return result, rows.Err()
}

func optionalText(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}
