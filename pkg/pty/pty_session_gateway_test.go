// Package pty pkg/pty/pty_session_gateway_test.go
package pty

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// newGatewayWithSession wires a sessionPtyGateway to a fakePty-backed session
// registered on a bare Host, bypassing Start (which would spawn a real shell).
// This exercises the gateway's registry interaction + detach/Stop/Read/Write
// semantics in isolation. Caller stops the fakePty.
func newGatewayWithSession(t *testing.T) (*Host, *sessionPtyGateway, *ptySession, *fakePty) {
	t.Helper()
	owner, _ := cipher.GenerateKeyPair()
	h := &Host{sessions: newSessionRegistry()}
	fp := newFakePty()
	sess := newPtySession(newSessionID(), owner, fp)
	h.sessions.add(sess)
	gw := newSessionPtyGateway(h, owner)
	gw.sess = sess
	gw.rdr = sess.follow()
	return h, gw, sess, fp
}

// TestSessionGateway_DetachKeepsSessionAlive is the core persistence property: a
// dropped stream (detach) must NOT kill the shell or evict the session — the
// pump keeps running and a later follower still sees fresh output.
func TestSessionGateway_DetachKeepsSessionAlive(t *testing.T) {
	h, gw, sess, fp := newGatewayWithSession(t)
	defer fp.Stop() //nolint:errcheck

	gw.detach() // simulate stream teardown

	require.False(t, sess.isClosed(), "detach must not close the session")
	_, ok := h.sessions.get(sess.id)
	require.True(t, ok, "detached session must remain in the registry")

	// The pump is still alive: a new follower reads output fed after detach.
	rdr := sess.follow()
	defer rdr.Close() //nolint:errcheck
	fp.feed([]byte("after-detach"))
	buf := make([]byte, 64)
	n := mustReadWithin(t, rdr, buf)
	require.Equal(t, "after-detach", string(buf[:n]))
}

// TestSessionGateway_StopRemovesAndKills verifies the explicit end-of-session
// path: Stop evicts from the registry and kills the shell (followers see EOF).
func TestSessionGateway_StopRemovesAndKills(t *testing.T) {
	h, gw, sess, fp := newGatewayWithSession(t)
	defer fp.Stop() //nolint:errcheck

	require.NoError(t, gw.Stop(nil, nil))

	_, ok := h.sessions.get(sess.id)
	require.False(t, ok, "Stop must evict the session from the registry")
	require.Eventually(t, sess.isClosed, time.Second, 5*time.Millisecond,
		"Stop must close the session")
}

// TestSessionGateway_ReadWrite checks the data path: Write reaches the pty's
// stdin and Read returns pumped output via the gateway's follower.
func TestSessionGateway_ReadWrite(t *testing.T) {
	_, gw, _, fp := newGatewayWithSession(t)
	defer fp.Stop() //nolint:errcheck

	// Write forwards to the pty's stdin.
	in := []byte("echo hi\n")
	var wn int
	require.NoError(t, gw.Write(&in, &wn))
	require.Equal(t, len(in), wn)
	require.Equal(t, "echo hi\n", string(fp.written))

	// Read returns output the pump drained from the pty.
	fp.feed([]byte("hi\n"))
	reqN := 64
	var respB []byte
	done := make(chan error, 1)
	go func() { done <- gw.Read(&reqN, &respB) }()
	select {
	case err := <-done:
		require.NoError(t, err)
		require.Equal(t, "hi\n", string(respB))
	case <-time.After(2 * time.Second):
		t.Fatal("gateway Read timed out")
	}
}

// TestSessionGateway_DoubleDetachSafe guards the follower count: detaching twice
// (e.g. stream teardown racing an explicit Stop) must decrement only once, so a
// second still-attached follower keeps the session non-idle.
func TestSessionGateway_DoubleDetachSafe(t *testing.T) {
	_, gw, sess, fp := newGatewayWithSession(t)
	defer fp.Stop() //nolint:errcheck

	other := sess.follow() // a second viewer stays attached
	defer other.Close()    //nolint:errcheck

	gw.detach()
	gw.detach() // must be a no-op the second time

	// With `other` still attached, the session is NOT idle. A buggy double
	// decrement would drop followers to 0 and make idleSince report > 0.
	require.Equal(t, time.Duration(0), sess.idleSince(time.Now()),
		"double detach under-counted followers")
}

// TestSessionGateway_ReadBeforeStart returns ErrPtyNotRunning rather than
// panicking when no session is bound yet.
func TestSessionGateway_ReadBeforeStart(t *testing.T) {
	h := &Host{sessions: newSessionRegistry()}
	gw := newSessionPtyGateway(h, cipher.PubKey{})
	reqN := 16
	var respB []byte
	require.ErrorIs(t, gw.Read(&reqN, &respB), ErrPtyNotRunning)
	require.ErrorIs(t, gw.Stop(nil, nil), ErrPtyNotRunning)
}
