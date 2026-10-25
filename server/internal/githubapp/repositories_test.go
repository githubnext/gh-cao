package githubapp

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/go-github/v66/github"
)

func TestRepositoryEnumerationPreservesIdentityAndFailsClosed(t *testing.T) {
	for _, body := range []string{
		`{"repositories":[{"id":42,"full_name":"octo/new","archived":false}]}`,
		`{"repositories":[{"full_name":"octo/new"}]}`,
		`{"repositories":[{"id":42}]}`,
	} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprint(w, body)
			}))
			defer server.Close()
			base, err := url.Parse(server.URL + "/")
			if err != nil {
				t.Fatal(err)
			}
			transport := github.NewClient(server.Client())
			transport.BaseURL = base
			client := &Client{installations: map[int64]*installationEntry{7: {client: transport}}}
			repositories, _, _, err := client.ListRepositoriesPage(t.Context(), 7, 1)
			if body == `{"repositories":[{"id":42,"full_name":"octo/new","archived":false}]}` {
				if err != nil || len(repositories) != 1 || repositories[0].ID != 42 || repositories[0].FullName != "octo/new" {
					t.Fatalf("immutable repository identity lost: %+v, %v", repositories, err)
				}
			} else if err == nil {
				t.Fatal("incomplete inventory could be interpreted as repository removal")
			}
		})
	}
}

func TestRepositoryListBackfillExcludesSuspendedInstallations(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page") == "2" {
			_, _ = fmt.Fprint(w, `[{"id":9,"account":{"login":"octo"},"suspended_at":null}]`)
			return
		}
		w.Header().Set("Link", "<"+serverURL(r)+"/app/installations?page=2>; rel=\"next\"")
		_, _ = fmt.Fprint(w, `[{"id":7,"account":{"login":"octo"},"suspended_at":null},
			{"id":8,"account":{"login":"octo"},"suspended_at":"2026-10-24T00:00:00Z"}]`)
	}))
	defer server.Close()
	base, err := url.Parse(server.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	transport := github.NewClient(server.Client())
	transport.BaseURL = base
	client := &Client{appClient: transport}
	installations, err := client.ListInstallations(t.Context())
	if err != nil || len(installations) != 2 || installations[0].ID != 7 || installations[1].ID != 9 {
		t.Fatalf("active installation inventory = %+v, %v", installations, err)
	}
}
