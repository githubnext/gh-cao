package main

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log"

	"github.com/githubnext/gh-cao/server/internal/server"
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

// readSessionArchiveRequests forwards owner capture requests until stdin
// closes. A pending request is never queued behind another: frames on disk
// are cumulative, so the latest pending request covers earlier ones.
func readSessionArchiveRequests(input io.Reader, requests chan<- sessionArchiveCaptureRequest, logger *log.Logger) {
	scanner := bufio.NewScanner(input)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for scanner.Scan() {
		var request sessionArchiveCaptureRequest
		if err := json.Unmarshal(scanner.Bytes(), &request); err != nil || request.Type != "session-archive-capture" ||
			len(request.Sources) == 0 || len(request.Sources) > 100 {
			logger.Print("ignored invalid session archive capture request")
			continue
		}
		select {
		case requests <- request:
		default:
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
