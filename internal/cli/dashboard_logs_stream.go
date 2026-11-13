package cli

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"
)

var errDashboardLogsIncomplete = errors.New("gh-aw JSONL stream ended without a final summary and complete event")

func streamDashboardLogs(ctx context.Context, request dashboardLogRequest, args []string, command dashboardLogCommand) error {
	publishCtx := ctx
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	publish := request.OnRuns
	batches := make(chan []map[string]any, 2)
	published := make(chan error, 1)
	go func() {
		for batch := range batches {
			if publish != nil {
				if err := publish(publishCtx, batch); err != nil {
					cancel()
					published <- err
					return
				}
			}
		}
		published <- nil
	}()
	request.OnRuns = func(ctx context.Context, batch []map[string]any) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case batches <- batch:
			return nil
		}
	}
	reader, writer := io.Pipe()
	defer func() { _ = reader.Close() }()
	done := make(chan error, 1)
	go func() {
		err := command(ctx, args, writer)
		_ = writer.CloseWithError(err)
		done <- err
	}()
	summary, parseErr := readDashboardLogStream(ctx, reader, request)
	if parseErr != nil {
		cancel()
		_ = reader.CloseWithError(parseErr)
	}
	close(batches)
	publishErr := <-published
	commandErr := <-done
	if publishErr != nil {
		return publishErr
	}
	if (errors.Is(parseErr, context.Canceled) || errors.Is(parseErr, context.DeadlineExceeded)) &&
		commandErr != nil && !errors.Is(commandErr, context.Canceled) && !errors.Is(commandErr, context.DeadlineExceeded) {
		return commandErr
	}
	if parseErr != nil && !errors.Is(parseErr, errDashboardLogsIncomplete) {
		return parseErr
	}
	if commandErr != nil {
		return commandErr
	}
	if parseErr != nil {
		return parseErr
	}
	for _, record := range summary {
		if request.OnRunReceived != nil {
			if err := request.OnRunReceived(ctx, record); err != nil {
				return err
			}
		}
	}
	if publish != nil {
		return publish(ctx, summary)
	}
	return ctx.Err()
}

type dashboardLogLine struct {
	content []byte
	err     error
}

func dashboardLogLines(ctx context.Context, input io.Reader) <-chan dashboardLogLine {
	lines := make(chan dashboardLogLine, 1)
	go func() {
		defer close(lines)
		scanner := bufio.NewScanner(io.LimitReader(input, (256<<20)+1))
		scanner.Buffer(make([]byte, 64<<10), 64<<20)
		for scanner.Scan() {
			line := dashboardLogLine{content: bytes.Clone(scanner.Bytes())}
			select {
			case lines <- line:
			case <-ctx.Done():
				return
			}
		}
		if err := scanner.Err(); err != nil {
			select {
			case lines <- dashboardLogLine{err: err}:
			case <-ctx.Done():
			}
		}
	}()
	return lines
}

func readDashboardLogStream(ctx context.Context, input io.Reader, request dashboardLogRequest) (summary []map[string]any, err error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	lines := dashboardLogLines(ctx, input)
	timer := time.NewTicker(time.Second)
	defer timer.Stop()
	pending := []map[string]any{}
	published := false
	flush := func() error {
		if len(pending) == 0 {
			return nil
		}
		batch := pending
		pending = nil
		published = true
		if request.OnRuns != nil {
			return request.OnRuns(ctx, batch)
		}
		return nil
	}
	defer func() {
		if err != nil && ctx.Err() == nil {
			err = errors.Join(err, flush())
		}
	}()
	summarized, complete := false, false
	total, runs := 0, 0
	for {
		var line []byte
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timer.C:
			if err := flush(); err != nil {
				return nil, err
			}
			continue
		case next, ok := <-lines:
			if !ok {
				if err := flush(); err != nil {
					return nil, err
				}
				if !summarized || !complete {
					return nil, errDashboardLogsIncomplete
				}
				return summary, nil
			}
			if next.err != nil {
				return nil, next.err
			}
			line = next.content
		}
		total += len(line) + 1
		if total > 256<<20 {
			return nil, errors.New("gh-aw JSONL stream exceeds the 256 MiB collection budget")
		}
		if strings.TrimSpace(string(line)) == "" {
			continue
		}
		var event struct {
			Type  string         `json:"type"`
			Stage string         `json:"stage"`
			Run   map[string]any `json:"run"`
			Data  *struct {
				Runs json.RawMessage `json:"runs"`
			} `json:"data"`
		}
		decoder := json.NewDecoder(bytes.NewReader(line))
		decoder.UseNumber()
		if err := decoder.Decode(&event); err != nil || event.Type == "" || complete {
			return nil, errors.New("gh-aw output is not a valid JSONL event stream")
		}
		if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
			return nil, errors.New("gh-aw JSONL output must contain one event per line")
		}
		switch event.Type {
		case "run":
			runs++
			if summarized || event.Run == nil || len(line) > 4<<20 || runs > request.Count {
				return nil, errors.New("gh-aw stream contains an invalid or out-of-budget run event")
			}
			dashboardStreamRunIdentity(event.Run)
			if request.OnRunReceived != nil {
				if err := request.OnRunReceived(ctx, event.Run); err != nil {
					return nil, err
				}
			}
			pending = append(pending, event.Run)
			if !published || len(pending) == 16 {
				if err := flush(); err != nil {
					return nil, err
				}
			}
		case "summary":
			if summarized || event.Data == nil || len(event.Data.Runs) == 0 {
				return nil, errors.New("gh-aw stream contains an invalid summary")
			}
			decoder := json.NewDecoder(bytes.NewReader(event.Data.Runs))
			decoder.UseNumber()
			if err := decoder.Decode(&summary); err != nil || len(summary) > request.Count {
				return nil, errors.New("gh-aw stream summary exceeds the run budget or is malformed")
			}
			for _, run := range summary {
				if run == nil {
					return nil, errors.New("gh-aw stream summary contains a null run")
				}
				dashboardStreamRunIdentity(run)
			}
			summarized = true
		case "complete":
			complete = true
		case "error":
			return nil, errors.New("gh-aw reported a collection error; last published evidence retained")
		case "warning", "progress":
			if event.Type == "warning" || event.Stage == "failed" || event.Stage == "skipped" {
				if request.Limited != nil {
					*request.Limited = true
				}
			}
		}
	}
}

func dashboardStreamRunIdentity(run map[string]any) {
	if id, ok := run["run_id"].(json.Number); ok {
		run["run_id"] = id.String()
	}
}
