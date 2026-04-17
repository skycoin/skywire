// Package dmsg pkg/dmsg/stream_test.go
package dmsg

import (
	"context"
	"fmt"
	"io"
	"net"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/nettest"

	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/noise"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
)

func TestStream(t *testing.T) {
	// Prepare mock discovery.
	dc := disc.NewMock(0)
	const maxSessions = 10

	// Prepare dmsg server.
	pkSrv, skSrv := GenKeyPair(t, "server")
	srvConf := &ServerConfig{
		MaxSessions:    maxSessions,
		UpdateInterval: 0,
	}
	srv := NewServer(pkSrv, skSrv, dc, srvConf, nil)
	srv.SetLogger(logging.MustGetLogger("server"))
	lisSrv, err := net.Listen("tcp", "")
	require.NoError(t, err)

	// Serve dmsg server.
	chSrv := make(chan error, 1)
	go func() { chSrv <- srv.Serve(lisSrv, "") }() //nolint:errcheck

	// Prepare and serve dmsg client A.
	pkA, skA := GenKeyPair(t, "client A")
	clientA := NewClient(pkA, skA, dc, DefaultConfig())
	clientA.SetLogger(logging.MustGetLogger("client_A"))
	go clientA.Serve(context.Background())

	// Prepare and serve dmsg client B.
	pkB, skB := GenKeyPair(t, "client B")
	clientB := NewClient(pkB, skB, dc, DefaultConfig())
	clientB.SetLogger(logging.MustGetLogger("client_B"))
	go clientB.Serve(context.Background())

	// Ensure all entities are registered in discovery before continuing.
	time.Sleep(time.Second * 2)

	// Helper functions.
	makePiper := func(dialer, listener *Client, port uint16) (net.Listener, nettest.MakePipe) {
		lis, err := listener.Listen(port)
		require.NoError(t, err)

		return lis, func() (c1, c2 net.Conn, stop func(), err error) {
			if c1, err = dialer.DialStream(context.TODO(), Addr{PK: listener.LocalPK(), Port: port}); err != nil {
				return
			}
			if c2, err = lis.Accept(); err != nil {
				return
			}
			stop = func() {
				_ = c1.Close() //nolint:errcheck
				_ = c2.Close() //nolint:errcheck
			}
			return
		}
	}

	t.Run("test_large_data_io", func(t *testing.T) {
		const port = 8080
		lis, makePipe := makePiper(clientA, clientB, port)
		connA, connB, stop, errA := makePipe()
		require.NoError(t, errA)

		fmt.Println(connA.LocalAddr(), connA.RemoteAddr())
		fmt.Println(connB.LocalAddr(), connB.RemoteAddr())

		largeData := cipher.RandByte(noise.MaxWriteSize)

		nA, errA := connA.Write(largeData)
		require.NoError(t, errA)
		require.Equal(t, len(largeData), nA)

		readB := make([]byte, len(largeData))
		nB, errB := io.ReadFull(connB, readB)
		require.NoError(t, errB)
		require.Equal(t, len(largeData), nB)
		require.Equal(t, largeData, readB)

		// Closing logic.
		stop()
		require.NoError(t, lis.Close())
	})

	// TODO: The Timeout portions of these tests sometimes fail for currently unknown reasons.
	// TODO: We need to look into whether nettest.TestConn is even suitable for dmsg.Stream.
	// TODO: If so, we need to see how to fix the behavior.
	//t.Run("TestConn", func(t *testing.T) {
	//	const rounds = 3
	//	listeners := make([]net.Listener, 0, rounds*2)
	//
	//	for port := uint16(1); port <= rounds; port++ {
	//		lis1, makePipe1 := makePiper(clientA, clientB, port)
	//		listeners = append(listeners, lis1)
	//		nettest.TestConn(t, makePipe1)
	//
	//		lis2, makePipe2 := makePiper(clientB, clientA, port)
	//		listeners = append(listeners, lis2)
	//		nettest.TestConn(t, makePipe2)
	//	}
	//
	//	// Closing logic.
	//	for _, lis := range listeners {
	//		require.NoError(t, lis.Close())
	//	}
	//})
	//
	//t.Run("TestConn concurrent", func(t *testing.T) {
	//	const rounds = 10
	//	listeners := make([]net.Listener, 0, rounds*2)
	//
	//	wg := new(sync.WaitGroup)
	//	wg.Add(rounds * 2)
	//
	//	for port := uint16(1); port <= rounds; port++ {
	//		lis1, makePipe1 := makePiper(clientA, clientB, port)
	//		listeners = append(listeners, lis1)
	//		go func(makePipe1 nettest.MakePipe) {
	//			nettest.TestConn(t, makePipe1)
	//			wg.Done()
	//		}(makePipe1)
	//
	//		lis2, makePipe2 := makePiper(clientB, clientA, port)
	//		listeners = append(listeners, lis2)
	//		go func(makePipe2 nettest.MakePipe) {
	//			nettest.TestConn(t, makePipe2)
	//			wg.Done()
	//		}(makePipe2)
	//	}
	//
	//	wg.Wait()
	//
	//	// Closing logic.
	//	for _, lis := range listeners {
	//		require.NoError(t, lis.Close())
	//	}
	//})

	t.Run("test_concurrent_dialing", func(t *testing.T) {
		const port = 8080
		const rounds = 10

		aErrs := make([]error, rounds)
		bErrs := make([]error, rounds)

		wg := new(sync.WaitGroup)
		wg.Add(rounds * 2)

		lis, err := clientA.Listen(port)
		require.NoError(t, err)

		for i := 0; i < rounds; i++ {
			go func(i int) {
				connB, err := clientB.Dial(context.TODO(), lis.DmsgAddr())
				if err != nil {
					bErrs[i] = err
				} else {
					_ = connB.Close() //nolint:errcheck
				}
				wg.Done()
			}(i)
			go func(i int) {
				connA, err := lis.Accept()
				if err != nil {
					aErrs[i] = err
				} else {
					_ = connA.Close() //nolint:errcheck
				}
				wg.Done()
			}(i)
		}

		wg.Wait()

		for _, err := range aErrs {
			require.NoError(t, err)
		}
		for _, err := range bErrs {
			require.NoError(t, err)
		}

		// Closing logic.
		require.NoError(t, lis.Close())
	})

	// Closing logic.
	require.NoError(t, clientB.Close())
	require.NoError(t, clientA.Close())
	require.NoError(t, srv.Close())
	require.NoError(t, <-chSrv)
}

func TestLookupIP(t *testing.T) {
	// Prepare mock discovery.
	dc := disc.NewMock(0)
	const maxSessions = 10

	// Prepare dmsg server A.
	pkSrvA, skSrvB := GenKeyPair(t, "server_A")
	srvConf := &ServerConfig{
		MaxSessions:    maxSessions,
		UpdateInterval: 0,
	}
	srv := NewServer(pkSrvA, skSrvB, dc, srvConf, nil)
	srv.SetLogger(logging.MustGetLogger("server"))
	lisSrv, err := net.Listen("tcp", "")
	require.NoError(t, err)

	// Serve dmsg server.
	chSrv := make(chan error, 1)
	go func() { chSrv <- srv.Serve(lisSrv, "") }() //nolint:errcheck

	// Prepare and serve dmsg client A.
	pkA, skA := GenKeyPair(t, "client A")

	clientConfig := &Config{
		MinSessions:    DefaultMinSessions,
		UpdateInterval: DefaultUpdateInterval * 5,
		ClientType:     "test",
	}

	dmsgC := NewClient(pkA, skA, dc, clientConfig)
	go dmsgC.Serve(context.Background())
	t.Cleanup(func() { assert.NoError(t, dmsgC.Close()) })
	<-dmsgC.Ready()

	t.Run("test_connected_server", func(t *testing.T) {
		// Ensure all entities are registered in discovery before continuing.
		time.Sleep(time.Second * 2)

		// Lookup IP.
		srvs := []cipher.PubKey{pkSrvA}
		ip, err := dmsgC.LookupIP(context.Background(), srvs)
		require.NoError(t, err)

		if runtime.GOOS == "windows" {
			require.Equal(t, net.ParseIP("127.0.0.1"), ip)
		} else {
			require.Equal(t, net.ParseIP("::1"), ip)
		}

		// Ensure all entities are deregistered in discovery before continuing.
		time.Sleep(time.Second * 2)
	})

	t.Run("test_disconnected_server", func(t *testing.T) {
		// Prepare dmsg server B.
		pkSrvB, skSrvB := GenKeyPair(t, "server_B")
		srvB := NewServer(pkSrvB, skSrvB, dc, srvConf, nil)
		srvB.SetLogger(logging.MustGetLogger("server_B"))
		lisSrvB, err := net.Listen("tcp", "")
		require.NoError(t, err)

		// Serve dmsg server B.
		chSrvB := make(chan error, 1)
		go func() { chSrvB <- srvB.Serve(lisSrvB, "") }() //nolint:errcheck
		<-srvB.Ready()

		// Explicitly connect the client to server B (the client's
		// background discovery loop won't re-poll for minutes).
		_, err = dmsgC.ConnectToAllServers(context.Background())
		require.NoError(t, err)

		// Verify the client actually has a session to server B.
		require.Eventually(t, func() bool {
			for _, pk := range dmsgC.ConnectedServersPK() {
				if pk == pkSrvB.String() {
					return true
				}
			}
			return false
		}, 10*time.Second, 200*time.Millisecond, "client did not connect to server B")

		srvs := []cipher.PubKey{pkSrvB}
		ip, err := dmsgC.LookupIP(context.Background(), srvs)
		require.NoError(t, err)

		if runtime.GOOS == "windows" {
			require.Equal(t, net.ParseIP("127.0.0.1"), ip)
		} else {
			require.Equal(t, net.ParseIP("::1"), ip)
		}

		// Stop server B to trigger session cleanup on the client side.
		require.NoError(t, srvB.Close())
		require.NoError(t, <-chSrvB)

		// Wait for server B session to be cleaned up from the client.
		require.Eventually(t, func() bool {
			pks := dmsgC.ConnectedServersPK()
			return len(pks) == 1 && pks[0] == pkSrvA.String()
		}, 10*time.Second, 500*time.Millisecond, "server B session was not cleaned up in time")
	})

	// Closing logic.
	require.NoError(t, dmsgC.Close())
	require.NoError(t, srv.Close())
	require.NoError(t, <-chSrv)
}

func GenKeyPair(t *testing.T, seed string) (cipher.PubKey, cipher.SecKey) {
	pk, sk, err := cipher.GenerateDeterministicKeyPair([]byte(seed))
	require.NoError(t, err)
	return pk, sk
}

// TestInvalidPublicKeyNoPanic tests that the server doesn't crash when receiving
// a connection with an invalid public key during the noise handshake.
// This is a regression test for a 2+ year old bug where invalid public keys
// would cause the server to panic and crash.
func TestInvalidPublicKeyNoPanic(t *testing.T) {
	// Prepare mock discovery.
	dc := disc.NewMock(0)
	const maxSessions = 10

	// Prepare dmsg server.
	pkSrv, skSrv := GenKeyPair(t, "server")
	srvConf := &ServerConfig{
		MaxSessions:    maxSessions,
		UpdateInterval: 0,
	}
	srv := NewServer(pkSrv, skSrv, dc, srvConf, nil)
	srv.SetLogger(logging.MustGetLogger("server"))
	lisSrv, err := net.Listen("tcp", "")
	require.NoError(t, err)

	// Serve dmsg server.
	chSrv := make(chan error, 1)
	go func() { chSrv <- srv.Serve(lisSrv, "") }() //nolint:errcheck

	// Give server time to start
	time.Sleep(500 * time.Millisecond)

	// Attempt to send a handshake with invalid public key data
	// This simulates a malicious or buggy client
	t.Run("invalid_pubkey_handshake", func(t *testing.T) {
		conn, err := net.Dial("tcp", lisSrv.Addr().String())
		require.NoError(t, err)
		defer func() { _ = conn.Close() }() //nolint:errcheck

		// Send invalid noise handshake data (contains invalid public key)
		// In a real noise handshake, the public key would be embedded in the message
		// We send malformed data that will trigger invalid public key error
		invalidData := make([]byte, 100)
		// Write some invalid data that looks like a handshake but has invalid key
		copy(invalidData, []byte{0x00, 0x32}) // frame length prefix (50 bytes)
		// Rest is invalid/random data that will fail public key validation
		for i := 2; i < len(invalidData); i++ {
			invalidData[i] = byte(i) // deterministic but invalid
		}

		_, err = conn.Write(invalidData)
		// Write may succeed, but the server should handle the invalid data gracefully
		if err != nil {
			t.Logf("Write failed (expected): %v", err)
		}

		// Give server time to process the invalid handshake
		time.Sleep(500 * time.Millisecond)

		// Read to see if connection was closed (expected behavior)
		buf := make([]byte, 10)
		_ = conn.SetReadDeadline(time.Now().Add(1 * time.Second)) //nolint:errcheck
		_, _ = conn.Read(buf)                                     //nolint:errcheck
		// We expect the connection to be closed or timeout
		// The important thing is the server didn't crash
	})

	// Verify server is still running and can accept valid connections
	t.Run("valid_connection_after_invalid", func(t *testing.T) {
		// Prepare and serve a valid dmsg client
		pkA, skA := GenKeyPair(t, "client A")
		clientA := NewClient(pkA, skA, dc, DefaultConfig())
		clientA.SetLogger(logging.MustGetLogger("client_A"))
		go clientA.Serve(context.Background())

		// Wait for client to register
		time.Sleep(time.Second * 2)

		// Attempt to use the client - if server crashed, this will fail
		lis, err := clientA.Listen(8081)
		require.NoError(t, err, "Server should still be running and accept valid connections")

		// Clean up
		require.NoError(t, lis.Close())
		require.NoError(t, clientA.Close())
	})

	// Closing logic - server should still be healthy
	require.NoError(t, srv.Close())
	require.NoError(t, <-chSrv)
}
