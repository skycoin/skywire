package router

import (
	"net"

	"github.com/SkycoinProject/skywire-mainnet/pkg/routing"
)

type noiseRouteGroup struct {
	rg *RouteGroup
	net.Conn
}

func newNoiseRouteGroup(rg *RouteGroup, wrappedRG net.Conn) *noiseRouteGroup {
	return &noiseRouteGroup{
		rg:   rg,
		Conn: wrappedRG,
	}
}

func (nrg *noiseRouteGroup) isClosed() bool {
	return nrg.rg.isClosed()
}

func (nrg *noiseRouteGroup) handlePacket(packet routing.Packet) error {
	return nrg.rg.handlePacket(packet)
}
