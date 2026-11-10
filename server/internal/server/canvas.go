package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"regexp"
	"strings"
)

// LocalCanvasConfig connects the local SQLite host to its owning Copilot
// process. The adapter only owns integration callbacks, never data or assets.
type LocalCanvasConfig struct {
	Prefix          string                      `json:"prefix"`
	AdapterURL      string                      `json:"adapterUrl"`
	MemoryDirectory string                      `json:"memoryDirectory,omitempty"`
	OperatorActions *LocalOperatorActionsConfig `json:"operatorActions,omitempty"`
}

type localCanvasContextKey struct{}

var canvasPrefix = regexp.MustCompile(`^/[a-f0-9]{48}$`)

func validateLocalCanvas(config Config, localSQLite bool) error {
	if config.Canvas == nil {
		return nil
	}
	if !localSQLite || config.GitHubOAuth != nil || config.CertFile != "" || config.KeyFile != "" {
		return errors.New("canvas integration requires local SQLite loopback HTTP serving")
	}
	host, _, err := net.SplitHostPort(config.Listen)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return errors.New("canvas integration requires a numeric loopback listener")
	}
	adapter, err := url.Parse(config.Canvas.AdapterURL)
	if err != nil || adapter.Scheme != "http" || adapter.User != nil ||
		adapter.RawQuery != "" || adapter.Fragment != "" ||
		!canvasPrefix.MatchString(adapter.Path) || !canvasPrefix.MatchString(config.Canvas.Prefix) {
		return errors.New("invalid local canvas adapter configuration")
	}
	ip := net.ParseIP(adapter.Hostname())
	if ip == nil || !ip.IsLoopback() || adapter.Port() == "" {
		return errors.New("canvas adapter must use a numeric loopback HTTP address")
	}
	return nil
}

func canvasAdapterPath(path string) bool {
	switch path {
	case "/__dashboard_socket", "/__query_designer", "/__query_designer/enhance",
		"/__custom_views", "/__dialog_selector", "/__copilot_session":
		return true
	}
	return strings.HasPrefix(path, "/__cli_") || strings.HasPrefix(path, "/api/v1/github/")
}

func canvasDataAPI(path string) bool {
	switch path {
	case "/api/v1/query", "/api/v1/refresh", "/api/v1/diagnostics", "/api/v1/events":
		return true
	}
	return strings.HasPrefix(path, "/api/v1/memory/")
}

func (a *App) canvasHandler(next http.Handler) http.Handler {
	if a.config.Canvas == nil {
		return next
	}
	config := a.config.Canvas
	adapter, _ := url.Parse(config.AdapterURL) // validated at construction
	proxy := &httputil.ReverseProxy{
		Rewrite: func(request *httputil.ProxyRequest) {
			request.SetURL(adapter)
			request.Out.Host = adapter.Host
			request.Out.Header.Del("Authorization")
			request.Out.Header.Del("Cookie")
		},
	}
	proxy.ErrorHandler = func(response http.ResponseWriter, request *http.Request, _ error) {
		writeError(request.Context(), response, http.StatusBadGateway, "canvas integration is unavailable")
	}
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if !validLocalRequestHost(request.Host) || request.URL.IsAbs() {
			http.Error(response, "invalid request host", http.StatusBadRequest)
			return
		}
		if !strings.HasPrefix(request.URL.Path, config.Prefix+"/") && request.URL.Path != config.Prefix {
			http.NotFound(response, request)
			return
		}
		if origin := request.Header.Get("Origin"); origin != "" && origin != "http://"+request.Host ||
			request.Header.Get("Sec-Fetch-Site") == "cross-site" {
			http.Error(response, "forbidden", http.StatusForbidden)
			return
		}
		request = request.Clone(context.WithValue(request.Context(), localCanvasContextKey{}, true))
		request.URL.Path = strings.TrimPrefix(request.URL.Path, config.Prefix)
		request.URL.RawPath = ""
		if request.URL.Path == "" {
			request.URL.Path = "/"
		}
		if canvasAdapterPath(request.URL.Path) && (config.OperatorActions == nil || !strings.HasPrefix(request.URL.Path, "/__cli_")) {
			securityHeaders(proxy).ServeHTTP(response, request)
			return
		}
		if (strings.HasPrefix(request.URL.Path, "/api/") && !canvasDataAPI(request.URL.Path)) ||
			strings.HasPrefix(request.URL.Path, "/auth/") || request.URL.Path == "/mcp" {
			http.NotFound(response, request)
			return
		}
		if request.URL.Path == "/" || request.URL.Path == "/index.html" {
			if !request.URL.Query().Has("local-preview") {
				query := request.URL.Query()
				mode := "canvas"
				if config.OperatorActions != nil {
					mode = "operator"
				}
				query.Set("local-preview", mode)
				http.Redirect(response, request, config.Prefix+"/?"+query.Encode(), http.StatusFound)
				return
			}
		}
		// The unguessable panel path is the canvas capability. Ordinary serve
		// keeps its existing bearer bootstrap; no bearer is exposed to Node.
		request.Header.Set("Authorization", "Bearer "+a.accessToken)
		next.ServeHTTP(response, request)
	})
}
