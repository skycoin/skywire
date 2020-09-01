package router

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/skycoin/dmsg/cipher"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/snet/directtp/tptypes"
	"github.com/skycoin/skywire/pkg/snet/snettest"
	"github.com/skycoin/skywire/pkg/transport"
)

func TestNewRouteGroup(t *testing.T) {
	rg := createRouteGroup(DefaultRouteGroupConfig())
	require.NotNil(t, rg)
	require.Equal(t, DefaultRouteGroupConfig(), rg.cfg)
}

// Uncomment for debugging
/*
func TestRouteGroupAlignment(t *testing.T) {
	alignment.PrintStruct(RouteGroup{})
}
*/

func TestRouteGroup_Close(t *testing.T) {
	rg1, rg2, m1, m2, teardown := setupEnv(t)

	ctx, cancel := context.WithCancel(context.Background())

	// push close packet from transport to route group
	go pushPackets(ctx, m2, rg2)
	go pushPackets(ctx, m1, rg1)

	err := rg1.Close()
	require.NoError(t, err)
	require.True(t, rg1.isClosed())
	require.True(t, rg2.isRemoteClosed())
	// rg1 should be done (not getting any new data, returning `io.EOF` on further reads)
	// but not closed
	require.False(t, rg2.isClosed())

	err = rg1.Close()
	require.Equal(t, io.ErrClosedPipe, err)

	err = rg2.Close()
	require.NoError(t, err)
	require.True(t, rg2.isClosed())

	err = rg2.Close()
	require.Equal(t, io.ErrClosedPipe, err)

	cancel()
	teardown()
}

func TestRouteGroup_Read(t *testing.T) {
	rg1, rg2, m1, m2, teardown := setupEnv(t)

	ctx, cancel := context.WithCancel(context.Background())

	// push close packet from transport to route group
	go pushPackets(ctx, m2, rg2)
	go pushPackets(ctx, m1, rg1)

	msg1 := []byte("hello1")
	msg2 := []byte("hello2")
	msg3 := []byte("hello3")
	buf1 := make([]byte, len(msg1))
	buf2 := make([]byte, len(msg2))
	buf3 := make([]byte, len(msg2)/2)
	buf4 := make([]byte, len(msg2)/2)

	rg1.readCh <- msg1
	rg2.readCh <- msg2
	rg2.readCh <- msg3

	n, err := rg1.Read([]byte{})
	require.Equal(t, 0, n)
	require.NoError(t, err)

	n, err = rg1.Read(buf1)
	require.NoError(t, err)
	require.Equal(t, msg1, buf1)
	require.Equal(t, len(msg1), n)

	n, err = rg2.Read(buf2)
	require.NoError(t, err)
	require.Equal(t, msg2, buf2)
	require.Equal(t, len(msg2), n)

	// Test short reads.
	n, err = rg2.Read(buf3)
	require.NoError(t, err)
	require.Equal(t, msg3[0:len(msg3)/2], buf3)
	require.Equal(t, len(msg3)/2, n)

	n, err = rg2.Read(buf4)
	require.NoError(t, err)
	require.Equal(t, msg3[len(msg3)/2:], buf4)
	require.Equal(t, len(msg3)/2, n)

	require.NoError(t, rg1.Close())
	require.NoError(t, rg2.Close())
	cancel()
	teardown()
}

func TestRouteGroup_Write(t *testing.T) {
	rg1, rg2, m1, m2, teardown := setupEnv(t)

	testWrite(t, rg1, rg2, m1, m2)

	require.NoError(t, rg1.Close())
	require.NoError(t, rg2.Close())
	teardown()
}

func testWrite(t *testing.T, rg1, rg2 *RouteGroup, m1, m2 *transport.Manager) {
	msg1 := []byte("hello1")
	msg2 := []byte("hello2")

	n, err := rg1.Write([]byte{})
	require.Equal(t, 0, n)
	require.NoError(t, err)

	n, err = rg2.Write([]byte{})
	require.Equal(t, 0, n)
	require.NoError(t, err)

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

	rg1.mu.Lock()
	tpBackup := rg1.tps[0]
	rg1.tps[0] = nil
	rg1.mu.Unlock()
	_, err = rg1.Write(msg1)
	require.Equal(t, ErrBadTransport, err)

	rg1.mu.Lock()
	rg1.tps[0] = tpBackup

	tpsBackup := rg1.tps
	rg1.tps = nil
	rg1.mu.Unlock()
	_, err = rg1.Write(msg1)
	require.Equal(t, ErrNoTransports, err)

	rg1.mu.Lock()
	rg1.tps = tpsBackup

	fwdBackup := rg1.fwd
	rg1.fwd = nil
	rg1.mu.Unlock()
	_, err = rg1.Write(msg1)
	require.Equal(t, ErrNoRules, err)

	rg1.mu.Lock()
	rg1.fwd = fwdBackup
	rg1.mu.Unlock()
}

func TestRouteGroup_ReadWrite(t *testing.T) {
	const iterations = 3

	for i := 0; i < iterations; i++ {
		testReadWrite(t, iterations)
	}
}

func testReadWrite(t *testing.T, iterations int) {
	rg1, rg2, m1, m2, teardown := setupEnv(t)

	ctx, cancel := context.WithCancel(context.Background())

	// push close packet from transport to route group
	go pushPackets(ctx, m2, rg2)
	go pushPackets(ctx, m1, rg1)

	testRouteGroupReadWrite(t, iterations, rg1, rg2)

	assert.NoError(t, rg1.Close())
	assert.NoError(t, rg2.Close())
	cancel()
	teardown()
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
	rg1, rg2, m1, m2, teardown := setupEnv(t)

	ctx, cancel := context.WithCancel(context.Background())

	// push close packet from transport to route group
	go pushPackets(ctx, m2, rg2)
	go pushPackets(ctx, m1, rg1)

	defer func() {
		cancel()
		teardown()
	}()

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

	var (
		errCh = make(chan error)
		nCh   = make(chan int)
		bufCh = make(chan []byte)
	)
	go func() {
		buf := make([]byte, size)
		n, err := rg2.Read(buf)
		errCh <- err
		nCh <- n
		bufCh <- buf
	}()

	// close remote to simulate `io.EOF` on local connection
	require.NoError(t, rg1.Close())

	err := <-errCh
	n := <-nCh
	readBuf := <-bufCh
	close(nCh)
	close(errCh)
	close(bufCh)
	require.Equal(t, io.EOF, err)
	require.Equal(t, 0, n)
	require.Equal(t, make([]byte, size), readBuf)

	require.NoError(t, rg2.Close())
}

func testArbitrarySizeOneMessage(t *testing.T, size int) {
	rg1, rg2, m1, m2, teardown := setupEnv(t)

	ctx, cancel := context.WithCancel(context.Background())

	// push close packet from transport to route group
	go pushPackets(ctx, m2, rg2)
	go pushPackets(ctx, m1, rg1)

	defer func() {
		cancel()
		teardown()
	}()

	msg := []byte(strings.Repeat("A", size))

	_, err := rg1.Write(msg)
	require.NoError(t, err)

	buf := make([]byte, size)
	n, err := rg2.Read(buf)
	require.NoError(t, err)
	require.Equal(t, size, n)
	require.Equal(t, msg, buf)

	var (
		errCh = make(chan error)
		nCh   = make(chan int)
		bufCh = make(chan []byte)
	)
	go func() {
		buf := make([]byte, size)
		n, err := rg2.Read(buf)
		errCh <- err
		nCh <- n
		bufCh <- buf
	}()

	// close remote to simulate `io.EOF` on local connection
	require.NoError(t, rg1.Close())

	err = <-errCh
	n = <-nCh
	readBuf := <-bufCh
	close(nCh)
	close(errCh)
	close(bufCh)
	require.Equal(t, io.EOF, err)
	require.Equal(t, 0, n)
	require.Equal(t, make([]byte, size), readBuf)

	require.NoError(t, rg2.Close())
}

func TestRouteGroup_LocalAddr(t *testing.T) {
	rg := createRouteGroup(DefaultRouteGroupConfig())
	require.Equal(t, rg.desc.Dst(), rg.LocalAddr())

	require.NoError(t, rg.Close())
}

func TestRouteGroup_RemoteAddr(t *testing.T) {
	rg := createRouteGroup(DefaultRouteGroupConfig())
	require.Equal(t, rg.desc.Src(), rg.RemoteAddr())

	require.NoError(t, rg.Close())
}

// TODO(darkrengarius): Uncomment and fix.
/*
func TestRouteGroup_TestConn(t *testing.T) {
	mp := func() (c1, c2 net.Conn, stop func(), err error) {
		rg1, rg2, m1, m2, teardown := setupEnv(t)

		ctx, cancel := context.WithCancel(context.Background())

		// push close packet from transport to route group
		go pushPackets(ctx, m2, rg2)
		go pushPackets(ctx, m1, rg1)

		stop = func() {
			_ = rg1.Close() // nolint:errcheck
			_ = rg2.Close() // nolint:errcheck
			cancel()
			teardown()
		}

		return rg1, rg2, stop, nil
	}

	nettest.TestConn(t, mp)
}
*/

func pushPackets(ctx context.Context, from *transport.Manager, to *RouteGroup) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		packet, err := from.ReadPacket()
		if err != nil {
			panic(err)
		}

		payload := packet.Payload()
		if len(payload) != int(packet.Size()) {
			panic("malformed packet")
		}

		switch packet.Type() {
		case routing.ClosePacket:
			if to.isClosed() {
				panic(io.ErrClosedPipe)
			}

			if err := to.handleClosePacket(routing.CloseCode(packet.Payload()[0])); err != nil {
				panic(err)
			}

			return
		case routing.DataPacket:
			if !safeSend(ctx, to, payload) {
				return
			}
		default:
			panic(fmt.Sprintf("wrong packet type %v", packet.Type()))
		}
	}
}

func safeSend(ctx context.Context, to *RouteGroup, payload []byte) (keepSending bool) {
	defer func() {
		if r := recover(); r != nil {
			keepSending = r == "send on closed channel"
		}
	}()

	to.readChMu.Lock()
	defer to.readChMu.Unlock()

	select {
	case <-ctx.Done():
		return false
	case <-to.closed:
		return false
	case to.readCh <- payload:
		return true
	}
}

func createRouteGroup(cfg *RouteGroupConfig) *RouteGroup {
	rt := routing.NewTable()

	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()
	port1 := routing.Port(1)
	port2 := routing.Port(2)
	desc := routing.NewRouteDescriptor(pk1, pk2, port1, port2)

	rg := NewRouteGroup(cfg, rt, desc)

	return rg
}

func setupEnv(t *testing.T) (rg1, rg2 *RouteGroup, m1, m2 *transport.Manager, teardown func()) {
	keys := snettest.GenKeyPairs(2)

	pk1 := keys[0].PK
	pk2 := keys[1].PK

	// create test env
	nEnv := snettest.NewEnv(t, keys, []string{tptypes.STCP})

	tpDisc := transport.NewDiscoveryMock()
	tpKeys := snettest.GenKeyPairs(2)

	m1, m2, tp1, tp2, err := transport.CreateTransportPair(tpDisc, tpKeys, nEnv, tptypes.STCP)
	require.NoError(t, err)
	require.NotNil(t, tp1)
	require.NotNil(t, tp2)
	require.NotNil(t, tp1.Entry)
	require.NotNil(t, tp2.Entry)

	// because some subtests of `TestConn` are highly specific in their behavior,
	// it's best to exceed the `readCh` size
	rgCfg := &RouteGroupConfig{
		ReadChBufSize:     defaultReadChBufSize * 3,
		KeepAliveInterval: defaultRouteGroupKeepAliveInterval,
	}

	rg1 = createRouteGroup(rgCfg)
	rg2 = createRouteGroup(rgCfg)

	r1RtIDs, err := rg1.rt.ReserveKeys(1)
	require.NoError(t, err)

	r2RtIDs, err := rg2.rt.ReserveKeys(1)
	require.NoError(t, err)

	r1FwdRule := routing.ForwardRule(ruleKeepAlive, r1RtIDs[0], r2RtIDs[0], tp1.Entry.ID, pk2, pk1, 0, 0)
	err = rg1.rt.SaveRule(r1FwdRule)
	require.NoError(t, err)

	r2FwdRule := routing.ForwardRule(ruleKeepAlive, r2RtIDs[0], r1RtIDs[0], tp2.Entry.ID, pk1, pk2, 0, 0)
	err = rg2.rt.SaveRule(r2FwdRule)
	require.NoError(t, err)

	r1FwdRtDesc := r1FwdRule.RouteDescriptor()
	rg1.mu.Lock()
	rg1.desc = r1FwdRtDesc.Invert()
	rg1.tps = append(rg1.tps, tp1)
	rg1.fwd = append(rg1.fwd, r1FwdRule)
	rg1.mu.Unlock()

	r2FwdRtDesc := r2FwdRule.RouteDescriptor()
	rg2.mu.Lock()
	rg2.desc = r2FwdRtDesc.Invert()
	rg2.tps = append(rg2.tps, tp2)
	rg2.fwd = append(rg2.fwd, r2FwdRule)
	rg2.mu.Unlock()

	teardown = func() {
		nEnv.Teardown()
	}

	return rg1, rg2, m1, m2, teardown
}
