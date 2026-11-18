package postgresx

import "testing"

func TestRunBundleStagesHaveBoundedStatistics(t *testing.T) {
	store, _ := nativeTestStore(t)
	at := seedBundleRuns(t, store)
	writer, err := store.beginCanonicalWrite(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Abort(t.Context())
	writer.delta = true
	if err := writer.stageRunBundles(t.Context(), bundleUpdate(at)); err != nil {
		t.Fatal(err)
	}
	accepted, err := writer.selectRunBundles(t.Context())
	if err != nil || accepted != 1 {
		t.Fatalf("select bounded Run input: accepted=%d error=%v", accepted, err)
	}
	for _, name := range []string{"runs_stage", "affected_runs"} {
		var estimated int64
		if err := writer.tx.QueryRow(t.Context(),
			"SELECT reltuples::bigint FROM pg_class WHERE oid=to_regclass($1)", "pg_temp."+name).
			Scan(&estimated); err != nil {
			t.Fatal(err)
		}
		if estimated != accepted {
			t.Fatalf("%s planner estimate=%d, want bounded input=%d", name, estimated, accepted)
		}
	}
}
