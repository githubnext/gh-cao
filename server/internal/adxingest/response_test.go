package adxingest

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const v1Response = `{"Tables":[
{"TableName":"Table_0","Columns":[{"ColumnName":"Bytes","ColumnType":"real"}],"Rows":[[0.0]]},
{"TableName":"Table_1","Columns":[],"Rows":[]},
{"TableName":"Table_2","Columns":[{"ColumnName":"Severity"},{"ColumnName":"StatusCode"},{"ColumnName":"StatusDescription"}],"Rows":[[4,0,"completed"],[6,0,"stats"]]},
{"TableName":"Table_3","Columns":[{"ColumnName":"Ordinal"},{"ColumnName":"Kind"},{"ColumnName":"Name"}],"Rows":[[0,"QueryResult","PrimaryResult"],[1,"QueryProperties","@ExtendedProperties"],[2,"QueryStatus","QueryStatus"]]}
]}`

func TestV1QueryResolvesStatusByTableOfContents(t *testing.T) {
	rows, err := decodeQueryResponse([]byte(v1Response))
	if err != nil || len(rows) != 1 {
		t.Fatalf("actual v1 response rejected: %v", err)
	}
	for _, response := range []string{
		strings.Replace(v1Response, `[4,0,"completed"]`, `[2,80,"SECRET-PARTIAL-RESULT"]`, 1),
		strings.Replace(v1Response, `[4,0,"completed"]`, `[4,80,"SECRET-ERROR"]`, 1),
		strings.Replace(v1Response, `[2,"QueryStatus"`, `[9,"QueryStatus"`, 1),
		strings.Replace(v1Response, `[2,"QueryStatus"`, `[0,"QueryStatus"`, 1),
		strings.Replace(v1Response, `"Severity"`, `"Unexpected"`, 1),
		strings.Replace(v1Response, `[2,"QueryStatus","QueryStatus"]`, `[2,"QueryProperties","@Unknown"]`, 1),
		v1Response + `{"Tables":[]}`,
	} {
		if _, err := decodeQueryResponse([]byte(response)); err == nil || strings.Contains(err.Error(), "SECRET") {
			t.Fatalf("unsafe partial/malformed response: %v", err)
		}
	}
}

func TestCapacityAcceptsRealIntegralBytesButFailsClosedAtBudget(t *testing.T) {
	for _, test := range []struct {
		value string
		ok    bool
	}{
		{"0.0", true}, {"99.0", true}, {"1e1", true}, {"100.0", false}, {"-1.0", false},
		{"1.5", false}, {"1e999", false}, {`"0"`, false}, {"null", false},
	} {
		t.Run(test.value, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(strings.Replace(v1Response, "[[0.0]]", "[["+test.value+"]]", 1)))
			}))
			defer server.Close()
			client := &Client{endpoint: server.URL, database: "fixture", http: server.Client(), storageBudget: 100}
			err := client.Capacity(t.Context())
			if (err == nil) != test.ok || !test.ok && !errors.Is(err, ErrStorage) {
				t.Fatalf("capacity response %s: %v", test.value, err)
			}
		})
	}
}
