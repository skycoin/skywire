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
		// Retry sending message up to 4 times with 5 second delays.
		// With proper waiting for routing rule expiration, fewer retries are needed.
		var res *http.Response
		var err error
		var lastError string

		for attempt := 0; attempt < 4; attempt++ {
			res, err = env.SendSkyMessage(sender, receiver, t.Name())

			// If HTTP request itself failed (e.g., connection timeout), retry
			if err != nil {
				lastError = fmt.Sprintf("HTTP request failed: %v", err)
				t.Logf("Attempt %d: %s (retrying in 5s)", attempt+1, lastError)

				if attempt < 3 { // Don't sleep after last attempt
					time.Sleep(5 * time.Second)
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
			t.Logf("Attempt %d: skychat returned error: %v (retrying in 5s)", attempt+1, lastError)
			require.NoError(t, res.Body.Close())

			if attempt < 3 { // Don't sleep after last attempt
				time.Sleep(5 * time.Second)
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
	// Known issue: visor containers do not restart cleanly (process state not fully reset).
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

			// Restart visor containers (ContainerRestart polls until containers are running)
			require.NoError(t, env.ContainerRestart(tc.restartList...))

			// Wait for DMSG to be ready on all restarted visors
			for _, visor := range tc.restartList {
				t.Logf("Waiting for DMSG to be ready on %s", visor)
				if err := env.WaitForDmsgDiscoveryEntry(visor, 60*time.Second); err != nil {
					t.Fatalf("DMSG not ready on %s after 60s: %v", visor, err)
				}
				t.Logf("DMSG ready on %s", visor)
			}

			// Wait for TPD to clear stale transport entries from restarted visors
			for _, visor := range tc.restartList {
				env.waitForTPDClean(visor, 15*time.Second)
			}

			// Re-establish transports after visor restart
			env.AddDefaultTransports(routerVisor, skychatVisors)

			// Wait for skychat apps to be ready on both sender and receiver
			senderApp := AppToRun{VisorHostName: tc.sender, AppName: "skychat", LauncherMode: "internal"}
			receiverApp := AppToRun{VisorHostName: tc.receiver, AppName: "skychat", LauncherMode: "internal"}

			// Wait for apps to be running before attempting to send messages
			for _, app := range []AppToRun{senderApp, receiverApp} {
				if err := env.waitForVisorApp(app); err != nil {
					t.Logf("Warning: app %s on %s may not be ready: %v", app.AppName, app.VisorHostName, err)
				}
			}

			// LEGITIMATE WAIT: Stale routing rules must expire via time-based GC.
			// Routing rules have a 30s keepalive (DefaultRouteKeepAlive), and the
			// GC runs periodically. There is no event or API to force immediate
			// expiration of stale rules on non-restarted visors, so a fixed wait
			// is the only option to prevent "routing table: rule not found" errors.
			t.Log("Waiting for stale routing rules to expire (10s)...")
			time.Sleep(10 * time.Second)

			checkMessage(t, tc.sender, tc.receiver)
		})
	}
}
