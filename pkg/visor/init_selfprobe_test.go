package visor

import (
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/logging"
)

// fakeReconnector records calls so the tests can assert the recovery
// path without a real dmsg client.
type fakeReconnector struct {
	calls int
}

func (f *fakeReconnector) ForceReconnect() int {
	f.calls++
	return 3 // pretend 3 sessions were closed
}

func TestHandleProbeResults_NoRecoveryOnTransientFailure(t *testing.T) {
	log := logging.MustGetLogger("test")
	rc := &fakeReconnector{}
	state := &probeState{consecutiveFails: map[uint16]int{}}

	// Single failure should not trigger recovery (threshold is 2).
	handleProbeResults(rc, state, map[uint16]bool{136: false}, time.Now(), false, log)

	if rc.calls != 0 {
		t.Fatalf("expected 0 reconnect calls on first failure, got %d", rc.calls)
	}
	if state.consecutiveFails[136] != 1 {
		t.Fatalf("expected consecutive=1, got %d", state.consecutiveFails[136])
	}
}

func TestHandleProbeResults_RecoversAfterThreshold(t *testing.T) {
	log := logging.MustGetLogger("test")
	rc := &fakeReconnector{}
	state := &probeState{consecutiveFails: map[uint16]int{}}

	// Two failures — triggers recovery exactly once.
	now := time.Now()
	handleProbeResults(rc, state, map[uint16]bool{136: false}, now, false, log)
	handleProbeResults(rc, state, map[uint16]bool{136: false}, now.Add(time.Minute), false, log)

	if rc.calls != 1 {
		t.Fatalf("expected 1 reconnect call, got %d", rc.calls)
	}
	if state.lastReconnect.IsZero() {
		t.Fatal("expected lastReconnect to be set")
	}
}

func TestHandleProbeResults_CooldownPreventsThrashing(t *testing.T) {
	log := logging.MustGetLogger("test")
	rc := &fakeReconnector{}
	state := &probeState{consecutiveFails: map[uint16]int{}}

	// Drive past threshold, triggering the first reconnect.
	t0 := time.Now()
	handleProbeResults(rc, state, map[uint16]bool{136: false}, t0, false, log)
	handleProbeResults(rc, state, map[uint16]bool{136: false}, t0.Add(time.Minute), false, log)
	if rc.calls != 1 {
		t.Fatalf("expected 1 reconnect, got %d", rc.calls)
	}

	// Keep failing within the cooldown window — no additional reconnects.
	for i := 0; i < 3; i++ {
		handleProbeResults(rc, state, map[uint16]bool{136: false},
			t0.Add(2*time.Minute+time.Duration(i)*time.Minute), false, log)
	}
	if rc.calls != 1 {
		t.Fatalf("expected 1 reconnect during cooldown, got %d", rc.calls)
	}

	// Once cooldown expires, a new failure triggers another reconnect.
	handleProbeResults(rc, state, map[uint16]bool{136: false},
		t0.Add(selfProbeRecoveryCooldown+time.Minute), false, log)
	if rc.calls != 2 {
		t.Fatalf("expected 2 reconnects after cooldown, got %d", rc.calls)
	}
}

func TestHandleProbeResults_SuccessResetsCounter(t *testing.T) {
	log := logging.MustGetLogger("test")
	rc := &fakeReconnector{}
	state := &probeState{consecutiveFails: map[uint16]int{136: 5}}

	handleProbeResults(rc, state, map[uint16]bool{136: true}, time.Now(), false, log)

	if state.consecutiveFails[136] != 0 {
		t.Fatalf("success should reset counter, got %d", state.consecutiveFails[136])
	}
	if rc.calls != 0 {
		t.Fatalf("success path should not call reconnect, got %d", rc.calls)
	}
}

func TestHandleProbeResults_IndependentPorts(t *testing.T) {
	log := logging.MustGetLogger("test")
	rc := &fakeReconnector{}
	state := &probeState{consecutiveFails: map[uint16]int{}}

	// Port 80 failing, port 136 healthy. Only port 80 accumulates.
	now := time.Now()
	handleProbeResults(rc, state, map[uint16]bool{80: false, 136: true}, now, false, log)
	if state.consecutiveFails[80] != 1 {
		t.Errorf("port 80 counter=%d, want 1", state.consecutiveFails[80])
	}
	if state.consecutiveFails[136] != 0 {
		t.Errorf("port 136 counter=%d, want 0", state.consecutiveFails[136])
	}
	if rc.calls != 0 {
		t.Errorf("no recovery yet, got %d calls", rc.calls)
	}

	// Second failure on port 80 crosses threshold — triggers reconnect.
	// Recovery is global (one DMSG client, one reconnect), so a single
	// call covers whichever port was the straw.
	handleProbeResults(rc, state, map[uint16]bool{80: false, 136: true}, now.Add(time.Minute), false, log)
	if rc.calls != 1 {
		t.Errorf("expected 1 reconnect after port 80 threshold, got %d", rc.calls)
	}
}

// TestProbeRecoveryWithheldWhileRelaying: the probe's recovery is
// ForceReconnect, which closes every dmsg session this visor holds — so while
// something is riding them it must not fire.
//
// The bug it guards is the one a dropped voice call is made of. A call is a
// single stream on a shared session; recovery takes the session down and the
// call with it, on the strength of two self-dials that missed. Against dmsg
// servers whose measured round-trips run to eight and ten seconds, two misses
// is a Tuesday.
func TestProbeRecoveryWithheldWhileRelaying(t *testing.T) {
	log := logging.MustGetLogger("selfprobe_test")
	rc := &fakeReconnector{}
	state := &probeState{consecutiveFails: make(map[uint16]int)}
	now := time.Now()

	// Past the threshold, twice over, with traffic moving throughout.
	handleProbeResults(rc, state, map[uint16]bool{136: false}, now, true, log)
	handleProbeResults(rc, state, map[uint16]bool{136: false}, now.Add(time.Minute), true, log)
	if rc.calls != 0 {
		t.Fatalf("recovery tore down %d live session set(s); want 0 while relaying", rc.calls)
	}

	// The traffic stops and the visor is still failing: now it recovers.
	handleProbeResults(rc, state, map[uint16]bool{136: false}, now.Add(2*time.Minute), false, log)
	if rc.calls != 1 {
		t.Fatalf("expected recovery once the traffic stopped, got %d", rc.calls)
	}
}
