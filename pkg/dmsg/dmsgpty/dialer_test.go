// Package dmsgpty pkg/dmsg/dmsgpty/dialer_test.go — pins the StreamDialer
// abstraction added so the outbound proxy-dial side of Host is
// pluggable. Phase 1 contract: the dialer field is what gets invoked
// on ExecRemote / handleProxy paths, not the bound *dmsg.Client.
package dmsgpty

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// fakeDialer captures the (pk, port) of each DialStream call and
// returns a fixed err. The err short-circuits the rest of ExecRemote
// before it touches the conn — the test only needs to prove the
// dialer field is what gets reached, not that an actual PtyClient
// boots over the returned conn.
type fakeDialer struct {
	calls   []fakeDialCall
	dialErr error
}

type fakeDialCall struct {
	pk   cipher.PubKey
	port uint16
}

func (f *fakeDialer) DialStream(_ context.Context, pk cipher.PubKey, port uint16) (net.Conn, error) {
	f.calls = append(f.calls, fakeDialCall{pk: pk, port: port})
	return nil, f.dialErr
}

func TestHost_ExecRemote_RoutesThroughInjectedDialer(t *testing.T) {
	// Custom dialer fed via NewHostWithDialer is what ExecRemote
	// invokes — proves the Host doesn't reach past the abstraction
	// to its bound dmsg.Client for the outbound side.
	pk, _ := cipher.GenerateKeyPair()
	want := errors.New("fake dial failed")
	fake := &fakeDialer{dialErr: want}

	// dmsgC stays nil because the listening side isn't exercised;
	// only the outbound path is under test. If anything reached for
	// h.dmsgC.DialStream pre-refactor, this test would NPE — the
	// existence of a non-NPE failure surface is the contract.
	h := NewHostWithDialer(nil, nil, fake)

	_, err := h.ExecRemote(context.Background(), pk, 22, &CommandExecReq{Name: "true"})
	if err == nil {
		t.Fatal("ExecRemote: want error from injected dialer, got nil")
	}
	if !errors.Is(err, want) {
		t.Errorf("ExecRemote err = %v, want it to wrap %v", err, want)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("dialer calls: got %d, want 1", len(fake.calls))
	}
	got := fake.calls[0]
	if got.pk != pk {
		t.Errorf("dialer call pk = %s, want %s", got.pk, pk)
	}
	if got.port != 22 {
		t.Errorf("dialer call port = %d, want 22", got.port)
	}
}

func TestHost_ExecRemote_DefaultPortFallback(t *testing.T) {
	// rPort=0 falls back to DefaultPort (matches pre-refactor
	// ExecRemote behavior). The fallback happens BEFORE the dialer
	// is invoked, so the dialer sees the resolved port.
	pk, _ := cipher.GenerateKeyPair()
	fake := &fakeDialer{dialErr: errors.New("stop early")}
	h := NewHostWithDialer(nil, nil, fake)

	_, _ = h.ExecRemote(context.Background(), pk, 0, &CommandExecReq{Name: "true"}) //nolint:errcheck
	if len(fake.calls) != 1 {
		t.Fatalf("dialer calls: got %d, want 1", len(fake.calls))
	}
	if got := fake.calls[0].port; got != DefaultPort {
		t.Errorf("port with rPort=0: got %d, want DefaultPort=%d", got, DefaultPort)
	}
}

// stubConn is a minimal net.Conn just to give MultiDialer something
// non-nil to return on the success path. No I/O is performed.
type stubConn struct{ net.Conn }

func TestMultiDialer_FirstSuccessWins(t *testing.T) {
	// First strategy succeeds → later strategies must not be invoked.
	// Pins the short-circuit: if skynet routes succeed, dmsg fallback
	// shouldn't open a parallel stream.
	pk, _ := cipher.GenerateKeyPair()
	stub := &stubConn{}
	first := &captureDialer{conn: stub}
	second := &captureDialer{err: errors.New("must-not-be-called")}

	conn, err := MultiDialer{first, second}.DialStream(context.Background(), pk, 22)
	if err != nil {
		t.Fatalf("DialStream: unexpected err %v", err)
	}
	if conn != stub {
		t.Errorf("DialStream returned %v, want stub conn", conn)
	}
	if first.count != 1 {
		t.Errorf("first.count = %d, want 1", first.count)
	}
	if second.count != 0 {
		t.Errorf("second.count = %d, want 0 (later strategy must short-circuit on first success)", second.count)
	}
}

func TestMultiDialer_FallsThroughOnError(t *testing.T) {
	// First strategy errors → next strategy is tried. Pins the
	// transport-fallback behavior (skynet fails → dmsg succeeds).
	pk, _ := cipher.GenerateKeyPair()
	stub := &stubConn{}
	first := &captureDialer{err: errors.New("skynet unreachable")}
	second := &captureDialer{conn: stub}

	conn, err := MultiDialer{first, second}.DialStream(context.Background(), pk, 22)
	if err != nil {
		t.Fatalf("DialStream: unexpected err %v", err)
	}
	if conn != stub {
		t.Errorf("DialStream returned %v, want stub conn", conn)
	}
	if first.count != 1 || second.count != 1 {
		t.Errorf("call counts: first=%d second=%d, want 1/1", first.count, second.count)
	}
}

func TestMultiDialer_AllFailJoinsErrors(t *testing.T) {
	// Every strategy errors → caller gets every strategy's error
	// for diagnostics (errors.Join surface). Operators need to see
	// "skynet timed out AND dmsg refused" not just one.
	pk, _ := cipher.GenerateKeyPair()
	errA := errors.New("skynet timeout")
	errB := errors.New("dmsg refused")
	a := &captureDialer{err: errA}
	b := &captureDialer{err: errB}

	_, err := MultiDialer{a, b}.DialStream(context.Background(), pk, 22)
	if err == nil {
		t.Fatal("DialStream: want error, got nil")
	}
	if !errors.Is(err, errA) || !errors.Is(err, errB) {
		t.Errorf("joined err missing a sub-error: %v (want both %v and %v)", err, errA, errB)
	}
}

func TestMultiDialer_EmptyErrors(t *testing.T) {
	// An empty MultiDialer is a configuration bug, not a runtime
	// crash. Errors cleanly so the caller sees the misconfiguration.
	pk, _ := cipher.GenerateKeyPair()
	_, err := MultiDialer{}.DialStream(context.Background(), pk, 22)
	if err == nil {
		t.Fatal("DialStream: empty MultiDialer should error, got nil")
	}
}

// captureDialer counts invocations and returns a fixed (conn, err).
// Distinct from fakeDialer above: that one captures full call args
// (pk, port) which the MultiDialer tests don't need.
type captureDialer struct {
	conn  net.Conn
	err   error
	count int
}

func (c *captureDialer) DialStream(_ context.Context, _ cipher.PubKey, _ uint16) (net.Conn, error) {
	c.count++
	return c.conn, c.err
}

// memListener is a minimal net.Listener that yields a single
// in-memory pipe conn on its first Accept and blocks until ctx
// expires. Lets us drive ListenAndServeNet through one accept cycle
// to exercise the extractor branch.
type memListener struct {
	conn     net.Conn
	served   bool
	done     chan struct{}
	closeOne sync.Once
}

func newMemListener(conn net.Conn) *memListener {
	return &memListener{conn: conn, done: make(chan struct{})}
}

func (l *memListener) Accept() (net.Conn, error) {
	if l.served {
		<-l.done
		return nil, net.ErrClosed
	}
	l.served = true
	return l.conn, nil
}

// Close is idempotent — ListenAndServeNet's ctx-done goroutine and
// the test cleanup both call Close, so double-close must not panic.
func (l *memListener) Close() error {
	l.closeOne.Do(func() { close(l.done) })
	return nil
}

func (l *memListener) Addr() net.Addr { return &net.TCPAddr{} }

func TestListenAndServeNet_RejectsConnWhenExtractorReturnsFalse(t *testing.T) {
	// Defense-in-depth: a conn whose extractor returns ok=false is
	// closed before any pty handshake. Whitelist isn't even
	// consulted — the conn type itself is foreign and shouldn't
	// reach the trust gate.
	server, client := net.Pipe()
	defer client.Close() //nolint:errcheck

	h := NewHostWithDialer(nil, NewMemoryWhitelist(), &fakeDialer{})
	lis := newMemListener(server)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	// Extractor returns false → conn must be closed without
	// authorize() being reached.
	done := make(chan error, 1)
	go func() {
		done <- h.ListenAndServeNet(ctx, lis, func(net.Conn) (cipher.PubKey, bool) {
			return cipher.PubKey{}, false
		})
	}()

	// Wait for the server-side conn to be closed by the handler.
	buf := make([]byte, 1)
	_ = server.SetReadDeadline(time.Now().Add(150 * time.Millisecond)) //nolint:errcheck,gosec
	_, err := server.Read(buf)
	if err == nil {
		t.Error("server conn was not closed; extractor-false should drop the conn")
	}
	_ = lis.Close() //nolint:errcheck,gosec
	<-done
}
