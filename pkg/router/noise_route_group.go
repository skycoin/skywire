package router

import (
	"net"
	"time"

	"github.com/skycoin/skywire/pkg/routing"
)

type NoiseRouteGroup struct {
	rg *RouteGroup
	net.Conn
}

func (nrg *NoiseRouteGroup) LocalAddr() net.Addr {
	return nrg.rg.LocalAddr()
}

func (nrg *NoiseRouteGroup) RemoteAddr() net.Addr {
	return nrg.rg.RemoteAddr()
}

func (nrg *NoiseRouteGroup) IsAlive() bool {
	return nrg.rg.IsAlive()
}

func (nrg *NoiseRouteGroup) Latency() time.Duration {
	return nrg.rg.Latency()
}

func (nrg *NoiseRouteGroup) Throughput() uint32 {
	return nrg.rg.Throughput()
}

func (nrg *NoiseRouteGroup) BandwidthSent() uint32 {
	return nrg.rg.BandwidthSent()
}

func (nrg *NoiseRouteGroup) isClosed() bool {
	return nrg.rg.isClosed()
}

func (nrg *NoiseRouteGroup) handlePacket(packet routing.Packet) error {
	return nrg.rg.handlePacket(packet)
}
