package main

import (
	"bytes"
	"fmt"
	"log"
	"strings"
	"testing"
)

func TestParseSessionArchiveRequest(t *testing.T) {
	manySources := strings.Repeat(`{"repository":"o/r"},`, maxSessionArchiveSources) + `{"repository":"o/r"}`
	tests := []struct {
		name    string
		line    string
		reason  string
		sources int
	}{
		{"valid", `{"type":"session-archive-capture","sources":[{"repository":"o/r","logs":"/l","frames":"/f"}]}`, "", 1},
		{"at source limit", `{"type":"session-archive-capture","sources":[` + strings.Repeat(`{"repository":"o/r"},`, maxSessionArchiveSources-1) + `{"repository":"o/r"}]}`, "", maxSessionArchiveSources},
		{"over source limit", `{"type":"session-archive-capture","sources":[` + manySources + `]}`, "too_many_sources", 0},
		{"not json", "not json", "invalid_json", 0},
		{"wrong field type", `{"type":"session-archive-capture","sources":"x"}`, "invalid_json", 0},
		{"other type", `{"type":"other","sources":[{"repository":"o/r"}]}`, "unexpected_type", 0},
		{"missing type", `{"sources":[{"repository":"o/r"}]}`, "unexpected_type", 0},
		{"empty sources", `{"type":"session-archive-capture","sources":[]}`, "no_sources", 0},
		{"absent sources", `{"type":"session-archive-capture"}`, "no_sources", 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request, reason := parseSessionArchiveRequest([]byte(test.line))
			if reason != test.reason || len(request.Sources) != test.sources {
				t.Fatalf("reason=%q sources=%d, want reason=%q sources=%d", reason, len(request.Sources), test.reason, test.sources)
			}
			if reason != "" && request.Type != "" {
				t.Fatalf("rejected request retained type %q", request.Type)
			}
		})
	}
}

func TestParseSessionArchiveRequestKeepsSourceFields(t *testing.T) {
	request, reason := parseSessionArchiveRequest([]byte(
		`{"type":"session-archive-capture","sources":[{"repository":"o/r","logs":"/l","frames":"/f"}]}`))
	if reason != "" {
		t.Fatalf("unexpected rejection %q", reason)
	}
	if got, want := request.Sources[0], (sessionArchiveCaptureSource{Repository: "o/r", Logs: "/l", Frames: "/f"}); got != want {
		t.Fatalf("source = %+v, want %+v", got, want)
	}
}

func TestReadSessionArchiveRequestsCoalescesWhenPending(t *testing.T) {
	var logs bytes.Buffer
	requests := make(chan sessionArchiveCaptureRequest, 1)
	input := strings.Join([]string{
		`{"type":"session-archive-capture","sources":[{"repository":"o/first"}]}`,
		`{"type":"session-archive-capture","sources":[{"repository":"o/second"}]}`,
		"",
	}, "\n")
	readSessionArchiveRequests(strings.NewReader(input), requests, log.New(&logs, "", 0))
	if len(requests) != 1 {
		t.Fatalf("pending = %d, want 1", len(requests))
	}
	if got := (<-requests).Sources[0].Repository; got != "o/first" {
		t.Fatalf("pending request repository = %q, want o/first", got)
	}
	if logs.Len() != 0 {
		t.Fatalf("valid requests logged rejections: %q", logs.String())
	}
}

func TestReadSessionArchiveRequestsReportsEachRejection(t *testing.T) {
	var logs bytes.Buffer
	requests := make(chan sessionArchiveCaptureRequest, 4)
	input := fmt.Sprintf("%s\n%s\n%s\n", "{", `{"type":"x","sources":[{}]}`, `{"type":"session-archive-capture","sources":[]}`)
	readSessionArchiveRequests(strings.NewReader(input), requests, log.New(&logs, "", 0))
	if len(requests) != 0 {
		t.Fatalf("rejected requests forwarded: %d", len(requests))
	}
	if got := strings.Count(logs.String(), "ignored invalid session archive capture request"); got != 3 {
		t.Fatalf("rejection lines = %d, want 3 in %q", got, logs.String())
	}
}
