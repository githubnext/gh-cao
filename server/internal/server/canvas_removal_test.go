package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSecurityHeadersRejectEmbeddingEvenWithRetiredCanvasFlags(t *testing.T) {
	handler := securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	request := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/?local-preview=canvas", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("X-Frame-Options") != "DENY" {
		t.Fatal("retired preview flags relaxed embedding protection")
	}
}
