package tokenbroker

import (
	"strings"
	"testing"
)

func TestUniqueJSONIsDepthBoundedAndStrict(t *testing.T) {
	for _, value := range []string{
		`{"a":1,"a":2}`,
		`{"a":{"b":1,"b":2}}`,
		`{"a":1,"\u0061":2}`,
		`{} {}`,
		`{"a":}`,
		string([]byte{'"', 0xff, '"'}),
		strings.Repeat("[", 66) + "0" + strings.Repeat("]", 66),
	} {
		if uniqueJSON([]byte(value)) == nil {
			t.Fatal("duplicate, invalid, or excessively nested JSON accepted")
		}
	}
	if err := uniqueJSON([]byte(`{"a":[1,{"b":true}],"c":null}`)); err != nil {
		t.Fatal("bounded unique JSON rejected")
	}
}
