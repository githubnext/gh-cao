package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/ingest"
)

func TestIngestStreamReusesRunnerAndReportsFailedPublications(t *testing.T) {
	var output bytes.Buffer
	calls := 0
	err := runIngestStream(t.Context(), strings.NewReader(strings.Repeat("{\"type\":\"ingest\"}\n", 3)), &output,
		func(context.Context) (ingest.Result, error) {
			calls++
			if calls == 2 {
				return ingest.Result{}, errors.New("private database diagnostics")
			}
			return ingest.Result{Revision: int64(calls)}, nil
		})
	if err != nil || calls != 3 {
		t.Fatalf("stream did not process independent publications: calls=%d err=%v", calls, err)
	}
	if strings.Contains(output.String(), "private") {
		t.Fatal("ingestion stream exposed database diagnostics")
	}
	decoder := json.NewDecoder(&output)
	for _, kind := range []string{"ready", "complete", "error", "complete"} {
		var response ingestStreamResponse
		if err := decoder.Decode(&response); err != nil || response.Type != kind {
			t.Fatalf("incorrect publication response: %+v %v", response, err)
		}
	}
}

func TestIngestStreamRejectsRequestsThatChangeItsScope(t *testing.T) {
	for _, input := range []string{
		`{"type":"ingest","source":"/other"}`, `{"type":"rebuild"}`, `{"type":"ingest"} {}`,
	} {
		t.Run(input, func(t *testing.T) {
			var output bytes.Buffer
			err := runIngestStream(t.Context(), strings.NewReader(input+"\n"), &output,
				func(context.Context) (ingest.Result, error) {
					t.Fatal("invalid request reached the canonical writer")
					return ingest.Result{}, nil
				})
			if err == nil {
				t.Fatal("invalid ingestion request was accepted")
			}
		})
	}
}
