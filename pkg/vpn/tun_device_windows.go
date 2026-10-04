//go:build windows
// +build windows

// Package vpn pkg/vpn/tun_device_windows.go c4-app-vpn
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
	packets := [][]byte{buf}
	sizes := make([]int, 1)
	_, err := t.tun.Read(packets, sizes, 0)
	if err != nil {
		return 0, err
	}
	return sizes[0], nil
}

// Write writes one packet. tun.Device.Write returns a count of packets, not
// bytes, and io.Copy took that 1 for a short write and stopped the client.
func (t *tunDevice) Write(buf []byte) (int, error) {
	if _, err := t.tun.Write([][]byte{buf}, 0); err != nil {
		return 0, err
	}
	return len(buf), nil
}

func (t *tunDevice) Close() error {
	return t.tun.Close()
}

func (t *tunDevice) Name() string {
	return t.name
}
