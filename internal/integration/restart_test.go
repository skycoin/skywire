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

	dumpLogsOnFailure := func(t *testing.T, visors ...string) {
		if !t.Failed() {
			return
		}
		t.Log("=== Test failed, dumping visor logs ===")
		for _, visor := range visors {
			logs, err := env.ReadLog(visor)
			if err != nil {
				t.Logf("Failed to read logs from %s: %v", visor, err)
				continue
			}
			t.Logf("\n=== Logs from %s ===\n%s\n=== End logs from %s ===\n", visor, logs, visor)
		}
	}

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
			// Dump logs on failure
			t.Cleanup(func() {
				dumpLogsOnFailure(t, visorA, visorB, visorC)
			})

			// Remove transports before restart to clean up TPD entries
			require.NoError(t, env.RemoveAllTransports(tc.restartList...))

			// Restart visor containers
			require.NoError(t, env.ContainerRestart(tc.restartList...))
			time.Sleep(RestartDelay)

			// Re-establish transports after visor restart
			env.AddDefaultTransports(routerVisor, skychatVisors)

			// Wait for skychat apps to be ready on both sender and receiver
			senderApp := AppToRun{VisorHostName: tc.sender, AppName: "skychat"}
			receiverApp := AppToRun{VisorHostName: tc.receiver, AppName: "skychat"}

			// Wait for apps to be running before attempting to send messages
			for _, app := range []AppToRun{senderApp, receiverApp} {
				if err := env.waitForVisorApp(app); err != nil {
					t.Logf("Warning: app %s on %s may not be ready: %v", app.AppName, app.VisorHostName, err)
				}
			}

			// Small additional delay for route establishment
			time.Sleep(5 * time.Second)

			checkMessage(t, tc.sender, tc.receiver)
		})
	}
}
