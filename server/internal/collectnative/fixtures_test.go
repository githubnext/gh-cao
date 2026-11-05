package collectnative

import (
	"os"
	"testing"
)

func readSessionFixture(t *testing.T, name string) []byte {
	t.Helper()
	root, err := os.OpenRoot("../../../dashboard/site/test/fixtures")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	content, err := root.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return content
}
