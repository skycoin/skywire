// Package pty pkg/pty/pty_session_reattach_test.go
package pty

import (
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// testShell returns a long-lived command suitable for spawning a real pty in a
// test: `cat` blocks on stdin on unix; `cmd.exe` stays interactive on windows.
func testShell() string {
	if runtime.GOOS == "windows" {
		return "cmd.exe"
	}
	return "cat"
}

// startFakeSession spawns a session on the host via a gateway, but with a
// fakePty injected in place of a real shell (the gateway's start() would call
// NewPty). We reproduce start()'s registry wiring directly so the data path is
// driven by a controllable fakePty.
func startFakeSession(t *testing.T, h *Host, owner cipher.PubKey) (*sessionPtyGateway, *ptySession, *fakePty) {
	t.Helper()
	fp := newFakePty()
	sess := newPtySession(newSessionID(), owner, fp)
	h.sessions.add(sess)
	gw := newSessionPtyGateway(h, owner)
	gw.sess = sess
	gw.rdr = sess.follow()
	return gw, sess, fp
}

// TestSessionReattach_ReplaysMissedOutput is the end-to-end reconnect property:
// after a stream drops (gateway1 detaches), a NEW gateway Attach'd to the same
// id replays the output produced while detached, then keeps streaming live.
func TestSessionReattach_ReplaysMissedOutput(t *testing.T) {
	owner, _ := cipher.GenerateKeyPair()
	h := &Host{sessions: newSessionRegistry()}

	gw1, sess, fp := startFakeSession(t, h, owner)
	defer func() { _ = fp.Stop() }()

	// Stream drops.
	gw1.detach()

	// Output produced while the client was away — buffered in the ring.
	fp.feed([]byte("while-away\n"))
	require.Eventually(t, func() bool {
		sess.mu.Lock()
		defer sess.mu.Unlock()
		return sess.ring.len() >= len("while-away\n")
	}, time.Second, 5*time.Millisecond)

	// Reconnect: a fresh gateway (same owner) attaches by id and replays.
	gw2 := newSessionPtyGateway(h, owner)
	require.NoError(t, gw2.Attach(&sess.id, nil))

	reqN := 64
	var respB []byte
	require.NoError(t, gw2.Read(&reqN, &respB))
	require.Equal(t, "while-away\n", string(respB), "reattach must replay missed output")

	// And live output continues to flow to the reattached client.
	fp.feed([]byte("live\n"))
	respB = nil
	done := make(chan error, 1)
	go func() { done <- gw2.Read(&reqN, &respB) }()
	select {
	case err := <-done:
		require.NoError(t, err)
		require.Equal(t, "live\n", string(respB))
	case <-time.After(2 * time.Second):
		t.Fatal("post-reattach live Read timed out")
	}
}

// TestSessionReattach_RejectsUnknownID surfaces ErrSessionNotFound for an id
// that was never created or has been reaped.
func TestSessionReattach_RejectsUnknownID(t *testing.T) {
	owner, _ := cipher.GenerateKeyPair()
	h := &Host{sessions: newSessionRegistry()}
	gw := newSessionPtyGateway(h, owner)
	missing := "deadbeefdeadbeef"
	require.ErrorIs(t, gw.Attach(&missing, nil), ErrSessionNotFound)
}

// TestSessionReattach_RejectsForeignOwner stops one peer from hijacking another
// peer's shell: Attach with a different owner PK is refused.
func TestSessionReattach_RejectsForeignOwner(t *testing.T) {
	ownerA, _ := cipher.GenerateKeyPair()
	ownerB, _ := cipher.GenerateKeyPair()
	h := &Host{sessions: newSessionRegistry()}

	_, sess, fp := startFakeSession(t, h, ownerA)
	defer func() { _ = fp.Stop() }()

	gwB := newSessionPtyGateway(h, ownerB)
	require.ErrorIs(t, gwB.Attach(&sess.id, nil), ErrSessionNotOwned)
}

// TestStartSession_ReturnsID checks the id-returning start path end to end
// against a real shell, and that the id is registry-resolvable.
func TestStartSession_ReturnsID(t *testing.T) {
	owner, _ := cipher.GenerateKeyPair()
	h := &Host{sessions: newSessionRegistry()}
	gw := newSessionPtyGateway(h, owner)

	var sid string
	require.NoError(t, gw.StartSession(&CommandReq{Name: testShell()}, &sid))
	require.NotEmpty(t, sid)
	defer func() { _ = gw.Stop(nil, nil) }()

	got, ok := h.sessions.get(sid)
	require.True(t, ok)
	require.Equal(t, owner, got.ownerPK)
}
