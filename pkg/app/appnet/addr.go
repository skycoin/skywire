// Package appnet pkg/app/appnet/addr.go
package appnet

import (
	"errors"
	"fmt"
	"net"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
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
// to `Addr` if possible. Accepts:
//
//   - dmsg.Addr      — pre-skynet legacy address; mapped to TypeDmsg.
//   - routing.Addr   — emitted by route-group conns (PingRoute path).
//   - appnet.Addr    — already in this package's type; emitted by
//     directConn (AppDirect VStream conns). Passed
//     through unchanged so listeners that call
//     WrapConn on a conn from the AppDirect dispatch
//     loop accept it instead of erroring with
//     ErrUnknownAddrType.
//
// Pre-fix: any listener calling WrapConn on a direct-dial conn (most
// notably the ping listener at init_services.go) hit the default
// branch, returned ErrUnknownAddrType, logged "Failed to wrap ping
// conn" and closed the stream. The client then saw the close on its
// first write and surfaced "write size: use of closed network
// connection" — a confusing failure mode that masked the underlying
// type-assertion gap.
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
	case Addr:
		return a, nil
	default:
		return Addr{}, ErrUnknownAddrType
	}
}
