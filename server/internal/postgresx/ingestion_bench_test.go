package postgresx

import (
	"fmt"
	"testing"
	"time"

	"github.com/githubnext/gh-cao/server/internal/model"
)

func BenchmarkLocalIngestion(b *testing.B) {
	for _, retained := range []int{100, 1600, 10000} {
		b.Run(fmt.Sprintf("retained_%d", retained), func(b *testing.B) {
			for _, mode := range []string{"snapshot", "bundles"} {
				b.Run(mode, func(b *testing.B) {
					store, _ := nativeTestStore(b)
					at := time.Now().UTC().Truncate(time.Hour).Add(-time.Hour)
					rows := map[string][]model.Row{
						"$repositories": {{"id": "repository", "owner": "synthetic", "name": "benchmark"}},
						"$workflows":    {{"id": "workflow", "repositoryId": "repository", "path": ".github/workflows/test.md"}},
					}
					for i := 0; i < retained; i++ {
						id := fmt.Sprintf("run:%06d", i)
						rows["$runs"] = append(rows["$runs"], model.Row{
							"id": id, "attempt": 1, "repositoryId": "repository", "workflowId": "workflow",
							"createdAt": at, "updatedAt": at, "status": "completed", "conclusion": "success",
						})
						rows["$tools"] = append(rows["$tools"], model.Row{"id": "tool:" + id, "runId": id, "name": "read"})
						rows["$audits"] = append(rows["$audits"], model.Row{
							"id": "audit:" + id, "runId": id, "source": "session", "type": "agent.step", "timestamp": at,
						})
					}
					snapshot := func() State {
						writer, err := store.BeginIngestion(b.Context())
						if err != nil {
							b.Fatal(err)
						}
						defer writer.Abort(b.Context())
						for _, source := range tableNames() {
							for _, row := range rows[source] {
								if err := writer.Append(b.Context(), source, row); err != nil {
									b.Fatal(err)
								}
							}
						}
						state, err := writer.Publish(b.Context(), "synthetic-benchmark")
						if err != nil {
							b.Fatal(err)
						}
						return state
					}
					previous := snapshot()
					input := RunBundlePublication{Version: 1}
					for i := 0; i < MaxRunBundles; i++ {
						input.Runs = append(input.Runs, RunBundle{
							Run: rows["$runs"][i],
							Collections: map[string]RunBundleCollection{
								"tools":  {Coverage: "complete", Rows: rows["$tools"][i : i+1]},
								"audits": {Coverage: "complete", Rows: rows["$audits"][i : i+1]},
							},
						})
					}
					observed := time.Now().UTC().Truncate(time.Microsecond)
					b.ReportAllocs()
					b.ResetTimer()
					for i := 0; i < b.N; i++ {
						var state State
						if mode == "snapshot" {
							state = snapshot()
						} else {
							input.ObservedAt = observed.Add(time.Duration(i+1) * time.Microsecond)
							for _, bundle := range input.Runs {
								bundle.Run["inputTokens"] = i
							}
							var err error
							state, err = store.PublishRunBundles(b.Context(), input)
							if err != nil {
								b.Fatal(err)
							}
						}
						if state.Revision != previous.Revision+1 {
							b.Fatal("synthetic ingestion did not publish a new revision")
						}
						if state.Counts["$runs"] != retained || state.Counts["$tools"] != retained || state.Counts["$audits"] != retained {
							b.Fatalf("synthetic ingestion lost evidence: %+v", state.Counts)
						}
						previous = state
					}
					b.StopTimer()
				})
			}
		})
	}
}
