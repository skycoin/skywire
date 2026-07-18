//go:build !windows
// +build !windows

// Package vpn pkg/vpn/tun_device_unix.go c4-app-vpn
package vpn

import (
	"fmt"

	"github.com/songgao/water"
)

func newTUNDevice() (TUNDevice, error) {
	tun, err := water.New(water.Config{
		DeviceType: water.TUN,
	})
	if err != nil {
		return nil, fmt.Errorf("error allocating TUN interface: %w", err)
	}

	return tun, nil
}
