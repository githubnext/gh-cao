package sessionarchive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"
)

type ConverterConfig struct {
	DuckDBPath             string
	Timeout                time.Duration
	MemoryBytes, TempBytes int64
	Threads                int
}

type Converter struct {
	config ConverterConfig
	slot   chan struct{}
}

func NewConverter(c ConverterConfig) (*Converter, error) {
	if c.DuckDBPath == "" {
		c.DuckDBPath = "duckdb"
	}
	path, err := exec.LookPath(c.DuckDBPath)
	if err != nil {
		return nil, fmt.Errorf("pinned DuckDB %s is required: %w", DuckDBVersion, err)
	}
	c.DuckDBPath, err = filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if c.Timeout == 0 {
		c.Timeout = 30 * time.Second
	}
	if c.MemoryBytes == 0 {
		c.MemoryBytes = 256 << 20
	}
	if c.TempBytes == 0 {
		c.TempBytes = 256 << 20
	}
	if c.Threads == 0 {
		c.Threads = 1
	}
	if c.Timeout < time.Second || c.Timeout > 2*time.Minute || c.MemoryBytes < 32<<20 || c.MemoryBytes > 1<<30 ||
		c.TempBytes < 1<<20 || c.TempBytes > 1<<30 || c.Threads < 1 || c.Threads > 4 {
		return nil, errors.New("invalid bounded DuckDB configuration")
	}
	converter := &Converter{config: c, slot: make(chan struct{}, 1)}
	var version []struct{ Version string }
	if err := converter.query(context.Background(), `SELECT version() AS Version`, &version); err != nil {
		return nil, err
	}
	if len(version) != 1 || version[0].Version != DuckDBVersion {
		return nil, fmt.Errorf("DuckDB runtime must be exactly %s", DuckDBVersion)
	}
	return converter, nil
}

type boundedOutput struct {
	bytes.Buffer
	limit int
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.limit {
		return 0, ErrLimit
	}
	return b.Buffer.Write(p)
}

func literal(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func (c *Converter) query(ctx context.Context, sql string, target any) error {
	select {
	case c.slot <- struct{}{}:
		defer func() { <-c.slot }()
	default:
		return ErrBusy
	}
	ctx, cancel := context.WithTimeout(ctx, c.config.Timeout)
	defer cancel()
	temp, err := os.MkdirTemp("", "cao-archive-duckdb-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(temp) }()
	settings := fmt.Sprintf("SET memory_limit='%dB'; SET threads=%d; SET temp_directory=%s; SET max_temp_directory_size='%dB'; SET autoinstall_known_extensions=false; SET autoload_known_extensions=false; ",
		c.config.MemoryBytes, c.config.Threads, literal(temp), c.config.TempBytes)
	if target != nil {
		sql = "COPY (" + sql + ") TO '/dev/stdout' (FORMAT JSON, ARRAY true)"
	}
	// Only package-owned plans reach this subprocess. This is not an SQL sandbox.
	cmd := exec.CommandContext(ctx, c.config.DuckDBPath, "-batch", "-bail", "-init", os.DevNull, "-json", ":memory:")
	cmd.Stdin = strings.NewReader(settings + sql + ";\n")
	stdout, stderr := &boundedOutput{limit: 32 << 20}, &boundedOutput{limit: 16 << 10}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if stdout.Len() >= stdout.limit {
			return ErrLimit
		}
		// CLI diagnostics may contain approved payloads and paths.
		return fmt.Errorf("bounded DuckDB execution failed: %w", err)
	}
	if target == nil {
		return nil
	}
	return decode(stdout.Bytes(), target)
}

func decode(content []byte, target any) error {
	d := json.NewDecoder(bytes.NewReader(content))
	d.UseNumber()
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return err
	}
	if d.Decode(new(any)) != io.EOF {
		return ErrIntegrity
	}
	return nil
}

func validateProjection(p Projection) error {
	if !validIdentity(p.Namespace, p.Repository, p.RunID, p.Attempt) || len(p.Events) == 0 || len(p.Events) > 32768 ||
		len(p.ID) != 64 || len(p.Version) != 64 {
		return ErrIntegrity
	}
	for i, e := range p.Events {
		if e.Namespace != p.Namespace || e.Repository != p.Repository || e.RunID != p.RunID ||
			e.RunAttempt != p.Attempt || e.TraceID != p.ID || e.ProjectionID != p.Version ||
			e.ContractVersion != ContractVersion || e.Sequence != int64(i) || !json.Valid([]byte(e.PayloadJSON)) ||
			e.ContentDigest != hash([]byte(e.PayloadJSON)) || e.ArtifactDigest != p.ArtifactDigest ||
			e.PolicyDigest != p.PolicyDigest || e.CompletionDay != p.Day {
			return ErrIntegrity
		}
	}
	return nil
}

// Write creates an immutable, repository/day-homogeneous packed file using the
// TypeSpec-generated explicit column types. Existing destinations are refused.
func (c *Converter) Write(ctx context.Context, path string, projections []Projection) (result File, err error) {
	if len(projections) == 0 {
		return File{}, ErrIntegrity
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		return File{}, errors.New("Parquet destination already exists or is unreadable")
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return File{}, err
	}
	input, err := os.CreateTemp(filepath.Dir(path), ".conversion-*.jsonl")
	if err != nil {
		return File{}, err
	}
	defer func() { _ = input.Close(); _ = os.Remove(input.Name()) }()
	defer func() {
		if err != nil {
			_ = os.Remove(path)
		}
	}()
	encoder := json.NewEncoder(input)
	seen := map[string]bool{}
	var inputBytes int64
	for _, p := range projections {
		if err := validateProjection(p); err != nil {
			return File{}, err
		}
		first := projections[0]
		if p.Namespace != first.Namespace || p.Repository != first.Repository || p.Day != first.Day || seen[p.ID] {
			return File{}, ErrIntegrity
		}
		seen[p.ID] = true
		for _, event := range p.Events {
			inputBytes += int64(len(event.PayloadJSON))
			if inputBytes > 128<<20 {
				return File{}, ErrCapacity
			}
			if err := encoder.Encode(event); err != nil {
				return File{}, err
			}
		}
		result.Members = append(result.Members, p.Member())
		result.Rows += int64(len(p.Events))
	}
	if err := input.Sync(); err != nil {
		return File{}, err
	}
	if err := input.Close(); err != nil {
		return File{}, err
	}
	source := fmt.Sprintf("read_json(%s, format='newline_delimited', columns=%s, maximum_object_size=67108864)", literal(input.Name()), eventJSONColumns)
	sql := fmt.Sprintf("COPY (SELECT %s FROM %s ORDER BY RunId, RunAttempt, Sequence) TO %s (FORMAT PARQUET, COMPRESSION ZSTD, ROW_GROUP_SIZE 122880)", eventSelect, source, literal(path))
	if err := c.query(ctx, sql, nil); err != nil {
		return File{}, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return File{}, err
	}
	file, err := os.Open(path)
	if err != nil {
		return File{}, err
	}
	defer func() { _ = file.Close() }()
	if err := file.Sync(); err != nil {
		return File{}, err
	}
	content, err := io.ReadAll(io.LimitReader(file, 256<<20+1))
	if err != nil || len(content) > 256<<20 {
		return File{}, ErrCapacity
	}
	result.Path, result.Digest, result.Bytes = path, hash(content), int64(len(content))
	if err := c.Verify(ctx, path, result); err != nil {
		return File{}, err
	}
	return result, nil
}

func (c *Converter) Verify(ctx context.Context, path string, expected File) error {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() != expected.Bytes || info.Size() > 256<<20 {
		return ErrIntegrity
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	content, err := io.ReadAll(io.LimitReader(file, expected.Bytes+1))
	if err != nil || hash(content) != expected.Digest {
		return ErrIntegrity
	}
	var columns []struct {
		Name    string `json:"column_name"`
		Type    string `json:"column_type"`
		Null    string `json:"null"`
		Key     any    `json:"key"`
		Default any    `json:"default"`
		Extra   any    `json:"extra"`
	}
	if err := c.query(ctx, "SELECT * FROM (DESCRIBE SELECT * FROM read_parquet("+literal(path)+", hive_partitioning=false))", &columns); err != nil {
		return err
	}
	if len(columns) != len(eventColumns) {
		return ErrIntegrity
	}
	for i, column := range columns {
		if column.Name != eventColumns[i] || column.Type != eventSQLTypes[i] {
			return ErrIntegrity
		}
	}
	var members []struct {
		Namespace, Repository, RunID, TraceID, ProjectionID string
		RunAttempt, Events, First, Last, Unique             int64
		Valid                                               bool
	}
	sql := `SELECT Namespace,Repository,RunId AS RunID,RunAttempt,TraceId AS TraceID,ProjectionId AS ProjectionID,
count(*) AS Events,min(Sequence) AS First,max(Sequence) AS Last,count(DISTINCT Sequence) AS Unique,
bool_and(ContractVersion=1 AND sha256(PayloadJSON)=ContentDigest AND json_valid(PayloadJSON)) AS Valid
FROM read_parquet(` + literal(path) + `, hive_partitioning=false)
GROUP BY Namespace,Repository,RunId,RunAttempt,TraceId,ProjectionId ORDER BY RunId,RunAttempt`
	if err := c.query(ctx, sql, &members); err != nil {
		return err
	}
	if len(members) != len(expected.Members) {
		return ErrIntegrity
	}
	var rows int64
	for _, actual := range members {
		m := Member{actual.Namespace, actual.Repository, actual.RunID, actual.RunAttempt, actual.TraceID, actual.ProjectionID, actual.Events}
		if !slices.Contains(expected.Members, m) || !actual.Valid || actual.First != 0 || actual.Last != actual.Events-1 || actual.Unique != actual.Events {
			return ErrIntegrity
		}
		rows += actual.Events
	}
	if rows != expected.Rows {
		return ErrIntegrity
	}
	return nil
}

func memberKey(m Member) string {
	return m.Namespace + "\x00" + m.Repository + "\x00" + m.RunID + "\x00" + strconv.FormatInt(m.Attempt, 10)
}
