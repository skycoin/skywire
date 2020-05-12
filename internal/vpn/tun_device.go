package vpn

import "io"

type TUNDevice interface {
	io.ReadWriteCloser
	Name() string
}
