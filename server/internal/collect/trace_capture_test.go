package collect

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type traceProbe struct {
	target, record string
	fail           bool
	captured       bool
}

func (p *traceProbe) PrepareCache(_ context.Context, _ string, _, target string) error {
	p.target = target
	return os.WriteFile(target+"new.jsonl", []byte("{}\n"), 0o600)
}
func (p *traceProbe) Capture(_ context.Context, _ string, _, prefix string) error {
	content, err := os.ReadFile(p.record)
	if err != nil {
		return err
	}
	if !strings.Contains(string(content), "collect-workflow-logs.mjs") || strings.Contains(string(content), "session-evidence.mjs") || prefix != p.target {
		return errors.New("trace capture was not before lossy projection")
	}
	p.captured = true
	if p.fail {
		return errors.New("capture backpressure")
	}
	return nil
}
func (*traceProbe) WithdrawRepository(context.Context, string) error { return nil }

func TestTraceAdmissionPrecedesCachePublicationAndProjection(t *testing.T) {
	for _, fail := range []bool{true, false} {
		t.Run(map[bool]string{true: "backpressure", false: "captured"}[fail], func(t *testing.T) {
			directory := t.TempDir()
			catalog := filepath.Join(directory, "catalog")
			writeValidCollectionCatalog(t, catalog)
			record := filepath.Join(directory, "commands")
			node := writeFakeBinary(t, directory, "node", `printf '%s\n' "$@" >> `+record)
			probe := &traceProbe{record: record, fail: fail}
			runner := Runner{Lake: Lake{Directory: filepath.Join(directory, "lake")}, CatalogRoot: catalog,
				NodeBinary: node, GitHubBinary: "gh", Tokens: fakeTokens{token: "test"}, Traces: probe}
			err := runner.Collect(t.Context(), Task{Repository: "octo/api", InstallationID: 1})
			if (err != nil) != fail || !probe.captured {
				t.Fatalf("capture result: %v", err)
			}
			_, published := os.Stat(filepath.Join(runner.Lake.ShardDirectory(), runner.Lake.ShardPrefix("octo/api")+"new.jsonl"))
			if fail && !errors.Is(published, os.ErrNotExist) {
				t.Fatal("uncaptured metadata committed to skip future acquisition")
			}
			if !fail && published != nil {
				t.Fatal("captured metadata was not published")
			}
		})
	}
}
