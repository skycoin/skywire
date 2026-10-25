//go:build linux

// Package network pkg/transport/network/link_stats_linux_test.go c2-net-transport
package network

import (
	"io"
	"net"
	"testing"

	kcp "github.com/0magnet/kcp-go/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	types "github.com/skycoin/skywire/pkg/transport/types"
)

func TestConnDetailsLinkTCP(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer lis.Close() //nolint:errcheck
	go func() {
		c, err := lis.Accept()
		if err == nil {
			_, _ = io.Copy(io.Discard, c) //nolint:errcheck
			_ = c.Close()                 //nolint:errcheck
		}
	}()
	c, err := net.Dial("tcp", lis.Addr().String())
	require.NoError(t, err)
	defer c.Close() //nolint:errcheck
	_, err = c.Write(make([]byte, 256<<10))
	require.NoError(t, err)

	d := (&transport{Conn: c, rawConn: c, transportType: types.STCPR}).ConnDetails()
	require.NotNil(t, d.Link)
	require.NotNil(t, d.Link.Loss)
	assert.Positive(t, d.Link.Loss.PacketsSent)
	assert.Positive(t, d.Link.SRTTMs)
	assert.Positive(t, d.Link.CwndBytes)
}

func TestConnDetailsLinkKCP(t *testing.T) {
	lis, err := kcp.ListenWithOptions("127.0.0.1:0", nil, 0, 0)
	require.NoError(t, err)
	defer lis.Close() //nolint:errcheck
	s, err := kcp.DialWithOptions(lis.Addr().String(), nil, 0, 0)
	require.NoError(t, err)
	defer s.Close() //nolint:errcheck

	d := (&transport{Conn: s, rawConn: s, transportType: types.SUDPH}).ConnDetails()
	require.NotNil(t, d.Link)
	assert.Positive(t, d.Link.RTOMs)
	assert.Nil(t, d.Link.Loss, "KCP keeps no per-session loss count")
}
