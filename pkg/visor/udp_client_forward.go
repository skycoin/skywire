// Package visor pkg/visor/udp_client_forward.go c3-vis-core
// faithful-UDP port forwarding (#2607 stage-4d). The server side
// (init_udp_forwarding.go) bridges inbound datagram routes to a local
// service; this is the mirror: it dials a remote forwarded_ports.udp
// service via DialPacket and bridges it to a local UDP socket the
// operator's UDP app talks to.
package visor

import (
	"errors"
	"fmt"
	"net"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
)

// DialUDPForward opens a faithful-UDP forward: it DialPackets to
// remotePK:remotePort (which must be a forwarded_ports.udp service on
// the peer) and binds a local UDP socket on localPort, bridging the two.
// The operator's UDP app then talks to 127.0.0.1:localPort and its
// datagrams reach the remote service (and replies come back). Runs until
// StopUDPForward(localPort) or visor shutdown.
func (v *Visor) DialUDPForward(remotePK cipher.PubKey, remotePort, localPort int) error {
	if remotePort < 1 || remotePort > 65535 || localPort < 1 || localPort > 65535 {
		return fmt.Errorf("ports must be in 1..65535 (remote=%d local=%d)", remotePort, localPort)
	}
	nw, err := appnet.ResolveNetworker(appnet.TypeSkynet)
	if err != nil {
		return fmt.Errorf("skynet networker unavailable: %w", err)
	}
	pn, ok := nw.(appnet.PacketNetworker)
	if !ok {
		return errors.New("skynet networker does not support datagrams (build too old?)")
	}

	v.udpFwdMu.Lock()
	if _, exists := v.udpClientBridges[localPort]; exists {
		v.udpFwdMu.Unlock()
		return fmt.Errorf("local UDP port %d already forwarding", localPort)
	}
	v.udpFwdMu.Unlock()

	// Bind the local UDP socket the operator's app sends to.
	local, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: localPort}) //nolint:gosec
	if err != nil {
		return fmt.Errorf("bind local udp :%d: %w", localPort, err)
	}

	addr := appnet.Addr{Net: appnet.TypeSkynet, PubKey: remotePK, Port: routing.Port(remotePort)} //nolint:gosec
	dgConn, err := pn.DialPacketContext(v.ctx, addr)
	if err != nil {
		local.Close() //nolint:errcheck,gosec
		return fmt.Errorf("dial UDP forward %s:%d: %w", remotePK, remotePort, err)
	}

	bridge := NewUDPBridge(local, dgConn, UDPBridgeConfig{Logger: v.log})
	// Client side: no SetLocalPeer — the first datagram from the local
	// app captures its source addr; replies route back to it.
	bridge.Start()

	v.udpFwdMu.Lock()
	v.udpClientBridges[localPort] = bridge
	v.udpFwdMu.Unlock()

	v.log.WithField("remote", fmt.Sprintf("%s:%d", remotePK, remotePort)).
		WithField("local", fmt.Sprintf("127.0.0.1:%d", localPort)).
		Info("UDP forward established")
	return nil
}

// StopUDPForward tears down a client-side UDP forward by its local port.
func (v *Visor) StopUDPForward(localPort int) error {
	v.udpFwdMu.Lock()
	bridge, ok := v.udpClientBridges[localPort]
	if ok {
		delete(v.udpClientBridges, localPort)
	}
	v.udpFwdMu.Unlock()
	if !ok {
		return fmt.Errorf("no UDP forward on local port %d", localPort)
	}
	return bridge.Close()
}

// ListUDPForwards returns the local ports with active client-side UDP
// forwards.
func (v *Visor) ListUDPForwards() ([]int, error) {
	v.udpFwdMu.Lock()
	defer v.udpFwdMu.Unlock()
	ports := make([]int, 0, len(v.udpClientBridges))
	for p := range v.udpClientBridges {
		ports = append(ports, p)
	}
	return ports, nil
}
