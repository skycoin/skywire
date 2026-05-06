// Package appnet pkg/app/appnet/skywire_conn.go
package appnet

import (
	"net"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/routing"
)

// SkywireConn is a connection wrapper for skynet.
//
// nrg is nil for connections established via the direct-transport
// app-dial path (no route group, no setup-node). Direct conns expose
// the same net.Conn surface but the metric / status accessors return
// zero values since the route-group bookkeeping that backs them
// doesn't exist for raw vstreams.
type SkywireConn struct {
	net.Conn
	nrg        *router.NoiseRouteGroup
	freePort   func()
	freePortMx sync.RWMutex
	once       sync.Once
}

// IsAlive checks whether connection is alive.
func (c *SkywireConn) IsAlive() bool {
	if c.nrg == nil {
		return c.Conn != nil
	}
	return c.nrg.IsAlive()
}

// Latency returns latency till remote (ms).
func (c *SkywireConn) Latency() time.Duration {
	if c.nrg == nil {
		return 0
	}
	return c.nrg.Latency()
}

// UploadSpeed returns upload speed (bytes/s).
func (c *SkywireConn) UploadSpeed() uint32 {
	if c.nrg == nil {
		return 0
	}
	return c.nrg.UploadSpeed()
}

// DownloadSpeed returns download speed (bytes/s).
func (c *SkywireConn) DownloadSpeed() uint32 {
	if c.nrg == nil {
		return 0
	}
	return c.nrg.DownloadSpeed()
}

// BandwidthSent returns amount of bandwidth sent (bytes).
func (c *SkywireConn) BandwidthSent() uint64 {
	if c.nrg == nil {
		return 0
	}
	return c.nrg.BandwidthSent()
}

// BandwidthReceived returns amount of bandwidth received (bytes).
func (c *SkywireConn) BandwidthReceived() uint64 {
	if c.nrg == nil {
		return 0
	}
	return c.nrg.BandwidthReceived()
}

// SetError sets the close error.
func (c *SkywireConn) SetError(err error) {
	if c.nrg == nil {
		return
	}
	c.nrg.SetError(err)
}

// GetError gets the close error.
func (c *SkywireConn) GetError() error {
	if c.nrg == nil {
		return nil
	}
	return c.nrg.GetError()
}

// RouteHops returns the list of visor public keys that form the route path.
func (c *SkywireConn) RouteHops() []cipher.PubKey {
	if c.nrg == nil {
		return nil
	}
	return c.nrg.RouteHops()
}

// RouteHopDetails returns detailed information about each hop in the route.
func (c *SkywireConn) RouteHopDetails() []router.RouteHopInfo {
	if c.nrg == nil {
		return nil
	}
	return c.nrg.RouteHopDetails()
}

// SetForwardHops sets the complete forward route hops.
func (c *SkywireConn) SetForwardHops(hops []routing.Hop) {
	if c.nrg == nil {
		return
	}
	c.nrg.SetForwardHops(hops)
}

// Close closes connection.
func (c *SkywireConn) Close() error {
	var err error

	c.once.Do(func() {
		defer func() {
			c.freePortMx.RLock()
			defer c.freePortMx.RUnlock()
			if c.freePort != nil {
				c.freePort()
			}
		}()

		err = c.Conn.Close()
	})

	return err
}
