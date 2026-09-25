package vpn

import (
	"net"
	"testing"

	"github.com/stretchr/testify/require"
)

// routeRecorder stands in for the OS routing table.
type routeRecorder struct{ installed map[string]bool }

func testClient(t *testing.T, pinned ...string) (*Client, *routeRecorder) {
	t.Helper()
	rr := &routeRecorder{installed: map[string]bool{}}
	c := &Client{closeC: make(chan struct{})}
	for _, p := range pinned {
		c.directIPs = append(c.directIPs, net.ParseIP(p))
		rr.installed[p] = true
	}
	c.addDirect = func(ip net.IP) error { rr.installed[ip.String()] = true; return nil }
	c.delDirect = func(ip net.IP) error { delete(rr.installed, ip.String()); return nil }
	c.initDirectRoutes()
	return c, rr
}

// TestSharedIPKeepsItsRouteUntilTheLastUserCloses: two transports to one
// IP; the first to close must not pull the route from under the second.
func TestSharedIPKeepsItsRouteUntilTheLastUserCloses(t *testing.T) {
	c, rr := testClient(t)
	ip := net.ParseIP("203.0.113.7")
	require.NoError(t, c.AddDirectRoute(ip))
	require.NoError(t, c.AddDirectRoute(ip))
	require.True(t, rr.installed["203.0.113.7"])

	require.NoError(t, c.RemoveDirectRoute(ip))
	require.True(t, rr.installed["203.0.113.7"], "one user left")
	require.NoError(t, c.RemoveDirectRoute(ip))
	require.False(t, rr.installed["203.0.113.7"], "last user gone")

	require.NoError(t, c.RemoveDirectRoute(ip), "an extra close is harmless")
	require.NoError(t, c.AddDirectRoute(ip))
	require.True(t, rr.installed["203.0.113.7"], "and a new dial installs it again")
}

// TestPinnedRoutesSurviveCloses: the IPs named at start (discovery,
// the sessions up then) keep their routes whatever closes later.
func TestPinnedRoutesSurviveCloses(t *testing.T) {
	c, rr := testClient(t, "198.51.100.1")
	ip := net.ParseIP("198.51.100.1")
	require.NoError(t, c.AddDirectRoute(ip))
	require.NoError(t, c.RemoveDirectRoute(ip))
	require.NoError(t, c.RemoveDirectRoute(ip))
	require.True(t, rr.installed["198.51.100.1"])
}
