package doctor

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestResolveRenderFormat(t *testing.T) {
	cases := []struct {
		name    string
		format  string
		want    renderFormat
		wantErr bool
	}{
		{name: "empty defaults to text", format: "", want: renderFormatText},
		{name: "text is accepted", format: "text", want: renderFormatText},
		{name: "text is case and whitespace insensitive", format: "  TEXT  ", want: renderFormatText},
		{name: "json is accepted", format: "json", want: renderFormatJSON},
		{name: "json is case insensitive", format: "JSON", want: renderFormatJSON},
		{name: "unknown format is rejected", format: "xml", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveRenderFormat(tc.format)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("resolveRenderFormat(%q) = nil error, want error", tc.format)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveRenderFormat(%q) returned unexpected error: %v", tc.format, err)
			}
			if got != tc.want {
				t.Fatalf("resolveRenderFormat(%q) = %q, want %q", tc.format, got, tc.want)
			}
		})
	}
}

func sampleReport() Report {
	return Report{
		SchemaVersion: ReportSchemaVersion,
		Tool:          "cao-dashboard",
		Version:       "test",
		Profile:       "actions",
		Namespace:     "cao",
		Checks: []Check{
			{ID: "runtime.example", Area: "runtime", Title: "Example", Status: StatusPass, Summary: "ok"},
		},
		Summary: Summary{Total: 1, Pass: 1, Status: StatusPass},
	}
}

func TestRenderTextIsDefaultAndIncludesSummary(t *testing.T) {
	var buffer bytes.Buffer
	report := sampleReport()
	if err := Render(&buffer, report, ""); err != nil {
		t.Fatalf("Render returned unexpected error: %v", err)
	}
	output := buffer.String()
	if !strings.Contains(output, "CAO server check-up") {
		t.Fatalf("Render(text) output missing header, got: %q", output)
	}
	if !strings.Contains(output, "runtime.example") {
		t.Fatalf("Render(text) output missing check id, got: %q", output)
	}
}

func TestRenderJSONProducesDecodableReport(t *testing.T) {
	var buffer bytes.Buffer
	report := sampleReport()
	if err := Render(&buffer, report, "json"); err != nil {
		t.Fatalf("Render returned unexpected error: %v", err)
	}
	var decoded Report
	if err := json.Unmarshal(buffer.Bytes(), &decoded); err != nil {
		t.Fatalf("Render(json) output did not decode as a Report: %v", err)
	}
	if decoded.Tool != report.Tool || decoded.Summary.Total != report.Summary.Total {
		t.Fatalf("Render(json) round-tripped incorrectly, got: %+v", decoded)
	}
}

func TestRenderRejectsUnknownFormat(t *testing.T) {
	var buffer bytes.Buffer
	err := Render(&buffer, sampleReport(), "yaml")
	if err == nil {
		t.Fatal("Render with unknown format returned nil error, want error")
	}
	if buffer.Len() != 0 {
		t.Fatalf("Render with unknown format wrote output, want none; got: %q", buffer.String())
	}
}
