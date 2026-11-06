package hostedcli

import (
	"bytes"
	"context"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/githubnext/gh-cao/server/internal/operational"
)

// maxBufferedLine bounds frames and unresolved credential-shaped spans.
// Registered credentials additionally retain at most their longest prefix.
const maxBufferedLine = 8 * 1024

const suppressedOutput = "[REDACTED]\n[Output suppressed after an overlong credential-shaped span.]\n"

// CommandOutput applies the shared bounded, cross-frame credential redaction
// before native operator output enters the durable command queue.
type CommandOutput struct{ sink *outputSink }

func NewCommandOutput(ctx context.Context, queue operational.CLICommandQueue, lease operational.CLICommandLease, id int64) *CommandOutput {
	return &CommandOutput{sink: newOutputSink(ctx, queue, lease, id)}
}

func (o *CommandOutput) Write(stream string, data []byte) { o.sink.write(stream, data) }
func (o *CommandOutput) Close()                           { o.sink.close() }

// outputSink redacts each stream before line/size framing. Neither a frame
// boundary nor an interleaved write from the other stream ends a credential.
type outputSink struct {
	ctx     context.Context
	queue   operational.CLICommandQueue
	lease   operational.CLICommandLease
	id      int64
	mutex   sync.Mutex
	streams map[string]*outputStream
	secrets [][]byte
	stopped bool
}

func newOutputSink(ctx context.Context, queue operational.CLICommandQueue, lease operational.CLICommandLease, id int64) *outputSink {
	return &outputSink{ctx: ctx, queue: queue, lease: lease, id: id, streams: map[string]*outputStream{}}
}

func (s *outputSink) secret(value string) {
	if value == "" {
		return
	}
	s.mutex.Lock()
	s.secrets = append(s.secrets, []byte(value))
	s.mutex.Unlock()
}

func (s *outputSink) notice(text string) {
	s.write("stderr", []byte(text))
}

func (s *outputSink) write(stream string, data []byte) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.stopped {
		return
	}
	output := s.streams[stream]
	if output == nil {
		output = &outputStream{sink: s, name: stream}
		s.streams[stream] = output
	}
	for _, b := range data {
		if s.stopped || output.suppressed {
			return
		}
		// Normalize NUL before matching, rather than splicing a credential
		// together after it has already passed through redaction.
		if b != 0 {
			output.known = append(output.known, b)
			output.redactKnown(false)
		}
	}
}

func (s *outputSink) close() {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	for _, stream := range []string{"stdout", "stderr"} {
		if output := s.streams[stream]; output != nil && !s.stopped {
			output.redactKnown(true)
			for len(output.candidate) > 0 {
				output.finishCandidate()
			}
			output.flushFrame(true)
		}
	}
	s.stopped = true
	clear(s.streams)
	s.secrets = nil
}

func (s *outputSink) appendLocked(stream, text string) {
	if s.stopped {
		return
	}
	text = operational.RedactCLICommandText(text)
	if text == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(s.ctx), 10*time.Second)
	defer cancel()
	for len(text) > 0 {
		size := min(len(text), maxBufferedLine)
		for size < len(text) && !utf8.RuneStart(text[size]) {
			size--
		}
		if _, err := s.queue.AppendOutput(ctx, s.lease, s.id, stream, text[:size]); err != nil {
			// A canceled or finished command no longer accepts output.
			s.stopped = true
			return
		}
		text = text[size:]
	}
}

type outputStream struct {
	sink       *outputSink
	name       string
	known      []byte
	candidate  []byte
	frame      []byte
	state      credentialState
	suppressed bool
}

func (o *outputStream) redactKnown(final bool) {
	for len(o.known) > 0 {
		matched, pending := 0, false
		for _, secret := range o.sink.secrets {
			if len(o.known) < len(secret) && bytes.HasPrefix(secret, o.known) {
				pending = true
			} else if bytes.HasPrefix(o.known, secret) {
				matched = max(matched, len(secret))
			}
		}
		if pending {
			if !final {
				return
			}
			// EOF must not expose a truncated registered credential.
			matched = len(o.known)
		}
		if matched > 0 {
			for _, b := range []byte("[REDACTED]") {
				o.redactCredential(b)
			}
		} else {
			o.redactCredential(o.known[0])
			matched = 1
		}
		o.known = append(o.known[:0], o.known[matched:]...)
	}
}

type credentialState uint8

const (
	credentialPrefix credentialState = iota
	credentialWord
	credentialAWS
	credentialKeyHeader
	credentialFlag
	credentialBearerStart
	credentialBearerSpace
	credentialKeyStart
	credentialKeyQuoted
	credentialValueStart
	credentialValue
)

// These introducers retain possible matches of operational.RedactCLICommandText
// until their syntax ends. Complete spans still use that shared redactor.
// Matching introducers without word-boundary checks is deliberately conservative.
var credentialIntroducers = []struct {
	text  string
	state credentialState
}{
	{"ghp_", credentialWord}, {"gho_", credentialWord}, {"ghu_", credentialWord},
	{"ghs_", credentialWord}, {"ghr_", credentialWord}, {"github_pat_", credentialWord},
	{"akia", credentialAWS}, {"-----begin ", credentialKeyHeader},
	{"bearer", credentialBearerStart},
	{"token", credentialKeyStart}, {"secret", credentialKeyStart},
	{"password", credentialKeyStart}, {"credential", credentialKeyStart},
	{"authorization", credentialKeyStart}, {"privatekey", credentialKeyStart},
	{"private-key", credentialKeyStart}, {"private_key", credentialKeyStart},
	{"--", credentialFlag},
}

func (o *outputStream) redactCredential(b byte) {
	if o.suppressed {
		return
	}
	for o.state != credentialPrefix {
		if o.continueCredential(b) {
			o.retainCandidate(b)
			return
		}
		o.finishCandidate()
	}
	o.candidate = append(o.candidate, b)
	for len(o.candidate) > 0 {
		pending := false
		for _, introducer := range credentialIntroducers {
			matches, complete := foldedPrefix(introducer.text, o.candidate)
			if !matches {
				continue
			}
			pending = true
			if complete {
				o.state = introducer.state
				return
			}
		}
		if pending {
			return
		}
		o.emit(string(o.candidate[:1]))
		o.candidate = append(o.candidate[:0], o.candidate[1:]...)
	}
}

func foldedPrefix(text string, prefix []byte) (matches, complete bool) {
	for len(prefix) > 0 {
		if text == "" {
			return false, false
		}
		if !utf8.FullRune(prefix) {
			return true, false
		}
		r, width := utf8.DecodeRune(prefix)
		for folded := r; folded != rune(text[0]); {
			folded = unicode.SimpleFold(folded)
			if folded == r {
				return false, false
			}
		}
		prefix, text = prefix[width:], text[1:]
	}
	return true, text == ""
}

func credentialSpace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r' || b == '\f'
}

func credentialAlphanumeric(b byte) bool {
	return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}

func (o *outputStream) continueCredential(b byte) bool {
	switch o.state {
	case credentialWord, credentialAWS:
		return credentialAlphanumeric(b) || b == '_' || b >= utf8.RuneSelf
	case credentialKeyHeader:
		return b != '\n'
	case credentialFlag:
		if b == ' ' {
			if _, complete := foldedPrefix("-----begin", o.candidate); complete {
				o.state = credentialKeyHeader
				return true
			}
		}
		return credentialAlphanumeric(b) || b == '_' || b == '-' || b >= utf8.RuneSelf
	case credentialBearerStart:
		if !credentialSpace(b) {
			return false
		}
		o.state = credentialBearerSpace
	case credentialBearerSpace:
		if !credentialSpace(b) {
			o.state = credentialValue
		}
	case credentialKeyStart, credentialKeyQuoted:
		switch {
		case credentialSpace(b):
		case (b == '"' || b == '\'') && o.state == credentialKeyStart:
			o.state = credentialKeyQuoted
		case b == ':' || b == '=':
			o.state = credentialValueStart
		default:
			return false
		}
	case credentialValueStart:
		if !credentialSpace(b) {
			o.state = credentialValue
		}
	case credentialValue:
		return !credentialSpace(b)
	default:
		return false
	}
	return true
}

func (o *outputStream) retainCandidate(b byte) {
	if len(o.candidate) == maxBufferedLine {
		// A later suffix could itself introduce a credential spanning the
		// next delimiter. Stop this stream rather than guess where it ends.
		o.emit(suppressedOutput)
		o.candidate = o.candidate[:0]
		o.suppressed = true
		return
	}
	o.candidate = append(o.candidate, b)
}

func (o *outputStream) finishCandidate() {
	text := operational.RedactCLICommandText(string(o.candidate))
	o.candidate = o.candidate[:0]
	o.state = credentialPrefix
	if text != "" {
		// A rejected or partially redacted candidate can end in another
		// introducer (for example --authorization=...). Only its first
		// byte is resolved; rescan the rest before publishing it.
		o.emit(text[:1])
		for i := 1; i < len(text); i++ {
			o.redactCredential(text[i])
		}
	}
}

func (o *outputStream) emit(text string) {
	for i := range len(text) {
		o.frame = append(o.frame, text[i])
		if text[i] == '\n' || len(o.frame) >= maxBufferedLine {
			o.flushFrame(false)
		}
	}
}

func (o *outputStream) flushFrame(final bool) {
	for len(o.frame) > 0 {
		size, limit := 0, min(len(o.frame), maxBufferedLine)
		for size < limit {
			if !final && !utf8.FullRune(o.frame[size:]) {
				break
			}
			_, width := utf8.DecodeRune(o.frame[size:])
			if size+width > limit {
				break
			}
			size += width
		}
		if size == 0 {
			return
		}
		o.sink.appendLocked(o.name, string(o.frame[:size]))
		o.frame = append(o.frame[:0], o.frame[size:]...)
		if !final {
			return
		}
	}
}
