//go:build ios

// Package vpn pkg/vpn/tun_device_ios.go c4-app-vpn
package vpn

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync/atomic"

	"golang.org/x/sys/unix"
)

// iosTunFD is the utun descriptor the packet-tunnel extension handed over
// (skywire_set_tun_fd), or -1. An app on iOS cannot open a utun itself: the
// system creates it for the extension, which keeps ownership.
var iosTunFD atomic.Int64

func init() { iosTunFD.Store(-1) }

var errIOSNoTun = errors.New("no TUN: the packet-tunnel extension has not handed one over")

// SetIOSTunFD records the extension's utun descriptor for the next device.
func SetIOSTunFD(fd int) {
	iosTunFD.Store(int64(fd))
}

// newTUNDevice wraps a duplicate of the extension's descriptor, so closing the
// device (which unblocks its Read) leaves the extension's own open. Non-blocking,
// so the Go runtime polls it rather than parking a thread in read(2).
func newTUNDevice() (TUNDevice, error) {
	fd := int(iosTunFD.Load())
	if fd < 0 {
		return nil, errIOSNoTun
	}
	dup, err := unix.Dup(fd)
	if err != nil {
		return nil, fmt.Errorf("error duplicating the TUN descriptor: %w", err)
	}
	if err := unix.SetNonblock(dup, true); err != nil {
		_ = unix.Close(dup)
		return nil, fmt.Errorf("error putting the TUN descriptor in non-blocking mode: %w", err)
	}
	// SYSPROTO_CONTROL, UTUN_OPT_IFNAME, as wireguard-go's tun_darwin.go asks.
	name, err := unix.GetsockoptString(dup, 2, 2)
	if err != nil || !strings.HasPrefix(name, "utun") {
		name = "utun"
	}
	return newUtunDevice(os.NewFile(uintptr(dup), name), name), nil
}
