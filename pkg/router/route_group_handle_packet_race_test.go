package router

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
)

// TestHandleDataPacket_NoSendOnClosedReadCh is a regression test for the
// route_group panic on send-to-closed-readCh that 3-agent skychat
// repro 2026-05-19 surfaced. Scenario: a data packet arrives at
// handleDataPacket while the route group is being closed by the
// remote side (i.e. closeInitiator=false). Pre-fix, the select at
// handleDataPacket only watched rg.closed (which is NEVER closed on
// remote-initiated close), missed rg.remoteClosed, and could pick
// the send-to-readCh case after close(rg.readCh) ran — panic.
//
// The fix has two layers: the missing select case for rg.remoteClosed
// catches the common path, and a defer-recover() at function entry
// catches the residual scheduling-race window between setRemoteClosed
// and close(rg.readCh).
//
// This test drives many concurrent handleDataPacket calls against a
// route group while close() runs in remote-initiated mode, asserting
// the test never panics. Without either layer of the fix, a panic
// here crashes the test binary.
func TestHandleDataPacket_NoSendOnClosedReadCh(t *testing.T) {
	l := logging.NewMasterLogger()
	rt := routing.NewTable(l.PackageLogger("rgt"))

	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()
	desc := routing.NewRouteDescriptor(pk1, pk2, 1, 2)

	rg := NewRouteGroup(DefaultRouteGroupConfig(), rt, desc, l)

	// One real reverse rule so handleDataPacket has something to
	// look up in the mux-recv path (and to give close() a non-empty
	// fwd-set to clean up). We don't add to rg.fwd / rg.tps so
	// broadcastClosePackets is a no-op — the test exercises the
	// once.Do close path without needing a live transport.
	pkt, err := routing.MakeDataPacket(routing.RouteID(1), []byte("test-payload"))
	require.NoError(t, err)

	// Push packets concurrently while close() runs. The race window
	// pre-fix is tiny; loop count is high enough to hit it reliably
	// under -race.
	const N = 200
	var sentOK, recovered int32

	var wg sync.WaitGroup
	for i := 0; i < N; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				// Test-level recover so a panic in handleDataPacket
				// (i.e. the fix being absent) fails the test
				// loudly instead of nuking the test binary.
				if r := recover(); r != nil {
					atomic.AddInt32(&recovered, 1)
				}
			}()
			if err := rg.handleDataPacket(pkt); err == nil {
				atomic.AddInt32(&sentOK, 1)
			}
		}()
	}

	// Run remote-initiated close concurrently with the senders.
	// rg.close (lowercase) is package-internal and does NOT mark
	// this side as the close initiator — exactly the path that hit
	// the panic in production.
	time.Sleep(10 * time.Millisecond) // let some senders enter the select
	closeDone := make(chan error, 1)
	go func() {
		closeDone <- rg.close(routing.CloseRequested)
	}()

	wg.Wait()
	require.Eventually(t, func() bool {
		select {
		case <-closeDone:
			return true
		default:
			return false
		}
	}, 5*time.Second, 50*time.Millisecond)

	// Either the sender saw the close and returned ErrClosedPipe
	// (good) or it succeeded before the close ran (also good).
	// What MUST NOT happen: a panic killing the goroutine without
	// hitting handleDataPacket's defer-recover. recovered > 0
	// means the fix is incomplete (test-level recover caught what
	// handleDataPacket's didn't).
	require.Zero(t, atomic.LoadInt32(&recovered),
		"handleDataPacket panicked %d/%d times — fix incomplete",
		atomic.LoadInt32(&recovered), N)

	t.Logf("ok: %d/%d sends succeeded, %d goroutines recovered at test level",
		atomic.LoadInt32(&sentOK), N, atomic.LoadInt32(&recovered))
}
