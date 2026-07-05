//go:build client_e2e
// +build client_e2e

package nativee2e

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestVisorConnectivity asserts both native visors booted, hold a dmsg session,
// and registered in the (in-memory) dmsg discovery — i.e. the core visor runtime
// works on this OS with no Docker.
func TestVisorConnectivity(t *testing.T) {
	for name, rpc := range map[string]string{"visorA": rpcA, "visorB": rpcB} {
		pk := visorPK(t, rpc)
		t.Logf("%s pk=%s", name, pk)

		sess, err := cli("dmsg", "--rpc", rpc, "sessions")
		require.NoError(t, err)
		require.Contains(t, sess, "Connected sessions:", "%s has no dmsg sessions", name)
		require.NotContains(t, sess, "Connected sessions: 0", "%s has 0 dmsg sessions", name)

		// Registered in dmsg discovery (delegated to the deployment's dmsg-server).
		var entry string
		require.Eventually(t, func() bool {
			body, err := httpGet(dmsgDiscURL + "/dmsg-discovery/entry/" + pk)
			if err != nil {
				return false
			}
			entry = body
			return strings.Contains(body, pk) && strings.Contains(body, "delegated_servers")
		}, 60*time.Second, 3*time.Second,
			"%s not registered in dmsg discovery (last=%.150q)", name, entry)
	}
}

// TestHypervisorPing checks the visor's embedded hypervisor HTTP API is serving
// (visorA has it enabled with auth off). Confirms the hypervisor runtime works
// natively — the client control-plane surface.
func TestHypervisorPing(t *testing.T) {
	var body string
	require.Eventually(t, func() bool {
		b, err := httpGet(hypervisorA + "/api/ping")
		if err != nil {
			return false
		}
		body = b
		return strings.Contains(b, "PONG!")
	}, 60*time.Second, 3*time.Second, "hypervisor /api/ping never returned PONG (last=%.80q)", body)
}
