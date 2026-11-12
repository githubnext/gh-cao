package simulator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// BootstrapAPI is a strict, stateful, test-only GitHub surface for installation.
// Unlike the traffic simulator, unknown repositories and routes return 404.
// Assets are supplied by the caller; it never fetches releases from GitHub.
type BootstrapAPI struct {
	mu           sync.Mutex
	Repositories map[string]*BootstrapRepository
	Assets       map[string][]byte
	Release      string
	Git          http.Handler
	Requests     []string
	Unhandled    []string
	ServerURL    string
}

type BootstrapRepository struct {
	ID                    int64
	Owner                 string
	Name                  string
	Private               bool
	Internal              bool
	ServerURL             string
	Secrets               []string
	Variables             map[string]string
	OrganizationVariables map[string]string
}

func NewBootstrapAPI(release string, assets map[string][]byte) *BootstrapAPI {
	return &BootstrapAPI{
		Repositories: map[string]*BootstrapRepository{},
		Assets:       assets,
		Release:      release,
	}
}

var bootstrapName = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`)

func (a *BootstrapAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	path := strings.TrimPrefix(r.URL.Path, "/api/v3")
	operation := r.Method + " " + path
	a.Requests = append(a.Requests, operation)
	setRateHeaders(w, successRate(APIWindow{}, nil))
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-OAuth-Scopes", "repo, read:org, gist, workflow")
	write := func(status int, value any) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(value)
	}
	segments := strings.Split(strings.Trim(path, "/"), "/")
	if len(segments) >= 3 && strings.HasSuffix(segments[1], ".git") && a.Git != nil {
		key := segments[0] + "/" + strings.TrimSuffix(segments[1], ".git")
		if a.Repositories[key] == nil {
			writeNotFound(w)
			return
		}
		// CGI requires a content length, while Git streams larger packs with
		// chunked encoding. Bound the local fixture before handing it off.
		if r.ContentLength < 0 {
			body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<20))
			if err != nil {
				write(http.StatusRequestEntityTooLarge, map[string]string{"message": "Simulator Git pack exceeds limit"})
				return
			}
			r = r.Clone(r.Context())
			r.Body = io.NopCloser(bytes.NewReader(body))
			r.ContentLength = int64(len(body))
			r.TransferEncoding = nil
		}
		w.Header().Del("Content-Type")
		a.Git.ServeHTTP(w, r)
		return
	}
	switch {
	case r.Method == http.MethodGet && path == "/users/platform":
		write(http.StatusOK, map[string]any{"login": "platform", "id": 2, "node_id": "O_platform", "type": "Organization"})
		return
	case r.Method == http.MethodGet && path == "/user":
		write(http.StatusOK, map[string]any{"login": "simulator", "id": 1})
		return
	case r.Method == http.MethodGet && path == "/":
		write(http.StatusOK, map[string]any{})
		return
	case r.Method == http.MethodPost && (path == "/graphql" || path == "/api/graphql"):
		a.graphql(w, r)
		return
	case r.Method == http.MethodGet && path == "/repos/githubnext/gh-cao/releases":
		write(http.StatusOK, []any{a.release()})
		return
	case r.Method == http.MethodGet && path == "/repos/githubnext/gh-cao/releases/tags/"+a.Release:
		write(http.StatusOK, a.release())
		return
	case r.Method == http.MethodGet && strings.HasPrefix(path, "/githubnext/gh-cao/releases/download/"+a.Release+"/"):
		name := strings.TrimPrefix(path, "/githubnext/gh-cao/releases/download/"+a.Release+"/")
		if asset, ok := a.Assets[name]; ok {
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write(asset) // #nosec G705 -- test-owned release bytes served as application/octet-stream, not HTML; escaping would corrupt assets.
			return
		}
	}
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if r.Method == http.MethodPost && (path == "/user/repos" || len(parts) == 3 && parts[0] == "orgs" && parts[2] == "repos") {
		var input struct {
			Name    string `json:"name"`
			Private bool   `json:"private"`
		}
		if json.NewDecoder(r.Body).Decode(&input) != nil {
			write(http.StatusBadRequest, map[string]string{"message": "Invalid repository"})
			return
		}
		owner := "simulator"
		if parts[0] == "orgs" {
			owner = parts[1]
		}
		repo, err := a.create(owner, input.Name, input.Private)
		if err != nil {
			write(http.StatusUnprocessableEntity, map[string]string{"message": err.Error()})
			return
		}
		write(http.StatusCreated, repo.rest())
		return
	}
	if len(parts) >= 3 && parts[0] == "repos" {
		repo := a.Repositories[parts[1]+"/"+parts[2]]
		if repo == nil {
			writeNotFound(w)
			return
		}
		if len(parts) == 3 && r.Method == http.MethodGet {
			write(http.StatusOK, repo.rest())
			return
		}
		if len(parts) == 3 && r.Method == http.MethodPatch {
			var input struct {
				Private *bool `json:"private"`
			}
			if json.NewDecoder(r.Body).Decode(&input) != nil || input.Private == nil {
				write(http.StatusBadRequest, map[string]string{"message": "Expected explicit repository visibility"})
				return
			}
			repo.Private = *input.Private
			write(http.StatusOK, repo.rest())
			return
		}
		if len(parts) == 5 && parts[3] == "actions" && parts[4] == "secrets" && r.Method == http.MethodGet {
			secrets := make([]map[string]string, 0, len(repo.Secrets))
			for _, name := range repo.Secrets {
				secrets = append(secrets, map[string]string{"name": name})
			}
			write(http.StatusOK, map[string]any{"total_count": len(secrets), "secrets": secrets})
			return
		}
		if len(parts) >= 5 && parts[3] == "actions" && (parts[4] == "variables" ||
			parts[4] == "organization-variables" && len(parts) == 5 && r.Method == http.MethodGet) {
			if len(parts) == 5 && r.Method == http.MethodGet {
				source := repo.Variables
				if parts[4] == "organization-variables" {
					source = repo.OrganizationVariables
				}
				names := make([]string, 0, len(source))
				for name := range source {
					names = append(names, name)
				}
				sort.Strings(names)
				variables := make([]map[string]string, 0, len(names))
				for _, name := range names {
					variables = append(variables, map[string]string{"name": name, "value": source[name]})
				}
				write(http.StatusOK, map[string]any{"total_count": len(variables), "variables": variables})
				return
			}
			if len(parts) == 5 && r.Method == http.MethodPost {
				var input struct{ Name, Value string }
				if json.NewDecoder(r.Body).Decode(&input) != nil || !bootstrapName.MatchString(input.Name) {
					write(http.StatusBadRequest, map[string]string{"message": "Invalid variable"})
					return
				}
				if _, exists := repo.Variables[input.Name]; exists {
					write(http.StatusConflict, map[string]string{"message": "Variable already exists"})
					return
				}
				repo.Variables[input.Name] = input.Value
				write(http.StatusCreated, map[string]any{})
				return
			}
			if len(parts) == 6 {
				value, exists := repo.Variables[parts[5]]
				if !exists {
					writeNotFound(w)
					return
				}
				switch r.Method {
				case http.MethodGet:
					write(http.StatusOK, map[string]string{"name": parts[5], "value": value})
					return
				case http.MethodPatch:
					var input struct{ Value string }
					if json.NewDecoder(r.Body).Decode(&input) != nil {
						write(http.StatusBadRequest, map[string]string{"message": "Invalid variable"})
						return
					}
					repo.Variables[parts[5]] = input.Value
					w.WriteHeader(http.StatusNoContent)
					return
				}
			}
		}
	}
	a.Unhandled = append(a.Unhandled, operation)
	writeNotFound(w)
}

func (a *BootstrapAPI) create(owner, name string, private bool) (*BootstrapRepository, error) {
	if !bootstrapName.MatchString(owner) || !bootstrapName.MatchString(name) {
		return nil, fmt.Errorf("invalid repository name")
	}
	key := owner + "/" + name
	if a.Repositories[key] != nil {
		return nil, fmt.Errorf("repository already exists")
	}
	repo := &BootstrapRepository{
		ID: int64(len(a.Repositories) + 1), Owner: owner, Name: name,
		Private: private, Variables: map[string]string{}, ServerURL: a.ServerURL,
	}
	a.Repositories[key] = repo
	return repo, nil
}

func (r *BootstrapRepository) serverURL() string {
	if r.ServerURL != "" {
		return r.ServerURL
	}
	return "https://github.com"
}

func (r *BootstrapRepository) rest() map[string]any {
	visibility := "public"
	if r.Private {
		visibility = "private"
	}
	if r.Internal {
		visibility = "internal"
	}
	return map[string]any{
		"id": r.ID, "node_id": fmt.Sprintf("R_%d", r.ID),
		"name": r.Name, "full_name": r.Owner + "/" + r.Name, "private": r.Private,
		"visibility": visibility, "default_branch": "main",
		"owner":     map[string]any{"login": r.Owner, "id": 1, "type": "Organization"},
		"html_url":  r.serverURL() + "/" + r.Owner + "/" + r.Name,
		"clone_url": r.serverURL() + "/" + r.Owner + "/" + r.Name + ".git",
	}
}

func (r *BootstrapRepository) graphql() map[string]any {
	visibility := "PUBLIC"
	if r.Private {
		visibility = "PRIVATE"
	}
	if r.Internal {
		visibility = "INTERNAL"
	}
	return map[string]any{
		"id": fmt.Sprintf("R_%d", r.ID), "databaseId": r.ID, "name": r.Name,
		"nameWithOwner": r.Owner + "/" + r.Name, "visibility": visibility, "isPrivate": r.Private,
		"owner":            map[string]string{"login": r.Owner, "id": "O_" + r.Owner},
		"url":              r.serverURL() + "/" + r.Owner + "/" + r.Name,
		"sshUrl":           "git@github.com:" + r.Owner + "/" + r.Name + ".git",
		"defaultBranchRef": map[string]string{"name": "main"},
		"viewerPermission": "ADMIN", "hasIssuesEnabled": true, "hasWikiEnabled": true,
		"description": "", "parent": nil,
		"mergeCommitAllowed": true, "rebaseMergeAllowed": true, "squashMergeAllowed": true,
	}
}

func (a *BootstrapAPI) graphql(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Query     string         `json:"query"`
		Variables map[string]any `json:"variables"`
	}
	if json.NewDecoder(r.Body).Decode(&input) != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	fields, err := bootstrapFields(input.Query)
	if err != nil {
		a.Unhandled = append(a.Unhandled, "POST /graphql (unsupported selection)")
		_ = json.NewEncoder(w).Encode(map[string]any{"errors": []any{map[string]string{"message": err.Error()}}})
		return
	}
	data := map[string]any{}
	switch {
	case strings.Contains(input.Query, "createRepository"):
		shape := map[string]any{"createRepository": map[string]any{"repository": (&BootstrapRepository{}).graphql()}}
		if _, err := bootstrapProjection(shape, fields); err != nil {
			a.Unhandled = append(a.Unhandled, "POST /graphql (unsupported field)")
			_ = json.NewEncoder(w).Encode(map[string]any{"errors": []any{map[string]string{"message": err.Error()}}})
			return
		}
		fields, _ := input.Variables["input"].(map[string]any)
		name, _ := fields["name"].(string)
		owner := "simulator"
		switch fields["ownerId"] {
		case "O_platform":
			owner = "platform"
		case nil, "U_simulator":
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"errors": []any{map[string]string{"message": "Unknown repository owner"}}})
			return
		}
		repo, err := a.create(owner, name, fields["visibility"] == "PRIVATE")
		if err != nil {
			_ = json.NewEncoder(w).Encode(map[string]any{"errors": []any{map[string]string{"message": err.Error()}}})
			return
		}
		data["createRepository"] = map[string]any{"repository": repo.graphql()}
	case strings.Contains(input.Query, "repository("):
		owner := bootstrapArgument(input.Query, "owner", input.Variables)
		name := bootstrapArgument(input.Query, "name", input.Variables)
		repo := a.Repositories[owner+"/"+name]
		if repo == nil {
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"repository": nil}, "errors": []any{
				map[string]string{"message": "Could not resolve to a Repository", "type": "NOT_FOUND"},
			}})
			return
		}
		data["repository"] = repo.graphql()
	case strings.Contains(input.Query, "repositoryOwner("):
		owner := bootstrapArgument(input.Query, "login", input.Variables)
		nodes := []any{}
		keys := make([]string, 0, len(a.Repositories))
		for key := range a.Repositories {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			repo := a.Repositories[key]
			if strings.EqualFold(repo.Owner, owner) {
				nodes = append(nodes, repo.graphql())
			}
		}
		data["repositoryOwner"] = map[string]any{
			"login": owner,
			"repositories": map[string]any{
				"nodes": nodes, "totalCount": len(nodes),
				"pageInfo": map[string]any{"hasNextPage": false, "endCursor": nil},
			},
		}
	case strings.Contains(input.Query, "organization("):
		data["organization"] = map[string]string{"id": "O_platform", "login": "platform"}
	case strings.Contains(input.Query, "user("):
		data["user"] = map[string]string{"id": "U_simulator", "login": "simulator"}
	case strings.Contains(input.Query, "viewer"):
		data["viewer"] = map[string]any{
			"login": "simulator", "id": "U_simulator",
			"organizations": map[string]any{
				"nodes":    []any{map[string]any{"login": "platform", "viewerCanCreateRepositories": true}},
				"pageInfo": map[string]any{"hasNextPage": false, "endCursor": nil},
			},
		}
	default:
		a.Unhandled = append(a.Unhandled, "POST /graphql (unsupported query)")
		_ = json.NewEncoder(w).Encode(map[string]any{"errors": []any{map[string]string{"message": "Unsupported simulator query"}}})
		return
	}
	projected, err := bootstrapProjection(data, fields)
	if err != nil {
		a.Unhandled = append(a.Unhandled, "POST /graphql (unsupported field)")
		_ = json.NewEncoder(w).Encode(map[string]any{"errors": []any{map[string]string{"message": err.Error()}}})
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"data": projected})
}

func (a *BootstrapAPI) release() map[string]any {
	assets := make([]any, 0, len(a.Assets))
	for name, content := range a.Assets {
		assets = append(assets, map[string]any{
			"name": name, "size": len(content), "state": "uploaded",
			"browser_download_url": "https://github.com/githubnext/gh-cao/releases/download/" + a.Release + "/" + name,
		})
	}
	return map[string]any{
		"tag_name": a.Release, "draft": false, "prerelease": false, "assets": assets,
		"published_at": time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).Format(time.RFC3339),
	}
}
