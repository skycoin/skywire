package router

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/nettest"

	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/SkycoinProject/skywire-mainnet/pkg/routing"
	"github.com/SkycoinProject/skywire-mainnet/pkg/snet/snettest"
	"github.com/SkycoinProject/skywire-mainnet/pkg/snet/stcp"
	"github.com/SkycoinProject/skywire-mainnet/pkg/transport"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRouteGroup(t *testing.T) {
	rg := createRouteGroup(DefaultRouteGroupConfig())
	require.NotNil(t, rg)
	require.Equal(t, DefaultRouteGroupConfig(), rg.cfg)
}

func TestRouteGroup_Close(t *testing.T) {
	keys := snettest.GenKeyPairs(2)

	pk1 := keys[0].PK
	pk2 := keys[1].PK

	// create test env
	nEnv := snettest.NewEnv(t, keys, []string{stcp.Type})
	defer nEnv.Teardown()

	tpDisc := transport.NewDiscoveryMock()
	tpKeys := snettest.GenKeyPairs(2)

	m1, m2, tp1, tp2, err := transport.CreateTransportPair(tpDisc, tpKeys, nEnv, stcp.Type)
	require.NoError(t, err)
	require.NotNil(t, tp1)
	require.NotNil(t, tp2)
	require.NotNil(t, tp1.Entry)
	require.NotNil(t, tp2.Entry)

	rg0 := createRouteGroup(DefaultRouteGroupConfig())
	rg1 := createRouteGroup(DefaultRouteGroupConfig())

	// reserve FWD and CNSM IDs for r0.
	r0RtIDs, err := rg0.rt.ReserveKeys(2)
	require.NoError(t, err)

	// reserve FWD and CNSM IDs for r1.
	r1RtIDs, err := rg1.rt.ReserveKeys(2)
	require.NoError(t, err)

	r0FwdRule := routing.ForwardRule(ruleKeepAlive, r0RtIDs[0], r1RtIDs[1], tp1.Entry.ID, pk2, pk1, 0, 0)
	r0CnsmRule := routing.ConsumeRule(ruleKeepAlive, r0RtIDs[1], pk1, pk2, 0, 0)

	err = rg0.rt.SaveRule(r0FwdRule)
	require.NoError(t, err)
	err = rg0.rt.SaveRule(r0CnsmRule)
	require.NoError(t, err)

	r1FwdRule := routing.ForwardRule(ruleKeepAlive, r1RtIDs[0], r0RtIDs[1], tp2.Entry.ID, pk1, pk2, 0, 0)
	r1CnsmRule := routing.ConsumeRule(ruleKeepAlive, r1RtIDs[1], pk2, pk1, 0, 0)

	err = rg1.rt.SaveRule(r1FwdRule)
	require.NoError(t, err)
	err = rg1.rt.SaveRule(r1CnsmRule)
	require.NoError(t, err)

	r0FwdRtDesc := r0FwdRule.RouteDescriptor()
	rg0.desc = r0FwdRtDesc.Invert()
	rg0.tps = append(rg0.tps, tp1)
	rg0.fwd = append(rg0.fwd, r0FwdRule)

	r1FwdRtDesc := r1FwdRule.RouteDescriptor()
	rg1.desc = r1FwdRtDesc.Invert()
	rg1.tps = append(rg1.tps, tp2)
	rg1.fwd = append(rg1.fwd, r1FwdRule)

	// push close packet from transport to route group
	go func() {
		packet, err := m1.ReadPacket()
		if err != nil {
			panic(err)
		}

		if packet.Type() != routing.ClosePacket {
			panic("wrong packet type")
		}

		if err := rg0.handleClosePacket(routing.CloseCode(packet.Payload()[0])); err != nil {
			panic(err)
		}
	}()

	// push close packet from transport to route group
	go func() {
		packet, err := m2.ReadPacket()
		if err != nil {
			panic(err)
		}

		if packet.Type() != routing.ClosePacket {
			panic("wrong packet type")
		}

		if err := rg1.handleClosePacket(routing.CloseCode(packet.Payload()[0])); err != nil {
			panic(err)
		}
	}()

	err = rg0.Close()
	require.NoError(t, err)
	require.True(t, rg0.isClosed())
	require.True(t, rg1.isRemoteClosed())
	// rg1 should be done (not getting any new data, returning `io.EOF` on further reads)
	// but not closed
	require.False(t, rg1.isClosed())

	err = rg0.Close()
	require.Equal(t, io.ErrClosedPipe, err)

	err = rg1.Close()
	require.NoError(t, err)
	require.True(t, rg1.isClosed())

	err = rg1.Close()
	require.Equal(t, io.ErrClosedPipe, err)
}

func TestRouteGroup_Read(t *testing.T) {
	msg1 := []byte("hello1")
	msg2 := []byte("hello2")
	msg3 := []byte("hello3")
	buf1 := make([]byte, len(msg1))
	buf2 := make([]byte, len(msg2))
	buf3 := make([]byte, len(msg2)/2)
	buf4 := make([]byte, len(msg2)/2)

	rg1 := createRouteGroup(DefaultRouteGroupConfig())
	rg2 := createRouteGroup(DefaultRouteGroupConfig())

	_, _, teardown := createTransports(t, rg1, rg2, stcp.Type)
	defer teardown()

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
}

func TestRouteGroup_Write(t *testing.T) {
	msg1 := []byte("hello1")

	rg1 := createRouteGroup(DefaultRouteGroupConfig())
	require.NotNil(t, rg1)

	_, err := rg1.Write(msg1)
	require.Equal(t, ErrNoTransports, err)
	require.NoError(t, rg1.Close())

	rg1 = createRouteGroup(DefaultRouteGroupConfig())
	rg2 := createRouteGroup(DefaultRouteGroupConfig())

	m1, m2, teardown := createTransports(t, rg1, rg2, stcp.Type)
	defer teardown()

	testWrite(t, rg1, rg2, m1, m2)

	require.NoError(t, rg1.Close())
	require.NoError(t, rg2.Close())
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

	tpBackup := rg1.tps[0]
	rg1.tps[0] = nil
	_, err = rg1.Write(msg1)
	require.Equal(t, ErrBadTransport, err)

	rg1.tps[0] = tpBackup

	tpsBackup := rg1.tps
	rg1.tps = nil
	_, err = rg1.Write(msg1)
	require.Equal(t, ErrNoTransports, err)

	rg1.tps = tpsBackup

	fwdBackup := rg1.fwd
	rg1.fwd = nil
	_, err = rg1.Write(msg1)
	require.Equal(t, ErrNoRules, err)

	rg1.fwd = fwdBackup
}

func TestRouteGroup_ReadWrite(t *testing.T) {
	const iterations = 3

	for i := 0; i < iterations; i++ {
		testReadWrite(t, iterations)
	}
}

func testReadWrite(t *testing.T, iterations int) {
	rg1 := createRouteGroup(DefaultRouteGroupConfig())
	rg2 := createRouteGroup(DefaultRouteGroupConfig())
	m1, m2, teardownEnv := createTransports(t, rg1, rg2, stcp.Type)

	ctx, cancel := context.WithCancel(context.Background())

	go pushPackets(ctx, m1, rg1)

	go pushPackets(ctx, m2, rg2)

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
	keys := snettest.GenKeyPairs(2)

	pk1 := keys[0].PK
	pk2 := keys[1].PK

	// create test env
	nEnv := snettest.NewEnv(t, keys, []string{stcp.Type})

	tpDisc := transport.NewDiscoveryMock()
	tpKeys := snettest.GenKeyPairs(2)

	m1, m2, tp1, tp2, err := transport.CreateTransportPair(tpDisc, tpKeys, nEnv, stcp.Type)
	require.NoError(t, err)
	require.NotNil(t, tp1)
	require.NotNil(t, tp2)
	require.NotNil(t, tp1.Entry)
	require.NotNil(t, tp2.Entry)

	rg0 := createRouteGroup(DefaultRouteGroupConfig())
	rg1 := createRouteGroup(DefaultRouteGroupConfig())

	r0RtIDs, err := rg0.rt.ReserveKeys(1)
	require.NoError(t, err)

	r1RtIDs, err := rg1.rt.ReserveKeys(1)
	require.NoError(t, err)

	r0FwdRule := routing.ForwardRule(ruleKeepAlive, r0RtIDs[0], r1RtIDs[0], tp1.Entry.ID, pk2, pk1, 0, 0)
	err = rg0.rt.SaveRule(r0FwdRule)
	require.NoError(t, err)

	r1FwdRule := routing.ForwardRule(ruleKeepAlive, r1RtIDs[0], r0RtIDs[0], tp2.Entry.ID, pk1, pk2, 0, 0)
	err = rg1.rt.SaveRule(r1FwdRule)
	require.NoError(t, err)

	r0FwdRtDesc := r0FwdRule.RouteDescriptor()
	rg0.desc = r0FwdRtDesc.Invert()
	rg0.tps = append(rg0.tps, tp1)
	rg0.fwd = append(rg0.fwd, r0FwdRule)

	r1FwdRtDesc := r1FwdRule.RouteDescriptor()
	rg1.desc = r1FwdRtDesc.Invert()
	rg1.tps = append(rg1.tps, tp2)
	rg1.fwd = append(rg1.fwd, r1FwdRule)

	ctx, cancel := context.WithCancel(context.Background())

	// push close packet from transport to route group
	go pushPackets(ctx, m2, rg1)
	go pushPackets(ctx, m1, rg0)

	defer func() {
		cancel()
		nEnv.Teardown()
	}()

	chunkSize := 1024

	msg := []byte(strings.Repeat("A", size))

	for offset := 0; offset < size; offset += chunkSize {
		_, err := rg0.Write(msg[offset : offset+chunkSize])
		require.NoError(t, err)
	}

	for offset := 0; offset < size; offset += chunkSize {
		buf := make([]byte, chunkSize)
		n, err := rg1.Read(buf)
		require.NoError(t, err)
		require.Equal(t, chunkSize, n)
		require.Equal(t, msg[offset:offset+chunkSize], buf)
	}

	errCh := make(chan error)
	nCh := make(chan int)
	bufCh := make(chan []byte)
	go func() {
		buf := make([]byte, size)
		n, err := rg1.Read(buf)
		errCh <- err
		nCh <- n
		bufCh <- buf
	}()

	// close remote to simulate `io.EOF` on local connection
	require.NoError(t, rg0.Close())

	err = <-errCh
	n := <-nCh
	readBuf := <-bufCh
	close(nCh)
	close(errCh)
	close(bufCh)
	require.Equal(t, io.EOF, err)
	require.Equal(t, 0, n)
	require.Equal(t, make([]byte, size), readBuf)

	require.NoError(t, rg1.Close())
}

func testArbitrarySizeOneMessage(t *testing.T, size int) {
	keys := snettest.GenKeyPairs(2)

	pk1 := keys[0].PK
	pk2 := keys[1].PK

	// create test env
	nEnv := snettest.NewEnv(t, keys, []string{stcp.Type})

	tpDisc := transport.NewDiscoveryMock()
	tpKeys := snettest.GenKeyPairs(2)

	m1, m2, tp1, tp2, err := transport.CreateTransportPair(tpDisc, tpKeys, nEnv, stcp.Type)
	require.NoError(t, err)
	require.NotNil(t, tp1)
	require.NotNil(t, tp2)
	require.NotNil(t, tp1.Entry)
	require.NotNil(t, tp2.Entry)

	rg0 := createRouteGroup(DefaultRouteGroupConfig())
	rg1 := createRouteGroup(DefaultRouteGroupConfig())

	r0RtIDs, err := rg0.rt.ReserveKeys(1)
	require.NoError(t, err)

	r1RtIDs, err := rg1.rt.ReserveKeys(1)
	require.NoError(t, err)

	r0FwdRule := routing.ForwardRule(ruleKeepAlive, r0RtIDs[0], r1RtIDs[0], tp1.Entry.ID, pk2, pk1, 0, 0)
	err = rg0.rt.SaveRule(r0FwdRule)
	require.NoError(t, err)

	r1FwdRule := routing.ForwardRule(ruleKeepAlive, r1RtIDs[0], r0RtIDs[0], tp2.Entry.ID, pk1, pk2, 0, 0)
	err = rg1.rt.SaveRule(r1FwdRule)
	require.NoError(t, err)

	r0FwdRtDesc := r0FwdRule.RouteDescriptor()
	rg0.desc = r0FwdRtDesc.Invert()
	rg0.tps = append(rg0.tps, tp1)
	rg0.fwd = append(rg0.fwd, r0FwdRule)

	r1FwdRtDesc := r1FwdRule.RouteDescriptor()
	rg1.desc = r1FwdRtDesc.Invert()
	rg1.tps = append(rg1.tps, tp2)
	rg1.fwd = append(rg1.fwd, r1FwdRule)

	ctx, cancel := context.WithCancel(context.Background())

	// push close packet from transport to route group
	go pushPackets(ctx, m2, rg1)
	go pushPackets(ctx, m1, rg0)

	defer func() {
		cancel()
		nEnv.Teardown()
	}()

	msg := []byte(strings.Repeat("A", size))

	_, err = rg0.Write(msg)
	require.NoError(t, err)

	buf := make([]byte, size)
	n, err := rg1.Read(buf)
	require.NoError(t, err)
	require.Equal(t, size, n)
	require.Equal(t, msg, buf)

	errCh := make(chan error)
	nCh := make(chan int)
	bufCh := make(chan []byte)
	go func() {
		buf := make([]byte, size)
		n, err := rg1.Read(buf)
		errCh <- err
		nCh <- n
		bufCh <- buf
	}()

	// close remote to simulate `io.EOF` on local connection
	require.NoError(t, rg0.Close())

	err = <-errCh
	n = <-nCh
	readBuf := <-bufCh
	close(nCh)
	close(errCh)
	close(bufCh)
	require.Equal(t, io.EOF, err)
	require.Equal(t, 0, n)
	require.Equal(t, make([]byte, size), readBuf)

	require.NoError(t, rg1.Close())
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

// TODO (Darkren): uncomment and fix
func TestRouteGroup_TestConn(t *testing.T) {
	mp := func() (c1, c2 net.Conn, stop func(), err error) {
		keys := snettest.GenKeyPairs(2)

		pk1 := keys[0].PK
		pk2 := keys[1].PK

		// create test env
		nEnv := snettest.NewEnv(t, keys, []string{stcp.Type})

		tpDisc := transport.NewDiscoveryMock()
		tpKeys := snettest.GenKeyPairs(2)

		m1, m2, tp1, tp2, err := transport.CreateTransportPair(tpDisc, tpKeys, nEnv, stcp.Type)
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

		rg0 := createRouteGroup(rgCfg)
		rg1 := createRouteGroup(rgCfg)

		r0RtIDs, err := rg0.rt.ReserveKeys(1)
		require.NoError(t, err)

		r1RtIDs, err := rg1.rt.ReserveKeys(1)
		require.NoError(t, err)

		r0FwdRule := routing.ForwardRule(ruleKeepAlive, r0RtIDs[0], r1RtIDs[0], tp1.Entry.ID, pk2, pk1, 0, 0)
		err = rg0.rt.SaveRule(r0FwdRule)
		require.NoError(t, err)

		r1FwdRule := routing.ForwardRule(ruleKeepAlive, r1RtIDs[0], r0RtIDs[0], tp2.Entry.ID, pk1, pk2, 0, 0)
		err = rg1.rt.SaveRule(r1FwdRule)
		require.NoError(t, err)

		r0FwdRtDesc := r0FwdRule.RouteDescriptor()
		rg0.desc = r0FwdRtDesc.Invert()
		rg0.tps = append(rg0.tps, tp1)
		rg0.fwd = append(rg0.fwd, r0FwdRule)

		r1FwdRtDesc := r1FwdRule.RouteDescriptor()
		rg1.desc = r1FwdRtDesc.Invert()
		rg1.tps = append(rg1.tps, tp2)
		rg1.fwd = append(rg1.fwd, r1FwdRule)

		ctx, cancel := context.WithCancel(context.Background())

		// push close packet from transport to route group
		go pushPackets(ctx, m2, rg1)
		go pushPackets(ctx, m1, rg0)

		stop = func() {
			_ = rg0.Close() // nolint:errcheck
			_ = rg1.Close() // nolint:errcheck
			cancel()
			nEnv.Teardown()
		}

		return rg0, rg1, stop, nil
	}

	nettest.TestConn(t, mp)
}

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
			fmt.Printf("GOT CLOSE PACKET ON %s\n", to.LocalAddr().String())
			if to.isClosed() {
				panic(io.ErrClosedPipe)
			}

			if err := to.handleClosePacket(routing.CloseCode(packet.Payload()[0])); err != nil {
				panic(err)
			}

			return
		case routing.DataPacket:
			//fmt.Println("GOT DATA PACKET")
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
			// TODO: come up with idea how to get rid of panic
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
	rt := routing.NewTable(routing.DefaultConfig())

	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()
	port1 := routing.Port(1)
	port2 := routing.Port(2)
	desc := routing.NewRouteDescriptor(pk1, pk2, port1, port2)

	rg := NewRouteGroup(cfg, rt, desc)

	return rg
}

// nolint:unparam
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
