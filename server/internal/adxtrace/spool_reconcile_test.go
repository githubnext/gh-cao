package adxtrace

import (
	"os"
	"slices"
	"testing"
)

func listObjectKeys(t *testing.T, objects Objects) []string {
	t.Helper()
	entries, err := objects.List()
	if err != nil {
		t.Fatal(err)
	}
	keys := make([]string, 0, len(entries))
	for _, entry := range entries {
		keys = append(keys, entry.Key)
	}
	slices.Sort(keys)
	return keys
}

func TestReconcileObjects(t *testing.T) {
	state := func() *ledger {
		return &ledger{
			Records: map[string]*record{
				"a": {File: "kept.trace"},
				"b": {File: "withmining.trace", Mining: &miningProjection{File: "kept.mining"}},
				"c": {},
			},
			Datasets: map[string]*dataset{"d": {File: "kept.dataset"}},
		}
	}
	tests := []struct {
		name    string
		files   []string
		want    []string
		wantErr bool
	}{
		{name: "empty spool", want: nil},
		{
			name:  "referenced objects are kept",
			files: []string{"ledger.json", "lock", "kept.trace", "withmining.trace", "kept.mining", "kept.dataset"},
			want:  []string{"kept.dataset", "kept.mining", "kept.trace", "ledger.json", "lock", "withmining.trace"},
		},
		{
			name:  "temporary and orphan objects are removed",
			files: []string{"ledger.json", "kept.trace", "x.tmp", "ledger.json.tmp", "orphan.trace", "orphan.dataset", "orphan.mining"},
			want:  []string{"kept.trace", "ledger.json"},
		},
		{
			name:    "unexpected file is rejected",
			files:   []string{"ledger.json", "notes.txt"},
			wantErr: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := t.TempDir()
			root, err := os.OpenRoot(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = root.Close() }()
			objects := localObjects{root}
			for _, name := range test.files {
				if err := objects.Write(name, []byte("x")); err != nil {
					t.Fatal(err)
				}
			}
			err = reconcileObjects(objects, state())
			if (err != nil) != test.wantErr {
				t.Fatalf("reconcileObjects error = %v, want error %t", err, test.wantErr)
			}
			if test.wantErr {
				return
			}
			if got := listObjectKeys(t, objects); !slices.Equal(got, test.want) {
				t.Fatalf("remaining objects = %v, want %v", got, test.want)
			}
		})
	}
}

func TestReconcileObjectsListFailure(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	objects := localObjects{root}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	if err := reconcileObjects(objects, &ledger{}); err == nil {
		t.Fatalf("reconcileObjects on a closed root error = %v, want failure", err)
	}
}
