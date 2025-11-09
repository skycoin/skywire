//go:build !no_ci
// +build !no_ci

package integration_test

import (
	"fmt"
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
		// Retry sending message up to 5 times with 5 second delays
		// to handle transient transport setup delays after restart
		var res *http.Response
		var err error
		var lastError string

		for attempt := 0; attempt < 5; attempt++ {
			res, err = env.SendSkyMessage(sender, receiver, t.Name())

			// If HTTP request itself failed (e.g., connection timeout), retry
			if err != nil {
				lastError = fmt.Sprintf("HTTP request failed: %v", err)
				t.Logf("Attempt %d: %s (retrying in 10s)", attempt+1, lastError)

				if attempt < 4 { // Don't sleep after last attempt
					time.Sleep(10 * time.Second)
				}
				continue
			}

			if res.StatusCode == http.StatusOK {
				require.NoError(t, res.Body.Close())
				return
			}

			// Log error and retry
			data, readErr := io.ReadAll(res.Body)
			require.NoError(t, readErr)
			lastError = string(data)
			t.Logf("Attempt %d: skychat returned error: %v (retrying in 10s)", attempt+1, lastError)
			require.NoError(t, res.Body.Close())

			if attempt < 4 { // Don't sleep after last attempt
				time.Sleep(10 * time.Second)
			}
		}

		// All retries failed
		if err != nil {
			require.NoError(t, err, "HTTP request failed after all retries: %v", lastError)
		} else {
			t.Logf("All retry attempts failed. Last error: %v", lastError)
			require.Equal(t, http.StatusOK, res.StatusCode, res)
		}
	}
	// TODO(ersonp): currently there is some issue with the visor containers that needs to be fixed first that causes the visor to not start properly
	// after a restart
	// t.Run("Init messaging env. Restart visors", func(t *testing.T) {
	// 	require.NoError(t, env.ContainerRestart(visorA, visorB, visorC))
	// 	time.Sleep(RestartDelay)
	// 	checkMessage(t, visorA, visorC)
	// })

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Restart visor containers
			require.NoError(t, env.ContainerRestart(tc.restartList...))
			time.Sleep(RestartDelay)

			// Re-establish transports after visor restart
			env.AddDefaultTransports(routerVisor, skychatVisors)
			
			// Additional delay for transports to stabilize
			time.Sleep(10 * time.Second)

			checkMessage(t, tc.sender, tc.receiver)
		})
	}
}
