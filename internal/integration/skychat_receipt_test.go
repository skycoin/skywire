//go:build !no_ci
// +build !no_ci

package integration_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestEnv_SkychatSendReceipt asserts that a skychat DM is actually RECEIVED
// on the far visor — closing the gap the existing SendSkyMessage tests leave
// open (they only POST to the sender's /message and assert the sender's HTTP
// 200; nothing confirms visor-B ever saw the frame).
//
// It uses the CLI's built-in receipt confirmation: `skychat send --wait`
// wraps the message in a chat-msg envelope with a unique id and blocks for
// visor-B's chat-app to send a chat-ack back. An "Acked by <pk>" print means
// visor-B RECEIVED and processed the message end to end — a genuine
// send→receive assertion, not a sender-side status code.
//
// Single-hop A→B over the default skynet route (the same transport
// AddDefaultTransports sets up, and the same path the passing SendSkyMessage
// tests exercise); the ack returns over that same framed conn.
func TestEnv_SkychatSendReceipt(t *testing.T) {
	env := NewEnv().
		GatherContainersInfo().
		GatherVisorPKs([]string{visorA, visorB}).
		AddDefaultTransports(visorA, []string{visorB})

	env.VerifyAppRunning(t, visorA, "skychat")
	env.VerifyAppRunning(t, visorB, "skychat")

	pkB := env.visorPKs[visorB]
	require.NotEmpty(t, pkB, "visor-B PK not gathered")

	// Unique, space-free marker (env.Exec splits the command on spaces).
	marker := fmt.Sprintf("e2e-receipt-%d", time.Now().UnixNano())

	// The CLI runs in the e2e-test container and targets visor-a's chat-app
	// over the docker network; that chat-app delivers to visor-B and relays
	// visor-B's chat-ack back to us.
	sendCmd := fmt.Sprintf(
		"/release/skywire cli skychat send --addr %s:8001 --to %s --net skynet -m %s --wait 10s",
		visorA, pkB, marker)

	var out string
	var err error
	acked := false
	for attempt := 1; attempt <= 4 && !acked; attempt++ {
		out, err = env.Exec(sendCmd)
		if err == nil && strings.Contains(out, "Acked by") {
			acked = true
			break
		}
		t.Logf("skychat send A->B attempt %d not acked: err=%v out=%.200q", attempt, err, out)
		time.Sleep(5 * time.Second)
	}
	require.Truef(t, acked, "A->B skychat DM never acked by visor-B: last err=%v out=%q", err, out)
	require.Containsf(t, out, pkB, "ack line should name the recipient PK: %q", out)
	t.Logf("A->B skychat DM acked by visor-B (%s) — receipt confirmed", pkB)
}
