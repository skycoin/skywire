//go:build emulong

// Package router pkg/router/scenarios_emulong_test.go c2-net-routing
//
// Scenarios at LIVE scale. They take longer than the fast suite's budget, so
// they are behind the emulong tag:
//
//	go test ./pkg/router/ -tags emulong -run EmuLong -count=1 -v
package router

import (
	"testing"
	"time"
)

// TestEmuLongDeadLegDoesNotDragTheGroup is scenario (a) at the shape the live
// rig produced: a 7 MB/s / 45 ms leg beside one running at 60 KB/s with ~9 s of
// queueing — group rg49220 of bench/2026-09-18/1008cc8e5/mux-compose-T2xL2,
// whose own detector read "mean 755.9 vs 9291.3 ms over 8/8 samples".
//
// KNOWN FAILING on develop as of 2026-09-18 (e897dbfbb). Measured here: the
// 8 MB download does not finish inside 90 s (6.5 MB delivered, wire/goodput
// 1.94) while the good leg alone takes 1.17 s, and the outclassed leg is never
// ruled probe-only. Both halves of the #5037 ruling miss it, for the same
// reason — the leg is so deeply queued that it never acknowledges anything:
//
//	its delay basis stays at the FIRST-HOP 45 ms (the send→ack term never gets
//	a sample), so the delay half reads it as the FASTEST leg in the group; and
//	delivKnown stays false, so the goodput half declines to rule a leg that
//	looks merely cold.
//
// The scheduler meanwhile put 5.5 MB on it. The shallower-queue version in the
// fast suite (TestEmuDeadLegShareIsBounded) shows the other end of the same
// hole: there the leg DOES ack, reading 1700 ms against the good leg's 350 ms —
// under the 6x the delay half needs, because both bases are send→ack and
// inflate together — while the goodput readings are 6.4 MB/s against 65 KB/s.
func TestEmuLongDeadLegDoesNotDragTheGroup(t *testing.T) {
	const bytes = 8 * emuMB
	const timeout = 90 * time.Second
	good := symmetric("good-7MBs-45ms", 7*emuMB, 45*time.Millisecond, 2*emuMB)
	// Slow because it QUEUES: 540 KB of buffer at 60 KB/s is the ~9 s delay
	// basis the live detector read.
	slow := symmetric("slow-60KBs-9s", 60*1024, 45*time.Millisecond, 540*1024)

	baseRig := newEmuRig(t, emuOpts{Legs: []emuLegSpec{good}})
	bx := baseRig.Transfer(emuDown, bytes, timeout)
	base := baseRig.Summary("good-alone", emuDown, bx)
	baseRig.Close()
	t.Log(base.Table())

	rig := newEmuRig(t, emuOpts{Legs: []emuLegSpec{good, slow}})
	x := rig.Transfer(emuDown, bytes, timeout)
	s := rig.Summary("dead-leg-live-scale", emuDown, x)
	s.Notes = append(s.Notes, rig.legBases(rig.B))
	t.Log(s.Table())

	if !s.HashOK {
		t.Errorf("transfer did not complete intact: got %d/%d bytes", s.Got, s.Bytes)
	}
	if want := 0.9 * base.GoodputBps(); s.GoodputBps() < want {
		t.Errorf("two legs ran at %.0f B/s, under 0.9x the good leg alone (%.0f B/s): the dead leg is dragging the group",
			s.GoodputBps(), want)
	}
	limit := uint64(LegProbeBytes()) + 128*1024 //nolint:gosec // the default is positive
	if got := s.Legs[1].PayloadBytes; got > limit {
		t.Errorf("the outclassed leg carried %d bytes of payload, past its probe budget %d", got, limit)
	}
}
