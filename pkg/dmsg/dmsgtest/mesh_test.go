// Package dmsgtest pkg/dmsgtest/mesh_test.go
//
//nolint:errcheck,gosec
package dmsgtest

import (
	"bytes"
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/nettest"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
)

// TestServerMesh_CrossServerDial verifies that a client connected to server A
// can dial a client connected to server B when the two servers are peered.
// Each client uses a separate filtered discovery so it only sees one server.
func TestServerMesh_CrossServerDial(t *testing.T) {
	const timeout = 30 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Shared discovery for servers to register in.
	sharedDC := disc.NewMock(0)

	// --- Server A ---
	pkA, skA := cipher.GenerateKeyPair()
	lisA, err := nettest.NewLocalListener("tcp")
	require.NoError(t, err)

	// --- Server B ---
	pkB, skB := cipher.GenerateKeyPair()
	lisB, err := nettest.NewLocalListener("tcp")
	require.NoError(t, err)

	// Configure servers as static peers of each other.
	confA := &dmsg.ServerConfig{
		MaxSessions:    10,
		UpdateInterval: dmsg.DefaultUpdateInterval,
		Peers: []dmsg.PeerEntry{
			{PK: pkB, Addr: lisB.Addr().String()},
		},
	}
	confB := &dmsg.ServerConfig{
		MaxSessions:    10,
		UpdateInterval: dmsg.DefaultUpdateInterval,
		Peers: []dmsg.PeerEntry{
			{PK: pkA, Addr: lisA.Addr().String()},
		},
	}

	srvA := dmsg.NewServer(pkA, skA, sharedDC, confA, nil)
	srvB := dmsg.NewServer(pkB, skB, sharedDC, confB, nil)

	// Start servers.
	var srvWg sync.WaitGroup
	srvWg.Add(2)
	go func() {
		defer srvWg.Done()
		if err := srvA.Serve(lisA, ""); err != nil {
			t.Logf("Server A stopped: %v", err)
		}
	}()
	go func() {
		defer srvWg.Done()
		if err := srvB.Serve(lisB, ""); err != nil {
			t.Logf("Server B stopped: %v", err)
		}
	}()

	select {
	case <-srvA.Ready():
	case <-ctx.Done():
		t.Fatal("Server A not ready")
	}
	select {
	case <-srvB.Ready():
	case <-ctx.Done():
		t.Fatal("Server B not ready")
	}

	// Give peer connections time to establish.
	// macOS CI runners need extra time for noise handshakes between servers.
	time.Sleep(5 * time.Second)

	// --- Client 1: only sees Server A ---
	dcA := disc.NewMock(0)
	entryA, err := sharedDC.Entry(ctx, pkA)
	require.NoError(t, err)
	require.NoError(t, dcA.PostEntry(ctx, entryA))

	pk1, sk1 := cipher.GenerateKeyPair()
	client1 := dmsg.NewClient(pk1, sk1, dcA, &dmsg.Config{MinSessions: 1})
	go client1.Serve(ctx)
	select {
	case <-client1.Ready():
	case <-ctx.Done():
		t.Fatal("Client 1 not ready")
	}

	// Register client1 in shared discovery so server B can look it up if needed.
	entry1, _ := dcA.Entry(ctx, pk1)
	sharedDC.PostEntry(ctx, entry1)

	// --- Client 2: only sees Server B ---
	dcB := disc.NewMock(0)
	entryB, err := sharedDC.Entry(ctx, pkB)
	require.NoError(t, err)
	require.NoError(t, dcB.PostEntry(ctx, entryB))

	pk2, sk2 := cipher.GenerateKeyPair()
	client2 := dmsg.NewClient(pk2, sk2, dcB, &dmsg.Config{MinSessions: 1})
	go client2.Serve(ctx)
	select {
	case <-client2.Ready():
	case <-ctx.Done():
		t.Fatal("Client 2 not ready")
	}

	// Register client2 in shared discovery so server A can route to it.
	entry2, _ := dcB.Entry(ctx, pk2)
	sharedDC.PostEntry(ctx, entry2)

	// Also register client2's entry in client1's discovery so client1 can
	// look up client2's delegated servers for dialing.
	dcA.PostEntry(ctx, entry2)

	// --- Cross-server dial: Client 1 (on A) dials Client 2 (on B) ---
	const port = uint16(200)

	lis2, err := client2.Listen(port)
	require.NoError(t, err)
	defer lis2.Close()

	// Retry cross-server dial — peer mesh connection may still be establishing.
	var stream1 *dmsg.Stream
	for attempt := 0; attempt < 3; attempt++ {
		stream1, err = client1.DialStream(ctx, dmsg.Addr{PK: pk2, Port: port})
		if err == nil {
			break
		}
		t.Logf("Cross-server dial attempt %d failed: %v", attempt+1, err)
		time.Sleep(2 * time.Second)
	}
	require.NoError(t, err, "Cross-server dial should succeed via peer mesh")
	defer stream1.Close()

	conn2, err := lis2.AcceptStream()
	require.NoError(t, err)
	defer conn2.Close()

	// --- Verify bidirectional data transfer ---
	payload := cipher.RandByte(1024)

	// Client 1 -> Client 2
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, wErr := stream1.Write(payload)
		assert.NoError(t, wErr)
	}()

	recv := make([]byte, len(payload))
	_, err = io.ReadFull(conn2, recv)
	require.NoError(t, err)
	wg.Wait()
	require.True(t, bytes.Equal(payload, recv), "data mismatch: client1 -> client2")

	// Client 2 -> Client 1
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, wErr := conn2.Write(payload)
		assert.NoError(t, wErr)
	}()

	recv2 := make([]byte, len(payload))
	_, err = io.ReadFull(stream1, recv2)
	require.NoError(t, err)
	wg.Wait()
	require.True(t, bytes.Equal(payload, recv2), "data mismatch: client2 -> client1")

	t.Log("Cross-server bidirectional stream test passed.")

	// Cleanup.
	client1.Close()
	client2.Close()
	srvA.Close()
	srvB.Close()
	srvWg.Wait()
}

// nonPublicReverseSetup stands up a public server S_pub (AcceptPeerAnnouncements
// = accept) and a non-public server S_priv that dials S_pub and announces
// itself as a forwardable peer; S_priv is NOT registered in the public
// discovery. It then connects client A (reachable only via S_priv) and client B
// (on S_pub), and attempts B's dial of A. The dial can only succeed via the
// announced reverse-forward path. Returns B's dial result so callers can assert
// both the gate-on (success) and gate-off (failure) behavior.
func nonPublicReverseSetup(t *testing.T, ctx context.Context, accept bool) (streamB *dmsg.Stream, accA func() (*dmsg.Stream, error), cleanup func(), dialErr error) {
	t.Helper()

	// Public discovery — only S_pub registers here.
	sharedDC := disc.NewMock(0)

	pkPub, skPub := cipher.GenerateKeyPair()
	lisPub, err := nettest.NewLocalListener("tcp")
	require.NoError(t, err)
	pkPriv, skPriv := cipher.GenerateKeyPair()

	confPub := &dmsg.ServerConfig{
		MaxSessions:             10,
		UpdateInterval:          dmsg.DefaultUpdateInterval,
		AcceptPeerAnnouncements: accept,
		AcceptedPeerPKs:         []cipher.PubKey{pkPriv},
	}

	lisPriv, err := nettest.NewLocalListener("tcp")
	require.NoError(t, err)
	privDC := disc.NewMock(0) // S_priv's own discovery, so its clients can find it
	confPriv := &dmsg.ServerConfig{
		MaxSessions:    10,
		UpdateInterval: dmsg.DefaultUpdateInterval,
		AnnounceAsPeer: true,
		Peers:          []dmsg.PeerEntry{{PK: pkPub, Addr: lisPub.Addr().String()}},
	}

	srvPub := dmsg.NewServer(pkPub, skPub, sharedDC, confPub, nil)
	srvPriv := dmsg.NewServer(pkPriv, skPriv, privDC, confPriv, nil)

	var srvWg sync.WaitGroup
	srvWg.Add(2)
	go func() { defer srvWg.Done(); srvPub.Serve(lisPub, "") }()
	go func() { defer srvWg.Done(); srvPriv.Serve(lisPriv, "") }()

	select {
	case <-srvPub.Ready():
	case <-ctx.Done():
		t.Fatal("S_pub not ready")
	}
	select {
	case <-srvPriv.Ready():
	case <-ctx.Done():
		t.Fatal("S_priv not ready")
	}

	// The defining property: S_priv is NOT in the public discovery.
	_, err = sharedDC.Entry(ctx, pkPriv)
	require.Error(t, err, "S_priv must not be registered in the public discovery")

	// Let the peer announcement complete over the outbound link.
	time.Sleep(5 * time.Second)

	// Client A — reachable only via S_priv.
	dcA := disc.NewMock(0)
	entryPriv, err := privDC.Entry(ctx, pkPriv)
	require.NoError(t, err)
	require.NoError(t, dcA.PostEntry(ctx, entryPriv))

	pkA, skA := cipher.GenerateKeyPair()
	clientA := dmsg.NewClient(pkA, skA, dcA, &dmsg.Config{MinSessions: 1})
	go clientA.Serve(ctx)
	select {
	case <-clientA.Ready():
	case <-ctx.Done():
		t.Fatal("Client A not ready")
	}

	const port = uint16(200)
	lisA, err := clientA.Listen(port)
	require.NoError(t, err)

	// Client B — on S_pub; must resolve A's entry (delegated server = S_priv).
	dcB := disc.NewMock(0)
	entryPub, err := sharedDC.Entry(ctx, pkPub)
	require.NoError(t, err)
	require.NoError(t, dcB.PostEntry(ctx, entryPub))
	entryA, _ := dcA.Entry(ctx, pkA)
	require.NoError(t, dcB.PostEntry(ctx, entryA))

	pkB, skB := cipher.GenerateKeyPair()
	clientB := dmsg.NewClient(pkB, skB, dcB, &dmsg.Config{MinSessions: 1})
	go clientB.Serve(ctx)
	select {
	case <-clientB.Ready():
	case <-ctx.Done():
		t.Fatal("Client B not ready")
	}

	// B dials A — only possible via the announced reverse-forward path.
	for attempt := 0; attempt < 3; attempt++ {
		streamB, dialErr = clientB.DialStream(ctx, dmsg.Addr{PK: pkA, Port: port})
		if dialErr == nil {
			break
		}
		t.Logf("Reverse dial attempt %d: %v", attempt+1, dialErr)
		time.Sleep(2 * time.Second)
	}

	accA = lisA.AcceptStream
	cleanup = func() {
		if streamB != nil {
			streamB.Close()
		}
		lisA.Close()
		clientA.Close()
		clientB.Close()
		srvPub.Close()
		srvPriv.Close()
		srvWg.Wait()
	}
	return streamB, accA, cleanup, dialErr
}

// TestNonPublicServer_ReverseDial verifies the inbound-peer-announcement
// reverse path: a non-public server (NOT in the public discovery) dials a
// public server and announces itself as a forwardable peer, after which a
// client on the public server can dial a client reachable ONLY through the
// non-public server, with full bidirectional transfer.
func TestNonPublicServer_ReverseDial(t *testing.T) {
	const timeout = 40 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	streamB, accA, cleanup, dialErr := nonPublicReverseSetup(t, ctx, true)
	defer cleanup()

	require.NoError(t, dialErr, "reverse dial must succeed when announcements are accepted")
	require.NotNil(t, streamB)

	// AcceptStream and the io.ReadFull calls below do NOT observe ctx: on a
	// stalled reverse-forward path they block forever, hanging the whole test
	// binary until go test's global watchdog kills the entire package with an
	// undiagnosable "panic: test timed out" (seen intermittently on CI). Enforce
	// this test's OWN 40s ctx on the accept + the data transfer so a stall fails
	// fast, on THIS test, with a clear message — turning a package-wide hang into
	// an isolated, diagnosable failure.
	deadline, hasDeadline := ctx.Deadline()
	require.True(t, hasDeadline)

	type acceptResult struct {
		stream *dmsg.Stream
		err    error
	}
	accCh := make(chan acceptResult, 1)
	go func() {
		s, e := accA()
		accCh <- acceptResult{stream: s, err: e}
	}()
	var connA *dmsg.Stream
	select {
	case r := <-accCh:
		require.NoError(t, r.err)
		connA = r.stream
	case <-ctx.Done():
		t.Fatal("reverse-forward path stalled: A never accepted the reverse-dialed stream within the test timeout")
	}
	defer connA.Close()
	require.NoError(t, streamB.SetDeadline(deadline))
	require.NoError(t, connA.SetDeadline(deadline))

	payload := cipher.RandByte(1024)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); _, wErr := streamB.Write(payload); assert.NoError(t, wErr) }()
	recv := make([]byte, len(payload))
	_, err := io.ReadFull(connA, recv)
	require.NoError(t, err, "B -> A read over reverse-forward path")
	wg.Wait()
	require.True(t, bytes.Equal(payload, recv), "data mismatch: B -> A")

	wg.Add(1)
	go func() { defer wg.Done(); _, wErr := connA.Write(payload); assert.NoError(t, wErr) }()
	recv2 := make([]byte, len(payload))
	_, err = io.ReadFull(streamB, recv2)
	require.NoError(t, err, "A -> B read over reverse-forward path")
	wg.Wait()
	require.True(t, bytes.Equal(payload, recv2), "data mismatch: A -> B")

	t.Log("Non-public-server reverse-dial test passed.")
}

// TestNonPublicServer_ReverseDial_GateOff is the negative control: with
// AcceptPeerAnnouncements disabled on the public server, the same reverse dial
// must FAIL — proving it is the announcement gate (not the static-peer path)
// that makes the non-public server's clients reachable inbound.
func TestNonPublicServer_ReverseDial_GateOff(t *testing.T) {
	const timeout = 40 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	streamB, _, cleanup, dialErr := nonPublicReverseSetup(t, ctx, false)
	defer cleanup()

	require.Error(t, dialErr, "reverse dial must fail when announcements are not accepted")
	require.Nil(t, streamB)
}
