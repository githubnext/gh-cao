package postgresx

import (
	"reflect"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/model"
)

func TestEntityFieldValue(t *testing.T) {
	row := model.Row{
		"id": "run", "null": nil, "plain": "value",
		"provenance": map[string]any{"source": "session", "null": nil},
		"typed":      model.Row{"source": "canonical"},
		"sourceLink": "https://example.test/run", "source-link": "https://example.test/run",
	}
	for _, test := range []struct {
		path    string
		want    any
		present bool
	}{
		{"id", "run", true},
		{"missing", nil, false},
		{"null", nil, true},
		{"provenance.source", "session", true},
		{"provenance.null", nil, true},
		{"provenance.missing", nil, false},
		{"typed.source", "canonical", true},
		{"plain.href", nil, false},
		{"null.source", nil, false},
		{"missing.source", nil, false},
		{"sourceLink", "https://example.test/run", true},
		{"sourceLink.href", "https://example.test/run", true},
		{"sourceLink.relation", "source", true},
		{"source-link.href", "https://example.test/run", true},
		{"source-link.relation", "source", true},
		{"sourceLink.other", nil, false},
		{"provenance.source.extra", "session", true},
	} {
		t.Run(test.path, func(t *testing.T) {
			got, present := entityFieldValue(row, test.path)
			if present != test.present || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("got (%v, %v), want (%v, %v)", got, present, test.want, test.present)
			}
		})
	}
}

func BenchmarkEntityFieldValue(b *testing.B) {
	row := model.Row{"id": "run", "provenance": map[string]any{"source": "session"}}
	for _, source := range []string{"$runs", "$audits", "$tools"} {
		b.Run(source, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				for _, column := range entityTables[source].columns {
					entityFieldValue(row, column.field)
				}
			}
		})
	}
}
