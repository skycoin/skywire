// Package dmsg pkg/dmsg/stream_test.go
package dmsg

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/nettest"

	"github.com/skycoin/skywire/pkg/disc"
	"github.com/skycoin/skywire/pkg/noise"
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

func GenKeyPair(t *testing.T, seed string) (cipher.PubKey, cipher.SecKey) {
	pk, sk, err := cipher.GenerateDeterministicKeyPair([]byte(seed))
	require.NoError(t, err)
	return pk, sk
}
