// Package vpn pkg/vpn/client_config.go c4-app-vpn
package vpn

import (
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skynetca"
	"github.com/skycoin/skywire/pkg/vpnrouter/meshgw"
)

// ClientConfig is a configuration for VPN client.
type ClientConfig struct {
	Killswitch bool
	ServerPK   cipher.PubKey
	DNSAddr    string
	// MuxRoutes, when > 1, dials the server over that many parallel
	// (multiplexed) routes. MinHops, when >= 2, forces the route through
	// at least that many intermediate visors (multihop) instead of a
	// direct path. Both default to 0 (the plain single-route dial).
	MuxRoutes int
	MinHops   int
	// DmsgFallback, when true, dials the VPN server over a direct dmsg stream if
	// the skynet (route) dial fails. Opt-in: dmsg relays through a dmsg server, so
	// the fallback trades higher latency and a metadata downgrade (the dmsg server
	// sees both endpoint PKs, unlike a source-routed multihop skynet path) for the
	// resilience of an always-available baseline. Default off keeps the plain
	// skynet-only dial.
	DmsgFallback bool

	// MeshGateway, when true (Linux only), additionally runs a local MESH
	// GATEWAY: the host resolves *.dmsg / *.skynet by name and those connections
	// are proxied over the mesh (bypassing the tunnel), the single-machine
	// counterpart of the vpn-router's LAN gateway. MeshDial must be set.
	MeshGateway bool
	// MeshGatewayCIDR is the synthetic-IP pool (empty = 100.64.0.0/16).
	MeshGatewayCIDR string
	// MeshDial performs the mesh dial for resolved names; the vpn-client app
	// wires it to its app.Client. Required when MeshGateway is true.
	MeshDial meshgw.MeshDial
	// MeshAliases maps friendly names to PKs (reach http://<alias>.dmsg). Optional.
	MeshAliases map[string]cipher.PubKey
	// MeshTLSMinter, when set, enables TLS-MITM for HTTPS to *.dmsg / *.skynet.
	// The host must trust the minter's CA. Optional.
	MeshTLSMinter skynetca.LeafMinter
}
