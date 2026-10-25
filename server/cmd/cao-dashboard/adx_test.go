package main

import (
	"bytes"
	"strings"
	"testing"
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
