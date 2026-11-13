package ingest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	"github.com/githubnext/gh-cao/server/internal/postgresx"
	"github.com/githubnext/gh-cao/server/internal/telemetry"
)

const RunBundlesFile = "run-bundles.json"
const maxRunBundlesBytes = 32 << 20

// RunBundles reads only the fixed, manifested delta beside the configured
// snapshot. Requests cannot supply another path, namespace or credentials.
func RunBundles(ctx context.Context, store *postgresx.Store, directory string, options Options) (result Result, err error) {
	ctx, span := telemetry.Tracer().Start(ctx, telemetry.SpanIngestRun)
	span.SetAttributes(attribute.String("cao_dashboard.ingest.mode", "run-bundles"))
	defer func() {
		if err != nil {
			span.SetStatus(codes.Error, "run-bundle ingestion failed")
		} else {
			span.SetAttributes(attribute.Int64("cao_dashboard.ingest.revision", result.Revision))
			span.SetStatus(codes.Ok, "")
		}
		span.End()
	}()
	if options.Supplement != nil || options.SupplementRevision != "" || options.Force {
		return Result{}, errors.New("run-bundle ingestion does not accept snapshot force or supplement options")
	}
	if _, err := loadDefinitions(options.DatabaseQueriesPath); err != nil {
		return Result{}, err
	}
	manifest, _, _, err := ValidateManifest(directory)
	if err != nil {
		return Result{}, err
	}
	expected := manifest[RunBundlesFile]
	if expected == "" {
		return Result{}, errors.New("configured source does not contain a manifested Run-bundle publication")
	}
	// #nosec G304 -- fixed basename beneath the operator-configured artifact directory.
	file, err := os.Open(filepath.Join(directory, RunBundlesFile))
	if err != nil {
		return Result{}, err
	}
	defer func() { _ = file.Close() }()
	content, err := io.ReadAll(io.LimitReader(file, maxRunBundlesBytes+1))
	if err != nil {
		return Result{}, err
	}
	if len(content) > maxRunBundlesBytes {
		return Result{}, errors.New("run-bundle publication exceeds the 32 MiB input budget")
	}
	actual := sha256.Sum256(content)
	if !strings.EqualFold(hex.EncodeToString(actual[:]), expected) {
		return Result{}, errors.New("run-bundle publication checksum mismatch")
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	decoder.DisallowUnknownFields()
	var publication postgresx.RunBundlePublication
	if err := decoder.Decode(&publication); err != nil {
		return Result{}, fmt.Errorf("decode Run-bundle publication: %w", err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return Result{}, errors.New("run-bundle publication contains trailing JSON")
	}
	state, err := store.PublishRunBundles(ctx, publication)
	if err != nil {
		return Result{}, err
	}
	ingestLog.Printf("published Run bundles revision=%d bundles=%d", state.Revision, len(publication.Runs))
	return stateResult(state), nil
}
