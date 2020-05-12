//+build windows

package vpn

import (
	"fmt"

	"golang.zx2c4.com/wireguard/tun"
)

func createTUN() (TUNDevice, error) {
	// TODO: generate new name or put it into constant
	tun, err := tun.CreateTUN("tun0", TUNMTU)
	if err != nil {
		return nil, fmt.Errorf("error allocating TUN interface: %w", err)
	}

	name, err := tun.Name()
	if err != nil {
		return nil, fmt.Errorf("error getting interface name: %w", err)
	}

	return &tunDevice{
		tun:  tun,
		name: name,
	}, nil
}
