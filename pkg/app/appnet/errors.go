// Package appnet pkg/app/appnet/errors.go
package appnet

import (
	"fmt"

	"github.com/skycoin/skywire/pkg/skyenv"
)

// ErrServiceOffline is used to get a verbose error of GetListenerError
func ErrServiceOffline(port uint16) error {
	switch port {
	case skyenv.SkychatPort:
		return fmt.Errorf("no listener on port %d, skychat offline", port)
	case skyenv.SkysocksPort:
		return fmt.Errorf("no listener on port %d, skysocks offline", port)
	case skyenv.SkysocksClientPort:
		return fmt.Errorf("no listener on port %d, skysocks-client offline", port)
	case skyenv.VPNServerPort:
		return fmt.Errorf("no listener on port %d, vpn-server offline", port)
	case skyenv.VPNClientPort:
		return fmt.Errorf("no listener on port %d, vpn-client offline", port)
	}
	return fmt.Errorf("no listener on port %d, service offline", port)
}
