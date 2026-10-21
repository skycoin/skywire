package meshgw

import (
	"context"
	"net"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// TestStatusHostsGoToLocal checks that with a local dial set, the reserved
// status hosts resolve and are reached through it, while the skywire zone
// answers nothing else.
func TestStatusHostsGoToLocal(t *testing.T) {
	meshDials := 0
	g, err := New(func(context.Context, string, cipher.PubKey, uint16) (net.Conn, error) {
		meshDials++
		return nil, net.ErrClosed
	}, "100.64.0.0/16", nil, nil)
	require.NoError(t, err)

	// Without a local dial a status name is not a mesh name.
	_, err = g.Resolve("dmsg", "status.dmsg")
	require.Error(t, err)

	type call struct {
		host string
		port uint16
	}
	var calls []call
	g.SetLocal(func(_ context.Context, host string, port uint16) (net.Conn, error) {
		calls = append(calls, call{host, port})
		c, _ := net.Pipe()
		return c, nil
	})

	for _, tc := range []struct{ scheme, name string }{
		{"dmsg", "status.dmsg"},
		{"skynet", "status.skynet"},
		{"skywire", "status.skywire"},
	} {
		ip, err := g.Resolve(tc.scheme, tc.name)
		require.NoError(t, err, tc.name)
		c, err := g.DialIP(context.Background(), ip, 80)
		require.NoError(t, err, tc.name)
		_ = c.Close() //nolint:errcheck
	}
	require.Equal(t, []call{{"status.dmsg", 80}, {"status.skynet", 80}, {"status.skywire", 80}}, calls)
	require.Zero(t, meshDials)

	pk, _ := cipher.GenerateKeyPair()
	_, err = g.Resolve("skywire", pk.Hex()+".skywire")
	require.Error(t, err, "the skywire zone serves only status hosts")
	_, err = g.Resolve("skynet", pk.Hex()+".skynet")
	require.NoError(t, err, "mesh names still resolve")
}
