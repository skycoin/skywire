package dht

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
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

		// Full nodes also bulk-pull the data set from each bootstrap
		// peer at startup and periodically thereafter, so that "full
		// node" actually means "holds the network's data" rather than
		// "would store it if anyone Put through me." Without this loop
		// a freshly-restarted full node sat near-empty until passive
		// peer Puts trickled in (observed in the field: ~87 items on a
		// dev visor while a long-lived hub had ~3500).
		if n.store.IsFullNode() {
			n.wg.Add(1)
			go func() {
				defer n.wg.Done()
				n.fullNodePullLoop()
			}()
		}
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
			n.log.WithError(err).WithField("peer", p.PK.String()).Debug("PutValue failed")
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

// dial tries p2p transports first, then falls back to DMSG.
// Skips peers in the negative cache (failed DHT dial recently).
func (n *Node) dial(ctx context.Context, pk cipher.PubKey) (io.ReadWriteCloser, error) {
	// Check negative cache.
	n.noDHTMu.RLock()
	if t, ok := n.noDHT[pk]; ok && time.Since(t) < dhtNegCacheTTL {
		n.noDHTMu.RUnlock()
		return nil, fmt.Errorf("dht: peer %s cached as non-DHT", pk.String())
	}
	n.noDHTMu.RUnlock()

	// Skip p2p transports for bootstrap peers (DMSG servers) — there
	// will never be a direct STCPR/SUDPH transport to them.
	isBootstrap := false
	for _, bpk := range n.cfg.BootstrapPKs {
		if bpk == pk {
			isBootstrap = true
			break
		}
	}

	// Prefer p2p transports (STCPR/SUDPH) over DMSG — direct, lower
	// latency, doesn't burden DMSG servers. Fall back to DMSG only
	// when no direct transport exists.
	var err error
	if !isBootstrap {
		for _, tp := range n.extraTransports {
			conn, err2 := tp.Dial(ctx, pk)
			if err2 == nil {
				return conn, nil
			}
			if err == nil {
				err = err2
			}
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

// rpcPutBatch sends many items+targets in a single RPC round-trip.
// Returns a count of items the peer reported as stored, plus a count
// of per-item errors (errors are only logged at debug; the call as a
// whole succeeds if the RPC round-trip completed). The peer-side
// MaxValueSize cap on the wire (4MB) is enforced by readMsg's
// length-bounded check; chunk large pushes if you can hit that.
func (n *Node) rpcPutBatch(ctx context.Context, p Peer, items []MutableItem, targets []NodeID) (stored int, errs int, err error) {
	if len(items) == 0 {
		return 0, 0, nil
	}
	ctx, cancel := rpcCtx(ctx)
	defer cancel()

	conn, err := n.dial(ctx, p.PK)
	if err != nil {
		return 0, 0, err
	}
	defer conn.Close() //nolint:errcheck,gosec

	req := PutBatchRequest{
		SenderID: n.id,
		SenderPK: n.pk,
		Items:    items,
		Targets:  targets,
	}
	var resp PutBatchResponse
	if err := rpcCall(ctx, conn, methodPutBatch, req, &resp); err != nil {
		return 0, 0, err
	}
	for i, ok := range resp.Stored {
		if ok {
			stored++
		} else {
			errs++
			if i < len(resp.Errors) && resp.Errors[i] != "" {
				n.log.WithField("peer", p.PK.String()).
					WithField("err", resp.Errors[i]).
					Debug("rpcPutBatch: item rejected")
			}
		}
	}
	n.rt.Update(Peer{ID: p.ID, PK: p.PK, LastSeen: time.Now()})
	return stored, errs, nil
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

// SyncFrom paginates GetItems from a single remote peer, mirroring each
// returned item into the local store under its server-supplied target
// key. Returns the count of items mirrored and the first call-level
// error encountered (partial progress is reported as a return value).
//
// Used by the manual `dht sync` RPC. The full-node pull loop uses
// Reconcile, which is SyncFrom plus a write-back phase.
func (n *Node) SyncFrom(ctx context.Context, remotePK cipher.PubKey, salt string) (int, error) {
	stored, _, err := n.syncFromCollect(ctx, remotePK, salt)
	return stored, err
}

// syncFromCollect is SyncFrom that also returns the set of target keys
// seen on the remote, for callers that need to compute a delta.
func (n *Node) syncFromCollect(ctx context.Context, remotePK cipher.PubKey, salt string) (int, map[NodeID]struct{}, error) {
	stored := 0
	seen := make(map[NodeID]struct{})
	var cursor uint64
	for {
		resp, err := n.GetItemsFrom(ctx, remotePK, salt, cursor, 0)
		if err != nil {
			return stored, seen, err
		}
		var maxSeq uint64
		for i, item := range resp.Items {
			// PutMirror with the server's target key. Mirrored items
			// have item.K = mirror's PK (not subject), so item.Target()
			// would point to the wrong storage slot.
			if i < len(resp.Targets) {
				seen[resp.Targets[i]] = struct{}{}
				n.store.PutMirror(resp.Targets[i], item)
				stored++
			} else {
				// Old servers don't send targets — fall back to the
				// item's own (possibly self-keyed) target.
				if putErr := n.store.Put(item); putErr == nil {
					seen[item.Target()] = struct{}{}
					stored++
				}
			}
			if item.Seq > maxSeq {
				maxSeq = item.Seq
			}
		}
		if !resp.HasMore || len(resp.Items) == 0 || maxSeq <= cursor {
			break
		}
		cursor = maxSeq
	}
	return stored, seen, nil
}

// reconcile pulls from a remote peer (like SyncFrom) and then pushes
// back any items in our store that the remote didn't report. The push
// phase is what makes full-node-to-full-node coverage actually
// converge: with pull alone, each full node ends up with the union of
// every other full node's data, but the source peers themselves never
// see what they were missing.
//
// Lowercase by design: callers must verify that remotePK is a full
// node before invoking, since the receiver-side PutMirror handler
// stores anything we push without distance/admission gating. The only
// in-tree caller (fullNodePullOnce) iterates BootstrapPKs, which the
// deployment guarantees are DHT full nodes; other callers should
// satisfy the same invariant.
//
// Returns (pulled, pushed) item counts plus any error from either
// phase. Push errors are aggregated as debug logs and don't fail the
// overall reconcile — best-effort.
func (n *Node) reconcile(ctx context.Context, remotePK cipher.PubKey, salt string) (pulled, pushed int, err error) {
	pulled, seen, err := n.syncFromCollect(ctx, remotePK, salt)
	if err != nil {
		return pulled, 0, err
	}

	// Push phase: paginate our local store and batch-push items whose
	// target keys weren't returned by the peer. Pagination uses the
	// same Seq cursor strategy as the pull phase to avoid the
	// 1000-item GetItems batch cap. Within each store page we also
	// chunk the wire payload so a single PutBatch never crosses the
	// 4MB rpcCall size limit.
	peerID := NodeIDFromPubKey(remotePK)
	peer := Peer{ID: peerID, PK: remotePK}
	const maxBatchBytes = 3 * 1024 * 1024 // headroom under readMsg's 4MB cap
	var cursor uint64
	for {
		items, targets, hasMore := n.store.GetItems(salt, cursor, 0)
		if len(items) == 0 {
			break
		}
		var maxSeq uint64
		batchItems := make([]MutableItem, 0, len(items))
		batchTargets := make([]NodeID, 0, len(items))
		batchBytes := 0
		flush := func() {
			if len(batchItems) == 0 {
				return
			}
			stored, _, pushErr := n.rpcPutBatch(ctx, peer, batchItems, batchTargets)
			if pushErr != nil {
				n.log.WithError(pushErr).
					WithField("peer", remotePK.String()).
					Debug("Reconcile: rpcPutBatch failed")
			}
			pushed += stored
			batchItems = batchItems[:0]
			batchTargets = batchTargets[:0]
			batchBytes = 0
		}
		for i, item := range items {
			if item.Seq > maxSeq {
				maxSeq = item.Seq
			}
			// Don't push items the peer already gave us back to them.
			if _, ok := seen[targets[i]]; ok {
				continue
			}
			// Estimate item wire size: V dominates, plus K (33), Sig (65),
			// Salt, and target (32). Round generously to stay safe.
			itemBytes := len(item.V) + len(item.Salt) + 200
			if batchBytes+itemBytes > maxBatchBytes && len(batchItems) > 0 {
				flush()
			}
			batchItems = append(batchItems, item)
			batchTargets = append(batchTargets, targets[i])
			batchBytes += itemBytes
		}
		flush()
		if !hasMore || maxSeq <= cursor {
			break
		}
		cursor = maxSeq
	}

	return pulled, pushed, nil
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

	case methodPutBatch:
		var req PutBatchRequest
		if json.Unmarshal(data, &req) != nil {
			return
		}
		n.rt.Update(Peer{ID: req.SenderID, PK: req.SenderPK})
		stored := make([]bool, len(req.Items))
		errs := make([]string, len(req.Items))
		for i := range req.Items {
			var hasTarget bool
			if i < len(req.Targets) && !req.Targets[i].IsZero() {
				hasTarget = true
			}
			if hasTarget {
				// PutMirror is void — verify happens inside; we
				// can't distinguish accepted from rejected, but a
				// fresh-or-newer item will always land. Treat as
				// accepted to keep the wire shape simple.
				n.store.PutMirror(req.Targets[i], req.Items[i])
				stored[i] = true
				continue
			}
			if putErr := n.store.Put(req.Items[i]); putErr != nil {
				errs[i] = putErr.Error()
			} else {
				stored[i] = true
			}
		}
		// Drop the all-empty error slice to keep small batches tiny.
		var hasErr bool
		for _, e := range errs {
			if e != "" {
				hasErr = true
				break
			}
		}
		resp := PutBatchResponse{Stored: stored}
		if hasErr {
			resp.Errors = errs
		}
		writeMsg(conn, methodPutBatch, resp) //nolint:errcheck,gosec
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
			n.log.WithError(err).WithField("pk", pk.String()).Debug("Bootstrap ping failed")
			continue
		}
		n.rt.Update(p)
		found++
	}
	return found
}

// fullNodePullLoop reconciles the data set with each bootstrap peer at
// startup, then re-runs on a long interval. Each pass pulls items the
// peer has that we don't AND pushes items we have that the peer
// doesn't, so the bootstrap full nodes converge to the same content
// over time instead of each keeping its own partial view.
//
// Failure mode: any individual peer call may fail (peer offline, slow,
// non-DHT). Errors are logged at Debug and the loop moves on to the
// next peer. The whole pass is "best effort" — we'd rather fill in
// from one peer than block on a dead one.
func (n *Node) fullNodePullLoop() {
	const (
		startupDelay   = 5 * time.Second
		retryInterval  = 1 * time.Hour
		perPeerTimeout = 5 * time.Minute
	)

	// Wait for bootstrap to ping at least once before our first pull;
	// dialing a peer that hasn't been pinged yet adds extra noDHT-cache
	// misses for no benefit.
	select {
	case <-n.ctx.Done():
		return
	case <-time.After(startupDelay):
	}

	for {
		n.fullNodePullOnce(perPeerTimeout)

		select {
		case <-n.ctx.Done():
			return
		case <-time.After(retryInterval):
		}
	}
}

// fullNodePullOnce reconciles with bootstrap peers AND any peers
// advertising themselves as full nodes (signed FullNodeAdvert in salt
// "fullnode"), with a per-peer timeout. The signed advert is what
// makes pushing to non-bootstrap peers safe: the peer explicitly
// claims it accepts the full-node responsibility of storing arbitrary
// mirror puts.
//
// Reconcile pulls items we don't have from the peer and pushes back
// items they don't have from us, so over time the full nodes
// converge. The dial layer already skips peers in the noDHT negative
// cache, so we just let Reconcile return an error and move on.
func (n *Node) fullNodePullOnce(perPeerTimeout time.Duration) {
	// Bootstrap PKs first (deployment-trusted full nodes), then
	// peer-advertised full nodes (signed advert verified by store
	// signature check at insertion time, freshness re-checked here).
	peers := make([]cipher.PubKey, 0, len(n.cfg.BootstrapPKs))
	seen := make(map[cipher.PubKey]struct{})
	for _, pk := range n.cfg.BootstrapPKs {
		if pk == n.pk {
			continue
		}
		if _, dup := seen[pk]; dup {
			continue
		}
		seen[pk] = struct{}{}
		peers = append(peers, pk)
	}
	advertised := FindAdvertisedFullNodes(n)
	for _, pk := range advertised {
		if pk == n.pk {
			continue
		}
		if _, dup := seen[pk]; dup {
			continue
		}
		seen[pk] = struct{}{}
		peers = append(peers, pk)
	}

	totalPulled := 0
	totalPushed := 0
	tried := 0
	for _, pk := range peers {
		tried++
		ctx, cancel := context.WithTimeout(n.ctx, perPeerTimeout)
		// Empty salt => all salts.
		pulled, pushed, err := n.reconcile(ctx, pk, "")
		cancel()
		if err != nil {
			n.log.WithError(err).WithField("peer", pk.String()).
				Debug("Full-node reconcile with peer failed")
			continue
		}
		totalPulled += pulled
		totalPushed += pushed
		n.log.WithField("peer", pk.String()).
			WithField("pulled", pulled).
			WithField("pushed", pushed).
			Debug("Full-node reconcile with peer complete")
	}
	if tried > 0 {
		n.log.WithField("tried", tried).
			WithField("bootstrap", len(n.cfg.BootstrapPKs)).
			WithField("advertised", len(advertised)).
			WithField("pulled", totalPulled).
			WithField("pushed", totalPushed).
			WithField("store_size", n.store.Len()).
			Info("Full-node reconcile pass complete")
	}
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
