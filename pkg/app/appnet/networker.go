// Package appnet pkg/app/appnet/networker.go
package appnet

import (
	"context"
	"errors"
	"net"
	"sync"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
)

//go:generate mockery --name Networker --case underscore --inpackage

var (
	// ErrNoSuchNetworker is being returned when there's no suitable networker.
	ErrNoSuchNetworker = errors.New("no such networker")
	// ErrNetworkerAlreadyExists is being returned when there's already one with such Network type.
	ErrNetworkerAlreadyExists = errors.New("networker already exists")
)

// nolint: gochecknoglobals
var (
	networkers   = make(map[Type]Networker)
	networkersMx sync.RWMutex
)

// AddNetworker associates Networker with the `network`.
func AddNetworker(t Type, n Networker) error {
	networkersMx.Lock()
	defer networkersMx.Unlock()

	if _, ok := networkers[t]; ok {
		return ErrNetworkerAlreadyExists
	}

	networkers[t] = n

	return nil
}

// ResolveNetworker resolves Networker by `network`.
func ResolveNetworker(t Type) (Networker, error) {
	networkersMx.RLock()

	n, ok := networkers[t]
	if !ok {
		networkersMx.RUnlock()
		return nil, ErrNoSuchNetworker
	}

	networkersMx.RUnlock()

	return n, nil
}

// ClearNetworkers removes all the stored networkers.
func ClearNetworkers() {
	networkersMx.Lock()
	defer networkersMx.Unlock()

	networkers = make(map[Type]Networker)
}

// Networker defines basic network operations, such as Dial/Listen.
type Networker interface {
	Dial(addr Addr) (net.Conn, error)
	Ping(pk cipher.PubKey, addr Addr) (net.Conn, error)
	DialContext(ctx context.Context, addr Addr) (net.Conn, error)
	Listen(addr Addr) (net.Listener, error)
	ListenContext(ctx context.Context, addr Addr) (net.Listener, error)
}

// Dial dials the remote `addr`.
func Dial(addr Addr) (net.Conn, error) {
	return DialContext(context.Background(), addr)
}

// Ping dials the remote `addr`.
func Ping(pk cipher.PubKey, addr Addr) (net.Conn, error) {
	n, err := ResolveNetworker(addr.Net)
	if err != nil {
		return nil, err
	}
	return n.Ping(pk, addr)
}

// DialContext dials the remote `addr` with the context.
func DialContext(ctx context.Context, addr Addr) (net.Conn, error) {
	n, err := ResolveNetworker(addr.Net)
	if err != nil {
		return nil, err
	}

	return n.DialContext(ctx, addr)
}

// Listen starts listening on the local `addr`.
func Listen(addr Addr) (net.Listener, error) {
	return ListenContext(context.Background(), addr)
}

// ListenContext starts listening on the local `addr` with the context.
func ListenContext(ctx context.Context, addr Addr) (net.Listener, error) {
	networker, err := ResolveNetworker(addr.Net)
	if err != nil {
		return nil, err
	}

	return networker.ListenContext(ctx, addr)
}
