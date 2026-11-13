package namespaces

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNormalizeNamespace(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{input: "team-one", expected: "cao:team-one"},
		{input: " CAO:Team_One ", expected: "cao:team_one"},
		{input: "9", expected: "cao:9"},
	}
	for _, test := range tests {
		actual, err := NormalizeNamespace(test.input)
		if err != nil {
			t.Fatalf("NormalizeNamespace(%q): %v", test.input, err)
		}
		if actual != test.expected {
			t.Fatalf("NormalizeNamespace(%q) = %q, want %q", test.input, actual, test.expected)
		}
	}
	for _, input := range []string{"", "cao:", "team*", "team?", "team[1]", "team:name", "team.name", strings.Repeat("a", 65)} {
		if _, err := NormalizeNamespace(input); err == nil {
			t.Errorf("NormalizeNamespace(%q) accepted an ambiguous namespace", input)
		}
	}
}

func TestDefaultNamespaceIsStablePerCheckout(t *testing.T) {
	checkout := t.TempDir()
	if err := os.WriteFile(filepath.Join(checkout, ".git"), []byte("gitdir: test"), 0o600); err != nil {
		t.Fatal(err)
	}
	serverDirectory := filepath.Join(checkout, "server", "internal")
	if err := os.MkdirAll(serverDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	fromRoot, err := DefaultNamespace(checkout)
	if err != nil {
		t.Fatal(err)
	}
	fromServer, err := DefaultNamespace(serverDirectory)
	if err != nil {
		t.Fatal(err)
	}
	if fromRoot != fromServer {
		t.Fatalf("checkout namespace changed by working directory: %q != %q", fromRoot, fromServer)
	}
	other, err := DefaultNamespace(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if other == fromRoot {
		t.Fatalf("different checkout paths shared namespace %q", fromRoot)
	}
	if !strings.HasPrefix(fromRoot, namespacePrefix+"checkout-") || len(fromRoot) > 64 {
		t.Fatalf("unexpected default namespace %q", fromRoot)
	}
}

func TestCheckoutRootFindsGitMarker(t *testing.T) {
	checkout := t.TempDir()
	if err := os.WriteFile(filepath.Join(checkout, ".git"), []byte("gitdir: test"), 0o600); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(checkout, "server", "internal")
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatal(err)
	}

	root, source := checkoutRoot(nested)
	if root != checkout {
		t.Fatalf("checkoutRoot(%q) root = %q, want %q", nested, root, checkout)
	}
	if source != checkoutRootSourceGitMarker {
		t.Fatalf("checkoutRoot(%q) source = %q, want %q", nested, source, checkoutRootSourceGitMarker)
	}
}

func TestCheckoutRootFallsBackWithoutGitMarker(t *testing.T) {
	directory := t.TempDir()

	root, source := checkoutRoot(directory)
	if root != directory {
		t.Fatalf("checkoutRoot(%q) root = %q, want the original directory", directory, root)
	}
	if source != checkoutRootSourceFallback {
		t.Fatalf("checkoutRoot(%q) source = %q, want %q", directory, source, checkoutRootSourceFallback)
	}
}

func TestEnvironmentPreservesNamespaceAndRejectsLegacySelectors(t *testing.T) {
	for _, test := range []struct {
		name   string
		values map[string]string
		want   string
		err    string
	}{
		{"unset", nil, "", ""},
		{"canonical identity", map[string]string{"CAO_DATABASE_NAMESPACE": " cao:existing "}, "cao:existing", ""},
		{"unprefixed canonical identity", map[string]string{"CAO_DATABASE_NAMESPACE": "Existing.Dataset"}, "Existing.Dataset", ""},
		{"legacy hosted", map[string]string{"REDIS_NAMESPACE": "existing"}, "", "REDIS_NAMESPACE"},
		{"legacy CLI", map[string]string{"CAO_REDIS_NAMESPACE": "existing"}, "", "CAO_REDIS_NAMESPACE"},
		{"conflicting selectors", map[string]string{"CAO_DATABASE_NAMESPACE": "new", "REDIS_NAMESPACE": "old"}, "", "REDIS_NAMESPACE"},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := Environment(func(key string) (string, bool) {
				value, exists := test.values[key]
				return value, exists
			})
			if test.err != "" {
				if err == nil || !strings.Contains(err.Error(), test.err) {
					t.Fatalf("Environment() = %q, %v; want rejection of %s", got, err, test.err)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("Environment() = %q, %v; want %q", got, err, test.want)
			}
		})
	}
}
