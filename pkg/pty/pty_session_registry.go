// Package pty pkg/pty/pty_session_registry.go c3-vis-pty
//
// sessionRegistry tracks live ptySessions by id so the host side of the pty can
// survive a dropped stream: when a client's connection tears down the gateway
// detaches its follower (instead of killing the shell), leaving the session in
// the registry for a later reattach. A GC goroutine reaps sessions that have
// either exited (the shell quit) or sat idle (no follower) past a TTL, so an
// abandoned shell can't leak indefinitely.
//
// This is the host-side mechanism for detach/reattach (Option B). The attach
// protocol that lets a reconnecting client rebind to an id is a later phase;
// the registry + GC + id allocation live here.
package pty

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// Reattach errors. ErrSessionNotFound means the id is unknown — the session was
// never created, already Stop'd, or GC-reaped after its idle TTL. Both are
// surfaced to a reconnecting client so it can fall back to starting fresh.
var (
	ErrSessionNotFound = errors.New("dmsgpty: session not found")
	ErrSessionNotOwned = errors.New("dmsgpty: session owned by a different public key")
)

// Defaults for the persistent-session GC. The TTL is how long a detached
// (follower-less) session lingers before reaping; the interval is how often the
// sweeper runs. Both are deliberately modest — a detached shell is a convenience
// for brief reconnects, not a long-lived daemon.
const (
	defaultSessionTTL    = 5 * time.Minute
	defaultSessionGCTick = 30 * time.Second
)

// newSessionID returns a short random hex id for a session. crypto/rand.Read
// effectively never fails on the platforms we run; on the impossible error path
// the zero-filled id is still unique enough for a single process (the registry
// only needs uniqueness among live sessions).
func newSessionID() string {
	var b [8]byte
	_, _ = rand.Read(b[:]) //nolint:errcheck
	return hex.EncodeToString(b[:])
}

// sessionRegistry is a concurrency-safe map of live sessions by id.
type sessionRegistry struct {
	mu sync.Mutex
	m  map[string]*ptySession
}

func newSessionRegistry() *sessionRegistry {
	return &sessionRegistry{m: make(map[string]*ptySession)}
}

func (r *sessionRegistry) add(s *ptySession) {
	r.mu.Lock()
	r.m[s.id] = s
	r.mu.Unlock()
}

// get returns the live session for id, if present.
func (r *sessionRegistry) get(id string) (*ptySession, bool) {
	r.mu.Lock()
	s, ok := r.m[id]
	r.mu.Unlock()
	return s, ok
}

func (r *sessionRegistry) remove(id string) {
	r.mu.Lock()
	delete(r.m, id)
	r.mu.Unlock()
}

func (r *sessionRegistry) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.m)
}

// reap removes and stops every session that has exited (pump closed) or been
// idle — no followers — for at least ttl. The actual stop() runs after the
// registry lock is released so a slow Stop can't block other registry ops, and
// stopping an already-exited session is a harmless no-op. Returns the count
// reaped.
func (r *sessionRegistry) reap(now time.Time, ttl time.Duration) int {
	r.mu.Lock()
	var dead []*ptySession
	for id, s := range r.m {
		if s.isClosed() || s.idleSince(now) >= ttl {
			dead = append(dead, s)
			delete(r.m, id)
		}
	}
	r.mu.Unlock()
	for _, s := range dead {
		_ = s.stop() //nolint:errcheck
	}
	return len(dead)
}

// ctxKeyRemotePK is the context key under which the accept loops stash the
// authenticated remote PK, so a session gateway can stamp the session's owner
// without threading the PK through the mux/handleFunc signatures.
type ctxKeyRemotePK struct{}

// withRemotePK returns a child context carrying the remote PK.
func withRemotePK(ctx context.Context, pk cipher.PubKey) context.Context {
	return context.WithValue(ctx, ctxKeyRemotePK{}, pk)
}

// remotePKFromCtx pulls the remote PK stashed by withRemotePK, or the zero PK
// when absent (e.g. the local CLI control socket, which has no peer identity).
func remotePKFromCtx(ctx context.Context) cipher.PubKey {
	if pk, ok := ctx.Value(ctxKeyRemotePK{}).(cipher.PubKey); ok {
		return pk
	}
	return cipher.PubKey{}
}
