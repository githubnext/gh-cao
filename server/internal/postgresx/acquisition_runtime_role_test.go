package postgresx

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/adxtrace"
	"github.com/githubnext/gh-cao/server/internal/collectnative"
)

func TestNativeAcquisitionAndTraceRuntimeRole(t *testing.T) {
	owner, config := nativeTestStore(t)
	store := valueRuntimeRoleStore(t, owner, config)
	acquisition := store.Acquisition()
	identity := collectnative.Identity{Repository: "octo/api", InstallationID: 7, RunID: 303, Attempt: 1}
	if err := acquisition.SetEnrollment(t.Context(), identity.Repository, identity.InstallationID); err != nil {
		t.Fatal(err)
	}
	claim, err := acquisition.Claim(t.Context(), identity, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	evidence := collectnative.Evidence{Version: 1, Identity: identity, Run: map[string]any{
		"organization": "octo", "repository": "octo/api", "run_id": 303, "run_attempt": 1,
		"workflow_name": "Native", "workflow_path": ".github/workflows/native.lock.yml",
	}, Gaps: []string{"session-unavailable"}}
	if err := acquisition.Publish(t.Context(), claim, evidence); err != nil {
		t.Fatal(err)
	}
	if count, err := acquisition.Replay(t.Context(), 1); err != nil || count != 1 {
		t.Fatalf("non-owner production role cannot publish native canonical facts: %d %v", count, err)
	}
	storage, err := store.TraceStorage("native-runtime", 1<<20, 10)
	if err != nil {
		t.Fatal(err)
	}
	key := strings.Repeat("a", 64) + ".trace"
	if err := storage.Transaction(t.Context(), func(objects adxtrace.Objects) error {
		return objects.Write(key, []byte("approved synthetic evidence"))
	}); err != nil {
		t.Fatal(err)
	}
	if err := storage.Transaction(t.Context(), func(objects adxtrace.Objects) error {
		content, err := objects.Read(key, 1024)
		if err != nil || string(content) != "approved synthetic evidence" {
			return errors.New("runtime cannot read its approved object")
		}
		return objects.Remove(key)
	}); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"cao_acquisition_scopes", "cao_acquisition_attempts", "cao_trace_objects"} {
		for _, privilege := range []string{"TRUNCATE", "TRIGGER", "REFERENCES"} {
			var allowed bool
			if err := store.db.QueryRowContext(t.Context(), `SELECT has_table_privilege(current_user,$1,$2)`,
				table, privilege).Scan(&allowed); err != nil || allowed {
				t.Fatalf("runtime received bootstrap authority %s on %s: %v", privilege, table, err)
			}
		}
	}
}
