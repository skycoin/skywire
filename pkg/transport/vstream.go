// Package transport pkg/transport/vstream.go c2-net-transport
//
// VStream provides virtual bidirectional streams over route ID 0 transport
// packets. Used by the cascade RPC relay and DHT transport layer to multiplex
// multiple logical connections over a single managed transport.
//
// Wire format per packet: [streamID:8][senderPK:33][flags:1][data...]
// Flags: 0x00=data, 0x01=SYN (new stream), 0x02=FIN (close)
//
// A visor also acts as a dmsg-server-style RELAY over its skynet transports:
// a SYN carrying the Relay flag names a third-party destination PK and is
// signed by the originator. If the destination is not this visor and a direct
// transport to it exists, the SYN is forwarded (byte-spliced) to that peer —
// PK-addressed forwarding with no route, mirroring the dmsg server's
// forwardViaPeer/bridgeStream. Relaying is always on and bounded (1-hop guard
// via the Relayed flag + a concurrent-relay cap); it is not configurable off.
// See pkg/transport/vstream_relay.go and docs/design/dmsg-bootstrap-floor.md.
//
// Relay SYN wire format:
//
//	[streamID:8][senderPK:33][flags:1][dstPK:33][originID:8][sig:65]
//
// where sig = sign_senderSK(originID || senderPK || dstPK). originID is the
// originator's own stream id, preserved end-to-end (the relay remaps streamID
// but not originID) so the signature verifies at the destination.
package transport

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
)

// VStream header constants.
const (
	VStreamHeaderSize = 8 + 33 + 1 // streamID + senderPK + flags
	// vstreamRelaySynHeaderSize is the header of a relay SYN, which appends
	// [dstPK:33][originID:8][sig:65] after the base header.
	vstreamRelaySynHeaderSize = VStreamHeaderSize + 33 + 8 + 65

	VStreamFlagData = 0x00
	VStreamFlagSyn  = 0x01
	VStreamFlagFin  = 0x02
	// VStreamFlagRelay marks a SYN destined for a third-party PK carried in
	// the extended relay header; the relay forwards it PK-addressed.
	VStreamFlagRelay = 0x04
	// VStreamFlagRelayed is set by a relay on the leg it emits, so the next
	// hop refuses to forward again (1-hop guard, mirrors dmsg's ss.isPeer).
	VStreamFlagRelayed = 0x08

	// DefaultMaxRelayedVStreams bounds concurrent relayed streams a visor
	// carries on behalf of others, so an always-on relay can't be amplified.
	DefaultMaxRelayedVStreams = 4096
)

// VStreamMux multiplexes virtual streams over route ID 0 packets.
// Multiple instances can coexist (one per packet type, e.g., DHTPacket
// for DHT, CascadeSetupPacket for RSN relay).
type VStreamMux struct {
	log        *logging.Logger
	tm         *Manager
	packetType routing.PacketType // which route-ID-0 packet type to use

	// streams is keyed by (transport, wire stream id): ids are only unique
	// per peer pair, and both ends allocate them. Local ids take the parity
	// nextIDFor derives from the two PKs, so a stream this side opened can
	// never collide with one the peer opened on the same transport.
	streams   map[streamKey]*VStream
	streamsMu sync.Mutex
	streamID  uint64

	// Counters for `visor state` (see Stats). Atomics.
	framesUnknownStream int64 // DATA/FIN for a stream id we do not have
	stalledStreams      int64 // streams closed because the reader never drained
	acceptDropped       int64 // SYNs closed because Accept was not called in time

	// relays holds active relay legs keyed by (inbound transport, wire
	// streamID). Each direction of a bridged stream is registered pointing
	// at its peer leg, so a DATA/FIN frame arriving on either transport is
	// spliced to the other. relayCount is the live count (atomic), bounded
	// by maxRelays.
	relays     map[relayKey]relayKey
	relaysMu   sync.Mutex
	relayCount int64
	maxRelays  int

	incoming chan *VStream
	done     chan struct{}
	once     sync.Once

	// Routing-policy hook for direct dials. Set via
	// SetDirectDialHook; nil = no policy check. Read on every
	// Dial; the RWMutex covers the swap (hook itself must be
	// thread-safe).
	directDialHookMu sync.RWMutex
	directDialHook   DirectDialHookFn
}

// NewVStreamMux creates a virtual stream multiplexer for the given packet type.
func NewVStreamMux(tm *Manager, packetType routing.PacketType, log *logging.Logger) *VStreamMux {
	return &VStreamMux{
		log:        log,
		tm:         tm,
		packetType: packetType,
		streams:    make(map[streamKey]*VStream),
		relays:     make(map[relayKey]relayKey),
		maxRelays:  DefaultMaxRelayedVStreams,
		incoming:   make(chan *VStream, 32),
		done:       make(chan struct{}),
	}
}

// streamKey identifies a stream on one transport. Wire ids are per peer pair.
type streamKey struct {
	tp uuid.UUID
	id uint64
}

const (
	// vstreamReadBuf is the per-stream inbound frame queue.
	vstreamReadBuf = 1024
	// vstreamStallTimeout is how long HandlePacket (the transport read loop)
	// waits for a full queue to drain before it gives the stream up.
	vstreamStallTimeout = 30 * time.Second
	// vstreamAcceptTimeout bounds the wait for Accept on an inbound SYN.
	vstreamAcceptTimeout = 5 * time.Second
)

// nextIDFor allocates a wire stream id for a stream this side opens to
// remotePK. Both ends of a transport allocate ids, and the map is keyed per
// transport, so the two allocators must not overlap: the side with the larger
// PK uses odd ids, the other even. Before this both used a plain counter and,
// with both sides freshly started, a tab's relay session and its host's RPC
// dial to the tab got the same id — frames crossed streams and yamux died
// with "invalid protocol version".
func (m *VStreamMux) nextIDFor(remotePK cipher.PubKey) uint64 {
	id := atomic.AddUint64(&m.streamID, 1) << 1
	local := m.localPK()
	if bytes.Compare(local[:], remotePK[:]) > 0 {
		id |= 1
	}
	return id
}

// deliver queues one inbound DATA frame for the stream's reader, blocking the
// transport's read loop (back-pressure) while the queue is full. A VStream
// carries yamux/Noise sessions that assume a reliable ordered byte stream, so
// a frame must never be dropped: that corrupts the session instead of closing
// it. A reader that drains nothing for vstreamStallTimeout is dead — close the
// stream so the peer sees EOF and the read loop is freed.
func (m *VStreamMux) deliver(stream *VStream, buf []byte) {
	select {
	case stream.readBuf <- buf:
		return
	case <-stream.closed:
		return
	default:
	}
	t := time.NewTimer(vstreamStallTimeout)
	defer t.Stop()
	select {
	case stream.readBuf <- buf:
	case <-stream.closed:
	case <-t.C:
		atomic.AddInt64(&m.stalledStreams, 1)
		m.log.WithField("stream", stream.id).WithField("remote", stream.remotePK.String()).
			Warn("vstream: reader stalled; closing stream")
		stream.Close() //nolint:errcheck,gosec
	}
}

// offerIncoming hands an inbound stream to Accept, waiting up to
// vstreamAcceptTimeout for the accept loop; a mux nobody accepts from closes
// the stream (counted) instead of leaking it.
func (m *VStreamMux) offerIncoming(stream *VStream, what string) {
	t := time.NewTimer(vstreamAcceptTimeout)
	defer t.Stop()
	select {
	case m.incoming <- stream:
	case <-m.done:
		stream.Close() //nolint:errcheck,gosec
	case <-t.C:
		atomic.AddInt64(&m.acceptDropped, 1)
		m.log.Warn("vstream: " + what + " not accepted in time; closing")
		stream.Close() //nolint:errcheck,gosec
	}
}

func (m *VStreamMux) localPK() cipher.PubKey {
	return m.tm.Local()
}

// DirectDialHookFn is the policy hook fired before a VStreamMux
// direct dial. Returns a non-nil error to refuse the dial (the
// error surfaces as the Dial return). Pass through a non-error
// nil return to allow. nil hook = no policy check.
//
// appName is the originating app's name (skysocks-client,
// vpn-client, etc.) when the caller supplied one — empty when
// not threaded (older code paths). The routing-policy bridge
// uses it to look up per-app policy rules so a script can
// branch on ctx.app for direct dials just as it does for
// overlay dials.
//
// Lives as a func type rather than an interface so the transport
// package doesn't take a dependency on the router/policy package
// — the visor wires this with a closure that bridges into the
// existing routing-policy stack.
type DirectDialHookFn func(remotePK cipher.PubKey, transportKind, appName string) error

// SetDirectDialHook attaches a hook fired before every Dial.
// Returning an error from the hook causes Dial to fail with that
// error — useful for vpn-killswitch-style policies that want to
// refuse direct dials over the wrong transport kind. Goroutine-
// safe; the hook itself must be thread-safe.
func (m *VStreamMux) SetDirectDialHook(hook DirectDialHookFn) {
	m.directDialHookMu.Lock()
	m.directDialHook = hook
	m.directDialHookMu.Unlock()
}

func (m *VStreamMux) loadDirectDialHook() DirectDialHookFn {
	m.directDialHookMu.RLock()
	defer m.directDialHookMu.RUnlock()
	return m.directDialHook
}

// Dial opens a virtual stream to a remote PK over an existing
// transport. appName is the originating app's name; pass "" when
// not known. The hook receives appName so per-app policies can
// branch correctly on the direct-dial path.
func (m *VStreamMux) Dial(remotePK cipher.PubKey, appName string) (*VStream, error) {
	// Find a non-DMSG transport to this peer. DMSG transports use their
	// own stream multiplexing and don't support route ID 0 packets.
	var targetTp *ManagedTransport
	m.tm.WalkTransports(func(tp *ManagedTransport) bool {
		if tp.Remote() == remotePK && !tp.IsClosed() && tp.Type() != "dmsg" {
			targetTp = tp
			return false
		}
		return true
	})
	if targetTp == nil {
		return nil, fmt.Errorf("vstream: no non-DMSG transport to %s", remotePK.String())
	}

	// Routing-policy hook: give the operator's script a chance to
	// refuse the direct dial (e.g. vpn-killswitch refusing
	// sudph-only on a busy uplink). Hook errors are propagated as
	// the Dial error.
	if hook := m.loadDirectDialHook(); hook != nil {
		if err := hook(remotePK, string(targetTp.Type()), appName); err != nil {
			m.log.WithField("remote", remotePK.String()).
				WithField("type", targetTp.Type()).
				WithError(err).
				Debug("VStreamMux: dial refused by routing policy")
			return nil, err
		}
	}

	m.log.WithField("tp", targetTp.Entry.ID.String()).
		WithField("type", targetTp.Type()).
		WithField("remote", remotePK.String()).
		Debug("VStreamMux: dialing on transport")

	return m.DialOnTransport(targetTp)
}

// DialByTransportID opens a virtual stream to remotePK pinned to a
// specific transport (looked up by ID). Use this when the caller
// has a specific transport in mind — e.g. ping-tree measuring the
// latency of *that exact* transport rather than "any direct
// transport to the peer." Same DMSG-exclusion + closed-transport
// rules as Dial; returns an error when no matching transport
// exists or the matching one isn't currently usable.
func (m *VStreamMux) DialByTransportID(remotePK cipher.PubKey, tpID uuid.UUID) (*VStream, error) {
	var targetTp *ManagedTransport
	m.tm.WalkTransports(func(tp *ManagedTransport) bool {
		if tp.Entry.ID == tpID && tp.Remote() == remotePK && !tp.IsClosed() && tp.Type() != "dmsg" {
			targetTp = tp
			return false
		}
		return true
	})
	if targetTp == nil {
		return nil, fmt.Errorf("vstream: transport %s to %s not found or unusable", tpID, remotePK.String())
	}

	m.log.WithField("tp", targetTp.Entry.ID.String()).
		WithField("type", targetTp.Type()).
		WithField("remote", remotePK.String()).
		Debug("VStreamMux: dialing on pinned transport (by ID)")

	return m.DialOnTransport(targetTp)
}

// DialOnTransport opens a virtual stream on a specific transport.
func (m *VStreamMux) DialOnTransport(tp *ManagedTransport) (*VStream, error) {
	id := m.nextIDFor(tp.Remote())
	stream := &VStream{
		id:       id,
		remotePK: tp.Remote(),
		tpID:     tp.Entry.ID,
		readBuf:  make(chan []byte, vstreamReadBuf),
		closed:   make(chan struct{}),
		mux:      m,
	}

	m.streamsMu.Lock()
	m.streams[streamKey{tp.Entry.ID, id}] = stream
	m.streamsMu.Unlock()

	// Send SYN.
	if err := stream.sendFlag(VStreamFlagSyn, nil); err != nil {
		stream.Close() //nolint:errcheck,gosec
		return nil, fmt.Errorf("vstream: send SYN: %w", err)
	}

	return stream, nil
}

// HandlePacket processes an incoming route ID 0 packet for this mux.
// Register this as the handler for the appropriate packet type.
func (m *VStreamMux) HandlePacket(p routing.Packet, mt *ManagedTransport) {
	payload := p.Payload()
	if len(payload) < VStreamHeaderSize {
		return
	}
	streamID := binary.BigEndian.Uint64(payload[:8])
	var remotePK cipher.PubKey
	copy(remotePK[:], payload[8:41])
	flags := payload[41]
	data := payload[VStreamHeaderSize:]

	// Established relay leg? Splice DATA/FIN straight through to the paired
	// leg on the other transport. Checked before local handling; a relayed
	// stream is never a local endpoint on this node.
	inKey := relayKey{tp: mt.Entry.ID, streamID: streamID}
	if peer, ok := m.lookupRelayLeg(inKey); ok {
		if flags&VStreamFlagFin != 0 {
			m.forwardRelayFrame(peer, VStreamFlagFin, nil)
			m.teardownRelayLeg(inKey, peer)
			return
		}
		m.forwardRelayFrame(peer, VStreamFlagData, data)
		return
	}

	// Relay SYN: a new stream addressed to a third-party PK. Verify, then
	// either terminate locally (we are the destination) or forward.
	if flags&VStreamFlagSyn != 0 && flags&VStreamFlagRelay != 0 {
		m.handleRelaySyn(mt, streamID, remotePK, flags, payload)
		return
	}

	switch {
	case flags&VStreamFlagSyn != 0:
		stream := &VStream{
			id:       streamID,
			remotePK: remotePK,
			tpID:     mt.Entry.ID,
			readBuf:  make(chan []byte, vstreamReadBuf),
			closed:   make(chan struct{}),
			mux:      m,
		}
		m.streamsMu.Lock()
		m.streams[streamKey{mt.Entry.ID, streamID}] = stream
		m.streamsMu.Unlock()
		m.offerIncoming(stream, "incoming stream")

	case flags&VStreamFlagFin != 0:
		key := streamKey{mt.Entry.ID, streamID}
		m.streamsMu.Lock()
		stream, ok := m.streams[key]
		if ok {
			delete(m.streams, key)
		}
		m.streamsMu.Unlock()
		if ok {
			stream.Close() //nolint:errcheck,gosec
		} else {
			atomic.AddInt64(&m.framesUnknownStream, 1)
		}

	default: // DATA
		m.streamsMu.Lock()
		stream, ok := m.streams[streamKey{mt.Entry.ID, streamID}]
		m.streamsMu.Unlock()
		if !ok {
			atomic.AddInt64(&m.framesUnknownStream, 1)
			return
		}
		buf := make([]byte, len(data))
		copy(buf, data)
		m.deliver(stream, buf)
	}
}

// Accept returns the next incoming virtual stream.
func (m *VStreamMux) Accept() (*VStream, error) {
	select {
	case s := <-m.incoming:
		return s, nil
	case <-m.done:
		return nil, net.ErrClosed
	}
}

// Close shuts down the mux and all streams.
func (m *VStreamMux) Close() error {
	m.once.Do(func() {
		close(m.done)
		// Snapshot the streams under the lock, then close them WITHOUT
		// holding it: VStream.Close re-acquires streamsMu (to delete
		// itself from the map), so closing while holding the lock here
		// self-deadlocks on the non-reentrant mutex.
		m.streamsMu.Lock()
		streams := make([]*VStream, 0, len(m.streams))
		for _, s := range m.streams {
			streams = append(streams, s)
		}
		m.streamsMu.Unlock()
		for _, s := range streams {
			s.Close() //nolint:errcheck,gosec
		}
	})
	return nil
}

// VStream is a virtual bidirectional connection over route ID 0 packets.
type VStream struct {
	id       uint64
	remotePK cipher.PubKey
	tpID     uuid.UUID
	readBuf  chan []byte
	readLeft []byte // leftover from partial read
	closed   chan struct{}
	once     sync.Once
	mux      *VStreamMux
}

// Read implements io.Reader. Handles partial reads correctly.
func (s *VStream) Read(p []byte) (int, error) {
	// Return leftover data from previous read first.
	if len(s.readLeft) > 0 {
		n := copy(p, s.readLeft)
		s.readLeft = s.readLeft[n:]
		return n, nil
	}

	select {
	case data, ok := <-s.readBuf:
		if !ok {
			return 0, io.EOF
		}
		n := copy(p, data)
		if n < len(data) {
			s.readLeft = data[n:]
		}
		return n, nil
	case <-s.closed:
		return 0, io.EOF
	}
}

// vstreamMaxData is the most payload one DATA frame carries: the transport
// packet size field is 16 bits and the frame header takes VStreamHeaderSize
// of it.
const vstreamMaxData = math.MaxUint16 - VStreamHeaderSize

// Write implements io.Writer, segmenting p into frames of at most
// vstreamMaxData bytes. Before this the whole of p went into ONE packet whose
// 16-bit size field silently truncated (uint16(len)), so a yamux frame or RPC
// reply over 64 KiB was written in full but declared short — the receiver
// parsed the excess as packet headers ("unknown packet type: Unknown(112)"
// with ASCII payload bytes as the type) and the session on top died with
// "invalid protocol version".
func (s *VStream) Write(p []byte) (int, error) {
	n := 0
	for len(p) > 0 {
		chunk := p
		if len(chunk) > vstreamMaxData {
			chunk = p[:vstreamMaxData]
		}
		if err := s.sendFlag(VStreamFlagData, chunk); err != nil {
			return n, err
		}
		n += len(chunk)
		p = p[len(chunk):]
	}
	return n, nil
}

// Close closes the virtual stream.
func (s *VStream) Close() error {
	s.once.Do(func() {
		close(s.closed)
		s.mux.streamsMu.Lock()
		delete(s.mux.streams, streamKey{s.tpID, s.id})
		s.mux.streamsMu.Unlock()
		// Best-effort FIN — don't block if transport is dead.
		s.sendFlag(VStreamFlagFin, nil) //nolint:errcheck,gosec
	})
	return nil
}

// RemotePK returns the remote peer's public key.
func (s *VStream) RemotePK() cipher.PubKey {
	return s.remotePK
}

func (s *VStream) sendFlag(flag byte, data []byte) error {
	select {
	case <-s.closed:
		if flag != VStreamFlagFin {
			return io.ErrClosedPipe
		}
	default:
	}

	tp, err := s.mux.tm.GetTransportByID(s.tpID)
	if err != nil {
		return fmt.Errorf("vstream write: transport %s: %w", s.tpID, err)
	}

	if len(data) > vstreamMaxData {
		return fmt.Errorf("vstream write: frame of %d bytes exceeds %d", len(data), vstreamMaxData)
	}
	localPK := s.mux.localPK()
	payload := make([]byte, VStreamHeaderSize+len(data))
	binary.BigEndian.PutUint64(payload[:8], s.id)
	copy(payload[8:41], localPK[:])
	payload[41] = flag
	copy(payload[VStreamHeaderSize:], data)

	pkt := make(routing.Packet, routing.PacketHeaderSize+len(payload))
	pkt[routing.PacketTypeOffset] = byte(s.mux.packetType)
	binary.BigEndian.PutUint16(pkt[routing.PacketPayloadSizeOffset:], uint16(len(payload))) //nolint:gosec
	copy(pkt[routing.PacketPayloadOffset:], payload)

	return tp.WriteRawPacket(pkt)
}

// VStreamMuxStats is the mux's `visor state` view: what the logs used to be the
// only window on. Counters are cumulative since start.
type VStreamMuxStats struct {
	PacketType          string `json:"packet_type"`
	Streams             int    `json:"streams"`
	RelayLegs           int64  `json:"relay_legs"`
	FramesUnknownStream int64  `json:"frames_unknown_stream"`
	StalledStreams      int64  `json:"stalled_streams"`
	AcceptDropped       int64  `json:"accept_dropped"`
}

// Stats snapshots the mux counters. FramesUnknownStream climbing on a healthy
// link means frames are arriving for streams this side does not have: the
// peer's transport lost a handler (see Manager.applyHandlers) or ids collided.
func (m *VStreamMux) Stats() VStreamMuxStats {
	m.streamsMu.Lock()
	n := len(m.streams)
	m.streamsMu.Unlock()
	return VStreamMuxStats{
		PacketType:          m.packetType.String(),
		Streams:             n,
		RelayLegs:           atomic.LoadInt64(&m.relayCount),
		FramesUnknownStream: atomic.LoadInt64(&m.framesUnknownStream),
		StalledStreams:      atomic.LoadInt64(&m.stalledStreams),
		AcceptDropped:       atomic.LoadInt64(&m.acceptDropped),
	}
}
