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

	"github.com/skycoin/skywire/pkg/dmsg/disc"
	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
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
