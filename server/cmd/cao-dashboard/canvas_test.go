package main

import (
	"os"
	"path/filepath"
	"testing"
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

func TestReadOperatorConfigOmittedPathDisablesMaintenance(t *testing.T) {
	config, err := readOperatorConfig("")
	if err != nil || config != nil {
		t.Fatalf("got %v, %v; want nil, nil", config, err)
	}
}

func TestReadOperatorConfigFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "operator.json")
	if err := os.WriteFile(path, []byte(`{"adapterUrl":"http://127.0.0.1:1"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := readOperatorConfig(path)
	if err != nil || config.AdapterURL != "http://127.0.0.1:1" || config.OperatorActions != nil {
		t.Fatalf("got %+v, %v", config, err)
	}
}

func TestReadOperatorConfigMissingFile(t *testing.T) {
	_, err := readOperatorConfig(filepath.Join(t.TempDir(), "absent.json"))
	if err == nil || err.Error() != "read native operator configuration" {
		t.Fatalf("got %v", err)
	}
}

func TestParseOperatorConfigRejectsInvalidDocuments(t *testing.T) {
	for _, bad := range []string{"", "not json", `{"adapterUrl":1}`} {
		if _, err := parseOperatorConfig([]byte(bad)); err == nil || err.Error() != "invalid native operator configuration" {
			t.Errorf("%q: got %v", bad, err)
		}
	}
}
