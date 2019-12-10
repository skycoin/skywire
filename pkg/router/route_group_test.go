package router

import (
	"context"
	"io"
	"math/rand"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SkycoinProject/dmsg"
	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/nettest"

	"github.com/SkycoinProject/skywire-mainnet/pkg/routing"
	"github.com/SkycoinProject/skywire-mainnet/pkg/snet/snettest"
	"github.com/SkycoinProject/skywire-mainnet/pkg/transport"
)

func TestNewRouteGroup(t *testing.T) {
	rg := createRouteGroup()
	require.NotNil(t, rg)
}

func TestRouteGroup_Close(t *testing.T) {
	rg := createRouteGroup()
	require.NotNil(t, rg)

	require.False(t, rg.isClosed())
	require.NoError(t, rg.Close())
	require.True(t, rg.isClosed())
}

func TestRouteGroup_Read(t *testing.T) {
	msg1 := []byte("hello1")
	msg2 := []byte("hello2")
	buf1 := make([]byte, len(msg1))
	buf2 := make([]byte, len(msg2))

	rg1 := createRouteGroup()
	rg2 := createRouteGroup()

	_, _, teardown := createTransports(t, rg1, rg2, dmsg.Type)
	defer teardown()

	rg1.readCh <- msg1
	rg2.readCh <- msg2

	n, err := rg1.Read(buf1)
	require.NoError(t, err)
	require.Equal(t, msg1, buf1)
	require.Equal(t, len(msg1), n)

	n, err = rg2.Read(buf2)
	require.NoError(t, err)
	require.Equal(t, msg2, buf2)
	require.Equal(t, len(msg2), n)
}

func TestRouteGroup_Write(t *testing.T) {
	msg1 := []byte("hello1")
	msg2 := []byte("hello2")

	rg1 := createRouteGroup()
	require.NotNil(t, rg1)

	_, err := rg1.Write(msg1)
	require.Equal(t, ErrNoTransports, err)
	require.NoError(t, rg1.Close())

	rg1 = createRouteGroup()
	rg2 := createRouteGroup()

	m1, m2, teardown := createTransports(t, rg1, rg2, dmsg.Type)
	defer teardown()

	_, err = rg1.Write(msg1)
	require.NoError(t, err)

	_, err = rg2.Write(msg2)
	require.NoError(t, err)

	recv, err := m1.ReadPacket()
	require.NoError(t, err)
	require.Equal(t, msg2, recv.Payload())

	recv, err = m2.ReadPacket()
	require.NoError(t, err)
	require.Equal(t, msg1, recv.Payload())
}

func TestRouteGroup_ReadWrite(t *testing.T) {
	const iterations = 3

	for i := 0; i < iterations; i++ {
		testReadWrite(t, iterations)
	}
}

func testReadWrite(t *testing.T, iterations int) {
	rg1 := createRouteGroup()
	rg2 := createRouteGroup()
	m1, m2, teardownEnv := createTransports(t, rg1, rg2, dmsg.Type)

	ctx, cancel := context.WithCancel(context.Background())

	go pushPackets(ctx, t, m1, rg1)

	go pushPackets(ctx, t, m2, rg2)

	testRouteGroupReadWrite(t, iterations, rg1, rg2)

	cancel()

	assert.NoError(t, rg1.Close())
	assert.NoError(t, rg2.Close())

	teardownEnv()
}

func testRouteGroupReadWrite(t *testing.T, iterations int, rg1, rg2 io.ReadWriter) {
	msg1 := []byte("hello1_")
	msg2 := []byte("hello2_")

	t.Run("Group", func(t *testing.T) {
		t.Run("MultipleWriteRead", func(t *testing.T) {
			testMultipleWR(t, iterations, rg1, rg2, msg1, msg2)
		})

		t.Run("SingleReadWrite", func(t *testing.T) {
			testSingleRW(t, rg1, rg2, msg1, msg2)
		})

		t.Run("MultipleReadWrite", func(t *testing.T) {
			testMultipleRW(t, iterations, rg1, rg2, msg1, msg2)
		})

		t.Run("SingleWriteRead", func(t *testing.T) {
			testSingleWR(t, rg1, rg2, msg1, msg2)
		})
	})
}

func testSingleWR(t *testing.T, rg1, rg2 io.ReadWriter, msg1, msg2 []byte) {
	_, err := rg1.Write(msg1)
	require.NoError(t, err)

	_, err = rg2.Write(msg2)
	require.NoError(t, err)

	buf1 := make([]byte, len(msg2))
	_, err = rg1.Read(buf1)
	require.NoError(t, err)
	require.Equal(t, msg2, buf1)

	buf2 := make([]byte, len(msg1))
	_, err = rg2.Read(buf2)
	require.NoError(t, err)
	require.Equal(t, msg1, buf2)
}

func testMultipleRW(t *testing.T, iterations int, rg1, rg2 io.ReadWriter, msg1, msg2 []byte) {
	var err1, err2 error

	for i := 0; i < iterations; i++ {
		var wg sync.WaitGroup

		wg.Add(1)

		go func() {
			defer wg.Done()

			time.Sleep(100 * time.Millisecond)

			for j := 0; j < iterations; j++ {
				_, err := rg1.Write(append(msg1, []byte(strconv.Itoa(j))...))
				require.NoError(t, err)

				_, err = rg2.Write(append(msg2, []byte(strconv.Itoa(j))...))
				require.NoError(t, err)
			}
		}()

		require.NoError(t, err1)
		require.NoError(t, err2)

		for j := 0; j < iterations; j++ {
			msg := append(msg2, []byte(strconv.Itoa(j))...)
			buf1 := make([]byte, len(msg))
			_, err := rg1.Read(buf1)
			require.NoError(t, err)
			require.Equal(t, msg, buf1)
		}

		for j := 0; j < iterations; j++ {
			msg := append(msg1, []byte(strconv.Itoa(j))...)
			buf2 := make([]byte, len(msg))
			_, err := rg2.Read(buf2)
			require.NoError(t, err)
			require.Equal(t, msg, buf2)
		}

		wg.Wait()
	}
}

func testSingleRW(t *testing.T, rg1, rg2 io.ReadWriter, msg1, msg2 []byte) {
	var err1, err2 error

	go func() {
		time.Sleep(1 * time.Second)
		_, err1 = rg1.Write(msg1)
		_, err2 = rg2.Write(msg2)
	}()

	require.NoError(t, err1)
	require.NoError(t, err2)

	buf1 := make([]byte, len(msg2))
	_, err := rg1.Read(buf1)
	require.NoError(t, err)
	require.Equal(t, msg2, buf1)

	buf2 := make([]byte, len(msg1))
	_, err = rg2.Read(buf2)
	require.NoError(t, err)
	require.Equal(t, msg1, buf2)
}

func testMultipleWR(t *testing.T, iterations int, rg1, rg2 io.ReadWriter, msg1, msg2 []byte) {
	for i := 0; i < iterations; i++ {
		for j := 0; j < iterations; j++ {
			_, err := rg1.Write(append(msg1, []byte(strconv.Itoa(j))...))
			require.NoError(t, err)

			_, err = rg2.Write(append(msg2, []byte(strconv.Itoa(j))...))
			require.NoError(t, err)
		}

		for j := 0; j < iterations; j++ {
			msg := append(msg2, []byte(strconv.Itoa(j))...)
			buf1 := make([]byte, len(msg))
			_, err := rg1.Read(buf1)
			require.NoError(t, err)
			require.Equal(t, msg, buf1)
		}

		for j := 0; j < iterations; j++ {
			msg := append(msg1, []byte(strconv.Itoa(j))...)
			buf2 := make([]byte, len(msg))
			_, err := rg2.Read(buf2)
			require.NoError(t, err)
			require.Equal(t, msg, buf2)
		}
	}
}

func TestArbitrarySizeOneMessage(t *testing.T) {
	// Test fails if message size is above 4059
	const (
		value1 = 4058 // dmsg/noise.maxFrameSize - 38
		value2 = 4059 // dmsg/noise.maxFrameSize - 37
	)

	var wg sync.WaitGroup

	wg.Add(1)

	t.Run("Value1", func(t *testing.T) {
		defer wg.Done()
		testArbitrarySizeOneMessage(t, value1)
	})

	wg.Wait()

	t.Run("Value2", func(t *testing.T) {
		testArbitrarySizeOneMessage(t, value2)
	})
}

func TestArbitrarySizeMultipleMessagesByChunks(t *testing.T) {
	// Test fails if message size is above 64810
	const (
		value1 = 64810 // 2^16 - 726
		value2 = 64811 // 2^16 - 725
	)

	var wg sync.WaitGroup

	wg.Add(1)

	t.Run("Value1", func(t *testing.T) {
		defer wg.Done()
		testArbitrarySizeMultipleMessagesByChunks(t, value1)
	})

	wg.Wait()

	t.Run("Value2", func(t *testing.T) {
		testArbitrarySizeMultipleMessagesByChunks(t, value2)
	})
}

func testArbitrarySizeMultipleMessagesByChunks(t *testing.T, size int) {
	rg1 := createRouteGroup()
	rg2 := createRouteGroup()
	m1, m2, teardownEnv := createTransports(t, rg1, rg2, dmsg.Type)

	ctx, cancel := context.WithCancel(context.Background())

	defer func() {
		cancel()
		teardownEnv()
	}()

	go pushPackets(ctx, t, m1, rg1)

	go pushPackets(ctx, t, m2, rg2)

	chunkSize := 1024

	msg := []byte(strings.Repeat("A", size))

	for offset := 0; offset < size; offset += chunkSize {
		_, err := rg1.Write(msg[offset : offset+chunkSize])
		require.NoError(t, err)
	}

	for offset := 0; offset < size; offset += chunkSize {
		buf := make([]byte, chunkSize)
		n, err := rg2.Read(buf)
		require.NoError(t, err)
		require.Equal(t, chunkSize, n)
		require.Equal(t, msg[offset:offset+chunkSize], buf)
	}

	buf := make([]byte, chunkSize)
	n, err := rg2.Read(buf)
	assert.Equal(t, io.EOF, err)
	assert.Equal(t, 0, n)
	assert.Equal(t, make([]byte, chunkSize), buf)
}

func testArbitrarySizeOneMessage(t *testing.T, size int) {
	rg1 := createRouteGroup()
	rg2 := createRouteGroup()
	m1, m2, teardownEnv := createTransports(t, rg1, rg2, dmsg.Type)

	ctx, cancel := context.WithCancel(context.Background())

	defer func() {
		cancel()
		teardownEnv()
	}()

	go pushPackets(ctx, t, m1, rg1)

	go pushPackets(ctx, t, m2, rg2)

	msg := []byte(strings.Repeat("A", size))

	_, err := rg1.Write(msg)
	require.NoError(t, err)

	buf := make([]byte, size)
	n, err := rg2.Read(buf)
	require.NoError(t, err)
	require.Equal(t, size, n)
	require.Equal(t, msg, buf)

	buf = make([]byte, size)
	n, err = rg2.Read(buf)
	require.Equal(t, io.EOF, err)
	require.Equal(t, 0, n)
	require.Equal(t, make([]byte, size), buf)
}

func TestRouteGroup_LocalAddr(t *testing.T) {
	rg := createRouteGroup()
	require.Equal(t, rg.desc.Dst(), rg.LocalAddr())
}

func TestRouteGroup_RemoteAddr(t *testing.T) {
	rg := createRouteGroup()
	require.Equal(t, rg.desc.Src(), rg.RemoteAddr())
}

// TODO: fix hangs
func TestRouteGroup_TestConn(t *testing.T) {
	mp := func() (c1, c2 net.Conn, stop func(), err error) {
		rg1 := createRouteGroup()
		rg2 := createRouteGroup()

		c1, c2 = rg1, rg2

		m1, m2, teardownEnv := createTransports(t, rg1, rg2, dmsg.Type)
		ctx, cancel := context.WithCancel(context.Background())

		go pushPackets(ctx, t, m1, rg1)

		go pushPackets(ctx, t, m2, rg2)

		stop = func() {
			cancel()
			teardownEnv()
		}

		return
	}

	nettest.TestConn(t, mp)
}

func pushPackets(ctx context.Context, t *testing.T, from *transport.Manager, to *RouteGroup) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-to.done:
			return
		default:
			packet, err := from.ReadPacket()
			assert.NoError(t, err)

			if packet.Type() != routing.DataPacket {
				continue
			}

			payload := packet.Payload()
			if len(payload) != int(packet.Size()) {
				panic("malformed packet")
			}

			select {
			case <-ctx.Done():
				return
			case <-to.done:
				return
			case to.readCh <- payload:
			}
		}
	}
}

func createRouteGroup() *RouteGroup {
	rt := routing.NewTable(routing.DefaultConfig())

	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()
	port1 := routing.Port(1)
	port2 := routing.Port(2)
	desc := routing.NewRouteDescriptor(pk1, pk2, port1, port2)

	cfg := DefaultRouteGroupConfig()
	rg := NewRouteGroup(cfg, rt, desc)

	return rg
}

func createTransports(t *testing.T, rg1, rg2 *RouteGroup, network string) (m1, m2 *transport.Manager, teardown func()) {
	tpDisc := transport.NewDiscoveryMock()
	keys := snettest.GenKeyPairs(2)

	nEnv := snettest.NewEnv(t, keys, []string{network})

	m1, m2, tp1, tp2, err := transport.CreateTransportPair(tpDisc, keys, nEnv, network)
	require.NoError(t, err)
	require.NotNil(t, tp1)
	require.NotNil(t, tp2)
	require.NotNil(t, tp1.Entry)
	require.NotNil(t, tp2.Entry)

	keepAlive := 1 * time.Hour
	// TODO: remove rand
	id1 := routing.RouteID(rand.Int()) // nolint: gosec
	id2 := routing.RouteID(rand.Int()) // nolint: gosec
	port1 := routing.Port(1)
	port2 := routing.Port(2)
	rule1 := routing.ForwardRule(keepAlive, id1, id2, tp2.Entry.ID, keys[0].PK, keys[1].PK, port1, port2)
	rule2 := routing.ForwardRule(keepAlive, id2, id1, tp1.Entry.ID, keys[1].PK, keys[0].PK, port2, port1)

	rg1.mu.Lock()
	rg1.tps = append(rg1.tps, tp1)
	rg1.fwd = append(rg1.fwd, rule1)
	rg1.mu.Unlock()

	rg2.mu.Lock()
	rg2.tps = append(rg2.tps, tp2)
	rg2.fwd = append(rg2.fwd, rule2)
	rg2.mu.Unlock()

	return m1, m2, func() {
		nEnv.Teardown()
	}
}
