package dht

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
)

// ErrNotFound is returned when a value lookup finds no item.
var ErrNotFound = errors.New("dht: item not found")

// Node is a Kademlia DHT node.
type Node struct {
	pk              cipher.PubKey
	sk              cipher.SecKey
	id              NodeID
	rt              *RoutingTable
	store           *Store
	tp              Transport
	extraTransports []Transport // additional transports (e.g., transport-layer DHT)
	log             *logging.Logger
	cfg             Config
	ctx             context.Context
	cancel          context.CancelFunc
	wg              sync.WaitGroup
	// noDHT caches peers that failed DHT dial (port 100 not listening).
	// Skips them for dhtNegCacheTTL to avoid wasting DialStream attempts.
	noDHTMu sync.RWMutex
	noDHT   map[cipher.PubKey]time.Time
}

const dhtNegCacheTTL = 10 * time.Minute

// New creates a new DHT node. Call Start to begin serving.
func New(cfg Config, pk cipher.PubKey, sk cipher.SecKey, tp Transport, log *logging.Logger) *Node {
	cfg.SetDefaults()
	id := NodeIDFromPubKey(pk)
	store := NewStore(cfg.MaxItems, cfg.ItemTTL)
	trust := NewTrustPolicy(cfg.WhitelistedPKs, cfg.TrustedPKs)
	store.SetTrustPolicy(trust, cfg.PublicPoolSize, cfg.RateLimitPerPK)
	return &Node{
		pk:    pk,
		sk:    sk,
		id:    id,
		rt:    NewRoutingTable(id),
		store: store,
		tp:    tp,
		log:   log,
		cfg:   cfg,
	}
}

// ID returns the node's DHT ID.
func (n *Node) ID() NodeID { return n.id }

// PK returns the node's public key.
func (n *Node) PK() cipher.PubKey { return n.pk }

// RoutingTable returns the node's routing table (for inspection/testing).
func (n *Node) RoutingTable() *RoutingTable { return n.rt }

// Store returns the node's item store (for inspection/testing).
func (n *Node) Store() *Store { return n.store }

// AddTransport adds a secondary transport for DHT communication.
// The node will listen on it and use it for dialing alongside the primary transport.
// Must be called after Start.
func (n *Node) AddTransport(tp Transport) {
	n.extraTransports = append(n.extraTransports, tp)
	// Start serving on the new transport.
	lis, err := tp.Listen()
	if err != nil {
		n.log.WithError(err).Warn("DHT: failed to listen on extra transport")
		return
	}
	n.wg.Add(1)
	go func() {
		defer n.wg.Done()
		n.serve(lis)
	}()
	n.log.Info("DHT: extra transport listener started")
}

// Start begins listening for RPC requests and bootstraps the routing table.
func (n *Node) Start(ctx context.Context) error {
	n.ctx, n.cancel = context.WithCancel(ctx)

	lis, err := n.tp.Listen()
	if err != nil {
		return fmt.Errorf("dht: listen: %w", err)
	}

	// Serve incoming RPCs.
	n.wg.Add(1)
	go func() {
		defer n.wg.Done()
		n.serve(lis)
	}()

	// Periodic maintenance: expire items, refresh buckets.
	n.wg.Add(1)
	go func() {
		defer n.wg.Done()
		n.maintenanceLoop()
	}()

	// Bootstrap asynchronously with periodic retry. The first attempt
	// runs immediately; if no peers are found, it retries every 30 seconds
	// until at least one peer is in the routing table.
	if len(n.cfg.BootstrapPKs) > 0 {
		n.wg.Add(1)
		go func() {
			defer n.wg.Done()
			n.bootstrapLoop()
		}()
	}

	return nil
}

// Stop shuts down the DHT node.
func (n *Node) Stop() error {
	if n.cancel != nil {
		n.cancel()
	}
	n.wg.Wait()
	return nil
}

// Put publishes a mutable item to the DHT.
func (n *Node) Put(ctx context.Context, value []byte, seq uint64, salt []byte) error {
	if len(value) > MaxValueSize {
		return ErrValueTooLarge
	}
	if len(salt) > MaxSaltSize {
		return ErrSaltTooLarge
	}

	item := MutableItem{
		K:    n.pk,
		Seq:  seq,
		V:    value,
		Salt: salt,
	}
	if err := item.Sign(n.sk); err != nil {
		return err
	}

	// Store locally (may return ErrSeqNotMonotonic on re-publish, which is fine).
	_ = n.store.Put(item) //nolint:errcheck

	// Find K closest nodes to the target and push the item.
	target := item.Target()
	closest, _, err := n.iterativeLookup(ctx, target, false)
	if err != nil {
		return fmt.Errorf("dht: put lookup: %w", err)
	}

	var putErrors int
	for _, p := range closest {
		if err := n.rpcPutValue(ctx, p, item); err != nil {
			n.log.WithError(err).WithField("peer", p.PK.String()[:8]).Debug("PutValue failed")
			putErrors++
		}
	}

	if len(closest) == 0 {
		n.log.Debug("Put stored locally only (no remote peers found)")
	} else if putErrors == len(closest) {
		return fmt.Errorf("dht: put failed on all %d peers", len(closest))
	}

	return nil
}

// PutSigned publishes a pre-signed mutable item to the DHT on behalf of
// another publisher. The item must already have K (publisher PK), Seq,
// Sig, V, and Salt set. The signature is verified against K — the caller
// does NOT need the publisher's secret key.
//
// Use case: deployment services mirroring entries from old visors that
// don't dual-write to the DHT. The entry's own signature (created by
// the visor) proves authenticity; the DHT node just distributes it.
func (n *Node) PutSigned(ctx context.Context, item MutableItem) error {
	if err := item.Verify(); err != nil {
		return fmt.Errorf("dht: invalid pre-signed item: %w", err)
	}

	// Store locally.
	_ = n.store.Put(item) //nolint:errcheck

	// Find K closest nodes to the target and push the item.
	target := item.Target()
	closest, _, err := n.iterativeLookup(ctx, target, false)
	if err != nil {
		return fmt.Errorf("dht: put-signed lookup: %w", err)
	}

	var putErrors int
	for _, p := range closest {
		if err := n.rpcPutValue(ctx, p, item); err != nil {
			putErrors++
		}
	}

	if len(closest) > 0 && putErrors == len(closest) {
		return fmt.Errorf("dht: put-signed failed on all %d peers", len(closest))
	}

	return nil
}

// Get retrieves a mutable item from the DHT by publisher pubkey and salt.
func (n *Node) Get(ctx context.Context, pk cipher.PubKey, salt []byte) (*MutableItem, error) {
	target := (&MutableItem{K: pk, Salt: salt}).Target()

	// Check local store first.
	if item := n.store.Get(target); item != nil {
		return item, nil
	}

	// Iterative value lookup.
	_, item, err := n.iterativeLookup(ctx, target, true)
	if err != nil {
		return nil, err
	}
	if item == nil {
		return nil, ErrNotFound
	}

	// Verify and cache locally.
	if err := item.Verify(); err != nil {
		return nil, err
	}
	_ = n.store.Put(*item) //nolint:errcheck

	return item, nil
}

// --- RPC client methods ---

// rpcCtx wraps a context with the per-RPC timeout.
func rpcCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, rpcTimeout)
}

// dial tries the primary transport, then extra transports.
// Skips peers in the negative cache (failed DHT dial recently).
func (n *Node) dial(ctx context.Context, pk cipher.PubKey) (io.ReadWriteCloser, error) {
	// Check negative cache.
	n.noDHTMu.RLock()
	if t, ok := n.noDHT[pk]; ok && time.Since(t) < dhtNegCacheTTL {
		n.noDHTMu.RUnlock()
		return nil, fmt.Errorf("dht: peer %s cached as non-DHT", pk.String()[:8])
	}
	n.noDHTMu.RUnlock()

	// Prefer p2p transports (STCPR/SUDPH) over DMSG — direct, lower
	// latency, doesn't burden DMSG servers. Fall back to DMSG only
	// when no direct transport exists.
	var err error
	for _, tp := range n.extraTransports {
		conn, err2 := tp.Dial(ctx, pk)
		if err2 == nil {
			return conn, nil
		}
		if err == nil {
			err = err2
		}
	}
	conn, dmsgErr := n.tp.Dial(ctx, pk)
	if dmsgErr == nil {
		return conn, nil
	}
	if err == nil {
		err = dmsgErr
	}

	// All transports failed — cache as non-DHT peer.
	n.noDHTMu.Lock()
	if n.noDHT == nil {
		n.noDHT = make(map[cipher.PubKey]time.Time)
	}
	n.noDHT[pk] = time.Now()
	n.noDHTMu.Unlock()

	return nil, err
}

func (n *Node) rpcFindNode(ctx context.Context, p Peer, target NodeID) (*FindNodeResponse, error) {
	ctx, cancel := rpcCtx(ctx)
	defer cancel()

	conn, err := n.dial(ctx, p.PK)
	if err != nil {
		return nil, err
	}
	defer conn.Close() //nolint:errcheck,gosec

	req := FindNodeRequest{SenderID: n.id, SenderPK: n.pk, Target: target}
	var resp FindNodeResponse
	if err := rpcCall(ctx, conn, methodFindNode, req, &resp); err != nil {
		return nil, err
	}

	// Update routing table with the peer we successfully contacted.
	n.rt.Update(Peer{ID: p.ID, PK: p.PK, LastSeen: time.Now()})

	return &resp, nil
}

func (n *Node) rpcGetValue(ctx context.Context, p Peer, target NodeID) (*GetValueResponse, error) {
	ctx, cancel := rpcCtx(ctx)
	defer cancel()

	conn, err := n.dial(ctx, p.PK)
	if err != nil {
		return nil, err
	}
	defer conn.Close() //nolint:errcheck,gosec

	req := GetValueRequest{SenderID: n.id, SenderPK: n.pk, Target: target}
	var resp GetValueResponse
	if err := rpcCall(ctx, conn, methodGetValue, req, &resp); err != nil {
		return nil, err
	}

	n.rt.Update(Peer{ID: p.ID, PK: p.PK, LastSeen: time.Now()})
	return &resp, nil
}

func (n *Node) rpcPutValue(ctx context.Context, p Peer, item MutableItem) error {
	ctx, cancel := rpcCtx(ctx)
	defer cancel()

	conn, err := n.dial(ctx, p.PK)
	if err != nil {
		return err
	}
	defer conn.Close() //nolint:errcheck,gosec

	req := PutValueRequest{SenderID: n.id, SenderPK: n.pk, Item: item}
	var resp PutValueResponse
	if err := rpcCall(ctx, conn, methodPutValue, req, &resp); err != nil {
		return err
	}
	if !resp.Stored {
		return fmt.Errorf("dht: remote rejected put: %s", resp.Error)
	}

	n.rt.Update(Peer{ID: p.ID, PK: p.PK, LastSeen: time.Now()})
	return nil
}

func (n *Node) rpcPutMirror(ctx context.Context, p Peer, item MutableItem, target NodeID) error {
	ctx, cancel := rpcCtx(ctx)
	defer cancel()

	conn, err := n.dial(ctx, p.PK)
	if err != nil {
		return err
	}
	defer conn.Close() //nolint:errcheck,gosec

	req := PutValueRequest{SenderID: n.id, SenderPK: n.pk, Item: item, MirrorTarget: target}
	var resp PutValueResponse
	if err := rpcCall(ctx, conn, methodPutValue, req, &resp); err != nil {
		return err
	}
	return nil
}

// GetItemsFrom fetches a batch of items from a specific peer.
// Used for bulk sync from a full node.
func (n *Node) GetItemsFrom(ctx context.Context, pk cipher.PubKey, salt string, sinceSeq uint64, limit int) (*GetItemsResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second) // longer timeout for bulk data
	defer cancel()

	conn, err := n.dial(ctx, pk)
	if err != nil {
		return nil, err
	}
	defer conn.Close() //nolint:errcheck,gosec

	req := GetItemsRequest{
		SenderID: n.id,
		SenderPK: n.pk,
		Salt:     salt,
		SinceSeq: sinceSeq,
		Limit:    limit,
	}
	var resp GetItemsResponse
	if err := rpcCall(ctx, conn, methodGetItems, req, &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

func (n *Node) rpcPing(ctx context.Context, p Peer) error {
	ctx, cancel := rpcCtx(ctx)
	defer cancel()

	conn, err := n.dial(ctx, p.PK)
	if err != nil {
		return err
	}
	defer conn.Close() //nolint:errcheck,gosec

	req := PingRequest{SenderID: n.id, SenderPK: n.pk}
	var resp PingResponse
	return rpcCall(ctx, conn, methodPing, req, &resp)
}

// --- Server ---

// maxConcurrentDHTHandlers limits in-flight RPC handlers.
const maxConcurrentDHTHandlers = 128

func (n *Node) serve(lis Listener) {
	// Close the listener when context is canceled so Accept unblocks.
	go func() {
		<-n.ctx.Done()
		lis.Close() //nolint:errcheck,gosec
	}()
	sem := make(chan struct{}, maxConcurrentDHTHandlers)
	for {
		conn, remotePK, err := lis.Accept()
		if err != nil {
			return // listener closed or context canceled
		}
		sem <- struct{}{}
		n.wg.Add(1)
		go func() {
			defer n.wg.Done()
			defer func() { <-sem }()
			n.handleConn(conn, remotePK)
		}()
	}
}

func (n *Node) handleConn(conn io.ReadWriteCloser, remotePK cipher.PubKey) {
	defer conn.Close() //nolint:errcheck,gosec

	method, data, err := readMsg(conn)
	if err != nil {
		return
	}

	// Update routing table with the caller.
	remoteID := NodeIDFromPubKey(remotePK)
	n.rt.Update(Peer{ID: remoteID, PK: remotePK, LastSeen: time.Now()})

	switch method {
	case methodPing:
		resp := PingResponse{ResponderID: n.id, ResponderPK: n.pk}
		writeMsg(conn, methodPing, resp) //nolint:errcheck,gosec

	case methodFindNode:
		var req FindNodeRequest
		if json.Unmarshal(data, &req) != nil {
			return
		}
		closest := n.rt.FindClosest(req.Target, K)
		resp := FindNodeResponse{Closest: closest}
		writeMsg(conn, methodFindNode, resp) //nolint:errcheck,gosec

	case methodGetValue:
		var req GetValueRequest
		if json.Unmarshal(data, &req) != nil {
			return
		}
		item := n.store.Get(req.Target)
		closest := n.rt.FindClosest(req.Target, K)
		resp := GetValueResponse{Item: item, Closest: closest}
		writeMsg(conn, methodGetValue, resp) //nolint:errcheck,gosec

	case methodPutValue:
		var req PutValueRequest
		if json.Unmarshal(data, &req) != nil {
			return
		}
		var putErr error
		if !req.MirrorTarget.IsZero() {
			// Mirrored entry: store under the explicit target.
			n.store.PutMirror(req.MirrorTarget, req.Item)
		} else {
			putErr = n.store.Put(req.Item)
		}
		resp := PutValueResponse{Stored: putErr == nil}
		if putErr != nil {
			resp.Error = putErr.Error()
		}
		writeMsg(conn, methodPutValue, resp) //nolint:errcheck,gosec

	case methodGetItems:
		var req GetItemsRequest
		if json.Unmarshal(data, &req) != nil {
			return
		}
		n.rt.Update(Peer{ID: req.SenderID, PK: req.SenderPK})
		items, targets, hasMore := n.store.GetItems(req.Salt, req.SinceSeq, req.Limit)
		resp := GetItemsResponse{Items: items, Targets: targets, HasMore: hasMore}
		writeMsg(conn, methodGetItems, resp) //nolint:errcheck,gosec
	}
}

// --- Bootstrap & Maintenance ---

// bootstrapLoop tries to connect to bootstrap peers. Retries every 30 seconds
// until at least one peer is found, then retries every 5 minutes to maintain
// connectivity. This handles the case where bootstrap peers are temporarily
// unreachable (e.g., deployment restart, DMSG server not ready yet).
func (n *Node) bootstrapLoop() {
	const retryFast = 30 * time.Second
	const retrySlow = 5 * time.Minute

	for {
		found := n.bootstrapOnce()

		if found > 0 {
			n.log.WithField("peers", found).Info("DHT bootstrap succeeded")
			// Do a self-lookup to populate nearby buckets.
			if _, _, err := n.iterativeLookup(n.ctx, n.id, false); err != nil {
				n.log.WithError(err).Debug("Bootstrap self-lookup failed")
			}
		}

		// Choose retry interval: fast if no peers, slow if we have some.
		interval := retryFast
		if n.rt.Size() > 0 {
			interval = retrySlow
		}

		select {
		case <-n.ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

// bootstrapOnce attempts to ping each bootstrap peer once. Returns the
// number of peers successfully added to the routing table.
func (n *Node) bootstrapOnce() int {
	found := 0
	for _, pk := range n.cfg.BootstrapPKs {
		if pk == n.pk {
			continue
		}
		p := Peer{ID: NodeIDFromPubKey(pk), PK: pk}
		if err := n.rpcPing(n.ctx, p); err != nil {
			n.log.WithError(err).WithField("pk", pk.String()[:8]).Debug("Bootstrap ping failed")
			continue
		}
		n.rt.Update(p)
		found++
	}
	return found
}

func (n *Node) maintenanceLoop() {
	ticker := time.NewTicker(n.cfg.RefreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-n.ctx.Done():
			return
		case <-ticker.C:
			removed := n.store.ExpireSweep()
			if removed > 0 {
				n.log.WithField("removed", removed).Debug("Expired DHT items")
			}
		}
	}
}
