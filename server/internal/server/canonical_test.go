package server

import (
	"errors"
	"net/http"
	"testing"
)

func TestClassifyCanonicalErrorSucceedsWithNilError(t *testing.T) {
	outcome, status, message := classifyCanonicalError(nil)
	if outcome != canonicalOutcomeOK {
		t.Errorf("outcome = %q, want %q", outcome, canonicalOutcomeOK)
	}
	if status != http.StatusOK {
		t.Errorf("status = %d, want %d", status, http.StatusOK)
	}
	if message != "" {
		t.Errorf("message = %q, want empty", message)
	}
}

func TestClassifyCanonicalErrorReportsNotFound(t *testing.T) {
	outcome, status, message := classifyCanonicalError(errCanonicalEntityNotFound)
	if outcome != canonicalOutcomeNotFound {
		t.Errorf("outcome = %q, want %q", outcome, canonicalOutcomeNotFound)
	}
	if status != http.StatusNotFound {
		t.Errorf("status = %d, want %d", status, http.StatusNotFound)
	}
	if message != "canonical entity was not found" {
		t.Errorf("message = %q, want %q", message, "canonical entity was not found")
	}
}

func TestClassifyCanonicalErrorReportsNotFoundThroughWrappedError(t *testing.T) {
	wrapped := errors.Join(errors.New("context"), errCanonicalEntityNotFound)
	outcome, status, _ := classifyCanonicalError(wrapped)
	if outcome != canonicalOutcomeNotFound {
		t.Errorf("outcome = %q, want %q", outcome, canonicalOutcomeNotFound)
	}
	if status != http.StatusNotFound {
		t.Errorf("status = %d, want %d", status, http.StatusNotFound)
	}
}

func TestClassifyCanonicalErrorReportsUnavailableForOtherErrors(t *testing.T) {
	outcome, status, message := classifyCanonicalError(errors.New("operational is unreachable"))
	if outcome != canonicalOutcomeUnavailable {
		t.Errorf("outcome = %q, want %q", outcome, canonicalOutcomeUnavailable)
	}
	if status != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", status, http.StatusServiceUnavailable)
	}
	if message != "canonical data is unavailable" {
		t.Errorf("message = %q, want %q", message, "canonical data is unavailable")
	}
}
