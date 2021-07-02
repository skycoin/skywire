package router

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/skycoin/dmsg/cipher"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
)

func TestNewRouteGroup(t *testing.T) {
	rg := createRouteGroup(DefaultRouteGroupConfig())
	require.NotNil(t, rg)
	require.Equal(t, DefaultRouteGroupConfig(), rg.cfg)
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
		case routing.HandshakePacket:
			// error won't happen with the handshake packet
			_ = to.handlePacket(packet) //nolint:errcheck
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
