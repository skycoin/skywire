// Package router pkg/router/route_group_intake_test.go
package router

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/routing"
)

// One route group whose app stopped reading must not stop the others. The
// router has a single inbound loop for every transport and every route group
// (serveTransportManager), so before the per-group intake worker a full readCh
// parked that shared loop for up to 30 s per packet and EVERY group on the
// visor — on every first hop — stopped receiving at once (55–66 s blackouts,
// bench/2026-09-16/845dd383e-smoke/mux-tunnels-2.recovery.tsv).
//
// The loop is modeled faithfully: one goroutine dispatching to both groups in
// turn, exactly as the router does.
func TestRouteGroupIntake_StalledGroupDoesNotStallOthers(t *testing.T) {
	cfg := DefaultRouteGroupConfig()
	cfg.ReadChBufSize = 1 // a tiny app queue: the stuck group fills it at once

	stuck := createRouteGroup(cfg)
	live := createRouteGroup(cfg)
	t.Cleanup(func() {
		_ = stuck.Close() //nolint:errcheck
		_ = live.Close()  //nolint:errcheck
	})

	pkt, err := routing.MakeDataPacket(1, []byte("payload"))
	require.NoError(t, err)

	// The shared loop: nobody reads from `stuck`, so its worker parks on readCh
	// after the first couple of packets. Feed enough to overflow its intake
	// queue too, then hand one packet to the live group from the SAME loop.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < intakeChBufSize+16; i++ {
			if herr := stuck.handlePacket(pkt); herr != nil {
				t.Errorf("stalled group handlePacket: %v", herr)
				return
			}
		}
		if herr := live.handlePacket(pkt); herr != nil {
			t.Errorf("live group handlePacket: %v", herr)
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the shared dispatch loop blocked on the stalled route group")
	}

	// The live group receives promptly even though the other group is wedged.
	read := make(chan int, 1)
	go func() {
		buf := make([]byte, 64)
		n, rerr := live.read(buf)
		if rerr != nil {
			return
		}
		read <- n
	}()

	select {
	case n := <-read:
		require.Equal(t, len("payload"), n)
	case <-time.After(time.Second):
		t.Fatal("live route group did not receive while another group was stalled")
	}

	// And the loss is confined to the stalled group, counted where `visor state
	// --select diag` can see it.
	require.Eventually(t, func() bool {
		_, _, drops := stuck.intakeQueue()
		return drops > 0
	}, 5*time.Second, 10*time.Millisecond, "an overflowing intake queue must count its drops")

	_, capacity, _ := live.intakeQueue()
	require.Equal(t, intakeChBufSize, capacity)
	_, _, liveDrops := live.intakeQueue()
	require.Zero(t, liveDrops, "the healthy group must not drop anything")
}
