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
	pk     cipher.PubKey
	sk     cipher.SecKey
	id     NodeID
	rt     *RoutingTable
	store  *Store
	tp     Transport
	log    *logging.Logger
	cfg    Config
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// New creates a new DHT node. Call Start to begin serving.
func New(cfg Config, pk cipher.PubKey, sk cipher.SecKey, tp Transport, log *logging.Logger) *Node {
	cfg.SetDefaults()
	id := NodeIDFromPubKey(pk)
	return &Node{
		pk:    pk,
		sk:    sk,
		id:    id,
		rt:    NewRoutingTable(id),
		store: NewStore(cfg.MaxItems, cfg.ItemTTL),
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

	// Bootstrap.
	if len(n.cfg.BootstrapPKs) > 0 {
		n.bootstrap()
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

	// Store locally.
	_ = n.store.Put(item)

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

	if putErrors == len(closest) && len(closest) > 0 {
		return fmt.Errorf("dht: put failed on all %d peers", len(closest))
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
	_ = n.store.Put(*item)

	return item, nil
}

// --- RPC client methods ---

func (n *Node) rpcFindNode(ctx context.Context, p Peer, target NodeID) (*FindNodeResponse, error) {
	conn, err := n.tp.Dial(ctx, p.PK)
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
	conn, err := n.tp.Dial(ctx, p.PK)
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
	conn, err := n.tp.Dial(ctx, p.PK)
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

func (n *Node) rpcPing(ctx context.Context, p Peer) error {
	conn, err := n.tp.Dial(ctx, p.PK)
	if err != nil {
		return err
	}
	defer conn.Close() //nolint:errcheck,gosec

	req := PingRequest{SenderID: n.id, SenderPK: n.pk}
	var resp PingResponse
	return rpcCall(ctx, conn, methodPing, req, &resp)
}

// --- Server ---

func (n *Node) serve(lis Listener) {
	// Close the listener when context is canceled so Accept unblocks.
	go func() {
		<-n.ctx.Done()
		lis.Close() //nolint:errcheck,gosec
	}()
	for {
		conn, remotePK, err := lis.Accept()
		if err != nil {
			return // listener closed or context canceled
		}
		go n.handleConn(conn, remotePK)
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
		err := n.store.Put(req.Item)
		resp := PutValueResponse{Stored: err == nil}
		if err != nil {
			resp.Error = err.Error()
		}
		writeMsg(conn, methodPutValue, resp) //nolint:errcheck,gosec
	}
}

// --- Bootstrap & Maintenance ---

func (n *Node) bootstrap() {
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
	}

	// Do a self-lookup to populate nearby buckets.
	if _, _, err := n.iterativeLookup(n.ctx, n.id, false); err != nil {
		n.log.WithError(err).Debug("Bootstrap self-lookup failed")
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
