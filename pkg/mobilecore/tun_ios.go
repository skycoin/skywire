//go:build ios

// Package mobilecore pkg/mobilecore/tun_ios.go c4-vis-core
package mobilecore

import (
	"errors"
	"sync/atomic"
)

// tunFD is the utun descriptor the packet-tunnel extension handed over, or
// -1. vpn-client's iOS TUN device (pkg/vpn/tun_device_ios.go, playbook item
// 7.4) is what reads it; until that lands the descriptor is only kept.
var tunFD atomic.Int64

func init() { tunFD.Store(-1) }

// SetTunFD hands the core the extension's utun descriptor. The extension
// keeps ownership: the core never closes it.
func SetTunFD(fd int) error {
	if fd < 0 {
		return errors.New("mobilecore: invalid TUN descriptor")
	}
	tunFD.Store(int64(fd))
	return nil
}
