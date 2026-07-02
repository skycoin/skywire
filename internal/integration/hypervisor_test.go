//go:build !no_ci
// +build !no_ci

// Package integration_test — pkg/../internal/integration/hypervisor_test.go:
// end-to-end coverage for the Hypervisor HTTP API (pkg/visor/hypervisor.go), the
// control plane that manages a fleet of visors over dmsg.
//
// This closes the "Hypervisor web UI / API — no e2e at all" gap from the coverage
// report. In the integration deployment visor-b runs the hypervisor (started with
// `-q http`, http_addr :8000, enable_auth=false, enable_tls=false — see
// docker/integration/visorB.json), and visor-a + visor-c are configured to be
// MANAGED by it (their `hypervisors` list carries visor-b's PK). So the hypervisor
// reaches its managed visors over dmsg and proxies operator actions to them via
// their visor RPC.
//
// The test drives the real HTTP API from the test-runner container (no auth, no
// TLS) and asserts control-plane behavior end to end:
//   - liveness (/api/ping) and hypervisor self-info (/api/about, dmsg connected);
//   - fleet discovery (/api/visors lists visor-a, visor-b AND visor-c — proving the
//     managed visors connected to the hypervisor over dmsg);
//   - per-visor RPC-over-dmsg reads (/api/visors/{pk}/health);
//   - a full remote transport lifecycle driven THROUGH the hypervisor: create an
//     stcpr transport visor-a→visor-c via POST, see it in the GET listing, then
//     DELETE it — proving the hypervisor actually commands a remote visor's RPC
//     over dmsg, which is the whole point of the control plane.
package integration_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// hypervisorAPI is visor-b's hypervisor HTTP API base URL. Plain HTTP + no auth
// because docker/integration/visorB.json sets enable_tls=false, enable_auth=false.
const hypervisorAPI = "http://visor-b:8000"

// hvOverview is the subset of pkg/visor.Overview the fleet-listing assertions read.
type hvOverview struct {
	LocalPK string `json:"local_pk"`
}

// hvTransportSummary mirrors the JSON of pkg/visor.TransportSummary (the shape
// returned by POST/GET .../transports).
type hvTransportSummary struct {
	ID     string `json:"id"`
	Local  string `json:"local_pk"`
	Remote string `json:"remote_pk"`
	Type   string `json:"type"`
	Label  string `json:"label"`
}

// TestEnv_HypervisorAPI exercises the hypervisor HTTP control-plane API on visor-b
// against its managed fleet (visor-a, visor-b, visor-c).
func TestEnv_HypervisorAPI(t *testing.T) {
	start := time.Now()
	env := NewEnv().
		GatherContainersInfo().
		GatherVisorPKs([]string{visorA, visorB, visorC})

	// The hypervisor reaches its managed visors over dmsg, so every visor must be
	// discoverable before the fleet-level endpoints resolve. Mirrors the other e2e.
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
	pkB := env.visorPKs[visorB]
	pkC := env.visorPKs[visorC]
	require.NotEmpty(t, pkA)
	require.NotEmpty(t, pkB)
	require.NotEmpty(t, pkC)

	// httpGet runs a bounded curl inside the test-runner and returns (body, exit).
	// The runner shares the `visors` docker network with visor-b, so `visor-b:8000`
	// resolves and is reachable (same path the RPC-over-network tests use).
	httpGet := func(path string) (string, int, error) {
		res, err := env.execResult("curl -s -S -m 15 " + hypervisorAPI + path)
		if err != nil {
			return "", -1, err
		}
		return res.Stdout(), res.ExitCode, nil
	}

	// --- 1. Liveness: /api/ping (public, unauthenticated) ----------------------
	t.Run("ping", func(t *testing.T) {
		var body string
		require.Eventually(t, func() bool {
			b, code, err := httpGet("/api/ping")
			if err != nil || code != 0 {
				return false
			}
			body = b
			return strings.Contains(b, "PONG!")
		}, 60*time.Second, 3*time.Second,
			"hypervisor /api/ping never returned PONG (is visor-b's hypervisor up on :8000?) last=%.100q", body)
	})

	// --- 2. Hypervisor self-info: /api/about -----------------------------------
	// Proves the hypervisor identifies itself (its own PK) and reports a live dmsg
	// client — the prerequisite for reaching managed visors.
	t.Run("about", func(t *testing.T) {
		var about struct {
			PubKey        string `json:"public_key"`
			DmsgConnected bool   `json:"dmsg_connected"`
			DmsgSessions  int    `json:"dmsg_sessions"`
		}
		require.Eventually(t, func() bool {
			b, code, err := httpGet("/api/about")
			if err != nil || code != 0 {
				return false
			}
			if json.Unmarshal([]byte(b), &about) != nil {
				return false
			}
			return about.DmsgConnected
		}, 90*time.Second, 3*time.Second, "hypervisor /api/about never reported dmsg_connected")
		require.Equal(t, pkB, about.PubKey, "/api/about should report visor-b's PK as the hypervisor identity")
		require.Positive(t, about.DmsgSessions, "hypervisor should hold at least one dmsg session")
	})

	// --- 3. Fleet discovery: /api/visors ---------------------------------------
	// The listing must include the hypervisor's own visor (visor-b) AND both
	// managed remotes (visor-a, visor-c) — proving they connected to the
	// hypervisor over dmsg. Retry: the managed visors register with the hypervisor
	// asynchronously after startup.
	t.Run("visors_list", func(t *testing.T) {
		var last string
		require.Eventually(t, func() bool {
			b, code, err := httpGet("/api/visors")
			if err != nil || code != 0 {
				return false
			}
			last = b
			var visors []hvOverview
			if json.Unmarshal([]byte(b), &visors) != nil {
				return false
			}
			seen := make(map[string]bool, len(visors))
			for _, v := range visors {
				seen[v.LocalPK] = true
			}
			return seen[pkA] && seen[pkB] && seen[pkC]
		}, 120*time.Second, 5*time.Second,
			"hypervisor /api/visors did not list all three managed visors (a=%s b=%s c=%s) last=%.300q", pkA, pkB, pkC, last)
	})

	// --- 4. Per-visor RPC over dmsg: /api/visors/{pk}/health -------------------
	// The hypervisor fetches visor-a's Health via its RPC over dmsg; a 200 status
	// in the body proves the hypervisor→visor-a RPC round-tripped.
	t.Run("visor_health", func(t *testing.T) {
		var health struct {
			Status         int    `json:"status"`
			ServicesHealth string `json:"services_health"`
		}
		var last string
		require.Eventually(t, func() bool {
			b, code, err := httpGet("/api/visors/" + pkA + "/health")
			if err != nil || code != 0 {
				return false
			}
			last = b
			if json.Unmarshal([]byte(b), &health) != nil {
				return false
			}
			return health.Status == 200
		}, 90*time.Second, 5*time.Second,
			"hypervisor /api/visors/%s/health never reported status 200 (RPC over dmsg) last=%.200q", pkA, last)
	})

	// --- 5. Remote transport lifecycle THROUGH the hypervisor ------------------
	// Create an stcpr transport visor-a→visor-c by POSTing to the hypervisor, which
	// commands visor-a's RPC over dmsg. stcpr is the reliable transport type on the
	// directly-reachable Docker network. Then confirm it's listed and delete it.
	// AddTransport is idempotent (returns the existing managed transport if one is
	// already present), so this is robust to a transport left by another test.
	t.Run("transport_lifecycle", func(t *testing.T) {
		// The hypervisor enforces CSRF on POST/PUT/DELETE (the --csrf flag defaults
		// ON), independent of enable_auth. Fetch a fresh stateless token from
		// /api/csrf and send it as the X-CSRF-Token header. Header uses the
		// no-space "Name:value" form because the exec helper splits argv on spaces.
		csrf := func() string {
			b, code, err := httpGet("/api/csrf")
			if err != nil || code != 0 {
				return ""
			}
			var c struct {
				Token string `json:"csrf_token"`
			}
			if json.Unmarshal([]byte(b), &c) != nil {
				return ""
			}
			return c.Token
		}

		// curl argv is space-split by the exec helper (no shell), so the JSON body
		// MUST contain no spaces; httputil.ReadJSON decodes it regardless of the
		// missing Content-Type header (but disallows unknown fields — send only the
		// known ones).
		body := fmt.Sprintf(`{"transport_type":"stcpr","remote_pk":"%s","label":"user"}`, pkC)

		var created hvTransportSummary
		var last string
		require.Eventually(t, func() bool {
			tok := csrf()
			if tok == "" {
				return false
			}
			postCmd := fmt.Sprintf("curl -s -S -m 25 -X POST -H X-CSRF-Token:%s -d %s %s/api/visors/%s/transports",
				tok, body, hypervisorAPI, pkA)
			res, err := env.execResult(postCmd)
			if err != nil || res.ExitCode != 0 {
				return false
			}
			last = res.Stdout()
			if json.Unmarshal([]byte(last), &created) != nil {
				return false
			}
			return created.ID != "" && created.Remote == pkC
		}, 120*time.Second, 5*time.Second,
			"hypervisor POST .../visors/%s/transports (stcpr→%s) never returned a transport summary last=%.300q", pkA, pkC, last)

		require.Equal(t, "stcpr", created.Type, "created transport type should be stcpr")
		require.Equal(t, pkA, created.Local, "transport local_pk should be visor-a")
		t.Logf("hypervisor created transport %s (visor-a→visor-c, stcpr)", created.ID)

		// It must show up in the hypervisor's transport listing for visor-a.
		listBody, code, err := httpGet("/api/visors/" + pkA + "/transports")
		require.NoError(t, err)
		require.Equal(t, 0, code)
		var listed []hvTransportSummary
		require.NoError(t, json.Unmarshal([]byte(listBody), &listed), "transport listing should be a JSON array; got %.200q", listBody)
		found := false
		for _, tp := range listed {
			if tp.ID == created.ID {
				found = true
				break
			}
		}
		require.Truef(t, found, "created transport %s not present in hypervisor listing for visor-a", created.ID)

		// Delete it through the hypervisor (CSRF-guarded too); returns `true`.
		delTok := csrf()
		require.NotEmpty(t, delTok, "could not obtain CSRF token for DELETE")
		delRes, err := env.execResult(fmt.Sprintf("curl -s -S -m 15 -X DELETE -H X-CSRF-Token:%s %s/api/visors/%s/transports/%s",
			delTok, hypervisorAPI, pkA, created.ID))
		require.NoError(t, err)
		require.Equal(t, 0, delRes.ExitCode, "DELETE transport curl failed: %s", delRes.Stderr())
		require.Contains(t, delRes.Stdout(), "true", "hypervisor DELETE transport should return true; got %.100q", delRes.Stdout())
		t.Logf("hypervisor deleted transport %s", created.ID)
	})

	t.Logf("TestEnv_HypervisorAPI completed in %v", time.Since(start).Round(time.Second))
}
