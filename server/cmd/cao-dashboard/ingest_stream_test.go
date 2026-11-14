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
		func(context.Context, ingestStreamRequest) (ingest.Result, error) {
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
		`{"type":"ingest","mode":"automatic"}`, `{"type":"ingest","mode":"run-bundles","namespace":"other"}`,
	} {
		t.Run(input, func(t *testing.T) {
			var output bytes.Buffer
			err := runIngestStream(t.Context(), strings.NewReader(input+"\n"), &output,
				func(context.Context, ingestStreamRequest) (ingest.Result, error) {
					t.Fatal("invalid request reached the canonical writer")
					return ingest.Result{}, nil
				})
			if err == nil {
				t.Fatal("invalid ingestion request was accepted")
			}
		})
	}
}

func TestIngestStreamExplicitlyRoutesRunBundles(t *testing.T) {
	var output bytes.Buffer
	var modes []string
	err := runIngestStream(t.Context(), strings.NewReader(
		"{\"type\":\"ingest\",\"mode\":\"run-bundles\"}\n{\"type\":\"ingest\",\"mode\":\"snapshot\"}\n"), &output,
		func(_ context.Context, request ingestStreamRequest) (ingest.Result, error) {
			modes = append(modes, request.Mode)
			return ingest.Result{Revision: 1}, nil
		})
	if err != nil || strings.Join(modes, ",") != "run-bundles,snapshot" {
		t.Fatalf("explicit ingestion mode was not honored: %v %v", modes, err)
	}
}

func TestParseIngestStreamRequest(t *testing.T) {
	for _, test := range []struct {
		name, line, mode string
	}{
		{"default mode", `{"type":"ingest"}`, ""},
		{"snapshot", `{"type":"ingest","mode":"snapshot"}`, "snapshot"},
		{"run bundles", `{"type":"ingest","mode":"run-bundles"}`, "run-bundles"},
		{"trailing whitespace", `{"type":"ingest"}  `, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, err := parseIngestStreamRequest([]byte(test.line))
			if err != nil || request.Type != "ingest" || request.Mode != test.mode {
				t.Fatalf("valid request was not parsed: %+v %v", request, err)
			}
		})
	}
}

func TestParseIngestStreamRequestRejectsInvalidLines(t *testing.T) {
	for _, test := range []struct {
		name, line, message string
	}{
		{"empty", ``, "invalid ingestion stream request"},
		{"malformed", `{"type":`, "invalid ingestion stream request"},
		{"unknown type", `{"type":"rebuild"}`, "invalid ingestion stream request"},
		{"unknown mode", `{"type":"ingest","mode":"automatic"}`, "invalid ingestion stream request"},
		{"unknown field", `{"type":"ingest","source":"/other"}`, "invalid ingestion stream request"},
		{"second document", `{"type":"ingest"} {"type":"ingest"}`, "ingestion stream requires one request per line"},
		{"trailing garbage", `{"type":"ingest"} x`, "ingestion stream requires one request per line"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request, err := parseIngestStreamRequest([]byte(test.line))
			if err == nil || err.Error() != test.message || request != (ingestStreamRequest{}) {
				t.Fatalf("invalid request was accepted: %+v %v", request, err)
			}
		})
	}
}

func TestIngestStreamStopsAfterRejectedRequest(t *testing.T) {
	var output bytes.Buffer
	calls := 0
	err := runIngestStream(t.Context(), strings.NewReader("{\"type\":\"ingest\"}\n{\"type\":\"rebuild\"}\n{\"type\":\"ingest\"}\n"), &output,
		func(context.Context, ingestStreamRequest) (ingest.Result, error) {
			calls++
			return ingest.Result{Revision: 1}, nil
		})
	if err == nil || calls != 1 {
		t.Fatalf("stream continued past a rejected request: calls=%d err=%v", calls, err)
	}
	decoder := json.NewDecoder(&output)
	for _, kind := range []string{"ready", "complete"} {
		var response ingestStreamResponse
		if err := decoder.Decode(&response); err != nil || response.Type != kind {
			t.Fatalf("incorrect response before rejection: %+v %v", response, err)
		}
	}
	if decoder.More() {
		t.Fatal("rejected request produced a response")
	}
}
