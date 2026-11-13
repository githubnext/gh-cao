package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strconv"
	"time"

	"github.com/githubnext/gh-cao/server/internal/model"
	"github.com/githubnext/gh-cao/server/internal/operational"
	"github.com/githubnext/gh-cao/server/internal/query"
	"github.com/githubnext/gh-cao/server/internal/telemetry"
)

const queryCacheTimeout = 2 * time.Second

var errValueRevisionUnavailable = errors.New("value evidence revision is unavailable")

// Zero values select bounded production defaults; Disabled bypasses the cache.
type QueryCacheConfig struct {
	Disabled       bool
	MaxResultBytes int64
	MaxBytes       int64
	MinDuration    time.Duration
}

func (config QueryCacheConfig) resolve() (QueryCacheConfig, error) {
	if config.MaxResultBytes < 0 || config.MaxBytes < 0 || config.MinDuration < 0 {
		return config, errors.New("query cache limits and minimum duration cannot be negative")
	}
	if config.MaxResultBytes == 0 {
		config.MaxResultBytes = 1 << 20
	}
	if config.MaxBytes == 0 {
		config.MaxBytes = 64 << 20
	}
	if config.MinDuration == 0 {
		config.MinDuration = 100 * time.Millisecond
	}
	if config.MaxResultBytes > config.MaxBytes {
		return config, errors.New("query cache result limit cannot exceed its total memory budget")
	}
	return config, nil
}

func queryCacheConfigFromEnv(config QueryCacheConfig) (QueryCacheConfig, error) {
	if !config.Disabled {
		if value, present := os.LookupEnv("CAO_QUERY_CACHE_DISABLED"); present {
			disabled, err := strconv.ParseBool(value)
			if err != nil {
				return config, errors.New("CAO_QUERY_CACHE_DISABLED must be a boolean")
			}
			config.Disabled = disabled
		}
	}
	for name, target := range map[string]*int64{
		"CAO_QUERY_CACHE_MAX_RESULT_BYTES": &config.MaxResultBytes,
		"CAO_QUERY_CACHE_MAX_BYTES":        &config.MaxBytes,
	} {
		if *target != 0 {
			continue
		}
		if value, present := os.LookupEnv(name); present {
			number, err := strconv.ParseInt(value, 10, 64)
			if err != nil || number <= 0 {
				return config, fmt.Errorf("%s must be a positive byte count", name)
			}
			*target = number
		}
	}
	return config.resolve()
}

func (a *App) queryCacheIdentity(input queryRequest, admin bool) (string, error) {
	// Client evaluation timestamps are ignored by execution. Including them
	// would make ingestion-driven browser refreshes invalidate the cache.
	input.EvaluatedAt = ""
	identity := struct {
		Version         int                `json:"version"`
		Admin           bool               `json:"admin"`
		DatabaseQueries []query.Definition `json:"databaseQueries"`
		Dashboard       []query.Definition `json:"dashboard"`
		Request         queryRequest       `json:"request"`
	}{model.SchemaVersion, admin, a.databaseQueries, a.config.DashboardQueries, input}
	hash := sha256.New()
	if err := json.NewEncoder(hash).Encode(identity); err != nil {
		return "", fmt.Errorf("encode query cache identity: %w", err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func (a *App) queryResultCacheIdentity(ctx context.Context, input queryRequest, admin bool) (string, error) {
	key, err := a.queryCacheIdentity(input, admin)
	if err != nil {
		return "", err
	}
	definitions, requested := a.queryPlan(input)
	dependencies, err := query.Dependencies(definitions, requested)
	if err != nil {
		return "", err
	}
	valueSources := []string{"$operationalValueEvidence", "$operationalValues"}
	readsValue := false
	for _, source := range valueSources {
		readsValue = readsValue || slices.Contains(dependencies, source)
	}
	for _, definition := range definitions {
		if !slices.Contains(dependencies, definition.Name) {
			continue
		}
		for _, source := range valueSources {
			readsValue = readsValue || definition.From == source || slices.Contains(definition.Union, source)
			for _, join := range definition.Joins {
				readsValue = readsValue || join.Source == source
			}
		}
	}
	if !readsValue {
		return key, nil
	}
	// Value publication and collector withdrawal must be visible after the
	// revision event, not after the ordinary five-minute cache lifetime.
	active, err := a.database.State(ctx)
	if err != nil {
		return "", errValueRevisionUnavailable
	}
	hash := sha256.Sum256([]byte(key + ":" + strconv.FormatInt(active.Revision, 10)))
	return hex.EncodeToString(hash[:]), nil
}

func (a *App) loadCachedQuery(ctx context.Context, key string, config QueryCacheConfig) (response queryResponse, hit bool, err error) {
	instruments, err := a.cacheTelemetry()
	if err != nil {
		return queryResponse{}, false, err
	}
	ctx, span := telemetry.Tracer().Start(ctx, telemetry.SpanQueryCacheLookup)
	defer span.End()
	started := time.Now()
	var data []byte
	var stats operational.QueryCacheStats
	var observedStats *operational.QueryCacheStats
	defer func() {
		outcome := "miss"
		if err != nil {
			outcome = "error"
		} else if hit {
			outcome = "hit"
		}
		instruments.observe(ctx, span, "lookup", outcome, started, len(data), observedStats)
	}()
	ctx, cancel := context.WithTimeout(ctx, queryCacheTimeout)
	defer cancel()
	data, stats, err = a.services.Cache.CachedQueryResult(ctx, key, config.MaxResultBytes, config.MaxBytes)
	if err == nil {
		observedStats = &stats
	}
	if err != nil || data == nil {
		return queryResponse{}, false, err
	}
	var entry cachedQueryResult
	decoder := json.NewDecoder(bytes.NewReader(data))
	// Preserve exact numeric lexemes, including large identifiers, across hits.
	decoder.UseNumber()
	if err := decoder.Decode(&entry); err != nil {
		return queryResponse{}, false, errors.New("invalid cached query result")
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return queryResponse{}, false, errors.New("invalid cached query result")
	}
	if entry.ETag != key {
		return queryResponse{}, false, errors.New("cached query ETag does not match its identity")
	}
	result := entry.Result
	if result.Sources == nil || result.EvaluatedAt == "" {
		return queryResponse{}, false, errors.New("incomplete cached query result")
	}
	// A cache hit does no plan work and must not charge the original execution's
	// rate-limit cost or report its duration as a new database execution.
	result.Metrics = model.Metrics{
		OutputRows: result.Metrics.OutputRows,
		PushedDown: []string{}, FallbackOperations: []string{},
	}
	return result, true, nil
}

type cachedQueryResult struct {
	ETag   string        `json:"etag"`
	Result queryResponse `json:"result"`
}

var errQueryCacheTooLarge = errors.New("query result is too large to cache")

type boundedQueryBuffer struct {
	bytes.Buffer
	limit int64
}

func (buffer *boundedQueryBuffer) Write(data []byte) (int, error) {
	if int64(buffer.Len())+int64(len(data)) > buffer.limit {
		return 0, errQueryCacheTooLarge
	}
	return buffer.Buffer.Write(data)
}

func (a *App) storeCachedQuery(ctx context.Context, key string, result queryResponse, config QueryCacheConfig) (err error) {
	instruments, err := a.cacheTelemetry()
	if err != nil {
		return err
	}
	ctx, span := telemetry.Tracer().Start(ctx, telemetry.SpanQueryCacheStore)
	defer span.End()
	started := time.Now()
	outcome := "result-size"
	var payloadBytes int
	var observedStats *operational.QueryCacheStats
	defer func() {
		if err != nil {
			outcome = "error"
		}
		instruments.observe(ctx, span, "admission", outcome, started, payloadBytes, observedStats)
	}()
	data, compact, err := encodeCompactQuery(key, result, config.MaxResultBytes)
	if err != nil {
		return err
	}
	if !compact {
		return nil
	}
	payloadBytes = len(data)
	ctx, cancel := context.WithTimeout(ctx, queryCacheTimeout)
	defer cancel()
	stored, stats, err := a.services.Cache.CacheQueryResult(ctx, key, data, config.MaxResultBytes, config.MaxBytes)
	if err == nil {
		observedStats = &stats
		outcome = "capacity"
		if stored {
			outcome = "stored"
		}
	}
	return err
}

func encodeCompactQuery(etag string, result queryResponse, limit int64) ([]byte, bool, error) {
	// Reject bulky output before encoding/json allocates a complete document.
	// This is output-only accounting, not the expensive plan's input footprint.
	var outputBytes int64
	for _, source := range result.Sources {
		outputBytes += query.EstimateRowsBytes(source.Rows)
		outputBytes += query.EstimateRowsBytes([]model.Row{model.Row(source.Metadata)})
		outputBytes += int64(len(source.Source) + len(source.ContinuationToken))
		if outputBytes > limit {
			return nil, false, nil
		}
	}
	buffer := boundedQueryBuffer{limit: limit}
	if err := json.NewEncoder(&buffer).Encode(cachedQueryResult{ETag: etag, Result: result}); err != nil {
		if errors.Is(err, errQueryCacheTooLarge) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("encode cached query result: %w", err)
	}
	return buffer.Bytes(), true, nil
}

func queryCacheError(err error) (queryResponse, int, error) {
	// Never expose operational storage errors, internal cache keys, or cached content.
	queryCacheLog.Printf("request failed reason=cache-unavailable")
	if errors.Is(err, context.Canceled) {
		return queryResponse{}, http.StatusServiceUnavailable, context.Canceled
	}
	return queryResponse{}, http.StatusServiceUnavailable, errors.New("query result cache is unavailable")
}
