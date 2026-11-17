package sessionarchiveblob

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/sessionarchive"
)

// Runs the production keyword-match KQL over the pinned upstream keyword
// texts (Kelvin sign, long s, dotted and dotless i, non-ASCII letters) through
// real Kusto, whose regex \b, \w and (?i) differ from JavaScript.
func TestKustoKeywordLowering(t *testing.T) {
	engine := kustoArchiveEngine(t)
	content, err := os.ReadFile("../sessionarchive/testdata/lowering/cases.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases struct {
		Keywords []struct {
			ID      string `json:"id"`
			Text    string `json:"text"`
			Retries bool   `json:"retries"`
			Guard   bool   `json:"guard"`
		} `json:"keywords"`
	}
	if json.Unmarshal(content, &cases) != nil || len(cases.Keywords) == 0 {
		t.Fatal("missing pinned keyword cases")
	}
	values := make([]string, 0, 2*len(cases.Keywords))
	want := map[string][2]bool{}
	for _, c := range cases.Keywords {
		values = append(values, literal(c.ID), literal(c.Text))
		want[c.ID] = [2]bool{c.Retries, c.Guard}
	}
	kql := "datatable(id:string,[" + literal("text") + "]:string)[" + strings.Join(values, ",") + "]" +
		" | project id, retries=" + kqlExpression(sessionarchive.KeywordMatchExpression("text", "retry")) +
		", guard=" + kqlExpression(sessionarchive.KeywordMatchExpression("text", "guard"))
	response, err := engine.Execute(t.Context(), "/v1/rest/query", kql)
	if err != nil {
		t.Fatal(err)
	}
	var result struct{ Tables []queryTable }
	if json.Unmarshal(response, &result) != nil || len(result.Tables) == 0 {
		t.Fatal("real Kusto returned no query table")
	}
	rows := result.Tables[0].Rows
	if len(rows) != len(cases.Keywords) {
		t.Fatalf("real Kusto returned %d texts, want %d", len(rows), len(cases.Keywords))
	}
	for _, row := range rows {
		var id string
		var got [2]bool
		if len(row) != 3 || json.Unmarshal(row[0], &id) != nil || json.Unmarshal(row[1], &got[0]) != nil || json.Unmarshal(row[2], &got[1]) != nil {
			t.Fatal("invalid real Kusto keyword row")
		}
		if got != want[id] {
			t.Errorf("%s: Kusto retries=%t guard=%t, upstream %v", id, got[0], got[1], want[id])
		}
	}
}
