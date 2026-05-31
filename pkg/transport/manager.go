// Package transport pkg/transport/manager.go
package transport

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/app/appevent"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/transport/network"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
	tspec "github.com/skycoin/skywire/pkg/transport/spec"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

const reconnectPhaseDelay = 10 * time.Second
const reconnectRemoteTimeout = 3 * time.Second
const transportReRegisterInterval = 90 * time.Second

// PersistentTransports is the wire-format pinning entry. Aliased
// from pkg/transport/spec so existing callers writing
// `transport.PersistentTransports{...}` keep compiling, while the
// canonical (WASM-clean) definition lives in the spec leaf package.
type PersistentTransports = tspec.PersistentTransports

// ManagerConfig configures a Manager.
type ManagerConfig struct {
	PubKey                    cipher.PubKey
	SecKey                    cipher.SecKey
	DiscoveryClient           DiscoveryClient
	LogStore                  LogStore
	PersistentTransportsCache []PersistentTransports
	PTpsCacheMu               sync.RWMutex
	Version                   string // Visor version for reporting to TPD
	// ARTransportLimit controls AR registration:
	//   0 = stay registered (default)
	//   N > 0 = deregister after N transports
	//   N < 0 = never register
	ARTransportLimit int
}

// LatencyFallbackCallback is called when transport-level ping fails to produce
// latency data (remote visor doesn't support transport ping frames).
// It falls back to measuring latency via RSN route setup.
type LatencyFallbackCallback func(ctx context.Context, remote cipher.PubKey, tpID uuid.UUID) (latencyMs float64)

// RouteChecker is called before tearing down an existing transport for re-creation.
// It returns true if any active routing rule references the given transport ID,
// meaning the transport is actively carrying route traffic and must not be torn down.
type RouteChecker func(tpID uuid.UUID) bool

// Manager manages Transports.
type Manager struct {
	Logger   *logging.Logger
	Conf     *ManagerConfig
	tps      map[uuid.UUID]*ManagedTransport
	arClient addrresolver.APIClient
	ebc      *appevent.Broadcaster

	readCh chan routing.Packet
	mx     sync.RWMutex
	wg     sync.WaitGroup
	done   chan struct{}

	readyOnce sync.Once // ensure we only ready once.
	ready     chan struct{}

	factory    network.ClientFactory
	netClients map[types.Type]network.Client

	// latencyFallback is called when transport-level ping doesn't produce
	// latency data after a grace period (old visor that doesn't support
	// transport ping frames). Falls back to RSN-based route measurement.
	latencyFallback   LatencyFallbackCallback
	latencyFallbackMu sync.RWMutex

	// routeChecker is called before tearing down an existing transport for re-creation.
	// If it returns true, the transport has active routes and must not be torn down.
	routeChecker   RouteChecker
	routeCheckerMu sync.RWMutex

	// tpdLeafPub mirrors register / deregister to the visor's CXO
	// stats publisher tree (the same tree that already carries
	// transports/<uuid>/current and timeline leaves). Set lazily by
	// the visor's stats init after the publisher is constructed; nil
	// means "no CXO publish, HTTP path only." See SetTPDLeafPublisher.
	tpdLeafPubMu sync.RWMutex
	tpdLeafPub   TPDLeafPublisher

	// regNudge signals the re-registration loop to run soon (after a short debounce).
	// Sent after accepting a new transport so it gets batch-registered quickly.
	regNudge chan struct{}

	// delQueue collects transport IDs for deferred batch deletion from TPD.
	// Transports queue their ID here on close instead of making individual HTTP calls.
	delQueue   []uuid.UUID
	delQueueMu sync.Mutex
	delNudge   chan struct{}

	// arDeregistered tracks whether the visor has deregistered from AR
	// due to ar_transport_limit being reached. Once true, it stays true
	// until the visor restarts.
	arDeregistered   bool
	arDeregisteredMu sync.Mutex

	// cascadeHandler handles cascade protocol packets (route ID 0) on any transport.
	cascadeHandler   func(p routing.Packet, mt *ManagedTransport)
	cascadeHandlerMu sync.RWMutex

	// dhtHandler handles DHT protocol packets (route ID 0) on any transport.
	dhtHandler   func(p routing.Packet, mt *ManagedTransport)
	dhtHandlerMu sync.RWMutex

	// setupRPCHandler handles RSN RPC relay packets (route ID 0) on any transport.
	setupRPCHandler   func(p routing.Packet, mt *ManagedTransport)
	setupRPCHandlerMu sync.RWMutex

	// visorRPCHandler handles visor RPC packets (route ID 0) on any transport.
	visorRPCHandler   func(p routing.Packet, mt *ManagedTransport)
	visorRPCHandlerMu sync.RWMutex
	// skynetFwdHandler handles skynet forward packets (route ID 0).
	skynetFwdHandler   func(p routing.Packet, mt *ManagedTransport)
	skynetFwdHandlerMu sync.RWMutex
	// appDirectHandler handles direct skywire-network app dial packets
	// (route ID 0). Set by the visor at init when it brings up its
	// AppDirect VStreamMux.
	appDirectHandler   func(p routing.Packet, mt *ManagedTransport)
	appDirectHandlerMu sync.RWMutex
}

// NewManager creates a Manager with the provided configuration and transport factories.
// 'factories' should be ordered by preference.
func NewManager(log *logging.Logger, arClient addrresolver.APIClient, ebc *appevent.Broadcaster, config *ManagerConfig, factory network.ClientFactory) (*Manager, error) {
	if log == nil {
		log = logging.MustGetLogger("tp_manager")
	}
	tm := &Manager{
		Logger:     log,
		Conf:       config,
		tps:        make(map[uuid.UUID]*ManagedTransport),
		readCh:     make(chan routing.Packet, 20),
		done:       make(chan struct{}),
		ready:      make(chan struct{}),
		netClients: make(map[types.Type]network.Client),
		arClient:   arClient,
		factory:    factory,
		ebc:        ebc,
		regNudge:   make(chan struct{}, 1),
		delNudge:   make(chan struct{}, 1),
	}
	return tm, nil
}

// InitDmsgClient initilizes the dmsg client and also adds dmsgC to the factory
func (tm *Manager) InitDmsgClient(ctx context.Context, dmsgC *dmsg.Client) {
	tm.factory.DmsgC = dmsgC
	tm.InitClient(ctx, types.DMSG, 0)
}

// Serve starts all network clients and starts accepting connections
// from all those clients
// Additionally, it runs cleanup and persistent reconnection routines
func (tm *Manager) Serve(ctx context.Context) {
	// for cleanup, reconnect, re-registration, deferred deletion, and
	// transport-maintenance goroutines (the latter replaces what used
	// to be 2 goroutines per ManagedTransport)
	tm.wg.Add(5)
	go tm.cleanupTransports(ctx)
	go tm.runReconnectPersistent(ctx)
	go tm.runReRegisterTransports(ctx)
	go tm.runDeferredDeletions(ctx)
	go tm.runTransportMaintenance(ctx)
	tm.Logger.Debug("transport manager is serving.")
}

// runTransportMaintenance drives the periodic per-transport work
// (LogStore flush + transport-level ping) from a single central
// goroutine. Replaces the previous design where each ManagedTransport
// ran its own logLoop and pingLoop goroutines plus tickers.
//
// On a hub visor with hundreds of automatic transports the old
// per-transport tickers were the dominant cost in runtime-scheduler
// CPU: 460 transports × (one 3s ticker + one 30s ticker) = ~150
// scheduler wake-ups per second across the visor, the vast majority
// for no-op work (logMod() reporting nothing changed). pprof captured
// that as a 55% slice of total samples in runtime.mcall +
// runtime.findRunnable + futex on a steady-state idle visor.
//
// Centralizing collapses those wake-ups to two (logTicker + pingTicker)
// regardless of transport count, and drops ~2N goroutines from the
// steady-state count.
func (tm *Manager) runTransportMaintenance(ctx context.Context) {
	defer tm.wg.Done()

	logTicker := time.NewTicker(logWriteInterval)
	pingTicker := time.NewTicker(transportPingInterval)
	defer logTicker.Stop()
	defer pingTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-tm.done:
			return
		case <-logTicker.C:
			tm.recordAllTransportLogs()
		case <-pingTicker.C:
			tm.pingAllTransports()
		}
	}
}

// recordAllTransportLogs flushes every live transport's in-memory log
// entry to the LogStore. The store is an in-memory map (see
// init_transport.go's logS = transport.InMemoryTransportLogStore())
// so each flush is a single mutex-protected write; iterating all
// transports inline is cheap and avoids spawning per-flush goroutines.
func (tm *Manager) recordAllTransportLogs() {
	tm.mx.RLock()
	snapshot := make([]*ManagedTransport, 0, len(tm.tps))
	for _, mt := range tm.tps {
		snapshot = append(snapshot, mt)
	}
	tm.mx.RUnlock()

	for _, mt := range snapshot {
		mt.recordLog()
	}
}

// pingAllTransports fans tickPing across the live transport set.
//
// Concurrency: tickPing's sendTransportPing path does a small Write on
// the underlying transport (15-byte ping packet) that can in principle
// block on a stalled remote. To prevent one slow transport from
// stalling the rest of the ping sweep, we bound concurrency at
// pingFanoutWorkers — large enough that the full sweep finishes well
// within transportPingInterval even on a hub visor with hundreds of
// transports, small enough that the burst spawn isn't itself a
// scheduler hot spot.
//
// Per-tick goroutine accounting: pingFanoutWorkers short-lived
// goroutines per pingTicker fire (every transportPingInterval) ≈ one
// short-lived spike per minute. Compared to the previous model — one
// long-lived goroutine per transport for the lifetime of the
// transport — this is a substantial steady-state goroutine reduction.
func (tm *Manager) pingAllTransports() {
	tm.mx.RLock()
	snapshot := make([]*ManagedTransport, 0, len(tm.tps))
	for _, mt := range tm.tps {
		snapshot = append(snapshot, mt)
	}
	tm.mx.RUnlock()

	const pingFanoutWorkers = 32
	sem := make(chan struct{}, pingFanoutWorkers)
	var wg sync.WaitGroup
	for _, mt := range snapshot {
		sem <- struct{}{}
		wg.Add(1)
		go func(mt *ManagedTransport) {
			defer wg.Done()
			defer func() { <-sem }()
			mt.tickPing()
		}(mt)
	}
	wg.Wait()
}

func (tm *Manager) runReconnectPersistent(ctx context.Context) {
	defer tm.wg.Done()
	ticker := time.NewTicker(reconnectPhaseDelay)
	defer ticker.Stop()
	tm.reconnectPersistent(ctx)
	for {
		select {
		case <-ticker.C:
			tm.reconnectPersistent(ctx)
			// Reset to wait full timeout no matter how long the last phase took.
			// Using Reset instead of creating a new ticker avoids leaking the old one.
			ticker.Reset(reconnectPhaseDelay)
		case <-tm.done:
			return
		case <-ctx.Done():
			return
		}
	}
}

func (tm *Manager) reconnectPersistent(ctx context.Context) {
	for _, remote := range tm.getPTpsCache() {
		tm.Logger.Debugf("Reconnecting to persistent transport to %s, type %s", remote.PK, remote.NetType)
		deadlined, cancel := context.WithTimeout(ctx, reconnectRemoteTimeout)
		_, err := tm.saveTransportInternal(deadlined, remote.PK, remote.NetType, LabelUser, SaveTransportOptions{})
		if err != nil {
			tm.Logger.WithError(err).
				WithField("remote_pk", remote.PK).
				WithField("network_type", remote.NetType).
				Warnf("Cannot connect to persistent remote")
		}
		cancel()
	}
}

func (tm *Manager) runReRegisterTransports(ctx context.Context) {
	defer tm.wg.Done()
	ticker := time.NewTicker(transportReRegisterInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			tm.reRegisterTransports(ctx)
		case <-tm.regNudge:
			// New transport accepted — debounce 5s to batch concurrent accepts,
			// then register all transports in one call.
			tm.Logger.Debug("Registration nudge received, debouncing...")
			select {
			case <-time.After(5 * time.Second):
			case <-tm.done:
				return
			case <-ctx.Done():
				return
			}
			// Drain any additional nudges that arrived during debounce
			for {
				select {
				case <-tm.regNudge:
				default:
					goto register
				}
			}
		register:
			tm.reRegisterTransports(ctx)
			ticker.Reset(transportReRegisterInterval)
		case <-tm.done:
			return
		case <-ctx.Done():
			return
		}
	}
}

// queueDeletion adds a transport ID to the deferred deletion queue.
// Called by ManagedTransport.close() instead of making individual HTTP calls.
func (tm *Manager) queueDeletion(id uuid.UUID) {
	tm.delQueueMu.Lock()
	tm.delQueue = append(tm.delQueue, id)
	tm.delQueueMu.Unlock()
	// Non-blocking nudge
	select {
	case tm.delNudge <- struct{}{}:
	default: // nudge already pending
	}
}

const deletionDebounce = 5 * time.Second

// runDeferredDeletions batch-deletes transport IDs that were queued by closing transports.
// Uses the same debounce pattern as registration nudges to avoid flooding TPD.
func (tm *Manager) runDeferredDeletions(ctx context.Context) {
	defer tm.wg.Done()
	for {
		select {
		case <-tm.delNudge:
			// Debounce: wait for burst of closures to settle
			tm.Logger.Debug("Deletion nudge received, debouncing...")
			select {
			case <-time.After(deletionDebounce):
			case <-tm.done:
				tm.flushDeletionQueue()
				return
			case <-ctx.Done():
				return
			}
			// Drain any additional nudges
			for {
				select {
				case <-tm.delNudge:
				default:
					goto flush
				}
			}
		flush:
			tm.flushDeletionQueue()
		case <-tm.done:
			tm.flushDeletionQueue()
			return
		case <-ctx.Done():
			return
		}
	}
}

// flushDeletionQueue batch-deletes all queued transport IDs from TPD.
func (tm *Manager) flushDeletionQueue() {
	tm.delQueueMu.Lock()
	ids := tm.delQueue
	tm.delQueue = nil
	tm.delQueueMu.Unlock()

	if len(ids) == 0 {
		return
	}

	tm.Logger.Debugf("Batch deleting %d transports from TPD", len(ids))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	deleted, err := tm.Conf.DiscoveryClient.DeleteTransports(ctx, ids)
	cancel()
	if err != nil {
		tm.Logger.WithError(err).Warnf("Batch delete completed with error: %d/%d deleted", deleted, len(ids))
	} else {
		tm.Logger.Debugf("Batch deleted %d/%d transports from TPD", deleted, len(ids))
	}

	// Mirror to the CXO publisher tree. Dual-write phase: best-effort,
	// and idempotent on TPD's side (DeregisterTransport on a
	// not-found ID is a no-op).
	tm.publishTPDTombstones(ids)
}

func (tm *Manager) reRegisterTransports(ctx context.Context) {
	// Bandwidth and latency are no longer carried in re-registration
	// — visors publish those on their own CXO telemetry feed
	// (pkg/cxo/treestore) and serve them via HTTP-over-DMSG. TPD
	// becomes a pull-side aggregator. Re-registration is purely a
	// liveness signal for the bare transport list now.
	tm.mx.RLock()
	bareEntries := make([]*Entry, 0, len(tm.tps))
	for _, tp := range tm.tps {
		if tp.IsClosed() {
			continue
		}
		// Skip self-loop transports. They are diagnostic-only — both
		// edges are the same PK so they never appear in real routes.
		// Publishing them to TPD or the DHT would mislead other
		// visors' pathfinders into considering them legitimate
		// network state.
		if tp.Entry.Edges[0] == tp.Entry.Edges[1] {
			continue
		}
		bareEntries = append(bareEntries, &tp.Entry)
	}
	tm.mx.RUnlock()

	// Empty publish IS still useful: when our last real transport
	// goes away, downstream readers (DHT entries, TPD aggregator)
	// otherwise keep serving the previous stale list under our PK
	// indefinitely. Republishing the empty list with a fresh sequence
	// cleans them up.
	tm.Logger.Debugf("Re-registering %d transports with discovery", len(bareEntries))

	if err := tm.Conf.DiscoveryClient.RegisterTransportsV3(ctx, tm.Conf.Version, bareEntries...); err != nil {
		tm.Logger.WithError(err).Warn("Failed to re-register transports with discovery")
	} else {
		tm.Logger.Debugf("Successfully re-registered %d transports", len(bareEntries))
	}

	// Mirror to the CXO publisher tree. Dual-write phase: HTTP is
	// authoritative; CXO is best-effort and converges by the next
	// tick if a publish fails or the publisher isn't yet wired.
	tm.publishTPDEntries(bareEntries)
}

func (tm *Manager) getPTpsCache() []PersistentTransports {
	tm.Conf.PTpsCacheMu.Lock()
	defer tm.Conf.PTpsCacheMu.Unlock()

	return tm.Conf.PersistentTransportsCache
}

// TPDLeafPublisher is the contract the transport manager uses to
// mirror register / deregister events into a CXO publisher tree
// consumed by TPD's aggregator. *treestore.Publisher already
// satisfies this; declaring it as an interface keeps the transport
// package free of a cxo/treestore dependency. Wired by
// pkg/visor/init_stats.go after the publisher is built.
type TPDLeafPublisher interface {
	Put(path string, value []byte) error
	Delete(path string) error
}

// SetTPDLeafPublisher installs (or clears, if nil) the CXO publisher
// hook used to mirror transport register / deregister leaves to TPD.
// Safe to call after Serve has started — the next register tick or
// flushDeletionQueue picks it up. Calling with nil disables CXO
// mirroring without affecting the HTTP register path.
func (tm *Manager) SetTPDLeafPublisher(p TPDLeafPublisher) {
	tm.tpdLeafPubMu.Lock()
	tm.tpdLeafPub = p
	tm.tpdLeafPubMu.Unlock()
}

func (tm *Manager) tpdLeafPublisher() TPDLeafPublisher {
	tm.tpdLeafPubMu.RLock()
	defer tm.tpdLeafPubMu.RUnlock()
	return tm.tpdLeafPub
}

// publishTPDEntries publishes one transports/<uuid>/entry leaf per
// entry. Best-effort: failures are logged at debug and the next
// reRegister tick retries.
func (tm *Manager) publishTPDEntries(entries []*Entry) {
	pub := tm.tpdLeafPublisher()
	if pub == nil || len(entries) == 0 {
		return
	}
	type entryLeaf struct {
		Version string `json:"version,omitempty"`
		Entry   *Entry `json:"entry"`
	}
	for _, e := range entries {
		if e == nil {
			continue
		}
		body, err := json.Marshal(entryLeaf{Version: tm.Conf.Version, Entry: e})
		if err != nil {
			tm.Logger.WithError(err).WithField("transport", e.ID).
				Debug("Failed to marshal transport entry leaf")
			continue
		}
		path := fmt.Sprintf("transports/%s/entry", e.ID.String())
		if err := pub.Put(path, body); err != nil {
			tm.Logger.WithError(err).WithField("path", path).
				Debug("Failed to publish transport entry leaf to CXO")
		}
	}
}

// publishTPDTombstones publishes a transports/<uuid>/tombstone leaf
// per id, and removes the corresponding /entry leaf. Best-effort.
func (tm *Manager) publishTPDTombstones(ids []uuid.UUID) {
	pub := tm.tpdLeafPublisher()
	if pub == nil || len(ids) == 0 {
		return
	}
	body, err := json.Marshal(struct {
		DeletedAt time.Time `json:"deleted_at"`
	}{DeletedAt: time.Now().UTC()})
	if err != nil {
		tm.Logger.WithError(err).Debug("Failed to marshal tombstone body")
		return
	}
	for _, id := range ids {
		// Delete the entry leaf so a future tree walk doesn't replay
		// the now-stale registration; the tombstone leaf is the
		// positive deletion signal that drives TPD's aggregator.
		entryPath := fmt.Sprintf("transports/%s/entry", id.String())
		if err := pub.Delete(entryPath); err != nil {
			tm.Logger.WithError(err).WithField("path", entryPath).
				Debug("Failed to delete transport entry leaf from CXO")
		}
		tombPath := fmt.Sprintf("transports/%s/tombstone", id.String())
		if err := pub.Put(tombPath, body); err != nil {
			tm.Logger.WithError(err).WithField("path", tombPath).
				Debug("Failed to publish transport tombstone to CXO")
		}
	}
}

// SetPTpsCache sets the PersistentTransportsCache
func (tm *Manager) SetPTpsCache(pTps []PersistentTransports) {
	tm.Conf.PTpsCacheMu.Lock()
	defer tm.Conf.PTpsCacheMu.Unlock()

	tm.Conf.PersistentTransportsCache = pTps
}

// SetCascadeHandler sets the handler for cascade protocol packets (route ID 0).
// Called by the router to register its cascade handler.
func (tm *Manager) SetCascadeHandler(h func(p routing.Packet, mt *ManagedTransport)) {
	tm.cascadeHandlerMu.Lock()
	defer tm.cascadeHandlerMu.Unlock()
	tm.cascadeHandler = h
	// Propagate to existing transports.
	tm.mx.RLock()
	for _, mt := range tm.tps {
		mt.cascadeHandler = h
	}
	tm.mx.RUnlock()
}

// checkARLimit checks if the transport count has reached the AR transport limit.
// If so, deregisters from the address resolver. This is a one-way action —
// the visor stays deregistered until restart.
func (tm *Manager) checkARLimit() {
	limit := tm.Conf.ARTransportLimit
	if limit <= 0 {
		return // 0 = no limit, negative = never registered
	}

	tm.arDeregisteredMu.Lock()
	if tm.arDeregistered {
		tm.arDeregisteredMu.Unlock()
		return
	}

	count := tm.TransportCount()
	if count >= limit {
		tm.arDeregistered = true
		tm.arDeregisteredMu.Unlock()
		tm.Logger.WithField("count", count).WithField("limit", limit).
			Info("AR transport limit reached — deregistering from address resolver")
		if tm.arClient != nil {
			if err := tm.arClient.Close(); err != nil {
				tm.Logger.WithError(err).Warn("Failed to close AR client during deregistration")
			}
		}
	} else {
		tm.arDeregisteredMu.Unlock()
	}
}

// ShouldRegisterAR returns false if the AR transport limit is negative
// (never register). Callers should check this during visor initialization.
func (tm *Manager) ShouldRegisterAR() bool {
	return tm.Conf.ARTransportLimit >= 0
}

// SetDHTHandler sets the handler for DHT protocol packets (route ID 0).
func (tm *Manager) SetDHTHandler(h func(p routing.Packet, mt *ManagedTransport)) {
	tm.dhtHandlerMu.Lock()
	defer tm.dhtHandlerMu.Unlock()
	tm.dhtHandler = h
	tm.mx.RLock()
	for _, mt := range tm.tps {
		mt.dhtHandler = h
	}
	tm.mx.RUnlock()
}

// SetVisorRPCHandler sets the handler for visor RPC packets (route ID 0).
func (tm *Manager) SetVisorRPCHandler(h func(p routing.Packet, mt *ManagedTransport)) {
	tm.visorRPCHandlerMu.Lock()
	defer tm.visorRPCHandlerMu.Unlock()
	tm.visorRPCHandler = h
	tm.mx.RLock()
	for _, mt := range tm.tps {
		mt.visorRPCHandler = h
	}
	tm.mx.RUnlock()
}

// SetSkynetForwardHandler sets the handler for skynet forward packets (route ID 0).
func (tm *Manager) SetSkynetForwardHandler(h func(p routing.Packet, mt *ManagedTransport)) {
	tm.skynetFwdHandlerMu.Lock()
	defer tm.skynetFwdHandlerMu.Unlock()
	tm.skynetFwdHandler = h
	tm.mx.RLock()
	for _, mt := range tm.tps {
		mt.skynetFwdHandler = h
	}
	tm.mx.RUnlock()
}

// SetAppDirectHandler sets the handler for direct skywire-network app dial
// packets (route ID 0). Used by the visor to deliver inbound direct-dial
// streams to the per-app server-side accept loop without route setup.
func (tm *Manager) SetAppDirectHandler(h func(p routing.Packet, mt *ManagedTransport)) {
	tm.appDirectHandlerMu.Lock()
	defer tm.appDirectHandlerMu.Unlock()
	tm.appDirectHandler = h
	tm.mx.RLock()
	for _, mt := range tm.tps {
		mt.appDirectHandler = h
	}
	tm.mx.RUnlock()
}

// SetSetupRPCHandler sets the handler for RSN RPC relay packets (route ID 0).
func (tm *Manager) SetSetupRPCHandler(h func(p routing.Packet, mt *ManagedTransport)) {
	tm.setupRPCHandlerMu.Lock()
	defer tm.setupRPCHandlerMu.Unlock()
	tm.setupRPCHandler = h
	tm.mx.RLock()
	for _, mt := range tm.tps {
		mt.setupRPCHandler = h
	}
	tm.mx.RUnlock()
}

// SetRouteChecker sets the callback used to determine if a transport has active routes.
// SetLatencyFallback sets the callback used when transport-level ping fails
// to produce latency data (remote visor doesn't support transport ping frames).
func (tm *Manager) SetLatencyFallback(cb LatencyFallbackCallback) {
	tm.latencyFallbackMu.Lock()
	defer tm.latencyFallbackMu.Unlock()
	tm.latencyFallback = cb
}

// invokeLatencyFallback is called by the managed transport's pingLoop when
// transport-level pings produce no response after a grace period. It falls
// back to the RSN-based route measurement for backward compatibility with
// old visors that don't support transport ping frames.
func (tm *Manager) invokeLatencyFallback(remote cipher.PubKey, tp *ManagedTransport) {
	tm.latencyFallbackMu.RLock()
	cb := tm.latencyFallback
	tm.latencyFallbackMu.RUnlock()

	if cb == nil {
		return
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()

		latencyMs := cb(ctx, remote, tp.Entry.ID)
		if latencyMs > 0 {
			tp.SetLatency(latencyMs)
			tm.Logger.Debugf("Transport %s latency (RSN fallback): %.2f ms", tp.Entry.ID, latencyMs)
		}
	}()
}

// When set, transport re-creation is blocked for transports that are currently
// referenced by routing rules, protecting in-flight route traffic.
func (tm *Manager) SetRouteChecker(rc RouteChecker) {
	tm.routeCheckerMu.Lock()
	defer tm.routeCheckerMu.Unlock()
	tm.routeChecker = rc
}

// hasActiveRoutes calls the registered RouteChecker to test if tpID is in use.
// Returns false (safe to tear down) when no checker is registered.
func (tm *Manager) hasActiveRoutes(tpID uuid.UUID) bool {
	tm.routeCheckerMu.RLock()
	rc := tm.routeChecker
	tm.routeCheckerMu.RUnlock()
	if rc == nil {
		return false
	}
	return rc(tpID)
}

// InitClient initilizes a network client
func (tm *Manager) InitClient(ctx context.Context, netType types.Type, port int) {
	client, err := tm.factory.MakeClient(netType, port)
	if err != nil {
		tm.Logger.Warnf("Cannot initialize %s transport client", netType)
	}
	tm.mx.Lock()
	tm.netClients[netType] = client
	tm.mx.Unlock()
	tm.runClient(ctx, netType)

	// Transport Manager is 'ready' once we have successfully initilized
	// with at least one transport client.
	tm.readyOnce.Do(func() { close(tm.ready) })
}

// Ready checks if the transport manager is ready with atleast one transport
func (tm *Manager) Ready() <-chan struct{} {
	return tm.ready
}

func (tm *Manager) runClient(ctx context.Context, netType types.Type) {
	if tm.isClosing() {
		return
	}
	tm.mx.Lock()
	client := tm.netClients[netType]
	tm.mx.Unlock()
	tm.Logger.Debugf("Serving %s network", client.Type())
	err := client.Start()
	if err != nil {
		tm.Logger.WithError(err).Errorf("Failed to listen on %s network", client.Type())
	}
	lis, err := client.Listen(skyenv.TransportPort)
	if err != nil {
		tm.Logger.WithError(err).Fatalf("failed to listen on network '%s' of port '%d'",
			client.Type(), skyenv.TransportPort)
		return
	}
	tm.Logger.Debugf("listening on network: %s", client.Type())
	if client.Type() != types.DMSG {
		tm.wg.Add(1)
	}
	go tm.acceptTransports(ctx, lis, netType)
}

func (tm *Manager) acceptTransports(ctx context.Context, lis network.Listener, t types.Type) {
	// we do not close dmsg client explicitly, so we don't have to wait for it to finish
	if t != types.DMSG {
		defer tm.wg.Done()
	}
	for {
		select {
		case <-ctx.Done():
		case <-tm.done:
			return
		default:
			if err := tm.acceptTransport(ctx, lis); err != nil {
				log := tm.Logger.WithError(err)
				if errors.Is(err, dmsg.ErrEntityClosed) {
					log.Debug("Dmsg client stopped serving.")
					return
				}
				if errors.Is(err, io.ErrClosedPipe) {
					return
				}
				log.Warnf("Failed to accept transport")
				// Continue accepting other transports instead of stopping
				continue
			}
		}
	}
}

func (tm *Manager) cleanupTransports(ctx context.Context) {
	defer tm.wg.Done()
	ticker := time.NewTicker(1 * time.Second)
	for {
		select {
		case <-ticker.C:
			tm.mx.Lock()
			var toDelete []*ManagedTransport
			for _, tp := range tm.tps {
				if tp.IsClosed() {
					toDelete = append(toDelete, tp)
				}
			}
			for _, tp := range toDelete {
				delete(tm.tps, tp.Entry.ID)
			}
			tm.mx.Unlock()
			if len(toDelete) > 0 {
				tm.Logger.Debugf("Deleted %d unused transport entries", len(toDelete))
			}
		case <-ctx.Done():
		case <-tm.done:
			return
		}
	}
}

// Networks returns all the network types contained within the TransportManager.
func (tm *Manager) Networks() []types.Type {
	tm.mx.Lock()
	defer tm.mx.Unlock()
	var nets []types.Type
	for netType := range tm.netClients {
		nets = append(nets, netType)
	}
	return nets
}

// Stcpr returns stcpr client
func (tm *Manager) Stcpr() (network.Client, bool) {
	tm.mx.Lock()
	defer tm.mx.Unlock()
	c, ok := tm.netClients[types.STCPR]
	return c, ok
}

// TransportCount returns the number of active transports.
func (tm *Manager) TransportCount() int {
	tm.mx.RLock()
	defer tm.mx.RUnlock()
	return len(tm.tps)
}

func (tm *Manager) acceptTransport(ctx context.Context, lis network.Listener) error {
	transport, err := lis.AcceptTransport()
	if err != nil {
		return err
	}

	tm.Logger.Debugf("recv transport request: type(%s) remote(%s)", lis.Network(), transport.RemotePK())

	tm.mx.Lock()
	defer tm.mx.Unlock()

	if tm.isClosing() {
		return errors.New("transport.Manager is closing. Skipping incoming transport")
	}

	// For transports for purpose(data).

	tpID := tm.tpIDFromPK(transport.RemotePK(), transport.Network())

	client, ok := tm.netClients[transport.Network()]
	if !ok {
		return fmt.Errorf("client not found for the type %s", transport.Network())
	}

	mTp, ok := tm.tps[tpID]
	if !ok {
		tm.Logger.Debugln("No TP found, creating new one")

		// Use no-op discovery client for self-transports
		// Self-transports don't need TPD registration and would cause deadlock
		dc := tm.Conf.DiscoveryClient
		if transport.RemotePK() == client.PK() {
			dc = NewNoopDiscoveryClient()
			tm.Logger.Debug("Using no-op discovery client for self-transport (accept)")
		}

		mTp = NewManagedTransport(ManagedTransportConfig{
			client:         client,
			DC:             dc,
			LS:             tm.Conf.LogStore,
			RemotePK:       transport.RemotePK(),
			TransportLabel: LabelUser,
			ebc:            tm.ebc,
			mlog:           tm.factory.MLogger,
			QueueDeletion:  tm.queueDeletion,
		})
		mTp.manager = tm
		tm.cascadeHandlerMu.RLock()
		mTp.cascadeHandler = tm.cascadeHandler
		tm.cascadeHandlerMu.RUnlock()
		tm.dhtHandlerMu.RLock()
		mTp.dhtHandler = tm.dhtHandler
		tm.dhtHandlerMu.RUnlock()
		tm.setupRPCHandlerMu.RLock()
		mTp.setupRPCHandler = tm.setupRPCHandler
		tm.setupRPCHandlerMu.RUnlock()
		tm.visorRPCHandlerMu.RLock()
		mTp.visorRPCHandler = tm.visorRPCHandler
		tm.visorRPCHandlerMu.RUnlock()
		tm.skynetFwdHandlerMu.RLock()
		mTp.skynetFwdHandler = tm.skynetFwdHandler
		tm.skynetFwdHandlerMu.RUnlock()
		tm.appDirectHandlerMu.RLock()
		mTp.appDirectHandler = tm.appDirectHandler
		tm.appDirectHandlerMu.RUnlock()

		go func() {
			mTp.Serve(tm.readCh)

			tm.mx.Lock()
			delete(tm.tps, mTp.Entry.ID)
			tm.mx.Unlock()
		}()

		tm.tps[tpID] = mTp

		// Check AR transport limit after accepting a new transport.
		go tm.checkARLimit()
	} else {
		// Transport already exists. Before allowing Accept() to tear down the
		// underlying connection (via setTransport), check whether any routing
		// rule currently references this transport ID. If yes, tearing it down
		// would break active routes, so we return the existing transport instead.
		if tm.hasActiveRoutes(tpID) {
			tm.Logger.WithField("tp_id", tpID).
				WithField("remote_pk", transport.RemotePK()).
				Warn("Rejecting transport re-creation: existing transport has active routes")
			if err := transport.Close(); err != nil {
				tm.Logger.WithError(err).Warn("Failed to close incoming transport rejected due to active routes")
			}
			return nil
		}
		tm.Logger.Debugln("TP found, accepting...")
	}

	if err := mTp.Accept(ctx, transport); err != nil {
		// Close the transport to prevent CLOSE_WAIT connection leak
		if closeErr := transport.Close(); closeErr != nil {
			tm.Logger.WithError(closeErr).Warn("Failed to close transport after Accept error")
		}
		return err
	}

	tm.Logger.Debugf("accepted tp: type(%s) remote(%s) tpID(%s) new(%v)", lis.Network(), transport.RemotePK(), tpID, !ok)

	// Nudge the re-registration loop to batch-register this transport with TPD soon.
	// Registration is deferred to avoid per-transport HTTP calls that hit rate limits.
	select {
	case tm.regNudge <- struct{}{}:
	default: // nudge already pending
	}

	// NOTE: Do NOT measure latency on the accepting side.
	// Only the initiating side measures latency to avoid race conditions where
	// both visors try to set up ping routes simultaneously on the same port.

	return nil
}

// ErrNotFound is returned when requested transport is not found
var ErrNotFound = errors.New("transport not found")

// ErrUnknownNetwork occurs on attempt to use an unknown network type.
var ErrUnknownNetwork = errors.New("unknown network type")

// IsKnownNetwork returns true when netName is a known
// network type that we are able to operate in.
//
// Wrapper around the lockless helper for external callers. Methods
// that already hold tm.mx (read or write) MUST call
// isKnownNetworkLocked directly — calling this exported wrapper
// from inside a held read lock deadlocks against any pending
// writer (Go's RWMutex prioritizes pending writers to prevent
// writer starvation, so a recursive RLock attempt blocks when a
// writer is waiting). Production case that bit us: GetTransport
// previously called IsKnownNetwork from inside its own RLock; with
// cleanupTransports or acceptTransport pending the write lock, the
// recursive read attempt deadlocked, freezing every subsequent
// transport operation including ServeRPCClient's redial attempts.
func (tm *Manager) IsKnownNetwork(netName types.Type) bool {
	tm.mx.RLock()
	defer tm.mx.RUnlock()
	return tm.isKnownNetworkLocked(netName)
}

// isKnownNetworkLocked is the lockless variant of IsKnownNetwork.
// Caller must hold tm.mx (read or write).
func (tm *Manager) isKnownNetworkLocked(netName types.Type) bool {
	_, ok := tm.netClients[netName]
	return ok
}

// GetTransport gets transport entity to the given remote
func (tm *Manager) GetTransport(remote cipher.PubKey, netType types.Type) (*ManagedTransport, error) {
	tm.mx.RLock()
	defer tm.mx.RUnlock()
	if !tm.isKnownNetworkLocked(netType) {
		return nil, ErrUnknownNetwork
	}

	tpID := tm.tpIDFromPK(remote, netType)
	tp, ok := tm.tps[tpID]
	if !ok {
		return nil, fmt.Errorf("transport to %s of type %s error: %w", remote, netType, ErrNotFound)
	}
	return tp, nil
}

// GetTransportByID retrieves transport by its ID, if it exists
func (tm *Manager) GetTransportByID(tpID uuid.UUID) (*ManagedTransport, error) {
	tm.mx.RLock()
	defer tm.mx.RUnlock()
	tp, ok := tm.tps[tpID]
	if !ok {
		return nil, ErrNotFound
	}
	return tp, nil
}

// GetTransportsByLabel returns all transports that have given label
func (tm *Manager) GetTransportsByLabel(label Label) []*ManagedTransport {
	tm.mx.RLock()
	defer tm.mx.RUnlock()
	var trs []*ManagedTransport
	for _, tr := range tm.tps {
		if tr.Entry.Label == label {
			trs = append(trs, tr)
		}
	}
	return trs
}

// GetTransportsByLabels returns all transports matching any of the given labels
func (tm *Manager) GetTransportsByLabels(labels ...Label) []*ManagedTransport {
	tm.mx.RLock()
	defer tm.mx.RUnlock()
	var trs []*ManagedTransport
	for _, tr := range tm.tps {
		for _, label := range labels {
			if tr.Entry.Label == label {
				trs = append(trs, tr)
				break
			}
		}
	}
	return trs
}

// ARClient returns the address resolver client used by this transport manager.
func (tm *Manager) ARClient() addrresolver.APIClient {
	return tm.arClient
}

// SaveTransportOptions contains options for transport creation.
type SaveTransportOptions struct {
	NoRegister bool // skip transport discovery registration
}

// SaveTransport begins to attempt to establish data transports to the given 'remote' visor.
func (tm *Manager) SaveTransport(ctx context.Context, remote cipher.PubKey, netType types.Type, label Label) (*ManagedTransport, error) {
	return tm.SaveTransportWithOptions(ctx, remote, netType, label, SaveTransportOptions{})
}

// SaveTransportNoRegister is like SaveTransport but skips transport discovery registration.
// This is only valid for user-labeled transports.
func (tm *Manager) SaveTransportNoRegister(ctx context.Context, remote cipher.PubKey, netType types.Type, label Label) (*ManagedTransport, error) {
	return tm.SaveTransportWithOptions(ctx, remote, netType, label, SaveTransportOptions{NoRegister: true})
}

// SaveTransportWithOptions creates a transport with the given options.
func (tm *Manager) SaveTransportWithOptions(ctx context.Context, remote cipher.PubKey, netType types.Type, label Label, opts SaveTransportOptions) (*ManagedTransport, error) {
	if tm.isClosing() {
		return nil, io.ErrClosedPipe
	}
	for {
		mTp, err := tm.saveTransportInternal(ctx, remote, netType, label, opts)

		if err != nil {
			if err == ErrNotServing {
				continue
			}
			return nil, fmt.Errorf("save transport: %w", err)
		}
		return mTp, nil
	}
}

func (tm *Manager) saveTransportInternal(ctx context.Context, remote cipher.PubKey, netType types.Type, label Label, opts SaveTransportOptions) (*ManagedTransport, error) {
	if !tm.IsKnownNetwork(netType) {
		return nil, ErrUnknownNetwork
	}

	tpID := tm.tpIDFromPK(remote, netType)
	tm.Logger.Debugf("Initializing TP with ID %s", tpID)

	oldMTp, err := tm.GetTransportByID(tpID)
	if err == nil {
		tm.Logger.Debug("Found an old mTp from internal map.")
		return oldMTp, nil
	}

	tm.mx.RLock()
	client, ok := tm.netClients[netType]
	tm.mx.RUnlock()
	if !ok {
		return nil, fmt.Errorf("client not found for the type %s", netType)
	}

	// Use no-op discovery client for self-transports or when NoRegister is requested
	// Self-transports can't be used for routing (routes can't go through same key twice)
	// so they shouldn't be registered in transport discovery
	dc := tm.Conf.DiscoveryClient
	if remote == client.PK() {
		dc = NewNoopDiscoveryClient()
		tm.Logger.Debug("Using no-op discovery client for self-transport")
	} else if opts.NoRegister {
		dc = NewNoopDiscoveryClient()
		tm.Logger.Debug("Using no-op discovery client (NoRegister requested)")
	}

	mTp := NewManagedTransport(ManagedTransportConfig{
		client:         client,
		ebc:            tm.ebc,
		DC:             dc,
		LS:             tm.Conf.LogStore,
		RemotePK:       remote,
		TransportLabel: label,
		mlog:           tm.factory.MLogger,
		QueueDeletion:  tm.queueDeletion,
	})
	mTp.manager = tm
	tm.cascadeHandlerMu.RLock()
	mTp.cascadeHandler = tm.cascadeHandler
	tm.cascadeHandlerMu.RUnlock()
	tm.dhtHandlerMu.RLock()
	mTp.dhtHandler = tm.dhtHandler
	tm.dhtHandlerMu.RUnlock()
	tm.setupRPCHandlerMu.RLock()
	mTp.setupRPCHandler = tm.setupRPCHandler
	tm.setupRPCHandlerMu.RUnlock()
	tm.visorRPCHandlerMu.RLock()
	mTp.visorRPCHandler = tm.visorRPCHandler
	tm.visorRPCHandlerMu.RUnlock()
	tm.skynetFwdHandlerMu.RLock()
	mTp.skynetFwdHandler = tm.skynetFwdHandler
	tm.skynetFwdHandlerMu.RUnlock()
	tm.appDirectHandlerMu.RLock()
	mTp.appDirectHandler = tm.appDirectHandler
	tm.appDirectHandlerMu.RUnlock()

	tm.Logger.Debugf("Dialing transport to %v via %v", mTp.Remote(), mTp.client.Type())
	errCh := make(chan error)
	go mTp.DialAsync(ctx, errCh)
	err = <-errCh
	if err != nil {
		tm.Logger.Debugf("Error dialing transport to %v via %v: %v", mTp.Remote(), mTp.client.Type(), err)
		// Use closeWithoutDeregister since the transport was never registered with TPD
		// (registration only happens during settlement handshake after successful dial)
		mTp.closeWithoutDeregister()
		return nil, err
	}
	go mTp.Serve(tm.readCh)
	tm.mx.Lock()
	tm.tps[tpID] = mTp
	tm.mx.Unlock()

	// Check AR transport limit after dialing a new transport.
	go tm.checkARLimit()
	tm.Logger.Debugf("saved transport: remote(%s) type(%s) tpID(%s)", remote, netType, tpID)

	// Latency is now measured at the transport level via transport-level
	// ping/pong frames in the managed transport's pingLoop, so no callback needed.

	return mTp, nil
}

// STCPRRemoteAddrs gets remote IPs for all known STCPR transports.
func (tm *Manager) STCPRRemoteAddrs() []string {
	var addrs []string

	tm.mx.RLock()
	defer tm.mx.RUnlock()

	for _, tp := range tm.tps {
		if tp.transport != nil {
			remoteRaw := tp.transport.RemoteRawAddr().String()
			if tp.Entry.Type == types.STCPR && remoteRaw != "" {
				addrs = append(addrs, remoteRaw)
			}
		}
	}

	return addrs
}

// DeleteTransport deregisters the Transport of Transport ID in transport discovery and deletes it locally.
func (tm *Manager) DeleteTransport(id uuid.UUID) {
	tm.mx.Lock()
	if tm.isClosing() {
		tm.mx.Unlock()
		return
	}

	// Get transport and remove from map immediately
	tp, ok := tm.tps[id]
	if ok {
		delete(tm.tps, id)
	}
	tm.mx.Unlock()

	if !ok {
		return
	}

	// Close transport asynchronously
	// For individual deletions (RPC calls), we want to return quickly
	// The reconciliation process will catch any TPD cleanup failures
	go tp.close()
}

// DeleteAllTransports deregisters all Transports in transport discovery and deletes them locally.
func (tm *Manager) DeleteAllTransports() {
	tm.mx.Lock()
	if tm.isClosing() {
		tm.mx.Unlock()
		return
	}

	// Get all transports and clear map immediately
	tps := make([]*ManagedTransport, 0, len(tm.tps))
	for _, tp := range tm.tps {
		tps = append(tps, tp)
	}
	tm.tps = make(map[uuid.UUID]*ManagedTransport)
	tm.mx.Unlock()

	// Close all transports concurrently
	var wg sync.WaitGroup
	for _, tp := range tps {
		wg.Add(1)
		go func(mtp *ManagedTransport) {
			defer wg.Done()
			mtp.close()
		}(tp)
	}

	// Wait for all transports to close, with timeout
	// This is critical for tests that restart - we need TPD cleanup to finish
	// But can't wait forever if TPD is unreachable
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// All transports closed; TPD deregistration queued for batch processing
		tm.Logger.Debug("All transports closed successfully")
	case <-time.After(30 * time.Second):
		// Timeout - some transports may not have completed TPD cleanup
		// Reconciliation process will clean up stale entries later
		tm.Logger.Warnf("Timeout waiting for %d transports to close after 30s", len(tps))
	}
}

// ReadPacket reads data packets from routes.
func (tm *Manager) ReadPacket() (routing.Packet, error) {
	p, ok := <-tm.readCh
	if !ok {
		return nil, ErrNotServing
	}
	return p, nil
}

/*
	STATE
*/

// Transport obtains a Transport via a given Transport ID.
func (tm *Manager) Transport(id uuid.UUID) *ManagedTransport {
	tm.mx.RLock()
	tr := tm.tps[id]
	tm.mx.RUnlock()
	return tr
}

// WalkTransports ranges through all transports.
func (tm *Manager) WalkTransports(walk func(tp *ManagedTransport) bool) {
	tm.mx.RLock()
	for _, tp := range tm.tps {
		if ok := walk(tp); !ok {
			break
		}
	}
	tm.mx.RUnlock()
}

// Local returns Manager.config.PubKey
func (tm *Manager) Local() cipher.PubKey {
	return tm.Conf.PubKey
}

// Close closes opened transports, network clients
// and all service tasks of transport manager
func (tm *Manager) Close() {
	select {
	case <-tm.done:
		return
	default:
	}
	close(tm.done)
	tm.mx.Lock()
	defer tm.mx.Unlock()

	// Collect transport IDs for batch deregistration (skip noop clients)
	var tpIDs []uuid.UUID
	for _, tr := range tm.tps {
		// Skip transports with noop discovery client (self-transports)
		if _, isNoop := tr.dc.(*noopDiscoveryClient); !isNoop {
			tpIDs = append(tpIDs, tr.Entry.ID)
		}
	}

	// Batch deregister from TPD (with fallback to sequential)
	if len(tpIDs) > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		deleted, err := tm.Conf.DiscoveryClient.DeleteTransports(ctx, tpIDs)
		cancel()
		if err != nil {
			tm.Logger.WithError(err).Warnf("Batch deregister completed with error: %d/%d deleted", deleted, len(tpIDs))
		} else {
			tm.Logger.Debugf("Batch deregistered %d/%d transports from TPD", deleted, len(tpIDs))
		}
	}

	// Close underlying transports (skip TPD deregistration since we already did it)
	for _, tr := range tm.tps {
		tr.closeWithoutDeregister()
	}

	for _, client := range tm.netClients {
		err := client.Close()
		if err != nil {
			tm.Logger.WithError(err).Warnf("Failed to close %s client", client.Type())
		}
	}
	err := tm.arClient.Close()
	if err != nil {
		tm.Logger.WithError(err).Warnf("Failed to close arClient")
	}
	tm.wg.Wait()
	close(tm.readCh)
}

func (tm *Manager) isClosing() bool {
	select {
	case <-tm.done:
		return true
	default:
		return false
	}
}

func (tm *Manager) tpIDFromPK(pk cipher.PubKey, netType types.Type) uuid.UUID {
	return MakeTransportID(tm.Conf.PubKey, pk, netType)
}
