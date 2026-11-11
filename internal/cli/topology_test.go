package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTopologyCommandsOffline(t *testing.T) {
	directory := t.TempDir()
	bin := filepath.Join(directory, "bin")
	if err := os.Mkdir(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "bicep"), []byte("#!/bin/sh\nexit 0\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	config, output := filepath.Join(directory, "requirements.yaml"), filepath.Join(directory, "bundle")
	run := func(args ...string) (string, error) {
		t.Helper()
		root := newRootCommand("test", func(context.Context) (string, error) {
			t.Fatal("topology must not probe GitHub")
			return "", nil
		})
		var buffer bytes.Buffer
		root.SetOut(&buffer)
		root.SetErr(&buffer)
		root.SetArgs(append([]string{"topology"}, args...))
		err := root.ExecuteContext(context.Background())
		return buffer.String(), err
	}
	for _, args := range [][]string{
		{"--help"}, {"init", "--out", config}, {"generate", "--config", config, "--out", output}, {"check", output},
	} {
		if text, err := run(args...); err != nil {
			t.Fatalf("%v: %s: %v", args, text, err)
		}
	}
	for _, args := range [][]string{
		{"init", "--out", config}, {"generate", "--config", config, "--out", output}, {"generate", "--config", config}, {"check"}, {"generate", "--unknown"},
	} {
		if _, err := run(args...); err == nil {
			t.Fatalf("expected failure: %v", args)
		}
	}
	if err := os.WriteFile(filepath.Join(output, "main.bicep"), []byte("edited"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := run("check", output); err == nil || !strings.Contains(err.Error(), "drift") {
		t.Fatalf("expected drift: %v", err)
	}
}
