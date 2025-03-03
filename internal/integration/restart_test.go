//go:build !no_ci
// +build !no_ci

package integration_test

import (
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// nolint:funlen
func TestRestart(t *testing.T) {
	const routerVisor = visorB

	skychatVisors := []string{visorA, visorC}

	testCases := []struct {
		name        string
		sender      string
		receiver    string
		restartList []string
	}{
		{
			name:        "r: ac, s: a->c",
			sender:      visorA,
			receiver:    visorC,
			restartList: []string{visorA, visorC},
		},
		{
			name:        "r: ca, s: c->a",
			sender:      visorC,
			receiver:    visorA,
			restartList: []string{visorC, visorA},
		},
		{
			name:        "r: c, s: a->c",
			sender:      visorA,
			receiver:    visorC,
			restartList: []string{visorC},
		},
		{
			name:        "r: a, s: a->c",
			sender:      visorA,
			receiver:    visorC,
			restartList: []string{visorA},
		},
	}

	env := NewEnv().
		GatherContainersInfo().
		GatherVisorPKs([]string{visorA, visorB, visorC}).
		AddDefaultTransports(routerVisor, skychatVisors)

	checkMessage := func(t *testing.T, sender, receiver string) {
		res, err := env.SendSkyMessage(sender, receiver, t.Name())
		require.NoError(t, err)

		if res.StatusCode != http.StatusOK {
			data, err := io.ReadAll(res.Body)
			require.NoError(t, err)
			t.Logf("skychat returned error: %v", string(data))
		}

		require.Equal(t, http.StatusOK, res.StatusCode, res)
		require.NoError(t, res.Body.Close())
	}
	// TODO(ersonp): currently there is some issue with the visor containers that needs to be fixed first that causes the visor to not start properly
	// after a restart
	t.Run("Init messaging env. Restart visors", func(t *testing.T) {
		require.NoError(t, env.ContainerRestart(visorA, visorB, visorC))
		time.Sleep(RestartDelay)
		checkMessage(t, visorA, visorC)
	})

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Restart containers
			require.NoError(t, env.ContainerRestart(tc.restartList...))
			time.Sleep(RestartDelay)

			checkMessage(t, tc.sender, tc.receiver)
		})
	}
}
