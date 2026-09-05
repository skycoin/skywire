package dmsg

import (
	"context"
	"testing"
)

// TestServerSweepIsOptIn pins the default. The sweep costs a full Noise
// handshake per server in the deployment, so a plain context must never enable
// it — otherwise one dead public key becomes a handshake storm against the
// whole fleet, and the dial-failure backoff that exists to prevent exactly that
// is defeated.
func TestServerSweepIsOptIn(t *testing.T) {
	if serverSweepEnabled(context.Background()) {
		t.Fatal("a plain context must not enable the server sweep")
	}
}

// TestWithServerSweepEnables covers the opt-in and that it survives being
// wrapped, which is how callers actually use it (WithServerSweep then
// WithTimeout, or the reverse).
func TestWithServerSweepEnables(t *testing.T) {
	ctx := WithServerSweep(context.Background())
	if !serverSweepEnabled(ctx) {
		t.Fatal("WithServerSweep must enable the sweep")
	}

	wrapped, cancel := context.WithCancel(ctx)
	defer cancel()
	if !serverSweepEnabled(wrapped) {
		t.Error("the sweep opt-in must survive context wrapping")
	}
}

// TestServerSweepKeyIsTyped guards the collision the typed key exists to
// prevent: the package already carries string-keyed context values
// ("dmsgServer", "socks5_proxy"), so a bare string key here could be set —
// or cleared — by unrelated code that happens to use the same literal.
func TestServerSweepKeyIsTyped(t *testing.T) {
	ctx := context.WithValue(context.Background(), "serverSweep", true) //nolint:staticcheck,revive // deliberately the wrong key shape
	if serverSweepEnabled(ctx) {
		t.Error("a string-keyed value must not enable the sweep")
	}
}
