package auditdelivery

import (
	"net/http"
	"strings"
	"testing"
)

func TestCreateStatusError(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusCreated, http.StatusNoContent, http.StatusPreconditionFailed} {
		if err := createStatusError(status); err != nil {
			t.Errorf("status %d: unexpected error %v", status, err)
		}
	}
	for _, status := range []int{http.StatusBadRequest, http.StatusForbidden, http.StatusInternalServerError} {
		err := createStatusError(status)
		if err == nil || !strings.Contains(err.Error(), "create rejected") {
			t.Errorf("status %d: got %v, want create rejection", status, err)
		}
	}
}

func TestCommitStatusError(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusCreated, http.StatusNoContent} {
		if err := commitStatusError(status); err != nil {
			t.Errorf("status %d: unexpected error %v", status, err)
		}
	}
	if err := commitStatusError(http.StatusPreconditionFailed); err == nil || !strings.Contains(err.Error(), "checkpoint conflict") {
		t.Errorf("precondition failure: got %v, want conflict", err)
	}
	for _, status := range []int{http.StatusBadRequest, http.StatusNotFound, http.StatusBadGateway} {
		err := commitStatusError(status)
		if err == nil || !strings.Contains(err.Error(), "checkpoint rejected") {
			t.Errorf("status %d: got %v, want rejection", status, err)
		}
	}
}
