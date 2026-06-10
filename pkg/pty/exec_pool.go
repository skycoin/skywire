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
//
// Locking discipline: execMu guards the pool map and every pooledExec
// field, but is NEVER held across a session Close. *PtyClient.Close() is a
// synchronous, un-timeouted RPC round-trip (Stop() + rpcC.Close()) over a
// possibly-dead stream; since execMu is Host-global, closing under it would
// stall every peer's pooled-exec ops behind one dead peer's slow Close.
// retireLocked therefore RETURNS the session to close and each public
// method closes it after releasing the lock.
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
	pe := h.execPool[key]
	if pe == nil || pe.dead {
		h.execMu.Unlock()
		return nil
	}
	if pe.refs == 0 && time.Since(pe.lastUsed) > execSessionTTL {
		stale := h.retireLocked(key, pe) // idle too long → force a fresh dial
		h.execMu.Unlock()
		if stale != nil {
			_ = stale.Close() //nolint:errcheck,gosec
		}
		return nil
	}
	pe.refs++
	pe.lastUsed = time.Now()
	h.execMu.Unlock()
	return pe
}

// releaseExec returns a session after an Exec. A non-nil execErr means the
// RPC/connection failed (NOT a non-zero command exit, which comes back with
// err==nil) — the session is retired so no one reuses a dead conn.
func (h *Host) releaseExec(key execKey, pe *pooledExec, execErr error) {
	h.execMu.Lock()
	pe.refs--
	pe.lastUsed = time.Now()
	var toClose execSession
	switch {
	case execErr != nil && !pe.dead:
		toClose = h.retireLocked(key, pe)
	case pe.dead && pe.refs == 0:
		// A concurrent Exec retired it while we were in flight; close once
		// we're the last one out.
		toClose = pe.sess
	}
	h.execMu.Unlock()
	if toClose != nil {
		_ = toClose.Close() //nolint:errcheck,gosec
	}
}

// retireLocked unmaps pe and marks it dead, returning the session the CALLER
// must Close() AFTER releasing execMu (or nil if an Exec is still in flight —
// the last releaseExec closes it then). It never closes under the lock; see
// the package "Locking discipline" note. Caller holds execMu.
func (h *Host) retireLocked(key execKey, pe *pooledExec) execSession {
	pe.dead = true
	if h.execPool[key] == pe {
		delete(h.execPool, key)
	}
	if pe.refs == 0 {
		return pe.sess
	}
	return nil
}

// cacheExec stores a freshly-dialed live session for reuse and
// opportunistically retires idle ones (bounds the pool by recently-active
// peers without a background goroutine). First-writer wins if another
// goroutine cached the same key concurrently. All Close()s happen after the
// lock is released.
func (h *Host) cacheExec(key execKey, sess execSession) {
	h.execMu.Lock()
	var toClose []execSession
	for k, pe := range h.execPool {
		if pe.refs == 0 && time.Since(pe.lastUsed) > execSessionTTL {
			if stale := h.retireLocked(k, pe); stale != nil {
				toClose = append(toClose, stale)
			}
		}
	}
	if existing := h.execPool[key]; existing != nil && !existing.dead {
		toClose = append(toClose, sess) // lost the race; don't leak ours
	} else {
		if h.execPool == nil {
			h.execPool = make(map[execKey]*pooledExec)
		}
		h.execPool[key] = &pooledExec{sess: sess, lastUsed: time.Now()}
	}
	h.execMu.Unlock()
	for _, s := range toClose {
		_ = s.Close() //nolint:errcheck,gosec
	}
}
