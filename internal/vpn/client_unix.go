//+build !windows

package vpn

import (
	"fmt"
	"io"

	"github.com/songgao/water"
)

func createTUN() (io.ReadWriteCloser, error) {
	tun, err := water.New(water.Config{
		DeviceType: water.TUN,
	})
	if err != nil {
		return nil, fmt.Errorf("error allocating TUN interface: %w", err)
	}

	return tun, nil
}
