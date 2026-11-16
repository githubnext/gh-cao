package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/hostedcli"
)

func (a *App) operatorAllows(ctx context.Context, selected *LocalOperatorActionsConfig, plan hostedcli.Plan) (bool, error) {
	if !hostedcli.LocalRepositoryOperationsAllowed(plan.Repository) {
		return false, nil
	}
	if selected == nil {
		return false, nil
	}
	connection := a.operatorConnection()
	publishing := plan.Kind == hostedcli.KindGhAwUpdate || plan.Kind == hostedcli.KindGhAwUpgrade
	if connection == nil || !connection.ManagedUpdates || !publishing {
		return selected.allows(plan.Repository), nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	owner, repository, _ := strings.Cut(plan.Repository, "/")
	endpoint := connection.AdapterURL + "/repositories/" + url.PathEscape(owner) + "/" + url.PathEscape(repository) + "/update-access"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return false, cliFailure(http.StatusServiceUnavailable, "The local CAO managed update scope is unavailable.")
	}
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		return false, cliFailure(http.StatusServiceUnavailable, "The local CAO managed update scope could not be verified.")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return false, cliFailure(http.StatusServiceUnavailable, "The local CAO configuration is unavailable or invalid; no command was started.")
	}
	var result struct {
		Allowed *bool `json:"allowed"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(nil, response.Body, 8192))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil || result.Allowed == nil {
		return false, cliFailure(http.StatusServiceUnavailable, "The native managed update scope response is invalid.")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return false, cliFailure(http.StatusServiceUnavailable, "The native managed update scope response is invalid.")
	}
	return *result.Allowed, nil
}
