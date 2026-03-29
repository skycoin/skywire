// Package transport pkg/transport/managed_transport.go
package transport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/app/appevent"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skyenv"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/httputil"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/netutil"
	"github.com/skycoin/skywire/pkg/transport/network"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

const (
	logWriteInterval = time.Second * 3
)

// Records number of managedTransports.
var mTpCount int32

var (
	// ErrNotServing is the error returned when a transport is no longer served.
	ErrNotServing = errors.New("transport is no longer being served")
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
	// QueueDeletion, when set, defers TPD deregistration to the manager's batch
	// deletion loop instead of making an HTTP call per transport close.
	QueueDeletion func(id uuid.UUID)
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

	timeout       time.Duration
	queueDeletion func(id uuid.UUID)

	latencyStats LatencyStats
	latencyMx    sync.RWMutex
}

// LatencyStats holds latency measurement statistics for a transport.
type LatencyStats struct {
	Min float64 `json:"min_ms"` // Minimum observed latency in milliseconds
	Max float64 `json:"max_ms"` // Maximum observed latency in milliseconds
	Avg float64 `json:"avg_ms"` // Average latency in milliseconds
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
		log:           log,
		rPK:           conf.RemotePK,
		dc:            conf.DC,
		ls:            conf.LS,
		client:        conf.client,
		Entry:         entry,
		LogEntry:      logEntry,
		transportCh:   make(chan struct{}, 1),
		done:          make(chan struct{}),
		timeout:       conf.InactiveTimeout,
		queueDeletion: conf.QueueDeletion,
	}
	return mt
}

// GetLatency returns the average inter-visor ping latency in milliseconds.
// For backwards compatibility, returns the average latency.
func (mt *ManagedTransport) GetLatency() float64 {
	mt.latencyMx.RLock()
	defer mt.latencyMx.RUnlock()
	return mt.latencyStats.Avg
}

// GetLatencyStats returns the full latency statistics (min/max/avg).
func (mt *ManagedTransport) GetLatencyStats() LatencyStats {
	mt.latencyMx.RLock()
	defer mt.latencyMx.RUnlock()
	return mt.latencyStats
}

// SetLatency sets the average inter-visor ping latency in milliseconds.
// For backwards compatibility; prefer SetLatencyStats for full statistics.
func (mt *ManagedTransport) SetLatency(latencyMs float64) {
	mt.latencyMx.Lock()
	defer mt.latencyMx.Unlock()
	mt.latencyStats.Avg = latencyMs
	// If min/max not set, initialize them
	if mt.latencyStats.Min == 0 || latencyMs < mt.latencyStats.Min {
		mt.latencyStats.Min = latencyMs
	}
	if latencyMs > mt.latencyStats.Max {
		mt.latencyStats.Max = latencyMs
	}
}

// SetLatencyStats sets the full latency statistics.
func (mt *ManagedTransport) SetLatencyStats(stats LatencyStats) {
	mt.latencyMx.Lock()
	defer mt.latencyMx.Unlock()
	mt.latencyStats = stats
}

// GetBandwidth returns the current cumulative bandwidth for this transport.
func (mt *ManagedTransport) GetBandwidth() *BandwidthData {
	mt.logMx.Lock()
	defer mt.logMx.Unlock()

	return &BandwidthData{
		SentBytes: atomic.LoadUint64(mt.LogEntry.SentBytes),
		RecvBytes: atomic.LoadUint64(mt.LogEntry.RecvBytes),
	}
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
			// Check if this is an expected shutdown error (closed pipe/connection)
			// These occur during normal shutdown and should not be logged as warnings
			errStr := err.Error()
			if strings.Contains(errStr, "closed pipe") ||
				strings.Contains(errStr, "closed network connection") ||
				errors.Is(err, io.EOF) ||
				errors.Is(err, net.ErrClosed) {
				log.WithError(err).Debug("Transport closed, stopping read loop")
			} else {
				log.WithError(err).Warn("Failed to read packet, closing transport")
			}
			mt.close()
			return
		}
		select {
		case <-mt.done:
			return
		case readCh <- p:
		case <-time.After(30 * time.Second):
			log.Warn("Dropping packet: readCh full for 30s (application not reading)")
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

// close underlying transport and queue deregistration from transport discovery.
// If queueDeletion is set (manager-level batch deletion), the transport ID is
// queued for deferred batch deletion instead of making an individual HTTP call.
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
	if mt.queueDeletion != nil {
		mt.queueDeletion(mt.Entry.ID)
	} else {
		_ = mt.deleteFromDiscovery() //nolint:errcheck
	}
}

// closeWithoutDeregister closes the transport without deregistering from TPD.
// Used when batch deregistration is done at the manager level.
func (mt *ManagedTransport) closeWithoutDeregister() {
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

	// Skip settlement handshake for self-connections (noopDiscoveryClient)
	// Self-connections don't register to TPD and would deadlock during settlement
	if _, isNoop := mt.dc.(*noopDiscoveryClient); !isNoop {
		mt.log.Debug("Performing settlement handshake...")
		receivedLabel, err := MakeSettlementHS(false, mt.log, mt.Entry.Label).Do(ctx, mt.dc, transport, mt.client.SK())
		if err != nil {
			return fmt.Errorf("settlement handshake failed: %w", err)
		}
		// Adopt the initiator's label so both ends agree on the transport's origin.
		// This is backward-compatible: older visors always send LabelUser, which is
		// the same default the responder used before this change.
		if receivedLabel != "" {
			mt.Entry.Label = receivedLabel
		}
	} else {
		mt.log.Debug("Skipping settlement handshake for self-connection (noop discovery client)")
	}

	mt.log.Debug("Setting underlying transport...")
	mt.setTransport(transport)
	return nil
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

	// Skip settlement handshake for self-connections (noopDiscoveryClient)
	// Self-connections don't register to TPD and would deadlock during settlement
	if _, isNoop := mt.dc.(*noopDiscoveryClient); !isNoop {
		if _, err := MakeSettlementHS(true, mt.log, mt.Entry.Label).Do(ctx, mt.dc, transport, mt.client.SK()); err != nil {
			return fmt.Errorf("settlement handshake failed: %w", err)
		}
	} else {
		mt.log.Debug("Skipping settlement handshake for self-connection (noop discovery client)")
	}

	mt.setTransport(transport)
	return nil
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

// setTransport sets 'mt.transport' (the underlying transport).
// If 'mt.transport' is already occupied, close the old one and use the new one.
// This handles stale/zombie transports where one side thinks it exists but the other doesn't.
func (mt *ManagedTransport) setTransport(newTransport network.Transport) {
	if mt.transport != nil {
		mt.log.Debug("Underlying transport already exists, closing old transport to accept new one.")
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
}

func (mt *ManagedTransport) deleteFromDiscovery() error {
	retrier := netutil.NewRetrier(mt.log, 1*time.Second, netutil.DefaultMaxBackoff, 5, 2)
	return retrier.Do(context.Background(), func() error {
		err := mt.dc.DeleteTransport(context.Background(), mt.Entry.ID)
		if err != nil {
			mt.log.WithField("tp-id", mt.Entry.ID).WithError(err).Debug("Error deleting transport")
		}
		if _, ok := err.(net.Error); ok {
			mt.log.
				WithError(err).
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
// Respects context cancellation to prevent blocking forever on dead transports.
func (mt *ManagedTransport) WritePacket(ctx context.Context, packet routing.Packet) error {
	mt.transportMx.Lock()

	if mt.transport == nil {
		mt.transportMx.Unlock()
		return fmt.Errorf("write packet: cannot write to transport, transport is not set up")
	}

	// Run the write in a goroutine so we can respect context cancellation.
	// Without this, a dead transport's Write blocks forever, deadlocking
	// the route group close path and the rules GC goroutine.
	type writeResult struct {
		n   int
		err error
	}
	ch := make(chan writeResult, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				ch <- writeResult{0, fmt.Errorf("panic in transport write: %v", r)}
			}
		}()
		n, err := mt.transport.Write(packet)
		ch <- writeResult{n, err}
	}()

	select {
	case <-ctx.Done():
		mt.transportMx.Unlock()
		return ctx.Err()
	case res := <-ch:
		if res.err != nil {
			// Release the lock BEFORE calling close, which also acquires it
			mt.transportMx.Unlock()
			mt.close()
			return res.err
		}
		if res.n > routing.PacketHeaderSize {
			mt.logSent(uint64(res.n - routing.PacketHeaderSize)) //nolint:gosec
		}
		mt.transportMx.Unlock()
		return nil
	}
}

// WARNING: Not thread safe.
func (mt *ManagedTransport) readPacket() (packet routing.Packet, err error) {
	log := mt.log.WithField("func", "readPacket")

	var tp network.Transport
	for {
		if tp = mt.getTransport(); tp != nil {
			break
		}
		select {
		case <-mt.done:
			return nil, ErrNotServing
		case <-mt.transportCh:
		}
	}

	log.Trace("Awaiting packet...")

	// Set a read deadline to prevent blocking forever on a half-open TCP connection.
	// Without this, a dead transport causes the readLoop goroutine to leak permanently.
	// The deadline is refreshed on each read attempt; successful reads reset it.
	const readTimeout = 3 * time.Minute
	if err = tp.SetReadDeadline(time.Now().Add(readTimeout)); err != nil {
		log.WithError(err).Debug("Failed to set read deadline")
	}

	h := make(routing.Packet, routing.PacketHeaderSize)
	if _, err = io.ReadFull(tp, h); err != nil {
		log.WithError(err).Debugf("Failed to read packet header.")
		return nil, err
	}
	log.WithField("header_len", len(h)).WithField("header_raw", h).Trace("Read packet header.")
	p := make([]byte, h.Size())
	if _, err = io.ReadFull(tp, p); err != nil {
		log.WithError(err).Debugf("Failed to read packet payload.")
		return nil, err
	}
	log.WithField("payload_len", len(p)).Trace("Read packet payload.")

	packet = append(h, p...)
	if n := len(packet); n > routing.PacketHeaderSize {
		mt.logRecv(uint64(n - routing.PacketHeaderSize)) //nolint:gosec
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
func (mt *ManagedTransport) Type() types.Type { return mt.client.Type() }
