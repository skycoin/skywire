// Package setup pkg/setup/id_reserver.go
package setup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/router/routerclient"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport/network"
)

// ErrNoKey is returned when key is not found.
var ErrNoKey = errors.New("id reservoir has no key")

//go:generate mockery -name IDReserver -case underscore -inpkg

// IDReserver reserves route IDs from remote routers.
// It takes in a slice of paths where each path is a slice of hops and each hop has src and dst public keys and also a
// transport ID.
type IDReserver interface {
	io.Closer
	fmt.Stringer

	// ReserveIDs reserves route IDs from the router clients.
	// It uses an internal map to know how many ids to reserve from each router.
	ReserveIDs(ctx context.Context) error

	// PopID pops a reserved route ID from the ID stack of the given public key.
	PopID(pk cipher.PubKey) (routing.RouteID, bool)

	// TotalIDs returns the total number of route IDs we have reserved from the routers.
	TotalIDs() int

	// Client returns a router client of given public key.
	Client(pk cipher.PubKey) *routerclient.Client
}

type idReserver struct {
	total int                                 // the total number of route IDs we reserve from the routers
	rcM   routerclient.Map                    // map of router clients
	rec   map[cipher.PubKey]uint8             // this records the number of expected rules per visor PK
	ids   map[cipher.PubKey][]routing.RouteID // this records the obtained rules per visor PK
	mx    sync.Mutex
}

// NewIDReserver creates a new route ID reserver from a dialer and a slice of paths.
// The exact number of route IDs to reserve from each router is determined from the slice of paths.
func NewIDReserver(ctx context.Context, dialer network.Dialer, paths [][]routing.Hop) (IDReserver, error) {
	var total int // the total number of route IDs we reserve from the routers

	// Prepare 'rec': A map representing the number of expected rules per visor PK.
	rec := make(map[cipher.PubKey]uint8)
	for _, hops := range paths {
		if len(hops) == 0 {
			continue
		}
		rec[hops[0].From]++
		for _, hop := range hops {
			rec[hop.To]++
		}
		total += len(hops) + 1
	}

	// Prepare 'clients': A map of router clients.
	pks := make([]cipher.PubKey, 0, len(rec))
	for pk := range rec {
		pks = append(pks, pk)
	}
	clients, err := routerclient.MakeMap(ctx, dialer, pks)
	if err != nil {
		return nil, fmt.Errorf("a dial attempt failed with: %v", err)
	}

	// Return result.
	return &idReserver{
		total: total,
		rcM:   clients,
		rec:   rec,
		ids:   make(map[cipher.PubKey][]routing.RouteID, total),
	}, nil
}

func (idr *idReserver) ReserveIDs(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, len(idr.rec))
	defer close(errCh)

	for pk, n := range idr.rec {
		go func(pk cipher.PubKey, n uint8) {
			rtIDs, err := idr.rcM.Client(pk).ReserveIDs(ctx, n)
			if err != nil {
				cancel()
				errCh <- fmt.Errorf("reserve routeID from %s failed: %w", pk, err)
				return
			}
			idr.mx.Lock()
			idr.ids[pk] = rtIDs
			idr.mx.Unlock()
			errCh <- nil
		}(pk, n)
	}

	return firstError(len(idr.rec), errCh)
}

func (idr *idReserver) PopID(pk cipher.PubKey) (routing.RouteID, bool) {
	idr.mx.Lock()
	defer idr.mx.Unlock()

	ids, ok := idr.ids[pk]
	if !ok || len(ids) == 0 {
		return 0, false
	}

	idr.ids[pk] = ids[1:]

	return ids[0], true
}

func (idr *idReserver) TotalIDs() int {
	return idr.total
}

func (idr *idReserver) Client(pk cipher.PubKey) *routerclient.Client {
	return idr.rcM[pk]
}

func (idr *idReserver) String() string {
	idr.mx.Lock()
	defer idr.mx.Unlock()
	b, _ := json.MarshalIndent(idr.ids, "", "\t") //nolint:errcheck
	return string(b)
}

func (idr *idReserver) Close() error {
	if errs := idr.rcM.CloseAll(); errs != nil {
		return fmt.Errorf("router client map closed with errors: %v", errs)
	}
	return nil
}

func firstError(n int, errCh <-chan error) error {
	var firstErr error
	for i := 0; i < n; i++ {
		if err := <-errCh; firstErr == nil && err != nil {
			firstErr = err
		}
	}
	return firstErr
}
