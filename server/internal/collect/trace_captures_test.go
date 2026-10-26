package collect

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type destinationProbe struct {
	input, suffix       string
	captured, withdrawn bool
	fail                bool
}

func (p *destinationProbe) PrepareCache(_ context.Context, _ string, source, target string) error {
	// #nosec G304 -- the cache prefix is created inside this test's private directory.
	data, err := os.ReadFile(source + "1.jsonl")
	if err != nil {
		return err
	}
	p.input = string(data)
	// #nosec G703 -- prefixes are test-owned files under a private temporary directory.
	return os.WriteFile(target+"1.jsonl", append(data, p.suffix...), 0o600)
}

func (p *destinationProbe) Capture(context.Context, string, string, string) error {
	p.captured = true
	if p.fail {
		return errors.New("destination unavailable")
	}
	return nil
}

func (p *destinationProbe) WithdrawRepository(context.Context, string) error {
	p.withdrawn = true
	if p.fail {
		return errors.New("destination unavailable")
	}
	return nil
}

func TestTraceCapturesRequireEveryDestinationBeforeCachePublication(t *testing.T) {
	directory := t.TempDir()
	source, target := filepath.Join(directory, "source-"), filepath.Join(directory, "target-")
	if err := os.WriteFile(source+"1.jsonl", []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	first, second := &destinationProbe{suffix: "-first"}, &destinationProbe{suffix: "-second"}
	captures := TraceCaptures{first, second}
	if err := captures.PrepareCache(t.Context(), "octo/api", source, target); err != nil {
		t.Fatal(err)
	}
	// #nosec G304 -- the cache prefix is created inside this test's private directory.
	data, err := os.ReadFile(target + "1.jsonl")
	if err != nil || string(data) != "source-first-second" || second.input != "source-first" {
		t.Fatalf("cache destinations did not compose: %s %v", data, err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".capture-cache-") {
			t.Fatal("private intermediate cache was not removed")
		}
	}
	first.fail = true
	if err := captures.Capture(t.Context(), "octo/api", "logs", target); err == nil || !first.captured || !second.captured {
		t.Fatal("independent capture failure was hidden or prevented the other admission")
	}
	if err := captures.WithdrawRepository(t.Context(), "octo/api"); err == nil || !first.withdrawn || !second.withdrawn {
		t.Fatal("withdrawal did not reach every destination")
	}
}
