// Package visor pkg/visor/node_health.go c3-vis-core
package visor

import (
	"context"
	"fmt"
	"net/rpc"
	"sort"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// cacheTTL bounds how long a computed health snapshot is served before the
// next on-demand request triggers a recompute.
const cacheTTL = 60 * time.Second

// nodeCheckTimeout bounds a single per-node discovery + dial + health-check.
const nodeCheckTimeout = 10 * time.Second

// NodeHealth represents the health status of a setup node.
type NodeHealth struct {
	PK          cipher.PubKey `json:"pk"`
	Healthy     bool          `json:"healthy"`
	LastChecked time.Time     `json:"last_checked"`
	LastError   string        `json:"last_error,omitempty"`
	Latency     time.Duration `json:"latency_ms"`
}

// NodeHealthTracker computes health status of TPS and RSN nodes on demand,
// behind a short TTL cache. It re-seeds its node set from the supplied
// providers on every recompute so runtime config_refresh changes are picked
// up. Results feed the api_tps UI panel RPCs only; the real transport/route
// dial path does not depend on this.
type NodeHealthTracker struct {
	dmsgC *dmsg.Client
	log   *logging.Logger

	// tpsProvider / rsnProvider return the current effective node sets. They
	// are read fresh on every recompute so config changes take effect without
	// a restart.
	tpsProvider func() []cipher.PubKey
	rsnProvider func() []cipher.PubKey

	baseCtx context.Context

	mu        sync.RWMutex
	tpsHealth map[cipher.PubKey]*NodeHealth
	rsnHealth map[cipher.PubKey]*NodeHealth
	lastCheck time.Time

	// refreshMu serializes recompute so concurrent RPCs don't stampede the
	// per-node dials (singleflight-style: one recompute at a time).
	refreshMu sync.Mutex

	// checkFn performs a single per-node check. It defaults to checkNode and
	// is overridable in tests to avoid real dmsg dials.
	checkFn func(ctx context.Context, pk cipher.PubKey, port uint16, kind string) *NodeHealth
}

// NewNodeHealthTracker creates a new on-demand health tracker. The providers
// are invoked on every recompute to (re)seed the node set.
func NewNodeHealthTracker(dmsgC *dmsg.Client, log *logging.Logger, tpsProvider, rsnProvider func() []cipher.PubKey) *NodeHealthTracker {
	nht := &NodeHealthTracker{
		dmsgC:       dmsgC,
		log:         log,
		tpsProvider: tpsProvider,
		rsnProvider: rsnProvider,
		baseCtx:     context.Background(),
		tpsHealth:   make(map[cipher.PubKey]*NodeHealth),
		rsnHealth:   make(map[cipher.PubKey]*NodeHealth),
	}
	nht.checkFn = nht.checkNode
	return nht
}

// Start records the base context used for on-demand checks. It no longer runs
// a background ticker — health is computed lazily when an api_tps RPC asks for
// it (see ensureFresh). Kept for lifecycle symmetry.
func (nht *NodeHealthTracker) Start(ctx context.Context) {
	if ctx != nil {
		nht.baseCtx = ctx
	}
	nht.log.Debug("Node health tracker ready (on-demand)")
}

// ensureFresh recomputes health if the cached snapshot is older than cacheTTL.
// A single recompute runs at a time; concurrent callers block on refreshMu and
// re-check freshness after acquiring it, so they return the just-computed data
// without launching a second storm of dials.
func (nht *NodeHealthTracker) ensureFresh() {
	nht.mu.RLock()
	fresh := !nht.lastCheck.IsZero() && time.Since(nht.lastCheck) < cacheTTL
	nht.mu.RUnlock()
	if fresh {
		return
	}

	nht.refreshMu.Lock()
	defer nht.refreshMu.Unlock()

	// Re-check: another goroutine may have recomputed while we waited.
	nht.mu.RLock()
	fresh = !nht.lastCheck.IsZero() && time.Since(nht.lastCheck) < cacheTTL
	nht.mu.RUnlock()
	if fresh {
		return
	}

	nht.recompute()
}

// recompute re-seeds the node set from the providers and checks every node
// concurrently, then swaps in the fresh snapshot.
func (nht *NodeHealthTracker) recompute() {
	ctx := nht.baseCtx

	var tpsPKs, rsnPKs []cipher.PubKey
	if nht.tpsProvider != nil {
		tpsPKs = nht.tpsProvider()
	}
	if nht.rsnProvider != nil {
		rsnPKs = nht.rsnProvider()
	}

	tpsResults := make([]*NodeHealth, len(tpsPKs))
	rsnResults := make([]*NodeHealth, len(rsnPKs))

	var wg sync.WaitGroup
	for i, pk := range tpsPKs {
		wg.Add(1)
		go func(i int, pk cipher.PubKey) {
			defer wg.Done()
			tpsResults[i] = nht.checkFn(ctx, pk, skyenv.DmsgTransportSetupServicePort, "TPS")
		}(i, pk)
	}
	for i, pk := range rsnPKs {
		wg.Add(1)
		go func(i int, pk cipher.PubKey) {
			defer wg.Done()
			rsnResults[i] = nht.checkFn(ctx, pk, skyenv.DmsgSetupPort, "RSN")
		}(i, pk)
	}
	wg.Wait()

	tpsMap := make(map[cipher.PubKey]*NodeHealth, len(tpsResults))
	tpsHealthy := 0
	for _, h := range tpsResults {
		tpsMap[h.PK] = h
		if h.Healthy {
			tpsHealthy++
		}
	}
	rsnMap := make(map[cipher.PubKey]*NodeHealth, len(rsnResults))
	rsnHealthy := 0
	for _, h := range rsnResults {
		rsnMap[h.PK] = h
		if h.Healthy {
			rsnHealthy++
		}
	}

	nht.mu.Lock()
	nht.tpsHealth = tpsMap
	nht.rsnHealth = rsnMap
	nht.lastCheck = time.Now()
	nht.mu.Unlock()

	if len(tpsPKs) > 0 && tpsHealthy == 0 {
		nht.log.Error("No transport setup nodes are responding")
	}
	if len(rsnPKs) > 0 && rsnHealthy == 0 {
		nht.log.Error("No route setup nodes are responding")
	}
}

// checkNode performs a discovery lookup + dmsg dial + HealthCheck RPC against a
// single setup node and returns its health. kind is "TPS" or "RSN" (for logs).
func (nht *NodeHealthTracker) checkNode(ctx context.Context, pk cipher.PubKey, port uint16, kind string) *NodeHealth {
	start := time.Now()
	err := nht.doHealthCheck(ctx, pk, port)
	latency := time.Since(start)

	h := &NodeHealth{PK: pk, LastChecked: time.Now(), Latency: latency}
	if err != nil {
		h.Healthy = false
		h.LastError = err.Error()
		nht.log.WithField("pk", pk.String()).WithError(err).Warnf("%s health check failed", kind)
	} else {
		h.Healthy = true
		nht.log.WithField("pk", pk.String()).WithField("latency", latency).Debugf("%s health check passed", kind)
	}
	return h
}

// doHealthCheck performs the actual health check via dmsg against the given port.
func (nht *NodeHealthTracker) doHealthCheck(ctx context.Context, pk cipher.PubKey, port uint16) error {
	// Quick discovery lookup before dialing — if the PK isn't registered
	// as a client in dmsg-discovery, there's no point dialing (it would
	// fall back to dialViaConnectedServers and burn port reservations
	// across all servers for the full timeout for nothing).
	if _, err := nht.dmsgC.DiscEntry(ctx, pk); err != nil {
		return fmt.Errorf("not in dmsg discovery: %w", err)
	}

	checkCtx, cancel := context.WithTimeout(ctx, nodeCheckTimeout)
	defer cancel()

	conn, err := nht.dmsgC.Dial(checkCtx, dmsg.Addr{PK: pk, Port: port})
	if err != nil {
		return fmt.Errorf("dmsg dial failed: %w", err)
	}
	defer conn.Close() //nolint:errcheck,gosec

	client := rpc.NewClient(conn)
	defer client.Close() //nolint:errcheck,gosec

	var reply TPSHealthCheckReply
	if err := client.Call("SetupRPCGateway.HealthCheck", &TPSHealthCheckArgs{}, &reply); err != nil {
		return fmt.Errorf("health check RPC failed: %w", err)
	}

	if reply.Status != "OK" {
		return fmt.Errorf("unhealthy status: %s", reply.Status)
	}

	return nil
}

// GetTPSNodesSorted returns TPS nodes sorted by health (healthy first, then by latency).
func (nht *NodeHealthTracker) GetTPSNodesSorted() []cipher.PubKey {
	nht.ensureFresh()

	nht.mu.RLock()
	defer nht.mu.RUnlock()

	nodes := make([]*NodeHealth, 0, len(nht.tpsHealth))
	for _, h := range nht.tpsHealth {
		nodes = append(nodes, h)
	}

	sort.Slice(nodes, func(i, j int) bool {
		// Healthy nodes first
		if nodes[i].Healthy != nodes[j].Healthy {
			return nodes[i].Healthy
		}
		// Then by latency (lower is better)
		return nodes[i].Latency < nodes[j].Latency
	})

	result := make([]cipher.PubKey, len(nodes))
	for i, n := range nodes {
		result[i] = n.PK
	}
	return result
}

// GetRSNNodesSorted returns RSN nodes sorted by health (healthy first, then by latency).
func (nht *NodeHealthTracker) GetRSNNodesSorted() []cipher.PubKey {
	nht.ensureFresh()

	nht.mu.RLock()
	defer nht.mu.RUnlock()

	nodes := make([]*NodeHealth, 0, len(nht.rsnHealth))
	for _, h := range nht.rsnHealth {
		nodes = append(nodes, h)
	}

	sort.Slice(nodes, func(i, j int) bool {
		// Healthy nodes first
		if nodes[i].Healthy != nodes[j].Healthy {
			return nodes[i].Healthy
		}
		// Then by latency (lower is better)
		return nodes[i].Latency < nodes[j].Latency
	})

	result := make([]cipher.PubKey, len(nodes))
	for i, n := range nodes {
		result[i] = n.PK
	}
	return result
}

// GetTPSHealth returns health status for all TPS nodes.
func (nht *NodeHealthTracker) GetTPSHealth() []NodeHealth {
	nht.ensureFresh()

	nht.mu.RLock()
	defer nht.mu.RUnlock()

	result := make([]NodeHealth, 0, len(nht.tpsHealth))
	for _, h := range nht.tpsHealth {
		result = append(result, *h)
	}
	return result
}

// GetRSNHealth returns health status for all RSN nodes.
func (nht *NodeHealthTracker) GetRSNHealth() []NodeHealth {
	nht.ensureFresh()

	nht.mu.RLock()
	defer nht.mu.RUnlock()

	result := make([]NodeHealth, 0, len(nht.rsnHealth))
	for _, h := range nht.rsnHealth {
		result = append(result, *h)
	}
	return result
}
