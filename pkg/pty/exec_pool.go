// Package pty pkg/pty/exec_pool.go
//
// Session reuse for one-shot remote Exec. Establishing a dmsgpty session
// (dial + noise + the RPC writeRequest/readResponse handshake + the
// remote's proxy dial) costs ~10-15s and was paid fresh on EVERY
// `cli pty exec`. The remote Exec gateway is STATELESS — each Exec spawns
// its own os/exec.Cmd and touches no gateway state (see exec_gateway.go) —
// so one established session can serve unlimited sequential Exec calls.
// This pool caches one session per (remote PK, port, dialer) so only the
// first exec to a peer pays setup; the rest are an RPC round-trip.
//
// Correctness over a flaky overlay: a failed Exec (an RPC/connection
// error, distinct from a non-zero command exit which returns err==nil)
// retires the session, so the caller's next exec re-dials. A dead pooled
// conn is never silently reused — the trap that bit the RSN pool (a TTL
// outliving the underlying conn → handing out shut-down connections).
package pty

import (
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// execSessionTTL bounds how long an idle pooled session is kept before it's
// closed. Short enough that a remote restart / transport drop doesn't leave
// many dead streams pooled; the stale-on-Exec eviction is the correctness
// backstop regardless of this value.
const execSessionTTL = 90 * time.Second

// execSession is the slice of *PtyClient the pool needs. An interface so the
// pool is unit-testable without a live dmsg stream + RPC handshake.
type execSession interface {
	Exec(req *CommandExecReq) (*CommandExecResult, error)
	Close() error
}

// execKey identifies a pooled session. The dialer identity is part of the
// key so the same peer reached via dmsg vs skynet doesn't alias to one
// session (different underlying transport, different latency).
type execKey struct {
	pk     cipher.PubKey
	port   uint16
	dialer string
}

type pooledExec struct {
	sess     execSession
	refs     int
	lastUsed time.Time
	dead     bool
}

// acquireExec returns a live pooled session for key with refs incremented,
// or nil if none is usable (absent / retired / idle past TTL). On a non-nil
// return the caller MUST call releaseExec exactly once.
func (h *Host) acquireExec(key execKey) *pooledExec {
	h.execMu.Lock()
	defer h.execMu.Unlock()
	pe := h.execPool[key]
	if pe == nil || pe.dead {
		return nil
	}
	if pe.refs == 0 && time.Since(pe.lastUsed) > execSessionTTL {
		h.retireLocked(key, pe) // idle too long → force a fresh dial
		return nil
	}
	pe.refs++
	pe.lastUsed = time.Now()
	return pe
}

// releaseExec returns a session after an Exec. A non-nil execErr means the
// RPC/connection failed (NOT a non-zero command exit, which comes back with
// err==nil) — the session is retired so no one reuses a dead conn.
func (h *Host) releaseExec(key execKey, pe *pooledExec, execErr error) {
	h.execMu.Lock()
	defer h.execMu.Unlock()
	pe.refs--
	pe.lastUsed = time.Now()
	if execErr != nil && !pe.dead {
		h.retireLocked(key, pe)
		return
	}
	// A concurrent Exec may have retired it while we were in flight; close
	// once we're the last one out.
	if pe.dead && pe.refs == 0 {
		_ = pe.sess.Close() //nolint:errcheck
	}
}

// retireLocked unmaps pe and closes it if no Exec is in flight; if one still
// is (refs>0), it's marked dead so no new caller reuses it and the last
// releaseExec closes it. Caller holds execMu.
func (h *Host) retireLocked(key execKey, pe *pooledExec) {
	pe.dead = true
	if h.execPool[key] == pe {
		delete(h.execPool, key)
	}
	if pe.refs == 0 {
		_ = pe.sess.Close() //nolint:errcheck
	}
}

// cacheExec stores a freshly-dialed live session for reuse and
// opportunistically retires idle ones (bounds the pool by recently-active
// peers without a background goroutine). First-writer wins if another
// goroutine cached the same key concurrently.
func (h *Host) cacheExec(key execKey, sess execSession) {
	h.execMu.Lock()
	defer h.execMu.Unlock()
	for k, pe := range h.execPool {
		if pe.refs == 0 && time.Since(pe.lastUsed) > execSessionTTL {
			h.retireLocked(k, pe)
		}
	}
	if existing := h.execPool[key]; existing != nil && !existing.dead {
		_ = sess.Close() //nolint:errcheck // lost the race; don't leak ours
		return
	}
	if h.execPool == nil {
		h.execPool = make(map[execKey]*pooledExec)
	}
	h.execPool[key] = &pooledExec{sess: sess, lastUsed: time.Now()}
}
