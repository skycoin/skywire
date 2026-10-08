//go:build !tinygo || (js && wasm)

// Package router pkg/router/setup_conn_lost_test.go c2-net-routing
package router

import (
	"errors"
	"fmt"
	"io"
	"net/rpc"
	"testing"
)

// A cascade whose setup node connection closed is told apart from one the
// setup node refused, so the dial retries on a fresh connection.
func TestSetupConnLost(t *testing.T) {
	for _, err := range []error{rpc.ErrShutdown, io.EOF, io.ErrUnexpectedEOF} {
		if !setupConnLost(fmt.Errorf("cascade: sign install RPC: %w", err)) {
			t.Fatalf("%v not treated as a lost connection", err)
		}
	}
	if setupConnLost(errors.New("cascade: fwd reserve rejected: RSN signature verification failed")) {
		t.Fatal("a refusal treated as a lost connection")
	}
}
