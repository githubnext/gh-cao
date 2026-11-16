package cli

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/internal/ghcli"
)

// Only this native adapter holds the CLI identity. Lookup does not enqueue a
// command or require confirmation; enrollment requires dashboard confirmation.
func dashboardRepositorySearch(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Text  string `json:"text"`
		Limit int    `json:"limit"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		http.Error(w, "invalid repository lookup", http.StatusBadRequest)
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		http.Error(w, "invalid repository lookup", http.StatusBadRequest)
		return
	}
	input.Text = strings.TrimSpace(input.Text)
	owner, query, qualified := strings.Cut(input.Text, "/")
	if !qualified || query == "" || !nativeOwner.MatchString(owner) || !nativeSetupRepositoryPattern.MatchString(input.Text) ||
		len(input.Text) > 200 || strings.Contains(input.Text, "*") || input.Limit < 1 || input.Limit > 50 {
		http.Error(w, "invalid repository lookup", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	args := []string{"search", "repos",
		"--json", "fullName,description,name,owner,url,visibility,isArchived,isPrivate",
		"--limit", strconv.Itoa(input.Limit), "--owner", owner}
	output, err := ghcli.ExecBounded(ctx, 1<<20, append(args, "--", query)...)
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			status = http.StatusGatewayTimeout
		}
		http.Error(w, "GitHub repository search failed", status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, output)
}
