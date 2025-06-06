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
	packets := [][]byte{buf}
	sizes := make([]int, 1)
	n, err := t.tun.Read(packets, sizes, 0)
	if err != nil {
		return 0, err
	}
	return sizes[0], nil
}

func (t *tunDevice) Write(buf []byte) (int, error) {
	packets := [][]byte{buf}
	sizes := []int{len(buf)}
	n, err := t.tun.Write(packets, sizes, 0)
	if err != nil {
		return 0, err
	}
	return n, nil
}

func (t *tunDevice) Close() error {
	return t.tun.Close()
}

func (t *tunDevice) Name() string {
	return t.name
}
