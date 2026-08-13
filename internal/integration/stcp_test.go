//go:build !no_ci
// +build !no_ci

// Package integration_test — pkg/../internal/integration/stcp_test.go: end-to-end
// DATA-ROUTE coverage for the STCP (skywire-tcp) transport.
//
// The other transport e2e tests (and the STCP leg of TestEnv_Tp) only assert a
// transport can be created — a "type check". STCP is the one transport with no
// address resolver: it dials a statically-supplied TCP endpoint (the peer's
// skywire-tcp.listening_address, :7777) — so this test goes further and proves
// real application data actually ROUTES over an STCP transport end to end.
//
// STCP has the lowest preference among the direct transports (STCPR > QUIC >
// SUDPH > STCP), so if any stcpr transport to the peer exists the router would use
// that instead. We therefore remove all transports first (autoconnect does not
// re-add stcpr between the test visors), add ONLY an STCP transport A→B, push
// several skychat messages A→B, and assert the STCP transport's own sent-byte
// counter grew well past its keepalive baseline — i.e. the payload demonstrably
// crossed the STCP link, since no other A↔B path existed.
package integration_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cliout/clitp"
	types "github.com/skycoin/skywire/pkg/transport/types"
	skyvisor "github.com/skycoin/skywire/pkg/visor"
)

// stcpTpView is the CLI transport view. It is the CLI's own output type,
// imported rather than restated: this file used to declare a private copy of
// the field names, which the compiler never compared against what the CLI
// actually emits — so a renamed field would have unmarshaled to zero here and
// asserted on it, instead of failing to build. The RPC TransportSummary is not
// usable for this: it nests the byte counters under an omitted "log", while the
// CLI emits them at the top level, which is precisely what these tests read.
type stcpTpView = clitp.Transport

// VisorTpAddSTCP adds an STCP transport from visor to pk, dialing the given
// ip:port (or host:port). STCP has no address resolver, so the peer's TCP
// endpoint must be supplied explicitly via --addr.
func (env *TestEnv) VisorTpAddSTCP(visor, pk, addr string) (*skyvisor.TransportSummary, error) {
	cmd := fmt.Sprintf("/release/skywire cli --rpc %v:3435 tp add %s --type stcp --addr %s --json", visor, pk, addr)
	out, err := env.visorTpExec(cmd)
	if err != nil {
		return nil, err
	}
	return out[0], nil
}

// visorTpView reads the CLI transport view (with byte counters) for one transport
// id on visor.
func (env *TestEnv) visorTpView(visor, id string) (stcpTpView, error) {
	cmd := fmt.Sprintf("/release/skywire cli --rpc %v:3435 tp -i %s --json", visor, id)
	var out []stcpTpView
	if err := env.ExecJSON(cmd, &out); err != nil {
		return stcpTpView{}, err
	}
	if len(out) == 0 {
		return stcpTpView{}, fmt.Errorf("no transport %s on %s", id, visor)
	}
	return out[0], nil
}

// TestEnv_STCPDataRoute establishes an STCP transport visor-a→visor-b as the ONLY
// path between them, routes skychat application data over it, and proves the data
// crossed the STCP link via the transport's byte counters.
func TestEnv_STCPDataRoute(t *testing.T) {
	start := time.Now()
	env := NewEnv().
		GatherContainersInfo().
		GatherVisorPKs([]string{visorA, visorB, visorC})

	// Route setup and transport registration ride dmsg, so wait for the visors to
	// be registered before touching transports.
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

	// skychat is the data source/sink for the route; it must be running on both ends.
	env.VerifyAppRunning(t, visorA, "skychat")
	env.VerifyAppRunning(t, visorB, "skychat")

	// Force STCP to be the only A↔B path: STCP is the lowest-preference direct
	// transport, so a lingering stcpr would win route selection. Autoconnect does
	// not re-create stcpr between the test visors, so a clean slate stays clean.
	require.NoError(t, env.RemoveAllTransports(visorA, visorB, visorC))
	time.Sleep(3 * time.Second)

	pkB := env.visorPKs[visorB]
	// STCP dials the peer's skywire-tcp listener (:7777), resolved from the
	// container hostname — no pk_table / address resolver involved.
	addr := fmt.Sprintf("%s:7777", visorB)

	var stcpTp *skyvisor.TransportSummary
	var err error
	for attempt := 1; attempt <= 3; attempt++ {
		stcpTp, err = env.VisorTpAddSTCP(visorA, pkB, addr)
		if err == nil {
			break
		}
		t.Logf("STCP add attempt %d/3 failed: %v", attempt, err)
		time.Sleep(3 * time.Second)
	}
	require.NoError(t, err, "STCP transport visor-a->visor-b must be creatable")
	require.Equal(t, types.STCP, stcpTp.Type, "transport type must be STCP")
	require.Contains(t, stcpTp.Remote.Hex(), pkB, "remote PK mismatch on STCP transport")
	defer func() { _, _ = env.VisorTpRm(visorA, stcpTp.ID) }() //nolint // best-effort cleanup

	// Baseline: an idle transport moves a few bytes via keepalive/RTT ping-pong.
	before, err := env.visorTpView(visorA, stcpTp.ID.String())
	require.NoError(t, err)
	t.Logf("STCP transport baseline: sent=%d recv=%d", before.SentBytes, before.RecvBytes)

	// Route real application data A→B over the STCP transport.
	const msgs = 6
	payload := strings.Repeat("x", 256)
	for i := 0; i < msgs; i++ {
		resp, serr := env.SendSkyMessage(visorA, visorB, fmt.Sprintf("stcp-data-route-%d-%s", i, payload))
		require.NoError(t, serr, "skychat message %d over STCP failed", i)
		if resp != nil && resp.Body != nil {
			require.NoError(t, resp.Body.Close())
		}
	}

	// Prove the app data traversed the STCP transport: its sent-byte counter must
	// grow well beyond the keepalive baseline. STCP is the only A↔B transport, so
	// there is no other path the traffic could have taken. (6×256B payloads +
	// noise framing ≫ 1000 bytes; keepalive contributes only a handful.)
	var after stcpTpView
	require.Eventually(t, func() bool {
		after, err = env.visorTpView(visorA, stcpTp.ID.String())
		return err == nil && after.SentBytes > before.SentBytes+1000
	}, 20*time.Second, 1*time.Second,
		"STCP transport sent_bytes did not grow with app traffic (baseline sent=%d)", before.SentBytes)

	t.Logf("STCP data-route verified: sent %d→%d, recv %d→%d over %d skychat msgs in %v",
		before.SentBytes, after.SentBytes, before.RecvBytes, after.RecvBytes, msgs,
		time.Since(start).Round(time.Second))
}
