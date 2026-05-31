// Package dmsgtracker manager_test.go: unit tests for the Manager's
// deterministic surface — error classification, the done/isDone helper,
// constructor defaults, and the Get / GetBulk / Close / InProgressCount
// methods exercised without a live dmsg client (dc == nil, so no serve
// goroutine and no tracker-establishment dials are triggered).
package dmsgtracker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/logging"
)

func newManagerNoDC(t *testing.T, interval, tmout time.Duration) *Manager {
	t.Helper()
	// dc == nil => NewDmsgTrackerManager does NOT start serve(), and we must
	// avoid any path that dials (establishTracker), which would nil-panic.
	return NewDmsgTrackerManager(logging.NewMasterLogger(), nil, interval, tmout)
}

func TestIsExpectedTrackerLookupErr(t *testing.T) {
	expected := []error{
		context.Canceled,
		context.DeadlineExceeded,
		disc.ErrKeyNotFound,
		fmt.Errorf("wrapped: %w", context.Canceled),
		errors.New("Get http://disc/: context canceled"),
		errors.New("lookup failed: context deadline exceeded"),
		errors.New("dmsg discovery: entry is not found"),
	}
	for _, err := range expected {
		require.True(t, isExpectedTrackerLookupErr(err), "%v should be expected", err)
	}

	notExpected := []error{
		errors.New("connection refused"),
		errors.New("some other failure"),
		io.ErrClosedPipe,
	}
	for _, err := range notExpected {
		require.False(t, isExpectedTrackerLookupErr(err), "%v should NOT be expected", err)
	}
}

func TestIsDone(t *testing.T) {
	open := make(chan struct{})
	require.False(t, isDone(open))

	closed := make(chan struct{})
	close(closed)
	require.True(t, isDone(closed))
}

func TestNewDmsgTrackerManager_Defaults(t *testing.T) {
	// Zero interval/timeout fall back to the package defaults.
	dtm := newManagerNoDC(t, 0, 0)
	require.Equal(t, DefaultDTMUpdateInterval, dtm.updateInterval)
	require.Equal(t, DefaultDTMUpdateTimeout, dtm.updateTimeout)

	// Explicit values are honored.
	dtm2 := newManagerNoDC(t, 3*time.Second, 5*time.Second)
	require.Equal(t, 3*time.Second, dtm2.updateInterval)
	require.Equal(t, 5*time.Second, dtm2.updateTimeout)
}

func TestManager_GetAndGetBulk(t *testing.T) {
	dtm := newManagerNoDC(t, time.Minute, time.Second)

	pkA, _ := cipher.GenerateKeyPair()
	pkB, _ := cipher.GenerateKeyPair()

	// Unknown PK.
	_, ok := dtm.Get(pkA)
	require.False(t, ok)

	// Seed the tracker map directly (Get/GetBulk only read .sum, never ctrl).
	dtm.dts[pkA] = &DmsgTracker{sum: DmsgClientSummary{PK: pkA, RoundTrip: 5 * time.Millisecond}}
	dtm.dts[pkB] = &DmsgTracker{sum: DmsgClientSummary{PK: pkB, RoundTrip: 7 * time.Millisecond}}

	sum, ok := dtm.Get(pkA)
	require.True(t, ok)
	require.Equal(t, pkA, sum.PK)
	require.Equal(t, 5*time.Millisecond, sum.RoundTrip)

	// GetBulk over known PKs returns both, sorted by PK (no establishment
	// goroutine is spawned because both are present).
	out := dtm.GetBulk(context.Background(), []cipher.PubKey{pkA, pkB})
	require.Len(t, out, 2)
	require.True(t, out[0].PK.Big().Cmp(out[1].PK.Big()) < 0, "GetBulk output must be sorted by PK")
}

func TestManager_Close(t *testing.T) {
	dtm := newManagerNoDC(t, time.Minute, time.Second)

	require.NoError(t, dtm.Close())
	// Second close reports already-closed.
	require.ErrorIs(t, dtm.Close(), io.ErrClosedPipe)

	// After close, Get reports not-found and ShouldGet reports closed pipe.
	pk, _ := cipher.GenerateKeyPair()
	_, ok := dtm.Get(pk)
	require.False(t, ok)

	_, err := dtm.ShouldGet(context.Background(), pk)
	require.ErrorIs(t, err, io.ErrClosedPipe)
}

func TestManager_InProgressCount(t *testing.T) {
	dtm := newManagerNoDC(t, time.Minute, time.Second)
	require.Equal(t, 0, dtm.InProgressCount())

	pk, _ := cipher.GenerateKeyPair()
	dtm.inProgress[pk] = struct{}{}
	require.Equal(t, 1, dtm.InProgressCount())
}
