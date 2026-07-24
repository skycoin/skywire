//go:build client_e2e
// +build client_e2e

package nativee2e

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// skychat HTTP addresses — must match the `--addr` args wired into the
// skychat app entries in testdata/visorA.json (:8001) and visorB.json
// (:8011). Distinct ports because both visors run as host processes on
// the same loopback host.
const (
	skychatAddrA = "127.0.0.1:8001"
	skychatAddrB = "127.0.0.1:8011"
)

// TestSkychatSendReceive drives a real skychat DM across two host-process
// visors, natively (no Docker):
//
//   - visor-A's chat-app sends a DM to visor-B over dmsg with a unique
//     marker and --wait, so the CLI blocks for visor-B's chat-ack. An
//     "Acked by <pk>" print means visor-B's chat-app actually RECEIVED
//     and processed the message — a genuine send→receive assertion, not
//     just sender-side HTTP 200.
//   - As a second, independent proof, the marker must show up in
//     visor-B's durable /history (both chat-apps run with --persist).
//
// dmsg is used (not skynet) so the DM needs no pre-added route/transport
// — the dmsg session between the two visors carries it directly.
func TestSkychatSendReceive(t *testing.T) {
	// skychat is auto_start:false in the configs (like skysocks-client /
	// vpn-client) so it stays off the visor's fragile cold-start dmsg
	// window. Start it explicitly now that both visors + the network are
	// up, then wait for each chat-app's HTTP surface.
	startSkychat(t, rpcA)
	startSkychat(t, rpcB)
	requireChatAppReady(t, skychatAddrA, rpcA, "visorA")
	requireChatAppReady(t, skychatAddrB, rpcB, "visorB")

	pkB := visorPK(t, rpcB)
	marker := fmt.Sprintf("nativee2e-dm-%d", time.Now().UnixNano())

	// Send A -> B with receipt-ack. Retry across the cold single-server
	// loopback's early dmsg-session churn, same pattern as the skysocks test.
	var out string
	var err error
	acked := false
	for attempt := 1; attempt <= 6 && !acked; attempt++ {
		out, err = cliT(20*time.Second, "skychat", "send",
			"--addr", skychatAddrA, "--to", pkB, "--net", "dmsg", "-m", marker, "--wait", "10s")
		if err == nil && strings.Contains(out, "Acked by") {
			acked = true
			break
		}
		t.Logf("skychat send A->B attempt %d not acked: err=%v out=%q", attempt, err, out)
		time.Sleep(5 * time.Second)
	}
	require.Truef(t, acked, "A->B skychat DM never acked by visor-B: last err=%v out=%q", err, out)
	require.Containsf(t, out, pkB, "ack line should name the recipient PK: %q", out)
	t.Logf("A->B DM acked by visor-B (%s)", pkB)

	// Independent confirmation: the DM is durably stored on visor-B and
	// retrievable via its /history with the exact unique marker.
	found := false
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) && !found {
		h, herr := cli("skychat", "history", "--addr", skychatAddrB, "--limit", "50")
		if herr == nil && strings.Contains(h, marker) {
			found = true
			break
		}
		time.Sleep(2 * time.Second)
	}
	require.Truef(t, found, "unique marker %q never appeared in visor-B skychat /history", marker)
	t.Logf("marker present in visor-B history — receipt confirmed independently")
}

// startSkychat launches the (auto_start:false) skychat app on a visor via
// its RPC. A benign "already started" is fine — requireChatAppReady is the
// real gate. Retries a couple of times since a just-ready visor's app
// proc-manager can briefly reject the start on a cold deployment.
func startSkychat(t *testing.T, rpc string) {
	t.Helper()
	for attempt := 1; attempt <= 3; attempt++ {
		out, err := cli("visor", "--rpc", rpc, "app", "start", "skychat")
		t.Logf("app start skychat (%s) attempt %d: out=%q err=%v", rpc, attempt, out, err)
		if err == nil {
			return
		}
		time.Sleep(3 * time.Second)
	}
}

// requireChatAppReady polls the skychat app's /status (via `cli skychat
// status`) until it answers, so the send doesn't race app startup. On
// timeout it dumps the app's launcher status + the visor log tail so a
// startup failure is diagnosable (the harness wipes the workdir on exit).
func requireChatAppReady(t *testing.T, addr, rpc, logName string) {
	t.Helper()
	// Generous window: on a marginally-ready visor (cold dmsg deployment),
	// the app-launcher can be slow to spawn skychat. The HTTP bind itself
	// is near-instant once launched, so this only covers launch latency.
	deadline := time.Now().Add(120 * time.Second)
	var lastErr error
	var lastOut string
	for time.Now().Before(deadline) {
		out, err := cli("skychat", "status", "--addr", addr)
		if err == nil {
			return
		}
		lastErr, lastOut = err, out
		time.Sleep(2 * time.Second)
	}
	appls, _ := cli("visor", "--rpc", rpc, "app", "ls")
	t.Logf("skychat status err=%v out=%q", lastErr, lastOut)
	t.Logf("visor app ls (%s):\n%s", rpc, appls)
	t.Logf("visor log tail (%s.log):\n%s", logName, visorLogTail(logName, 100))
	t.Fatalf("skychat app never became reachable at %s", addr)
}

// visorLogTail returns the last n lines of a visor's log in the harness
// workdir (which still exists during the test; teardown removes it). Runs of
// lines carrying the same message (differing only by their leading timestamp)
// are collapsed to "…(×N)" so a hot log-spam loop can't bury the real error.
func visorLogTail(name string, n int) string {
	b, err := os.ReadFile(filepath.Join(env.work, name+".log"))
	if err != nil {
		return fmt.Sprintf("(read %s.log: %v)", name, err)
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	// msgKey drops the leading "[timestamp] LEVEL [tag]: " so repeated messages
	// compare equal regardless of their per-line timestamp.
	msgKey := func(s string) string {
		if i := strings.LastIndex(s, "]: "); i >= 0 {
			return s[i+3:]
		}
		return s
	}
	var out []string
	for i := 0; i < len(lines); {
		j := i + 1
		for j < len(lines) && msgKey(lines[j]) == msgKey(lines[i]) {
			j++
		}
		if j-i > 1 {
			out = append(out, fmt.Sprintf("%s  …(×%d)", lines[i], j-i))
		} else {
			out = append(out, lines[i])
		}
		i = j
	}
	return strings.Join(out, "\n")
}
