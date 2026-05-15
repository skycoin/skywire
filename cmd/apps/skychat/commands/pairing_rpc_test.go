// Package commands cmd/apps/skychat/commands/pairing_rpc_test.go
//
// Unit tests for the retry-on-shutdown helper. The watchdog and dial
// paths are integration-shaped (need a real TCP listener + visor RPC
// server) and are exercised by the e2e tests; this file focuses on
// the pure error-classification + retry-control logic.

package commands

import (
	"context"
	"errors"
	"net/rpc"
	"testing"

	"github.com/skycoin/skywire/pkg/visor"
)

func init() {
	// pairRPCDo logs the reconnect transition through appLog, which is
	// nil until RunSkychat wires it up. Install a no-op so the tests
	// can drive the retry path without RunSkychat.
	if appLog == nil {
		appLog = func(string, ...interface{}) {}
	}
}

func TestIsRPCShutdown(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"unrelated", errors.New("boom"), false},
		{"rpc.ErrShutdown direct", rpc.ErrShutdown, true},
		{"rpc.ErrShutdown wrapped", &wrapErr{rpc.ErrShutdown}, true},
		{"string form", errors.New("connection is shut down"), true},
		{"closed conn", errors.New("use of closed network connection"), true},
		{"deadline exceeded", context.DeadlineExceeded, true},
		{"non-shutdown timeout", errors.New("i/o timeout"), false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isRPCShutdown(c.err); got != c.want {
				t.Fatalf("isRPCShutdown(%v) = %v, want %v", c.err, got, c.want)
			}
		})
	}
}

// wrapErr is a minimal error wrapper that forwards Unwrap so
// errors.Is can traverse to the inner sentinel.
type wrapErr struct{ inner error }

func (w *wrapErr) Error() string { return "wrap: " + w.inner.Error() }
func (w *wrapErr) Unwrap() error { return w.inner }

// sentinelAPI implements visor.API trivially (via nil embedding) and
// carries an identifier so the retry test can verify whether fn was
// called with the original or the post-redial client. Any method
// invoked through the embedded nil interface will panic — the tests
// here intentionally don't call any visor API method, only inspect
// the *sentinelAPI pointer identity.
type sentinelAPI struct {
	visorAPIShim
	id int
}

// visorAPIShim is a typed shim so embedding it satisfies visor.API.
// The methods aren't called in tests; the type only exists so
// sentinelAPI compiles.
type visorAPIShim struct{ visor.API }

func TestPairRPCDoRetryOnShutdown(t *testing.T) {
	t.Run("no client returns unavailable", func(t *testing.T) {
		err := pairRPCDo("Op",
			func() visor.API { return nil },
			func(string) visor.API { return nil },
			func(visor.API) error { t.Fatal("fn must not be called"); return nil },
		)
		if !errors.Is(err, errPairRPCUnavailable) {
			t.Fatalf("want errPairRPCUnavailable, got %v", err)
		}
	})

	t.Run("success on first call: no redial", func(t *testing.T) {
		first := &sentinelAPI{id: 1}
		calls := 0
		redials := 0
		err := pairRPCDo("Op",
			func() visor.API { return first },
			func(string) visor.API { redials++; return &sentinelAPI{id: 2} },
			func(c visor.API) error {
				calls++
				if c != first {
					t.Fatalf("call %d: unexpected client", calls)
				}
				return nil
			},
		)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if calls != 1 || redials != 0 {
			t.Fatalf("calls=%d redials=%d, want 1/0", calls, redials)
		}
	})

	t.Run("non-shutdown error returns immediately", func(t *testing.T) {
		first := &sentinelAPI{id: 1}
		boom := errors.New("transport refused")
		calls := 0
		redials := 0
		err := pairRPCDo("Op",
			func() visor.API { return first },
			func(string) visor.API { redials++; return &sentinelAPI{id: 2} },
			func(visor.API) error { calls++; return boom },
		)
		if !errors.Is(err, boom) {
			t.Fatalf("want boom, got %v", err)
		}
		if calls != 1 || redials != 0 {
			t.Fatalf("calls=%d redials=%d, want 1/0", calls, redials)
		}
	})

	t.Run("shutdown triggers exactly one redial+retry", func(t *testing.T) {
		first := &sentinelAPI{id: 1}
		second := &sentinelAPI{id: 2}
		calls := 0
		redials := 0
		err := pairRPCDo("Op",
			func() visor.API { return first },
			func(string) visor.API { redials++; return second },
			func(c visor.API) error {
				calls++
				switch calls {
				case 1:
					if c != first {
						t.Fatalf("call 1: want first client")
					}
					return rpc.ErrShutdown
				case 2:
					if c != second {
						t.Fatalf("call 2: want second client")
					}
					return nil
				default:
					t.Fatalf("unexpected call %d", calls)
					return nil
				}
			},
		)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if calls != 2 || redials != 1 {
			t.Fatalf("calls=%d redials=%d, want 2/1", calls, redials)
		}
	})

	t.Run("redial fails: surface unavailable", func(t *testing.T) {
		first := &sentinelAPI{id: 1}
		calls := 0
		redials := 0
		err := pairRPCDo("Op",
			func() visor.API { return first },
			func(string) visor.API { redials++; return nil },
			func(visor.API) error { calls++; return rpc.ErrShutdown },
		)
		if !errors.Is(err, errPairRPCUnavailable) {
			t.Fatalf("want errPairRPCUnavailable, got %v", err)
		}
		if calls != 1 || redials != 1 {
			t.Fatalf("calls=%d redials=%d, want 1/1", calls, redials)
		}
	})

	t.Run("second call also shutdown: surface that err", func(t *testing.T) {
		first := &sentinelAPI{id: 1}
		second := &sentinelAPI{id: 2}
		calls := 0
		redials := 0
		err := pairRPCDo("Op",
			func() visor.API { return first },
			func(string) visor.API { redials++; return second },
			func(visor.API) error { calls++; return rpc.ErrShutdown },
		)
		if !errors.Is(err, rpc.ErrShutdown) {
			t.Fatalf("want rpc.ErrShutdown, got %v", err)
		}
		// No second retry — only one redial per call.
		if calls != 2 || redials != 1 {
			t.Fatalf("calls=%d redials=%d, want 2/1", calls, redials)
		}
	})
}
