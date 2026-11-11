// Package hostedcli executes dashboard-declared CLI actions on the server.
//
// Hosted execution is optional and disabled unless an operator enables it
// explicitly with a command-line flag. Requests resolve only declared
// dashboard actions; browsers never supply executable text. Each command runs
// with a fresh GitHub App installation token scoped to one repository and the
// minimum permissions for its command kind, and the token is revoked as soon
// as the subprocess exits.
package hostedcli

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/githubnext/gh-cao/server/internal/dashboardassets"
	"github.com/githubnext/gh-cao/server/internal/logger"
	"github.com/githubnext/gh-cao/server/internal/operational"
)

var catalogLog = logger.New("cao:hostedcli:catalog")

// MaxInputCharacters mirrors dashboard/cli-action-contract.mjs.
const MaxInputCharacters = 100000

var (
	templateToken  = regexp.MustCompile(`\{\{([a-z][a-z0-9]*(?:-[a-z0-9]+)*)\}\}`)
	actionID       = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)
	repositorySlug = regexp.MustCompile(`^[A-Za-z0-9_.][A-Za-z0-9_.-]*/[A-Za-z0-9_.][A-Za-z0-9_.-]*$`)
	safeToken      = regexp.MustCompile(`^[A-Za-z0-9_.][A-Za-z0-9_.:/@-]*$`)
	safeURL        = regexp.MustCompile(`^https://[A-Za-z0-9._~:/%+-]+$`)
)

var contextFields = map[string]bool{"cao-url": true, "cao-repository": true, "cao-repository-url": true}

// RequestError carries an HTTP status for a rejected request without echoing
// request content.
type RequestError struct {
	Status  int
	Message string
}

func (e *RequestError) Error() string { return e.Message }

func requestError(status int, message string) error {
	return &RequestError{Status: status, Message: message}
}

// Argument is one declared boolean flag of a CLI action.
type Argument struct {
	ID      string `json:"id"`
	Flag    string `json:"flag"`
	Default bool   `json:"default"`
}

// Action is one dashboard-declared CLI action.
type Action struct {
	ID        string     `json:"id"`
	Command   string     `json:"command"`
	CopyOnly  bool       `json:"copy-only"`
	Arguments []Argument `json:"arguments"`
}

// Catalog is the immutable set of declared actions loaded from dashboard.json.
type Catalog struct {
	actions map[string]Action
}

// LoadCatalog reads the declared CLI actions from a dashboard document.
func LoadCatalog(path string) (*Catalog, error) {
	// #nosec G304 -- the operator configures the dashboard document path.
	content, err := dashboardassets.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read dashboard CLI actions: %w", err)
	}
	return ParseCatalog(content)
}

// ParseCatalog parses the declared CLI actions from a dashboard document.
func ParseCatalog(content []byte) (*Catalog, error) {
	var document struct {
		Dashboard struct {
			Actions []Action `json:"cli-actions"`
		} `json:"dashboard"`
	}
	if err := json.Unmarshal(content, &document); err != nil {
		return nil, fmt.Errorf("parse dashboard CLI actions: %w", err)
	}
	catalog := &Catalog{actions: map[string]Action{}}
	for _, action := range document.Dashboard.Actions {
		if !actionID.MatchString(action.ID) || strings.TrimSpace(action.Command) == "" {
			return nil, errors.New("dashboard CLI action declaration is invalid")
		}
		if _, exists := catalog.actions[action.ID]; exists {
			return nil, fmt.Errorf("dashboard CLI action %q is declared twice", action.ID)
		}
		catalog.actions[action.ID] = action
	}
	catalogLog.Printf("loaded dashboard CLI actions count=%d", len(catalog.actions))
	return catalog, nil
}

// Request is the browser-supplied selection of a declared action.
type Request struct {
	ID             string            `json:"id"`
	Arguments      map[string]bool   `json:"arguments,omitempty"`
	Values         map[string]string `json:"values,omitempty"`
	Input          *string           `json:"input,omitempty"`
	IdempotencyKey string            `json:"idempotencyKey,omitempty"`
}

// TemplateContext is host-owned CAO identity rendered into declared commands.
type TemplateContext map[string]string

// NewTemplateContext validates and builds the host-owned template context.
func NewTemplateContext(repository, githubURLBase, caoURL string) (TemplateContext, error) {
	context := TemplateContext{}
	if repository != "" {
		if !repositorySlug.MatchString(repository) {
			return nil, errors.New("CAO repository must be an OWNER/REPOSITORY name")
		}
		if githubURLBase == "" {
			githubURLBase = "https://github.com"
		}
		base, err := normalizeURL(githubURLBase)
		if err != nil {
			return nil, err
		}
		context["cao-repository"] = repository
		context["cao-repository-url"] = base + repository
	}
	if caoURL != "" {
		normalized, err := normalizeURL(caoURL)
		if err != nil {
			return nil, err
		}
		context["cao-url"] = normalized
	}
	return context, nil
}

func normalizeURL(value string) (string, error) {
	invalid := errors.New("CAO URL must be an HTTPS URL without credentials, query, fragment, or command delimiters")
	if len(value) > 2048 || !safeURL.MatchString(value) {
		return "", invalid
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Host == "" {
		return "", invalid
	}
	parsed.Host = strings.ToLower(parsed.Host)
	return strings.TrimSuffix(parsed.String(), "/") + "/", nil
}

// Resolve renders a declared action into a queued invocation. It mirrors
// resolveQueuedCliAction in dashboard/cli-action-api.mjs.
func (c *Catalog) Resolve(request Request, context TemplateContext) (operational.CLICommandInvocation, error) {
	if !actionID.MatchString(request.ID) ||
		(request.Input != nil && (*request.Input == "" || utf8.RuneCountInString(*request.Input) > MaxInputCharacters)) {
		return operational.CLICommandInvocation{}, requestError(400, "Invalid CLI action request.")
	}
	action, ok := c.actions[request.ID]
	if !ok {
		return operational.CLICommandInvocation{}, requestError(404, "CLI action is not declared by this dashboard.")
	}
	if action.CopyOnly {
		return operational.CLICommandInvocation{}, requestError(403, "CLI action is copy-only.")
	}
	declared := map[string]bool{}
	for _, argument := range action.Arguments {
		declared[argument.ID] = true
	}
	for id := range request.Arguments {
		if !declared[id] {
			return operational.CLICommandInvocation{}, requestError(400, "Invalid CLI action arguments.")
		}
	}
	fields := map[string]bool{}
	for _, match := range templateToken.FindAllStringSubmatch(action.Command, -1) {
		if !contextFields[match[1]] {
			fields[match[1]] = true
		}
	}
	for field := range request.Values {
		if !fields[field] {
			return operational.CLICommandInvocation{}, requestError(400, "Invalid CLI action template values.")
		}
	}
	for field := range fields {
		if _, ok := request.Values[field]; !ok {
			return operational.CLICommandInvocation{}, requestError(400, "Invalid CLI action template values.")
		}
	}
	rendered, err := render(action.Command, request.Values, context)
	if err != nil {
		return operational.CLICommandInvocation{}, requestError(400, "Invalid CLI action template values.")
	}
	parts := []string{rendered}
	for _, argument := range action.Arguments {
		selected, provided := request.Arguments[argument.ID]
		if (provided && selected) || (!provided && argument.Default) {
			parts = append(parts, argument.Flag)
		}
	}
	command := strings.Join(parts, " ")
	if (request.Input != nil) != (command == "gh agent-task create --from-file -") {
		return operational.CLICommandInvocation{}, requestError(400, "CLI action input is not valid for this command.")
	}
	return operational.CLICommandInvocation{
		ActionID: action.ID, Command: command, Arguments: request.Arguments, Values: request.Values, Input: request.Input,
	}, nil
}

func render(command string, values map[string]string, context TemplateContext) (string, error) {
	var renderErr error
	rendered := templateToken.ReplaceAllStringFunc(command, func(token string) string {
		field := templateToken.FindStringSubmatch(token)[1]
		value := values[field]
		if contextFields[field] {
			value = context[field]
		}
		urlField := field == "cao-url" || field == "cao-repository-url"
		limit := 200
		if urlField {
			limit = 2048
		}
		pattern := safeToken
		switch {
		case urlField:
			pattern = safeURL
		case field == "repository" || field == "cao-repository":
			pattern = repositorySlug
		}
		if value == "" || len(value) > limit || !pattern.MatchString(value) {
			renderErr = fmt.Errorf("template value %q is invalid", field)
			return ""
		}
		if urlField {
			if _, err := normalizeURL(value); err != nil {
				renderErr = err
				return ""
			}
		}
		return value
	})
	return rendered, renderErr
}
