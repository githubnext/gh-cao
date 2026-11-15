package sessionarchive

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/githubnext/gh-cao/server/internal/adxtrace"
)

func hash(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func Project(config adxtrace.Config, namespace, repository, runID, day string, attempt int64, artifact []byte, now time.Time) (Projection, error) {
	if err := config.Validate(); err != nil {
		return Projection{}, err
	}
	if !config.Enabled || !slices.Contains(config.Repositories, repository) || !validIdentity(namespace, repository, runID, attempt) {
		return Projection{}, ErrIntegrity
	}
	e, err := adxtrace.Project(config, repository, runID, day, attempt, artifact, now)
	if err != nil {
		return Projection{}, err
	}
	return FromEvidence(namespace, e)
}

// FromEvidence reconstructs the approved logical event without another parser
// or approval policy; transport chunks are verified before joining.
func FromEvidence(namespace string, e adxtrace.Evidence) (Projection, error) {
	if !validIdentity(namespace, e.Repository, e.RunID, e.Attempt) || e.Version == "" ||
		e.EvidenceVersion != adxtrace.PreservationVersion || len(e.EventOmissions) != len(e.Tables["TraceEvents"]) {
		return Projection{}, ErrIntegrity
	}
	p := Projection{Namespace: namespace, Repository: e.Repository, RunID: e.RunID, Attempt: e.Attempt,
		ID: e.ID, Version: hash([]byte(fmt.Sprintf("cao-parquet-events-%d\x00%s", ContractVersion, e.Version))),
		Day: e.Day, ArtifactDigest: e.ArtifactDigest, PolicyDigest: e.PolicyDigest,
		CapturedAt: e.CapturedAt, Gaps: slices.Clone(e.Gaps)}
	parts := e.Tables["TraceContentParts"]
	partIndex, projectedBytes := 0, 0
	for i, row := range e.Tables["TraceEvents"] {
		integer := func(name string) int64 {
			n, _ := strconv.ParseInt(fmt.Sprint(row[name]), 10, 64)
			return n
		}
		if integer("Sequence") != int64(i) || integer("PartCount") < 1 {
			return Projection{}, ErrIntegrity
		}
		var content strings.Builder
		for j := int64(0); j < integer("PartCount"); j++ {
			if partIndex >= len(parts) || fmt.Sprint(parts[partIndex]["Sequence"]) != strconv.Itoa(i) ||
				fmt.Sprint(parts[partIndex]["Part"]) != strconv.FormatInt(j, 10) {
				return Projection{}, ErrIntegrity
			}
			text, ok := parts[partIndex]["Content"].(string)
			if !ok {
				return Projection{}, ErrIntegrity
			}
			content.WriteString(text)
			partIndex++
		}
		if hash([]byte(content.String())) != row["ContentDigest"] || !json.Valid([]byte(content.String())) {
			return Projection{}, ErrIntegrity
		}
		event := Event{Sequence: int64(i), Type: fmt.Sprint(row["Type"]), ContentDigest: hash([]byte(content.String())),
			EvidenceVersion: e.EvidenceVersion, OmissionReasons: slices.Clone(e.EventOmissions[i])}
		event.Namespace, event.Repository, event.RunID, event.RunAttempt = namespace, e.Repository, e.RunID, e.Attempt
		event.TraceID, event.ProjectionID, event.ContractVersion = e.ID, p.Version, ContractVersion
		event.CompletionDay, event.ArtifactDigest, event.PolicyDigest = e.Day, e.ArtifactDigest, e.PolicyDigest
		event.CapturedAt, event.Gaps, event.PayloadJSON = e.CapturedAt.UTC().Format(time.RFC3339Nano), slices.Clone(e.Gaps), content.String()
		if event.Gaps == nil {
			event.Gaps = []string{}
		}
		if err := projectEventFields(&event); err != nil {
			return Projection{}, err
		}
		encoded, err := json.Marshal(event)
		if err != nil {
			return Projection{}, err
		}
		projectedBytes += len(encoded)
		if projectedBytes > 128<<20 {
			return Projection{}, ErrCapacity
		}
		p.Events = append(p.Events, event)
	}
	if len(p.Events) == 0 || partIndex != len(parts) {
		return Projection{}, ErrIntegrity
	}
	return p, nil
}

func omitted(path string, reasons []string) bool {
	for _, reason := range reasons {
		source, kind, _ := strings.Cut(reason, ":")
		if path == source || kind == "excluded" && strings.HasPrefix(path, source+".") {
			return true
		}
	}
	return false
}

func evidenceInteger(value json.Number) (int64, bool) {
	text := string(value)
	negative := strings.HasPrefix(text, "-")
	text = strings.TrimPrefix(text, "-")
	mantissa, exponent := text, "0"
	if i := strings.IndexAny(text, "eE"); i >= 0 {
		mantissa, exponent = text[:i], text[i+1:]
	}
	fraction := 0
	if i := strings.IndexByte(mantissa, '.'); i >= 0 {
		fraction = len(mantissa) - i - 1
		mantissa = mantissa[:i] + mantissa[i+1:]
	}
	digits := strings.TrimLeft(mantissa, "0")
	if digits == "" {
		return 0, true
	}
	scale, err := strconv.ParseInt(exponent, 10, 64)
	if err != nil || scale < -int64(len(digits)) || scale > int64(fraction)+19 {
		return 0, false
	}
	scale -= int64(fraction)
	if scale < 0 {
		if -scale > int64(len(digits)-len(strings.TrimRight(digits, "0"))) {
			return 0, false
		}
		digits = digits[:int64(len(digits))+scale]
		scale = 0
	}
	if int64(len(digits))+scale > 19 {
		return 0, false
	}
	digits += strings.Repeat("0", int(scale))
	if negative {
		digits = "-" + digits
	}
	n, err := strconv.ParseInt(digits, 10, 64)
	return n, err == nil
}

func evidenceScalar(value any, field evidenceField) (any, bool) {
	switch field.Kind {
	case "string":
		text, ok := value.(string)
		return text, ok
	case "bool":
		flag, ok := value.(bool)
		return flag, ok
	case "long":
		number, ok := value.(json.Number)
		if !ok {
			return nil, false
		}
		n, ok := evidenceInteger(number)
		if field.Validation == "count" {
			ok = ok && n >= 0 && n <= 9007199254740991
		}
		return n, ok
	case "real":
		number, ok := value.(json.Number)
		if !ok {
			return nil, false
		}
		n, err := number.Float64()
		ok = err == nil && !math.IsInf(n, 0) && !math.IsNaN(n)
		if field.Validation == "duration" {
			ok = ok && n >= 0
		}
		return n, ok
	case "object":
		_, ok := value.(map[string]any)
		return nil, ok
	case "array":
		_, ok := value.([]any)
		return nil, ok
	case "any":
		return nil, true
	default:
		return nil, false
	}
}

// Typed values and field states come only from the approved, redacted payload.
// The generated bindings also own structured-family state, not another dataset.
func projectEventFields(event *Event) error {
	var payload map[string]any
	if err := decode([]byte(event.PayloadJSON), &payload); err != nil || payload == nil {
		return ErrIntegrity
	}
	if payload["type"] != event.Type {
		return ErrIntegrity
	}
	event.PresentFields, event.NullFields, event.InvalidFields = []string{}, []string{}, []string{}
	if event.OmissionReasons == nil {
		event.OmissionReasons = []string{}
	}
	target := reflect.ValueOf(event).Elem()
	for _, field := range evidenceFields {
		if field.Index >= 0 {
			target.Field(field.Index).SetZero()
		}
		value, present, invalidParent := adxtrace.EvidenceValue(payload, field.Path)
		if present {
			event.PresentFields = append(event.PresentFields, field.Name)
		}
		if present && value == nil {
			event.NullFields = append(event.NullFields, field.Name)
			continue
		}
		if omitted(field.Path, event.OmissionReasons) {
			continue
		}
		typed, valid := evidenceScalar(value, field)
		if invalidParent || present && !valid {
			event.InvalidFields = append(event.InvalidFields, field.Name)
		} else if present && field.Index >= 0 {
			column := target.Field(field.Index)
			if column.Kind() == reflect.Pointer {
				pointer := reflect.New(column.Type().Elem())
				pointer.Elem().Set(reflect.ValueOf(typed))
				column.Set(pointer)
			} else {
				column.Set(reflect.ValueOf(typed))
			}
		}
	}
	return nil
}

// ValidateEvent verifies generated typed values and source states against the
// approved payload. Physical empty strings are qualified by field metadata.
func ValidateEvent(event Event) error {
	if event.ContractVersion != ContractVersion || event.EvidenceVersion != adxtrace.PreservationVersion ||
		event.ContentDigest != hash([]byte(event.PayloadJSON)) {
		return ErrIntegrity
	}
	if len(event.OmissionReasons) > 2*len(adxtrace.EvidencePaths) {
		return ErrIntegrity
	}
	for _, reason := range event.OmissionReasons {
		path, kind, ok := strings.Cut(reason, ":")
		if !ok || !slices.Contains(adxtrace.EvidencePaths, path) || !slices.Contains([]string{"excluded", "redacted"}, kind) {
			return ErrIntegrity
		}
	}
	expected := event
	if err := projectEventFields(&expected); err != nil {
		return err
	}
	if !reflect.DeepEqual(event, expected) {
		return ErrIntegrity
	}
	return nil
}

func (p Projection) Member() Member {
	return Member{p.Namespace, p.Repository, p.RunID, p.Attempt, p.ID, p.Version, int64(len(p.Events))}
}
