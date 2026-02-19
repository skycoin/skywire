// Package visor pkg/visor/node_health.go
package visor

import (
	"context"
	"fmt"
	"net/rpc"
	"sort"
	"sync"
	"time"

	"github.com/skycoin/dmsg/pkg/dmsg"

	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
)

// NodeHealth represents the health status of a setup node.
type NodeHealth struct {
	PK          cipher.PubKey `json:"pk"`
	Healthy     bool          `json:"healthy"`
	LastChecked time.Time     `json:"last_checked"`
	LastError   string        `json:"last_error,omitempty"`
	Latency     time.Duration `json:"latency_ms"`
}

// NodeHealthTracker tracks health status of TPS and RSN nodes.
type NodeHealthTracker struct {
	dmsgC *dmsg.Client
	log   *logging.Logger

	tpsHealth map[cipher.PubKey]*NodeHealth
	rsnHealth map[cipher.PubKey]*NodeHealth
	mu        sync.RWMutex

	checkInterval time.Duration
}

// NewNodeHealthTracker creates a new health tracker.
func NewNodeHealthTracker(dmsgC *dmsg.Client, log *logging.Logger) *NodeHealthTracker {
	return &NodeHealthTracker{
		dmsgC:         dmsgC,
		log:           log,
		tpsHealth:     make(map[cipher.PubKey]*NodeHealth),
		rsnHealth:     make(map[cipher.PubKey]*NodeHealth),
		checkInterval: 1 * time.Hour,
	}
}

// Start begins periodic health checking of configured nodes.
func (nht *NodeHealthTracker) Start(ctx context.Context, tpsNodes, rsnNodes []cipher.PubKey) {
	// Initialize health entries
	nht.mu.Lock()
	for _, pk := range tpsNodes {
		nht.tpsHealth[pk] = &NodeHealth{PK: pk}
	}
	for _, pk := range rsnNodes {
		nht.rsnHealth[pk] = &NodeHealth{PK: pk}
	}
	nht.mu.Unlock()

	// Initial check after short delay
	go func() {
		select {
		case <-ctx.Done():
			return
		case <-time.After(10 * time.Second):
		}
		nht.checkAllNodes(ctx)
	}()

	// Periodic checks
	go func() {
		ticker := time.NewTicker(nht.checkInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				nht.checkAllNodes(ctx)
			}
		}
	}()

	nht.log.Info("Node health tracking started")
}

// checkAllNodes performs health checks on all configured nodes.
func (nht *NodeHealthTracker) checkAllNodes(ctx context.Context) {
	nht.log.Debug("Starting periodic health check of setup nodes")

	nht.mu.RLock()
	tpsPKs := make([]cipher.PubKey, 0, len(nht.tpsHealth))
	for pk := range nht.tpsHealth {
		tpsPKs = append(tpsPKs, pk)
	}
	rsnPKs := make([]cipher.PubKey, 0, len(nht.rsnHealth))
	for pk := range nht.rsnHealth {
		rsnPKs = append(rsnPKs, pk)
	}
	nht.mu.RUnlock()

	// Check TPS nodes
	tpsHealthyCount := 0
	for _, pk := range tpsPKs {
		if ctx.Err() != nil {
			return
		}
		nht.checkTPSHealth(ctx, pk)
	}

	// Count healthy TPS nodes
	nht.mu.RLock()
	for _, h := range nht.tpsHealth {
		if h.Healthy {
			tpsHealthyCount++
		}
	}
	nht.mu.RUnlock()

	if len(tpsPKs) > 0 && tpsHealthyCount == 0 {
		nht.log.Error("No transport setup nodes are responding")
	}

	// Check RSN nodes
	rsnHealthyCount := 0
	for _, pk := range rsnPKs {
		if ctx.Err() != nil {
			return
		}
		nht.checkRSNHealth(ctx, pk)
	}

	// Count healthy RSN nodes
	nht.mu.RLock()
	for _, h := range nht.rsnHealth {
		if h.Healthy {
			rsnHealthyCount++
		}
	}
	nht.mu.RUnlock()

	if len(rsnPKs) > 0 && rsnHealthyCount == 0 {
		nht.log.Error("No route setup nodes are responding")
	}

	nht.log.Debug("Completed periodic health check of setup nodes")
}

// checkTPSHealth checks the health of a single TPS node.
func (nht *NodeHealthTracker) checkTPSHealth(ctx context.Context, pk cipher.PubKey) {
	start := time.Now()
	err := nht.doTPSHealthCheck(ctx, pk)
	latency := time.Since(start)

	nht.mu.Lock()
	defer nht.mu.Unlock()

	health, ok := nht.tpsHealth[pk]
	if !ok {
		health = &NodeHealth{PK: pk}
		nht.tpsHealth[pk] = health
	}

	health.LastChecked = time.Now()
	health.Latency = latency

	if err != nil {
		health.Healthy = false
		health.LastError = err.Error()
		nht.log.WithField("pk", pk.String()).WithError(err).Warn("TPS health check failed")
	} else {
		health.Healthy = true
		health.LastError = ""
		nht.log.WithField("pk", pk.String()).WithField("latency", latency).Debug("TPS health check passed")
	}
}

// doTPSHealthCheck performs the actual TPS health check via dmsg.
func (nht *NodeHealthTracker) doTPSHealthCheck(ctx context.Context, pk cipher.PubKey) error {
	checkCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	conn, err := nht.dmsgC.Dial(checkCtx, dmsg.Addr{PK: pk, Port: skyenv.DmsgTransportSetupServicePort})
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

// checkRSNHealth checks the health of a single RSN node.
func (nht *NodeHealthTracker) checkRSNHealth(ctx context.Context, pk cipher.PubKey) {
	start := time.Now()
	err := nht.doRSNHealthCheck(ctx, pk)
	latency := time.Since(start)

	nht.mu.Lock()
	defer nht.mu.Unlock()

	health, ok := nht.rsnHealth[pk]
	if !ok {
		health = &NodeHealth{PK: pk}
		nht.rsnHealth[pk] = health
	}

	health.LastChecked = time.Now()
	health.Latency = latency

	if err != nil {
		health.Healthy = false
		health.LastError = err.Error()
		nht.log.WithField("pk", pk.String()).WithError(err).Warn("RSN health check failed")
	} else {
		health.Healthy = true
		health.LastError = ""
		nht.log.WithField("pk", pk.String()).WithField("latency", latency).Debug("RSN health check passed")
	}
}

// doRSNHealthCheck performs the actual RSN health check via dmsg.
func (nht *NodeHealthTracker) doRSNHealthCheck(ctx context.Context, pk cipher.PubKey) error {
	checkCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	conn, err := nht.dmsgC.Dial(checkCtx, dmsg.Addr{PK: pk, Port: skyenv.DmsgSetupPort})
	if err != nil {
		return fmt.Errorf("dmsg dial failed: %w", err)
	}
	defer conn.Close() //nolint:errcheck,gosec

	client := rpc.NewClient(conn)
	defer client.Close() //nolint:errcheck,gosec

	// RSN uses the same health check pattern
	var reply RSNHealthCheckReply
	if err := client.Call("SetupRPCGateway.HealthCheck", &RSNHealthCheckArgs{}, &reply); err != nil {
		return fmt.Errorf("health check RPC failed: %w", err)
	}

	if reply.Status != "OK" {
		return fmt.Errorf("unhealthy status: %s", reply.Status)
	}

	return nil
}

// GetTPSNodesSorted returns TPS nodes sorted by health (healthy first, then by latency).
func (nht *NodeHealthTracker) GetTPSNodesSorted() []cipher.PubKey {
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
	nht.mu.RLock()
	defer nht.mu.RUnlock()

	result := make([]NodeHealth, 0, len(nht.rsnHealth))
	for _, h := range nht.rsnHealth {
		result = append(result, *h)
	}
	return result
}

// IsTPSHealthy returns whether a specific TPS node is healthy.
func (nht *NodeHealthTracker) IsTPSHealthy(pk cipher.PubKey) bool {
	nht.mu.RLock()
	defer nht.mu.RUnlock()

	if h, ok := nht.tpsHealth[pk]; ok {
		return h.Healthy
	}
	return false
}

// IsRSNHealthy returns whether a specific RSN node is healthy.
func (nht *NodeHealthTracker) IsRSNHealthy(pk cipher.PubKey) bool {
	nht.mu.RLock()
	defer nht.mu.RUnlock()

	if h, ok := nht.rsnHealth[pk]; ok {
		return h.Healthy
	}
	return false
}

// RSNHealthCheckArgs is input for RSN health check.
type RSNHealthCheckArgs struct{}

// RSNHealthCheckReply is output from RSN health check.
type RSNHealthCheckReply struct {
	Status string
}
