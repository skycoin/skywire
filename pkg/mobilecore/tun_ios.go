//go:build ios

// Package mobilecore pkg/mobilecore/tun_ios.go c4-vis-core
package mobilecore

import (
	"encoding/json"
	"errors"

	"github.com/skycoin/skywire/pkg/vpn"
)

// SetTunFD hands the core the extension's utun descriptor, for vpn-client's
// next TUN device (pkg/vpn/tun_device_ios.go). The extension keeps ownership:
// the core works on a duplicate.
func SetTunFD(fd int) error {
	if fd < 0 {
		return errors.New("mobilecore: invalid TUN descriptor")
	}
	vpn.SetIOSTunFD(fd)
	return nil
}

// TunSettings is the tunnel vpn-client last asked for, as JSON, for the
// extension to apply as its NEPacketTunnelNetworkSettings.
func TunSettings() (string, error) {
	b, err := json.Marshal(vpn.CurrentTunSettings())
	return string(b), err
}
