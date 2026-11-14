// Package vpn pkg/vpn/tun_device.go c3-app-vpn
package vpn

import (
	"io"
)

// TUNDevice is a wrapper for TUN interface.
type TUNDevice interface {
	io.ReadWriteCloser
	Name() string
}
