package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log"

	debuglogger "github.com/githubnext/gh-cao/server/internal/logger"
	"github.com/githubnext/gh-cao/server/internal/server"
)

var archiveRequestLog = debuglogger.New("cao:dashboard:session-archive-requests")

const (
	sessionArchiveCaptureType = "session-archive-capture"
	maxSessionArchiveSources  = 100
)

// sessionArchiveCaptureSource names one repository's owner-written frames.
type sessionArchiveCaptureSource struct {
	Repository string `json:"repository"`
	Logs       string `json:"logs"`
	Frames     string `json:"frames"`
}

type sessionArchiveCaptureRequest struct {
	Type    string                        `json:"type"`
	Sources []sessionArchiveCaptureSource `json:"sources"`
}

type sessionArchiveCapturer interface {
	CaptureSessionArchiveFrames(ctx context.Context, repository, logs, frames string) (server.SessionArchiveCapture, error)
}

// parseSessionArchiveRequest decodes one owner stdin line; reason is empty when
// the request is valid and otherwise names the rejection class.
func parseSessionArchiveRequest(line []byte) (request sessionArchiveCaptureRequest, reason string) {
	if err := json.Unmarshal(line, &request); err != nil {
		return sessionArchiveCaptureRequest{}, "invalid_json"
	}
	switch {
	case request.Type != sessionArchiveCaptureType:
		return sessionArchiveCaptureRequest{}, "unexpected_type"
	case len(request.Sources) == 0:
		return sessionArchiveCaptureRequest{}, "no_sources"
	case len(request.Sources) > maxSessionArchiveSources:
		return sessionArchiveCaptureRequest{}, "too_many_sources"
	}
	return request, ""
}

// readSessionArchiveRequests forwards owner capture requests until stdin
// closes. A pending request is never queued behind another: frames on disk
// are cumulative, so the latest pending request covers earlier ones.
func readSessionArchiveRequests(input io.Reader, requests chan<- sessionArchiveCaptureRequest, logger *log.Logger) {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		request, reason := parseSessionArchiveRequest(scanner.Bytes())
		if reason != "" {
			archiveRequestLog.Printf("session archive capture request rejected: reason=%s", reason)
			logger.Print("ignored invalid session archive capture request")
			continue
		}
		select {
		case requests <- request:
		default:
			archiveRequestLog.Printf("session archive capture request coalesced: sources=%d", len(request.Sources))
		}
	}
}

func serveSessionArchiveRequests(ctx context.Context, app sessionArchiveCapturer, requests <-chan sessionArchiveCaptureRequest, logger *log.Logger) {
	reported := map[string]server.SessionArchiveCapture{}
	for {
		var request sessionArchiveCaptureRequest
		select {
		case <-ctx.Done():
			return
		case request = <-requests:
		}
		for _, source := range request.Sources {
			result, err := app.CaptureSessionArchiveFrames(ctx, source.Repository, source.Logs, source.Frames)
			if err != nil {
				if ctx.Err() != nil {
					return
				}
				logger.Printf("session archive capture incomplete repository=%s: %v", source.Repository, err)
				continue
			}
			if reported[source.Repository] != result {
				reported[source.Repository] = result
				logger.Printf("session archive capture repository=%s frames=%d admitted=%d gaps=%d",
					source.Repository, result.Frames, result.Admitted, result.Gaps)
			}
		}
	}
}
