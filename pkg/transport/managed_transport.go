package transport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/skycoin/dmsg/cipher"
	"github.com/skycoin/dmsg/httputil"
	"github.com/skycoin/dmsg/netutil"
	"github.com/skycoin/skycoin/src/util/logging"

	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/snet"
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

// Constants associated with transport redial loop.
const (
	tpInitBO  = time.Millisecond * 500
	tpMaxBO   = time.Minute
	tpTries   = 0
	tpFactor  = 2
	tpTimeout = time.Second * 3 // timeout for a single try
)

// ManagedTransportConfig is a configuration for managed transport.
type ManagedTransportConfig struct {
	Net            *snet.Network
	DC             DiscoveryClient
	LS             LogStore
	RemotePK       cipher.PubKey
	NetName        string
	AfterClosed    TPCloseCallback
	TransportLabel Label
}

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

	redialCancel context.CancelFunc // for canceling redialling logic
	redialMx     sync.Mutex

	n      *snet.Network
	conn   *snet.Conn
	connCh chan struct{}
	connMx sync.Mutex

	done chan struct{}
	once sync.Once
	wg   sync.WaitGroup

	remoteAddr string

	afterClosedMu sync.RWMutex
	afterClosed   TPCloseCallback
}

// NewManagedTransport creates a new ManagedTransport.
func NewManagedTransport(conf ManagedTransportConfig, isInitiator bool) *ManagedTransport {
	initiator, target := conf.Net.LocalPK(), conf.RemotePK
	if !isInitiator {
		initiator, target = target, initiator
	}
	mt := &ManagedTransport{
		log:         logging.MustGetLogger(fmt.Sprintf("tp:%s", conf.RemotePK.String()[:6])),
		rPK:         conf.RemotePK,
		netName:     conf.NetName,
		n:           conf.Net,
		dc:          conf.DC,
		ls:          conf.LS,
		Entry:       MakeEntry(initiator, target, conf.NetName, true, conf.TransportLabel),
		LogEntry:    new(LogEntry),
		connCh:      make(chan struct{}, 1),
		done:        make(chan struct{}),
		afterClosed: conf.AfterClosed,
	}
	mt.wg.Add(2)
	return mt
}

// IsUp returns true if transport status is up.
func (mt *ManagedTransport) IsUp() bool {
	mt.isUpMux.Lock()
	isUp := mt.isUp && mt.isUpErr == nil
	mt.isUpMux.Unlock()
	return isUp
}

// Serve serves and manages the transport.
func (mt *ManagedTransport) Serve(readCh chan<- routing.Packet) {
	defer mt.wg.Done()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-mt.done
		cancel()
	}()

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
				log.WithError(err).Warn("Failed to read packet.")
				continue
			}
			select {
			case <-mt.done:
				return

			case readCh <- p:
			}
		}
	}()

	// Logging & redialing loop.
	logTicker := time.NewTicker(logWriteInterval)
	for {
		select {
		case <-mt.done:
			logTicker.Stop()
			return

		case <-logTicker.C:
			if mt.logMod() {
				if err := mt.ls.Record(mt.Entry.ID, mt.LogEntry); err != nil {
					mt.log.WithError(err).Warn("Failed to record log entry.")
				}
				continue
			}

			// Only initiator is responsible for redialing.
			if !mt.isInitiator() {
				continue
			}

			// If there has not been any activity, ensure underlying 'write' tp is still up.
			if err := mt.redialLoop(ctx); err != nil {
				mt.log.WithError(err).Debug("Stopped reconnecting underlying connection.")
			}
		}
	}
}

func (mt *ManagedTransport) onAfterClosed(f TPCloseCallback) {
	mt.afterClosedMu.Lock()
	mt.afterClosed = f
	mt.afterClosedMu.Unlock()
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

func (mt *ManagedTransport) close() {
	mt.disconnect()

	mt.afterClosedMu.RLock()
	afterClosed := mt.afterClosed
	mt.afterClosedMu.RUnlock()

	if afterClosed != nil {
		afterClosed(mt.netName, mt.remoteAddr)
	}
}

// disconnect stops serving the transport and ensures that transport status is updated to DOWN.
// It also waits until mt.Serve returns if specified.
func (mt *ManagedTransport) disconnect() {
	mt.once.Do(func() { close(mt.done) })
	_ = mt.updateStatus(false, 1) //nolint:errcheck
}

// Accept accepts a new underlying connection.
func (mt *ManagedTransport) Accept(ctx context.Context, conn *snet.Conn) error {
	mt.connMx.Lock()
	defer mt.connMx.Unlock()

	if conn.Network() != mt.netName {
		return ErrWrongNetwork
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

	mt.log.Debug("Performing settlement handshake...")
	if err := MakeSettlementHS(false).Do(ctx, mt.dc, conn, mt.n.LocalSK()); err != nil {
		return fmt.Errorf("settlement handshake failed: %w", err)
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
		return fmt.Errorf("snet.Dial: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, time.Second*20)
	defer cancel()

	if err := MakeSettlementHS(true).Do(ctx, mt.dc, tp, mt.n.LocalSK()); err != nil {
		return fmt.Errorf("settlement handshake failed: %w", err)
	}

	if err := mt.setConn(tp); err != nil {
		return fmt.Errorf("setConn: %w", err)
	}

	return nil
}

// redial only actually dials if transport is still registered in transport discovery.
// The 'retry' output specifies whether we can retry dial on failure.
func (mt *ManagedTransport) redial(ctx context.Context) error {
	if !mt.isServing() {
		return ErrNotServing
	}

	if _, err := mt.dc.GetTransportByID(ctx, mt.Entry.ID); err != nil {
		// If the error is a temporary network error, we should retry at a later stage.
		if netErr, ok := err.(net.Error); ok && netErr.Temporary() {

			return err
		}

		// If the error is not temporary, it most likely means that the transport is no longer registered.
		// Hence, we should close the managed transport.
		mt.disconnect()
		mt.log.
			WithError(err).
			Warn("Transport closed due to redial failure. Transport is likely no longer in discovery.")

		return ErrNotServing
	}

	return mt.dial(ctx)
}

// redialLoop calls redial in a loop with exponential back-off until success or transport closure.
func (mt *ManagedTransport) redialLoop(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	mt.redialMx.Lock()
	mt.redialCancel = cancel
	mt.redialMx.Unlock()

	retry := netutil.NewRetrier(mt.log, tpInitBO, tpMaxBO, tpTries, tpFactor).
		WithErrWhitelist(ErrNotServing, context.Canceled)

	// Only redial when there is no underlying conn.
	return retry.Do(ctx, func() (err error) {
		tryCtx, cancel := context.WithTimeout(ctx, tpTimeout)
		defer cancel()
		mt.connMx.Lock()
		if mt.conn == nil {
			err = mt.redial(tryCtx)
		}
		mt.connMx.Unlock()
		return err
	})
}

func (mt *ManagedTransport) isLeastSignificantEdge() bool {
	sorted := SortEdges(mt.Entry.Edges[0], mt.Entry.Edges[1])
	return sorted[0] == mt.n.LocalPK()
}

func (mt *ManagedTransport) isInitiator() bool {
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
		return fmt.Errorf("failed to update transport status: %w", err)
	}

	// Set new underlying connection.
	mt.conn = newConn
	select {
	case mt.connCh <- struct{}{}:
		mt.log.Debug("Sent signal to 'mt.connCh'.")
	default:
	}

	// Cancel reconnection logic.
	mt.redialMx.Lock()
	if mt.redialCancel != nil {
		mt.redialCancel()
	}
	mt.redialMx.Unlock()

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
	defer mt.isUpMux.Unlock()

	// If last update is the same as current, nothing needs to be done.
	if mt.isUp == isUp {
		return nil
	}

	for i := 0; i < tries; i++ {
		// @evanlinjin: We don't pass context as we always want transport status to be updated.
		if _, err = mt.dc.UpdateStatuses(context.Background(), &Status{ID: mt.Entry.ID, IsUp: isUp}); err != nil {

			// Only retry if error is temporary.
			if netErr, ok := err.(net.Error); ok && netErr.Temporary() {
				mt.log.
					WithError(err).
					WithField("temporary", true).
					WithField("retry", i+1 < tries).
					Warn("Failed to update transport status.")
				continue
			}

			// Close managed transport if associated entry is not in discovery.
			if httpErr, ok := err.(*httputil.HTTPError); ok && httpErr.Status == http.StatusNotFound {
				mt.log.
					WithError(err).
					WithField("temporary", false).
					WithField("retry", false).
					Warn("Failed to update transport status. Closing transport...")
				mt.isUp = false
				mt.isUpErr = httpErr
				mt.once.Do(func() { close(mt.done) }) // Only time when mt.done is closed outside of mt.close()
				return
			}

			break
		}
		mt.log.
			WithField("status", statusString(isUp)).
			Info("Transport status updated.")
		break
	}

	mt.isUp = isUp
	mt.isUpErr = err
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
		if err := mt.redial(ctx); err != nil {

			// TODO(evanlinjin): Determine whether we need to call 'mt.wg.Wait()' here.
			if err == ErrNotServing {
				mt.wg.Wait()
			}

			return fmt.Errorf("failed to redial underlying connection: %w", err)
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
	log := mt.log.WithField("func", "readPacket")

	var conn *snet.Conn
	for {
		if conn = mt.getConn(); conn != nil {
			break
		}
		select {
		case <-mt.done:
			return nil, ErrNotServing
		case <-mt.connCh:
		}
	}

	log.Debug("Awaiting packet...")

	h := make(routing.Packet, routing.PacketHeaderSize)
	if _, err = io.ReadFull(conn, h); err != nil {
		log.WithError(err).Debugf("Failed to read packet header.")
		return nil, err
	}
	log.WithField("header_len", len(h)).WithField("header_raw", h).Debug("Read packet header.")

	p := make([]byte, h.Size())
	if _, err = io.ReadFull(conn, p); err != nil {
		log.WithError(err).Debugf("Failed to read packet payload.")
		return nil, err
	}
	log.WithField("payload_len", len(p)).Debug("Read packet payload.")

	packet = append(h, p...)
	if n := len(packet); n > routing.PacketHeaderSize {
		mt.logRecv(uint64(n - routing.PacketHeaderSize))
	}

	log.WithField("type", packet.Type().String()).
		WithField("rt_id", packet.RouteID()).
		WithField("size", packet.Size()).
		Debug("Received packet.")
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
