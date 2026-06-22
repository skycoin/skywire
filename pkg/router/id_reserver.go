//go:build !tinygo

// Package router pkg/router/id_reserver.go
package router

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport/network"
)

// ErrNoKey is returned when key is not found.
var ErrNoKey = errors.New("id reservoir has no key")

//go:generate mockery --name IDReserver --case underscore --inpackage

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
	Client(pk cipher.PubKey) *Client

	// ReturnToPool returns connections to a pool instead of closing them.
	ReturnToPool(pool *ClientPool)
}

type idReserver struct {
	total  int                                 // the total number of route IDs we reserve from the routers
	rcM    Map                                 // map of router clients
	rec    map[cipher.PubKey]uint8             // this records the number of expected rules per visor PK
	ids    map[cipher.PubKey][]routing.RouteID // this records the obtained rules per visor PK
	pool   *ClientPool                         // non-nil → re-dial a stale conn through the pool
	dialer network.Dialer                      // fresh-dial source when pool is nil
	mx     sync.Mutex
}

// NewIDReserver creates a new route ID reserver from a dialer and a slice of paths.
// The exact number of route IDs to reserve from each router is determined from the slice of paths.
// If pool is non-nil, connections are borrowed from the pool instead of dialed fresh.
func NewIDReserver(ctx context.Context, dialer network.Dialer, pool *ClientPool, paths [][]routing.Hop) (IDReserver, error) {
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
	var clients Map
	var err error
	if pool != nil {
		clients, err = MakePooledMap(ctx, pool, pks)
	} else {
		clients, err = MakeMap(ctx, dialer, pks)
	}
	if err != nil {
		// %w (not %v) preserves the underlying *router.DialError in the
		// chain so the setupmetrics collector can identify which hop
		// actually failed and avoid penalizing the destination for a
		// source-side unreachability.
		return nil, fmt.Errorf("a dial attempt failed with: %w", err)
	}

	// Return result.
	return &idReserver{
		total:  total,
		rcM:    clients,
		rec:    rec,
		ids:    make(map[cipher.PubKey][]routing.RouteID, total),
		pool:   pool,
		dialer: dialer,
	}, nil
}

// isPooledConnShutdown reports whether err is net/rpc's "connection is shut
// down" — returned when a call is made on an already-closed rpc.Client, the
// signature of a stale connection handed back by the pool.
func isPooledConnShutdown(err error) bool {
	return err != nil && strings.Contains(err.Error(), "connection is shut down")
}

// redial discards a dead client and dials a fresh one for pk, replacing it in
// the client map so the later rule-install reuses the live connection. Returns
// nil when no fresh connection can be established (caller surfaces the original
// error). See ReserveIDs for why this is needed.
func (idr *idReserver) redial(ctx context.Context, pk cipher.PubKey, dead *Client) *Client {
	if dead != nil {
		_ = dead.Close() //nolint:errcheck,gosec // never return a corpse to the pool
	}
	var (
		fresh *Client
		err   error
	)
	switch {
	case idr.pool != nil:
		// The pool entry for pk was consumed at build time, so Get dials fresh.
		fresh, err = idr.pool.Get(ctx, pk)
	case idr.dialer != nil:
		fresh, err = NewClient(ctx, idr.dialer, pk)
	default:
		return nil
	}
	if err != nil || fresh == nil {
		return nil
	}
	idr.mx.Lock()
	idr.rcM[pk] = fresh
	idr.mx.Unlock()
	return fresh
}

func (idr *idReserver) ReserveIDs(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	errCh := make(chan error, len(idr.rec))
	defer close(errCh)

	for pk, n := range idr.rec {
		go func(pk cipher.PubKey, n uint8) {
			// Read under idr.mx: a sibling goroutine's redial() writes rcM[pk]
			// under the same lock, so an unlocked read here is a data race on
			// the shared map (different keys still touch the map's internals).
			idr.mx.Lock()
			client := idr.rcM.Client(pk)
			idr.mx.Unlock()
			if client == nil {
				// NOTE: deliberately do NOT cancel the shared ctx on a single
				// hop's failure. cancel() here propagated to every sibling
				// goroutine still blocked in client.ReserveIDs(ctx); their
				// Client.call() reacts to ctx.Done() by closing the underlying
				// (pooled) DMSG stream — RST-ing perfectly healthy
				// intermediates mid-reservation and turning one hop's blip into
				// a whole-route setup failure (the "RST race" that made
				// multi-hop / mux route setup fail at a high rate). Each hop is
				// independently bounded by its own per-call rpcDeadline, and
				// firstError below already waits for all N goroutines and
				// returns the first error — so a genuinely dead hop still fails
				// the route, without resetting the healthy hops.
				errCh <- fmt.Errorf("reserve routeID from %s failed: no client available", pk)
				return
			}
			rtIDs, err := client.ReserveIDs(ctx, n)
			if isPooledConnShutdown(err) {
				// Stale pooled connection. ClientPool.Get's liveness check
				// (client_pool.go) only discards conns idle past the stream
				// read deadline — so a conn that died for any OTHER reason (the
				// intermediate restarted, its dmsg session/transport dropped)
				// is handed back as a corpse and this first call returns
				// "connection is shut down". Re-dial fresh ONCE and retry: a
				// reachable hop succeeds on the retry, a genuinely-dead one
				// still fails below. This was the dominant route-setup failure
				// (id_reservation, ~59% on a live RSN) — a single bad pooled
				// conn was failing the whole reservation with no retry.
				if fresh := idr.redial(ctx, pk, client); fresh != nil {
					rtIDs, err = fresh.ReserveIDs(ctx, n)
				}
			}
			if err != nil {
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

func (idr *idReserver) Client(pk cipher.PubKey) *Client {
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

// ReturnToPool returns all connections to the pool for reuse instead of closing them.
func (idr *idReserver) ReturnToPool(pool *ClientPool) {
	idr.rcM.ReturnToPool(pool)
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
