package vpn

import (
	"errors"
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

// gwTable is a route table that remembers the gateway of each route.
type gwTable struct {
	c      *Client
	routes map[string]string // ip -> gateway
	failOn string            // an add to this ip fails
	gone   bool              // the old routes vanished with their interface
}

func repinClient(t *testing.T, gw string, ips ...string) (*Client, *gwTable) {
	t.Helper()
	c := &Client{closeC: make(chan struct{}), defaultGateway: net.ParseIP(gw)}
	tb := &gwTable{c: c, routes: map[string]string{}}
	for _, ip := range ips {
		c.directIPs = append(c.directIPs, net.ParseIP(ip))
		tb.routes[ip] = gw
	}
	c.addDirect = func(ip net.IP) error {
		if ip.String() == tb.failOn {
			return errors.New("add failed")
		}
		tb.routes[ip.String()] = c.defaultGateway.String()
		return nil
	}
	c.delDirect = func(ip net.IP) error {
		if tb.gone {
			return errors.New("no such route")
		}
		if tb.routes[ip.String()] != c.defaultGateway.String() {
			t.Errorf("delete of %s must name the gateway it was added with", ip)
		}
		delete(tb.routes, ip.String())
		return nil
	}
	c.initDirectRoutes()
	return c, tb
}

func TestRepinMovesEveryRouteToTheNewGateway(t *testing.T) {
	c, tb := repinClient(t, "192.168.1.1", "203.0.113.10", "198.51.100.20")
	c.gatewayFn = func() (net.IP, error) { return net.ParseIP("10.64.0.1"), nil }

	gw, changed, err := c.RepinDirectRoutes()
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, "10.64.0.1", gw.String())
	require.Equal(t, map[string]string{"203.0.113.10": "10.64.0.1", "198.51.100.20": "10.64.0.1"}, tb.routes)
	require.Equal(t, "10.64.0.1", c.gateway().String(), "the handshake reserves the new gateway")

	// A route added after the move goes through the new gateway too.
	require.NoError(t, c.AddDirectRoute(net.ParseIP("192.0.2.30")))
	require.Equal(t, "10.64.0.1", tb.routes["192.0.2.30"])

	// The same gateway again is a no-op.
	_, changed, err = c.RepinDirectRoutes()
	require.NoError(t, err)
	require.False(t, changed)
}

// The old interface took its routes with it: the deletes fail, the adds
// still happen, and one failed add is reported without stopping the rest.
func TestRepinSurvivesVanishedRoutesAndReportsFailures(t *testing.T) {
	c, tb := repinClient(t, "192.168.1.1", "203.0.113.10", "198.51.100.20")
	tb.gone = true
	tb.failOn = "203.0.113.10"
	c.gatewayFn = func() (net.IP, error) { return net.ParseIP("172.20.10.1"), nil }

	_, changed, err := c.RepinDirectRoutes()
	require.True(t, changed)
	require.Error(t, err)
	require.Equal(t, "172.20.10.1", tb.routes["198.51.100.20"])
}

func TestRepinNeedsAGateway(t *testing.T) {
	c, tb := repinClient(t, "192.168.1.1", "203.0.113.10")
	c.gatewayFn = func() (net.IP, error) { return nil, errors.New("no route") }
	_, _, err := c.RepinDirectRoutes()
	require.Error(t, err)
	c.gatewayFn = func() (net.IP, error) { return net.IPv4zero, nil }
	_, _, err = c.RepinDirectRoutes()
	require.Error(t, err)
	require.Equal(t, "192.168.1.1", tb.routes["203.0.113.10"], "routes stay when there is nothing to move to")
}
