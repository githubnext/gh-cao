package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/server"
)

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
