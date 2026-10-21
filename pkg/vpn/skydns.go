// Package vpn pkg/vpn/skydns.go c4-app-vpn
package vpn

import (
	"io"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skydns"
	"github.com/skycoin/skywire/pkg/skynetca"
	"github.com/skycoin/skywire/pkg/vpnrouter/meshgw"
)

// skyDNSSplit is the TUN-to-server writer while SkyDNS is on. Packets for its
// range go to the engine, and the rest go on to the server.
type skyDNSSplit struct {
	sky  *skydns.Engine
	next io.Writer
}

func (s skyDNSSplit) Write(p []byte) (int, error) {
	if skydns.Owns(p) {
		return s.sky.Write(p)
	}
	return s.next.Write(p)
}

// SkyDNSConfig is SkyDNS on a tunnel of its own, with no VPN server behind it.
type SkyDNSConfig struct {
	// Dial carries connections to mesh names. See MeshDialer.
	Dial meshgw.MeshDial
	// Local reaches the visor's own status pages. Optional.
	Local meshgw.LocalDial
	// Upstream is the resolver for every other name, an IP. Empty means the
	// phone's own, as the app reports them.
	Upstream string
	// Minter, when set, terminates HTTPS to mesh names with leaves from a
	// local CA the device trusts. Without it only plain HTTP reaches them.
	Minter skynetca.LeafMinter
	// Aliases maps names such as "home" to the visors they stand for.
	Aliases map[string]cipher.PubKey
	Log     logrus.FieldLogger
}
