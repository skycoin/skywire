// Package visor pkg/visor/reach_card.go c3-vis-core
//
// Reach cards: a visor tells peers itself how to dial it. It serves the
// records the address resolver holds for it, signed, on GET /reach over dmsg
// and skynet, and a dialer asks the peer for its card before it asks the
// resolver. The resolver is left telling a visor what to put in its card, and
// answering for peers that do not serve one.
package visor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
	types "github.com/skycoin/skywire/pkg/transport/types"
	"github.com/skycoin/skywire/pkg/visor/logserver"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

const (
	// reachCardKeep is how long a fetched card answers dials to its visor.
	reachCardKeep = 2 * time.Minute
	// reachCardMissKeep is how long a visor that served no card is not asked again.
	reachCardMissKeep = 5 * time.Minute
	// reachCardFetchTimeout bounds asking a peer for its card.
	reachCardFetchTimeout = 3 * time.Second
	// reachCardBodyKeep is how long this visor reuses its own signed card.
	reachCardBodyKeep = 30 * time.Second
)

// reachCards holds this visor's own card and the cards it fetched.
type reachCards struct {
	mu    sync.Mutex
	own   []byte
	ownAt time.Time
	// binds is the last bound payload per type, to notice an address change.
	binds map[string]string
	// warming is the peers whose card a background fetch is getting.
	warming map[cipher.PubKey]struct{}
	peers   map[cipher.PubKey]reachCardEntry
	hits    atomic.Uint64
	misses  atomic.Uint64
	fetches atomic.Uint64
}

type reachCardEntry struct {
	records map[types.Type]addrresolver.VisorData // nil when the peer served none
	at      time.Time
}

// advertisesReach is the one lever for being dialable by strangers: a visor
// that keeps out of the address resolver (ar_transport_limit < 0) serves no
// card either.
func (v *Visor) advertisesReach() bool {
	return v.conf != nil && (v.conf.Transport == nil || v.conf.Transport.ARTransportLimit >= 0)
}

// ReachCardBody is this visor's signed reach card, built from its own address
// resolver records.
func (v *Visor) ReachCardBody() ([]byte, error) {
	if !v.advertisesReach() {
		return nil, logserver.ErrNotReachable
	}
	v.reach.mu.Lock()
	if v.reach.own != nil && time.Since(v.reach.ownAt) < reachCardBodyKeep {
		body := v.reach.own
		v.reach.mu.Unlock()
		return body, nil
	}
	v.reach.mu.Unlock()

	if v.arSelf.age() > arSelfMaxAge {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		v.arSelfRefreshOnce(ctx, nil)
		cancel()
	}
	records := make(map[types.Type]addrresolver.VisorData, len(arSelfTypes))
	for _, t := range arSelfTypes {
		if d, ok := v.arSelf.get(t); ok {
			records[types.Type(t)] = d
		}
	}
	if len(records) == 0 {
		return nil, fmt.Errorf("no address resolver records yet")
	}
	card, err := addrresolver.NewReachCard(v.conf.PK, v.conf.SK, time.Now(), records)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(card)
	if err != nil {
		return nil, err
	}
	v.reach.mu.Lock()
	v.reach.own, v.reach.ownAt = body, time.Now()
	v.reach.mu.Unlock()
	return body, nil
}

// resolveFast answers an address lookup from the peer's own reach card, then
// from the resolver's bindings feed, before the resolver is asked.
//
// While few peers serve a card, waiting on a fetch before every first dial
// would slow dials the feed answers at once. So a held card answers first;
// else the feed answers and the card is fetched in the background for the
// next dial; else the dial waits on the card before the resolver is asked.
func (v *Visor) resolveFast(ctx context.Context, tType string, pk cipher.PubKey) (addrresolver.VisorData, bool) {
	if v.conf == nil || pk == v.conf.PK {
		return v.arResolveFast(ctx, tType, pk)
	}
	t := types.NormalizeType(types.Type(tType))
	if e, fresh := v.heldReachCard(pk); fresh {
		if d, ok := e.records[t]; ok {
			v.reach.hits.Add(1)
			return d, true
		}
		v.reach.misses.Add(1)
		return v.arResolveFast(ctx, tType, pk)
	}
	if d, ok := v.arResolveFast(ctx, tType, pk); ok {
		v.warmReachCard(pk)
		return d, true
	}
	return v.reachCardRecord(ctx, t, pk)
}

// heldReachCard returns pk's cached card entry and whether it is still fresh.
func (v *Visor) heldReachCard(pk cipher.PubKey) (reachCardEntry, bool) {
	v.reach.mu.Lock()
	e, ok := v.reach.peers[pk]
	v.reach.mu.Unlock()
	age := time.Since(e.at)
	return e, ok && ((e.records != nil && age < reachCardKeep) || (e.records == nil && age < reachCardMissKeep))
}

func (v *Visor) storeReachCard(pk cipher.PubKey, e reachCardEntry) {
	v.reach.mu.Lock()
	if v.reach.peers == nil {
		v.reach.peers = make(map[cipher.PubKey]reachCardEntry)
	}
	v.reach.peers[pk] = e
	delete(v.reach.warming, pk)
	v.reach.mu.Unlock()
}

// warmReachCard fetches pk's card in the background, once at a time.
func (v *Visor) warmReachCard(pk cipher.PubKey) {
	v.reach.mu.Lock()
	if v.reach.warming == nil {
		v.reach.warming = make(map[cipher.PubKey]struct{})
	}
	if _, busy := v.reach.warming[pk]; busy {
		v.reach.mu.Unlock()
		return
	}
	v.reach.warming[pk] = struct{}{}
	v.reach.mu.Unlock()
	go func() {
		now := time.Now()
		v.storeReachCard(pk, reachCardEntry{records: v.fetchReachCard(context.Background(), pk), at: now})
	}()
}

// reachCardRecord returns pk's record for tType from its card, fetching the
// card when none is held.
func (v *Visor) reachCardRecord(ctx context.Context, tType types.Type, pk cipher.PubKey) (addrresolver.VisorData, bool) {
	if v.conf == nil || pk == v.conf.PK {
		return addrresolver.VisorData{}, false
	}
	e, fresh := v.heldReachCard(pk)
	if !fresh {
		e = reachCardEntry{records: v.fetchReachCard(ctx, pk), at: time.Now()}
		v.storeReachCard(pk, e)
	}
	d, ok := e.records[types.NormalizeType(tType)]
	if ok {
		v.reach.hits.Add(1)
	} else {
		v.reach.misses.Add(1)
	}
	return d, ok
}

// fetchReachCard asks pk for its card over skynet when a transport reaches
// it, else dmsg. It returns nil when there is none or it does not verify.
func (v *Visor) fetchReachCard(ctx context.Context, pk cipher.PubKey) map[types.Type]addrresolver.VisorData {
	if v.dmsgC == nil {
		return nil // not up yet; the resolver answers
	}
	v.reach.fetches.Add(1)
	ctx, cancel := context.WithTimeout(ctx, reachCardFetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+pk.Hex()+":80"+addrresolver.ReachPath, nil)
	if err != nil {
		return nil
	}
	resp, err := (&http.Client{Transport: v.dmsgHTTPTransport()}).Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var card addrresolver.ReachCard
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&card); err != nil {
		return nil
	}
	records, err := card.Verify(pk)
	if err != nil {
		return nil
	}
	return records
}

func (v *Visor) reachCardStats() *visorapi.DiagReachCards {
	v.reach.mu.Lock()
	held := 0
	for _, e := range v.reach.peers {
		if e.records != nil {
			held++
		}
	}
	v.reach.mu.Unlock()
	return &visorapi.DiagReachCards{
		Advertised: v.advertisesReach(),
		Held:       held,
		Fetches:    v.reach.fetches.Load(),
		Hits:       v.reach.hits.Load(),
		Misses:     v.reach.misses.Load(),
	}
}

// noteReachBind runs on every address resolver bind. When a type's bound
// addresses change, as on a move to another network, the visor's own records
// and card are dropped, so the next card is rebuilt from the resolver's view.
func (v *Visor) noteReachBind(netType string, payload addrresolver.LocalAddresses) {
	b, err := json.Marshal(payload)
	if err != nil {
		return
	}
	v.reach.mu.Lock()
	if v.reach.binds == nil {
		v.reach.binds = make(map[string]string)
	}
	changed := v.reach.binds[netType] != string(b)
	v.reach.binds[netType] = string(b)
	if changed {
		v.reach.own = nil
	}
	v.reach.mu.Unlock()
	if changed {
		v.arSelf.invalidate()
	}
}
