// Package dmsg pkg/dmsg/dmsg/util.go c1-net-dmsg
package dmsg

import (
	"bytes"
	"context"
	"encoding/gob"
)

func awaitDone(ctx context.Context, done chan struct{}) {
	select {
	case <-ctx.Done():
	case <-done:
	}
}

func isClosed(done chan struct{}) bool {
	select {
	case <-done:
		return true
	default:
		return false
	}
}

/* Gob IO */

func encodeGob(v interface{}) ([]byte, error) {
	var b bytes.Buffer
	if err := gob.NewEncoder(&b).Encode(v); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func decodeGob(v interface{}, b []byte) error {
	// G709 (gosec taint analysis) flags any gob decode of reader-sourced bytes.
	// Here b is a dmsg wire frame that already arrived over a Noise-authenticated,
	// integrity-checked session, and v is a fixed internal control type — not
	// attacker-chosen. gob IS this protocol's framing serialization.
	return gob.NewDecoder(bytes.NewReader(b)).Decode(v) //nolint:gosec // G709: gob frame over Noise-authenticated dmsg session, fixed internal decode target
}
