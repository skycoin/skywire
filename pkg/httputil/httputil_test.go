package httputil

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"testing"
)

// TestIsIOErrorShortWriteVariants pins the fix for the production
// panic loop where http.timeoutWriter's "short write: i/o deadline
// reached" failed errors.Is(io.ErrShortWrite) (different sentinel)
// and slipped past the discriminator, causing WriteJSON to panic
// rather than return on a slow-client write timeout.
func TestIsIOErrorShortWriteVariants(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"random unrelated", errors.New("malformed UTF-8"), false},

		// sentinels: must keep working
		{"io.ErrShortWrite sentinel", io.ErrShortWrite, true},
		{"io.ErrClosedPipe sentinel", io.ErrClosedPipe, true},
		{"net.ErrClosed sentinel", net.ErrClosed, true},
		{"os.ErrDeadlineExceeded sentinel", os.ErrDeadlineExceeded, true},
		{"wrapped io.ErrShortWrite", fmt.Errorf("foo: %w", io.ErrShortWrite), true},

		// the production case: same message, different identity
		{"http.timeoutWriter short-write text", errors.New("short write: i/o deadline reached"), true},
		{"i/o timeout text", errors.New("write tcp 10.0.0.1:443: i/o timeout"), true},
		{"deadline exceeded text", errors.New("context deadline exceeded"), true},

		// existing string-match fallbacks still work
		{"broken pipe text", errors.New("write tcp: broken pipe"), true},
		{"connection reset text", errors.New("read tcp: connection reset by peer"), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := isIOError(c.err)
			if got != c.want {
				t.Errorf("isIOError(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}
