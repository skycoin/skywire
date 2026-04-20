// Package dmsgtest pkg/dmsgtest/e2e_test.go
//
//nolint:errcheck
package dmsgtest

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
)

func TestBidirectionalStreams(t *testing.T) {
	const timeout = time.Second * 30

	payloads := []struct {
		name string
		size int
	}{
		{"small_32B", 32},
		{"medium_4KB", 4 * 1024},
		{"large_64KB", 64 * 1024},
	}

	for _, p := range payloads {
		p := p
		t.Run(p.name, func(t *testing.T) {
			env := NewEnv(t, timeout)
			require.NoError(t, env.Startup(0, 1, 2, &dmsg.Config{MinSessions: 1}))
			t.Cleanup(env.Shutdown)

			clients := env.AllClients()
			require.Len(t, clients, 2)
			clientA := clients[0]
			clientB := clients[1]

			const port = uint16(100)

			lisA, err := clientA.Listen(port)
			require.NoError(t, err)
			t.Cleanup(func() { _ = lisA.Close() })

			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()

			// Client B dials Client A.
			streamB, err := clientB.DialStream(ctx, dmsg.Addr{PK: clientA.LocalPK(), Port: port})
			require.NoError(t, err)
			t.Cleanup(func() { _ = streamB.Close() })

			connA, err := lisA.AcceptStream()
			require.NoError(t, err)
			t.Cleanup(func() { _ = connA.Close() })

			// Test A -> B
			dataAtoB := cipher.RandByte(p.size)
			var wg sync.WaitGroup

			wg.Add(1)
			go func() {
				defer wg.Done()
				_, wErr := connA.Write(dataAtoB)
				assert.NoError(t, wErr)
			}()

			recvAtoB := make([]byte, p.size)
			_, err = io.ReadFull(streamB, recvAtoB)
			require.NoError(t, err)
			wg.Wait()
			require.True(t, bytes.Equal(dataAtoB, recvAtoB), "A->B data mismatch")

			// Test B -> A
			dataBtoA := cipher.RandByte(p.size)

			wg.Add(1)
			go func() {
				defer wg.Done()
				_, wErr := streamB.Write(dataBtoA)
				assert.NoError(t, wErr)
			}()

			recvBtoA := make([]byte, p.size)
			_, err = io.ReadFull(connA, recvBtoA)
			require.NoError(t, err)
			wg.Wait()
			require.True(t, bytes.Equal(dataBtoA, recvBtoA), "B->A data mismatch")
		})
	}
}

func TestMultiServerStreams(t *testing.T) {
	const timeout = time.Second * 30

	env := NewEnv(t, timeout)
	require.NoError(t, env.Startup(0, 2, 3, &dmsg.Config{MinSessions: 2}))
	t.Cleanup(env.Shutdown)

	clients := env.AllClients()
	require.Len(t, clients, 3)
	clientA := clients[0]
	clientB := clients[1]
	clientC := clients[2]

	const (
		portB = uint16(100)
		portC = uint16(101)
	)

	// Client B and C listen.
	lisB, err := clientB.Listen(portB)
	require.NoError(t, err)
	t.Cleanup(func() { _ = lisB.Close() })

	lisC, err := clientC.Listen(portC)
	require.NoError(t, err)
	t.Cleanup(func() { _ = lisC.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Client A dials Client B.
	streamAB, err := clientA.DialStream(ctx, dmsg.Addr{PK: clientB.LocalPK(), Port: portB})
	require.NoError(t, err)
	t.Cleanup(func() { _ = streamAB.Close() })

	connBA, err := lisB.AcceptStream()
	require.NoError(t, err)
	t.Cleanup(func() { _ = connBA.Close() })

	// Client B dials Client C.
	streamBC, err := clientB.DialStream(ctx, dmsg.Addr{PK: clientC.LocalPK(), Port: portC})
	require.NoError(t, err)
	t.Cleanup(func() { _ = streamBC.Close() })

	connCB, err := lisC.AcceptStream()
	require.NoError(t, err)
	t.Cleanup(func() { _ = connCB.Close() })

	// Verify data flows on A->B stream.
	dataAB := cipher.RandByte(512)
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		_, wErr := streamAB.Write(dataAB)
		assert.NoError(t, wErr)
	}()

	recvAB := make([]byte, 512)
	_, err = io.ReadFull(connBA, recvAB)
	require.NoError(t, err)
	wg.Wait()
	require.True(t, bytes.Equal(dataAB, recvAB), "A->B data mismatch")

	// Verify data flows on B->C stream.
	dataBC := cipher.RandByte(512)

	wg.Add(1)
	go func() {
		defer wg.Done()
		_, wErr := streamBC.Write(dataBC)
		assert.NoError(t, wErr)
	}()

	recvBC := make([]byte, 512)
	_, err = io.ReadFull(connCB, recvBC)
	require.NoError(t, err)
	wg.Wait()
	require.True(t, bytes.Equal(dataBC, recvBC), "B->C data mismatch")

	// Verify each client can maintain multiple simultaneous streams.
	// Open a second stream from A to B.
	streamAB2, err := clientA.DialStream(ctx, dmsg.Addr{PK: clientB.LocalPK(), Port: portB})
	require.NoError(t, err)
	t.Cleanup(func() { _ = streamAB2.Close() })

	connBA2, err := lisB.AcceptStream()
	require.NoError(t, err)
	t.Cleanup(func() { _ = connBA2.Close() })

	dataAB2 := cipher.RandByte(256)

	wg.Add(1)
	go func() {
		defer wg.Done()
		_, wErr := streamAB2.Write(dataAB2)
		assert.NoError(t, wErr)
	}()

	recvAB2 := make([]byte, 256)
	_, err = io.ReadFull(connBA2, recvAB2)
	require.NoError(t, err)
	wg.Wait()
	require.True(t, bytes.Equal(dataAB2, recvAB2), "A->B second stream data mismatch")
}

func TestConcurrentStreams(t *testing.T) {
	const timeout = time.Second * 60
	const numStreams = 10
	const payloadSize = 256

	env := NewEnv(t, timeout)
	require.NoError(t, env.Startup(0, 1, 2, &dmsg.Config{MinSessions: 1}))
	t.Cleanup(env.Shutdown)

	clients := env.AllClients()
	require.Len(t, clients, 2)
	clientA := clients[0]
	clientB := clients[1]

	const port = uint16(100)

	lisA, err := clientA.Listen(port)
	require.NoError(t, err)
	t.Cleanup(func() { _ = lisA.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Accept connections in the background, collecting accepted streams.
	type acceptedStream struct {
		stream *dmsg.Stream
		err    error
	}
	acceptCh := make(chan acceptedStream, numStreams)

	go func() {
		for i := 0; i < numStreams; i++ {
			s, aErr := lisA.AcceptStream()
			acceptCh <- acceptedStream{stream: s, err: aErr}
		}
	}()

	// Dial numStreams connections concurrently, each with unique data.
	type streamResult struct {
		idx    int
		sent   []byte
		stream *dmsg.Stream
		err    error
	}

	results := make([]streamResult, numStreams)
	var wg sync.WaitGroup

	for i := 0; i < numStreams; i++ {
		i := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			// Retry dials — on slow CI runners, concurrent handshakes
			// can transiently fail with "cannot connect to delegated server".
			var s *dmsg.Stream
			var dErr error
			for attempt := 0; attempt < 10; attempt++ {
				s, dErr = clientB.DialStream(ctx, dmsg.Addr{PK: clientA.LocalPK(), Port: port})
				if dErr == nil {
					break
				}
				time.Sleep(time.Duration(200+attempt*100) * time.Millisecond)
			}
			if dErr != nil {
				results[i] = streamResult{idx: i, err: dErr}
				return
			}
			results[i] = streamResult{idx: i, stream: s, sent: cipher.RandByte(payloadSize)}
		}()
	}
	wg.Wait()

	// Collect accepted streams.
	accepted := make([]*dmsg.Stream, 0, numStreams)
	for i := 0; i < numStreams; i++ {
		a := <-acceptCh
		require.NoError(t, a.err)
		accepted = append(accepted, a.stream)
	}

	// Send data on each dialed stream and read on the accepted side.
	var sendWg sync.WaitGroup
	for i := 0; i < numStreams; i++ {
		require.NoError(t, results[i].err, "stream %d dial failed", i)
		i := i
		sendWg.Add(1)
		go func() {
			defer sendWg.Done()
			_, wErr := results[i].stream.Write(results[i].sent)
			assert.NoError(t, wErr, "stream %d write failed", i)
		}()
	}

	// Read from each accepted stream. Since we do not know which accepted stream
	// corresponds to which dialed stream, we collect all received data and match.
	recvData := make([][]byte, numStreams)
	var recvWg sync.WaitGroup
	for i := 0; i < numStreams; i++ {
		i := i
		recvWg.Add(1)
		go func() {
			defer recvWg.Done()
			buf := make([]byte, payloadSize)
			_, rErr := io.ReadFull(accepted[i], buf)
			assert.NoError(t, rErr, "stream %d read failed", i)
			recvData[i] = buf
		}()
	}

	sendWg.Wait()
	recvWg.Wait()

	// Verify each sent payload appears exactly once in received data.
	for i := 0; i < numStreams; i++ {
		found := false
		for j := 0; j < numStreams; j++ {
			if bytes.Equal(results[i].sent, recvData[j]) {
				found = true
				break
			}
		}
		require.True(t, found, "stream %d: sent data not found in any received data", i)
	}

	// Cleanup streams.
	for i := 0; i < numStreams; i++ {
		_ = results[i].stream.Close()
		_ = accepted[i].Close()
	}
}

func TestSessionReconnect(t *testing.T) {
	const timeout = time.Second * 30

	env := NewEnv(t, timeout)
	// Start with 2 servers, 0 clients initially so we can track servers.
	require.NoError(t, env.Startup(0, 2, 0, nil))
	t.Cleanup(env.Shutdown)

	servers := env.AllServers()
	require.Len(t, servers, 2)

	// Create client that connects to both servers.
	clientA, err := env.NewClient(&dmsg.Config{MinSessions: 2})
	require.NoError(t, err)

	clientB, err := env.NewClient(&dmsg.Config{MinSessions: 2})
	require.NoError(t, err)

	const port = uint16(100)

	lisB, err := clientB.Listen(port)
	require.NoError(t, err)
	t.Cleanup(func() { _ = lisB.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Verify streams work before closing a server.
	stream1, err := clientA.DialStream(ctx, dmsg.Addr{PK: clientB.LocalPK(), Port: port})
	require.NoError(t, err)

	conn1, err := lisB.AcceptStream()
	require.NoError(t, err)

	data1 := cipher.RandByte(64)
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		_, wErr := stream1.Write(data1)
		assert.NoError(t, wErr)
	}()

	recv1 := make([]byte, 64)
	_, err = io.ReadFull(conn1, recv1)
	require.NoError(t, err)
	wg.Wait()
	require.True(t, bytes.Equal(data1, recv1))

	_ = stream1.Close()
	_ = conn1.Close()

	// Close one server.
	err = servers[0].Close()
	require.NoError(t, err)

	// Wait for both clients to detect the dead session and reconnect
	// via the remaining server. On slow CI runners the 2-second sleep
	// was insufficient; polling for actual readiness is robust.
	var stream2 *dmsg.Stream
	require.Eventually(t, func() bool {
		var dialErr error
		stream2, dialErr = clientA.DialStream(ctx, dmsg.Addr{PK: clientB.LocalPK(), Port: port})
		return dialErr == nil
	}, 15*time.Second, 500*time.Millisecond, "failed to dial after server closure")
	t.Cleanup(func() { _ = stream2.Close() })

	conn2, err := lisB.AcceptStream()
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn2.Close() })

	data2 := cipher.RandByte(64)

	wg.Add(1)
	go func() {
		defer wg.Done()
		_, wErr := stream2.Write(data2)
		assert.NoError(t, wErr)
	}()

	recv2 := make([]byte, 64)
	_, err = io.ReadFull(conn2, recv2)
	require.NoError(t, err)
	wg.Wait()
	require.True(t, bytes.Equal(data2, recv2), "data mismatch after server closure")
}

func TestListenerAcceptAll(t *testing.T) {
	const timeout = time.Second * 30

	env := NewEnv(t, timeout)
	require.NoError(t, env.Startup(0, 1, 2, &dmsg.Config{MinSessions: 1}))
	t.Cleanup(env.Shutdown)

	clients := env.AllClients()
	require.Len(t, clients, 2)
	clientA := clients[0]
	clientB := clients[1]

	const port = uint16(100)
	const numConns = 10

	lisA, err := clientA.Listen(port)
	require.NoError(t, err)
	t.Cleanup(func() { _ = lisA.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Dial numConns connections and accept them all.
	dialedStreams := make([]*dmsg.Stream, 0, numConns)
	for i := 0; i < numConns; i++ {
		s, dErr := clientB.DialStream(ctx, dmsg.Addr{PK: clientA.LocalPK(), Port: port})
		require.NoError(t, dErr, "dial %d should succeed", i)
		dialedStreams = append(dialedStreams, s)
	}

	// Accept all connections.
	for i := 0; i < numConns; i++ {
		conn, aErr := lisA.AcceptStream()
		require.NoError(t, aErr, "accept %d should succeed", i)
		require.NotNil(t, conn)
		_ = conn.Close()
	}

	// Cleanup dialed streams.
	for _, s := range dialedStreams {
		_ = s.Close()
	}
}

func TestPortOccupied(t *testing.T) {
	const timeout = time.Second * 30

	env := NewEnv(t, timeout)
	require.NoError(t, env.Startup(0, 1, 1, &dmsg.Config{MinSessions: 1}))
	t.Cleanup(env.Shutdown)

	clients := env.AllClients()
	require.Len(t, clients, 1)
	client := clients[0]

	const port = uint16(100)

	lis1, err := client.Listen(port)
	require.NoError(t, err)
	t.Cleanup(func() { _ = lis1.Close() })

	// Second listen on same port should return ErrPortOccupied.
	_, err = client.Listen(port)
	require.Error(t, err)
	require.ErrorIs(t, err, dmsg.ErrPortOccupied)
}

func TestDialNonexistentClient(t *testing.T) {
	const timeout = time.Second * 30

	env := NewEnv(t, timeout)
	require.NoError(t, env.Startup(0, 1, 1, &dmsg.Config{MinSessions: 1}))
	t.Cleanup(env.Shutdown)

	clients := env.AllClients()
	require.Len(t, clients, 1)
	client := clients[0]

	// Generate a random PK that does not exist in discovery.
	randomPK, _ := cipher.GenerateKeyPair()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()

	_, err := client.DialStream(ctx, dmsg.Addr{PK: randomPK, Port: 100})
	require.Error(t, err)
	require.ErrorIs(t, err, dmsg.ErrDiscEntryNotFound,
		fmt.Sprintf("expected ErrDiscEntryNotFound, got: %v", err))
}
