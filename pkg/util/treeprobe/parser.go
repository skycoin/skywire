// parser.go — NDJSON line decoder.
//
// Consumes a stream of envelope lines and emits one strongly-typed
// payload per call. The parser is buffer-aware (handles partial
// lines across Read boundaries) and forgiving of trailing whitespace
// + blank lines between events.

package treeprobe

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
)

// Parser wraps a bufio.Scanner over an NDJSON stream from
// `cli visor ping tree-stream`. Use NewParser to construct, then
// Next() in a loop until io.EOF.
type Parser struct {
	sc *bufio.Scanner
}

// NewParser constructs a Parser over r. Uses a 1 MiB scan buffer
// because PingResult events with long route arrays + error
// messages can run several KiB; default bufio buffer (64 KiB)
// would suffice today but bumping is cheap insurance against
// future field bloat.
func NewParser(r io.Reader) *Parser {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	return &Parser{sc: sc}
}

// Decoded is the parser's emit shape. Exactly one of the *Payload
// fields is non-nil per Decoded, matching Envelope.Type. The TS
// field is the RFC3339Nano timestamp string from the envelope —
// callers convert to time.Time on demand.
type Decoded struct {
	TS   string
	Type EventType

	Discovered   *Discovered
	PingResult   *PingResult
	LevelDone    *LevelDone
	RunDone      *RunDone
	StatusUpdate *StatusUpdate
	ServerError  *ServerError
}

// Next returns the next decoded event, io.EOF when the stream
// is exhausted, or a non-nil error on parse failure. Blank lines
// + trailing whitespace are skipped. Unknown Type values yield
// an error rather than silent skip — the spec is closed at 6
// event types and a new one shouldn't sneak past us undetected.
func (p *Parser) Next() (*Decoded, error) {
	for p.sc.Scan() {
		line := p.sc.Bytes()
		if len(line) == 0 {
			continue
		}
		// Skip lines that are all whitespace.
		isBlank := true
		for _, b := range line {
			if b != ' ' && b != '\t' && b != '\r' {
				isBlank = false
				break
			}
		}
		if isBlank {
			continue
		}

		var env Envelope
		if err := json.Unmarshal(line, &env); err != nil {
			return nil, fmt.Errorf("treeprobe: parse envelope: %w (line: %q)", err, truncate(line, 200))
		}
		return p.decodePayload(&env)
	}
	if err := p.sc.Err(); err != nil {
		return nil, fmt.Errorf("treeprobe: scan: %w", err)
	}
	return nil, io.EOF
}

// decodePayload unmarshals env.Data into the type-specific struct
// according to env.Type. Returns an error if Type is unrecognized
// or if the inner JSON is malformed for the declared type.
func (p *Parser) decodePayload(env *Envelope) (*Decoded, error) {
	d := &Decoded{TS: env.TS, Type: env.Type}

	var inner any
	switch env.Type {
	case TypeDiscovered:
		d.Discovered = &Discovered{}
		inner = d.Discovered
	case TypePingResult:
		d.PingResult = &PingResult{}
		inner = d.PingResult
	case TypeLevelDone:
		d.LevelDone = &LevelDone{}
		inner = d.LevelDone
	case TypeRunDone:
		d.RunDone = &RunDone{}
		inner = d.RunDone
	case TypeStatusUpdate:
		d.StatusUpdate = &StatusUpdate{}
		inner = d.StatusUpdate
	case TypeServerError:
		d.ServerError = &ServerError{}
		inner = d.ServerError
	default:
		return nil, fmt.Errorf("treeprobe: unknown event type %q", env.Type)
	}

	if err := json.Unmarshal(env.Data, inner); err != nil {
		return nil, fmt.Errorf("treeprobe: parse %s payload: %w (data: %q)",
			env.Type, err, truncate(env.Data, 200))
	}
	return d, nil
}

func truncate(b []byte, max int) []byte {
	if len(b) <= max {
		return b
	}
	out := make([]byte, max+3)
	copy(out, b[:max])
	copy(out[max:], "...")
	return out
}
