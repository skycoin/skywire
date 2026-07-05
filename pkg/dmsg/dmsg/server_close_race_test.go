package dmsg

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/net/nettest"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
)

// TestServer_CloseDuringSessionSetup is a regression test for a shutdown
// deadlock: Server.Close()'s s.wg.Wait() would hang forever when an inbound
// session was still mid-setup at the moment Close fired.
//
// handleSession spawns an awaitDone guard goroutine that closes the session
// when s.done fires — but SessionCommon.Close only closes a stream mux that is
// already installed. A Close that lands during the setup window (after the
// guard is spawned, before the yamux/smux is set) ran that guard as a no-op and
// left the session to Serve()/AcceptStream forever, so its wg-tracked
// handleSession goroutine never returned and Close() blocked in wg.Wait(). The
// crash surfaced when two peered servers were Closed in sequence (the mesh
// continually re-dials peer sessions, so one is usually mid-setup).
//
// The fix re-checks isClosed(s.done) after the mux is installed and closes the
// session so Serve returns immediately. Without that fix this test times out at
// the select below; with it, Close() returns promptly.
func TestServer_CloseDuringSessionSetup(t *testing.T) {
	dc := disc.NewMock(0)

	pk, sk := cipher.GenerateKeyPair()
	lis, err := nettest.NewLocalListener("tcp")
	require.NoError(t, err)

	srv := NewServer(pk, sk, dc, &ServerConfig{MaxSessions: 10, UpdateInterval: DefaultUpdateInterval}, nil)

	// closeReturned fires once the concurrent Close() (kicked off from inside the
	// setup window) has fully drained s.wg — the thing that used to deadlock.
	closeReturned := make(chan struct{})
	var once sync.Once
	testHookHandleSessionPreMux = func(s *Server) {
		once.Do(func() {
			// Deterministically reproduce the race: start Close() concurrently,
			// wait until it has signaled shutdown (s.done), and give the
			// awaitDone guard a moment to observe s.done and run its (no-op)
			// session Close — all BEFORE this session installs its mux.
			go func() {
				_ = srv.Close() //nolint
				close(closeReturned)
			}()
			<-s.done
			time.Sleep(100 * time.Millisecond)
		})
	}
	defer func() { testHookHandleSessionPreMux = nil }()

	go srv.Serve(lis, "") //nolint:errcheck
	<-srv.Ready()

	// A client dials the server, driving one handleSession through the hook.
	cpk, csk := cipher.GenerateKeyPair()
	cli := NewClient(cpk, csk, dc, &Config{MinSessions: 1})
	go cli.Serve(context.Background()) //nolint:errcheck
	defer cli.Close()                  //nolint:errcheck

	select {
	case <-closeReturned:
	case <-time.After(20 * time.Second):
		t.Fatal("Server.Close() deadlocked: a session mid-setup at shutdown was not guarded")
	}
}
