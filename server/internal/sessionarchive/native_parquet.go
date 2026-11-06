package sessionarchive

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"

	"github.com/parquet-go/parquet-go"
	"github.com/parquet-go/parquet-go/compress/zstd"
)

// Derive the physical schema from the generated logical contract, including
// its JSON names (which intentionally differ from some Go field names).
func nativeEventType() reflect.Type {
	event := reflect.TypeFor[Event]()
	fields := make([]reflect.StructField, event.NumField())
	for i := range fields {
		fields[i] = event.Field(i)
		name := fields[i].Tag.Get("json")
		tag := fmt.Sprintf(`parquet:"%s"`, name)
		if fields[i].Type.Kind() == reflect.Slice {
			tag = fmt.Sprintf(`parquet:"%s,list"`, name)
		}
		fields[i].Tag = reflect.StructTag(tag)
	}
	return reflect.StructOf(fields)
}

var nativeEvent = nativeEventType()

type boundedParquet struct {
	bytes.Buffer
	limit int64
}

func (b *boundedParquet) Write(p []byte) (int, error) {
	if int64(b.Len())+int64(len(p)) > b.limit {
		return 0, ErrCapacity
	}
	return b.Buffer.Write(p)
}

// NativeParquet converts one complete approved trace without files, SQL or a
// subprocess. A round trip verifies the generated schema and every event before
// the caller may publish these immutable bytes. Limits reject, never truncate.
func NativeParquet(ctx context.Context, p Projection, limit int64) ([]byte, File, error) {
	if err := validateProjection(p); err != nil {
		return nil, File{}, err
	}
	if limit < 1 || limit > 128<<20 {
		return nil, File{}, ErrCapacity
	}
	schema := parquet.SchemaOf(reflect.New(nativeEvent).Interface())
	buffer := &boundedParquet{limit: limit}
	writer := parquet.NewWriter(buffer, schema,
		parquet.Compression(&zstd.Codec{Concurrency: 1}),
		parquet.MaxRowsPerRowGroup(1024), parquet.PageBufferSize(64<<10))
	var logicalBytes int64
	for _, event := range p.Events {
		if err := ctx.Err(); err != nil {
			return nil, File{}, err
		}
		logicalBytes += int64(len(event.PayloadJSON))
		if logicalBytes > 128<<20 {
			return nil, File{}, ErrCapacity
		}
		row := reflect.New(nativeEvent)
		source := reflect.ValueOf(event)
		for i := range nativeEvent.NumField() {
			row.Elem().Field(i).Set(source.Field(i))
		}
		if err := writer.Write(row.Interface()); err != nil {
			return nil, File{}, err
		}
	}
	if err := writer.Close(); err != nil {
		return nil, File{}, err
	}
	content := buffer.Bytes()
	file, err := parquet.OpenFile(bytes.NewReader(content), int64(len(content)))
	if err != nil || file.Schema().String() != schema.String() || file.NumRows() != int64(len(p.Events)) {
		return nil, File{}, ErrIntegrity
	}
	reader := parquet.NewReader(file)
	defer func() { _ = reader.Close() }()
	for _, expected := range p.Events {
		if err := ctx.Err(); err != nil {
			return nil, File{}, err
		}
		row := reflect.New(nativeEvent)
		if err := reader.Read(row.Interface()); err != nil {
			return nil, File{}, ErrIntegrity
		}
		actual := reflect.ValueOf(expected)
		for i := range nativeEvent.NumField() {
			if !reflect.DeepEqual(row.Elem().Field(i).Interface(), actual.Field(i).Interface()) {
				return nil, File{}, ErrIntegrity
			}
		}
	}
	if err := reader.Read(reflect.New(nativeEvent).Interface()); !errors.Is(err, io.EOF) {
		return nil, File{}, ErrIntegrity
	}
	return content, File{Digest: hash(content), Day: p.Day, Bytes: int64(len(content)),
		Rows: int64(len(p.Events)), Members: []Member{p.Member()}}, nil
}
