// Package transport pkg/transport/managed_transport.go
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

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/httputil"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire-utilities/pkg/netutil"
	"github.com/skycoin/skywire/pkg/app/appevent"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/transport/network"
)

const (
	logWriteInterval = time.Second * 3
)

// Records number of managedTransports.
var mTpCount int32

var (
	// ErrNotServing is the error returned when a transport is no longer served.
	ErrNotServing = errors.New("transport is no longer being served")

	// ErrTransportAlreadyExists occurs when an underlying transport already exists.
	ErrTransportAlreadyExists = errors.New("underlying transport already exists")
)

// ManagedTransportConfig is a configuration for managed transport.
type ManagedTransportConfig struct {
	client          network.Client
	ebc             *appevent.Broadcaster
	DC              DiscoveryClient
	LS              LogStore
	RemotePK        cipher.PubKey
	TransportLabel  Label
	InactiveTimeout time.Duration
	mlog            *logging.MasterLogger
}

// ManagedTransport manages a direct line of communication between two visor nodes.
// There is a single underlying transport between two edges.
// Initial dialing can be requested by either edge of the transport.
type ManagedTransport struct {
	log *logging.Logger

	rPK        cipher.PubKey
	Entry      Entry
	LogEntry   *LogEntry
	logMx      sync.Mutex
	logUpdates uint32

	dc DiscoveryClient
	ls LogStore

	client      network.Client
	transport   network.Transport
	transportCh chan struct{}
	transportMx sync.Mutex

	done chan struct{}
	wg   sync.WaitGroup

	timeout time.Duration
}

// NewManagedTransport creates a new ManagedTransport.
func NewManagedTransport(conf ManagedTransportConfig) *ManagedTransport {
	aPK, bPK := conf.client.PK(), conf.RemotePK
	log := logging.MustGetLogger(fmt.Sprintf("tp:%s", conf.RemotePK.String()[:6]))
	if conf.mlog != nil {
		log = conf.mlog.PackageLogger(fmt.Sprintf("tp:%s", conf.RemotePK.String()[:6]))
	}
	entry := MakeEntry(aPK, bPK, conf.client.Type(), conf.TransportLabel)
	logEntry := MakeLogEntry(conf.LS, entry.ID, log)

	mt := &ManagedTransport{
		log:         log,
		rPK:         conf.RemotePK,
		dc:          conf.DC,
		ls:          conf.LS,
		client:      conf.client,
		Entry:       entry,
		LogEntry:    logEntry,
		transportCh: make(chan struct{}, 1),
		done:        make(chan struct{}),
		timeout:     conf.InactiveTimeout,
	}
	return mt
}

// Serve serves and manages the transport.
func (mt *ManagedTransport) Serve(readCh chan<- routing.Packet) {
	mt.wg.Add(3)
	log := mt.log.
		WithField("tp_id", mt.Entry.ID).
		WithField("remote_pk", mt.rPK).
		WithField("tp_index", atomic.AddInt32(&mTpCount, 1))

	log.Debug("Serving.")

	defer func() {
		mt.close()
		log.WithField("remaining_tps", atomic.AddInt32(&mTpCount, -1)).
			Debug("Stopped serving.")
	}()

	go mt.readLoop(readCh)
	mt.logLoop()
}

// readLoop continuously reads packets from the underlying transport
// and sends them to readCh
// This is a blocking call
func (mt *ManagedTransport) readLoop(readCh chan<- routing.Packet) {
	log := mt.log.WithField("src", "read_loop")
	defer mt.wg.Done()
	for {
		p, err := mt.readPacket()
		if err != nil {
			log.WithError(err).Warn("Failed to read packet, closing transport")
			mt.close()
			return
		}
		select {
		case <-mt.done:
			return
		case readCh <- p:
		}
	}
}

// logLoop continuously stores transport data in the log entry,
// in case there is data to store
// This is a blocking call
func (mt *ManagedTransport) logLoop() {
	defer func() {
		mt.recordLog()
		mt.wg.Done()
		mt.log.Debug("Stopped log loop")
	}()
	// Ensure logs tp logs are up to date before closing.
	logTicker := time.NewTicker(logWriteInterval)
	for {
		select {
		case <-mt.done:
			logTicker.Stop()
			return
		case <-logTicker.C:
			mt.recordLog()
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
	return nil
}

// IsClosed returns true when the transport is closed
// This instance cannot be used anymore and should be discarded
func (mt *ManagedTransport) IsClosed() bool {
	select {
	case <-mt.done:
		return true
	default:
		return false
	}
}

// close underlying transport and remove the entry from transport discovery
// todo: this currently performs http request to discovery service
// it only makes sense to wait for the completion if we are closing the visor itself,
// regular transport close operations should probably call it concurrently
// need to find a way to handle this properly (done channel in return?)
func (mt *ManagedTransport) close() {
	select {
	case <-mt.done:
		return
	default:
		close(mt.done)
	}
	mt.transportMx.Lock()
	close(mt.transportCh)
	if mt.transport != nil {
		if err := mt.transport.Close(); err != nil {
			mt.log.WithError(err).Warn("Failed to close underlying transport.")
		}
		mt.transport = nil
	}
	mt.transportMx.Unlock()
	_ = mt.deleteFromDiscovery() //nolint:errcheck
}

// Accept accepts a new underlying transport.
func (mt *ManagedTransport) Accept(ctx context.Context, transport network.Transport) error {
	mt.transportMx.Lock()
	defer mt.transportMx.Unlock()

	if transport.Network() != mt.Type() {
		return ErrWrongNetwork
	}

	if !mt.isServing() {
		mt.log.WithError(ErrNotServing).Debug()
		if err := transport.Close(); err != nil {
			mt.log.WithError(err).
				Warn("Failed to close newly accepted transport.")
		}
		return ErrNotServing
	}

	ctx, cancel := context.WithTimeout(ctx, time.Second*20)
	defer cancel()

	mt.log.Debug("Performing settlement handshake...")
	if err := MakeSettlementHS(false, mt.log).Do(ctx, mt.dc, transport, mt.client.SK()); err != nil {
		return fmt.Errorf("settlement handshake failed: %w", err)
	}

	mt.log.Debug("Setting underlying transport...")
	return mt.setTransport(transport)
}

// Dial dials a new underlying transport.
func (mt *ManagedTransport) Dial(ctx context.Context) error {
	mt.transportMx.Lock()
	defer mt.transportMx.Unlock()

	if !mt.isServing() {
		return ErrNotServing
	}

	if mt.transport != nil {
		return nil
	}
	return mt.dial(ctx)
}

// DialAsync is asynchronous version of dial that allows dialing in a different
// goroutine
func (mt *ManagedTransport) DialAsync(ctx context.Context, errCh chan error) {
	errCh <- mt.Dial(ctx)
}

func (mt *ManagedTransport) dial(ctx context.Context) error {
	transport, err := mt.client.Dial(ctx, mt.rPK, skyenv.TransportPort)
	if err != nil {
		return fmt.Errorf("mt.client.Dial: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, time.Second*20)
	defer cancel()

	if err := MakeSettlementHS(true, mt.log).Do(ctx, mt.dc, transport, mt.client.SK()); err != nil {
		return fmt.Errorf("settlement handshake failed: %w", err)
	}

	if err := mt.setTransport(transport); err != nil {
		return fmt.Errorf("setTransport: %w", err)
	}

	return nil
}

func (mt *ManagedTransport) isLeastSignificantEdge() bool {
	sorted := SortEdges(mt.Entry.Edges[0], mt.Entry.Edges[1])
	return sorted[0] == mt.client.PK()
}

/*
	<<< UNDERLYING TRANSPORT>>>
*/

func (mt *ManagedTransport) getTransport() network.Transport {
	if !mt.isServing() {
		return nil
	}

	mt.transportMx.Lock()
	transport := mt.transport
	mt.transportMx.Unlock()
	return transport
}

// set sets 'mt.transport' (the underlying transport).
// If 'mt.transport' is already occupied, close the newly introduced transport.
func (mt *ManagedTransport) setTransport(newTransport network.Transport) error {
	if mt.transport != nil {
		if mt.isLeastSignificantEdge() {
			mt.log.Debug("Underlying transport already exists, closing new transport.")
			if err := newTransport.Close(); err != nil {
				mt.log.WithError(err).Warn("Failed to close new transport.")
			}
			return ErrTransportAlreadyExists
		}

		mt.log.Debug("Underlying transport already exists, closing old transport.")
		if err := mt.transport.Close(); err != nil {
			mt.log.WithError(err).Warn("Failed to close old transport.")
		}
		mt.transport = nil
	}

	// Set new underlying transport.
	mt.transport = newTransport
	select {
	case mt.transportCh <- struct{}{}:
		mt.log.Debug("Sent signal to 'mt.transportCh'.")
	default:
	}
	return nil
}

func (mt *ManagedTransport) deleteFromDiscovery() error {
	retrier := netutil.NewRetrier(mt.log, 1*time.Second, netutil.DefaultMaxBackoff, 5, 2)
	return retrier.Do(context.Background(), func() error {
		err := mt.dc.DeleteTransport(context.Background(), mt.Entry.ID)
		mt.log.WithField("tp-id", mt.Entry.ID).WithError(err).Debug("Error deleting transport")
		if netErr, ok := err.(net.Error); ok && netErr.Temporary() { // nolint
			mt.log.
				WithError(err).
				WithField("timeout", true).
				Warn("Failed to update transport status.")
			return err
		}
		if httpErr, ok := err.(*httputil.HTTPError); ok && httpErr.Status == http.StatusNotFound {
			return nil
		}
		return err
	})
}

/*
	<<< PACKET MANAGEMENT >>>
*/

// WritePacket writes a packet to the remote.
func (mt *ManagedTransport) WritePacket(ctx context.Context, packet routing.Packet) error {
	mt.transportMx.Lock()
	defer mt.transportMx.Unlock()

	if mt.transport == nil {
		return fmt.Errorf("write packet: cannot write to transport, transport is not set up")
	}

	n, err := mt.transport.Write(packet)
	if err != nil {
		mt.close()
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

	var transport network.Transport
	for {
		if transport = mt.getTransport(); transport != nil {
			break
		}
		select {
		case <-mt.done:
			return nil, ErrNotServing
		case <-mt.transportCh:
		}
	}

	log.Trace("Awaiting packet...")

	h := make(routing.Packet, routing.PacketHeaderSize)
	if _, err = io.ReadFull(transport, h); err != nil {
		log.WithError(err).Debugf("Failed to read packet header.")
		return nil, err
	}
	log.WithField("header_len", len(h)).WithField("header_raw", h).Trace("Read packet header.")
	p := make([]byte, h.Size())
	if _, err = io.ReadFull(transport, p); err != nil {
		log.WithError(err).Debugf("Failed to read packet payload.")
		return nil, err
	}
	log.WithField("payload_len", len(p)).Trace("Read packet payload.")

	packet = append(h, p...)
	if n := len(packet); n > routing.PacketHeaderSize {
		mt.logRecv(uint64(n - routing.PacketHeaderSize))
	}

	log.WithField("type", packet.Type().String()).
		WithField("rt_id", packet.RouteID()).
		WithField("size", packet.Size()).
		Trace("Received packet.")
	return packet, nil
}

/*
	<<< TRANSPORT LOGGING >>>
*/

func (mt *ManagedTransport) logSent(b uint64) {
	mt.logMx.Lock()
	defer mt.logMx.Unlock()

	mt.LogEntry.AddSent(b)
	atomic.AddUint32(&mt.logUpdates, 1)
}

func (mt *ManagedTransport) logRecv(b uint64) {
	mt.logMx.Lock()
	defer mt.logMx.Unlock()

	mt.LogEntry.AddRecv(b)
	atomic.AddUint32(&mt.logUpdates, 1)
}

// logMod flushes the number of operations performed in this transport
// and returns true if it was bigger than 0
func (mt *ManagedTransport) logMod() bool {
	if ops := atomic.SwapUint32(&mt.logUpdates, 0); ops > 0 {
		mt.log.WithField("func", "ManagedTransport.logMod").Tracef("entry log: recording %d operations", ops)
		return true
	}
	return false
}

// records this transport's log, in case there is data to be logged
func (mt *ManagedTransport) recordLog() {
	if !mt.logMod() {
		return
	}

	mt.logMx.Lock()
	defer mt.logMx.Unlock()

	if err := mt.ls.Record(mt.Entry.ID, mt.LogEntry); err != nil {
		mt.log.WithError(err).Warn("Failed to record log entry.")
	}
}

// Remote returns the remote public key.
func (mt *ManagedTransport) Remote() cipher.PubKey { return mt.rPK }

// Type returns the transport type.
func (mt *ManagedTransport) Type() network.Type { return mt.client.Type() }
