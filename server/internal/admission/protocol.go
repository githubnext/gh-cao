// Package admission implements the cooperative gh-aw gate and completion receipt.
// It never grants runtime credentials, reserves funds, or mutates billing state.
package admission

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

var (
	ErrDenied      = errors.New("admission denied")
	ErrUnavailable = errors.New("admission dependency unavailable")
	decimal        = regexp.MustCompile(`^[1-9][0-9]*$`)
	repository     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}/[A-Za-z0-9_.-]{1,100}$`)
	workflowHash   = regexp.MustCompile(`^[0-9a-f]{64}:[0-9a-f]{64}$`)
	revision       = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

type Request struct {
	Repo            string `json:"repo"`
	WorkflowID      string `json:"workflow_id"`
	WorkflowHash    string `json:"workflow_hash"`
	WorkflowCallID  string `json:"workflow_call_id"`
	RunID           string `json:"run_id"`
	RunAttempt      string `json:"run_attempt"`
	Ref             string `json:"ref"`
	EventType       string `json:"event_type"`
	Actor           string `json:"actor"`
	TriggeringActor string `json:"triggering_actor"`
}

type Allowances struct {
	Primary         int32  `json:"max_ai_credits"`
	ThreatDetection *int32 `json:"max_ai_credits_threat_detection,omitempty"`
	Evals           *int32 `json:"max_ai_credits_evals,omitempty"`
}

func (a Allowances) valid() bool {
	return a.Primary > 0 && (a.ThreatDetection == nil || *a.ThreatDetection > 0) &&
		(a.Evals == nil || *a.Evals > 0)
}

// strictJSON rejects decoded duplicate names and unpaired surrogate escapes,
// which encoding/json otherwise silently replaces with U+FFFD.
func strictJSON(data []byte, target any) error {
	if !utf8.Valid(data) || !scalarEscapes(data) {
		return ErrDenied
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := scanJSON(decoder); err != nil {
		return ErrDenied
	}
	if _, err := decoder.Token(); err != io.EOF {
		return ErrDenied
	}
	decoder = json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return ErrDenied
	}
	return nil
}

func scanJSON(d *json.Decoder) error {
	t, err := d.Token()
	if err != nil {
		return err
	}
	switch t {
	case json.Delim('{'):
		seen := map[string]bool{}
		for d.More() {
			name, err := d.Token()
			if err != nil {
				return err
			}
			key, ok := name.(string)
			if !ok || seen[key] {
				return ErrDenied
			}
			seen[key] = true
			if err := scanJSON(d); err != nil {
				return err
			}
		}
		_, err = d.Token()
	case json.Delim('['):
		for d.More() {
			if err := scanJSON(d); err != nil {
				return err
			}
		}
		_, err = d.Token()
	}
	return err
}

// hexQuad decodes the four hexadecimal digits at data[start:start+4].
func hexQuad(data []byte, start int) (uint16, bool) {
	if start < 0 || start+4 > len(data) {
		return 0, false
	}
	var n uint16
	for _, b := range data[start : start+4] {
		n <<= 4
		switch {
		case b >= '0' && b <= '9':
			n += uint16(b - '0')
		case b >= 'a' && b <= 'f':
			n += uint16(b-'a') + 10
		case b >= 'A' && b <= 'F':
			n += uint16(b-'A') + 10
		default:
			return 0, false
		}
	}
	return n, true
}

func scalarEscapes(data []byte) bool {
	for i := 0; i < len(data); i++ {
		if data[i] != '\\' {
			continue
		}
		i++
		if i >= len(data) {
			return false
		}
		if data[i] != 'u' {
			continue
		}
		n, ok := hexQuad(data, i+1)
		if !ok {
			return false
		}
		i += 4
		if n >= 0xdc00 && n <= 0xdfff {
			return false
		}
		if n < 0xd800 || n > 0xdbff {
			continue
		}
		if i+6 >= len(data) || data[i+1] != '\\' || data[i+2] != 'u' {
			return false
		}
		n, ok = hexQuad(data, i+3)
		if !ok || n < 0xdc00 || n > 0xdfff {
			return false
		}
		i += 6
	}
	return true
}

func clean(s string, limit int) bool {
	return s != "" && len(s) <= limit && !strings.ContainsFunc(s, unicode.IsControl)
}

func (r Request) valid() bool {
	for _, field := range []struct {
		value string
		limit int
	}{
		{r.Repo, 140}, {r.WorkflowID, 1024}, {r.WorkflowCallID, 1400},
		{r.RunID, 128}, {r.RunAttempt, 128}, {r.Ref, 1024},
		{r.EventType, 100}, {r.Actor, 100}, {r.TriggeringActor, 100},
	} {
		if !clean(field.value, field.limit) {
			return false
		}
	}
	return repository.MatchString(r.Repo) && decimal.MatchString(r.RunID) &&
		decimal.MatchString(r.RunAttempt) && workflowHash.MatchString(r.WorkflowHash) &&
		r.WorkflowCallID == r.RunID+"-"+r.RunAttempt+":"+r.WorkflowID
}

func (r Request) matches(i Identity) bool {
	return r.Repo == i.Repository && r.WorkflowID == i.WorkflowRef &&
		r.RunID == i.RunID && r.RunAttempt == i.RunAttempt &&
		r.Ref == i.Ref && r.EventType == i.Event && r.Actor == i.Actor
}

func decodeRequest(data []byte) (Request, error) {
	var fields map[string]json.RawMessage
	if strictJSON(data, &fields) != nil || len(fields) != 10 {
		admissionLog.Printf("admission request rejected: reason=invalid_envelope bytes=%d", len(data))
		return Request{}, ErrDenied
	}
	r := Request{}
	for name, target := range map[string]*string{
		"repo": &r.Repo, "workflow_id": &r.WorkflowID, "workflow_hash": &r.WorkflowHash,
		"workflow_call_id": &r.WorkflowCallID, "run_id": &r.RunID, "run_attempt": &r.RunAttempt,
		"ref": &r.Ref, "event_type": &r.EventType, "actor": &r.Actor, "triggering_actor": &r.TriggeringActor,
	} {
		if json.Unmarshal(fields[name], target) != nil {
			admissionLog.Printf("admission request rejected: reason=invalid_field_type")
			return Request{}, ErrDenied
		}
	}
	if !r.valid() {
		admissionLog.Printf("admission request rejected: reason=invalid_field_value")
		return Request{}, ErrDenied
	}
	return r, nil
}
