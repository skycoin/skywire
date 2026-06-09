// Package pty pkg/pty/exec_pool_test.go
package pty

import (
	"errors"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// fakeExecSession is a stand-in for *PtyClient that counts Exec/Close calls
// and can be told to fail Exec (simulating a dead conn).
type fakeExecSession struct {
	execN  int
	failAt int // fail Exec at/after this call number; 0 = never
	closed int
}

func (f *fakeExecSession) Exec(*CommandExecReq) (*CommandExecResult, error) {
	f.execN++
	if f.failAt != 0 && f.execN >= f.failAt {
		return nil, errors.New("rpc: connection is shut down")
	}
	return &CommandExecResult{ExitCode: 0}, nil
}

func (f *fakeExecSession) Close() error { f.closed++; return nil }

func testKey() execKey {
	var pk cipher.PubKey
	return execKey{pk: pk, port: 22, dialer: "test"}
}

// A pooled session is reused across many acquires and never closed while live.
func TestExecPool_Reuse(t *testing.T) {
	h := &Host{}
	k := testKey()
	s := &fakeExecSession{}
	h.cacheExec(k, s)

	for i := 0; i < 3; i++ {
		pe := h.acquireExec(k)
		if pe == nil {
			t.Fatalf("acquire %d: expected pooled session, got nil", i)
		}
		if pe.sess != s {
			t.Fatalf("acquire %d: got a different session", i)
		}
		_, err := pe.sess.Exec(nil)
		h.releaseExec(k, pe, err)
	}
	if s.closed != 0 {
		t.Fatalf("live session closed %d times; want 0 (still pooled)", s.closed)
	}
}

// A failed Exec retires the session: next acquire misses, and it's closed once.
func TestExecPool_StaleEviction(t *testing.T) {
	h := &Host{}
	k := testKey()
	s := &fakeExecSession{failAt: 1}
	h.cacheExec(k, s)

	pe := h.acquireExec(k)
	if pe == nil {
		t.Fatal("acquire: expected session")
	}
	_, err := pe.sess.Exec(nil)
	if err == nil {
		t.Fatal("expected an Exec error from the fake")
	}
	h.releaseExec(k, pe, err)

	if got := h.acquireExec(k); got != nil {
		t.Fatal("acquire after stale: expected nil (evicted)")
	}
	if s.closed != 1 {
		t.Fatalf("stale session closed %d times; want exactly 1", s.closed)
	}
}

// An idle session past TTL is retired (and closed) on the next acquire.
func TestExecPool_IdleTTL(t *testing.T) {
	h := &Host{}
	k := testKey()
	s := &fakeExecSession{}
	h.cacheExec(k, s)

	h.execMu.Lock()
	h.execPool[k].lastUsed = time.Now().Add(-2 * execSessionTTL)
	h.execMu.Unlock()

	if got := h.acquireExec(k); got != nil {
		t.Fatal("acquire idle-expired: expected nil")
	}
	if s.closed != 1 {
		t.Fatalf("idle session closed %d times; want exactly 1", s.closed)
	}
}

// A session retired while a second Exec is in flight is closed exactly once,
// by the last releaseExec — never early (which would break the in-flight call).
func TestExecPool_ConcurrentReleaseClosesOnce(t *testing.T) {
	h := &Host{}
	k := testKey()
	s := &fakeExecSession{}
	h.cacheExec(k, s)

	pe1 := h.acquireExec(k)
	pe2 := h.acquireExec(k) // refs == 2, same instance
	if pe1 == nil || pe2 == nil || pe1 != pe2 {
		t.Fatal("expected the same pooled session acquired twice")
	}

	h.releaseExec(k, pe1, errors.New("broken")) // retires, but pe2 still in flight
	if s.closed != 0 {
		t.Fatalf("closed early while refs>0: closed=%d", s.closed)
	}
	h.releaseExec(k, pe2, nil) // last one out → close
	if s.closed != 1 {
		t.Fatalf("want exactly 1 close after last release, got %d", s.closed)
	}
	if got := h.acquireExec(k); got != nil {
		t.Fatal("retired session should not be re-acquirable")
	}
}
