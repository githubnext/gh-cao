package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/adxtrace"
)

func TestADXSchemaCommandIsOffline(t *testing.T) {
	t.Setenv("CAO_ADX_ENABLED", "true")
	command := newRootCommand()
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetArgs([]string{"adx", "schema"})
	if err := command.Execute(); err != nil {
		t.Fatal(err)
	}
	if strings.Count(output.String(), ".create-merge table ") != 15 ||
		!strings.Contains(output.String(), "cao_ReadySessions()") {
		t.Fatal("ADX setup schema is incomplete")
	}
}

func TestTraceNamespace(t *testing.T) {
	enabled := adxtrace.Config{Enabled: true}
	got, err := traceNamespace(enabled, "https://cluster.example.test", "traces")
	if err != nil || got != adxtrace.Namespace("https://cluster.example.test", "traces") {
		t.Fatalf("unexpected namespace %q, err=%v", got, err)
	}
	for name, c := range map[string]struct {
		config             adxtrace.Config
		endpoint, database string
	}{
		"disabled":         {adxtrace.Config{}, "https://cluster.example.test", "traces"},
		"missing endpoint": {enabled, "", "traces"},
		"missing database": {enabled, "https://cluster.example.test", ""},
	} {
		t.Run(name, func(t *testing.T) {
			if got, err := traceNamespace(c.config, c.endpoint, c.database); err == nil || got != "" {
				t.Fatalf("expected failure, got %q err=%v", got, err)
			}
		})
	}
}

func TestTraceSettingsFailClosedWhenDisabledOrInvalid(t *testing.T) {
	for _, value := range []string{"", "false", "maybe"} {
		env := map[string]string{"CAO_ADX_TRACES_ENABLED": value, "CAO_ADX_ENDPOINT": "https://cluster.example.test", "CAO_ADX_DATABASE": "traces"}
		if _, namespace, err := traceSettings(func(k string) string { return env[k] }); err == nil || namespace != "" {
			t.Fatalf("value %q: expected failure, got %q err=%v", value, namespace, err)
		}
	}
}
