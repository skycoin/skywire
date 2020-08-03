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
	"github.com/skycoin/skywire/pkg/snet/snettest"
)

// ManagerConfig configures a Manager.
type ManagerConfig struct {
	PubKey          cipher.PubKey
	SecKey          cipher.SecKey
	DefaultVisors   []cipher.PubKey // Visors to automatically connect to
	DiscoveryClient DiscoveryClient
	LogStore        LogStore
}

// Manager manages Transports.
type Manager struct {
	Logger *logging.Logger
	Conf   *ManagerConfig
	nets   map[string]struct{}
	tps    map[uuid.UUID]*ManagedTransport
	n      *snet.Network

	readCh    chan routing.Packet
	mx        sync.RWMutex
	wgMu      sync.Mutex
	wg        sync.WaitGroup
	serveOnce sync.Once // ensure we only serve once.
	closeOnce sync.Once // ensure we only close once.
	done      chan struct{}
}

// NewManager creates a Manager with the provided configuration and transport factories.
// 'factories' should be ordered by preference.
func NewManager(n *snet.Network, config *ManagerConfig) (*Manager, error) {
	nets := make(map[string]struct{})
	for _, netType := range n.TransportNetworks() {
		nets[netType] = struct{}{}
	}
	tm := &Manager{
		Logger: logging.MustGetLogger("tp_manager"),
		Conf:   config,
		nets:   nets,
		tps:    make(map[uuid.UUID]*ManagedTransport),
		n:      n,
		readCh: make(chan routing.Packet, 20),
		done:   make(chan struct{}),
	}
	return tm, nil
}

// Serve runs listening loop across all registered factories.
func (tm *Manager) Serve(ctx context.Context) {
	tm.serveOnce.Do(func() {
		tm.serve(ctx)
	})
}

func (tm *Manager) serve(ctx context.Context) {
	var listeners []*snet.Listener

	for _, netType := range tm.n.TransportNetworks() {
		if tm.isClosing() {
			return
		}

		lis, err := tm.n.Listen(netType, skyenv.DmsgTransportPort)
		if err != nil {
			tm.Logger.WithError(err).Fatalf("failed to listen on network '%s' of port '%d'",
				netType, skyenv.DmsgTransportPort)
			continue
		}
		tm.Logger.Infof("listening on network: %s", netType)
		listeners = append(listeners, lis)

		if tm.isClosing() {
			return
		}

		tm.wgMu.Lock()
		tm.wg.Add(1)
		tm.wgMu.Unlock()

		go func() {
			defer tm.wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case <-tm.done:
					return
				default:
					if err := tm.acceptTransport(ctx, lis); err != nil {
						tm.Logger.Warnf("Failed to accept connection: %s", err)
						if strings.Contains(err.Error(), "closed") {
							return
						}
					}
				}
			}
		}()
	}

	tm.initTransports(ctx)
	tm.Logger.Info("transport manager is serving.")

	// closing logic
	<-tm.done

	tm.Logger.Info("transport manager is closing.")
	defer tm.Logger.Info("transport manager closed.")

	// Close all listeners.
	for i, lis := range listeners {
		if err := lis.Close(); err != nil {
			tm.Logger.Warnf("listener %d of network '%s' closed with error: %v", i, lis.Network(), err)
		}
	}
}

func (tm *Manager) initTransports(ctx context.Context) {
	tm.mx.Lock()
	defer tm.mx.Unlock()

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
		if _, err := tm.saveTransport(remote, tpType); err != nil {
			tm.Logger.Warnf("INIT: failed to init tp: type(%s) remote(%s) tpID(%s)", tpType, remote, tpID)
		}
		tm.Logger.Debugf("Successfully initialized TP %v", *entry.Entry)
	}
}

func (tm *Manager) acceptTransport(ctx context.Context, lis *snet.Listener) error {
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

	mTp, ok := tm.tps[tpID]
	if !ok {
		tm.Logger.Debugln("No TP found, creating new one")

		mTp = NewManagedTransport(tm.n, tm.Conf.DiscoveryClient, tm.Conf.LogStore, conn.RemotePK(), lis.Network())

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

// SaveTransport begins to attempt to establish data transports to the given 'remote' visor.
func (tm *Manager) SaveTransport(ctx context.Context, remote cipher.PubKey, tpType string) (*ManagedTransport, error) {
	tm.mx.Lock()
	defer tm.mx.Unlock()

	if tm.isClosing() {
		return nil, io.ErrClosedPipe
	}

	const tries = 2

	var err error
	for i := 0; i < tries; i++ {
		mTp, err := tm.saveTransport(remote, tpType)
		if err != nil {
			return nil, err
		}

		if err = mTp.Dial(ctx); err != nil {
			// TODO(nkryuchkov): Check for an error that underlying connection is not established
			// and try again in this case. Otherwise, return the error.
			pkTableErr := fmt.Sprintf("pk table: entry of %s does not exist", remote.String())

			if err.Error() == pkTableErr {
				mTp.wg.Wait()
				delete(tm.tps, mTp.Entry.ID)

				return nil, err
			}

			if err == ErrNotServing {
				mTp.wg.Wait()
				delete(tm.tps, mTp.Entry.ID)
				continue
			}

			tm.Logger.
				WithError(err).
				Warn("Underlying connection is not yet established. Will retry later.")
		}
		return mTp, nil
	}

	tm.Logger.
		WithError(err).
		WithField("tries", tries).
		Error("Failed to serve managed transport. This is unexpected.")
	return nil, err
}

func (tm *Manager) saveTransport(remote cipher.PubKey, netName string) (*ManagedTransport, error) {
	if _, ok := tm.nets[netName]; !ok {
		return nil, errors.New("unknown transport type")
	}

	tpID := tm.tpIDFromPK(remote, netName)

	tm.Logger.Debugf("Initializing TP with ID %s", tpID)

	tp, ok := tm.tps[tpID]
	if ok {
		tm.Logger.Debugln("Got TP from map")
		return tp, nil
	}

	mTp := NewManagedTransport(tm.n, tm.Conf.DiscoveryClient, tm.Conf.LogStore, remote, netName)
	go func() {
		mTp.Serve(tm.readCh)
		tm.mx.Lock()
		delete(tm.tps, mTp.Entry.ID)
		tm.mx.Unlock()
	}()
	tm.tps[tpID] = mTp

	tm.Logger.Infof("saved transport: remote(%s) type(%s) tpID(%s)", remote, netName, tpID)
	return mTp, nil
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

// Networks returns all the network types contained within the TransportManager.
func (tm *Manager) Networks() []string {
	return tm.n.TransportNetworks()
}

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
	tm.closeOnce.Do(func() {
		tm.close()
	})
	return nil
}

func (tm *Manager) close() {
	if tm == nil {
		return
	}

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
	network string,
) (m0 *Manager, m1 *Manager, tp0 *ManagedTransport, tp1 *ManagedTransport, err error) {
	// Prepare tp manager 0.
	pk0, sk0 := keys[0].PK, keys[0].SK
	ls0 := InMemoryTransportLogStore()
	m0, err = NewManager(nEnv.Nets[0], &ManagerConfig{
		PubKey:          pk0,
		SecKey:          sk0,
		DiscoveryClient: tpDisc,
		LogStore:        ls0,
	})
	if err != nil {
		return nil, nil, nil, nil, err
	}

	go m0.Serve(context.TODO())

	// Prepare tp manager 1.
	pk1, sk1 := keys[1].PK, keys[1].SK
	ls1 := InMemoryTransportLogStore()
	m1, err = NewManager(nEnv.Nets[1], &ManagerConfig{
		PubKey:          pk1,
		SecKey:          sk1,
		DiscoveryClient: tpDisc,
		LogStore:        ls1,
	})
	if err != nil {
		return nil, nil, nil, nil, err
	}

	go m1.Serve(context.TODO())

	// Create data transport between manager 1 & manager 2.
	tp1, err = m1.SaveTransport(context.TODO(), pk0, network)
	if err != nil {
		return nil, nil, nil, nil, err
	}

	tp0 = m0.Transport(MakeTransportID(pk0, pk1, network))

	return m0, m1, tp0, tp1, nil
}
