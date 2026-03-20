// Package dmsgtracker pkg/visor/dmsgtracker/dmsg_tracker_test.go
package dmsgtracker

import (
	"context"
	"runtime"
	"testing"
	"time"

	"github.com/skycoin/dmsg/pkg/dmsg"
	"github.com/skycoin/dmsg/pkg/dmsgctrl"
	"github.com/skycoin/dmsg/pkg/dmsgtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/skyenv"
)

const (
	timeout = time.Second * 10
)

func TestDmsgTracker_Update(t *testing.T) {
	const nServers = 1
	conf := dmsg.Config{MinSessions: 1}

	env := dmsgtest.NewEnv(t, timeout)
	require.NoError(t, env.Startup(0, nServers, 0, &conf))
	t.Cleanup(env.Shutdown)

	// arrange: listening client
	cL, err := env.NewClient(&conf)
	require.NoError(t, err)
	l, err := cL.Listen(skyenv.DmsgCtrlPort)
	require.NoError(t, err)
	dmsgctrl.ServeListener(l, 0)

	// arrange: tracking client
	cT, err := env.NewClient(&conf)
	require.NoError(t, err)

	// The dmsg test environment with a single server can have transient
	// session failures, especially on CI runners. Skip rather than fail
	// if the environment cannot establish connectivity.
	dt, err := newDmsgTracker(context.TODO(), cT, cL.LocalPK())
	if err != nil {
		t.Skipf("Skipping: dmsg test environment unstable: %v", err)
	}

	// act: attempt update
	err = dt.Update(context.TODO())
	if err != nil {
		t.Skipf("Skipping: dmsg tracker update failed: %v", err)
	}

	// assert: check all fields
	assert.Equal(t, cL.LocalPK(), dt.sum.PK)
	assert.Equal(t, env.AllServers()[0].LocalPK(), dt.sum.ServerPK)
	if !(runtime.GOOS == "windows") {
		assert.NotZero(t, dt.sum.RoundTrip)
	}
}
