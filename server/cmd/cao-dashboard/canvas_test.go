package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/server"
)

func TestReadCanvasConfigOmittedPathDisablesCanvas(t *testing.T) {
	config, err := readCanvasConfig("")
	if err != nil || config != nil {
		t.Fatalf("got %v, %v; want nil, nil", config, err)
	}
}

func TestReadCanvasConfigFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "canvas.json")
	body := `{"prefix":"/abc","adapterUrl":"http://127.0.0.1:1","memoryDirectory":"/m"}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := readCanvasConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if config.Prefix != "/abc" || config.AdapterURL != "http://127.0.0.1:1" || config.MemoryDirectory != "/m" {
		t.Fatalf("unexpected config: %+v", config)
	}
}

func TestReadCanvasConfigMissingFile(t *testing.T) {
	_, err := readCanvasConfig(filepath.Join(t.TempDir(), "absent.json"))
	if err == nil || err.Error() != "read local canvas configuration" {
		t.Fatalf("got %v", err)
	}
}

func TestParseCanvasConfig(t *testing.T) {
	config, err := parseCanvasConfig([]byte(`{"prefix":"/p","adapterUrl":"http://x"}`))
	if err != nil || config.MemoryDirectory != "" || config.Prefix != "/p" {
		t.Fatalf("got %+v, %v", config, err)
	}
	for _, bad := range []string{"", "not json", `{"prefix":1}`} {
		if _, err := parseCanvasConfig([]byte(bad)); err == nil || err.Error() != "invalid local canvas configuration" {
			t.Errorf("%q: got %v", bad, err)
		}
	}
}

func TestReadOperatorConfig(t *testing.T) {
	if got, err := readOperatorConfig(""); err != nil || got != nil {
		t.Fatalf("omitted path: got %v, %v; want nil, nil", got, err)
	}
	path := filepath.Join(t.TempDir(), "operator.json")
	if got, err := readOperatorConfig(path); got != nil || err == nil || err.Error() != "read native operator configuration" {
		t.Fatalf("missing file: got %v, %v", got, err)
	}
	if err := os.WriteFile(path, []byte(`{"adapterUrl":"http://127.0.0.1:1234","operatorActions":{"repositories":["example/project"]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readOperatorConfig(path)
	want := &server.LocalOperatorConfig{AdapterURL: "http://127.0.0.1:1234", OperatorActions: &server.LocalOperatorActionsConfig{Repositories: []string{"example/project"}}}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("file: got %+v, %v; want %+v", got, err, want)
	}
	if err := os.WriteFile(path, []byte(`{"operatorActions":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := readOperatorConfig(path); got != nil || err == nil || err.Error() != "invalid native operator configuration" {
		t.Fatalf("invalid file: got %v, %v", got, err)
	}
}

func TestParseOperatorConfig(t *testing.T) {
	for _, input := range []string{`{}`, `null`, `{"operatorActions":null}`} {
		got, err := parseOperatorConfig([]byte(input))
		if err != nil || got == nil || got.AdapterURL != "" || got.OperatorActions != nil {
			t.Errorf("%q: got %+v, %v", input, got, err)
		}
	}
	for _, input := range []string{"", "not json", `{"adapterUrl":1}`, `{"operatorActions":{"repositories":[1]}}`} {
		got, err := parseOperatorConfig([]byte(input))
		if got != nil || err == nil || err.Error() != "invalid native operator configuration" {
			t.Errorf("%q: got %+v, %v", input, got, err)
		}
	}
}
