package main

import (
	"io"
	"strings"
	"testing"
)

func TestDoctorRejectsInvalidRedisMemoryBudget(t *testing.T) {
	t.Setenv("CAO_REDIS_MAX_BYTES", "not-a-memory-budget")
	command := newDoctorCommand()
	command.SetArgs([]string{"--redis-url", defaultRedisURL, "--namespace", "doctor-budget"})
	command.SetOut(io.Discard)
	command.SetErr(io.Discard)
	err := command.Execute()
	if err == nil || !strings.Contains(err.Error(), "CAO_REDIS_MAX_BYTES") {
		t.Fatalf("invalid memory-budget configuration was hidden: %v", err)
	}
	if strings.Contains(err.Error(), "not-a-memory-budget") {
		t.Fatalf("invalid configuration value leaked: %v", err)
	}
}
