package collect

import (
	"reflect"
	"strings"
	"testing"

	"github.com/githubnext/gh-cao/server/internal/githubapp"
)

func TestActiveRepositoriesSkipsArchivedAndNormalizes(t *testing.T) {
	names, ids, err := activeRepositories([]githubapp.Repository{
		{ID: 1, FullName: "Octo/API"},
		{ID: 2, FullName: "octo/old", Archived: true},
		{ID: 3, FullName: " octo/Web "},
	})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"octo/api", "octo/web"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("names = %v, want %v", names, want)
	}
	if want := []int64{1, 3}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("ids = %v, want %v", ids, want)
	}
}

func TestActiveRepositoriesRejectsInvalidName(t *testing.T) {
	names, ids, err := activeRepositories([]githubapp.Repository{
		{ID: 1, FullName: "octo/api"},
		{ID: 2, FullName: "not-a-repository"},
	})
	if err == nil || !strings.Contains(err.Error(), "normalize discovered repository") {
		t.Fatalf("err = %v, want normalization failure", err)
	}
	if names != nil || ids != nil {
		t.Fatalf("partial results returned: %v %v", names, ids)
	}
}

func TestActiveRepositoriesEmpty(t *testing.T) {
	names, ids, err := activeRepositories(nil)
	if err != nil || len(names) != 0 || len(ids) != 0 {
		t.Fatalf("got %v %v %v, want empty", names, ids, err)
	}
}

func TestDiffRepositories(t *testing.T) {
	for _, tc := range []struct {
		name              string
		previous, current []string
		wantRemoved       []string
		wantPrevious      []string
	}{
		{"none previous", nil, []string{"octo/api"}, nil, nil},
		{"unchanged", []string{"octo/api"}, []string{"octo/api"}, nil, []string{"octo/api"}},
		{"removed keeps previous order", []string{"octo/b", "octo/a", "octo/c"}, []string{"octo/a"},
			[]string{"octo/b", "octo/c"}, []string{"octo/a", "octo/b", "octo/c"}},
		{"added only", []string{"octo/a"}, []string{"octo/a", "octo/new"}, nil, []string{"octo/a"}},
		{"all removed", []string{"octo/a"}, nil, []string{"octo/a"}, []string{"octo/a"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			removed, previousSet := diffRepositories(tc.previous, tc.current)
			if !reflect.DeepEqual(removed, tc.wantRemoved) {
				t.Fatalf("removed = %v, want %v", removed, tc.wantRemoved)
			}
			if len(previousSet) != len(tc.wantPrevious) {
				t.Fatalf("previous set = %v, want %v", previousSet, tc.wantPrevious)
			}
			for _, name := range tc.wantPrevious {
				if !previousSet[name] {
					t.Fatalf("previous set missing %q: %v", name, previousSet)
				}
			}
		})
	}
}
