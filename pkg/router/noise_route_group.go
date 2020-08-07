package router

import (
	"net"

	"github.com/skycoin/skywire/pkg/routing"
)

type noiseRouteGroup struct {
	rg *RouteGroup
	net.Conn
}

func (nrg *noiseRouteGroup) LocalAddr() net.Addr {
	return nrg.rg.LocalAddr()
}

func (nrg *noiseRouteGroup) RemoteAddr() net.Addr {
	return nrg.rg.RemoteAddr()
}

func (nrg *noiseRouteGroup) isClosed() bool {
	return nrg.rg.isClosed()
}

func (nrg *noiseRouteGroup) handlePacket(packet routing.Packet) error {
	return nrg.rg.handlePacket(packet)
}
