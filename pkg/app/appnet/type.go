// Package appnet pkg/app/appnet/type.go
package appnet

// Type represents the network type.
type Type string

const (
	// TypeDmsg is a network type for dmsg communication.
	TypeDmsg Type = "dmsg"
	// TypeSkynet is a network type for skywire communication.
	TypeSkynet Type = "skynet"
	// TypeTCPDirect is a network type for noise-XK over raw TCP —
	// point-to-point with known endpoints, no dmsg-disc, no visor
	// routing. Apps use this as a reliability floor for
	// agent-to-agent comms that survives visor restarts and dmsg
	// infrastructure blips. See pkg/skywire/tcpnoise + #2706.
	TypeTCPDirect Type = "tcp-direct"
)

// IsValid checks whether the network contains valid value for the type.
func (n Type) IsValid() bool {
	_, ok := validNetworks[n]
	return ok
}

// nolint: gochecknoglobals
var (
	validNetworks = map[Type]struct{}{
		TypeDmsg:      {},
		TypeSkynet:    {},
		TypeTCPDirect: {},
	}
)
