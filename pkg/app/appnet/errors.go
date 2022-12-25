// Package appnet pkg/app/appnet/errors.go
package appnet

import (
	"fmt"

	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// ErrServiceOffline is used to get a verbose error of GetListenerError
func ErrServiceOffline(port uint16) error {
	switch port {
	case visorconfig.SkychatPort:
		return fmt.Errorf("no listener on port %d, skychat offline", port)
	case visorconfig.SkysocksPort:
		return fmt.Errorf("no listener on port %d, skysocks offline", port)
	case visorconfig.SkysocksClientPort:
		return fmt.Errorf("no listener on port %d, skysocks-client offline", port)
	case visorconfig.VPNServerPort:
		return fmt.Errorf("no listener on port %d, vpn-server offline", port)
	case visorconfig.VPNClientPort:
		return fmt.Errorf("no listener on port %d, vpn-client offline", port)
	}
	return fmt.Errorf("no listener on port %d, service offline", port)
}
