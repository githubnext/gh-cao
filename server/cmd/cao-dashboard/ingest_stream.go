package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/githubnext/gh-cao/server/internal/ingest"
	debuglogger "github.com/githubnext/gh-cao/server/internal/logger"
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
		result, err := run(ctx, request)
		ingestStreamLog.Printf("ingestion stream request finished: mode=%q ok=%t", request.Mode, err == nil)
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
