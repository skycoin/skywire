package setup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"sync"
	"time"

	"github.com/skycoin/dmsg/cipher"

	"github.com/skycoin/skywire/pkg/router/routerclient"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/snet"
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

	// ReserveIDs gets random route IDs.
	// It uses an internal map to know how many ids to reserve from each router.
	ReserveIDs()

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
	rnd   *rand.Rand
	mx    sync.Mutex
}

// NewIDReserver creates a new route ID reserver from a dialer and a slice of paths.
// The exact number of route IDs to reserve from each router is determined from the slice of paths.
func NewIDReserver(ctx context.Context, dialer snet.Dialer, paths [][]routing.Hop) (IDReserver, error) {
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
		rnd:   rand.New(rand.NewSource(time.Now().UnixNano())), //nolint:gosec
		ids:   make(map[cipher.PubKey][]routing.RouteID, total),
	}, nil
}

func (idr *idReserver) ReserveIDs() {
	ids := make(map[cipher.PubKey][]routing.RouteID)
	for pk, n := range idr.rec {
		ids[pk] = idr.genIDs(int(n))
	}

	idr.mx.Lock()
	idr.ids = ids
	idr.mx.Unlock()
}

func (idr *idReserver) genIDs(count int) []routing.RouteID {
	ids := make([]routing.RouteID, 0, count)
	for i := 0; i < count; i++ {
		ids = append(ids, routing.RouteID(idr.rnd.Uint32()))
	}

	return ids
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
