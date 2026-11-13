package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/githubnext/gh-cao/server/internal/ingest"
)

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
		var request ingestStreamRequest
		decoder := json.NewDecoder(bytes.NewReader(scanner.Bytes()))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil || request.Type != "ingest" ||
			request.Mode != "" && request.Mode != "snapshot" && request.Mode != "run-bundles" {
			return errors.New("invalid ingestion stream request")
		}
		if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
			return errors.New("ingestion stream requires one request per line")
		}
		result, err := run(ctx, request)
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
