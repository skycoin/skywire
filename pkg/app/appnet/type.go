// Package appnet pkg/app/appnet/type.go
package appnet

// Type represents the network type.
type Type string

const (
	// TypeDmsg is a network type for dmsg communication.
	TypeDmsg Type = "dmsg"
	// TypeSkynet is a network type for skywire communication.
	TypeSkynet Type = "skynet"
)

// IsValid checks whether the network contains valid value for the type.
func (n Type) IsValid() bool {
	_, ok := validNetworks[n]
	return ok
}

// nolint: gochecknoglobals
var (
	validNetworks = map[Type]struct{}{
		TypeDmsg:   {},
		TypeSkynet: {},
	}
)
