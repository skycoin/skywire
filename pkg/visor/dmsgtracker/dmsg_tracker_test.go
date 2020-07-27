package dmsgtracker

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/SkycoinProject/dmsg"
	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/SkycoinProject/dmsg/dmsgctrl"
	"github.com/SkycoinProject/dmsg/dmsgtest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/SkycoinProject/skywire-mainnet/pkg/skyenv"
)

func TestDmsgTracker_Update(t *testing.T) {
	const timeout = time.Second * 5
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
	dt, err := NewDmsgTracker(context.TODO(), cT, cL.LocalPK())
	require.NoError(t, err)

	// act: attempt update
	assert.NoError(t, dt.Update(context.TODO()))

	// assert: check all fields
	assert.Equal(t, cL.LocalPK(), dt.sum.PK)
	assert.Equal(t, env.AllServers()[0].LocalPK(), dt.sum.ServerPK)
	assert.NotZero(t, dt.sum.RoundTrip)
}

func TestDmsgTrackerManager_MustGet(t *testing.T) {
	const timeout = time.Second * 5
	const nServers = 1
	conf := dmsg.Config{MinSessions: 1}

	ctx, cancel := context.WithCancel(context.TODO())
	t.Cleanup(cancel)

	env := dmsgtest.NewEnv(t, timeout)
	require.NoError(t, env.Startup(0, nServers, 0, &conf))
	t.Cleanup(env.Shutdown)

	// arrange: tracker manager
	tmC, err := env.NewClient(&conf)
	require.NoError(t, err)
	tm := NewDmsgTrackerManager(nil, tmC, 0, 0)
	t.Cleanup(func() { assert.NoError(t, tm.Close()) })

	type testCase struct {
		add bool          // true:add_client false:close_client
		sk  cipher.SecKey // secret key of the client to add/close
	}

	_, sk1 := cipher.GenerateKeyPair()
	_, sk2 := cipher.GenerateKeyPair()
	_, sk3 := cipher.GenerateKeyPair()
	_, sk4 := cipher.GenerateKeyPair()

	testCases := []testCase{
		{add: true, sk: sk1},
		{add: true, sk: sk2},
		{add: false, sk: sk1},
		{add: true, sk: sk3},
		{add: true, sk: sk4},
		{add: false, sk: sk3},
		{add: false, sk: sk4},
	}

	for i, tc := range testCases {
		i, tc := i, tc

		pk, err := tc.sk.PubKey()
		require.NoError(t, err)

		if tc.add {
			name := fmt.Sprintf("%d:add_%s", i, tc.sk)
			t.Run(name, func(t *testing.T) {
				c, err := env.NewClientWithKeys(pk, tc.sk, &conf)
				require.NoError(t, err)
				l, err := c.Listen(skyenv.DmsgCtrlPort)
				require.NoError(t, err)
				dmsgctrl.ServeListener(l, 0)

				// act
				sum, err := tm.MustGet(ctx, pk)
				require.NoError(t, err)
				tm.updateAllTrackers(ctx, tm.dm)

				// assert
				assert.Equal(t, pk, sum.PK)
				assert.NotZero(t, sum.RoundTrip)
			})

		} else {
			name := fmt.Sprintf("%d:close_%s", i, tc.sk)
			t.Run(name, func(t *testing.T) {
				c, ok := env.ClientOfPK(pk)
				require.True(t, ok)

				// act
				assert.NoError(t, c.Close())
				tm.updateAllTrackers(ctx, tm.dm)

				// assert
				_, ok = tm.Get(pk)
				assert.False(t, ok)
			})
		}
	}
}
