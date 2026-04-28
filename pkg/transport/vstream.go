// Package transport pkg/transport/vstream.go
//
// VStream provides virtual bidirectional streams over route ID 0 transport
// packets. Used by the cascade RPC relay and DHT transport layer to multiplex
// multiple logical connections over a single managed transport.
//
// Wire format per packet: [streamID:8][senderPK:33][flags:1][data...]
// Flags: 0x00=data, 0x01=SYN (new stream), 0x02=FIN (close)
package transport

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
)

// VStream header constants.
const (
	VStreamHeaderSize = 8 + 33 + 1 // streamID + senderPK + flags
	VStreamFlagData   = 0x00
	VStreamFlagSyn    = 0x01
	VStreamFlagFin    = 0x02
)

// VStreamMux multiplexes virtual streams over route ID 0 packets.
// Multiple instances can coexist (one per packet type, e.g., DHTPacket
// for DHT, CascadeSetupPacket for RSN relay).
type VStreamMux struct {
	log        *logging.Logger
	tm         *Manager
	packetType routing.PacketType // which route-ID-0 packet type to use

	streams   map[uint64]*VStream
	streamsMu sync.Mutex
	streamID  uint64

	incoming chan *VStream
	done     chan struct{}
	once     sync.Once
}

// NewVStreamMux creates a virtual stream multiplexer for the given packet type.
func NewVStreamMux(tm *Manager, packetType routing.PacketType, log *logging.Logger) *VStreamMux {
	return &VStreamMux{
		log:        log,
		tm:         tm,
		packetType: packetType,
		streams:    make(map[uint64]*VStream),
		incoming:   make(chan *VStream, 32),
		done:       make(chan struct{}),
	}
}

func (m *VStreamMux) nextID() uint64 {
	return atomic.AddUint64(&m.streamID, 1)
}

func (m *VStreamMux) localPK() cipher.PubKey {
	return m.tm.Local()
}

// Dial opens a virtual stream to a remote PK over an existing transport.
func (m *VStreamMux) Dial(remotePK cipher.PubKey) (*VStream, error) {
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

	m.log.WithField("tp", targetTp.Entry.ID.String()).
		WithField("type", targetTp.Type()).
		WithField("remote", remotePK.String()).
		Debug("VStreamMux: dialing on transport")

	return m.DialOnTransport(targetTp)
}

// DialOnTransport opens a virtual stream on a specific transport.
func (m *VStreamMux) DialOnTransport(tp *ManagedTransport) (*VStream, error) {
	id := m.nextID()
	stream := &VStream{
		id:       id,
		remotePK: tp.Remote(),
		tpID:     tp.Entry.ID,
		readBuf:  make(chan []byte, 64),
		closed:   make(chan struct{}),
		mux:      m,
	}

	m.streamsMu.Lock()
	m.streams[id] = stream
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

	switch flags {
	case VStreamFlagSyn:
		stream := &VStream{
			id:       streamID,
			remotePK: remotePK,
			tpID:     mt.Entry.ID,
			readBuf:  make(chan []byte, 64),
			closed:   make(chan struct{}),
			mux:      m,
		}
		m.streamsMu.Lock()
		m.streams[streamID] = stream
		m.streamsMu.Unlock()

		select {
		case m.incoming <- stream:
		default:
			m.log.Warn("vstream: incoming stream dropped (buffer full)")
			stream.Close() //nolint:errcheck,gosec
		}

	case VStreamFlagData:
		m.streamsMu.Lock()
		stream, ok := m.streams[streamID]
		m.streamsMu.Unlock()
		if !ok {
			return
		}
		buf := make([]byte, len(data))
		copy(buf, data)
		select {
		case stream.readBuf <- buf:
		case <-stream.closed:
		default:
			m.log.Warn("vstream: read buffer full, dropping data")
		}

	case VStreamFlagFin:
		m.streamsMu.Lock()
		stream, ok := m.streams[streamID]
		if ok {
			delete(m.streams, streamID)
		}
		m.streamsMu.Unlock()
		if ok {
			stream.Close() //nolint:errcheck,gosec
		}
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
		m.streamsMu.Lock()
		for _, s := range m.streams {
			s.Close() //nolint:errcheck,gosec
		}
		m.streamsMu.Unlock()
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

// Write implements io.Writer.
func (s *VStream) Write(p []byte) (int, error) {
	return len(p), s.sendFlag(VStreamFlagData, p)
}

// Close closes the virtual stream.
func (s *VStream) Close() error {
	s.once.Do(func() {
		close(s.closed)
		s.mux.streamsMu.Lock()
		delete(s.mux.streams, s.id)
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
