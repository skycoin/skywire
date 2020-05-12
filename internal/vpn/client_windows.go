//+build windows

package vpn

import (
	"fmt"
	"io"
	"os"

	"golang.zx2c4.com/wireguard/tun"
)

func createTUN() (io.ReadWriteCloser, error) {
	tun, err := tun.CreateTUN("", TUNMTU)
	if err != nil {
		return nil, fmt.Errorf("error allocating TUN interface: %w", err)
	}

	if err == nil {
		realInterfaceName, err2 := tun.Name()
		if err2 == nil {
			interfaceName = realInterfaceName
		}
	} else {
		logger.Error.Println("Failed to create TUN device:", err)
		os.Exit(ExitSetupFailed)
	}
}
