// Package visor pkg/visor/init_udp_forwarding.go c3-vis-core
// faithful-UDP port forwarding (#2607 stage-4c). The dial/client side
// lives in appnet (DialPacket). This is the accept side: for every
// forwarded port marked UDP, the visor registers datagram intent with
// the router, then a single accept loop drains the datagram siblings
// the route-setup builds for inbound datagram routes and bridges each
// to the local UDP service via a UDPBridge.
package visor

import (
	"net"
	"time"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/routing"
)

// udpForwardLivenessPoll is how often a UDP-forward bridge checks whether
// its datagram route is still alive. A one-way UDP flow produces no write
// traffic to trip the sibling's write-failure detector, so the bridge
// polls IsAlive instead.
const udpForwardLivenessPoll = 10 * time.Second

// registerUDPForwardPorts marks every UDP forwarded port as datagram-
// capable so the router's accept side builds a sibling for inbound
// datagram routes targeting them. Idempotent.
func (v *Visor) registerUDPForwardPorts() {
	if v.router == nil {
		return
	}
	for _, fp := range v.forwardedPorts.List() {
		if fp.UDP {
			v.router.RegisterDatagramPort(routing.Port(fp.Port)) //nolint:gosec // port fits routing.Port
		}
	}
}

// serveUDPForwards is the accept loop: it drains datagram siblings the
// router accepts and bridges each to its local UDP service. Runs for the
// visor's lifetime (exits when v.ctx is done / the router closes).
func (v *Visor) serveUDPForwards(log *logging.Logger) {
	for {
		dg, dstPort, err := v.router.AcceptDatagram(v.ctx)
		if err != nil {
			log.WithError(err).Debug("UDP-forward accept loop stopping")
			return
		}
		fp := v.forwardedPorts.Get(int(dstPort))
		if fp == nil || !fp.UDP {
			log.WithField("dst_port", dstPort).
				Warn("accepted datagram for an unknown/non-UDP forwarded port; closing sibling")
			dg.Close() //nolint:errcheck,gosec
			continue
		}
		go v.bridgeUDPForward(dg, *fp, log)
	}
}

// bridgeUDPForward pumps datagrams between an accepted skynet sibling and
// the local UDP service the forwarded port targets, until either the
// route or the visor goes away.
func (v *Visor) bridgeUDPForward(dg *router.DatagramRouteGroup, fp ForwardedPort, log *logging.Logger) {
	target := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: fp.EffectiveLocalPort()}

	// Ephemeral local socket that talks to the service. skynetToLocal
	// writes inbound datagrams to `target`; localToSkynet reads the
	// service's replies and sends them back over the sibling.
	local, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		log.WithError(err).WithField("target", target.String()).
			Warn("UDP-forward: failed to bind local socket; dropping route")
		dg.Close() //nolint:errcheck,gosec
		return
	}

	bridge := NewUDPBridge(local, dg, UDPBridgeConfig{Logger: log})
	bridge.SetLocalPeer(target) // server side: send inbound to the service
	bridge.Start()
	log.WithField("port", fp.Port).WithField("local", target.String()).
		Debug("UDP-forward bridge established")

	// Tear the bridge down when the datagram route dies (the sibling is
	// reaped by the router on route close → IsAlive goes false) or the
	// visor shuts down. The sibling has no write traffic on a one-way
	// flow, so poll IsAlive rather than relying on a write-failure trip.
	defer bridge.Close() //nolint:errcheck
	for {
		select {
		case <-v.ctx.Done():
			return
		case <-bridge.closed:
			return
		default:
		}
		if !dg.IsAlive() {
			return
		}
		select {
		case <-v.ctx.Done():
			return
		case <-time.After(udpForwardLivenessPoll):
		}
	}
}
