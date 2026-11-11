package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path"
	"strings"
	"syscall"
	"time"
	"unicode"

	"github.com/spf13/cobra"

	debuglogger "github.com/githubnext/gh-cao/server/internal/logger"
)

var pagesLog = debuglogger.New("cao:dashboard:serve-pages")

func newServePagesCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "serve-pages",
		Short: "preview the synthetic Pages demo with browser IndexedDB (no operational data)",
		Args:  cobra.NoArgs,
	}
	directory := cmd.Flags().String("site", "../.tmp/dashboard-pages", "synthetic directory prepared by npm run dashboard:pages:build")
	listen := cmd.Flags().String("listen", "127.0.0.1:8080", "numeric loopback HTTP listen address")
	basePath := cmd.Flags().String("base-path", "/", "URL path prefix, e.g. /repository/; files are served unchanged")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		if err := validatePagesListen(*listen); err != nil {
			return err
		}
		root, handler, err := newPagesHandler(*directory, *basePath)
		if err != nil {
			return err
		}
		defer func() { _ = root.Close() }()
		ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", *listen)
		if err != nil {
			return fmt.Errorf("listen for Pages: %w", err)
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "Pages preview: http://%s%s\n", listener.Addr(), handler.basePath)
		httpServer := &http.Server{
			Handler: handler, ReadHeaderTimeout: 5 * time.Second,
			IdleTimeout: 60 * time.Second,
		}
		return runPagesServer(ctx, httpServer, listener, 5*time.Second)
	}
	return cmd
}

func validatePagesListen(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return errors.New("--listen must be a numeric loopback host:port")
	}
	return nil
}

type pagesHandler struct {
	root     *os.Root
	basePath string
}

// normalizePagesBasePath validates a clean absolute URL path and returns it
// with exactly one trailing slash.
func normalizePagesBasePath(basePath string) (string, error) {
	if !strings.HasPrefix(basePath, "/") || strings.ContainsAny(basePath, `\?#%`) ||
		strings.IndexFunc(basePath, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 ||
		path.Clean(basePath) != strings.TrimSuffix(basePath, "/") && basePath != "/" {
		return "", errors.New("--base-path must be a clean absolute URL path")
	}
	return strings.TrimSuffix(basePath, "/") + "/", nil
}

// pagesFileName maps a request path under basePath to a relative file name
// ("." for the site root); ok is false for paths outside the base or invalid.
func pagesFileName(basePath, requestPath string) (name string, ok bool) {
	if !strings.HasPrefix(requestPath, basePath) {
		return "", false
	}
	name = strings.TrimSuffix(strings.TrimPrefix(requestPath, basePath), "/")
	if strings.Contains(name, `\`) || name != "" && !fs.ValidPath(name) {
		return "", false
	}
	if name == "" {
		name = "."
	}
	return name, true
}

func newPagesHandler(directory, basePath string) (*os.Root, *pagesHandler, error) {
	basePath, err := normalizePagesBasePath(basePath)
	if err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(directory) == "" {
		return nil, nil, errors.New("--site must name a prepared Pages directory")
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return nil, nil, fmt.Errorf("open Pages directory: %w", err)
	}
	index, err := root.Open("index.html")
	if err == nil {
		var info fs.FileInfo
		info, err = index.Stat()
		if err == nil && (!info.Mode().IsRegular() || info.Size() == 0) {
			err = errors.New("index.html must be a non-empty regular file")
		}
		_ = index.Close()
	}
	if err != nil {
		_ = root.Close()
		return nil, nil, fmt.Errorf("pages directory requires a readable built index.html: %w", err)
	}
	marker, err := root.ReadFile(".cao-pages-demo.json")
	var demo struct {
		Tool      string `json:"tool"`
		Version   int    `json:"version"`
		Synthetic bool   `json:"synthetic"`
	}
	if err != nil || len(marker) > 4096 || json.Unmarshal(marker, &demo) != nil ||
		demo.Tool != "cao-pages-demo" || demo.Version != 1 || !demo.Synthetic {
		_ = root.Close()
		return nil, nil, errors.New("pages accepts only the synthetic demo; run npm run dashboard:pages:build, or use serve --sqlite for operational data")
	}
	for _, name := range []string{"gh-aw-logs.sqlite", "agent-summary.json", "payload-hashes.json"} {
		if _, err := root.Stat(name); !errors.Is(err, os.ErrNotExist) {
			_ = root.Close()
			return nil, nil, fmt.Errorf("pages demo must not contain operational export %s", name)
		}
	}
	pagesLog.Printf("pages handler ready: base_path_depth=%d", strings.Count(basePath, "/")-1)
	return root, &pagesHandler{root: root, basePath: basePath}, nil
}

func (h *pagesHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.basePath != "/" && r.URL.Path == strings.TrimSuffix(h.basePath, "/") {
		target := url.URL{Path: h.basePath, RawQuery: r.URL.RawQuery}
		http.Redirect(w, r, target.String(), http.StatusMovedPermanently)
		return
	}
	name, ok := pagesFileName(h.basePath, r.URL.Path)
	if !ok {
		http.NotFound(w, r)
		return
	}
	// Root confines even symlink resolution and concurrent renames to the
	// prepared deployment. Never use an unconfined path after this check.
	file, err := h.root.Open(name)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer func(opened *os.File) { _ = opened.Close() }(file)
	info, err := file.Stat()
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if !info.IsDir() && strings.HasSuffix(r.URL.Path, "/") {
		http.NotFound(w, r)
		return
	}
	if info.IsDir() {
		index, openErr := h.root.Open(path.Join(name, "index.html"))
		if openErr != nil {
			http.NotFound(w, r)
			return
		}
		defer func() { _ = index.Close() }()
		info, err = index.Stat()
		if err != nil || !info.Mode().IsRegular() {
			http.NotFound(w, r)
			return
		}
		if !strings.HasSuffix(r.URL.Path, "/") {
			target := url.URL{Path: h.basePath + name + "/", RawQuery: r.URL.RawQuery}
			http.Redirect(w, r, target.String(), http.StatusMovedPermanently)
			return
		}
		file = index
	}
	if !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	http.ServeContent(w, r, info.Name(), info.ModTime(), file)
}

func runPagesServer(ctx context.Context, httpServer *http.Server, listener net.Listener, shutdownTimeout time.Duration) error {
	defer func() { _ = httpServer.Close() }()
	stopped := make(chan error, 1)
	go func() { stopped <- httpServer.Serve(listener) }()
	select {
	case err := <-stopped:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		pagesLog.Printf("pages server shutting down: timeout_ms=%d", shutdownTimeout.Milliseconds())
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			pagesLog.Printf("pages server graceful shutdown failed: forced_close=true")
			_ = httpServer.Close()
			<-stopped
			return fmt.Errorf("stop Pages server: %w", err)
		}
		<-stopped
		return nil
	}
}
