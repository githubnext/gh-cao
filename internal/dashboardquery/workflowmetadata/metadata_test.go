package workflowmetadata

import (
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name, input string
		want        Metadata
	}{
		{"configured", "# gh-aw-metadata: {\"compiler_version\":\"v1.2.3\",\"agent_id\":\"pi\",\"engine_versions\":{\"copilot\":\"1.0\"}}\nname: Test", Metadata{"v1.2.3", "pi"}},
		{"legacy", "# gh-aw-metadata: {\"compiler_version\":\"v1.2.3\"}\r\n", Metadata{"v1.2.3", ""}},
		{"unrecognized", "# gh-aw-metadata: {\"compiler_version\":\"v1.2.3\",\"agent_id\":\"not-an-engine\"}", Metadata{"v1.2.3", ""}},
		{"wrong type", "# gh-aw-metadata: {\"compiler_version\":\"v1.2.3\",\"agent_id\":[\"pi\"]}", Metadata{"v1.2.3", ""}},
		{"malformed", "# gh-aw-metadata: {", Metadata{}},
		{"body", "name: Test\n# gh-aw-metadata: {\"agent_id\":\"pi\"}\n", Metadata{}},
		{"bounded", strings.Repeat("# header\n", 3000) + "# gh-aw-metadata: {\"agent_id\":\"pi\"}\n", Metadata{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Parse(strings.NewReader(tt.input)); got != tt.want {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}
