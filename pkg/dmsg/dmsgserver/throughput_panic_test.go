// Package dmsgserver pkg/dmsg/dmsgserver/throughput_panic_test.go c1-net-dmsg
package dmsgserver

import (
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/skycoin/skywire/pkg/cipher"
	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg/metrics"
	"github.com/skycoin/skywire/pkg/logging"
)

// A session reaches the server's session map before its noise handshake has
// installed any state, and stays reachable through the snapshot GetSessions
// returns after it is torn down. Reading its nonces used to dereference that
// missing state and panic — on the once-a-minute metrics goroutine, where
// nothing could recover it. The process exited, systemd restarted it, and every
// session on the server dropped; one production server logged 3317 restarts,
// about one every 80 seconds.
func TestCalculateThroughputSkipsSessionWithNoNoiseState(t *testing.T) {
	// The zero SessionCommon is exactly the shape that crashed: reachable, with
	// no noise state behind it.
	var bare dmsg.SessionCommon

	sessions := map[cipher.PubKey]*dmsg.SessionCommon{
		{}: &bare,
	}

	dec, enc, average := calculateThroughput(
		sessions,
		map[*dmsg.SessionCommon]uint64{},
		map[*dmsg.SessionCommon]uint64{},
	)

	// Skipped, not recorded as zero: recording zero would make the counter look
	// like it ran backwards on the next tick, which the delta arithmetic reads
	// as a uint64 overflow and turns into an enormous bogus throughput figure.
	if _, ok := dec[&bare]; ok {
		t.Error("a session with no noise state was recorded in the dec values")
	}
	if _, ok := enc[&bare]; ok {
		t.Error("a session with no noise state was recorded in the enc values")
	}
	if average != 0 {
		t.Errorf("average = %d, want 0 when no session could be read", average)
	}
}

// NonceValues reports that it could not read, rather than panicking.
func TestNonceValuesReportsUnavailable(t *testing.T) {
	var bare dmsg.SessionCommon
	if _, _, ok := bare.NonceValues(); ok {
		t.Error("NonceValues reported ok for a session with no noise state")
	}

	var nilSession *dmsg.SessionCommon
	if _, _, ok := nilSession.NonceValues(); ok {
		t.Error("NonceValues reported ok for a nil session")
	}
	// The single-value getters are kept nil-safe for any other caller.
	if got := nilSession.GetDecNonce(); got != 0 {
		t.Errorf("GetDecNonce on a nil session = %d, want 0", got)
	}
	if got := nilSession.GetEncNonce(); got != 0 {
		t.Errorf("GetEncNonce on a nil session = %d, want 0", got)
	}
}

// A panic in a periodic task must never reach the goroutine that runs it: these
// tasks publish metrics, and nothing served depends on them.
func TestSafelyContainsAPanickingTask(t *testing.T) {
	api := NewServerAPI(chi.NewRouter(), logging.MustGetLogger("test"), metrics.NewEmpty())

	ran := false
	api.safely("panicking-task", func() {
		ran = true
		panic("boom")
	})
	if !ran {
		t.Fatal("the task did not run")
	}

	// Still usable afterwards — the recover must not leave state wedged.
	after := false
	api.safely("ordinary-task", func() { after = true })
	if !after {
		t.Error("a later task did not run after an earlier one panicked")
	}
}
