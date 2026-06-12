// Package pty pkg/pty/pty_session_test.go
package pty

import (
	"io"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// fakePty is a controllable ptyIO for tests. feed() queues output the pump will
// Read; Stop()/close makes Read return EOF once drained.
type fakePty struct {
	mu       sync.Mutex
	cond     *sync.Cond
	out      []byte
	closed   bool
	closeErr error
	written  []byte
}

func newFakePty() *fakePty {
	f := &fakePty{}
	f.cond = sync.NewCond(&f.mu)
	return f
}

func (f *fakePty) Read(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for len(f.out) == 0 && !f.closed {
		f.cond.Wait()
	}
	if len(f.out) == 0 && f.closed {
		if f.closeErr != nil {
			return 0, f.closeErr
		}
		return 0, io.EOF
	}
	n := copy(p, f.out)
	f.out = f.out[n:]
	return n, nil
}

func (f *fakePty) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.written = append(f.written, p...)
	return len(p), nil
}

func (f *fakePty) SetPtySize(*WinSize) error { return nil }

func (f *fakePty) Stop() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.closed {
		f.closed = true
		f.cond.Broadcast()
	}
	return nil
}

func (f *fakePty) feed(b []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.out = append(f.out, b...)
	f.cond.Broadcast()
}

func mustReadWithin(t *testing.T, r io.Reader, buf []byte) int {
	t.Helper()
	type res struct {
		n   int
		err error
	}
	ch := make(chan res, 1)
	go func() { n, err := r.Read(buf); ch <- res{n, err} }()
	select {
	case rr := <-ch:
		require.NoError(t, rr.err)
		return rr.n
	case <-time.After(2 * time.Second):
		t.Fatal("read timed out")
		return 0
	}
}

func TestRingBuffer_WriteAndAt(t *testing.T) {
	r := newRingBuffer(8)
	r.write([]byte("abc"))
	require.Equal(t, 3, r.len())
	out := make([]byte, 16)
	require.Equal(t, "abc", string(out[:r.at(0, out)]))

	// Overflow: total "abcdefghij", cap 8 → keep newest "cdefghij".
	r.write([]byte("defghij"))
	require.Equal(t, 8, r.len())
	require.Equal(t, "cdefghij", string(out[:r.at(0, out)]))
	require.Equal(t, "hij", string(out[:r.at(5, out)]))
}

func TestRingBuffer_WriteLargerThanCap(t *testing.T) {
	r := newRingBuffer(4)
	r.write([]byte("abcdefgh")) // keep last 4
	out := make([]byte, 8)
	require.Equal(t, 4, r.len())
	require.Equal(t, "efgh", string(out[:r.at(0, out)]))
}

func TestPtySession_FollowerReceivesOutput(t *testing.T) {
	f := newFakePty()
	s := newPtySession("s1", cipher.PubKey{}, f)
	defer s.stop() //nolint:errcheck
	r := s.follow()
	defer r.Close() //nolint:errcheck

	f.feed([]byte("hello"))
	buf := make([]byte, 64)
	require.Equal(t, "hello", string(buf[:mustReadWithin(t, r, buf)]))
}

func TestPtySession_InputAndResizeForwardToPty(t *testing.T) {
	f := newFakePty()
	owner, _ := cipher.GenerateKeyPair()
	s := newPtySession("s2", owner, f)
	defer s.stop() //nolint:errcheck
	require.Equal(t, owner, s.ownerPK)

	_, err := s.write([]byte("ls -la\n"))
	require.NoError(t, err)
	require.NoError(t, s.setSize(&WinSize{Cols: 80, Rows: 24}))

	f.mu.Lock()
	got := string(f.written)
	f.mu.Unlock()
	require.Equal(t, "ls -la\n", got)
}

func TestPtySession_DetachKeepsRunningThenReplay(t *testing.T) {
	f := newFakePty()
	s := newPtySession("s1", cipher.PubKey{}, f)
	defer s.stop() //nolint:errcheck

	r := s.follow()
	f.feed([]byte("first"))
	buf := make([]byte, 128)
	require.Equal(t, "first", string(buf[:mustReadWithin(t, r, buf)]))

	// Detach; the "shell" keeps producing while nobody is attached.
	require.NoError(t, r.Close())
	f.feed([]byte("while-away"))
	require.Eventually(t, func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.total == int64(len("firstwhile-away"))
	}, time.Second, 5*time.Millisecond)

	// Reattach with replay → sees the output produced while detached.
	r2 := s.followReplay()
	defer r2.Close() //nolint:errcheck
	got := string(buf[:mustReadWithin(t, r2, buf)])
	require.Contains(t, got, "while-away")
}

func TestPtySession_StopUnblocksFollower(t *testing.T) {
	f := newFakePty()
	s := newPtySession("s1", cipher.PubKey{}, f)
	r := s.follow()
	defer r.Close() //nolint:errcheck

	go func() { time.Sleep(20 * time.Millisecond); _ = s.stop() }() //nolint:errcheck,gosec
	buf := make([]byte, 16)
	_, err := r.Read(buf) // blocks until stop → EOF
	require.ErrorIs(t, err, io.EOF)
	require.True(t, s.isClosed())
}

func TestPtySession_FellBehindSkipsGap(t *testing.T) {
	f := newFakePty()
	s := newPtySession("s1", cipher.PubKey{}, f)
	defer s.stop() //nolint:errcheck

	r := s.follow() // positioned at 0
	defer r.Close() //nolint:errcheck

	// Push more than the ring can hold WITHOUT the reader consuming, so the
	// reader's offset (0) falls behind the oldest retained byte.
	big := make([]byte, ptyRingCap+1024)
	for i := range big {
		big[i] = byte('A' + i%26)
	}
	f.feed(big)
	require.Eventually(t, func() bool {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.total == int64(len(big))
	}, time.Second, 5*time.Millisecond)

	// Read skips the overwritten gap and returns only retained (newest) bytes.
	buf := make([]byte, ptyRingCap*2)
	n := mustReadWithin(t, r, buf)
	require.LessOrEqual(t, n, ptyRingCap)
	require.Greater(t, n, 0)
}

func TestPtySession_IdleSince(t *testing.T) {
	f := newFakePty()
	s := newPtySession("s1", cipher.PubKey{}, f)
	defer s.stop() //nolint:errcheck

	require.Zero(t, s.idleSince(time.Now())) // never attached
	r := s.follow()
	require.Zero(t, s.idleSince(time.Now())) // attached
	require.NoError(t, r.Close())
	require.Greater(t, s.idleSince(time.Now().Add(time.Second)), time.Duration(0))
}
