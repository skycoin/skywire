//go:build !windows
// +build !windows

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
