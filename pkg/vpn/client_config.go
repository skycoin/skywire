// Package vpn pkg/vpn/client_config.go c4-app-vpn
package vpn

import "github.com/skycoin/skywire/pkg/cipher"

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
}
