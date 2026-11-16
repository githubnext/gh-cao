package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/hostedcli"
)

func (a *App) repositorySelector(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if _, ok := a.authorizeHostedCLI(w, r); !ok {
		return
	}
	var input struct {
		ID   string `json:"id"`
		Text string `json:"text"`
	}
	if err := readHostedCLIJSON(r, &input); err != nil {
		writeHostedCLIError(r.Context(), w, err)
		return
	}
	input.Text = strings.TrimSpace(input.Text)
	if len(input.Text) > 200 || !hostedcli.ValidRepository(input.Text) {
		writeError(r.Context(), w, http.StatusBadRequest, "Repository search requires an organization followed by / and at least one repository character, without wildcards.")
		return
	}
	selector, err := a.hostedCLI.catalog.ResolveRepositorySelector(input.ID)
	if err != nil {
		writeHostedCLIError(r.Context(), w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	payload, err := json.Marshal(struct {
		Text  string `json:"text"`
		Limit int    `json:"limit"`
	}{input.Text, selector.Query.Limit})
	if err != nil {
		writeError(r.Context(), w, http.StatusBadRequest, "Invalid repository search.")
		return
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, a.operatorConnection().AdapterURL+"/repositories/search", bytes.NewReader(payload))
	if err != nil {
		writeError(r.Context(), w, http.StatusBadGateway, "Native repository lookup is unavailable.")
		return
	}
	request.Header.Set("Content-Type", "application/json")
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(request)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			writeError(r.Context(), w, http.StatusGatewayTimeout, "GitHub repository search timed out.")
		} else {
			writeError(r.Context(), w, http.StatusBadGateway, "GitHub repository search failed; check the local GitHub CLI authentication.")
		}
		return
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		status := http.StatusBadGateway
		if response.StatusCode == http.StatusGatewayTimeout {
			status = http.StatusGatewayTimeout
		}
		writeError(r.Context(), w, status, "GitHub repository search failed; check the local GitHub CLI authentication or retry.")
		return
	}
	var rows []map[string]any
	decoder := json.NewDecoder(http.MaxBytesReader(nil, response.Body, 1<<20))
	if err := decoder.Decode(&rows); err != nil || rows == nil {
		writeError(r.Context(), w, http.StatusBadGateway, "GitHub repository search returned an invalid response.")
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		writeError(r.Context(), w, http.StatusBadGateway, "GitHub repository search returned an invalid response.")
		return
	}
	options, err := selector.Options(rows)
	if err != nil {
		writeError(r.Context(), w, http.StatusBadGateway, "GitHub repository search returned invalid options.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"options": options})
}
