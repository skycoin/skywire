// Package dmsgtest pkg/dmsgtest/env_test.go
package dmsgtest

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/dmsg"
)

func TestEnv(t *testing.T) {

	const timeout = time.Second * 30

	t.Run("startup_shutdown", func(t *testing.T) {
		cases := []struct {
			ServerN     int
			ClientN     int
			MinSessions int
		}{
			{5, 1, 1},
			{5, 3, 1},
			{5, 3, 3},
			{5, 10, 5},
			{5, 10, 10},
		}
		for i, c := range cases {
			env := NewEnv(t, timeout)
			err := env.Startup(5*time.Second, c.ServerN, c.ClientN, &dmsg.Config{
				MinSessions: c.MinSessions,
			})
			require.NoError(t, err, i)
			env.Shutdown()
		}
	})

	t.Run("restart_client", func(t *testing.T) {
		env := NewEnv(t, timeout)
		require.NoError(t, env.Startup(0, 3, 1, nil))
		defer env.Shutdown()

		// After closing all clients, n(clients) should be 0.
		require.Len(t, env.AllClients(), 1)
		env.CloseAllClients()
		require.Len(t, env.AllClients(), 0)

		// NewClient should result in n(clients) == 1.
		// Closing the created client should result in n(clients) == 0 after some time.
		client, err := env.NewClient(nil)
		require.NoError(t, err)
		require.Len(t, env.AllClients(), 1)
		require.NoError(t, client.Close())
		time.Sleep(time.Second)
		require.Len(t, env.AllClients(), 0)
	})

	// Ensure server entries are available and timeout when expected.
	// - Start discovery with entry_timeout=1s and x servers and y clients with update_interval=250ms
	// - Start as normal. Entries should show up in discovery, and stay there.
	t.Run("discovery_entry_timeout", func(t *testing.T) {
		const (
			updateInterval = time.Millisecond * 100
			entryTimeout   = time.Millisecond * 500
		)

		// Start env, discovery, server.
		env := NewEnv(t, timeout)
		defer env.Shutdown()

		require.NoError(t, env.Startup(entryTimeout, 0, 0, nil))
		d := env.Discovery()

		s, err := env.NewServer(updateInterval)
		require.NoError(t, err)

		// Ensure existence of server entries in given time interval.
		done := time.After(entryTimeout * 3)
	Loop:
		for {
			se, err := d.Entry(context.TODO(), s.LocalPK())
			require.NoError(t, err)
			require.NotNil(t, se.Server)

			select {
			case <-done:
				break Loop
			case <-time.After(updateInterval):
				continue
			}
		}
	})
}
