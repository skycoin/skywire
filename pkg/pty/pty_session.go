// Package pty pkg/pty/pty_session.go c3-vis-pty
//
// ptySession is the host-side persistence primitive for detach/reattach pty
// sessions (Option B, phase 1). A single pump goroutine is the ONLY reader of
// the underlying *Pty: it copies output into a bounded ring buffer and signals
// waiters. Clients read by following a monotonic logical offset into that ring,
// so there is never a reader handoff to race on — attach/detach is just whether
// a follower exists, and a detached shell keeps running because the pump keeps
// draining it (a kernel pty buffer is tiny; without a constant reader the shell
// blocks on write).
//
// Phase 1 lands the session + ring + pump (so a shell survives a dropped
// stream) and the registry + GC (so abandoned shells don't leak). The
// attach-by-id protocol, gateway Read rework, and client reconnect are later
// phases; this file is self-contained and unit-tested in isolation.
package pty

import (
	"io"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// ptyRingCap bounds the per-session output buffer. It must be large enough to
// hold a useful replay window for reattach yet small enough that idle detached
// sessions don't pin much memory before the GC reaps them.
const ptyRingCap = 256 * 1024

// ptyIO is the subset of *Pty that a ptySession drives. It's an interface so
// the session's pump/follower concurrency can be unit-tested with a fake pty.
type ptyIO interface {
	Read([]byte) (int, error)
	Write([]byte) (int, error)
	SetPtySize(*WinSize) error
	Stop() error
}

// ringBuffer is a fixed-capacity byte ring holding the most recent output.
// Oldest bytes are overwritten once full. It is NOT safe for concurrent use;
// the owning ptySession serializes access under its mutex.
type ringBuffer struct {
	buf   []byte
	start int // index of the oldest stored byte
	size  int // number of bytes stored, 0..len(buf)
}

func newRingBuffer(capacity int) *ringBuffer {
	return &ringBuffer{buf: make([]byte, capacity)}
}

func (r *ringBuffer) len() int { return r.size }

// write appends p, overwriting the oldest bytes once full.
func (r *ringBuffer) write(p []byte) {
	c := len(r.buf)
	if len(p) >= c {
		// p alone fills/overflows the ring: keep only its last c bytes.
		copy(r.buf, p[len(p)-c:])
		r.start, r.size = 0, c
		return
	}
	w := (r.start + r.size) % c // next write index
	n := copy(r.buf[w:], p)
	if n < len(p) {
		copy(r.buf, p[n:])
	}
	r.size += len(p)
	if r.size > c {
		// Overwrote `over` oldest bytes; advance start past them.
		over := r.size - c
		r.start = (r.start + over) % c
		r.size = c
	}
}

// at copies up to len(dst) bytes stored at ring offset `off` (0 == oldest
// stored byte) into dst, returning the count. Caller guarantees 0 <= off < size.
func (r *ringBuffer) at(off int, dst []byte) int {
	c := len(r.buf)
	avail := r.size - off
	n := len(dst)
	if n > avail {
		n = avail
	}
	idx := (r.start + off) % c
	first := copy(dst, r.buf[idx:min(idx+n, c)])
	if first < n {
		copy(dst[first:n], r.buf[:n-first])
	}
	return n
}

// ptySession wraps a *Pty with a persistence layer: a pump goroutine drains the
// pty into a ring, followers read by logical offset, and detach keeps the shell
// alive. Safe for concurrent use.
type ptySession struct {
	id      string
	ownerPK cipher.PubKey
	p       ptyIO

	mu    sync.Mutex
	cond  *sync.Cond
	ring  *ringBuffer
	total int64 // monotonic total bytes ever written by the pump

	followers  int       // number of attached readers
	lastDetach time.Time // when followers last dropped to 0
	closed     bool
	closeErr   error
}

// newPtySession wraps p (which must already be Start()ed) and launches the pump.
func newPtySession(id string, owner cipher.PubKey, p ptyIO) *ptySession {
	s := &ptySession{
		id:      id,
		ownerPK: owner,
		p:       p,
		ring:    newRingBuffer(ptyRingCap),
	}
	s.cond = sync.NewCond(&s.mu)
	go s.pump()
	return s
}

// pump is the single reader of the underlying pty. It runs until the pty errors
// (shell exit / closed), keeping the shell unblocked whether or not a follower
// is attached.
func (s *ptySession) pump() {
	buf := make([]byte, 32*1024)
	for {
		n, err := s.p.Read(buf)
		s.mu.Lock()
		if n > 0 {
			s.ring.write(buf[:n])
			s.total += int64(n)
		}
		if err != nil {
			s.closed = true
			s.closeErr = err
			s.cond.Broadcast()
			s.mu.Unlock()
			return
		}
		s.cond.Broadcast()
		s.mu.Unlock()
	}
}

// follow attaches a reader at the current end of the stream (no replay). Phase
// 2's reattach will instead start it at total-ring.len() to replay missed
// output. The returned reader must be Closed to detach.
func (s *ptySession) follow() *sessionReader {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.followers++
	return &sessionReader{s: s, pos: s.total}
}

// followReplay attaches a reader positioned to replay the buffered ring first
// (used on reattach so the client catches up on output produced while detached).
func (s *ptySession) followReplay() *sessionReader {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.followers++
	return &sessionReader{s: s, pos: s.total - int64(s.ring.len())}
}

func (s *ptySession) detach() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.followers > 0 {
		s.followers--
	}
	if s.followers == 0 {
		s.lastDetach = time.Now()
	}
}

// write forwards client input to the pty (stdin).
func (s *ptySession) write(b []byte) (int, error) { return s.p.Write(b) }

// setSize forwards a resize to the pty.
func (s *ptySession) setSize(sz *WinSize) error { return s.p.SetPtySize(sz) }

// isClosed reports whether the pump has exited.
func (s *ptySession) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

// idleSince reports how long the session has had no followers, or 0 if
// currently attached.
func (s *ptySession) idleSince(now time.Time) time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.followers > 0 || s.lastDetach.IsZero() {
		return 0
	}
	return now.Sub(s.lastDetach)
}

// stop kills the shell and unblocks any followers.
func (s *ptySession) stop() error {
	err := s.p.Stop() // makes the pump's Read error out → closes the session
	s.mu.Lock()
	if !s.closed {
		// Mark closed and wake followers now; the pump sets closeErr when its
		// Read returns. A nil closeErr means followers see a clean io.EOF —
		// what we want for an intentional stop.
		s.closed = true
		s.cond.Broadcast()
	}
	s.mu.Unlock()
	return err
}

// sessionReader is a follower's view: it reads output at its own logical offset,
// blocking for new bytes and surfacing close. Multiple readers can follow one
// session independently.
type sessionReader struct {
	s    *ptySession
	pos  int64
	once sync.Once // guards Close so a double-detach can't under-count followers
}

// Read returns output produced at or after this reader's offset, blocking until
// some is available or the session closes. If the reader has fallen further
// behind than the ring holds, it skips the overwritten gap (the shell re-renders
// on the next prompt) rather than returning stale bytes.
func (r *sessionReader) Read(p []byte) (int, error) {
	s := r.s
	s.mu.Lock()
	defer s.mu.Unlock()
	for {
		oldest := s.total - int64(s.ring.len())
		if r.pos < oldest {
			r.pos = oldest // fell behind; skip the overwritten gap
		}
		if r.pos < s.total {
			off := int(r.pos - oldest)
			n := s.ring.at(off, p)
			r.pos += int64(n)
			return n, nil
		}
		if s.closed {
			if s.closeErr != nil && s.closeErr != io.EOF {
				return 0, s.closeErr
			}
			return 0, io.EOF
		}
		s.cond.Wait()
	}
}

// Close detaches this reader from the session (the session itself keeps
// running). Idempotent: a gateway may detach both on stream teardown and on an
// explicit Stop, and a double call must not under-count the session's followers.
func (r *sessionReader) Close() error {
	r.once.Do(r.s.detach)
	return nil
}
