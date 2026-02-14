// Package appnet pkg/app/appnet/networker.go
package appnet

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
)

//go:generate mockery --name Networker --case underscore --inpackage

var (
	// ErrNoSuchNetworker is being returned when there's no suitable networker.
	ErrNoSuchNetworker = errors.New("no such networker")
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
	PingContext(ctx context.Context, pk cipher.PubKey, addr Addr) (net.Conn, error)
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
	return PingContext(context.Background(), pk, addr)
}

// PingContext dials the remote `addr` with the context.
func PingContext(ctx context.Context, pk cipher.PubKey, addr Addr) (net.Conn, error) {
	n, err := ResolveNetworker(addr.Net)
	if err != nil {
		return nil, err
	}
	return n.PingContext(ctx, pk, addr)
}

// PingContextWithTransport dials the remote `addr` using a specific transport.
// This skips route calculation and uses the provided transport directly.
// Only works with TypeSkynet networker.
func PingContextWithTransport(ctx context.Context, pk cipher.PubKey, addr Addr, transportID string) (net.Conn, error) {
	n, err := ResolveNetworker(addr.Net)
	if err != nil {
		return nil, err
	}

	// Type-assert to SkywireNetworker to access PingContextWithOpts
	if sn, ok := n.(*SkywireNetworker); ok && transportID != "" {
		tpUUID, err := uuid.Parse(transportID)
		if err != nil {
			return nil, fmt.Errorf("invalid transport ID: %w", err)
		}
		opts := router.DefaultDialOptions()
		opts.TransportID = tpUUID
		return sn.PingContextWithOpts(ctx, pk, addr, opts)
	}

	// Fallback to normal ping if not skywire networker or no transport ID
	return n.PingContext(ctx, pk, addr)
}

// RouteHopInfo contains detailed information about a single hop in a route.
type RouteHopInfo struct {
	TpID   string
	From   string
	To     string
	TpType string
}

// PingContextWithRoute dials the remote `addr` using a specific route.
// This skips route calculation and uses the provided forward/reverse hops.
// Only works with TypeSkynet networker.
func PingContextWithRoute(ctx context.Context, pk cipher.PubKey, addr Addr, forwardHops, reverseHops []RouteHopInfo) (net.Conn, error) {
	n, err := ResolveNetworker(addr.Net)
	if err != nil {
		return nil, err
	}

	// Type-assert to SkywireNetworker to access PingContextWithOpts
	sn, ok := n.(*SkywireNetworker)
	if !ok || len(forwardHops) == 0 || len(reverseHops) == 0 {
		// Fallback to normal ping
		return n.PingContext(ctx, pk, addr)
	}

	// Convert RouteHopInfo to routing.Hop
	fwdHops := make([]routing.Hop, len(forwardHops))
	for i, h := range forwardHops {
		tpUUID, err := uuid.Parse(h.TpID)
		if err != nil {
			return nil, fmt.Errorf("invalid transport ID in forward hop %d: %w", i, err)
		}
		var fromPK, toPK cipher.PubKey
		if err := fromPK.Set(h.From); err != nil {
			return nil, fmt.Errorf("invalid from PK in forward hop %d: %w", i, err)
		}
		if err := toPK.Set(h.To); err != nil {
			return nil, fmt.Errorf("invalid to PK in forward hop %d: %w", i, err)
		}
		fwdHops[i] = routing.Hop{TpID: tpUUID, From: fromPK, To: toPK}
	}

	revHops := make([]routing.Hop, len(reverseHops))
	for i, h := range reverseHops {
		tpUUID, err := uuid.Parse(h.TpID)
		if err != nil {
			return nil, fmt.Errorf("invalid transport ID in reverse hop %d: %w", i, err)
		}
		var fromPK, toPK cipher.PubKey
		if err := fromPK.Set(h.From); err != nil {
			return nil, fmt.Errorf("invalid from PK in reverse hop %d: %w", i, err)
		}
		if err := toPK.Set(h.To); err != nil {
			return nil, fmt.Errorf("invalid to PK in reverse hop %d: %w", i, err)
		}
		revHops[i] = routing.Hop{TpID: tpUUID, From: fromPK, To: toPK}
	}

	opts := router.DefaultDialOptions()
	opts.ForwardHops = fwdHops
	opts.ReverseHops = revHops
	return sn.PingContextWithOpts(ctx, pk, addr, opts)
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
