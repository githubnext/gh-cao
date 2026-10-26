// Package auditdelivery replays committed audit rows to an independently
// administered destination. It owns no queue or application-store writes.
package auditdelivery

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/githubnext/gh-cao/server/internal/logger"
	"github.com/githubnext/gh-cao/server/internal/operational"
)

var deliveryLog = logger.New("cao:auditdelivery")

var streamPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_-]{0,127}$`)

// Destination must durably create immutable objects and atomically replace a
// checkpoint only when its previous version still matches. A read-back receipt
// is required; an OTLP emission or asynchronous acceptance is not a receipt.
type Destination interface {
	Load(context.Context, string) (Checkpoint, string, bool, error)
	Create(context.Context, string, []byte) error
	Commit(context.Context, string, Checkpoint, string) error
}

type Reader interface {
	ListAudit(context.Context, operational.AuditFilter) ([]operational.AuditEvent, error)
	VerifyAudit(context.Context, ...operational.AuditAnchor) (operational.AuditVerification, error)
}

// Anchor is used only for explicit first-time enrollment. Obtain and preserve
// it independently before delivery, not by trusting the current database anew.
type Anchor struct {
	Stream     string                  `json:"stream"`
	Head       operational.AuditAnchor `json:"head"`
	ObservedAt time.Time               `json:"observed_at"`
}

type Checkpoint struct {
	Version      int                           `json:"version"`
	Stream       string                        `json:"stream"`
	Delivered    operational.AuditAnchor       `json:"delivered"`
	Verification operational.AuditVerification `json:"verification"`
	VerifiedAt   time.Time                     `json:"verified_at"`
}

type Envelope struct {
	Version      int                    `json:"version"`
	Stream       string                 `json:"stream"`
	Event        operational.AuditEvent `json:"event"`
	PreviousHash string                 `json:"previous_hash"`
	Hash         string                 `json:"hash"`
}

type Options struct {
	Stream       string
	MaxEvents    int
	MaxAnchorAge time.Duration
	Bootstrap    *Anchor
}

// Result contains only bounded operational metadata, never event payloads,
// destination URLs, or credentials. Remaining is relative to the verified head.
type Result struct {
	Status       string `json:"status"`
	Stage        string `json:"stage"`
	Delivered    int    `json:"delivered"`
	LastSequence int64  `json:"last_sequence"`
	VerifiedHead int64  `json:"verified_head"`
	Remaining    int64  `json:"remaining"`
}

func (o Options) Validate() error {
	if !streamPattern.MatchString(o.Stream) {
		return errors.New("audit delivery requires a 1-128 character alphanumeric, underscore, or hyphen stream ID")
	}
	if o.MaxEvents < 1 || o.MaxEvents > operational.AuditListLimit {
		return errors.New("audit delivery max-events must be between 1 and 1000")
	}
	if o.MaxAnchorAge < time.Minute || o.MaxAnchorAge > 7*24*time.Hour {
		return errors.New("audit delivery max-anchor-age must be between 1m and 168h")
	}
	return nil
}

// Run handles at most one bounded page and one checkpoint. Every retry starts
// from the destination checkpoint; partial deliveries replay create-only keys.
func Run(ctx context.Context, source Reader, destination Destination, options Options) (result Result, err error) {
	result.Status, result.Stage = "failure", "configuration"
	defer func() {
		deliveryLog.Printf("delivery finished: status=%s stage=%s delivered=%d last_sequence=%d remaining=%d",
			result.Status, result.Stage, result.Delivered, result.LastSequence, result.Remaining)
	}()
	if err := options.Validate(); err != nil {
		return result, err
	}
	result.Stage = "checkpoint_read"
	checkpoint, version, exists, err := destination.Load(ctx, options.Stream)
	if err != nil {
		return result, err
	}
	result.Stage = "anchor"
	now := time.Now().UTC()
	var head operational.AuditAnchor
	if exists {
		if err := validateCheckpoint(checkpoint, options, now); err != nil {
			return result, err
		}
		head = operational.AuditAnchor{Sequence: checkpoint.Verification.LastSequence, Hash: checkpoint.Verification.LastHash}
	} else {
		if options.Bootstrap == nil {
			return result, errors.New("audit delivery checkpoint missing; explicit independent bootstrap anchor required")
		}
		anchor := options.Bootstrap
		if anchor.Stream != options.Stream || !fresh(anchor.ObservedAt, now, options.MaxAnchorAge) || !validAnchor(anchor.Head) {
			return result, errors.New("audit delivery bootstrap anchor is invalid, stale, or belongs to another stream")
		}
		head = anchor.Head
		checkpoint = Checkpoint{Version: 1, Stream: options.Stream,
			Delivered: operational.AuditAnchor{Hash: hex.EncodeToString(operational.AuditGenesis())}}
	}
	result.LastSequence = checkpoint.Delivered.Sequence
	result.Stage = "source_read"
	events, err := source.ListAudit(ctx, operational.AuditFilter{AfterSequence: checkpoint.Delivered.Sequence, Limit: options.MaxEvents})
	if err != nil {
		return result, errors.New("audit delivery source read failed")
	}
	if len(events) > options.MaxEvents {
		return result, errors.New("audit delivery source exceeded the page bound")
	}
	result.Stage = "continuity"
	envelopes, end, err := chain(options.Stream, checkpoint.Delivered, events)
	if err != nil {
		return result, err
	}
	if end.Sequence == 0 {
		return result, errors.New("audit delivery enrollment requires committed history beginning at sequence 1")
	}
	anchors := verificationAnchors(head, checkpoint.Delivered, end)
	result.Stage = "verification"
	verified, err := source.VerifyAudit(ctx, anchors...)
	if err != nil {
		return result, errors.New("audit delivery anchored verification failed or was interrupted")
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	// VerifyAudit permits expired anchors for diagnostic use. Delivery must not.
	if !coversPage(verified, checkpoint.Delivered, end) {
		return result, errors.New("audit delivery retention gap or expired anchor")
	}
	result.VerifiedHead = verified.LastSequence
	result.Remaining = verified.LastSequence - end.Sequence
	next := Checkpoint{Version: 1, Stream: options.Stream, Delivered: end,
		Verification: verified, VerifiedAt: time.Now().UTC()}
	result.Stage = "event_delivery"
	for _, envelope := range envelopes {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		body, err := json.Marshal(envelope)
		if err != nil {
			return result, errors.New("audit delivery event encoding failed")
		}
		key := fmt.Sprintf("%s/events/%020d.json", options.Stream, envelope.Event.Sequence)
		if err := destination.Create(ctx, key, body); err != nil {
			return result, err
		}
		result.Delivered++
	}
	// Preserve every successful verification outside the mutable checkpoint,
	// including idle runs and results whose checkpoint CAS later loses a race.
	result.Stage = "verification_delivery"
	evidence, err := json.Marshal(next)
	if err != nil {
		return result, errors.New("audit delivery verification encoding failed")
	}
	digest := sha256.Sum256(evidence)
	key := fmt.Sprintf("%s/verifications/%x.json", options.Stream, digest)
	if err := destination.Create(ctx, key, evidence); err != nil {
		return result, err
	}
	result.Stage = "checkpoint_commit"
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := destination.Commit(ctx, options.Stream, next, version); err != nil {
		return result, err
	}
	result.LastSequence = end.Sequence
	result.Stage, result.Status = "complete", "success"
	return result, nil
}

// verificationAnchors lists the independent head plus the delivered and page-end
// anchors that actually advance past genesis or the previous delivery.
func verificationAnchors(head, delivered, end operational.AuditAnchor) []operational.AuditAnchor {
	anchors := []operational.AuditAnchor{head}
	if delivered.Sequence > 0 {
		anchors = append(anchors, delivered)
	}
	if end.Sequence > delivered.Sequence {
		anchors = append(anchors, end)
	}
	return anchors
}

// coversPage reports whether a verification used live anchors and retained
// history spanning from the first undelivered event through the page end.
func coversPage(verified operational.AuditVerification, delivered, end operational.AuditAnchor) bool {
	return verified.AnchorsExpired == 0 && verified.AnchorsChecked != 0 &&
		verified.FirstSequence <= delivered.Sequence+1 && verified.LastSequence >= end.Sequence
}

func validAnchor(anchor operational.AuditAnchor) bool {
	_, err := operational.NewAuditChainVerifier([]operational.AuditAnchor{anchor})
	return err == nil
}

func fresh(observed, now time.Time, maxAge time.Duration) bool {
	return !observed.IsZero() && !observed.After(now) && now.Sub(observed) <= maxAge
}

func validateCheckpoint(checkpoint Checkpoint, options Options, now time.Time) error {
	head := operational.AuditAnchor{Sequence: checkpoint.Verification.LastSequence, Hash: checkpoint.Verification.LastHash}
	if checkpoint.Version != 1 || checkpoint.Stream != options.Stream ||
		!fresh(checkpoint.VerifiedAt, now, options.MaxAnchorAge) ||
		!validAnchor(head) || !validAnchor(checkpoint.Delivered) ||
		checkpoint.Delivered.Sequence > head.Sequence {
		return errors.New("audit delivery checkpoint anchor is invalid, stale, or belongs to another stream")
	}
	return nil
}

func chain(stream string, previous operational.AuditAnchor, events []operational.AuditEvent) ([]Envelope, operational.AuditAnchor, error) {
	hash, err := hex.DecodeString(previous.Hash)
	if err != nil || len(hash) != sha256.Size {
		return nil, previous, errors.New("audit delivery cursor hash is invalid")
	}
	envelopes := make([]Envelope, 0, len(events))
	for _, event := range events {
		if event.Sequence != previous.Sequence+1 {
			return nil, previous, errors.New("audit delivery sequence gap; retained events cannot cover the checkpoint")
		}
		record, err := operational.CanonicalAuditRecord(event)
		if err != nil {
			return nil, previous, errors.New("audit delivery source record is invalid")
		}
		hash = operational.AuditChainHash(hash, record)
		envelope := Envelope{Version: 1, Stream: stream, Event: event, PreviousHash: previous.Hash, Hash: hex.EncodeToString(hash)}
		envelopes = append(envelopes, envelope)
		previous = operational.AuditAnchor{Sequence: event.Sequence, Hash: envelope.Hash}
	}
	return envelopes, previous, nil
}
