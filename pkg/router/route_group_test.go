package router

import (
	"context"
	"net"
	"testing"
	"time"

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
	require.NotNil(t, rg1)

	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(3*time.Second))
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		_, err := rg1.Read(buf1)
		errCh <- err
	}()

	var err error
	select {
	case <-ctx.Done():
		err = ctx.Err()
	case err = <-errCh:
	}
	require.Equal(t, context.DeadlineExceeded, err)
	require.NoError(t, rg1.Close())

	rg1 = createRouteGroup()
	rg2 := createRouteGroup()

	_, _, teardown := createTransports(t, rg1, rg2)
	defer teardown()

	go func() {
		rg1.readCh <- msg1
		rg2.readCh <- msg2
	}()

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

	m1, m2, teardown := createTransports(t, rg1, rg2)
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

func TestRouteGroup_LocalAddr(t *testing.T) {
	rg := createRouteGroup()
	require.Equal(t, rg.desc.Src(), rg.LocalAddr())
}

func TestRouteGroup_RemoteAddr(t *testing.T) {
	rg := createRouteGroup()
	require.Equal(t, rg.desc.Dst(), rg.RemoteAddr())
}

func TestRouteGroup_SetReadDeadline(t *testing.T) {
	rg := createRouteGroup()
	now := time.Now()

	require.NoError(t, rg.SetReadDeadline(now))
	require.Equal(t, now.UnixNano(), rg.readDeadline)
}

func TestRouteGroup_SetWriteDeadline(t *testing.T) {
	rg := createRouteGroup()
	now := time.Now()

	require.NoError(t, rg.SetWriteDeadline(now))
	require.Equal(t, now.UnixNano(), rg.writeDeadline)
}

func TestRouteGroup_SetDeadline(t *testing.T) {
	rg := createRouteGroup()
	now := time.Now()

	assert.NoError(t, rg.SetDeadline(now))
	assert.Equal(t, now.UnixNano(), rg.readDeadline)
	assert.Equal(t, now.UnixNano(), rg.writeDeadline)
}

func TestRouteGroup_TestConn(t *testing.T) {
	mp := func() (c1, c2 net.Conn, stop func(), err error) {
		rg1 := createRouteGroup()
		rg2 := createRouteGroup()

		c1, c2 = rg1, rg2

		m1, m2, teardownEnv := createTransports(t, rg1, rg2)
		ctx, cancel := context.WithCancel(context.Background())

		pushPackets := func(from *transport.Manager, to *RouteGroup) {
			for {
				select {
				case <-ctx.Done():
					return
				default:
					packet, err := from.ReadPacket()
					assert.NoError(t, err)
					select {
					case <-ctx.Done():
						return
					case to.readCh <- packet.Payload():
					}
				}
			}
		}

		go pushPackets(m1, rg1)
		go pushPackets(m2, rg2)

		stop = func() {
			cancel()
			teardownEnv()
			assert.NoError(t, c1.Close())
			assert.NoError(t, c2.Close())
		}
		return
	}
	nettest.TestConn(t, mp)
}

func createRouteGroup() *RouteGroup {
	rt := routing.NewTable(routing.DefaultConfig())

	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()
	port1 := routing.Port(1)
	port2 := routing.Port(2)
	desc := routing.NewRouteDescriptor(pk1, pk2, port1, port2)

	rg := NewRouteGroup(DefaultRouteGroupConfig(), rt, desc)
	return rg
}

func createTransports(t *testing.T, rg1, rg2 *RouteGroup) (m1, m2 *transport.Manager, teardown func()) {
	tpDisc := transport.NewDiscoveryMock()
	keys := snettest.GenKeyPairs(2)

	nEnv := snettest.NewEnv(t, keys)

	m1, m2, tp1, tp2, err := transport.CreateTransportPair(tpDisc, keys, nEnv)
	require.NoError(t, err)
	require.NotNil(t, tp1)
	require.NotNil(t, tp2)
	require.NotNil(t, tp1.Entry)
	require.NotNil(t, tp2.Entry)

	keepAlive := 1 * time.Hour
	id1 := routing.RouteID(1)
	id2 := routing.RouteID(2)
	port1 := routing.Port(1)
	port2 := routing.Port(2)
	rule1 := routing.ForwardRule(keepAlive, id1, id2, tp2.Entry.ID, keys[0].PK, port1, port2)
	rule2 := routing.ForwardRule(keepAlive, id2, id1, tp1.Entry.ID, keys[1].PK, port2, port1)

	rg1.tps = append(rg1.tps, tp1)
	rg1.fwd = append(rg1.fwd, rule1)
	rg2.tps = append(rg2.tps, tp2)
	rg2.fwd = append(rg2.fwd, rule2)

	return m1, m2, func() {
		nEnv.Teardown()
	}
}
