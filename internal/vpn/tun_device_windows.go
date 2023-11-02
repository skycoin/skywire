//go:build windows
// +build windows

package vpn

import (
	"fmt"

	"golang.zx2c4.com/wireguard/tun"
)

type tunDevice struct {
	tun  tun.Device
	name string
}

func newTUNDevice() (TUNDevice, error) {
	const tunName = "tun0"

	dev, err := tun.CreateTUN(tunName, TUNMTU)
	if err != nil {
		return nil, fmt.Errorf("error allocating TUN interface: %w", err)
	}

	name, err := dev.Name()
	if err != nil {
		return nil, fmt.Errorf("error getting interface name: %w", err)
	}

	return &tunDevice{
		tun:  dev,
		name: name,
	}, nil
}

func (t *tunDevice) Read(buf []byte) (int, error) {
	validBuf := [][]byte{buf}
	return t.tun.Read(validBuf, []int{len(buf) + 1}, 0)
}

func (t *tunDevice) Write(buf []byte) (int, error) {
	validBuf := [][]byte{buf}
	return t.tun.Write(validBuf, 0)
}

func (t *tunDevice) Close() error {
	return t.tun.Close()
}

func (t *tunDevice) Name() string {
	return t.name
}
