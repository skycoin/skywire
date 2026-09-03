//go:build !no_ci
// +build !no_ci

// Package integration_test — pkg/../internal/integration/transport_setup_test.go:
// end-to-end coverage for the transport-setup service HTTP API
// (pkg/deployment/tps/api), which the coverage report flagged as having zero
// unit or e2e coverage.
//
// transport-setup is the operator-facing service that manages transports on
// REMOTE visors: its HTTP API (`POST /add`, `POST /remove`, `GET /{pk}/transports`)
// dials the target visor over dmsg (DmsgTransportSetupPort) and drives that
// visor's TransportGateway RPC. In the deployment it runs inside
// deployment-services with its HTTP API on TCP :80 (transport-setup.json `port`),
// reachable from the test-runner as http://transport-setup:80 (extra_hosts →
// 174.0.0.17). The managed visors trust the transport-setup PK via their
// `transport_setup` config, so the RPC is authorized.
//
// This drives the real HTTP API against the live fleet: list a visor's transports
// (read path), create an stcpr transport visor-a→visor-c through the API, confirm
// it appears in the listing, then remove it — exercising the full
// HTTP → dmsg → visor-RPC round trip that no other test covered.
package integration_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// transportSetupAPI is the transport-setup service's HTTP API base URL. Plain
// HTTP on TCP :80 (docker/integration/transport-setup.json "port": 80).
const transportSetupAPI = "http://transport-setup:80"

// tpsTransport mirrors the JSON of pkg/transport/setup.TransportResponse (the
// shape returned by POST /add and each element of GET /{pk}/transports). The
// fields have no json tags, so the keys are the Go field names (capitalized).
type tpsTransport struct {
	ID     string `json:"ID"`
	Local  string `json:"Local"`
	Remote string `json:"Remote"`
	Type   string `json:"Type"`
}

// TestEnv_TransportSetup exercises the transport-setup HTTP API end to end
// against the managed fleet (visor-a … visor-c).
func TestEnv_TransportSetup(t *testing.T) {
	start := time.Now()
	env := NewEnv().
		GatherContainersInfo().
		GatherVisorPKs([]string{visorA, visorB, visorC})

	// The service reaches its target visors over dmsg, so every visor must be
	// discoverable before the API resolves. Mirrors the other dmsg e2e tests.
	for _, visor := range []string{visorA, visorB, visorC} {
		err := env.WaitForDmsgDiscoveryEntry(visor, 120*time.Second)
		if err != nil {
			t.Logf("Failed to find DMSG discovery entry for %s: %v", visor, err)
			if logs, logErr := env.ReadLog(visor); logErr == nil {
				t.Logf("Logs for %s:\n%s", visor, logs)
			}
		}
		require.NoError(t, err, "Visor %s not found in DMSG discovery", visor)
	}

	pkA := env.visorPKs[visorA]
	pkC := env.visorPKs[visorC]
	require.NotEmpty(t, pkA)
	require.NotEmpty(t, pkC)

	// listTransports fetches visor pk's transports through the API. Returns the
	// parsed list and whether the call cleanly succeeded (exit 0 + valid JSON).
	listTransports := func(pk string) ([]tpsTransport, bool) {
		res, err := env.execResult(fmt.Sprintf("curl -s -S -m 20 %s/%s/transports", transportSetupAPI, pk))
		if err != nil || res.ExitCode != 0 {
			return nil, false
		}
		var tps []tpsTransport
		if json.Unmarshal([]byte(res.Stdout()), &tps) != nil {
			return nil, false
		}
		return tps, true
	}

	// --- 1. Read path: GET /{pk}/transports ------------------------------------
	// Proves the API dials visor-a over dmsg and returns its TransportGateway
	// listing. Retry to absorb the service's dmsg-session / visor-RPC settling.
	t.Run("list", func(t *testing.T) {
		require.Eventually(t, func() bool {
			_, ok := listTransports(pkA)
			return ok
		}, 120*time.Second, 5*time.Second,
			"transport-setup GET /%s/transports never returned a valid transport list (is the service up on :80?)", pkA)
	})

	// --- 2. Create path: POST /add (visor-a → visor-c, stcpr) ------------------
	// The API dials visor-a and drives its TransportGateway.AddTransport. stcpr is
	// the reliable transport type on the directly-reachable Docker network.
	// SaveTransport is idempotent, so this tolerates a pre-existing transport.
	body := fmt.Sprintf(`{"from":"%s","to":"%s","type":"stcpr"}`, pkA, pkC)
	addCmd := fmt.Sprintf("curl -s -S -m 25 -X POST -d %s %s/add", body, transportSetupAPI)

	var created tpsTransport
	var last string
	require.Eventually(t, func() bool {
		res, err := env.execResult(addCmd)
		if err != nil || res.ExitCode != 0 {
			return false
		}
		last = res.Stdout()
		if json.Unmarshal([]byte(last), &created) != nil {
			return false
		}
		return created.ID != "" && strings.EqualFold(created.Remote, pkC)
	}, 120*time.Second, 5*time.Second,
		"transport-setup POST /add (stcpr %s→%s) never returned a transport summary last=%.300q", pkA, pkC, last)

	require.Equal(t, "stcpr", created.Type, "created transport type should be stcpr")
	require.True(t, strings.EqualFold(created.Local, pkA), "transport Local should be visor-a")
	t.Logf("transport-setup created transport %s (visor-a→visor-c, stcpr)", created.ID)

	// --- 3. Verify it appears in the listing -----------------------------------
	tps, ok := listTransports(pkA)
	require.True(t, ok, "listing visor-a transports after add failed")
	found := false
	for _, tp := range tps {
		if strings.EqualFold(tp.ID, created.ID) {
			found = true
			break
		}
	}
	require.Truef(t, found, "created transport %s not present in transport-setup listing for visor-a", created.ID)

	// --- 4. Remove it through the API ------------------------------------------
	// POST /remove drives the visor's TransportGateway.RemoveTransport directly
	// (the local HTTP path; remote dmsg removal is blocked by the gateway).
	rmBody := fmt.Sprintf(`{"from":"%s","id":"%s"}`, pkA, created.ID)
	rmRes, err := env.execResult(fmt.Sprintf("curl -s -S -m 20 -X POST -d %s %s/remove", rmBody, transportSetupAPI))
	require.NoError(t, err)
	require.Equal(t, 0, rmRes.ExitCode, "remove curl failed: %s", rmRes.Stderr())
	require.Contains(t, rmRes.Stdout(), "true", "transport-setup /remove should report Result:true; got %.150q", rmRes.Stdout())
	t.Logf("transport-setup removed transport %s", created.ID)

	t.Logf("TestEnv_TransportSetup completed in %v", time.Since(start).Round(time.Second))
}
