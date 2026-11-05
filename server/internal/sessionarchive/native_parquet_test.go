package sessionarchive

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/format"

	"github.com/githubnext/gh-cao/server/internal/adxtrace"
)

func nativeTestConfig(t *testing.T) Config {
	t.Helper()
	c := Config{Enabled: true, NativeProducer: true, Namespace: "private",
		Repositories: []string{"octo/api"},
		Policy: adxtrace.Policy{Version: "approved-v1", ContentApproved: true, RightsApproved: true,
			Events: map[string][]string{"session.format": {"version"}, "session.collection": {"complete"},
				"workflow.info": {"episode"}, "tool.execution_complete": {"output", "error", "success", "toolCallId"}}}}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestNativeParquetGeneratedSchemaAndCompleteRoundTrip(t *testing.T) {
	c := nativeTestConfig(t)
	p := testProjection(t, c, "123", strings.Repeat("approved λ null ' content\n", 100))
	p.Events[0].Gaps = []string{"explicit-gap"}
	content, f, err := NativeParquet(t.Context(), p, 128<<20)
	if err != nil {
		t.Fatal(err)
	}
	if f.Rows != int64(len(p.Events)) || f.Members[0] != p.Member() || f.Digest != hash(content) {
		t.Fatal("incomplete immutable file identity")
	}
	file, err := parquet.OpenFile(bytes.NewReader(content), int64(len(content)))
	if err != nil {
		t.Fatal(err)
	}
	columns := file.Schema().Columns()
	for i, name := range eventColumns {
		if columns[i][0] != name {
			t.Fatalf("generated column %d: %v != %s", i, columns[i], name)
		}
		if eventSQLTypes[i] == "BIGINT" && file.Schema().Fields()[i].Type().Kind() != parquet.Int64 {
			t.Fatal("generated integer type changed")
		}
	}
	for _, row := range file.Metadata().RowGroups {
		for _, column := range row.Columns {
			if column.MetaData.Codec != format.Zstd {
				t.Fatal("native file is not Zstandard")
			}
		}
	}
	reader := parquet.NewReader(file)
	defer func() { _ = reader.Close() }()
	for _, expected := range p.Events {
		row := reflect.New(nativeEvent)
		if err := reader.Read(row.Interface()); err != nil {
			t.Fatal(err)
		}
		if row.Elem().FieldByName("PayloadJSON").String() != expected.PayloadJSON {
			t.Fatal("JSON structure, numeric precision or explicit null lost")
		}
	}
	again, _, err := NativeParquet(t.Context(), p, 128<<20)
	if err != nil || !bytes.Equal(again, content) {
		t.Fatal("immutable conversion is not deterministic")
	}
	if _, _, err := NativeParquet(t.Context(), p, 16); !errors.Is(err, ErrCapacity) {
		t.Fatalf("file cap was not enforced: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := NativeParquet(ctx, p, 128<<20); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation ignored: %v", err)
	}
	p.Events[0].Sequence = 99
	if _, _, err := NativeParquet(t.Context(), p, 128<<20); !errors.Is(err, ErrIntegrity) {
		t.Fatal("partial or corrupt projection admitted")
	}
}

func TestNativeArchiveConfigIsFileFreeAndExplicit(t *testing.T) {
	c := nativeTestConfig(t)
	c.Directory = "/must-not-open"
	if c.Validate() == nil {
		t.Fatal("native producer selected a filesystem")
	}
	c.Directory = ""
	c.DuckDBPath = "/must-not-execute"
	if c.Validate() == nil {
		t.Fatal("native producer selected a subprocess")
	}
	env := map[string]string{
		"CAO_SESSION_ARCHIVE_ENABLED": "true", "CAO_SESSION_ARCHIVE_PRODUCER": "native",
		"CAO_SESSION_ARCHIVE_NAMESPACE": "private", "CAO_SESSION_ARCHIVE_REPOSITORIES": "octo/api",
		"CAO_SESSION_ARCHIVE_POLICY":      "/must-not-read",
		"CAO_SESSION_ARCHIVE_POLICY_JSON": `{"version":"v1","contentApproved":true,"rightsApproved":true,"events":{"session.format":["version"]}}`,
	}
	if _, err := FromEnv(func(key string) string { return env[key] }); err != nil {
		t.Fatal(err)
	}
	env["CAO_SESSION_ARCHIVE_MODE"] = "sample"
	if _, err := FromEnv(func(key string) string { return env[key] }); err == nil {
		t.Fatal("unsupported sampling did not fail closed")
	}
}
