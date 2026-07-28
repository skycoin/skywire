// Package commands cmd/apps/skychat/commands/tcp_direct_loops_test.go
//
// Coverage for the TCP-direct transport loops, which were the last of the
// lifecycle set still at 0%: runTCPListen, acceptTCPConn, runTCPPeerDialers,
// peerDialLoop and startTCPDirect.
//
// Unlike the dmsg/skynet paths these need no mesh at all — they are real TCP
// plus a noise-XK handshake, so the tests run both ends on 127.0.0.1 and drive
// actual encrypted frames through the production accept path into the shared
// DM controller.
//
// The load-bearing assertion is the WHITELIST GATE in acceptTCPConn. Its
// semantics are deliberately inverted from what "whitelist" suggests — an
// EMPTY list means OPEN (accept any authenticated peer), matching the skywire
// convention, while a NON-EMPTY list restricts. Getting that backwards either
// locks every operator out or silently serves the world, and neither shows up
// until someone is already exposed, so both directions are pinned here.
//
// Goroutine/cleanup note: these loops read the package-level chatCtrl. Every
// test that reaches chatCtrl.Serve waits until the conn is actually cached
// (HasConn takes the controller's mutex, which orders that read before the
// harness restores the global) or joins the goroutine it owns. Canceling and
// hoping is not enough for the race detector — see lifecycle_test.go.
package commands

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skychat/dm"
	"github.com/skycoin/skywire/pkg/skychat/message"
	"github.com/skycoin/skywire/pkg/skywire/tcpnoise"
)

// --- harness ----------------------------------------------------------------

// withTCPEnv installs a controller whose surfaced events the test can read,
// and restores every tcp-direct flag var.
func withTCPEnv(t *testing.T) <-chan dm.Event {
	t.Helper()
	if appLog == nil {
		appLog = func(string, ...any) {}
	}
	withChatLog(t)

	origCtrl := chatCtrl
	origListen, origPeers := tcpListen, tcpPeers
	origWL, origSK, origCfg := tcpWhitelist, tcpSKFlag, tcpConfigPath

	events := make(chan dm.Event, 16)
	ctrl := dm.New(dm.Config{
		OnEvent: func(ev dm.Event) {
			select {
			case events <- ev:
			default:
			}
		},
	})
	chatCtrl = ctrl

	// DMSGCURL_SK would otherwise leak an operator's real identity into
	// resolveTCPIdentity; t.Setenv restores it after the test.
	t.Setenv("DMSGCURL_SK", "")
	tcpListen, tcpPeers, tcpWhitelist, tcpSKFlag, tcpConfigPath = "", nil, "", "", ""

	t.Cleanup(func() {
		_ = ctrl.Close() //nolint:errcheck // unblocks any Serve still reading
		chatCtrl = origCtrl
		tcpListen, tcpPeers = origListen, origPeers
		tcpWhitelist, tcpSKFlag, tcpConfigPath = origWL, origSK, origCfg
	})
	return events
}

// freeTCPAddr reserves a loopback port and releases it, so the caller can hand
// the address to code that opens its own listener.
func freeTCPAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a port: %v", err)
	}
	addr := l.Addr().String()
	_ = l.Close() //nolint:errcheck
	return addr
}

// waitListening blocks until addr accepts TCP connections.
func waitListening(t *testing.T, addr string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		c, err := net.DialTimeout("tcp", addr, 200*time.Millisecond)
		if err == nil {
			_ = c.Close() //nolint:errcheck
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("nothing listening on %s after 5s", addr)
}

// waitConnCached polls until the controller has cached a conn for pk. The
// HasConn read takes the controller's mutex, which is what orders the
// goroutine's chatCtrl access before this test's cleanup.
func waitConnCached(t *testing.T, pk cipher.PubKey) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if chatCtrl.HasConn(pk) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("the controller never cached a conn for %s", pk.Hex()[:8])
}

func waitEvent(t *testing.T, ch <-chan dm.Event, d time.Duration) dm.Event {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(d):
		t.Fatal("no chat event surfaced from the tcp-direct conn")
		return dm.Event{}
	}
}

// startListener runs runTCPListen in the background and returns its address
// plus a stop func that cancels it and joins.
func startListener(t *testing.T, pk cipher.PubKey, sk cipher.SecKey,
	wl map[cipher.PubKey]struct{}) (addr string, stop func()) {
	t.Helper()
	addr = freeTCPAddr(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runTCPListen(ctx, addr, pk, sk, wl) }()
	waitListening(t, addr)

	return addr, func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("runTCPListen returned %v, want nil on context cancel", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("runTCPListen did not return after its context was canceled")
		}
	}
}

// --- runTCPListen / acceptTCPConn -------------------------------------------

// TestRunTCPListen_ServesWhitelistedPeer drives a full noise handshake and a
// real chat frame through the production accept path.
func TestRunTCPListen_ServesWhitelistedPeer(t *testing.T) {
	events := withTCPEnv(t)
	srvPK, srvSK := cipher.GenerateKeyPair()
	cliPK, cliSK := cipher.GenerateKeyPair()

	addr, stop := startListener(t, srvPK, srvSK, map[cipher.PubKey]struct{}{cliPK: {}})
	defer stop()

	conn, err := tcpnoise.Dial(context.Background(), addr, cliPK, cliSK, srvPK)
	if err != nil {
		t.Fatalf("noise dial: %v", err)
	}
	defer func() { _ = conn.Close() }() //nolint:errcheck

	// The accepted conn is cached under the noise-learned PK, so the existing
	// DM machinery (send, receipts, stale-conn eviction) works over TCP too.
	waitConnCached(t, cliPK)

	if err := message.WriteFrame(conn, []byte("over tcp-direct")); err != nil {
		t.Fatalf("write frame: %v", err)
	}
	ev := waitEvent(t, events, 5*time.Second)
	if ev.Text != "over tcp-direct" {
		t.Errorf("surfaced text = %q, want %q", ev.Text, "over tcp-direct")
	}
	if ev.Peer != cliPK.Hex() {
		t.Errorf("event peer = %s, want the noise-authenticated %s", ev.Peer, cliPK.Hex()[:8])
	}
	if ev.Dir != "in" {
		t.Errorf("event dir = %q, want in", ev.Dir)
	}
	// The transport is tagged so operators can tell which path carried it.
	if ev.Network != string(appnet.TypeTCPDirect) {
		t.Errorf("event network = %q, want the tcp-direct tag %q", ev.Network, appnet.TypeTCPDirect)
	}
}

// TestRunTCPListen_RejectsNonWhitelistedPeer — a NON-EMPTY whitelist restricts.
// The peer still completes the noise handshake (it is authenticated), then the
// gate drops it: no cached conn, and the socket is closed under it.
func TestRunTCPListen_RejectsNonWhitelistedPeer(t *testing.T) {
	withTCPEnv(t)
	srvPK, srvSK := cipher.GenerateKeyPair()
	cliPK, cliSK := cipher.GenerateKeyPair()
	allowed, _ := cipher.GenerateKeyPair() // someone else

	addr, stop := startListener(t, srvPK, srvSK, map[cipher.PubKey]struct{}{allowed: {}})
	defer stop()

	conn, err := tcpnoise.Dial(context.Background(), addr, cliPK, cliSK, srvPK)
	if err != nil {
		t.Fatalf("noise dial: %v", err)
	}
	defer func() { _ = conn.Close() }() //nolint:errcheck

	// The server hangs up rather than serving. A read TIMEOUT would mean the
	// conn was left open (i.e. we were served after all), so the error has to
	// be a real close, not a deadline.
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second)) //nolint:errcheck
	_, rerr := message.ReadFrame(conn)
	if rerr == nil {
		t.Fatal("a non-whitelisted peer should have its conn closed, not served")
	}
	var nerr net.Error
	if errors.As(rerr, &nerr) && nerr.Timeout() {
		t.Errorf("read timed out (%v) instead of failing on a closed conn — the peer was served", rerr)
	}
	if chatCtrl.HasConn(cliPK) {
		t.Error("a non-whitelisted peer must not be cached in the controller")
	}
}

// TestRunTCPListen_EmptyWhitelistIsOpen pins the inverted semantics: an empty
// list is OPEN (any authenticated peer), matching skynet's Server.useWL and the
// CXO treestore's nil-allowlist. Reading it as "deny all" would lock out every
// operator who set --tcp-listen without --tcp-whitelist.
func TestRunTCPListen_EmptyWhitelistIsOpen(t *testing.T) {
	withTCPEnv(t)
	srvPK, srvSK := cipher.GenerateKeyPair()
	cliPK, cliSK := cipher.GenerateKeyPair()

	addr, stop := startListener(t, srvPK, srvSK, map[cipher.PubKey]struct{}{})
	defer stop()

	conn, err := tcpnoise.Dial(context.Background(), addr, cliPK, cliSK, srvPK)
	if err != nil {
		t.Fatalf("noise dial: %v", err)
	}
	defer func() { _ = conn.Close() }() //nolint:errcheck

	waitConnCached(t, cliPK)
}

func TestRunTCPListen_BindFailureReturnsError(t *testing.T) {
	withTCPEnv(t)
	pk, sk := cipher.GenerateKeyPair()

	err := runTCPListen(context.Background(), "127.0.0.1:not-a-port", pk, sk, nil)
	if err == nil {
		t.Error("binding an invalid address should return an error, not block")
	}
}

// TestRunTCPListen_ContextCancelIsCleanShutdown — cancel must close the
// listener and return nil, not surface the resulting "use of closed network
// connection" as a failure.
func TestRunTCPListen_ContextCancelIsCleanShutdown(t *testing.T) {
	withTCPEnv(t)
	pk, sk := cipher.GenerateKeyPair()

	addr, stop := startListener(t, pk, sk, nil)
	stop() // asserts a nil return internally

	// The port is released, so it can be bound again.
	l, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("port %s still held after shutdown: %v", addr, err)
	}
	_ = l.Close() //nolint:errcheck
}

// TestAcceptTCPConn_HandshakeFailureDropsConn — a peer that hangs up mid
// handshake (or speaks something other than noise) must not be served.
func TestAcceptTCPConn_HandshakeFailureDropsConn(t *testing.T) {
	withTCPEnv(t)
	srvPK, srvSK := cipher.GenerateKeyPair()

	server, client := net.Pipe()
	_ = client.Close() //nolint:errcheck // hang up before the handshake starts

	done := make(chan struct{})
	go func() {
		defer close(done)
		acceptTCPConn(server, srvPK, srvSK, nil)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("acceptTCPConn should return promptly when the handshake fails")
	}
	if n := chatCtrl.Conns(); n != 0 {
		t.Errorf("a failed handshake cached %d conns, want 0", n)
	}
}

// --- peerDialLoop / runTCPPeerDialers ---------------------------------------

// noiseEchoServer accepts noise conns and hands each accepted conn to the
// returned channel, so a test can observe reconnects and close conns at will.
func noiseEchoServer(t *testing.T, pk cipher.PubKey, sk cipher.SecKey) (addr string, accepted <-chan net.Conn) {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = lis.Close() }) //nolint:errcheck

	ch := make(chan net.Conn, 8)
	go func() {
		for {
			raw, aerr := lis.Accept()
			if aerr != nil {
				return
			}
			go func(raw net.Conn) {
				c, _, herr := tcpnoise.Accept(raw, pk, sk)
				if herr != nil {
					return
				}
				select {
				case ch <- c:
				default:
					_ = c.Close() //nolint:errcheck
				}
			}(raw)
		}
	}()
	return lis.Addr().String(), ch
}

// TestPeerDialLoop_ConnectsAndRedialsAfterDisconnect — a dropped peer must be
// picked back up. Backoff resets on a successful dial, so the redial after a
// disconnect is immediate rather than waiting out the previous window.
func TestPeerDialLoop_ConnectsAndRedialsAfterDisconnect(t *testing.T) {
	withTCPEnv(t)
	srvPK, srvSK := cipher.GenerateKeyPair()
	cliPK, cliSK := cipher.GenerateKeyPair()

	addr, accepted := noiseEchoServer(t, srvPK, srvSK)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		peerDialLoop(ctx, srvPK, addr, cliPK, cliSK)
	}()

	first := <-acceptedWithin(t, accepted)
	waitConnCached(t, srvPK)

	// Drop the peer; the loop should come straight back.
	_ = first.Close() //nolint:errcheck
	second := <-acceptedWithin(t, accepted)

	cancel()
	_ = second.Close() //nolint:errcheck // unblocks the Serve the loop is inside
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("peerDialLoop did not exit after its context was canceled")
	}
}

// acceptedWithin adapts a receive-with-timeout into a channel expression.
func acceptedWithin(t *testing.T, ch <-chan net.Conn) <-chan net.Conn {
	t.Helper()
	out := make(chan net.Conn, 1)
	select {
	case c := <-ch:
		out <- c
	case <-time.After(10 * time.Second):
		t.Fatal("peer never connected")
	}
	return out
}

// TestPeerDialLoop_UnreachablePeerBacksOffAndExits — the backoff sleep must be
// interruptible, or shutdown stalls for up to tcpReconnectMax.
func TestPeerDialLoop_UnreachablePeerBacksOffAndExits(t *testing.T) {
	withTCPEnv(t)
	srvPK, _ := cipher.GenerateKeyPair()
	cliPK, cliSK := cipher.GenerateKeyPair()

	dead := freeTCPAddr(t) // reserved then released: nothing is listening

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		peerDialLoop(ctx, srvPK, dead, cliPK, cliSK)
	}()

	// Let the first dial fail and the loop enter its backoff sleep.
	time.Sleep(300 * time.Millisecond)
	cancel()

	start := time.Now()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("peerDialLoop did not exit during its backoff window")
	}
	if elapsed := time.Since(start); elapsed > tcpReconnectMin {
		t.Errorf("exit took %v — cancel should interrupt the backoff, not wait it out", elapsed)
	}
}

func TestRunTCPPeerDialers_SkipsBadSpecsAndDialsGoodOnes(t *testing.T) {
	withTCPEnv(t)
	srvPK, srvSK := cipher.GenerateKeyPair()
	cliPK, cliSK := cipher.GenerateKeyPair()

	addr, accepted := noiseEchoServer(t, srvPK, srvSK)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runTCPPeerDialers(ctx, []string{
		"not-a-url",                         // no tcp:// prefix
		"tcp://missing-at-separator",        // no @
		"tcp://deadbeef@127.0.0.1:1",        // bad pk
		"tcp://" + srvPK.Hex() + "@" + addr, // the good one
	}, cliPK, cliSK)

	conn := <-acceptedWithin(t, accepted)
	waitConnCached(t, srvPK)
	cancel()
	_ = conn.Close() //nolint:errcheck
}

// --- startTCPDirect ---------------------------------------------------------

func TestStartTCPDirect_DisabledIsNoOp(t *testing.T) {
	withTCPEnv(t)
	tcpListen, tcpPeers = "", nil

	if err := startTCPDirect(context.Background()); err != nil {
		t.Errorf("with neither flag set startTCPDirect should be a no-op, got %v", err)
	}
}

// TestStartTCPDirect_RequiresIdentity — TCP-direct pins peers by PK, so an
// ephemeral key would defeat the trust model. Enabling it without an identity
// must fail loudly at startup rather than silently generating one.
func TestStartTCPDirect_RequiresIdentity(t *testing.T) {
	withTCPEnv(t)
	tcpListen = freeTCPAddr(t)
	tcpSKFlag, tcpConfigPath = "", ""

	err := startTCPDirect(context.Background())
	if err == nil {
		t.Fatal("enabling tcp-direct with no configured identity should error")
	}
	if !strings.Contains(err.Error(), "identity") {
		t.Errorf("err = %v, want it to name the missing identity", err)
	}
}

func TestStartTCPDirect_ListensWithFlagIdentity(t *testing.T) {
	events := withTCPEnv(t)
	srvPK, srvSK := cipher.GenerateKeyPair()
	cliPK, cliSK := cipher.GenerateKeyPair()

	addr := freeTCPAddr(t)
	tcpListen = addr
	tcpSKFlag = srvSK.Hex()
	tcpWhitelist = cliPK.Hex()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := startTCPDirect(ctx); err != nil {
		t.Fatalf("startTCPDirect: %v", err)
	}
	waitListening(t, addr)

	conn, err := tcpnoise.Dial(context.Background(), addr, cliPK, cliSK, srvPK)
	if err != nil {
		t.Fatalf("noise dial: %v", err)
	}
	defer func() { _ = conn.Close() }() //nolint:errcheck

	waitConnCached(t, cliPK)
	if err := message.WriteFrame(conn, []byte("via startTCPDirect")); err != nil {
		t.Fatalf("write frame: %v", err)
	}
	if ev := waitEvent(t, events, 5*time.Second); ev.Text != "via startTCPDirect" {
		t.Errorf("surfaced text = %q", ev.Text)
	}
}

func TestStartTCPDirect_DialsConfiguredPeers(t *testing.T) {
	withTCPEnv(t)
	srvPK, srvSK := cipher.GenerateKeyPair()
	_, cliSK := cipher.GenerateKeyPair()

	addr, accepted := noiseEchoServer(t, srvPK, srvSK)
	tcpPeers = []string{"tcp://" + srvPK.Hex() + "@" + addr}
	tcpSKFlag = cliSK.Hex()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := startTCPDirect(ctx); err != nil {
		t.Fatalf("startTCPDirect: %v", err)
	}

	conn := <-acceptedWithin(t, accepted)
	waitConnCached(t, srvPK)
	cancel()
	_ = conn.Close() //nolint:errcheck
}
