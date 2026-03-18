// Package transport pkg/transport/manager.go
package transport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/skycoin/dmsg/pkg/dmsg"

	"github.com/skycoin/skywire/pkg/app/appevent"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport/network"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

const reconnectPhaseDelay = 10 * time.Second
const reconnectRemoteTimeout = 3 * time.Second
const transportReRegisterInterval = 90 * time.Second

// PersistentTransports is a persistent transports description
type PersistentTransports struct {
	PK      cipher.PubKey `json:"pk"`
	NetType types.Type    `json:"type"`
}

// ManagerConfig configures a Manager.
type ManagerConfig struct {
	PubKey                    cipher.PubKey
	SecKey                    cipher.SecKey
	DiscoveryClient           DiscoveryClient
	LogStore                  LogStore
	LatencyLogStore           LatencyLogStore
	PersistentTransportsCache []PersistentTransports
	PTpsCacheMu               sync.RWMutex
	Version                   string // Visor version for reporting to TPD
}

// TransportCreatedCallback is called after a transport is successfully created.
// It receives the remote public key and transport ID, and can return a latency measurement.
// If latency > 0, it will be set on the transport.
type TransportCreatedCallback func(ctx context.Context, remote cipher.PubKey, tpID uuid.UUID) (latencyMs float64)

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

	// onTransportCreated is called after a transport is successfully established.
	// Used by the visor to measure transport latency via the router.
	onTransportCreated   TransportCreatedCallback
	onTransportCreatedMu sync.RWMutex

	// syncTPDData enables syncing all TPD data on transport re-registration
	syncTPDData   bool
	syncTPDDataMu sync.RWMutex

	// tpdCache stores the cached transport discovery data for local route calculation
	tpdCache   []*Entry
	tpdCacheMu sync.RWMutex

	// regNudge signals the re-registration loop to run soon (after a short debounce).
	// Sent after accepting a new transport so it gets batch-registered quickly.
	regNudge chan struct{}

	// delQueue collects transport IDs for deferred batch deletion from TPD.
	// Transports queue their ID here on close instead of making individual HTTP calls.
	delQueue   []uuid.UUID
	delQueueMu sync.Mutex
	delNudge   chan struct{}
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
	// for cleanup, reconnect, re-registration, and deferred deletion goroutines
	tm.wg.Add(4)
	go tm.cleanupTransports(ctx)
	go tm.runReconnectPersistent(ctx)
	go tm.runReRegisterTransports(ctx)
	go tm.runDeferredDeletions(ctx)
	tm.Logger.Debug("transport manager is serving.")
}

func (tm *Manager) runReconnectPersistent(ctx context.Context) {
	defer tm.wg.Done()
	ticker := time.NewTicker(reconnectPhaseDelay)
	tm.reconnectPersistent(ctx)
	for {
		select {
		case <-ticker.C:
			tm.reconnectPersistent(ctx)
			// wait full timeout no matter how long the last phase took
			ticker = time.NewTicker(reconnectPhaseDelay)
		case <-tm.done:
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
}

func (tm *Manager) reRegisterTransports(ctx context.Context) {
	tm.mx.RLock()
	entries := make([]*SignedEntry, 0, len(tm.tps))
	for _, tp := range tm.tps {
		if tp.IsClosed() {
			continue
		}
		// Create signed entry for re-registration with latency stats, current bandwidth, and version
		stats := tp.GetLatencyStats()
		var latencyData *LatencyData
		if stats.Avg > 0 {
			// Convert milliseconds to microseconds
			latencyData = &LatencyData{
				Min: int64(stats.Min * 1000),
				Max: int64(stats.Max * 1000),
				Avg: int64(stats.Avg * 1000),
			}
		}
		se := &SignedEntry{
			Entry:     &tp.Entry,
			Latency:   latencyData,
			Bandwidth: tp.GetBandwidth(),
			Version:   tm.Conf.Version,
		}
		entries = append(entries, se)
	}
	tm.mx.RUnlock()

	if len(entries) == 0 {
		return
	}

	tm.Logger.Debugf("Re-registering %d transports with discovery", len(entries))

	// Check if TPD sync is enabled
	if tm.GetSyncTPDData() {
		allEntries, err := tm.Conf.DiscoveryClient.RegisterTransportsWithSync(ctx, entries...)
		if err != nil {
			tm.Logger.WithError(err).Warn("Failed to re-register transports with sync")
		} else {
			tm.Logger.Debugf("Successfully re-registered %d transports, synced %d TPD entries", len(entries), len(allEntries))
			tm.SetTPDCache(allEntries)
		}
		return
	}

	err := tm.Conf.DiscoveryClient.RegisterTransports(ctx, entries...)
	if err != nil {
		tm.Logger.WithError(err).Warn("Failed to re-register transports with discovery")
	} else {
		tm.Logger.Debugf("Successfully re-registered %d transports", len(entries))
	}
}

func (tm *Manager) getPTpsCache() []PersistentTransports {
	tm.Conf.PTpsCacheMu.Lock()
	defer tm.Conf.PTpsCacheMu.Unlock()

	return tm.Conf.PersistentTransportsCache
}

// SetPTpsCache sets the PersistentTransportsCache
func (tm *Manager) SetPTpsCache(pTps []PersistentTransports) {
	tm.Conf.PTpsCacheMu.Lock()
	defer tm.Conf.PTpsCacheMu.Unlock()

	tm.Conf.PersistentTransportsCache = pTps
}

// SetOnTransportCreated sets the callback that's invoked after a transport is created.
// The callback can measure latency and return it; if > 0, it's set on the transport.
func (tm *Manager) SetOnTransportCreated(cb TransportCreatedCallback) {
	tm.onTransportCreatedMu.Lock()
	defer tm.onTransportCreatedMu.Unlock()
	tm.onTransportCreated = cb
}

// SetSyncTPDData enables or disables syncing TPD data on transport re-registration.
func (tm *Manager) SetSyncTPDData(enabled bool) {
	tm.syncTPDDataMu.Lock()
	defer tm.syncTPDDataMu.Unlock()
	tm.syncTPDData = enabled
	tm.Logger.Infof("SetSyncTPDData: %v", enabled)
}

// GetSyncTPDData returns whether TPD sync is enabled.
func (tm *Manager) GetSyncTPDData() bool {
	tm.syncTPDDataMu.RLock()
	defer tm.syncTPDDataMu.RUnlock()
	return tm.syncTPDData
}

// SetTPDCache updates the cached TPD data for local route calculation.
func (tm *Manager) SetTPDCache(entries []*Entry) {
	tm.tpdCacheMu.Lock()
	defer tm.tpdCacheMu.Unlock()
	tm.tpdCache = entries
	tm.Logger.Debugf("TPD cache updated with %d entries", len(entries))
}

// GetTPDCache returns the cached TPD data.
func (tm *Manager) GetTPDCache() []*Entry {
	tm.tpdCacheMu.RLock()
	defer tm.tpdCacheMu.RUnlock()
	return tm.tpdCache
}

// invokeTransportCreatedCallback calls the registered callback after transport creation.
// It runs asynchronously to not block transport setup.
// Skips latency measurement for user-created transports.
func (tm *Manager) invokeTransportCreatedCallback(remote cipher.PubKey, tp *ManagedTransport) {
	// Skip latency measurement for user-created transports
	if tp.Entry.Label == LabelUser {
		return
	}

	tm.onTransportCreatedMu.RLock()
	cb := tm.onTransportCreated
	tm.onTransportCreatedMu.RUnlock()

	if cb == nil {
		return
	}

	// Run callback asynchronously to not block transport setup
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		latencyMs := cb(ctx, remote, tp.Entry.ID)
		if latencyMs > 0 {
			tp.SetLatency(latencyMs)
			tm.Logger.Debugf("Transport %s latency set to %.2f ms", tp.Entry.ID, latencyMs)

			// Log latency to file if latency log store is configured
			if tm.Conf.LatencyLogStore != nil {
				stats := tp.GetLatencyStats()
				if err := tm.Conf.LatencyLogStore.Record(tp.Entry.ID, stats.Min, stats.Max, stats.Avg); err != nil {
					tm.Logger.WithError(err).Warn("Failed to log latency")
				}
			}
		}
	}()
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
	transport, err := lis.AcceptTransport() // TODO: tcp panic.
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

		go func() {
			mTp.Serve(tm.readCh)

			tm.mx.Lock()
			delete(tm.tps, mTp.Entry.ID)
			tm.mx.Unlock()
		}()

		tm.tps[tpID] = mTp
	} else {
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
// network type that we are able to operate in
func (tm *Manager) IsKnownNetwork(netName types.Type) bool {
	tm.mx.RLock()
	defer tm.mx.RUnlock()
	_, ok := tm.netClients[netName]
	return ok
}

// GetTransport gets transport entity to the given remote
func (tm *Manager) GetTransport(remote cipher.PubKey, netType types.Type) (*ManagedTransport, error) {
	tm.mx.RLock()
	defer tm.mx.RUnlock()
	if !tm.IsKnownNetwork(netType) {
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
	NoRegister       bool // skip transport discovery registration
	SkipLatencyProbe bool // skip latency probe after transport creation
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
	tm.Logger.Debugf("saved transport: remote(%s) type(%s) tpID(%s)", remote, netType, tpID)

	// Invoke callback to measure latency (runs asynchronously)
	// Skip for self-transports as they can't be used for routing
	// Skip if SkipLatencyProbe is set (e.g., for transport setup-node)
	if remote != client.PK() && !opts.SkipLatencyProbe {
		tm.invokeTransportCreatedCallback(remote, mTp)
	}

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
