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
	"unicode"
	"unicode/utf8"

	"github.com/githubnext/gh-cao/internal/ghcli"
)

// Only this native adapter holds the CLI identity. Lookup does not enqueue a
// command or require terminal approval; enrollment still does.
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
	if !utf8.ValidString(input.Text) || utf8.RuneCountInString(input.Text) < 2 || utf8.RuneCountInString(input.Text) > 200 ||
		strings.Contains(input.Text, "*") || strings.ContainsFunc(input.Text, unicode.IsControl) || input.Limit < 1 || input.Limit > 50 {
		http.Error(w, "invalid repository lookup", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	args := []string{"search", "repos",
		"--json", "fullName,description,name,owner,url,visibility,isArchived,isPrivate",
		"--limit", strconv.Itoa(input.Limit)}
	query := input.Text
	if owner, name, qualified := strings.Cut(input.Text, "/"); qualified && name != "" &&
		nativeOwner.MatchString(owner) && nativeSetupRepositoryPattern.MatchString(input.Text) {
		args = append(args, "--owner", owner)
		query = name
	}
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
