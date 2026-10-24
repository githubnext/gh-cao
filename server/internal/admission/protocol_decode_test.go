package admission

import (
	"errors"
	"strings"
	"testing"
)

func TestHexQuad(t *testing.T) {
	for _, tc := range []struct {
		name  string
		data  string
		start int
		want  uint16
		ok    bool
	}{
		{"lower", "d83d", 0, 0xd83d, true},
		{"upper", "00FF", 0, 0x00ff, true},
		{"offset", "xx12aB", 2, 0x12ab, true},
		{"short", "12a", 0, 0, false},
		{"non-hex", "12g4", 0, 0, false},
		{"negative start", "1234", -1, 0, false},
		{"past end", "1234", 1, 0, false},
	} {
		got, ok := hexQuad([]byte(tc.data), tc.start)
		if got != tc.want || ok != tc.ok {
			t.Errorf("%s: got (%#x, %t), want (%#x, %t)", tc.name, got, ok, tc.want, tc.ok)
		}
	}
}

func TestScalarEscapes(t *testing.T) {
	for data, want := range map[string]bool{
		`"plain"`:        true,
		`"\n\"\\"`:       true,
		`"\u00e9"`:       true,
		`"\ud83d\ude00"`: true,
		`"\ud83d"`:       false,
		`"\ud83dx"`:      false,
		`"\ude00"`:       false,
		`"\ud83d\u0041"`: false,
		`"\u12"`:         false,
		`"\uzzzz"`:       false,
		`"trailing\`:     false,
	} {
		if got := scalarEscapes([]byte(data)); got != want {
			t.Errorf("scalarEscapes(%s) = %t, want %t", data, got, want)
		}
	}
}

func validRequestJSON() string {
	hash := strings.Repeat("a", 64) + ":" + strings.Repeat("b", 64)
	return `{"repo":"octo/app","workflow_id":"octo/app/.github/workflows/w.yml@refs/heads/main",` +
		`"workflow_hash":"` + hash + `","workflow_call_id":"7-1:octo/app/.github/workflows/w.yml@refs/heads/main",` +
		`"run_id":"7","run_attempt":"1","ref":"refs/heads/main","event_type":"push",` +
		`"actor":"octocat","triggering_actor":"octocat"}`
}

func TestDecodeRequest(t *testing.T) {
	req, err := decodeRequest([]byte(validRequestJSON()))
	if err != nil {
		t.Fatalf("valid request rejected: %v", err)
	}
	if req.Repo != "octo/app" || req.RunID != "7" || req.RunAttempt != "1" {
		t.Fatalf("unexpected request: %+v", req)
	}
	for name, data := range map[string]string{
		"empty":          ``,
		"missing field":  strings.Replace(validRequestJSON(), `"actor":"octocat",`, "", 1),
		"extra field":    strings.TrimSuffix(validRequestJSON(), "}") + `,"extra":"x"}`,
		"wrong type":     strings.Replace(validRequestJSON(), `"run_id":"7"`, `"run_id":7`, 1),
		"bad value":      strings.Replace(validRequestJSON(), `"repo":"octo/app"`, `"repo":"nope"`, 1),
		"call mismatch":  strings.Replace(validRequestJSON(), `"run_attempt":"1"`, `"run_attempt":"2"`, 1),
		"duplicate name": strings.Replace(validRequestJSON(), `"actor":"octocat",`, `"actor":"octocat","actor":"x",`, 1),
	} {
		if _, err := decodeRequest([]byte(data)); !errors.Is(err, ErrDenied) {
			t.Errorf("%s: err = %v, want ErrDenied", name, err)
		}
	}
}
