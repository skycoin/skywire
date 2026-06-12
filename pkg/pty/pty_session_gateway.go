// Package pty pkg/pty/pty_session_gateway.go
//
// sessionPtyGateway is the persistent-session variant of LocalPtyGateway. The
// classic gateway owns a *Pty directly and the host kills it when the serving
// connection ends; this gateway instead drives a registry-tracked *ptySession
// whose single pump keeps the shell alive across a dropped stream. On Start it
// creates and registers a session; on connection teardown the host calls
// detach() (not Stop), leaving the shell running for a later reattach until the
// GC reaps it.
//
// The wire protocol is identical to LocalPtyGateway — same RPC method set — so
// an unmodified client works unchanged; it simply can't reattach yet (it never
// learns the session id). The id-return + Attach RPC that enables reconnect is
// a later phase; this gateway is the host-side seam it plugs into.
package pty

import (
	"fmt"
	"sync"

	"github.com/skycoin/skywire/pkg/cipher"
)

// sessionPtyGateway implements PtyGateway backed by a persistent ptySession.
type sessionPtyGateway struct {
	h     *Host
	owner cipher.PubKey

	mu   sync.Mutex
	sess *ptySession
	rdr  *sessionReader
}

// newSessionPtyGateway returns a session-backed gateway. owner is the
// authenticated remote PK (zero for the local CLI socket); it stamps the
// session for future owner-scoped reattach authorization.
func newSessionPtyGateway(h *Host, owner cipher.PubKey) *sessionPtyGateway {
	return &sessionPtyGateway{h: h, owner: owner}
}

// Start spawns the shell, wraps it in a persistent session, registers it, and
// attaches this gateway as the first follower.
func (g *sessionPtyGateway) Start(req *CommandReq, _ *struct{}) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.sess != nil {
		return ErrPtyAlreadyRunning
	}
	p := NewPty()
	if err := p.Start(req.Name, req.Arg, req.Size, req.Env); err != nil {
		return err
	}
	sess := newPtySession(newSessionID(), g.owner, p)
	g.h.sessions.add(sess)
	g.sess = sess
	g.rdr = sess.follow()
	return nil
}

// Read returns output from the session at this follower's offset, blocking
// until output is available or the session closes. It must not hold g.mu while
// blocking, or it would stall concurrent Write/Stop RPCs.
func (g *sessionPtyGateway) Read(reqN *int, respB *[]byte) error {
	g.mu.Lock()
	rdr := g.rdr
	g.mu.Unlock()
	if rdr == nil {
		return ErrPtyNotRunning
	}
	n := *reqN
	if n <= 0 {
		return fmt.Errorf("invalid read size: %d", n)
	}
	if n > maxPtyReadSize {
		n = maxPtyReadSize
	}
	b := make([]byte, n)
	nr, err := rdr.Read(b)
	*respB = b[:nr]
	return err
}

// Write forwards client input (stdin) to the session's pty.
func (g *sessionPtyGateway) Write(reqB *[]byte, respN *int) error {
	g.mu.Lock()
	sess := g.sess
	g.mu.Unlock()
	if sess == nil {
		return ErrPtyNotRunning
	}
	var err error
	*respN, err = sess.write(*reqB)
	return err
}

// SetPtySize forwards a resize to the session's pty.
func (g *sessionPtyGateway) SetPtySize(size *WinSize, _ *struct{}) error {
	g.mu.Lock()
	sess := g.sess
	g.mu.Unlock()
	if sess == nil {
		return ErrPtyNotRunning
	}
	return sess.setSize(size)
}

// Stop kills the shell, removes the session from the registry, and detaches
// this follower. Unlike a stream teardown (detach), Stop is an explicit
// end-of-session — the shell does not survive.
func (g *sessionPtyGateway) Stop(_, _ *struct{}) error {
	g.mu.Lock()
	sess, rdr := g.sess, g.rdr
	g.mu.Unlock()
	if sess == nil {
		return ErrPtyNotRunning
	}
	if rdr != nil {
		_ = rdr.Close() //nolint:errcheck
	}
	g.h.sessions.remove(sess.id)
	return sess.stop()
}

// Ping is the no-op keepalive round-trip (see LocalPtyGateway.Ping).
func (g *sessionPtyGateway) Ping(_, _ *struct{}) error { return nil }

// Exec is the one-shot non-interactive path. It is independent of the
// persistent session (it spawns its own os/exec.Cmd and never touches the
// session's pty), so it delegates to the stateless LocalPtyGateway impl.
func (g *sessionPtyGateway) Exec(req *CommandExecReq, resp *CommandExecResult) error {
	return (&LocalPtyGateway{}).Exec(req, resp)
}

// detach releases this gateway's follower without killing the shell. The host
// calls it when the serving connection ends, so a dropped stream leaves the
// session running in the registry for a later reattach (the GC reaps it if no
// reconnect arrives within the TTL).
func (g *sessionPtyGateway) detach() {
	g.mu.Lock()
	rdr := g.rdr
	g.mu.Unlock()
	if rdr != nil {
		_ = rdr.Close() //nolint:errcheck
	}
}
