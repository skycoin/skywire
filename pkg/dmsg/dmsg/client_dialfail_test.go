package dmsg

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestDialFailBackoff covers the per-destination dial-failure backoff state
// machine: no backoff before a failure, fast-fail with the recorded error
// inside the window, doubling per repeat up to the cap, and a full reset on
// success. This is the guard against a persistently unreachable destination
// costing every periodic caller the full dial ladder (existing-session stream
// attempts plus new-session noise handshakes) every tick — measured as a
// wedged wasm visor pegging ~92% of a browser core for 44 minutes.
func TestDialFailBackoff(t *testing.T) {
	ce := &Client{}
	dst := mkPK(t)
	errBoom := errors.New("boom")

	// Clean destination: no backoff.
	if err, backing := ce.dialFailCheck(dst); backing {
		t.Fatalf("fresh destination must not be backing off (err=%v)", err)
	}

	// First failure opens a window carrying the error.
	ce.dialFailRecord(dst, errBoom)
	gotErr, backing := ce.dialFailCheck(dst)
	require.True(t, backing, "recorded failure must open a backoff window")
	require.ErrorIs(t, gotErr, errBoom)

	// Repeated failures double the NEXT window up to the cap.
	st := ce.dialFail[dst]
	require.Equal(t, 2*dialFailBackoffMin, st.backoff, "second window must double")
	for i := 0; i < 10; i++ {
		ce.dialFailRecord(dst, errBoom)
	}
	require.Equal(t, dialFailBackoffMax, ce.dialFail[dst].backoff, "backoff must cap")

	// An expired window stops fast-failing (the next real attempt goes through).
	ce.dialFail[dst].until = time.Now().Add(-time.Millisecond)
	if err, backing := ce.dialFailCheck(dst); backing {
		t.Fatalf("expired window must not fast-fail (err=%v)", err)
	}

	// Success wipes the state entirely, including the grown backoff.
	ce.dialFailRecord(dst, errBoom)
	ce.dialFailClear(dst)
	if err, backing := ce.dialFailCheck(dst); backing {
		t.Fatalf("cleared destination must not be backing off (err=%v)", err)
	}
	require.NotContains(t, ce.dialFail, dst)

	// Another destination is unaffected throughout.
	other := mkPK(t)
	ce.dialFailRecord(dst, errBoom)
	if err, backing := ce.dialFailCheck(other); backing {
		t.Fatalf("backoff must be per-destination (err=%v)", err)
	}
}
