package main

import (
	"bytes"
	"context"
	"io"
	"log"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/server"
)

type fakeSessionArchiveCapturer struct {
	calls chan string
}

func (f fakeSessionArchiveCapturer) CaptureSessionArchiveFrames(_ context.Context, repository, _, _ string) (server.SessionArchiveCapture, error) {
	f.calls <- repository
	return server.SessionArchiveCapture{Frames: 1, Admitted: 1}, nil
}

func TestSessionArchiveStdinRequests(t *testing.T) {
	var logs bytes.Buffer
	logger := log.New(&logs, "", 0)
	requests := make(chan sessionArchiveCaptureRequest, 1)
	readSessionArchiveRequests(strings.NewReader("not json\n"+
		`{"type":"other","sources":[{"repository":"o/r"}]}`+"\n"+
		`{"type":"session-archive-capture","sources":[{"repository":"o/r","logs":"/l","frames":"/f"}]}`+"\n"+
		`{"type":"session-archive-capture","sources":[{"repository":"o/dropped"}]}`+"\n"), requests, logger)
	if strings.Count(logs.String(), "ignored invalid") != 2 || len(requests) != 1 {
		t.Fatalf("logs=%q pending=%d", logs.String(), len(requests))
	}
	capturer := fakeSessionArchiveCapturer{calls: make(chan string, 2)}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		serveSessionArchiveRequests(ctx, capturer, requests, log.New(io.Discard, "", 0))
		close(done)
	}()
	select {
	case repository := <-capturer.calls:
		if repository != "o/r" {
			t.Fatalf("captured %s", repository)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("capture request was not served")
	}
	cancel()
	<-done
}
