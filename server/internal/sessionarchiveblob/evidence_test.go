package sessionarchiveblob

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/parquet-go/parquet-go"

	"github.com/githubnext/gh-cao/server/internal/sessionarchive"
)

func TestNativeOriginalEvidencePublicationAndReconstruction(t *testing.T) {
	n, protocol := nativeProtocol(t)
	raw, err := os.ReadFile("../sessionarchive/testdata/evidence.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := os.ReadFile("../sessionarchive/testdata/evidence-policy.json")
	if err != nil || json.Unmarshal(policy, &n.capture.Policy) != nil {
		t.Fatal("invalid explicit synthetic approval")
	}
	completed := protocol.now
	if err := n.CaptureSession(t.Context(), "octo/api", "123", 7, completed, raw, ""); err != nil {
		t.Fatal(err)
	}
	file := n.state.Manifest.Files[0]
	encoded := protocol.objects[file.Path]
	p, err := sessionarchive.Project(n.traceConfig(), "private", "octo/api", "123",
		completed.Format("2006-01-02"), 7, raw, n.state.NativeRecords[0].CapturedAt)
	if err != nil {
		t.Fatal(err)
	}
	eventType := reflect.TypeFor[sessionarchive.Event]()
	fields := make([]reflect.StructField, eventType.NumField())
	for i := range fields {
		fields[i] = eventType.Field(i)
		tag := fields[i].Tag.Get("json")
		if fields[i].Type.Kind() == reflect.Slice {
			tag += ",list"
		}
		fields[i].Tag = reflect.StructTag(`parquet:"` + tag + `"`)
	}
	physical := reflect.StructOf(fields)
	reader := parquet.NewReader(bytes.NewReader(encoded))
	defer func() { _ = reader.Close() }()
	restored := make([]sessionarchive.Event, 0, len(p.Events))
	for range p.Events {
		row := reflect.New(physical)
		if err := reader.Read(row.Interface()); err != nil {
			t.Fatal(err)
		}
		var event sessionarchive.Event
		target := reflect.ValueOf(&event).Elem()
		for i := range eventType.NumField() {
			target.Field(i).Set(row.Elem().Field(i))
		}
		restored = append(restored, event)
	}
	if !reflect.DeepEqual(restored, p.Events) || file.Rows != int64(len(p.Events)) ||
		file.Members[0].Attempt != 7 || len(n.state.NativeRecords) != 1 {
		t.Fatal("native Blob publication changed original scalar/structured evidence or exact attempt")
	}
	protocol.query = func(w http.ResponseWriter, _ *http.Request, kql string) {
		if !strings.Contains(kql, "join kind=inner (selected)") || !strings.Contains(kql, "PresentFields") {
			t.Error("query bypassed exact membership or omitted generated evidence metadata")
		}
		rows := make([][]any, 0, len(restored))
		for _, event := range restored {
			rows = append(rows, []any{event})
		}
		respond(w, []string{"Event"}, rows, true)
	}
	scope := sessionarchive.Scope{Namespace: "private", Repositories: []string{"octo/api"}}
	_, actual, lifetime, err := n.ReadTimeline(t.Context(), scope,
		sessionarchive.TimelineRequest{Repository: "octo/api", RunID: "123", Attempt: 7, Limit: 100})
	if err != nil || !reflect.DeepEqual(actual, p.Events) {
		t.Fatalf("generation-bound protocol reconstruction lost original evidence: %v", err)
	}
	defer func() { _ = lifetime.Close() }()
	if err := lifetime.Deliver(func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := n.Withdraw(t.Context(), "octo/api", "123", 7); err != nil {
		t.Fatal(err)
	}
	if !errors.Is(lifetime.Deliver(func() error { t.Fatal("withdrawn evidence delivered"); return nil }), ErrWithdrawn) {
		t.Fatal("original-evidence query lost withdrawal-aware delivery")
	}
}

func TestFreshEvidenceSchemaRequiresNewAcceptance(t *testing.T) {
	a, _, _ := newProtocol(t)
	schema := EventSchema()
	for _, field := range []string{"Success:bool", "DurationMs:real", "InputTokens:long", "PresentFields:dynamic", "InvalidFields:dynamic"} {
		if !strings.Contains(schema, field) {
			t.Fatalf("generated ADX evidence schema missing %s", field)
		}
	}
	old := validConfig()
	binding, nativeBinding := old.AcceptanceBinding(), old.NativeAcceptanceBinding()
	fresh := old
	fresh.Table = "FreshEvidenceEvents"
	fresh.Prefix = "fresh-evidence"
	if fresh.AcceptanceBinding() == binding || fresh.NativeAcceptanceBinding() == nativeBinding {
		t.Fatal("fresh destination reused reviewed acceptance")
	}
	fresh.Acceptance = nativeBinding
	if n, err := NewNativeWithServices(t.Context(), nativeCaptureConfig(t), fresh, Services{}); !errors.Is(err, ErrConformance) || n != nil {
		t.Fatal("stale native acceptance activated a fresh destination")
	}
	previous := fixture(t, 1, "previous-schema")
	previous.Version = 1
	if err := a.Publish(t.Context(), previous); !errors.Is(err, ErrIntegrity) {
		t.Fatal("previous schema was treated as a transparent migration")
	}
	if a.active.Generation != 0 {
		t.Fatal("old schema rejection modified current publication")
	}
}

func TestTypedEvidenceAnalyticalProtocol(t *testing.T) {
	for _, malformed := range []bool{false, true} {
		a, protocol, _ := newProtocol(t)
		a.config.AnalyticalAcceptance = a.config.AnalyticalAcceptanceBinding()
		definition := archiveDefinition(t, `"filter":{"predicates":[{"field":"Success","equals":false},{"field":"DurationMs","gte":0.125}]},"select":[{"field":"Success"},{"field":"DurationMs"}]`)
		protocol.query = func(w http.ResponseWriter, _ *http.Request, kql string) {
			if !strings.Contains(kql, `["Success"]==false`) || !strings.Contains(kql, `["DurationMs"]>=real(0.125)`) {
				t.Error("typed evidence operands changed across query adapters")
			}
			row := map[string]any{"Success": false, "DurationMs": 1.25}
			if malformed {
				row["Success"] = "false"
			}
			respond(w, []string{"rows", "tracesWithGaps"}, [][]any{{[]map[string]any{row}, 0}}, true)
		}
		scope := sessionarchive.Scope{Namespace: "private", Repositories: []string{"octo/api"}}
		_, result, lifetime, err := a.ReadAnalytical(t.Context(), scope, "octo/api", definition)
		if malformed {
			if !errors.Is(err, ErrIntegrity) || lifetime != nil {
				t.Fatal("invalid typed boolean response was coerced")
			}
		} else {
			if err != nil || result.Rows[0]["Success"] != false || result.Rows[0]["DurationMs"] != json.Number("1.25") {
				t.Fatalf("real/boolean result changed type: %#v %v", result, err)
			}
			if err := lifetime.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
}
