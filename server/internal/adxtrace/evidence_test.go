package adxtrace

import (
	"bytes"
	"strings"
	"testing"
)

func TestSplitContent(t *testing.T) {
	if got := splitContent(nil, 4); len(got) != 0 {
		t.Fatalf("empty content produced %d parts", len(got))
	}
	got := splitContent([]byte("abcdefghij"), 4)
	want := []string{"abcd", "efgh", "ij"}
	if len(got) != len(want) {
		t.Fatalf("parts = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if string(got[i]) != want[i] {
			t.Fatalf("part %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestSplitContentKeepsRunesWhole(t *testing.T) {
	content := []byte(strings.Repeat("é", 10))
	parts := splitContent(content, 5)
	if !bytes.Equal(bytes.Join(parts, nil), content) {
		t.Fatal("parts do not reassemble the content")
	}
	for i, part := range parts {
		if len(part) > 5 || !strings.HasSuffix(string(part), "é") || strings.ContainsRune(string(part), '\uFFFD') {
			t.Fatalf("part %d = %q splits a rune or exceeds the limit", i, part)
		}
	}
}

func TestRedactionPatterns(t *testing.T) {
	builtIn, err := redactionPatterns(nil)
	if err != nil || len(builtIn) != len(builtInSecrets) {
		t.Fatalf("built-in patterns = %d, err = %v", len(builtIn), err)
	}
	extended, err := redactionPatterns([]string{`internal-[0-9]+`})
	if err != nil {
		t.Fatal(err)
	}
	if len(extended) != len(builtInSecrets)+1 {
		t.Fatalf("patterns = %d, want %d", len(extended), len(builtInSecrets)+1)
	}
	if len(builtInSecrets) != len(builtIn) {
		t.Fatal("built-in secret set was mutated")
	}
	if !extended[len(extended)-1].MatchString("internal-42") {
		t.Fatal("policy pattern was not appended")
	}
	if _, err := redactionPatterns([]string{"("}); err == nil || err.Error() != "invalid trace redaction policy" {
		t.Fatalf("invalid pattern error = %v", err)
	}
}
