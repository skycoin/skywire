// Package router pkg/router/noise_route_group.go
package router

import (
	"net"
	"time"

	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
)

// NoiseRouteGroup is a route group wrapped with noise.
// Implements net.Conn.
type NoiseRouteGroup struct {
	rg *RouteGroup
	net.Conn
}

// LocalAddr returns local address.
func (nrg *NoiseRouteGroup) LocalAddr() net.Addr {
	return nrg.rg.LocalAddr()
}

// RemoteAddr returns remote address.
func (nrg *NoiseRouteGroup) RemoteAddr() net.Addr {
	return nrg.rg.RemoteAddr()
}

// IsAlive checks if connection is alive.
func (nrg *NoiseRouteGroup) IsAlive() bool {
	return nrg.rg.IsAlive()
}

// Latency returns latency till remote (ms).
func (nrg *NoiseRouteGroup) Latency() time.Duration {
	return nrg.rg.Latency()
}

// UploadSpeed returns upload speed (bytes/s).
func (nrg *NoiseRouteGroup) UploadSpeed() uint32 {
	return nrg.rg.UploadSpeed()
}

// DownloadSpeed returns upload speed (bytes/s).
func (nrg *NoiseRouteGroup) DownloadSpeed() uint32 {
	return nrg.rg.DownloadSpeed()
}

// BandwidthSent returns amount of bandwidth sent (bytes).
func (nrg *NoiseRouteGroup) BandwidthSent() uint64 {
	return nrg.rg.BandwidthSent()
}

// BandwidthReceived returns amount of bandwidth received (bytes).
func (nrg *NoiseRouteGroup) BandwidthReceived() uint64 {
	return nrg.rg.BandwidthReceived()
}

// SetError sets the close error.
func (nrg *NoiseRouteGroup) SetError(err error) {
	nrg.rg.SetError(err)
}

// GetError gets the close error.
func (nrg *NoiseRouteGroup) GetError() error {
	return nrg.rg.GetError()
}

func (nrg *NoiseRouteGroup) isClosed() bool {
	return nrg.rg.isClosed()
}

// RouteHops returns the list of visor public keys that form the route path.
func (nrg *NoiseRouteGroup) RouteHops() []cipher.PubKey {
	return nrg.rg.RouteHops()
}

// SetForwardHops sets the complete forward route hops.
func (nrg *NoiseRouteGroup) SetForwardHops(hops []routing.Hop) {
	nrg.rg.SetForwardHops(hops)
}

// RouteHopDetails returns detailed information about each hop in the route.
func (nrg *NoiseRouteGroup) RouteHopDetails() []RouteHopInfo {
	return nrg.rg.RouteHopDetails()
}

func (nrg *NoiseRouteGroup) handlePacket(packet routing.Packet) error {
	return nrg.rg.handlePacket(packet)
}
