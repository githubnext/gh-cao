package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/githubnext/gh-cao/server/internal/ingest"
	debuglogger "github.com/githubnext/gh-cao/server/internal/logger"
	"github.com/githubnext/gh-cao/server/internal/postgresx"
)

var ingestStreamLog = debuglogger.New("cao:dashboard:ingest-stream")

type ingestStreamResponse struct {
	Type   string         `json:"type"`
	Result *ingest.Result `json:"result,omitempty"`
	Modes  []string       `json:"modes,omitempty"`
}

type ingestStreamRequest struct {
	Type string `json:"type"`
	Mode string `json:"mode,omitempty"`
}

func runIngestStream(ctx context.Context, input io.Reader, output io.Writer, run func(context.Context, ingestStreamRequest) (ingest.Result, error)) error {
	encoder := json.NewEncoder(output)
	if err := encoder.Encode(ingestStreamResponse{Type: "ready", Modes: []string{"snapshot", "run-bundles"}}); err != nil {
		return err
	}
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 1024), 8192)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		request, err := parseIngestStreamRequest(scanner.Bytes())
		if err != nil {
			ingestStreamLog.Printf("ingestion stream request rejected")
			return err
		}
		started := time.Now()
		ingestStreamLog.Printf("ingestion stream request started: mode=%q", request.Mode)
		result, err := run(ctx, request)
		var conflict *postgresx.RunBundleReconciliationError
		if request.Mode == "run-bundles" && errors.As(err, &conflict) && ctx.Err() == nil {
			ingestStreamLog.Printf("ingestion stream authoritative reconciliation required: collection=%q delta_duration_ms=%d",
				conflict.Collection, time.Since(started).Milliseconds())
			// The source and namespace are fixed by the process. Re-read and
			// verify its full snapshot; never relax the delta ownership fence.
			result, err = run(ctx, ingestStreamRequest{Type: "ingest", Mode: "snapshot"})
		}
		if err != nil {
			var databaseError *pgconn.PgError
			if errors.As(err, &databaseError) {
				ingestStreamLog.Printf("ingestion stream request failed: sql_state=%q table=%q constraint=%q",
					databaseError.Code, databaseError.TableName, databaseError.ConstraintName)
			} else {
				ingestStreamLog.Printf("ingestion stream request failed: error_type=%T", err)
			}
		}
		ingestStreamLog.Printf("ingestion stream request finished: mode=%q ok=%t duration_ms=%d revision=%d",
			request.Mode, err == nil, time.Since(started).Milliseconds(), result.Revision)
		response := ingestStreamResponse{Type: "error"}
		if err == nil {
			response = ingestStreamResponse{Type: "complete", Result: &result}
		}
		if err := encoder.Encode(response); err != nil {
			return err
		}
	}
	return scanner.Err()
}

// parseIngestStreamRequest decodes one stream line, accepting exactly one known
// ingest request and rejecting unknown fields, modes, and trailing documents.
func parseIngestStreamRequest(line []byte) (ingestStreamRequest, error) {
	var request ingestStreamRequest
	decoder := json.NewDecoder(bytes.NewReader(line))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || request.Type != "ingest" ||
		request.Mode != "" && request.Mode != "snapshot" && request.Mode != "run-bundles" {
		return ingestStreamRequest{}, errors.New("invalid ingestion stream request")
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return ingestStreamRequest{}, errors.New("ingestion stream requires one request per line")
	}
	return request, nil
}
