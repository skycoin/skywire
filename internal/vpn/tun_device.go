package vpn

import "io"

// TUNDevice is a wrapper for TUN interface.
type TUNDevice interface {
	io.ReadWriteCloser
	Name() string
}
