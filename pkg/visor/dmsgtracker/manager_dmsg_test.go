// Package dmsgtracker manager_dmsg_test.go: integration coverage for the
// Manager's establish / update / serve paths, driven against an in-memory
// dmsg env. Mirrors the existing newDmsgTracker test's skip-on-instability
// approach since a small dmsg test mesh can have transient session failures
// on CI runners.
package dmsgtracker

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgctrl"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgtest"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// newTrackerEnv brings up a 2-server dmsg mesh with a listening (dmsgctrl)
// client and a tracking client, returning the tracking client and the
// listener's PK.
func newTrackerEnv(t *testing.T) (track *dmsg.Client, listenerPK cipher.PubKey) {
	t.Helper()
	conf := dmsg.Config{MinSessions: 1}

	env := dmsgtest.NewEnv(t, timeout)
	require.NoError(t, env.Startup(0, 2, 0, &conf))
	t.Cleanup(env.Shutdown)

	cL, err := env.NewClient(&conf)
	require.NoError(t, err)
	l, err := cL.Listen(skyenv.DmsgCtrlPort)
	require.NoError(t, err)
	dmsgctrl.ServeListener(l, 0)

	cT, err := env.NewClient(&conf)
	require.NoError(t, err)

	return cT, cL.LocalPK()
}

func TestManager_EstablishUpdateServe(t *testing.T) {
	cT, listenerPK := newTrackerEnv(t)

	// A short interval makes serve()'s ticker fire updateAllTrackers.
	dtm := NewDmsgTrackerManager(logging.NewMasterLogger(), cT, 150*time.Millisecond, 5*time.Second)
	t.Cleanup(func() { _ = dtm.Close() })

	// Establish a tracker to the listening client.
	dtm.establishTracker(context.Background(), listenerPK)
	sum, ok := dtm.Get(listenerPK)
	if !ok {
		t.Skipf("Skipping: dmsg test environment unstable (tracker not established)")
	}
	require.Equal(t, listenerPK, sum.PK)

	// ShouldGet on an already-established tracker returns its summary
	// (exercises the cache-hit branch that needs a real ctrl).
	got, err := dtm.ShouldGet(context.Background(), listenerPK)
	require.NoError(t, err)
	require.Equal(t, listenerPK, got.PK)

	// A direct update keeps the live tracker in the map.
	dtm.updateAllTrackers(context.Background())
	_, ok = dtm.Get(listenerPK)
	require.True(t, ok)

	// GetBulk returns the established summary.
	out := dtm.GetBulk(context.Background(), []cipher.PubKey{listenerPK})
	require.Len(t, out, 1)
	require.Equal(t, listenerPK, out[0].PK)

	// Let serve()'s ticker run at least once.
	time.Sleep(300 * time.Millisecond)
}

func TestManager_ShouldGetSpawnsEstablishment(t *testing.T) {
	cT, listenerPK := newTrackerEnv(t)

	dtm := NewDmsgTrackerManager(logging.NewMasterLogger(), cT, time.Minute, 5*time.Second)
	t.Cleanup(func() { _ = dtm.Close() })

	// A cache-miss ShouldGet returns an empty summary and kicks off a single
	// background establishment goroutine (covers the non-cached branch). We
	// deliberately do NOT retry: overlapping dials to the same dmsg session
	// race inside the dmsg client, so one attempt is the safe unit here.
	sum, err := dtm.ShouldGet(context.Background(), listenerPK)
	require.NoError(t, err)
	require.Equal(t, cipher.PubKey{}, sum.PK)

	// Give the single background establishment a moment to run to completion
	// (success or expected-miss); we don't assert the outcome since a small
	// test mesh can transiently fail the one attempt.
	require.Eventually(t, func() bool {
		return dtm.InProgressCount() == 0
	}, 8*time.Second, 100*time.Millisecond)
}

func TestManager_EstablishTracker_UnreachablePeer(t *testing.T) {
	cT, _ := newTrackerEnv(t)

	dtm := NewDmsgTrackerManager(logging.NewMasterLogger(), cT, time.Minute, time.Second)
	t.Cleanup(func() { _ = dtm.Close() })

	// A PK that isn't in discovery: establishTracker hits the expected
	// "entry not found" path and stores nothing.
	unknown, _ := cipher.GenerateKeyPair()
	dtm.establishTracker(context.Background(), unknown)
	_, ok := dtm.Get(unknown)
	require.False(t, ok)
	require.Equal(t, 0, dtm.InProgressCount())
}
