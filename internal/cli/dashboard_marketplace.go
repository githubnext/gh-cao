package cli

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"time"
)

// The credential-bearing resolver runs outside the read-only web server.
func dashboardMarketplaceHandler(server, policy string) http.HandlerFunc {
	var mutex sync.Mutex
	var digest [sha256.Size]byte
	var cached []byte
	var expires time.Time
	return func(w http.ResponseWriter, r *http.Request) {
		mutex.Lock()
		defer mutex.Unlock()
		content, err := os.ReadFile(policy)
		if err != nil {
			http.Error(w, "marketplace policy unavailable", http.StatusServiceUnavailable)
			return
		}
		current := sha256.Sum256(content)
		if current != digest || cached == nil || time.Now().After(expires) {
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
			defer cancel()
			command := exec.CommandContext(ctx, server, "__marketplace", "--policy", policy)
			// Inherit configured registry secrets only in this trusted resolver.
			command.Env = os.Environ()
			output, err := command.StdoutPipe()
			if err != nil {
				http.Error(w, "marketplace resolver unavailable", http.StatusServiceUnavailable)
				return
			}
			if err := command.Start(); err != nil {
				http.Error(w, "marketplace resolver unavailable", http.StatusServiceUnavailable)
				return
			}
			const limit = 32 << 20
			data, readErr := io.ReadAll(io.LimitReader(output, limit+1))
			if readErr != nil || len(data) > limit {
				_ = command.Process.Kill()
			}
			waitErr := command.Wait()
			if readErr != nil || len(data) > limit || waitErr != nil || !json.Valid(data) {
				http.Error(w, "marketplace resolution failed", http.StatusServiceUnavailable)
				return
			}
			var settings struct {
				Control struct {
					Marketplace struct {
						TTL int `json:"cache-ttl-seconds"`
					} `json:"marketplace"`
				} `json:"control-plane"`
			}
			if err := json.Unmarshal(content, &settings); err != nil {
				http.Error(w, "marketplace policy unavailable", http.StatusServiceUnavailable)
				return
			}
			ttl := 5 * time.Minute
			if settings.Control.Marketplace.TTL > 0 {
				ttl = time.Duration(settings.Control.Marketplace.TTL) * time.Second
			}
			cached, digest, expires = data, current, time.Now().Add(ttl)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(cached)
	}
}
