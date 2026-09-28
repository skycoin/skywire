//go:build !ios

// Package mobilecore pkg/mobilecore/tun.go c4-vis-core
package mobilecore

import "errors"

// SetTunFD is iOS-only: there the packet-tunnel extension owns the TUN
// device and hands its descriptor to the core. Android hands it over its own
// socket (pkg/vpn/tun_device_android.go), and desktop builds open their own.
func SetTunFD(_ int) error {
	return errors.New("mobilecore: SetTunFD is unsupported on this platform")
}
