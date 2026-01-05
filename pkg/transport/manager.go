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
	PersistentTransportsCache []PersistentTransports
	PTpsCacheMu               sync.RWMutex
}

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
	// for cleanup and reconnect goroutines
	tm.wg.Add(2)
	go tm.cleanupTransports(ctx)
	go tm.runReconnectPersistent(ctx)
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
		_, err := tm.saveTransport(deadlined, remote.PK, remote.NetType, LabelUser)
		if err != nil {
			tm.Logger.WithError(err).
				WithField("remote_pk", remote.PK).
				WithField("network_type", remote.NetType).
				Warnf("Cannot connect to persistent remote")
		}
		cancel()
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

		mTp = NewManagedTransport(ManagedTransportConfig{
			client:         client,
			DC:             tm.Conf.DiscoveryClient,
			LS:             tm.Conf.LogStore,
			RemotePK:       transport.RemotePK(),
			TransportLabel: LabelUser,
			ebc:            tm.ebc,
			mlog:           tm.factory.MLogger,
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

// SaveTransport begins to attempt to establish data transports to the given 'remote' visor.
func (tm *Manager) SaveTransport(ctx context.Context, remote cipher.PubKey, netType types.Type, label Label) (*ManagedTransport, error) {
	if tm.isClosing() {
		return nil, io.ErrClosedPipe
	}
	for {
		mTp, err := tm.saveTransport(ctx, remote, netType, label)

		if err != nil {
			if err == ErrNotServing {
				continue
			}
			return nil, fmt.Errorf("save transport: %w", err)
		}
		return mTp, nil
	}
}

func (tm *Manager) saveTransport(ctx context.Context, remote cipher.PubKey, netType types.Type, label Label) (*ManagedTransport, error) {
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

	// Use no-op discovery client for self-transports
	// Self-transports can't be used for routing (routes can't go through same key twice)
	// so they shouldn't be registered in transport discovery
	dc := tm.Conf.DiscoveryClient
	if remote == client.PK() {
		dc = NewNoopDiscoveryClient()
		tm.Logger.Debug("Using no-op discovery client for self-transport")
	}

	mTp := NewManagedTransport(ManagedTransportConfig{
		client:         client,
		ebc:            tm.ebc,
		DC:             dc,
		LS:             tm.Conf.LogStore,
		RemotePK:       remote,
		TransportLabel: label,
		mlog:           tm.factory.MLogger,
	})

	tm.Logger.Debugf("Dialing transport to %v via %v", mTp.Remote(), mTp.client.Type())
	errCh := make(chan error)
	go mTp.DialAsync(ctx, errCh)
	err = <-errCh
	if err != nil {
		tm.Logger.Debugf("Error dialing transport to %v via %v: %v", mTp.Remote(), mTp.client.Type(), err)
		if closeErr := mTp.Close(); closeErr != nil {
			tm.Logger.WithError(err).Warn("Error closing transport")
		}
		return nil, err
	}
	go mTp.Serve(tm.readCh)
	tm.mx.Lock()
	tm.tps[tpID] = mTp
	tm.mx.Unlock()
	tm.Logger.Debugf("saved transport: remote(%s) type(%s) tpID(%s)", remote, netType, tpID)
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

	// Get transport and remove from map immediately (before closing)
	tp, ok := tm.tps[id]
	if ok {
		delete(tm.tps, id)
	}
	tm.mx.Unlock()

	if !ok {
		return
	}

	// Close transport outside the lock with timeout
	// tp.close() makes HTTP calls to TPD which can block
	done := make(chan struct{})
	go func() {
		tp.close()
		close(done)
	}()

	// Wait up to 10 seconds for graceful close
	select {
	case <-done:
		// Clean shutdown completed
	case <-time.After(10 * time.Second):
		// Timeout - log warning but don't block
		// The goroutine will continue in background and eventually finish
		tm.Logger.WithField("transport_id", id).Warn("Transport close timed out after 10s, continuing anyway")
	}
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

	// Close all transports concurrently with timeout
	var wg sync.WaitGroup
	for _, tp := range tps {
		wg.Add(1)
		go func(mtp *ManagedTransport) {
			defer wg.Done()
			mtp.close()
		}(tp)
	}

	// Wait up to 10 seconds for all to close
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// All transports closed successfully
	case <-time.After(10 * time.Second):
		// Timeout - log warning but don't block
		tm.Logger.Warnf("Timeout waiting for %d transports to close after 10s, continuing anyway", len(tps))
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

	for _, tr := range tm.tps {
		tr.close()
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
