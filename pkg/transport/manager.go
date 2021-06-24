package transport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/skycoin/dmsg/cipher"
	"github.com/skycoin/skycoin/src/util/logging"

	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/snet"
	"github.com/skycoin/skywire/pkg/snet/arclient"
	"github.com/skycoin/skywire/pkg/snet/directtp"
	"github.com/skycoin/skywire/pkg/snet/directtp/tptypes"
	"github.com/skycoin/skywire/pkg/snet/snettest"
	"github.com/skycoin/skywire/pkg/transport/network"
)

// TPCloseCallback triggers after a session is closed.
type TPCloseCallback func(network, addr string)

// ManagerConfig configures a Manager.
type ManagerConfig struct {
	PubKey          cipher.PubKey
	SecKey          cipher.SecKey
	DiscoveryClient DiscoveryClient
	LogStore        LogStore
}

// Manager manages Transports.
type Manager struct {
	Logger   *logging.Logger
	Conf     *ManagerConfig
	tps      map[uuid.UUID]*ManagedTransport
	arClient arclient.APIClient

	readCh    chan routing.Packet
	mx        sync.RWMutex
	wgMu      sync.Mutex
	wg        sync.WaitGroup
	serveOnce sync.Once // ensure we only serve once.
	closeOnce sync.Once // ensure we only close once.
	done      chan struct{}

	afterTPClosed TPCloseCallback
	factory       network.ClientFactory
	netClients    map[network.Type]network.Client
}

// NewManager creates a Manager with the provided configuration and transport factories.
// 'factories' should be ordered by preference.
func NewManager(log *logging.Logger, arClient arclient.APIClient, config *ManagerConfig, factory network.ClientFactory) (*Manager, error) {
	if log == nil {
		log = logging.MustGetLogger("tp_manager")
	}
	tm := &Manager{
		Logger:     log,
		Conf:       config,
		tps:        make(map[uuid.UUID]*ManagedTransport),
		readCh:     make(chan routing.Packet, 20),
		done:       make(chan struct{}),
		netClients: make(map[network.Type]network.Client),
		arClient:   arClient,
		factory:    factory,
	}
	return tm, nil
}

// OnAfterTPClosed sets callback which will fire after transport gets closed.
func (tm *Manager) OnAfterTPClosed(f TPCloseCallback) {
	tm.mx.Lock()
	defer tm.mx.Unlock()

	tm.afterTPClosed = f

	// set callback for all already known tps
	for _, tp := range tm.tps {
		tp.onAfterClosed(f)
	}
}

// Serve runs listening loop across all registered factories.
func (tm *Manager) Serve(ctx context.Context) {
	tm.serveOnce.Do(func() {
		tm.serve(ctx)
	})
}

func (tm *Manager) serve(ctx context.Context) {
	tm.initClients()
	tm.runClients(ctx)
	tm.initTransports(ctx)
	tm.Logger.Info("transport manager is serving.")
}

func (tm *Manager) initClients() {
	acceptedNetworks := []network.Type{network.STCP}
	for _, netType := range acceptedNetworks {
		tm.netClients[netType] = tm.factory.MakeClient(netType)
	}
}

func (tm *Manager) runClients(ctx context.Context) {
	if tm.isClosing() {
		return
	}
	for _, client := range tm.netClients {
		tm.Logger.Infof("Serving %s network", client.Type())
		err := client.Serve()
		if err != nil {
			tm.Logger.WithError(err).Errorf("Failed to listen on %s network", client.Type())
			continue
		}
		lis, err := client.Listen(skyenv.DmsgTransportPort)
		if err != nil {
			tm.Logger.WithError(err).Fatalf("failed to listen on network '%s' of port '%d'",
				client.Type(), skyenv.DmsgTransportPort)
			return
		}
		tm.Logger.Infof("listening on network: %s", client.Type())
		tm.wgMu.Lock()
		tm.wg.Add(1)
		tm.wgMu.Unlock()
		go tm.acceptTransports(ctx, lis)
	}
}

func (tm *Manager) acceptTransports(ctx context.Context, lis *network.Listener) {
	defer tm.wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tm.done:
			return
		default:
			if err := tm.acceptTransport(ctx, lis); err != nil {
				tm.Logger.Warnf("Failed to accept connection: %v", err)
				if strings.Contains(err.Error(), "closed") {
					return
				}
			}
		}
	}
}

// Networks returns all the network types contained within the TransportManager.
func (tm *Manager) Networks() []string {
	var nets []string
	for netType := range tm.netClients {
		nets = append(nets, string(netType))
	}
	return nets
}

func (tm *Manager) initTransports(ctx context.Context) {

	entries, err := tm.Conf.DiscoveryClient.GetTransportsByEdge(ctx, tm.Conf.PubKey)
	if err != nil {
		log.Warnf("No transports found for local visor: %v", err)
	}
	tm.Logger.Debugf("Initializing %d transports", len(entries))
	for _, entry := range entries {
		tm.Logger.Debugf("Initializing TP %v", *entry.Entry)
		var (
			tpType = entry.Entry.Type
			remote = entry.Entry.RemoteEdge(tm.Conf.PubKey)
			tpID   = entry.Entry.ID
		)
		if _, err := tm.saveTransport(remote, tpType, entry.Entry.Label); err != nil {
			tm.Logger.Warnf("INIT: failed to init tp: type(%s) remote(%s) tpID(%s)", tpType, remote, tpID)
		} else {
			tm.Logger.Debugf("Successfully initialized TP %v", *entry.Entry)
		}
	}
}

func (tm *Manager) STcpr() (directtp.Client, bool) {
	return nil, false
}

func (tm *Manager) acceptTransport(ctx context.Context, lis *network.Listener) error {
	conn, err := lis.AcceptConn() // TODO: tcp panic.
	if err != nil {
		return err
	}

	tm.Logger.Infof("recv transport connection request: type(%s) remote(%s)", lis.Network(), conn.RemotePK())

	tm.mx.Lock()
	defer tm.mx.Unlock()

	if tm.isClosing() {
		return errors.New("transport.Manager is closing. Skipping incoming transport")
	}

	// For transports for purpose(data).

	tpID := tm.tpIDFromPK(conn.RemotePK(), conn.Network())

	client, ok := tm.netClients[network.Type(conn.Network())]
	if !ok {
		return fmt.Errorf("client not found for the type %s", conn.Network())
	}

	mTp, ok := tm.tps[tpID]
	if !ok {
		tm.Logger.Debugln("No TP found, creating new one")

		mTp = NewManagedTransport(ManagedTransportConfig{
			client:         client,
			DC:             tm.Conf.DiscoveryClient,
			LS:             tm.Conf.LogStore,
			RemotePK:       conn.RemotePK(),
			NetName:        lis.Network(),
			AfterClosed:    tm.afterTPClosed,
			TransportLabel: LabelUser,
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

	if err := mTp.Accept(ctx, conn); err != nil {
		return err
	}

	tm.Logger.Infof("accepted tp: type(%s) remote(%s) tpID(%s) new(%v)", lis.Network(), conn.RemotePK(), tpID, !ok)

	return nil
}

// GetTransport gets transport entity to the given remote
func (tm *Manager) GetTransport(remote cipher.PubKey, tpType string) (*ManagedTransport, error) {
	tm.mx.RLock()
	defer tm.mx.RUnlock()
	if !snet.IsKnownNetwork(tpType) {
		return nil, snet.ErrUnknownNetwork
	}

	tpID := tm.tpIDFromPK(remote, tpType)
	t, ok := tm.tps[tpID]
	if !ok {
		return nil, fmt.Errorf("transport to %s of type %s not found", remote, tpType)
	}
	return t, nil
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
func (tm *Manager) SaveTransport(ctx context.Context, remote cipher.PubKey, tpType string, label Label) (*ManagedTransport, error) {

	if tm.isClosing() {
		return nil, io.ErrClosedPipe
	}

	for {
		mTp, err := tm.saveTransport(remote, tpType, label)
		if err != nil {
			return nil, fmt.Errorf("save transport: %w", err)
		}

		tm.Logger.Debugf("Dialing transport to %v via %v", mTp.Remote(), mTp.netName)

		if err = mTp.Dial(ctx); err != nil {
			tm.Logger.Debugf("Error dialing transport to %v via %v: %v", mTp.Remote(), mTp.netName, err)
			// This occurs when an old tp is returned by 'tm.saveTransport', meaning a tp of the same transport ID was
			// just deleted (and has not yet fully closed). Hence, we should close and delete the old tp and try again.
			if err == ErrNotServing {
				if closeErr := mTp.Close(); closeErr != nil {
					tm.Logger.WithError(err).Warn("Closing mTp returns non-nil error.")
				}
				tm.deleteTransport(mTp.Entry.ID)
				continue
			}

			// This occurs when the tp type is STCP and the requested remote PK is not associated with an IP address in
			// the STCP table. There is no point in retrying as a connection would be impossible, so we just return an
			// error.
			if isSTCPTableError(remote, err) {
				if closeErr := mTp.Close(); closeErr != nil {
					tm.Logger.WithError(err).Warn("Closing mTp returns non-nil error.")
				}
				tm.deleteTransport(mTp.Entry.ID)
				return nil, err
			}

			tm.Logger.WithError(err).Warn("Underlying transport connection is not established, will retry later.")
		}

		return mTp, nil
	}
}

// isSTCPPKError returns true if the error is a STCP table error.
// This occurs the requested remote public key does not exist in the STCP table.
func isSTCPTableError(remotePK cipher.PubKey, err error) bool {
	return err.Error() == fmt.Sprintf("pk table: entry of %s does not exist", remotePK.String())
}

func (tm *Manager) saveTransport(remote cipher.PubKey, netName string, label Label) (*ManagedTransport, error) {
	tm.mx.Lock()
	defer tm.mx.Unlock()
	if !snet.IsKnownNetwork(netName) {
		return nil, snet.ErrUnknownNetwork
	}

	tpID := tm.tpIDFromPK(remote, netName)
	tm.Logger.Debugf("Initializing TP with ID %s", tpID)

	oldMTp, ok := tm.tps[tpID]
	if ok {
		tm.Logger.Debug("Found an old mTp from internal map.")
		return oldMTp, nil
	}

	client, ok := tm.netClients[network.Type(netName)]
	if !ok {
		return nil, fmt.Errorf("client not found for the type %s", netName)
	}

	afterTPClosed := tm.afterTPClosed

	mTp := NewManagedTransport(ManagedTransportConfig{
		client:         client,
		DC:             tm.Conf.DiscoveryClient,
		LS:             tm.Conf.LogStore,
		RemotePK:       remote,
		NetName:        netName,
		AfterClosed:    afterTPClosed,
		TransportLabel: label,
	})

	if mTp.netName == tptypes.STCPR {
		if tm.arClient != nil {
			visorData, err := tm.arClient.Resolve(context.Background(), mTp.netName, remote)
			if err == nil {
				mTp.remoteAddr = visorData.RemoteAddr
			} else {
				if err != arclient.ErrNoEntry {
					return nil, fmt.Errorf("failed to resolve %s: %w", remote, err)
				}
			}
		}
	}
	go func() {
		mTp.Serve(tm.readCh)
		tm.deleteTransport(mTp.Entry.ID)
	}()
	tm.tps[tpID] = mTp
	tm.Logger.Infof("saved transport: remote(%s) type(%s) tpID(%s)", remote, netName, tpID)
	return mTp, nil
}

// STCPRRemoteAddrs gets remote IPs for all known STCPR transports.
func (tm *Manager) STCPRRemoteAddrs() []string {
	var addrs []string

	tm.mx.RLock()
	defer tm.mx.RUnlock()

	for _, tp := range tm.tps {
		if tp.Entry.Type == tptypes.STCPR && tp.remoteAddr != "" {
			addrs = append(addrs, tp.remoteAddr)
		}
	}

	return addrs
}

// DeleteTransport deregisters the Transport of Transport ID in transport discovery and deletes it locally.
func (tm *Manager) DeleteTransport(id uuid.UUID) {
	tm.mx.Lock()
	defer tm.mx.Unlock()

	if tm.isClosing() {
		return
	}

	// Deregister transport before closing the underlying connection.
	if tp, ok := tm.tps[id]; ok {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()

		// Deregister transport.
		if err := tm.Conf.DiscoveryClient.DeleteTransport(ctx, id); err != nil {
			tm.Logger.WithError(err).Warnf("Failed to deregister transport of ID %s from discovery.", id)
		} else {
			tm.Logger.Infof("De-registered transport of ID %s from discovery.", id)
		}

		// Close underlying connection.
		tp.close()
		delete(tm.tps, id)
	}
}

func (tm *Manager) deleteTransport(id uuid.UUID) {
	tm.mx.Lock()
	defer tm.mx.Unlock()
	delete(tm.tps, id)
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

// Close closes opened transports and registered factories.
func (tm *Manager) Close() error {
	tm.closeOnce.Do(tm.close)
	return nil
}

func (tm *Manager) close() {
	tm.Logger.Info("transport manager is closing.")
	defer tm.Logger.Info("transport manager closed.")

	tm.mx.Lock()
	defer tm.mx.Unlock()

	close(tm.done)

	statuses := make([]*Status, 0, len(tm.tps))
	for _, tr := range tm.tps {
		tr.close()
	}
	if _, err := tm.Conf.DiscoveryClient.UpdateStatuses(context.Background(), statuses...); err != nil {
		tm.Logger.Warnf("failed to update transport statuses: %v", err)
	}

	tm.wgMu.Lock()
	tm.wg.Wait()
	tm.wgMu.Unlock()

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

func (tm *Manager) tpIDFromPK(pk cipher.PubKey, tpType string) uuid.UUID {
	return MakeTransportID(tm.Conf.PubKey, pk, tpType)
}

// CreateTransportPair create a new transport pair for tests.
func CreateTransportPair(
	tpDisc DiscoveryClient,
	keys []snettest.KeyPair,
	nEnv *snettest.Env,
	net string,
) (m0 *Manager, m1 *Manager, tp0 *ManagedTransport, tp1 *ManagedTransport, err error) {
	// Prepare tp manager 0.
	pk0, sk0 := keys[0].PK, keys[0].SK
	ls0 := InMemoryTransportLogStore()
	m0, err = NewManager(nil, new(arclient.MockAPIClient), &ManagerConfig{
		PubKey:          pk0,
		SecKey:          sk0,
		DiscoveryClient: tpDisc,
		LogStore:        ls0,
	}, network.ClientFactory{})
	if err != nil {
		return nil, nil, nil, nil, err
	}

	go m0.Serve(context.TODO())

	// Prepare tp manager 1.
	pk1, sk1 := keys[1].PK, keys[1].SK
	ls1 := InMemoryTransportLogStore()
	m1, err = NewManager(nil, new(arclient.MockAPIClient), &ManagerConfig{
		PubKey:          pk1,
		SecKey:          sk1,
		DiscoveryClient: tpDisc,
		LogStore:        ls1,
	}, network.ClientFactory{})
	if err != nil {
		return nil, nil, nil, nil, err
	}

	go m1.Serve(context.TODO())

	// Create data transport between manager 1 & manager 2.
	tp1, err = m1.SaveTransport(context.TODO(), pk0, net, LabelUser)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	tp0 = m0.Transport(MakeTransportID(pk0, pk1, net))

	return m0, m1, tp0, tp1, nil
}
