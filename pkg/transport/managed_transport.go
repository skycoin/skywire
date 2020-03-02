package transport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SkycoinProject/skywire-mainnet/internal/skyenv"

	"github.com/SkycoinProject/skywire-mainnet/pkg/routing"
	"github.com/SkycoinProject/skywire-mainnet/pkg/snet"

	"github.com/SkycoinProject/dmsg/cipher"
	"github.com/SkycoinProject/skycoin/src/util/logging"
)

const logWriteInterval = time.Second * 3

// Records number of managedTransports.
var mTpCount int32

var (
	// ErrNotServing is the error returned when a transport is no longer served.
	ErrNotServing = errors.New("transport is no longer being served")

	// ErrConnAlreadyExists occurs when an underlying transport connection already exists.
	ErrConnAlreadyExists = errors.New("underlying transport connection already exists")
)

// ManagedTransport manages a direct line of communication between two visor nodes.
// There is a single underlying connection between two edges.
// Initial dialing can be requested by either edge of the connection.
// However, only the edge with the least-significant public key can redial.
type ManagedTransport struct {
	log *logging.Logger

	rPK        cipher.PubKey
	netName    string
	Entry      Entry
	LogEntry   *LogEntry
	logUpdates uint32

	dc DiscoveryClient
	ls LogStore

	isUp    bool  // records last successful status update to discovery
	isUpErr error // records whether the last status update was successful or not
	isUpMux sync.Mutex

	n      *snet.Network
	conn   *snet.Conn
	connCh chan struct{}
	connMx sync.Mutex

	done chan struct{}
	once sync.Once
	wg   sync.WaitGroup
}

// NewManagedTransport creates a new ManagedTransport.
func NewManagedTransport(n *snet.Network, dc DiscoveryClient, ls LogStore, rPK cipher.PubKey, netName string) *ManagedTransport {
	mt := &ManagedTransport{
		log:      logging.MustGetLogger(fmt.Sprintf("tp:%s", rPK.String()[:6])),
		rPK:      rPK,
		netName:  netName,
		n:        n,
		dc:       dc,
		ls:       ls,
		Entry:    makeEntry(n.LocalPK(), rPK, netName),
		LogEntry: new(LogEntry),
		connCh:   make(chan struct{}, 1),
		done:     make(chan struct{}),
	}
	mt.wg.Add(2)
	return mt
}

// Serve serves and manages the transport.
func (mt *ManagedTransport) Serve(readCh chan<- routing.Packet) {
	defer mt.wg.Done()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-mt.done
		cancel()
	}()

	logTicker := time.NewTicker(logWriteInterval)
	defer logTicker.Stop()

	log := mt.log.
		WithField("tp_id", mt.Entry.ID).
		WithField("remote_pk", mt.rPK).
		WithField("tp_index", atomic.AddInt32(&mTpCount, 1))

	log.Info("Serving.")

	defer func() {
		// Ensure logs tp logs are up to date before closing.
		if mt.logMod() {
			if err := mt.ls.Record(mt.Entry.ID, mt.LogEntry); err != nil {
				log.WithError(err).Warn("Failed to record log entry.")
			}
		}

		// End connection.
		mt.connMx.Lock()
		close(mt.connCh)
		if mt.conn != nil {
			if err := mt.conn.Close(); err != nil {
				log.WithError(err).Warn("Failed to close underlying connection.")
			}
			mt.conn = nil
		}
		mt.connMx.Unlock()

		log.WithField("remaining_tps", atomic.AddInt32(&mTpCount, -1)).
			Info("Stopped serving.")
	}()

	// Read loop.
	go func() {
		log := mt.log.WithField("src", "read_loop")
		defer func() {
			cancel()
			mt.wg.Done()
			log.Debug("Closed read loop.")
		}()
		for {
			p, err := mt.readPacket()
			if err != nil {
				if err == ErrNotServing {
					mt.log.WithError(err).Debug("Failed to read packet. Returning...")
					return
				}
				mt.connMx.Lock()
				mt.clearConn()
				mt.connMx.Unlock()
				log.WithError(err).
					Warn("Failed to read packet.")
				continue
			}
			select {
			case <-mt.done:
				return

			case readCh <- p:
			}
		}
	}()

	// Redial loop.
	for {
		select {
		case <-mt.done:
			return

		case <-logTicker.C:
			if mt.logMod() {
				if err := mt.ls.Record(mt.Entry.ID, mt.LogEntry); err != nil {
					mt.log.Warnf("Failed to record log entry: %s", err)
				}
				continue
			}

			// Only least significant edge is responsible for redialing.
			if !mt.isLeastSignificantEdge() {
				continue
			}

			// If there has not been any activity, ensure underlying 'write' tp is still up.
			mt.connMx.Lock()
			if mt.conn == nil {
				if ok, err := mt.redial(ctx); err != nil {
					mt.log.Warnf("failed to redial underlying connection (redial loop): %v", err)
					if !ok {
						mt.connMx.Unlock()
						return
					}
				}
			}
			mt.connMx.Unlock()

		}
	}
}

func (mt *ManagedTransport) isServing() bool {
	select {
	case <-mt.done:
		return false
	default:
		return true
	}
}

// Close implements io.Closer
// It also waits for transport to stop serving before it returns.
// It only returns an error if transport status update fails.
func (mt *ManagedTransport) Close() (err error) {
	mt.close()
	mt.wg.Wait()

	mt.isUpMux.Lock()
	err = mt.isUpErr
	mt.isUpMux.Unlock()

	return err
}

// close stops serving the transport and ensures that transport status is updated to DOWN.
// It also waits until mt.Serve returns if specified.
func (mt *ManagedTransport) close() {
	mt.once.Do(func() { close(mt.done) })
	_ = mt.updateStatus(false, 1) //nolint:errcheck
}

// Accept accepts a new underlying connection.
func (mt *ManagedTransport) Accept(ctx context.Context, conn *snet.Conn) error {
	mt.connMx.Lock()
	defer mt.connMx.Unlock()

	if conn.Network() != mt.netName {
		return errors.New("wrong network") // TODO: Make global var.
	}

	if !mt.isServing() {
		mt.log.WithError(ErrNotServing).Debug()
		if err := conn.Close(); err != nil {
			mt.log.WithError(err).
				Warn("Failed to close newly accepted connection.")
		}
		return ErrNotServing
	}

	ctx, cancel := context.WithTimeout(ctx, time.Second*20)
	defer cancel()
	mt.log.Debug("Performing handshake...")
	if err := MakeSettlementHS(false).Do(ctx, mt.dc, conn, mt.n.LocalSK()); err != nil {
		return fmt.Errorf("settlement handshake failed: %v", err)
	}

	mt.log.Debug("Setting underlying connection...")

	return mt.setConn(conn)
}

// Dial dials a new underlying connection.
func (mt *ManagedTransport) Dial(ctx context.Context) error {
	mt.connMx.Lock()
	defer mt.connMx.Unlock()

	if !mt.isServing() {
		return ErrNotServing
	}

	if mt.conn != nil {
		return nil
	}
	return mt.dial(ctx)
}

func (mt *ManagedTransport) dial(ctx context.Context) error {
	tp, err := mt.n.Dial(ctx, mt.netName, mt.rPK, skyenv.DmsgTransportPort)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(ctx, time.Second*20)
	defer cancel()

	if err := MakeSettlementHS(true).Do(ctx, mt.dc, tp, mt.n.LocalSK()); err != nil {
		return fmt.Errorf("settlement handshake failed: %v", err)
	}
	return mt.setConn(tp)
}

// redial only actually dials if transport is still registered in transport discovery.
// The 'retry' output specifies whether we can retry dial on failure.
func (mt *ManagedTransport) redial(ctx context.Context) (retry bool, err error) {
	if !mt.isServing() {
		return false, ErrNotServing
	}

	if _, err = mt.dc.GetTransportByID(ctx, mt.Entry.ID); err != nil {

		// If the error is a temporary network error, we should retry at a later stage.
		if netErr, ok := err.(net.Error); ok && netErr.Temporary() {

			return true, err
		}

		// If the error is not temporary, it most likely means that the transport is no longer registered.
		// Hence, we should close the managed transport.
		mt.close()
		mt.log.
			WithError(err).
			Warn("Transport closed due to redial failure. Transport is likely no longer in discovery.")

		return false, ErrNotServing
	}

	return true, mt.dial(ctx)
}

func (mt *ManagedTransport) isLeastSignificantEdge() bool {
	return mt.Entry.EdgeIndex(mt.n.LocalPK()) == 0
}

/*
	<<< UNDERLYING CONNECTION >>>
*/

func (mt *ManagedTransport) getConn() *snet.Conn {
	if !mt.isServing() {
		return nil
	}

	mt.connMx.Lock()
	conn := mt.conn
	mt.connMx.Unlock()
	return conn
}

// setConn sets 'mt.conn' (the underlying connection).
// If 'mt.conn' is already occupied, close the newly introduced connection.
func (mt *ManagedTransport) setConn(newConn *snet.Conn) error {

	if mt.conn != nil {
		if mt.isLeastSignificantEdge() {
			mt.log.Debug("Underlying conn already exists, closing new conn.")
			if err := newConn.Close(); err != nil {
				log.WithError(err).Warn("Failed to close new conn.")
			}
			return ErrConnAlreadyExists
		}

		mt.log.Debug("Underlying conn already exists, closing old conn.")
		if err := mt.conn.Close(); err != nil {
			log.WithError(err).Warn("Failed to close old conn.")
		}
		mt.conn = nil
	}

	if err := mt.updateStatus(true, 1); err != nil {
		return fmt.Errorf("failed to update transport status: %v", err)
	}

	mt.conn = newConn
	select {
	case mt.connCh <- struct{}{}:
		mt.log.Debug("Sent signal to 'mt.connCh'.")
	default:
	}
	return nil
}

func (mt *ManagedTransport) clearConn() {
	if !mt.isServing() {
		return
	}

	if mt.conn != nil {
		if err := mt.conn.Close(); err != nil {
			log.WithError(err).Warn("Failed to close connection")
		}
		mt.conn = nil
	}
	_ = mt.updateStatus(false, 1) //nolint:errcheck
}

func (mt *ManagedTransport) updateStatus(isUp bool, tries int) (err error) {
	if tries < 1 {
		panic(fmt.Errorf("mt.updateStatus: invalid input: got tries=%d (want tries > 0)", tries))
	}

	// If not serving, we should update status to 'DOWN' and ensure 'updateStatus' returns error.
	if !mt.isServing() {
		isUp = false
	}
	defer func() {
		if err == nil && !mt.isServing() {
			err = ErrNotServing
		}
	}()

	mt.isUpMux.Lock()

	// If last update is the same as current, nothing needs to be done.
	if mt.isUp == isUp {
		mt.isUpMux.Unlock()
		return nil
	}

	for i := 0; i < tries; i++ {
		// @evanlinjin: We don't pass context as we always want transport status to be updated.
		if _, err = mt.dc.UpdateStatuses(context.Background(), &Status{ID: mt.Entry.ID, IsUp: isUp}); err != nil {
			mt.log.
				WithError(err).
				WithField("retry", i < tries).
				Warn("Failed to update transport status.")
			continue
		}
		mt.log.
			WithField("status", statusString(isUp)).
			Info("Transport status updated.")
		break
	}

	mt.isUp = isUp
	mt.isUpErr = err
	mt.isUpMux.Unlock()
	return err
}

func statusString(isUp bool) string {
	if isUp {
		return "UP"
	}
	return "DOWN"
}

/*
	<<< PACKET MANAGEMENT >>>
*/

// WritePacket writes a packet to the remote.
func (mt *ManagedTransport) WritePacket(ctx context.Context, packet routing.Packet) error {
	mt.connMx.Lock()
	defer mt.connMx.Unlock()

	if mt.conn == nil {
		if _, err := mt.redial(ctx); err != nil {

			// TODO(evanlinjin): Determine whether we need to call 'mt.wg.Wait()' here.
			if err == ErrNotServing {
				mt.wg.Wait()
			}

			return fmt.Errorf("failed to redial underlying connection: %v", err)
		}
	}

	n, err := mt.conn.Write(packet)
	if err != nil {
		mt.clearConn()
		return err
	}
	if n > routing.PacketHeaderSize {
		mt.logSent(uint64(n - routing.PacketHeaderSize))
	}
	return nil
}

// WARNING: Not thread safe.
func (mt *ManagedTransport) readPacket() (packet routing.Packet, err error) {
	var conn *snet.Conn
	for {
		if conn = mt.getConn(); conn != nil {
			mt.log.Debugf("Got conn in managed TP: %s", conn.RemoteAddr())
			break
		}
		select {
		case <-mt.done:
			return nil, ErrNotServing
		case <-mt.connCh:
		}
	}

	h := make(routing.Packet, routing.PacketHeaderSize)
	mt.log.Debugln("Trying to read packet header...")
	if _, err = io.ReadFull(conn, h); err != nil {
		mt.log.WithError(err).Debugf("Failed to read packet header: %v", err)
		return nil, err
	}
	mt.log.Debugf("Read packet header: %s", string(h))
	p := make([]byte, h.Size())
	if _, err = io.ReadFull(conn, p); err != nil {
		mt.log.WithError(err).Debugf("Error reading packet payload: %v", err)
		return nil, err
	}
	mt.log.Debugf("Read packet payload: %s", string(p))
	packet = append(h, p...)
	if n := len(packet); n > routing.PacketHeaderSize {
		mt.logRecv(uint64(n - routing.PacketHeaderSize))
	}
	mt.log.Infof("recv packet: type (%s) rtID(%d) size(%d)", packet.Type().String(), packet.RouteID(), packet.Size())
	return packet, nil
}

/*
	<<< TRANSPORT LOGGING >>>
*/

func (mt *ManagedTransport) logSent(b uint64) {
	mt.LogEntry.AddSent(b)
	atomic.AddUint32(&mt.logUpdates, 1)
}

func (mt *ManagedTransport) logRecv(b uint64) {
	mt.LogEntry.AddRecv(b)
	atomic.AddUint32(&mt.logUpdates, 1)
}

func (mt *ManagedTransport) logMod() bool {
	if ops := atomic.SwapUint32(&mt.logUpdates, 0); ops > 0 {
		mt.log.Infof("entry log: recording %d operations", ops)
		return true
	}
	return false
}

// Remote returns the remote public key.
func (mt *ManagedTransport) Remote() cipher.PubKey { return mt.rPK }

// Type returns the transport type.
func (mt *ManagedTransport) Type() string { return mt.netName }
