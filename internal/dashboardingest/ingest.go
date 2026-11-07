package dashboardingest

import (
	"bytes"
	"compress/gzip"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/dop251/goja"
	"github.com/dop251/goja_nodejs/require"
	jsurl "github.com/dop251/goja_nodejs/url"
)

//go:embed normalize.generated.js.gz
var bundledNormalizer []byte

type Input struct {
	JSONL     string           `json:"jsonl"`
	Artifacts []map[string]any `json:"artifacts,omitempty"`
}

func Hydrate(ctx context.Context, input Input) (map[string][]map[string]any, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	content, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	if len(content) > 128<<20 {
		return nil, errors.New("canonical log input exceeds the 128 MiB encoding budget")
	}
	compressed, err := gzip.NewReader(bytes.NewReader(bundledNormalizer))
	if err != nil {
		return nil, err
	}
	script, err := io.ReadAll(compressed)
	closeErr := compressed.Close()
	if err != nil || closeErr != nil {
		return nil, errors.Join(err, closeErr)
	}
	runtime := goja.New()
	require.NewRegistry().Enable(runtime)
	jsurl.Enable(runtime)
	if err := runtime.GlobalObject().Delete("require"); err != nil {
		return nil, err
	}
	if err := runtime.Set("__encodeUTF8", func(value string) []byte { return []byte(value) }); err != nil {
		return nil, err
	}
	clock := time.Now()
	if err := runtime.Set("performance", map[string]any{
		"now": func() float64 { return float64(time.Since(clock)) / float64(time.Millisecond) },
	}); err != nil {
		return nil, err
	}
	if _, err := runtime.RunString(`globalThis.TextEncoder = class {
		encode(value = "") { return new Uint8Array(__encodeUTF8(String(value))); }
	}; globalThis.TextDecoder = class {
		decode() { throw new TypeError("local ingestion requires JSONL text"); }
	};`); err != nil {
		return nil, err
	}
	bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-bounded.Done():
			runtime.Interrupt(bounded.Err())
		case <-done:
		}
	}()
	if _, err := runtime.RunString(string(script)); err != nil {
		return nil, fmt.Errorf("initialize canonical log normalizer: %w", err)
	}
	hydrate, ok := goja.AssertFunction(runtime.Get("CaoIngest").ToObject(runtime).Get("hydrate"))
	if !ok {
		return nil, errors.New("canonical log normalizer entry point is missing")
	}
	parse, ok := goja.AssertFunction(runtime.Get("JSON").ToObject(runtime).Get("parse"))
	if !ok {
		return nil, errors.New("canonical log normalizer JSON parser is missing")
	}
	value, err := parse(goja.Undefined(), runtime.ToValue(string(content)))
	if err != nil {
		return nil, err
	}
	result, err := hydrate(goja.Undefined(), value)
	if err != nil {
		return nil, fmt.Errorf("normalize gh-aw log evidence: %w", err)
	}
	output := result.String()
	if len(output) > 64<<20 {
		return nil, errors.New("canonical log evidence exceeds the 64 MiB output budget")
	}
	var batch map[string][]map[string]any
	if err := json.Unmarshal([]byte(output), &batch); err != nil {
		return nil, err
	}
	return batch, nil
}
