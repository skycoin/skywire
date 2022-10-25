// Package appnet pkg/app/appnet/addr.go
package appnet

import (
	"errors"
	"fmt"
	"net"

	"github.com/skycoin/dmsg/pkg/dmsg"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
)

var (
	// ErrUnknownAddrType is returned when trying to convert the
	// unknown addr type.
	ErrUnknownAddrType = errors.New("addr type is unknown")
)

// Addr implements net.Addr for network addresses.
type Addr struct {
	Net    Type
	PubKey cipher.PubKey
	Port   routing.Port
}

// Network returns network type.
func (a Addr) Network() string {
	return string(a.Net)
}

// String returns public key and port of visor split by colon.
func (a Addr) String() string {
	if a.Port == 0 {
		return fmt.Sprintf("%s:~", a.PubKey)
	}

	return fmt.Sprintf("%s:%d", a.PubKey, a.Port)
}

// PK returns public key of visor.
func (a Addr) PK() cipher.PubKey {
	return a.PubKey
}

// ConvertAddr asserts type of the passed `net.Addr` and converts it
// to `Addr` if possible.
func ConvertAddr(addr net.Addr) (Addr, error) {
	switch a := addr.(type) {
	case dmsg.Addr:
		return Addr{
			Net:    TypeDmsg,
			PubKey: a.PK,
			Port:   routing.Port(a.Port),
		}, nil
	case routing.Addr:
		return Addr{
			Net:    TypeSkynet,
			PubKey: a.PubKey,
			Port:   a.Port,
		}, nil
	default:
		return Addr{}, ErrUnknownAddrType
	}
}
