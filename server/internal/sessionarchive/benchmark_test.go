package sessionarchive

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func treeBytes(path string) int64 {
	var size int64
	_ = filepath.WalkDir(path, func(_ string, entry fs.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			if info, err := entry.Info(); err == nil {
				size += info.Size()
			}
		}
		return nil
	})
	return size
}

func benchmarkPayload(seed string, bytes int) string {
	var content strings.Builder
	for content.Len() < bytes {
		seed = hash([]byte(seed))
		content.WriteString(seed)
	}
	return content.String()[:bytes]
}

func benchmarkProjection(t *testing.T, c Config, run int) Projection {
	t.Helper()
	var source strings.Builder
	for i := 0; i < 8192; i++ {
		size := 256
		if i%512 == 511 {
			size = 64 << 10
		}
		event := map[string]any{"type": "tool.execution_complete", "id": strconv.Itoa(i),
			"data": map[string]any{"toolCallId": strconv.Itoa(i), "success": i%17 != 0, "error": nil,
				"output": map[string]any{"content": benchmarkPayload(strconv.Itoa(run)+":"+strconv.Itoa(i), size),
					"nested": []any{nil, true, json.Number("9007199254740993"), 1.25}}}}
		if i == 0 {
			event = map[string]any{"type": "session.format", "data": map[string]any{"version": 1}}
		}
		content, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		source.Write(content)
		source.WriteByte('\n')
	}
	p, err := Project(c.traceConfig(), c.Namespace, "octo/api", strconv.Itoa(run), time.Now().UTC().Format(time.DateOnly), 1, []byte(source.String()), time.Now())
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// This is deliberately opt-in, but never lowers the required million-event
// cardinality or substitutes a CLI simulator when enabled.
func TestDuckDBArchiveMillionEventBenchmark(t *testing.T) {
	if os.Getenv("CAO_SESSION_ARCHIVE_BENCHMARK") != "1" {
		t.Skip("opt in with CAO_SESSION_ARCHIVE_BENCHMARK=1 and CAO_SESSION_ARCHIVE_REQUIRE_DUCKDB=1")
	}
	_ = testConverter(t)
	c := testConfig(t)
	temp := t.TempDir()
	t.Setenv("TMPDIR", temp)
	a := openTestArchive(t, c, nil)
	type metrics struct {
		Events, Traces, Files                  int
		ParquetBytes, ApprovedBytes            int64
		PeakGoHeapBytes, PeakGoReservedBytes   uint64
		PeakArchiveVolumeBytes, PeakTempBytes  int64
		CLIHeapBudgetBytes, CLITempBudgetBytes int64
		ProjectSeconds, PublicationSeconds     float64
		SummarySeconds, TimelineSeconds        float64
		TotalSeconds                           float64
	}
	result := metrics{CLIHeapBudgetBytes: a.converter.config.MemoryBytes, CLITempBudgetBytes: a.converter.config.TempBytes}
	stop, stopped := make(chan struct{}), make(chan struct{})
	var mu sync.Mutex
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				var memory runtime.MemStats
				runtime.ReadMemStats(&memory)
				volume, temporary := treeBytes(c.Directory), treeBytes(temp)
				mu.Lock()
				result.PeakGoHeapBytes = max(result.PeakGoHeapBytes, memory.HeapAlloc)
				result.PeakGoReservedBytes = max(result.PeakGoReservedBytes, memory.Sys)
				result.PeakArchiveVolumeBytes = max(result.PeakArchiveVolumeBytes, volume)
				result.PeakTempBytes = max(result.PeakTempBytes, temporary)
				mu.Unlock()
			}
		}
	}()
	defer func() { close(stop); <-stopped }()
	started := time.Now()
	samples := map[string]Event{}
	for run := 1; run <= 128; run++ {
		start := time.Now()
		p := benchmarkProjection(t, c, run)
		mu.Lock()
		result.ProjectSeconds += time.Since(start).Seconds()
		result.Events += len(p.Events)
		result.Traces++
		for _, event := range p.Events {
			result.ApprovedBytes += int64(len(event.PayloadJSON))
		}
		mu.Unlock()
		if run == 1 || run == 128 {
			samples[p.RunID] = p.Events[511]
		}
		if err := a.Admit(t.Context(), p); err != nil {
			t.Fatal(err)
		}
		if run%8 == 0 {
			start := time.Now()
			if err := a.Flush(t.Context()); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			result.PublicationSeconds += time.Since(start).Seconds()
			mu.Unlock()
		}
	}
	s := pinTestArchive(t, a)
	start := time.Now()
	summary, err := s.Summary(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	result.SummarySeconds = time.Since(start).Seconds()
	mu.Unlock()
	var events int64
	for _, row := range summary {
		events += row.Events
	}
	if events != 1048576 || len(summary) != 128 {
		t.Fatalf("required complete million-event output failed: events=%d traces=%d", events, len(summary))
	}
	start = time.Now()
	for run, sample := range samples {
		rows, err := s.Timeline(context.Background(), TimelineRequest{"octo/api", run, 1, 8192})
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 8192 || rows[511].PayloadJSON != sample.PayloadJSON || rows[511].ContentDigest != sample.ContentDigest {
			t.Fatal("representative large-payload sample failed round-trip")
		}
	}
	var parquetBytes int64
	for _, file := range a.state.Manifest.Files {
		parquetBytes += file.Bytes
		if len(file.Members) != 8 || file.Rows != 65536 {
			t.Fatal("benchmark did not pack eight complete traces per file")
		}
	}
	mu.Lock()
	result.TimelineSeconds = time.Since(start).Seconds()
	result.Files = len(a.state.Manifest.Files)
	result.ParquetBytes = parquetBytes
	result.TotalSeconds = time.Since(started).Seconds()
	report, err := json.Marshal(result)
	mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if result.Files != 16 {
		t.Fatalf("expected 16 immutable packed files, got %d", result.Files)
	}
	t.Logf("million_event_production_archive_benchmark=%s", report)
	t.Log("Go heap and filesystem peaks sampled every 20ms; CLI memory/temp are configured engine budgets, not measured process RSS. No Azure/live ADX throughput established.")
}
