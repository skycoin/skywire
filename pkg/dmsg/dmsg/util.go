// Package dmsg pkg/dmsg/util.go
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
	return gob.NewDecoder(bytes.NewReader(b)).Decode(v)
}
